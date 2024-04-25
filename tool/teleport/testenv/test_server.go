/*
 * Teleport
 * Copyright (C) 2023  Gravitational, Inc.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

// Package testenv provides functions for creating test servers for testing.
package testenv

import (
	"context"
	"crypto"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gravitational/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/breaker"
	apidefaults "github.com/gravitational/teleport/api/defaults"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/types/accesslist"
	"github.com/gravitational/teleport/api/utils/keys"
	"github.com/gravitational/teleport/lib"
	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/cloud"
	"github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/modules"
	"github.com/gravitational/teleport/lib/service"
	"github.com/gravitational/teleport/lib/service/servicecfg"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/srv"
	"github.com/gravitational/teleport/lib/tlsca"
	"github.com/gravitational/teleport/lib/utils"
	"github.com/gravitational/teleport/tool/teleport/common"
)

// used to easily join test services
const staticToken = "test-static-token"

func init() {
	// If the test is re-executing itself, execute the command that comes over
	// the pipe. Used to test tsh ssh and tsh scp commands.
	if srv.IsReexec() {
		common.Run(common.Options{Args: os.Args[1:]})
		return
	}

	modules.SetModules(&cliModules{})
}

// WithInsecureDevMode is a test helper that sets insecure dev mode and resets
// it in test cleanup.
// It is NOT SAFE to use in parallel tests, because it modifies a global.
// To run insecure dev mode tests in parallel, group them together under a
// parent test and then run them as parallel subtests.
// and call WithInsecureDevMode before running all the tests in parallel.
func WithInsecureDevMode(t *testing.T, mode bool) {
	originalValue := lib.IsInsecureDevMode()
	lib.SetInsecureDevMode(mode)
	// To detect tests that run in parallel incorrectly, call t.Setenv with a
	// dummy env var - that function detects tests with parallel ancestors
	// and panics, preventing improper use of this helper.
	t.Setenv("WithInsecureDevMode", "1")
	t.Cleanup(func() {
		lib.SetInsecureDevMode(originalValue)
	})
}

// WithResyncInterval is a test helper that sets the tunnel resync interval and
// resets it in test cleanup.
// Useful to substantially speedup test cluster setup - passing 0 for the
// interval selects a reasonably fast default of 100ms.
// It is NOT SAFE to use in parallel tests, because it modifies a global.
func WithResyncInterval(t *testing.T, interval time.Duration) {
	if interval == 0 {
		interval = time.Millisecond * 100
	}
	oldResyncInterval := defaults.ResyncInterval
	defaults.ResyncInterval = interval
	// To detect tests that run in parallel incorrectly, call t.Setenv with a
	// dummy env var - that function detects tests with parallel ancestors
	// and panics, preventing improper use of this helper.
	t.Setenv("WithResyncInterval", "1")
	t.Cleanup(func() {
		defaults.ResyncInterval = oldResyncInterval
	})
}

// MakeTestServer creates a Teleport Server for testing.
func MakeTestServer(t *testing.T, opts ...TestServerOptFunc) (process *service.TeleportProcess) {
	t.Helper()

	var options TestServersOpts
	for _, opt := range opts {
		opt(&options)
	}

	// Set up a test auth server with default config.
	cfg := servicecfg.MakeDefaultConfig()
	cfg.CircuitBreakerConfig = breaker.NoopBreakerConfig()
	cfg.CachePolicy.Enabled = false
	// Disables cloud auto-imported labels when running tests in cloud envs
	// such as Github Actions.
	//
	// This is required otherwise Teleport will import cloud instance
	// labels, and use them for example as labels in Kubernetes Service and
	// cause some tests to fail because the output includes unexpected
	// labels.
	//
	// It is also found that Azure metadata client can throw "Too many
	// requests" during CI which fails services.NewTeleport.
	cfg.InstanceMetadataClient = cloud.NewDisabledIMDSClient()

	cfg.Hostname = "server01"
	cfg.DataDir = t.TempDir()
	cfg.Log = utils.NewLoggerForTests()
	authAddr := utils.NetAddr{AddrNetwork: "tcp", Addr: NewTCPListener(t, service.ListenerAuth, &cfg.FileDescriptors)}
	cfg.SetToken(staticToken)
	cfg.SetAuthServerAddress(authAddr)

	cfg.Auth.ListenAddr = authAddr
	cfg.Auth.BootstrapResources = options.Bootstrap
	cfg.Auth.StorageConfig.Params = backend.Params{defaults.BackendPath: filepath.Join(cfg.DataDir, defaults.BackendDir)}
	staticToken, err := types.NewStaticTokens(types.StaticTokensSpecV2{
		StaticTokens: []types.ProvisionTokenV1{{
			Roles:   []types.SystemRole{types.RoleProxy, types.RoleDatabase, types.RoleTrustedCluster, types.RoleNode, types.RoleApp},
			Expires: time.Now().Add(time.Minute),
			Token:   staticToken,
		}},
	})
	require.NoError(t, err)
	cfg.Auth.StaticTokens = staticToken

	cfg.Proxy.WebAddr = utils.NetAddr{AddrNetwork: "tcp", Addr: NewTCPListener(t, service.ListenerProxyWeb, &cfg.FileDescriptors)}
	cfg.Proxy.SSHAddr = utils.NetAddr{AddrNetwork: "tcp", Addr: NewTCPListener(t, service.ListenerProxySSH, &cfg.FileDescriptors)}
	cfg.Proxy.ReverseTunnelListenAddr = utils.NetAddr{AddrNetwork: "tcp", Addr: NewTCPListener(t, service.ListenerProxyTunnel, &cfg.FileDescriptors)}
	cfg.Proxy.DisableWebInterface = true

	cfg.SSH.Addr = utils.NetAddr{AddrNetwork: "tcp", Addr: NewTCPListener(t, service.ListenerNodeSSH, &cfg.FileDescriptors)}
	cfg.SSH.DisableCreateHostUser = true

	// Apply options
	for _, fn := range options.ConfigFuncs {
		fn(cfg)
	}

	process, err = service.NewTeleport(cfg)
	require.NoError(t, err, trace.DebugReport(err))
	require.NoError(t, process.Start())
	t.Cleanup(func() {
		require.NoError(t, process.Close())
		require.NoError(t, process.Wait())
	})

	waitForServices(t, process, cfg)

	return process
}

// NewTCPListener creates a new TCP listener on 127.0.0.1:0, adds it to the
// FileDescriptor slice (with the specified type) and returns its actual local
// address as a string (for use in configuration). Takes a pointer to the slice
// so that it's convenient to call in the middle of a FileConfig or Config
// struct literal.
func NewTCPListener(t *testing.T, lt service.ListenerType, fds *[]*servicecfg.FileDescriptor) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	addr := l.Addr().String()

	// File() returns a dup of the listener's file descriptor as an *os.File, so
	// the original net.Listener still needs to be closed.
	lf, err := l.(*net.TCPListener).File()
	require.NoError(t, err)
	fd := &servicecfg.FileDescriptor{
		Type:    string(lt),
		Address: addr,
		File:    lf,
	}
	// If the file descriptor slice ends up being passed to a TeleportProcess
	// that successfully starts, listeners will either get "imported" and used
	// or discarded and closed, this is just an extra safety measure that closes
	// the listener at the end of the test anyway (the finalizer would do that
	// anyway, in principle).
	t.Cleanup(func() { require.NoError(t, fd.Close()) })

	*fds = append(*fds, fd)
	return addr
}

func waitForServices(t *testing.T, auth *service.TeleportProcess, cfg *servicecfg.Config) {
	var serviceReadyEvents []string
	if cfg.Proxy.Enabled {
		serviceReadyEvents = append(serviceReadyEvents, service.ProxyWebServerReady)
	}
	if cfg.SSH.Enabled {
		serviceReadyEvents = append(serviceReadyEvents, service.NodeSSHReady)
	}
	if cfg.Databases.Enabled {
		serviceReadyEvents = append(serviceReadyEvents, service.DatabasesReady)
	}
	if cfg.Apps.Enabled {
		serviceReadyEvents = append(serviceReadyEvents, service.AppsReady)
	}
	if cfg.Auth.Enabled {
		serviceReadyEvents = append(serviceReadyEvents, service.AuthTLSReady)
	}
	waitForEvents(t, auth, serviceReadyEvents...)

	if cfg.Auth.Enabled && cfg.Databases.Enabled {
		waitForDatabases(t, auth, cfg.Databases.Databases)
	}
}

func waitForEvents(t *testing.T, svc service.Supervisor, events ...string) {
	for _, event := range events {
		_, err := svc.WaitForEventTimeout(10*time.Second, event)
		require.NoError(t, err, "service server didn't receive %v event after 10s", event)
	}
}

func waitForDatabases(t *testing.T, auth *service.TeleportProcess, dbs []servicecfg.Database) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		select {
		case <-time.After(500 * time.Millisecond):
			all, err := auth.GetAuthServer().GetDatabaseServers(ctx, apidefaults.Namespace)
			require.NoError(t, err)

			// Count how many input "dbs" are registered.
			var registered int
			for _, db := range dbs {
				for _, a := range all {
					if a.GetName() == db.Name {
						registered++
						break
					}
				}
			}

			if registered == len(dbs) {
				return
			}
		case <-ctx.Done():
			t.Fatal("Databases not registered after 10s")
		}
	}
}

type TestServersOpts struct {
	Bootstrap   []types.Resource
	ConfigFuncs []func(cfg *servicecfg.Config)
}

type TestServerOptFunc func(o *TestServersOpts)

// TODO(Joerger): consider applying these defaults in MakeTestServer by default.
func WithTestDefaults(t *testing.T) TestServerOptFunc {
	return WithAuthConfig(func(cfg *servicecfg.AuthConfig) {
		// Disable session recording to prevent writing to disk after the test concludes.
		cfg.SessionRecordingConfig.SetMode(types.RecordOff)
		// Speeds up tests considerably.
		cfg.StorageConfig.Params["poll_stream_period"] = 50 * time.Millisecond
	})
}

func WithBootstrap(bootstrap ...types.Resource) TestServerOptFunc {
	return func(o *TestServersOpts) {
		o.Bootstrap = append(o.Bootstrap, bootstrap...)
	}
}

func WithConfig(fn func(cfg *servicecfg.Config)) TestServerOptFunc {
	return func(o *TestServersOpts) {
		o.ConfigFuncs = append(o.ConfigFuncs, fn)
	}
}

func WithAuthConfig(fn func(*servicecfg.AuthConfig)) TestServerOptFunc {
	return WithConfig(func(cfg *servicecfg.Config) {
		fn(&cfg.Auth)
	})
}

func WithClusterName(t *testing.T, n string) TestServerOptFunc {
	return WithAuthConfig(func(cfg *servicecfg.AuthConfig) {
		clusterName, err := services.NewClusterNameWithRandomID(
			types.ClusterNameSpecV2{
				ClusterName: n,
			})
		require.NoError(t, err)
		cfg.ClusterName = clusterName
	})
}

func WithHostname(hostname string) TestServerOptFunc {
	return WithConfig(func(cfg *servicecfg.Config) {
		cfg.Hostname = hostname
	})
}

func WithSSHPublicAddrs(addrs ...string) TestServerOptFunc {
	return WithConfig(func(cfg *servicecfg.Config) {
		cfg.SSH.PublicAddrs = utils.MustParseAddrList(addrs...)
	})
}

func WithSSHLabel(key, value string) TestServerOptFunc {
	return WithConfig(func(cfg *servicecfg.Config) {
		if cfg.SSH.Labels == nil {
			cfg.SSH.Labels = make(map[string]string)
		}
		cfg.SSH.Labels[key] = value
	})
}

func WithAuthPreference(authPref types.AuthPreference) TestServerOptFunc {
	return WithConfig(func(cfg *servicecfg.Config) {
		cfg.Auth.Preference = authPref
	})
}

func SetupTrustedCluster(ctx context.Context, t *testing.T, rootServer, leafServer *service.TeleportProcess, additionalRoleMappings ...types.RoleMapping) {
	rootProxyAddr, err := rootServer.ProxyWebAddr()
	require.NoError(t, err)
	rootProxyTunnelAddr, err := rootServer.ProxyTunnelAddr()
	require.NoError(t, err)

	tc, err := types.NewTrustedCluster("root-cluster", types.TrustedClusterSpecV2{
		Enabled:              true,
		Token:                staticToken,
		ProxyAddress:         rootProxyAddr.String(),
		ReverseTunnelAddress: rootProxyTunnelAddr.String(),
		RoleMap: append(additionalRoleMappings,
			types.RoleMapping{
				Remote: "access",
				Local:  []string{"access"},
			},
		),
	})
	require.NoError(t, err)

	_, err = leafServer.GetAuthServer().UpsertTrustedCluster(ctx, tc)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		rt, err := rootServer.GetAuthServer().GetTunnelConnections("leaf")
		assert.NoError(t, err)
		assert.Len(t, rt, 1)
	}, time.Second*10, time.Second)
}

type cliModules struct{}

func (p *cliModules) GenerateAccessRequestPromotions(_ context.Context, _ modules.AccessResourcesGetter, _ types.AccessRequest) (*types.AccessRequestAllowedPromotions, error) {
	return &types.AccessRequestAllowedPromotions{}, nil
}

func (p *cliModules) GetSuggestedAccessLists(ctx context.Context, _ *tlsca.Identity, _ modules.AccessListSuggestionClient, _ modules.AccessListGetter, _ string) ([]*accesslist.AccessList, error) {
	return []*accesslist.AccessList{}, nil
}

// BuildType returns build type.
func (p *cliModules) BuildType() string {
	return "CLI"
}

// PrintVersion prints the Teleport version.
func (p *cliModules) PrintVersion() {
	fmt.Println("Teleport CLI")
}

// Features returns supported features
func (p *cliModules) Features() modules.Features {
	return modules.Features{
		Kubernetes:              true,
		DB:                      true,
		App:                     true,
		AdvancedAccessWorkflows: true,
		AccessControls:          true,
	}
}

// IsBoringBinary checks if the binary was compiled with BoringCrypto.
func (p *cliModules) IsBoringBinary() bool {
	return false
}

// AttestHardwareKey attests a hardware key.
func (p *cliModules) AttestHardwareKey(_ context.Context, _ interface{}, _ *keys.AttestationStatement, _ crypto.PublicKey, _ time.Duration) (*keys.AttestationData, error) {
	return nil, trace.NotFound("no attestation data for the given key")
}

func (p *cliModules) EnableRecoveryCodes() {
}

func (p *cliModules) EnablePlugins() {
}

func (p *cliModules) EnableAccessGraph() {}

func (p *cliModules) EnableAccessMonitoring() {}

func (p *cliModules) SetFeatures(f modules.Features) {
}
