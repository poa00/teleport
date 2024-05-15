// Teleport
// Copyright (C) 2024 Gravitational, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package vnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"sync"

	"github.com/gravitational/trace"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/api/types"
	prehogv1alpha "github.com/gravitational/teleport/gen/proto/go/prehog/v1alpha"
	teletermv1 "github.com/gravitational/teleport/gen/proto/go/teleport/lib/teleterm/v1"
	api "github.com/gravitational/teleport/gen/proto/go/teleport/lib/teleterm/vnet/v1"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/teleterm/api/uri"
	"github.com/gravitational/teleport/lib/teleterm/clusteridcache"
	"github.com/gravitational/teleport/lib/teleterm/daemon"
	logutils "github.com/gravitational/teleport/lib/utils/log"
	"github.com/gravitational/teleport/lib/vnet"
)

var log = logutils.NewPackageLogger(teleport.ComponentKey, "term:vnet")

type status int

const (
	statusNotRunning status = iota
	statusRunning
	statusClosed
)

// Service implements gRPC service for VNet.
type Service struct {
	api.UnimplementedVnetServiceServer

	cfg    Config
	mu     sync.Mutex
	status status
	// stopErrC is used to pass an error from goroutine that runs VNet in the background to the
	// goroutine which handles RPC for stopping VNet. stopErrC gets closed after VNet stops. Starting
	// VNet creates a new channel and assigns it as stopErrC.
	//
	// It's a buffered channel in case VNet crashes and there's no Stop RPC reading from stopErrC at
	// that moment.
	stopErrC chan error
	// cancel stops the VNet instance running in a separate goroutine.
	cancel context.CancelCauseFunc
}

// New creates an instance of Service.
func New(cfg Config) (*Service, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	return &Service{
		cfg: cfg,
	}, nil
}

type Config struct {
	// DaemonService is used to get cached clients and for usage reporting. If DaemonService was not
	// one giant blob of methods, Config could accept two separate services instead.
	DaemonService *daemon.Service
	// ClientStore is needed to extract api/profile.Profile from Connect's tsh dir. Technically it
	// could create Teleport clients as well, but we use daemon.Service for that instead since it
	// includes a bunch of teleterm-specific necessities.
	ClientStore *client.Store
	// ClusterIDCache is used for usage reporting to read cluster ID that needs to be included with
	// every event.
	ClusterIDCache *clusteridcache.Cache
	// InstallationID is a unique ID of this particular Connect installation, used for usage
	// reporting.
	InstallationID string
}

// CheckAndSetDefaults checks and sets the defaults
func (c *Config) CheckAndSetDefaults() error {
	if c.DaemonService == nil {
		return trace.BadParameter("missing DaemonService")
	}

	if c.ClientStore == nil {
		return trace.BadParameter("missing ClientStore")
	}

	if c.ClusterIDCache == nil {
		return trace.BadParameter("missing ClusterIDCache")
	}

	if c.InstallationID == "" {
		return trace.BadParameter("missing InstallationID")
	}

	return nil
}

func (s *Service) Start(ctx context.Context, req *api.StartRequest) (*api.StartResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status == statusClosed {
		return nil, trace.CompareFailed("VNet service has been closed")
	}

	if s.status == statusRunning {
		return &api.StartResponse{}, nil
	}

	usageReporter, err := NewUsageReporter(UsageReporterConfig{
		ClientStore:    s.cfg.ClientStore,
		ClientCache:    s.cfg.DaemonService,
		EventConsumer:  s.cfg.DaemonService,
		ClusterIDCache: s.cfg.ClusterIDCache,
		InstallationID: s.cfg.InstallationID,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	appProvider := &appProvider{
		daemonService: s.cfg.DaemonService,
		clientStore:   s.cfg.ClientStore,
		usageReporter: usageReporter,
	}

	longCtx, cancelLongCtx := context.WithCancelCause(context.Background())
	s.cancel = cancelLongCtx
	defer func() {
		// If by the end of this RPC the service is not running, make sure to cancel the long context.
		if s.status != statusRunning {
			cancelLongCtx(nil)
		}
	}()

	manager, adminCommandErrCh, err := vnet.Setup(ctx, longCtx, appProvider)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	s.stopErrC = make(chan error, 1)

	go func() {
		err := vnet.Run(longCtx, cancelLongCtx, manager, adminCommandErrCh)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.ErrorContext(longCtx, "VNet closed with an error", "error", err)
			s.stopErrC <- err
		}
		close(s.stopErrC)

		// TODO(ravicious): Notify the Electron app about change of VNet state, but only if it's
		// running. If it's not running, then the Start RPC has already failed and forwarded the error
		// to the user.

		s.mu.Lock()
		defer s.mu.Unlock()

		s.status = statusNotRunning
		cancelLongCtx(nil)
	}()

	s.status = statusRunning
	return &api.StartResponse{}, nil
}

// Stop stops VNet and cleans up used resources. Blocks until VNet stops or ctx is canceled.
func (s *Service) Stop(ctx context.Context, req *api.StopRequest) (*api.StopResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	errC := make(chan error)

	go func() {
		errC <- trace.Wrap(s.stopLocked())
	}()

	select {
	case <-ctx.Done():
		return nil, trace.Wrap(ctx.Err())
	case err := <-errC:
		if err != nil {
			return nil, trace.Wrap(err)
		}

		return &api.StopResponse{}, nil
	}

}

func (s *Service) stopLocked() error {
	if s.status == statusClosed {
		return trace.CompareFailed("VNet service has been closed")
	}

	if s.status == statusNotRunning {
		return nil
	}

	s.cancel(nil)
	s.status = statusNotRunning

	return trace.Wrap(<-s.stopErrC)
}

// Close stops VNet service and prevents it from being started again. Blocks until VNet stops.
// Intended for cleanup code when tsh daemon gets terminated.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.stopLocked()
	s.status = statusClosed

	return trace.Wrap(err)
}

