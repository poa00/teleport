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
	"os"
	"time"

	"github.com/gravitational/trace"
	"golang.org/x/sync/errgroup"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/gravitational/teleport/api/profile"
	"github.com/gravitational/teleport/api/types"
)

// SetupAndRun creates a network stack for VNet and runs it in the background. To do this, it also
// needs to launch an admin subcommand in the background. It returns [ProcessManager] which controls
// the lifecycle of both background tasks.
//
// The caller is expected to call Close on the process manager to close the network stack and clean
// up any resources used by it.
//
// ctx is used to wait for setup steps that happen before SetupAndRun hands out the control to the
// process manager. If ctx gets canceled during SetupAndRun, the process manager gets closed along
// with its background tasks.
func SetupAndRun(ctx context.Context, appProvider AppProvider) (*ProcessManager, error) {
	ipv6Prefix, err := IPv6Prefix()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	dnsIPv6 := Ipv6WithSuffix(ipv6Prefix, []byte{2})

	pm := newProcessManager()
	success := false
	defer func() {
		if !success {
			// Closes the socket and background tasks.
			pm.Close()
		}
	}()

	// Create the socket that's used to receive the TUN device from the admin subcommand.
	socket, socketPath, err := createUnixSocket()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	slog.DebugContext(ctx, "Created unix socket for admin subcommand", "socket", socketPath)
	pm.AddBackgroundTask(func(ctx context.Context) error {
		<-ctx.Done()
		return trace.Wrap(socket.Close())
	})

	// A channel to capture an error when waiting for a TUN device to be created.
	//
	// To create a TUN device, VNet first needs to start the admin subcommand. When the subcommand
	// starts, osascript shows a password prompt. If the user closes this prompt, execAdminSubcommand
	// fails and the socket ends up being closed. To make sure that the user sees the error from
	// osascript about prompt being closed instead of an error from receiveTUNDevice about reading
	// from a closed socket, we send the error from osascript immediately through this channel, rather
	// than depending on pm.Wait.
	tunOrAdminSubcommandErrC := make(chan error, 2)
	var tun tun.Device

	pm.AddBackgroundTask(func(ctx context.Context) error {
		err := execAdminSubcommand(ctx, socketPath, ipv6Prefix.String(), dnsIPv6.String())
		// Pass the osascript error immediately, without having to wait on pm to propagate the error.
		tunOrAdminSubcommandErrC <- trace.Wrap(err)
		return trace.Wrap(err)
	})

	go func() {
		tunDevice, err := receiveTUNDevice(socket)
		tun = tunDevice
		tunOrAdminSubcommandErrC <- err
	}()

	select {
	case <-ctx.Done():
		return nil, trace.Wrap(ctx.Err())
	case err := <-tunOrAdminSubcommandErrC:
		if err != nil {
			return nil, trace.Wrap(err)
		}

		if tun == nil {
			// If the execution ever gets there, it's because of a bug.
			return nil, trace.Errorf("no TUN device created, execAdminSubcommand must have returned early with no error")
		}
	}

	appResolver := NewTCPAppResolver(appProvider)

	ns, err := newNetworkStack(&Config{
		TUNDevice:          tun,
		IPv6Prefix:         ipv6Prefix,
		DNSIPv6:            dnsIPv6,
		TCPHandlerResolver: appResolver,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	pm.AddBackgroundTask(func(ctx context.Context) error {
		return trace.Wrap(ns.Run(ctx))
	})

	success = true
	return pm, nil
}

func newProcessManager() *ProcessManager {
	ctx, cancel := context.WithCancel(context.Background())
	g, ctx := errgroup.WithContext(ctx)

	return &ProcessManager{
		g:      g,
		ctx:    ctx,
		cancel: cancel,
	}
}

// ProcessManager handles background tasks needed to run VNet.
// Its semantics are similar to an error group with context.
type ProcessManager struct {
	g      *errgroup.Group
	ctx    context.Context
	cancel context.CancelFunc
}

// AddBackgroundTask adds a function to the error group. The context passed to bgTaskFunc is a
// background context that gets canceled when Close is called or any added bgTaskFunc returns an
// error.
func (pm *ProcessManager) AddBackgroundTask(bgTaskFunc func(ctx context.Context) error) {
	pm.g.Go(func() error {
		return trace.Wrap(bgTaskFunc(pm.ctx))
	})
}

// Wait blocks and waits for the background tasks to finish, which typically happens when another
// goroutine calls Close on the process manager.
func (pm *ProcessManager) Wait() error {
	return trace.Wrap(pm.g.Wait())
}

// Close stops any active background tasks by canceling the underlying context. It then returns the
// error from the error group.
func (pm *ProcessManager) Close() error {
	go func() {
		pm.cancel()
	}()
	return trace.Wrap(pm.g.Wait())
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
