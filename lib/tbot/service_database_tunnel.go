/*
 * Teleport
 * Copyright (C) 2024  Gravitational, Inc.
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

package tbot

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"

	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"

	"github.com/gravitational/teleport/api/client"
	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/lib/auth"
	"github.com/gravitational/teleport/lib/reversetunnelclient"
	"github.com/gravitational/teleport/lib/srv/alpnproxy"
	"github.com/gravitational/teleport/lib/srv/alpnproxy/common"
	"github.com/gravitational/teleport/lib/tbot/config"
	"github.com/gravitational/teleport/lib/tbot/identity"
	"github.com/gravitational/teleport/lib/tlsca"
)

var _ alpnproxy.LocalProxyMiddleware = (*alpnProxyMiddleware)(nil)

type alpnProxyMiddleware struct {
	onNewConnection func(ctx context.Context, lp *alpnproxy.LocalProxy, conn net.Conn) error
	onStart         func(ctx context.Context, lp *alpnproxy.LocalProxy) error
}

func (a alpnProxyMiddleware) OnNewConnection(ctx context.Context, lp *alpnproxy.LocalProxy, conn net.Conn) error {
	if a.onNewConnection != nil {
		return a.onNewConnection(ctx, lp, conn)
	}
	return nil
}

func (a alpnProxyMiddleware) OnStart(ctx context.Context, lp *alpnproxy.LocalProxy) error {
	if a.onStart != nil {
		return a.onStart(ctx, lp)
	}
	return nil
}

// DatabaseTunnelService is a service that listens on a local port and forwards
// connections to a remote database service. It is an authenticating tunnel and
// will automatically issue and renew certificates as needed.
type DatabaseTunnelService struct {
	botCfg         *config.BotConfig
	cfg            *config.DatabaseTunnelService
	log            logrus.FieldLogger
	resolver       reversetunnelclient.Resolver
	botClient      *auth.Client
	getBotIdentity getBotIdentityFn
}

// buildLocalProxyConfig initializes the service, fetching any initial information and setting
// up the localproxy.
func (s *DatabaseTunnelService) buildLocalProxyConfig(ctx context.Context) (lpCfg alpnproxy.LocalProxyConfig, err error) {
	ctx, span := tracer.Start(ctx, "DatabaseTunnelService/buildLocalProxyConfig")
	defer span.End()

	// Determine the roles to use for the impersonated db access user. We fall
	// back to all the roles the bot has if none are configured.
	roles := s.cfg.Roles
	if len(roles) == 0 {
		roles, err = fetchDefaultRoles(ctx, s.botClient, s.getBotIdentity())
		if err != nil {
			return alpnproxy.LocalProxyConfig{}, trace.Wrap(err, "fetching default roles")
		}
	}

	// Fetch information about the database and then issue the initial
	// certificate. We issue the initial certificate to allow us to fail faster.
	// We cache the routeToDatabase as these will not change during the lifetime
	// of the service and this reduces the time needed to issue a new
	// certificate.
	routeToDatabase, err := s.getRouteToDatabaseWithImpersonation(ctx, roles)
	if err != nil {
		return alpnproxy.LocalProxyConfig{}, trace.Wrap(err)
	}
	dbCert, err := s.issueCert(ctx, routeToDatabase, roles)
	if err != nil {
		return alpnproxy.LocalProxyConfig{}, trace.Wrap(err)
	}

	proxyAddr := "leaf.tele.ottr.sh:443"

	middleware := alpnProxyMiddleware{
		onNewConnection: func(ctx context.Context, lp *alpnproxy.LocalProxy, conn net.Conn) error {
			ctx, span := tracer.Start(ctx, "DatabaseTunnelService/OnNewConnection")
			defer span.End()

			// Check if the certificate needs reissuing, if so, reissue.
			if err := lp.CheckDBCerts(tlsca.RouteToDatabase{
				ServiceName: routeToDatabase.ServiceName,
				Protocol:    routeToDatabase.Protocol,
				Database:    routeToDatabase.Database,
				Username:    routeToDatabase.Username,
			}); err != nil {
				s.log.WithField("reason", err.Error()).Info("Certificate for tunnel needs reissuing.")
				cert, err := s.issueCert(ctx, routeToDatabase, roles)
				if err != nil {
					return trace.Wrap(err, "issuing cert")
				}
				lp.SetCerts([]tls.Certificate{*cert})
			}
			return nil
		},
	}

	alpnProtocol, err := common.ToALPNProtocol(routeToDatabase.Protocol)
	if err != nil {
		return alpnproxy.LocalProxyConfig{}, trace.Wrap(err)

	}
	lpConfig := alpnproxy.LocalProxyConfig{
		Middleware: middleware,

		RemoteProxyAddr: proxyAddr,
		ParentContext:   ctx,
		Protocols:       []common.Protocol{alpnProtocol},
		Certs:           []tls.Certificate{*dbCert},
	}
	if client.IsALPNConnUpgradeRequired(
		ctx,
		proxyAddr,
		s.botCfg.Insecure,
	) {
		lpConfig.ALPNConnUpgradeRequired = true
		// If ALPN Conn Upgrade will be used, we need to set the cluster CAs
		// to validate the Proxy's auth issued host cert.
		lpConfig.RootCAs = s.getBotIdentity().TLSCAPool
	}

	return lpConfig, nil
}

func (s *DatabaseTunnelService) Run(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "DatabaseTunnelService/Run")
	defer span.End()

	listenUrl, err := url.Parse(s.cfg.Listen)
	if err != nil {
		return trace.Wrap(err, "parsing listen url")
	}

	lpCfg, err := s.buildLocalProxyConfig(ctx)
	if err != nil {
		return trace.Wrap(err, "building local proxy config")
	}

	l, err := net.Listen("tcp", listenUrl.Host)
	if err != nil {
		return trace.Wrap(err, "opening listener")
	}
	defer func() {
		if err := l.Close(); err != nil {
			s.log.WithError(err).Error("Failed to close listener")
		}
	}()

	lp, err := alpnproxy.NewLocalProxy(lpCfg)
	if err != nil {
		return trace.Wrap(err, "creating local proxy")
	}
	defer func() {
		if err := lp.Close(); err != nil {
			s.log.WithError(err).Error("Failed to close local proxy")
		}
	}()

	return trace.Wrap(lp.Start(ctx))
}

// getRouteToDatabaseWithImpersonation fetches the route to the database with
// impersonation of roles. This ensures that the user's selected roles actually
// grant access to the database.
func (s *DatabaseTunnelService) getRouteToDatabaseWithImpersonation(ctx context.Context, roles []string) (proto.RouteToDatabase, error) {
	ctx, span := tracer.Start(ctx, "DatabaseTunnelService/getRouteToDatabaseWithImpersonation")
	defer span.End()

	impersonatedIdentity, err := generateIdentity(
		ctx,
		s.botClient,
		s.getBotIdentity(),
		roles,
		s.botCfg.CertificateTTL,
		nil,
	)
	if err != nil {
		return proto.RouteToDatabase{}, trace.Wrap(err)
	}

	impersonatedClient, err := clientForFacade(
		ctx,
		s.log,
		s.botCfg,
		identity.NewFacade(s.botCfg.FIPS, s.botCfg.Insecure, impersonatedIdentity),
		s.resolver,
	)
	if err != nil {
		return proto.RouteToDatabase{}, trace.Wrap(err)
	}
	defer func() {
		if err := impersonatedClient.Close(); err != nil {
			s.log.WithError(err).Error("Failed to close impersonated client.")
		}
	}()

	return getRouteToDatabase(ctx, s.log, impersonatedClient, s.cfg.Service, s.cfg.Username, s.cfg.Database)
}

func (s *DatabaseTunnelService) issueCert(
	ctx context.Context,
	route proto.RouteToDatabase,
	roles []string,
) (*tls.Certificate, error) {
	ctx, span := tracer.Start(ctx, "DatabaseTunnelService/issueCert")
	defer span.End()

	s.log.Debug("Issuing certificate for database tunnel.")
	ident, err := generateIdentity(
		ctx,
		s.botClient,
		s.getBotIdentity(),
		roles,
		s.botCfg.CertificateTTL,
		func(req *proto.UserCertsRequest) {
			req.RouteToDatabase = route
		})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	s.log.Info("Certificate issued for database tunnel.")

	return ident.TLSCert, nil
}

// String returns a human-readable string that can uniquely identify the
// service.
func (s *DatabaseTunnelService) String() string {
	return fmt.Sprintf("%s:%s", config.DatabaseTunnelServiceType, s.cfg.Listen)
}