type appProvider struct {
	daemonService *daemon.Service
	clientStore   *client.Store
	usageReporter *usageReporter
}

func (p *appProvider) ListProfiles() ([]string, error) {
	profiles, err := p.clientStore.ListProfiles()
	return profiles, trace.Wrap(err)
}

func (p *appProvider) GetCachedClient(ctx context.Context, profileName, leafClusterName string) (*client.ClusterClient, error) {
	uri := uri.NewClusterURI(profileName).AppendLeafCluster(leafClusterName)
	client, err := p.daemonService.GetCachedClient(ctx, uri)
	return client, trace.Wrap(err)
}

func (p *appProvider) ReissueAppCert(ctx context.Context, profileName, leafClusterName string, app types.Application) (tls.Certificate, error) {
	clusterURI := uri.NewClusterURI(profileName).AppendLeafCluster(leafClusterName)
	cluster, _, err := p.daemonService.ResolveClusterURI(clusterURI)
	if err != nil {
		return tls.Certificate{}, trace.Wrap(err)
	}

	client, err := p.daemonService.GetCachedClient(ctx, clusterURI)
	if err != nil {
		return tls.Certificate{}, trace.Wrap(err)
	}

	// TODO(ravicious): Copy stuff from DaemonService.reissueGatewayCerts in order to handle expired certs.
	cert, err := cluster.ReissueAppCert(ctx, client, app)
	return cert, trace.Wrap(err)
}

// GetDialOptions returns ALPN dial options for the profile.
func (p *appProvider) GetDialOptions(ctx context.Context, profileName string) (*vnet.DialOptions, error) {
	profile, err := p.clientStore.GetProfile(profileName)
	if err != nil {
		return nil, trace.Wrap(err, "loading user profile")
	}
	dialOpts := &vnet.DialOptions{
		WebProxyAddr:            profile.WebProxyAddr,
		ALPNConnUpgradeRequired: profile.TLSRoutingConnUpgradeRequired,
	}
	if dialOpts.ALPNConnUpgradeRequired {
		dialOpts.RootClusterCACertPool, err = p.getRootClusterCACertPool(ctx, profileName)
		if err != nil {
			return nil, trace.Wrap(err)
		}
	}
	return dialOpts, nil
}

// OnNewConnection submits a usage event once per appProvider lifetime.
// That is, if a user makes multiple connections to a single app, OnNewConnection submits a single
// event. This is to mimic how Connect submits events for its app gateways. This lets us compare
// popularity of VNet and app gateways.
func (p *appProvider) OnNewConnection(ctx context.Context, profileName, leafClusterName string, app types.Application) error {
	// Enqueue the event from a separate goroutine since we don't care about errors anyway and we also
	// don't want to slow down VNet connections.
	go func() {
		uri := uri.NewClusterURI(profileName).AppendLeafCluster(leafClusterName).AppendApp(app.GetName())

		err := p.usageReporter.ReportApp(ctx, uri)
		if err != nil {
			log.ErrorContext(ctx, "Failed to submit usage event", "app", uri, "error", err)
		}
	}()

	return nil
}

