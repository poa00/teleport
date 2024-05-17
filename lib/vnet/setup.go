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
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/gravitational/trace"
	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/tcpip"

	"github.com/gravitational/teleport/api/profile"
	"github.com/gravitational/teleport/api/types"
)

// CreateSocket creates a socket that's going to be used to receive the TUN device created by the
// admin subcommand. The admin subcommand quits when it detects that the socket has been closed.
func CreateSocket(ctx context.Context) (*net.UnixListener, string, error) {
	socket, socketPath, err := createUnixSocket()
	if err != nil {
		return nil, "", trace.Wrap(err)
	}
	slog.DebugContext(ctx, "Created unix socket for admin subcommand", "socket", socketPath)
	return socket, socketPath, nil
}

// TODO: Add comment.
func Setup(ctx context.Context, appProvider AppProvider, socket *net.UnixListener, ipv6Prefix, dnsIPv6 tcpip.Address) (*NetworkStack, error) {
	tun, err := receiveTUNDevice(ctx, socket)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	appResolver := NewTCPAppResolver(appProvider)

	ns, err := NewNetworkStack(&Config{
		TUNDevice:          tun,
		IPv6Prefix:         ipv6Prefix,
		DNSIPv6:            dnsIPv6,
		TCPHandlerResolver: appResolver,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return ns, nil
}

// AdminSubcommand is the tsh subcommand that should run as root that will create and setup a TUN device and
// pass the file descriptor for that device over the unix socket found at socketPath.
//
// It also handles host OS configuration that must run as root, and stays alive to keep the host configuration
// up to date. It will stay running until the socket at [socketPath] is deleting or encountering an
// unrecoverable error.
func AdminSubcommand(ctx context.Context, socketPath, ipv6Prefix, dnsAddr string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	tunCh, errCh := createAndSetupTUNDeviceAsRoot(ctx, ipv6Prefix, dnsAddr)
	var tun tun.Device
	select {
	case tun = <-tunCh:
	case err := <-errCh:
		return trace.Wrap(err, "performing admin setup")
	}
	tunName, err := tun.Name()
	if err != nil {
		return trace.Wrap(err, "getting TUN name")
	}
	if err := sendTUNNameAndFd(socketPath, tunName, tun.File().Fd()); err != nil {
		return trace.Wrap(err, "sending TUN over socket")
	}

	// Stay alive until we get an error on errCh, indicating that the osConfig loop exited.
	// If the socket is deleted, indicating that the parent process exited, cancel the context and then wait
	// for an err or errCh.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(socketPath); err != nil {
				slog.DebugContext(ctx, "failed to stat socket path, assuming parent exited")
				cancel()
				return trace.Wrap(<-errCh)
			}
		case err = <-errCh:
			return trace.Wrap(err)
		}
	}
}

// createAndSetupTUNDeviceAsRoot creates a virtual network device and configures the host OS to use that device for
// VNet connections.
//
// After the TUN device is created, it will be sent on the result channel. Any error will be sent on the err
// channel. Always select on both the result channel and the err channel when waiting for a result.
//
// This will keep running until [ctx] is canceled or an unrecoverable error is encountered, in order to keep
// the host OS configuration up to date.
func createAndSetupTUNDeviceAsRoot(ctx context.Context, ipv6Prefix, dnsAddr string) (<-chan tun.Device, <-chan error) {
	tunCh := make(chan tun.Device, 1)
	errCh := make(chan error, 1)

	tun, tunName, err := createTUNDevice(ctx)
	if err != nil {
		errCh <- trace.Wrap(err, "creating TUN device")
		return tunCh, errCh
	}
	tunCh <- tun

	go func() {
		var err error
		tunIPv6 := ipv6Prefix + "1"
		cfg := osConfig{
			tunName: tunName,
			tunIPv6: tunIPv6,
			dnsAddr: dnsAddr,
		}
		if cfg.dnsZones, err = dnsZones(); err != nil {
			errCh <- trace.Wrap(err, "getting DNS zones")
			return
		}
		if err := configureOS(ctx, &cfg); err != nil {
			errCh <- trace.Wrap(err, "configuring OS")
			return
		}

		// Re-check the DNS zones every 10 seconds, and configure the host OS appropriately.
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
	loop:
		for {
			select {
			case <-ticker.C:
				if cfg.dnsZones, err = dnsZones(); err != nil {
					errCh <- trace.Wrap(err, "getting DNS zones")
					return
				}
				if err := configureOS(ctx, &cfg); err != nil {
					errCh <- trace.Wrap(err, "configuring OS")
					return
				}
			case <-ctx.Done():
				break loop
			}
		}

		// Shutting down, deconfigure OS.
		errCh <- trace.Wrap(configureOS(ctx, &osConfig{}))
	}()
	return tunCh, errCh
}

func createTUNDevice(ctx context.Context) (tun.Device, string, error) {
	slog.DebugContext(ctx, "Creating TUN device.")
	dev, err := tun.CreateTUN("utun", mtu)
	if err != nil {
		return nil, "", trace.Wrap(err, "creating TUN device")
	}
	name, err := dev.Name()
	if err != nil {
		return nil, "", trace.Wrap(err, "getting TUN device name")
	}
	return dev, name, nil
}

type osConfig struct {
	tunName  string
	tunIPv6  string
	dnsAddr  string
	dnsZones []string
}

func dnsZones() ([]string, error) {
	profileDir := profile.FullProfilePath(os.Getenv(types.HomeEnvVar))
	profileNames, err := profile.ListProfileNames(profileDir)
	if err != nil {
		return nil, trace.Wrap(err, "listing profiles")
	}
	// profile names are Teleport proxy addresses.
	// TODO(nklaassen): support leaf clusters and custom DNS zones.
	// TODO(nklaassen): check if profiles are expired.
	return profileNames, nil
}
