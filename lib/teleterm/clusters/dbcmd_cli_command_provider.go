// Copyright 2022 Gravitational, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clusters

import (
	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/lib/client/db/dbcmd"
	"github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/teleterm/cmd/cmds"
	"github.com/gravitational/teleport/lib/teleterm/gateway"
	"github.com/gravitational/teleport/lib/tlsca"
)

// DbcmdCLICommandProvider provides CLI commands for database gateways. It needs Storage to read
// fresh profile state from the disk.
type DbcmdCLICommandProvider struct {
	storage StorageByResourceURI
	execer  dbcmd.Execer
}

type StorageByResourceURI interface {
	GetByResourceURI(string) (*Cluster, error)
}

func NewDbcmdCLICommandProvider(storage StorageByResourceURI, execer dbcmd.Execer) DbcmdCLICommandProvider {
	return DbcmdCLICommandProvider{
		storage: storage,
		execer:  execer,
	}
}

func (d DbcmdCLICommandProvider) GetCommand(gateway *gateway.Gateway) (cmds.Cmds, error) {
	cluster, err := d.storage.GetByResourceURI(gateway.TargetURI())
	if err != nil {
		return cmds.Cmds{}, trace.Wrap(err)
	}

	routeToDb := tlsca.RouteToDatabase{
		ServiceName: gateway.TargetName(),
		Protocol:    gateway.Protocol(),
		Username:    gateway.TargetUser(),
		Database:    gateway.TargetSubresourceName(),
	}

	opts := []dbcmd.ConnectCommandFunc{
		dbcmd.WithLogger(gateway.Log()),
		dbcmd.WithLocalProxy(gateway.LocalAddress(), gateway.LocalPortInt(), ""),
		dbcmd.WithNoTLS(),
		dbcmd.WithTolerateMissingCLIClient(),
		dbcmd.WithExecer(d.execer),
	}

	// DynamoDB doesn't support non-print-format use.
	if gateway.Protocol() == defaults.ProtocolDynamoDB {
		opts = append(opts, dbcmd.WithPrintFormat())
	}

	previewOpts := append(opts, dbcmd.WithPrintFormat())

	execCmd, err := dbcmd.NewCmdBuilder(cluster.clusterClient, &cluster.status, routeToDb, cluster.Name, opts...).GetConnectCommand()
	if err != nil {
		return cmds.Cmds{}, trace.Wrap(err)
	}

	previewCmd, err := dbcmd.NewCmdBuilder(cluster.clusterClient, &cluster.status, routeToDb, cluster.Name, previewOpts...).GetConnectCommand()
	if err != nil {
		return cmds.Cmds{}, trace.Wrap(err)
	}

	cmds := cmds.Cmds{
		Exec:    execCmd,
		Preview: previewCmd,
	}

	return cmds, nil
}