// getRootClusterCACertPool returns a certificate pool for the root cluster of the given profile.
func (p *appProvider) getRootClusterCACertPool(ctx context.Context, profileName string) (*x509.CertPool, error) {
	tc, err := p.newTeleportClient(ctx, profileName, "")
	if err != nil {
		return nil, trace.Wrap(err, "creating new client")
	}
	certPool, err := tc.RootClusterCACertPool(ctx)
	if err != nil {
		return nil, trace.Wrap(err, "loading root cluster CA cert pool")
	}
	return certPool, nil
}

func (p *appProvider) newTeleportClient(ctx context.Context, profileName, leafClusterName string) (*client.TeleportClient, error) {
	cfg := &client.Config{
		ClientStore: p.clientStore,
	}
	if err := cfg.LoadProfile(p.clientStore, profileName); err != nil {
		return nil, trace.Wrap(err, "loading client profile")
	}
	if leafClusterName != "" {
		cfg.SiteName = leafClusterName
	}
	tc, err := client.NewClient(cfg)
	if err != nil {
		return nil, trace.Wrap(err, "creating new client")
	}
	return tc, nil
}

type usageReporter struct {
	cfg UsageReporterConfig
	// reportedApps contains a set of URIs for apps which usage has been already reported.
	// App gateways (local proxies) in Connect report a single event per gateway created per app. VNet
	// needs to replicate this behavior, hence why it keeps track of reported apps to report only one
	// event per app per VNet's lifespan.
	reportedApps map[string]struct{}
	// mu protects access to reportedApps.
	mu sync.Mutex
}

type clientCache interface {
	GetCachedClient(context.Context, uri.ResourceURI) (*client.ClusterClient, error)
}

type eventConsumer interface {
	ReportUsageEvent(*teletermv1.ReportUsageEventRequest) error
}

type UsageReporterConfig struct {
	ClientStore   *client.Store
	ClientCache   clientCache
	EventConsumer eventConsumer
	// clusterIDCache stores cluster ID that needs to be included with each usage event. It's updated
	// outside of usageReporter – the middleware merely reads data from it. If the cache does not
	// contain the given cluster ID, usageReporter drops the event.
	ClusterIDCache *clusteridcache.Cache
	InstallationID string
}

func (c *UsageReporterConfig) CheckAndSetDefaults() error {
	if c.ClientStore == nil {
		return trace.BadParameter("missing ClientStore")
	}

	if c.ClientCache == nil {
		return trace.BadParameter("missing ClientCache")
	}

	if c.EventConsumer == nil {
		return trace.BadParameter("missing EventConsumer")
	}

	if c.ClusterIDCache == nil {
		return trace.BadParameter("missing ClusterIDCache")
	}

	if c.InstallationID == "" {
		return trace.BadParameter("missing InstallationID")
	}

	return nil
}

func NewUsageReporter(cfg UsageReporterConfig) (*usageReporter, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	return &usageReporter{
		cfg:          cfg,
		reportedApps: make(map[string]struct{}),
	}, nil
}

func (r *usageReporter) ReportApp(ctx context.Context, appURI uri.ResourceURI) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, hasAppBeenReported := r.reportedApps[appURI.String()]; hasAppBeenReported {
		log.DebugContext(ctx, "App was already reported", "app", appURI.String())
		return nil
	}

	rootClusterURI := appURI.GetRootClusterURI()
	client, err := r.cfg.ClientCache.GetCachedClient(ctx, appURI)
	if err != nil {
		return trace.Wrap(err)
	}
	rootClusterName := client.RootClusterName()
	profile, err := r.cfg.ClientStore.GetProfile(appURI.GetProfileName())
	if err != nil {
		return trace.Wrap(err)
	}

	clusterID, ok := r.cfg.ClusterIDCache.Load(rootClusterURI)
	if !ok {
		return trace.NotFound("cluster ID for %q not found", rootClusterURI)
	}

	log.DebugContext(ctx, "Reporting usage event", "app", appURI.String())

	err = r.cfg.EventConsumer.ReportUsageEvent(&teletermv1.ReportUsageEventRequest{
		AuthClusterId: clusterID,
		PrehogReq: &prehogv1alpha.SubmitConnectEventRequest{
			DistinctId: r.cfg.InstallationID,
			Timestamp:  timestamppb.Now(),
			Event: &prehogv1alpha.SubmitConnectEventRequest_ProtocolUse{
				ProtocolUse: &prehogv1alpha.ConnectProtocolUseEvent{
					ClusterName:   rootClusterName,
					UserName:      profile.Username,
					Protocol:      "app",
					Origin:        "vnet",
					AccessThrough: "vnet",
				},
			},
		},
	})
	if err != nil {
		return trace.Wrap(err, "adding usage event to queue")
	}

	r.reportedApps[appURI.String()] = struct{}{}

	return nil
}
