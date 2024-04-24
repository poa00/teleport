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

//go:build !darwin
// +build !darwin

package vnet

import (
	"context"
	"runtime"

	"github.com/gravitational/trace"
	"golang.zx2c4.com/wireguard/tun"
)

var (
	// VnetNotImplemented is an error indicating that VNet is not implemented on the host OS.
	VnetNotImplemented = &trace.NotImplementedError{Message: "VNet is not implemented on " + runtime.GOOS}
)

func createAndSetupTUNDeviceWithoutRoot(ctx context.Context, ipv6Prefix string) (tun.Device, string, error) {
	return nil, "", trace.Wrap(VnetNotImplemented)
}

func sendTUNNameAndFd(socketPath, tunName string, fd uintptr) error {
	return trace.Wrap(VnetNotImplemented)
}

func configureOS(ctx context.Context, cfg *osConfig) error {
	return trace.Wrap(VnetNotImplemented)
}
