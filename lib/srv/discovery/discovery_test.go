/*
Copyright 2022 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package discovery

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v2"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/eks"
	"github.com/aws/aws-sdk-go/service/eks/eksiface"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/aws/aws-sdk-go/service/redshift"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/aws/aws-sdk-go/service/ssm/ssmiface"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"

	"github.com/gravitational/teleport/api/defaults"
	usageeventsv1 "github.com/gravitational/teleport/api/gen/proto/go/usageevents/v1"
	"github.com/gravitational/teleport/api/internalutils/stream"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/types/events"
	"github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/teleport/lib/auth"
	"github.com/gravitational/teleport/lib/authz"
	"github.com/gravitational/teleport/lib/cloud"
	"github.com/gravitational/teleport/lib/cloud/azure"
	"github.com/gravitational/teleport/lib/cloud/gcp"
	"github.com/gravitational/teleport/lib/cloud/mocks"
	libevents "github.com/gravitational/teleport/lib/events"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/srv/server"
	usagereporter "github.com/gravitational/teleport/lib/usagereporter/teleport"
)

type mockSSMClient struct {
	ssmiface.SSMAPI
	commandOutput *ssm.SendCommandOutput
	invokeOutput  *ssm.GetCommandInvocationOutput
}

func (sm *mockSSMClient) SendCommandWithContext(_ context.Context, input *ssm.SendCommandInput, _ ...request.Option) (*ssm.SendCommandOutput, error) {
	return sm.commandOutput, nil
}

func (sm *mockSSMClient) GetCommandInvocationWithContext(_ context.Context, input *ssm.GetCommandInvocationInput, _ ...request.Option) (*ssm.GetCommandInvocationOutput, error) {
	return sm.invokeOutput, nil
}

func (sm *mockSSMClient) WaitUntilCommandExecutedWithContext(aws.Context, *ssm.GetCommandInvocationInput, ...request.WaiterOption) error {
	if aws.StringValue(sm.commandOutput.Command.Status) == ssm.CommandStatusFailed {
		return awserr.New(request.WaiterResourceNotReadyErrorCode, "err", nil)
	}
	return nil
}

type mockEmitter struct {
	eventHandler func(*testing.T, events.AuditEvent, *Server)
	server       *Server
	t            *testing.T
}

func (me *mockEmitter) EmitAuditEvent(ctx context.Context, event events.AuditEvent) error {
	if me.eventHandler != nil {
		me.eventHandler(me.t, event, me.server)
	}
	return nil
}

type mockUsageReporter struct {
	mu          sync.Mutex
	eventsCount int
}

func (m *mockUsageReporter) AnonymizeAndSubmit(events ...usagereporter.Anonymizable) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventsCount++
}

func (m *mockUsageReporter) EventsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eventsCount
}

type mockEC2Client struct {
	ec2iface.EC2API
	output *ec2.DescribeInstancesOutput
}

func (m *mockEC2Client) DescribeInstancesPagesWithContext(
	ctx context.Context, input *ec2.DescribeInstancesInput,
	f func(dio *ec2.DescribeInstancesOutput, b bool) bool, opts ...request.Option,
) error {
	f(m.output, true)
	return nil
}

func genEC2InstanceIDs(n int) []string {
	var ec2InstanceIDs []string
	for i := 0; i < n; i++ {
		ec2InstanceIDs = append(ec2InstanceIDs, fmt.Sprintf("instance-id-%d", i))
	}
	return ec2InstanceIDs
}

func genEC2Instances(n int) []*ec2.Instance {
	var ec2Instances []*ec2.Instance
	for _, id := range genEC2InstanceIDs(n) {
		ec2Instances = append(ec2Instances, &ec2.Instance{
			InstanceId: aws.String(id),
			Tags: []*ec2.Tag{{
				Key:   aws.String("env"),
				Value: aws.String("dev"),
			}},
			State: &ec2.InstanceState{
				Name: aws.String(ec2.InstanceStateNameRunning),
			},
		})
	}
	return ec2Instances
}

type mockSSMInstaller struct {
	mu                 sync.Mutex
	installedInstances []string
}

func (m *mockSSMInstaller) Run(_ context.Context, req server.SSMRunRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range req.Instances {
		m.installedInstances = append(m.installedInstances, inst.InstanceID)
	}
	return nil
}

func (m *mockSSMInstaller) GetInstalledInstances() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.installedInstances
}

func TestDiscoveryServer(t *testing.T) {
	t.Parallel()
	tcs := []struct {
		name string
		// presentInstances is a list of servers already present in teleport
		presentInstances       []types.Server
		foundEC2Instances      []*ec2.Instance
		ssm                    *mockSSMClient
		emitter                *mockEmitter
		wantInstalledInstances []string
	}{
		{
			name:             "no nodes present, 1 found ",
			presentInstances: []types.Server{},
			foundEC2Instances: []*ec2.Instance{
				{
					InstanceId: aws.String("instance-id-1"),
					Tags: []*ec2.Tag{{
						Key:   aws.String("env"),
						Value: aws.String("dev"),
					}},
					State: &ec2.InstanceState{
						Name: aws.String(ec2.InstanceStateNameRunning),
					},
				},
			},
			ssm: &mockSSMClient{
				commandOutput: &ssm.SendCommandOutput{
					Command: &ssm.Command{
						CommandId: aws.String("command-id-1"),
					},
				},
				invokeOutput: &ssm.GetCommandInvocationOutput{
					Status:       aws.String(ssm.CommandStatusSuccess),
					ResponseCode: aws.Int64(0),
				},
			},
			emitter: &mockEmitter{
				eventHandler: func(t *testing.T, ae events.AuditEvent, server *Server) {
					t.Helper()
					require.Equal(t, &events.SSMRun{
						Metadata: events.Metadata{
							Type: libevents.SSMRunEvent,
							Code: libevents.SSMRunSuccessCode,
						},
						CommandID:  "command-id-1",
						AccountID:  "owner",
						InstanceID: "instance-id-1",
						Region:     "eu-central-1",
						ExitCode:   0,
						Status:     ssm.CommandStatusSuccess,
					}, ae)
				},
			},
			wantInstalledInstances: []string{"instance-id-1"},
		},
		{
			name: "nodes present, instance filtered",
			presentInstances: []types.Server{
				&types.ServerV2{
					Kind: types.KindNode,
					Metadata: types.Metadata{
						Name: "name",
						Labels: map[string]string{
							types.AWSAccountIDLabel:  "owner",
							types.AWSInstanceIDLabel: "instance-id-1",
						},
						Namespace: defaults.Namespace,
					},
				},
			},
			foundEC2Instances: []*ec2.Instance{
				{
					InstanceId: aws.String("instance-id-1"),
					Tags: []*ec2.Tag{{
						Key:   aws.String("env"),
						Value: aws.String("dev"),
					}},
					State: &ec2.InstanceState{
						Name: aws.String(ec2.InstanceStateNameRunning),
					},
				},
			},
			ssm: &mockSSMClient{
				commandOutput: &ssm.SendCommandOutput{
					Command: &ssm.Command{
						CommandId: aws.String("command-id-1"),
					},
				},
				invokeOutput: &ssm.GetCommandInvocationOutput{
					Status:       aws.String(ssm.CommandStatusSuccess),
					ResponseCode: aws.Int64(0),
				},
			},
			emitter: &mockEmitter{},
		},
		{
			name: "nodes present, instance not filtered",
			presentInstances: []types.Server{
				&types.ServerV2{
					Kind: types.KindNode,
					Metadata: types.Metadata{
						Name: "name",
						Labels: map[string]string{
							types.AWSAccountIDLabel:  "owner",
							types.AWSInstanceIDLabel: "wow-its-a-different-instance",
						},
						Namespace: defaults.Namespace,
					},
				},
			},
			foundEC2Instances: []*ec2.Instance{
				{
					InstanceId: aws.String("instance-id-1"),
					Tags: []*ec2.Tag{{
						Key:   aws.String("env"),
						Value: aws.String("dev"),
					}},
					State: &ec2.InstanceState{
						Name: aws.String(ec2.InstanceStateNameRunning),
					},
				},
			},
			ssm: &mockSSMClient{
				commandOutput: &ssm.SendCommandOutput{
					Command: &ssm.Command{
						CommandId: aws.String("command-id-1"),
					},
				},
				invokeOutput: &ssm.GetCommandInvocationOutput{
					Status:       aws.String(ssm.CommandStatusSuccess),
					ResponseCode: aws.Int64(0),
				},
			},
			emitter:                &mockEmitter{},
			wantInstalledInstances: []string{"instance-id-1"},
		},
		{
			name:              "chunked nodes get 2 log messages",
			presentInstances:  []types.Server{},
			foundEC2Instances: genEC2Instances(58),
			ssm: &mockSSMClient{
				commandOutput: &ssm.SendCommandOutput{
					Command: &ssm.Command{
						CommandId: aws.String("command-id-1"),
					},
				},
				invokeOutput: &ssm.GetCommandInvocationOutput{
					Status:       aws.String(ssm.CommandStatusSuccess),
					ResponseCode: aws.Int64(0),
				},
			},
			emitter:                &mockEmitter{},
			wantInstalledInstances: genEC2InstanceIDs(58),
		},
	}

	for _, tc := range tcs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClients := cloud.TestCloudClients{
				EC2: &mockEC2Client{
					output: &ec2.DescribeInstancesOutput{
						Reservations: []*ec2.Reservation{
							{
								OwnerId:   aws.String("owner"),
								Instances: tc.foundEC2Instances,
							},
						},
					},
				},
				SSM: tc.ssm,
			}

			ctx := context.Background()
			// Create and start test auth server.
			testAuthServer, err := auth.NewTestAuthServer(auth.TestAuthServerConfig{
				Dir: t.TempDir(),
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, testAuthServer.Close()) })

			tlsServer, err := testAuthServer.NewTestTLSServer()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, tlsServer.Close()) })

			// Auth client for discovery service.
			identity := auth.TestServerID(types.RoleDiscovery, "hostID")
			authClient, err := tlsServer.NewClient(identity)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, authClient.Close()) })

			for _, instance := range tc.presentInstances {
				_, err := tlsServer.Auth().UpsertNode(ctx, instance)
				require.NoError(t, err)
			}

			installer := &mockSSMInstaller{}
			reporter := &mockUsageReporter{}
			tlsServer.Auth().SetUsageReporter(reporter)
			server, err := New(authz.ContextWithUser(context.Background(), identity.I), &Config{
				Clients:     &testClients,
				AccessPoint: tlsServer.Auth(),
				AWSMatchers: []types.AWSMatcher{{
					Types:   []string{"ec2"},
					Regions: []string{"eu-central-1"},
					Tags:    map[string]utils.Strings{"teleport": {"yes"}},
					SSM:     &types.AWSSSM{DocumentName: "document"},
					Params: &types.InstallerParams{
						InstallTeleport: true,
					},
				}},
				Emitter: tc.emitter,
			})

			require.NoError(t, err)
			server.ec2Installer = installer
			tc.emitter.server = server
			tc.emitter.t = t

			r, w := io.Pipe()
			t.Cleanup(func() {
				require.NoError(t, r.Close())
				require.NoError(t, w.Close())
			})

			go server.Start()
			t.Cleanup(server.Stop)

			if len(tc.wantInstalledInstances) > 0 {
				slices.Sort(tc.wantInstalledInstances)
				require.Eventually(t, func() bool {
					instances := installer.GetInstalledInstances()
					slices.Sort(instances)
					return slices.Equal(tc.wantInstalledInstances, instances) && reporter.EventsCount() == len(tc.wantInstalledInstances)
				}, 500*time.Millisecond, 50*time.Millisecond)
			} else {
				require.Never(t, func() bool {
					return len(installer.GetInstalledInstances()) > 0 || reporter.EventsCount() > 0
				}, 500*time.Millisecond, 50*time.Millisecond)
			}
		})
	}
}

func TestDiscoveryKube(t *testing.T) {
	const (
		mainDiscoveryGroup  = "main"
		otherDiscoveryGroup = "other"
	)
	t.Parallel()
	tcs := []struct {
		name                          string
		existingKubeClusters          []types.KubeCluster
		awsMatchers                   []types.AWSMatcher
		azureMatchers                 []types.AzureMatcher
		gcpMatchers                   []types.GCPMatcher
		expectedClustersToExistInAuth []types.KubeCluster
		clustersNotUpdated            []string
		expectedAssumedRoles          []string
		expectedExternalIDs           []string
		wantEvents                    int
	}{
		{
			name:                 "no clusters in auth server, import 2 prod clusters from EKS",
			existingKubeClusters: []types.KubeCluster{},
			awsMatchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
				},
			},
			expectedClustersToExistInAuth: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[0], mainDiscoveryGroup),
				mustConvertEKSToKubeCluster(t, eksMockClusters[1], mainDiscoveryGroup),
			},
			wantEvents: 2,
		},
		{
			name:                 "no clusters in auth server, import 2 prod clusters from EKS with assumed roles",
			existingKubeClusters: []types.KubeCluster{},
			awsMatchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
					AssumeRole: &types.AssumeRole{
						RoleARN:    "arn:aws:iam::123456789012:role/teleport-role",
						ExternalID: "external-id",
					},
				},
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
					AssumeRole: &types.AssumeRole{
						RoleARN:    "arn:aws:iam::123456789012:role/teleport-role2",
						ExternalID: "external-id2",
					},
				},
			},
			expectedClustersToExistInAuth: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[0], mainDiscoveryGroup),
				mustConvertEKSToKubeCluster(t, eksMockClusters[1], mainDiscoveryGroup),
			},
			expectedAssumedRoles: []string{"arn:aws:iam::123456789012:role/teleport-role", "arn:aws:iam::123456789012:role/teleport-role2"},
			expectedExternalIDs:  []string{"external-id", "external-id2"},
			wantEvents:           2,
		},
		{
			name:                 "no clusters in auth server, import 2 stg clusters from EKS",
			existingKubeClusters: []types.KubeCluster{},
			awsMatchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"stg"}},
				},
			},
			expectedClustersToExistInAuth: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[2], mainDiscoveryGroup),
				mustConvertEKSToKubeCluster(t, eksMockClusters[3], mainDiscoveryGroup),
			},
			wantEvents: 2,
		},
		{
			name: "1 cluster in auth server not updated + import 1 prod cluster from EKS",
			existingKubeClusters: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[0], mainDiscoveryGroup),
			},
			awsMatchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
				},
			},
			expectedClustersToExistInAuth: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[0], mainDiscoveryGroup),
				mustConvertEKSToKubeCluster(t, eksMockClusters[1], mainDiscoveryGroup),
			},
			clustersNotUpdated: []string{mustConvertEKSToKubeCluster(t, eksMockClusters[0], mainDiscoveryGroup).GetName()},
			wantEvents:         1,
		},
		{
			name: "1 cluster in auth that belongs the same discovery group but has unmatched labels + import 2 prod clusters from EKS",
			existingKubeClusters: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[3], mainDiscoveryGroup),
			},
			awsMatchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
				},
			},
			expectedClustersToExistInAuth: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[0], mainDiscoveryGroup),
				mustConvertEKSToKubeCluster(t, eksMockClusters[1], mainDiscoveryGroup),
			},
			clustersNotUpdated: []string{},
			wantEvents:         2,
		},
		{
			name: "1 cluster in auth that belongs to a different discovery group + import 2 prod clusters from EKS",
			existingKubeClusters: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[3], otherDiscoveryGroup),
			},
			awsMatchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
				},
			},
			expectedClustersToExistInAuth: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[3], otherDiscoveryGroup),
				mustConvertEKSToKubeCluster(t, eksMockClusters[0], mainDiscoveryGroup),
				mustConvertEKSToKubeCluster(t, eksMockClusters[1], mainDiscoveryGroup),
			},
			clustersNotUpdated: []string{},
			wantEvents:         2,
		},
		{
			name: "1 cluster in auth that must be updated + import 1 prod clusters from EKS",
			existingKubeClusters: []types.KubeCluster{
				// add an extra static label to force update in auth server
				modifyKubeCluster(mustConvertEKSToKubeCluster(t, eksMockClusters[1], mainDiscoveryGroup)),
			},
			awsMatchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
				},
			},
			expectedClustersToExistInAuth: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[0], mainDiscoveryGroup),
				mustConvertEKSToKubeCluster(t, eksMockClusters[1], mainDiscoveryGroup),
			},
			clustersNotUpdated: []string{},
			wantEvents:         1,
		},
		{
			name: "2 clusters in auth that matches but one must be updated +  import 2 prod clusters, 1 from EKS and other from AKS",
			existingKubeClusters: []types.KubeCluster{
				// add an extra static label to force update in auth server
				modifyKubeCluster(mustConvertEKSToKubeCluster(t, eksMockClusters[1], mainDiscoveryGroup)),
				mustConvertAKSToKubeCluster(t, aksMockClusters["group1"][0], mainDiscoveryGroup),
			},
			awsMatchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
				},
			},
			azureMatchers: []types.AzureMatcher{
				{
					Types:          []string{"aks"},
					ResourceTags:   map[string]utils.Strings{"env": {"prod"}},
					Regions:        []string{types.Wildcard},
					ResourceGroups: []string{types.Wildcard},
					Subscriptions:  []string{"sub1"},
				},
			},
			expectedClustersToExistInAuth: []types.KubeCluster{
				mustConvertEKSToKubeCluster(t, eksMockClusters[0], mainDiscoveryGroup),
				mustConvertEKSToKubeCluster(t, eksMockClusters[1], mainDiscoveryGroup),
				mustConvertAKSToKubeCluster(t, aksMockClusters["group1"][0], mainDiscoveryGroup),
				mustConvertAKSToKubeCluster(t, aksMockClusters["group1"][1], mainDiscoveryGroup),
			},
			clustersNotUpdated: []string{mustConvertAKSToKubeCluster(t, aksMockClusters["group1"][0], mainDiscoveryGroup).GetName()},
			wantEvents:         2,
		},
		{
			name:                 "no clusters in auth server, import 2 prod clusters from GKE",
			existingKubeClusters: []types.KubeCluster{},
			gcpMatchers: []types.GCPMatcher{
				{
					Types:      []string{"gke"},
					Locations:  []string{"*"},
					ProjectIDs: []string{"p1"},
					Tags:       map[string]utils.Strings{"env": {"prod"}},
				},
			},
			expectedClustersToExistInAuth: []types.KubeCluster{
				mustConvertGKEToKubeCluster(t, gkeMockClusters[0], mainDiscoveryGroup),
				mustConvertGKEToKubeCluster(t, gkeMockClusters[1], mainDiscoveryGroup),
			},
			wantEvents: 2,
		},
	}

	for _, tc := range tcs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sts := &mocks.STSMock{}
			testClients := cloud.TestCloudClients{
				STS:            sts,
				AzureAKSClient: newPopulatedAKSMock(),
				EKS:            newPopulatedEKSMock(),
				GCPGKE:         newPopulatedGCPMock(),
			}

			ctx := context.Background()
			// Create and start test auth server.
			testAuthServer, err := auth.NewTestAuthServer(auth.TestAuthServerConfig{
				Dir: t.TempDir(),
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, testAuthServer.Close()) })

			tlsServer, err := testAuthServer.NewTestTLSServer()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, tlsServer.Close()) })

			// Auth client for discovery service.
			identity := auth.TestServerID(types.RoleDiscovery, "hostID")
			authClient, err := tlsServer.NewClient(identity)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, authClient.Close()) })

			for _, kubeCluster := range tc.existingKubeClusters {
				err := tlsServer.Auth().CreateKubernetesCluster(ctx, kubeCluster)
				require.NoError(t, err)
			}
			// we analyze the logs emitted by discovery service to detect clusters that were not updated
			// because their state didn't change.
			r, w := io.Pipe()
			t.Cleanup(func() {
				require.NoError(t, r.Close())
				require.NoError(t, w.Close())
			})

			logger := logrus.New()
			logger.SetOutput(w)
			logger.SetLevel(logrus.DebugLevel)
			clustersNotUpdated := make(chan string, 10)
			go func() {
				// reconcileRegexp is the regex extractor of a log message emitted by reconciler when
				// the current state of the cluster is equal to the previous.
				// [r.log.Debugf("%v %v is already registered.", new.GetKind(), new.GetName())]
				// lib/services/reconciler.go
				reconcileRegexp := regexp.MustCompile("kube_cluster (.*) is already registered")

				scanner := bufio.NewScanner(r)
				for scanner.Scan() {
					text := scanner.Text()
					// we analyze the logs emitted by discovery service to detect clusters that were not updated
					// because their state didn't change.
					if reconcileRegexp.MatchString(text) {
						result := reconcileRegexp.FindStringSubmatch(text)
						if len(result) != 2 {
							continue
						}
						clustersNotUpdated <- result[1]
					}
				}
			}()
			reporter := &mockUsageReporter{}
			tlsServer.Auth().SetUsageReporter(reporter)
			discServer, err := New(
				authz.ContextWithUser(ctx, identity.I),
				&Config{
					Clients:        &testClients,
					AccessPoint:    tlsServer.Auth(),
					AWSMatchers:    tc.awsMatchers,
					AzureMatchers:  tc.azureMatchers,
					GCPMatchers:    tc.gcpMatchers,
					Emitter:        authClient,
					Log:            logger,
					DiscoveryGroup: mainDiscoveryGroup,
				})

			require.NoError(t, err)

			t.Cleanup(func() {
				discServer.Stop()
			})
			go discServer.Start()

			clustersNotUpdatedMap := sliceToSet(tc.clustersNotUpdated)
			clustersFoundInAuth := false
			require.Eventually(t, func() bool {
			loop:
				for {
					select {
					case cluster := <-clustersNotUpdated:
						if _, ok := clustersNotUpdatedMap[cluster]; !ok {
							require.Failf(t, "expected Action for cluster %s but got no action from reconciler", cluster)
						}
						delete(clustersNotUpdatedMap, cluster)
					default:
						kubeClusters, err := tlsServer.Auth().GetKubernetesClusters(ctx)
						require.NoError(t, err)
						if len(kubeClusters) == len(tc.expectedClustersToExistInAuth) {
							c1 := types.KubeClusters(kubeClusters).ToMap()
							c2 := types.KubeClusters(tc.expectedClustersToExistInAuth).ToMap()
							for k := range c1 {
								if services.CompareResources(c1[k], c2[k]) != services.Equal {
									return false
								}
							}
							clustersFoundInAuth = true
						}
						break loop
					}
				}
				return len(clustersNotUpdated) == 0 && clustersFoundInAuth
			}, 5*time.Second, 200*time.Millisecond)

			require.ElementsMatch(t, tc.expectedAssumedRoles, sts.GetAssumedRoleARNs(), "roles incorrectly assumed")
			require.ElementsMatch(t, tc.expectedExternalIDs, sts.GetAssumedRoleExternalIDs(), "external IDs incorrectly assumed")

			if tc.wantEvents > 0 {
				require.Eventually(t, func() bool {
					return reporter.EventsCount() == tc.wantEvents
				}, time.Second, 100*time.Millisecond)
			} else {
				require.Never(t, func() bool {
					return reporter.EventsCount() != 0
				}, time.Second, 100*time.Millisecond)
			}
		})
	}
}

func TestDiscoveryServer_New(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		desc                string
		cloudClients        cloud.Clients
		matchers            []types.AWSMatcher
		errAssertion        require.ErrorAssertionFunc
		discServerAssertion require.ValueAssertionFunc
	}{
		{
			desc:         "no matchers error",
			cloudClients: &cloud.TestCloudClients{STS: &mocks.STSMock{}},
			errAssertion: func(t require.TestingT, err error, i ...interface{}) {
				require.ErrorIs(t, err, &trace.BadParameterError{Message: "no matchers configured for discovery"})
			},
			discServerAssertion: require.Nil,
		},
		{
			desc:         "success with EKS matcher",
			cloudClients: &cloud.TestCloudClients{STS: &mocks.STSMock{}, EKS: &mocks.EKSMock{}},
			matchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
					AssumeRole: &types.AssumeRole{
						RoleARN:    "arn:aws:iam::123456789012:role/teleport-role",
						ExternalID: "external-id",
					},
				},
			},
			errAssertion: require.NoError,
			discServerAssertion: func(t require.TestingT, i interface{}, i2 ...interface{}) {
				require.NotNil(t, i)
				val, ok := i.(*Server)
				require.True(t, ok)
				require.Len(t, val.kubeFetchers, 1, "unexpected amount of kube fetchers")
			},
		},
		{
			desc: "EKS fetcher is skipped on initialization error (missing region)",
			cloudClients: &cloud.TestCloudClients{
				STS: &mocks.STSMock{},
				EKS: &mocks.EKSMock{},
			},
			matchers: []types.AWSMatcher{
				{
					Types:   []string{"eks"},
					Regions: []string{},
					Tags:    map[string]utils.Strings{"env": {"prod"}},
					AssumeRole: &types.AssumeRole{
						RoleARN:    "arn:aws:iam::123456789012:role/teleport-role",
						ExternalID: "external-id",
					},
				},
				{
					Types:   []string{"eks"},
					Regions: []string{"eu-west-1"},
					Tags:    map[string]utils.Strings{"env": {"staging"}},
					AssumeRole: &types.AssumeRole{
						RoleARN:    "arn:aws:iam::55555555555:role/teleport-role",
						ExternalID: "external-id2",
					},
				},
			},
			errAssertion: require.NoError,
			discServerAssertion: func(t require.TestingT, i interface{}, i2 ...interface{}) {
				require.NotNil(t, i)
				val, ok := i.(*Server)
				require.True(t, ok)
				require.Len(t, val.kubeFetchers, 1, "unexpected amount of kube fetchers")
			},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.desc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			discServer, err := New(
				ctx,
				&Config{
					Clients:     nil,
					AccessPoint: newFakeAccessPoint(),
					AWSMatchers: tt.matchers,
					Emitter:     &mockEmitter{},
				})

			tt.errAssertion(t, err)
			tt.discServerAssertion(t, discServer)
		})
	}
}

type mockAKSAPI struct {
	azure.AKSClient
	group map[string][]*azure.AKSCluster
}

func (m *mockAKSAPI) ListAll(ctx context.Context) ([]*azure.AKSCluster, error) {
	result := make([]*azure.AKSCluster, 0, 10)
	for _, v := range m.group {
		result = append(result, v...)
	}
	return result, nil
}

func (m *mockAKSAPI) ListWithinGroup(ctx context.Context, group string) ([]*azure.AKSCluster, error) {
	return m.group[group], nil
}

func newPopulatedAKSMock() *mockAKSAPI {
	return &mockAKSAPI{
		group: aksMockClusters,
	}
}

var aksMockClusters = map[string][]*azure.AKSCluster{
	"group1": {
		{
			Name:           "aks-cluster1",
			GroupName:      "group1",
			TenantID:       "tenantID",
			Location:       "uswest1",
			SubscriptionID: "subID",
			Tags: map[string]string{
				"env":      "prod",
				"location": "uswest1",
			},
			Properties: azure.AKSClusterProperties{},
		},
		{
			Name:           "aks-cluster2",
			GroupName:      "group1",
			TenantID:       "tenantID",
			Location:       "uswest2",
			SubscriptionID: "subID",
			Tags: map[string]string{
				"env":      "prod",
				"location": "uswest1",
			},
			Properties: azure.AKSClusterProperties{},
		},
	},
	"group2": {
		{
			Name:           "aks-cluster3",
			GroupName:      "group1",
			TenantID:       "tenantID",
			Location:       "uswest1",
			SubscriptionID: "subID",
			Tags: map[string]string{
				"env":      "stg",
				"location": "uswest1",
			},
			Properties: azure.AKSClusterProperties{},
		},
		{
			Name:           "aks-cluster4",
			GroupName:      "group1",
			TenantID:       "tenantID",
			Location:       "uswest2",
			SubscriptionID: "subID",
			Tags: map[string]string{
				"env":      "stg",
				"location": "uswest1",
			},
			Properties: azure.AKSClusterProperties{},
		},
	},
}

type mockEKSAPI struct {
	eksiface.EKSAPI
	clusters []*eks.Cluster
}

func (m *mockEKSAPI) ListClustersPagesWithContext(ctx aws.Context, req *eks.ListClustersInput, f func(*eks.ListClustersOutput, bool) bool, _ ...request.Option) error {
	var names []*string
	for _, cluster := range m.clusters {
		names = append(names, cluster.Name)
	}
	f(&eks.ListClustersOutput{
		Clusters: names[:len(names)/2],
	}, false)

	f(&eks.ListClustersOutput{
		Clusters: names[len(names)/2:],
	}, true)
	return nil
}

func (m *mockEKSAPI) DescribeClusterWithContext(_ aws.Context, req *eks.DescribeClusterInput, _ ...request.Option) (*eks.DescribeClusterOutput, error) {
	for _, cluster := range m.clusters {
		if aws.StringValue(cluster.Name) == aws.StringValue(req.Name) {
			return &eks.DescribeClusterOutput{
				Cluster: cluster,
			}, nil
		}
	}
	return nil, errors.New("cluster not found")
}

func newPopulatedEKSMock() *mockEKSAPI {
	return &mockEKSAPI{
		clusters: eksMockClusters,
	}
}

var eksMockClusters = []*eks.Cluster{
	{
		Name:   aws.String("eks-cluster1"),
		Arn:    aws.String("arn:aws:eks:eu-west-1:accountID:cluster/cluster1"),
		Status: aws.String(eks.ClusterStatusActive),
		Tags: map[string]*string{
			"env":      aws.String("prod"),
			"location": aws.String("eu-west-1"),
		},
	},
	{
		Name:   aws.String("eks-cluster2"),
		Arn:    aws.String("arn:aws:eks:eu-west-1:accountID:cluster/cluster2"),
		Status: aws.String(eks.ClusterStatusActive),
		Tags: map[string]*string{
			"env":      aws.String("prod"),
			"location": aws.String("eu-west-1"),
		},
	},

	{
		Name:   aws.String("eks-cluster3"),
		Arn:    aws.String("arn:aws:eks:eu-west-1:accountID:cluster/cluster3"),
		Status: aws.String(eks.ClusterStatusActive),
		Tags: map[string]*string{
			"env":      aws.String("stg"),
			"location": aws.String("eu-west-1"),
		},
	},
	{
		Name:   aws.String("eks-cluster4"),
		Arn:    aws.String("arn:aws:eks:eu-west-1:accountID:cluster/cluster1"),
		Status: aws.String(eks.ClusterStatusActive),
		Tags: map[string]*string{
			"env":      aws.String("stg"),
			"location": aws.String("eu-west-1"),
		},
	},
}

func mustConvertEKSToKubeCluster(t *testing.T, eksCluster *eks.Cluster, discoveryGroup string) types.KubeCluster {
	cluster, err := services.NewKubeClusterFromAWSEKS(eksCluster)
	require.NoError(t, err)
	cluster.GetStaticLabels()[types.TeleportInternalDiscoveryGroupName] = discoveryGroup
	cluster.SetOrigin(types.OriginCloud)
	return cluster
}

func mustConvertAKSToKubeCluster(t *testing.T, azureCluster *azure.AKSCluster, discoveryGroup string) types.KubeCluster {
	cluster, err := services.NewKubeClusterFromAzureAKS(azureCluster)
	require.NoError(t, err)
	cluster.GetStaticLabels()[types.TeleportInternalDiscoveryGroupName] = discoveryGroup
	cluster.SetOrigin(types.OriginCloud)
	return cluster
}

func modifyKubeCluster(cluster types.KubeCluster) types.KubeCluster {
	cluster.GetStaticLabels()["test"] = "test"
	return cluster
}

func sliceToSet[T comparable](slice []T) map[T]struct{} {
	set := map[T]struct{}{}
	for _, v := range slice {
		set[v] = struct{}{}
	}
	return set
}

func newPopulatedGCPMock() *mockGKEAPI {
	return &mockGKEAPI{
		clusters: gkeMockClusters,
	}
}

var gkeMockClusters = []gcp.GKECluster{
	{
		Name:   "cluster1",
		Status: containerpb.Cluster_RUNNING,
		Labels: map[string]string{
			"env":      "prod",
			"location": "central-1",
		},
		ProjectID:   "p1",
		Location:    "central-1",
		Description: "desc1",
	},
	{
		Name:   "cluster2",
		Status: containerpb.Cluster_RUNNING,
		Labels: map[string]string{
			"env":      "prod",
			"location": "central-1",
		},
		ProjectID:   "p1",
		Location:    "central-1",
		Description: "desc1",
	},
	{
		Name:   "cluster3",
		Status: containerpb.Cluster_RUNNING,
		Labels: map[string]string{
			"env":      "stg",
			"location": "central-1",
		},
		ProjectID:   "p1",
		Location:    "central-1",
		Description: "desc1",
	},
	{
		Name:   "cluster4",
		Status: containerpb.Cluster_RUNNING,
		Labels: map[string]string{
			"env":      "stg",
			"location": "central-1",
		},
		ProjectID:   "p1",
		Location:    "central-1",
		Description: "desc1",
	},
}

func mustConvertGKEToKubeCluster(t *testing.T, gkeCluster gcp.GKECluster, discoveryGroup string) types.KubeCluster {
	cluster, err := services.NewKubeClusterFromGCPGKE(gkeCluster)
	require.NoError(t, err)
	cluster.GetStaticLabels()[types.TeleportInternalDiscoveryGroupName] = discoveryGroup
	cluster.SetOrigin(types.OriginCloud)
	return cluster
}

type mockGKEAPI struct {
	gcp.GKEClient
	clusters []gcp.GKECluster
}

func (m *mockGKEAPI) ListClusters(ctx context.Context, projectID string, location string) ([]gcp.GKECluster, error) {
	return m.clusters, nil
}

func TestDiscoveryDatabase(t *testing.T) {
	const (
		mainDiscoveryGroup = "main"
	)
	awsRedshiftResource, awsRedshiftDB := makeRedshiftCluster(t, "aws-redshift", "us-east-1", mainDiscoveryGroup)
	awsRDSInstance, awsRDSDB := makeRDSInstance(t, "aws-rds", "us-west-1", mainDiscoveryGroup)
	azRedisResource, azRedisDB := makeAzureRedisServer(t, "az-redis", "sub1", "group1", "East US", mainDiscoveryGroup)

	role := types.AssumeRole{RoleARN: "arn:aws:iam::123456789012:role/test-role", ExternalID: "test123"}
	awsRDSDBWithRole := awsRDSDB.Copy()
	awsRDSDBWithRole.SetAWSAssumeRole("arn:aws:iam::123456789012:role/test-role")
	awsRDSDBWithRole.SetAWSExternalID("test123")

	testClients := &cloud.TestCloudClients{
		STS: &mocks.STSMock{},
		RDS: &mocks.RDSMock{
			DBInstances: []*rds.DBInstance{awsRDSInstance},
			DBEngineVersions: []*rds.DBEngineVersion{
				{Engine: aws.String(services.RDSEnginePostgres)},
			},
		},
		Redshift: &mocks.RedshiftMock{
			Clusters: []*redshift.Cluster{awsRedshiftResource},
		},
		AzureRedis: azure.NewRedisClientByAPI(&azure.ARMRedisMock{
			Servers: []*armredis.ResourceInfo{azRedisResource},
		}),
		AzureRedisEnterprise: azure.NewRedisEnterpriseClientByAPI(
			&azure.ARMRedisEnterpriseClusterMock{},
			&azure.ARMRedisEnterpriseDatabaseMock{},
		),
	}

	tcs := []struct {
		name              string
		existingDatabases []types.Database
		awsMatchers       []types.AWSMatcher
		azureMatchers     []types.AzureMatcher
		expectDatabases   []types.Database
		wantEvents        int
	}{
		{
			name: "discover AWS database",
			awsMatchers: []types.AWSMatcher{{
				Types:   []string{services.AWSMatcherRedshift},
				Tags:    map[string]utils.Strings{types.Wildcard: {types.Wildcard}},
				Regions: []string{"us-east-1"},
			}},
			expectDatabases: []types.Database{awsRedshiftDB},
			wantEvents:      1,
		},
		{
			name: "discover AWS database with assumed role",
			awsMatchers: []types.AWSMatcher{{
				Types:      []string{services.AWSMatcherRDS},
				Tags:       map[string]utils.Strings{types.Wildcard: {types.Wildcard}},
				Regions:    []string{"us-west-1"},
				AssumeRole: &role,
			}},
			expectDatabases: []types.Database{awsRDSDBWithRole},
			wantEvents:      1,
		},
		{
			name: "discover Azure database",
			azureMatchers: []types.AzureMatcher{{
				Types:          []string{services.AzureMatcherRedis},
				ResourceTags:   map[string]utils.Strings{types.Wildcard: {types.Wildcard}},
				Regions:        []string{types.Wildcard},
				ResourceGroups: []string{types.Wildcard},
				Subscriptions:  []string{"sub1"},
			}},
			expectDatabases: []types.Database{azRedisDB},
			wantEvents:      1,
		},
		{
			name: "update existing database",
			existingDatabases: []types.Database{
				mustNewDatabase(t, types.Metadata{
					Name:        awsRedshiftDB.GetName(),
					Description: "should be updated",
					Labels:      map[string]string{types.OriginLabel: types.OriginCloud, types.TeleportInternalDiscoveryGroupName: mainDiscoveryGroup},
				}, types.DatabaseSpecV3{
					Protocol: "redis",
					URI:      "should.be.updated.com:12345",
					AWS: types.AWS{
						Redshift: types.Redshift{
							ClusterID: "aws-redshift",
						},
					},
				}),
			},
			awsMatchers: []types.AWSMatcher{{
				Types:   []string{services.AWSMatcherRedshift},
				Tags:    map[string]utils.Strings{types.Wildcard: {types.Wildcard}},
				Regions: []string{"us-east-1"},
			}},
			expectDatabases: []types.Database{awsRedshiftDB},
		},
		{
			name: "update existing database with assumed role",
			existingDatabases: []types.Database{
				mustNewDatabase(t, types.Metadata{
					Name:        awsRDSDBWithRole.GetName(),
					Description: "should be updated",
					Labels:      map[string]string{types.OriginLabel: types.OriginCloud, types.TeleportInternalDiscoveryGroupName: mainDiscoveryGroup},
				}, types.DatabaseSpecV3{
					Protocol: "postgres",
					URI:      "should.be.updated.com:12345",
				}),
			},
			awsMatchers: []types.AWSMatcher{{
				Types:      []string{services.AWSMatcherRDS},
				Tags:       map[string]utils.Strings{types.Wildcard: {types.Wildcard}},
				Regions:    []string{"us-west-1"},
				AssumeRole: &role,
			}},
			expectDatabases: []types.Database{awsRDSDBWithRole},
		},
		{
			name: "delete existing database",
			existingDatabases: []types.Database{
				mustNewDatabase(t, types.Metadata{
					Name:        awsRedshiftDB.GetName(),
					Description: "should not be deleted",
					Labels:      map[string]string{types.OriginLabel: types.OriginCloud},
				}, types.DatabaseSpecV3{
					Protocol: "redis",
					URI:      "should.not.be.deleted.com:12345",
				}),
			},
			awsMatchers: []types.AWSMatcher{{
				Types:   []string{services.AWSMatcherRedshift},
				Tags:    map[string]utils.Strings{"do-not-match": {"do-not-match"}},
				Regions: []string{"us-east-1"},
			}},
			expectDatabases: []types.Database{
				mustNewDatabase(t, types.Metadata{
					Name:        awsRedshiftDB.GetName(),
					Description: "should not be deleted",
					Labels:      map[string]string{types.OriginLabel: types.OriginCloud},
				}, types.DatabaseSpecV3{
					Protocol: "redis",
					URI:      "should.not.be.deleted.com:12345",
				}),
			},
		},
		{
			name: "skip self-hosted database",
			existingDatabases: []types.Database{
				mustNewDatabase(t, types.Metadata{
					Name:        "self-hosted",
					Description: "should be ignored (not deleted)",
					Labels:      map[string]string{types.OriginLabel: types.OriginConfigFile},
				}, types.DatabaseSpecV3{
					Protocol: "mysql",
					URI:      "localhost:12345",
				}),
			},
			awsMatchers: []types.AWSMatcher{{
				Types:   []string{services.AWSMatcherRedshift},
				Tags:    map[string]utils.Strings{"do-not-match": {"do-not-match"}},
				Regions: []string{"us-east-1"},
			}},
			expectDatabases: []types.Database{
				mustNewDatabase(t, types.Metadata{
					Name:        "self-hosted",
					Description: "should be ignored (not deleted)",
					Labels:      map[string]string{types.OriginLabel: types.OriginConfigFile},
				}, types.DatabaseSpecV3{
					Protocol: "mysql",
					URI:      "localhost:12345",
				}),
			},
		},
	}

	for _, tc := range tcs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			// Create and start test auth server.
			testAuthServer, err := auth.NewTestAuthServer(auth.TestAuthServerConfig{
				Dir: t.TempDir(),
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, testAuthServer.Close()) })

			tlsServer, err := testAuthServer.NewTestTLSServer()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, tlsServer.Close()) })

			// Auth client for discovery service.
			identity := auth.TestServerID(types.RoleDiscovery, "hostID")
			authClient, err := tlsServer.NewClient(identity)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, authClient.Close()) })

			for _, database := range tc.existingDatabases {
				err := tlsServer.Auth().CreateDatabase(ctx, database)
				require.NoError(t, err)
			}

			waitForReconcile := make(chan struct{})
			reporter := &mockUsageReporter{}
			tlsServer.Auth().SetUsageReporter(reporter)
			srv, err := New(
				authz.ContextWithUser(ctx, identity.I),
				&Config{
					Clients:       testClients,
					AccessPoint:   tlsServer.Auth(),
					AWSMatchers:   tc.awsMatchers,
					AzureMatchers: tc.azureMatchers,
					Emitter:       authClient,
					onDatabaseReconcile: func() {
						waitForReconcile <- struct{}{}
					},
					DiscoveryGroup: mainDiscoveryGroup,
				})

			require.NoError(t, err)

			t.Cleanup(srv.Stop)
			go srv.Start()

			select {
			case <-waitForReconcile:
				// Use tlsServer.Auth() instead of authClient to compare
				// databases stored in auth. authClient was created with
				// types.RoleDiscovery and it does not have permissions to
				// access non-cloud databases.
				actualDatabases, err := tlsServer.Auth().GetDatabases(ctx)
				require.NoError(t, err)
				require.Empty(t, cmp.Diff(tc.expectDatabases, actualDatabases,
					cmpopts.IgnoreFields(types.Metadata{}, "ID"),
					cmpopts.IgnoreFields(types.DatabaseStatusV3{}, "CACert"),
				))
			case <-time.After(time.Second):
				t.Fatal("Didn't receive reconcile event after 1s")
			}

			if tc.wantEvents > 0 {
				require.Eventually(t, func() bool {
					return reporter.EventsCount() == tc.wantEvents
				}, time.Second, 100*time.Millisecond)
			} else {
				require.Never(t, func() bool {
					return reporter.EventsCount() != 0
				}, time.Second, 100*time.Millisecond)
			}
		})
	}
}

func makeRDSInstance(t *testing.T, name, region string, discoveryGroup string) (*rds.DBInstance, types.Database) {
	instance := &rds.DBInstance{
		DBInstanceArn:        aws.String(fmt.Sprintf("arn:aws:rds:%v:123456789012:db:%v", region, name)),
		DBInstanceIdentifier: aws.String(name),
		DbiResourceId:        aws.String(uuid.New().String()),
		Engine:               aws.String(services.RDSEnginePostgres),
		DBInstanceStatus:     aws.String("available"),
		Endpoint: &rds.Endpoint{
			Address: aws.String("localhost"),
			Port:    aws.Int64(5432),
		},
	}
	database, err := services.NewDatabaseFromRDSInstance(instance)
	require.NoError(t, err)
	database.SetOrigin(types.OriginCloud)
	staticLabels := database.GetStaticLabels()
	staticLabels[types.TeleportInternalDiscoveryGroupName] = discoveryGroup
	database.SetStaticLabels(staticLabels)
	return instance, database
}

func makeRedshiftCluster(t *testing.T, name, region string, discoveryGroup string) (*redshift.Cluster, types.Database) {
	t.Helper()
	cluster := &redshift.Cluster{
		ClusterIdentifier:   aws.String(name),
		ClusterNamespaceArn: aws.String(fmt.Sprintf("arn:aws:redshift:%s:123456789012:namespace:%s", region, name)),
		ClusterStatus:       aws.String("available"),
		Endpoint: &redshift.Endpoint{
			Address: aws.String("localhost"),
			Port:    aws.Int64(5439),
		},
	}

	database, err := services.NewDatabaseFromRedshiftCluster(cluster)
	require.NoError(t, err)
	database.SetOrigin(types.OriginCloud)
	staticLabels := database.GetStaticLabels()
	staticLabels[types.TeleportInternalDiscoveryGroupName] = discoveryGroup
	database.SetStaticLabels(staticLabels)
	return cluster, database
}

func makeAzureRedisServer(t *testing.T, name, subscription, group, region string, discoveryGroup string) (*armredis.ResourceInfo, types.Database) {
	t.Helper()
	resourceInfo := &armredis.ResourceInfo{
		Name:     to.Ptr(name),
		ID:       to.Ptr(fmt.Sprintf("/subscriptions/%v/resourceGroups/%v/providers/Microsoft.Cache/Redis/%v", subscription, group, name)),
		Location: to.Ptr(region),
		Properties: &armredis.Properties{
			HostName:          to.Ptr(fmt.Sprintf("%v.redis.cache.windows.net", name)),
			SSLPort:           to.Ptr(int32(6380)),
			ProvisioningState: to.Ptr(armredis.ProvisioningStateSucceeded),
		},
	}

	database, err := services.NewDatabaseFromAzureRedis(resourceInfo)
	require.NoError(t, err)
	database.SetOrigin(types.OriginCloud)
	staticLabels := database.GetStaticLabels()
	staticLabels[types.TeleportInternalDiscoveryGroupName] = discoveryGroup
	database.SetStaticLabels(staticLabels)
	return resourceInfo, database
}

func mustNewDatabase(t *testing.T, meta types.Metadata, spec types.DatabaseSpecV3) types.Database {
	t.Helper()
	database, err := types.NewDatabaseV3(meta, spec)
	require.NoError(t, err)
	return database
}

type mockAzureRunCommandClient struct{}

func (m *mockAzureRunCommandClient) Run(_ context.Context, _ azure.RunCommandRequest) error {
	return nil
}

type mockAzureClient struct {
	vms []*armcompute.VirtualMachine
}

func (m *mockAzureClient) Get(_ context.Context, _ string) (*azure.VirtualMachine, error) {
	return nil, nil
}

func (m *mockAzureClient) GetByVMID(_ context.Context, _, _ string) (*azure.VirtualMachine, error) {
	return nil, nil
}

func (m *mockAzureClient) ListVirtualMachines(_ context.Context, _ string) ([]*armcompute.VirtualMachine, error) {
	return m.vms, nil
}

type mockAzureInstaller struct {
	mu                 sync.Mutex
	installedInstances []string
}

func (m *mockAzureInstaller) Run(_ context.Context, req server.AzureRunRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range req.Instances {
		m.installedInstances = append(m.installedInstances, *inst.Name)
	}
	return nil
}

func (m *mockAzureInstaller) GetInstalledInstances() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.installedInstances
}

func TestAzureVMDiscovery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                   string
		presentVMs             []types.Server
		foundAzureVMs          []*armcompute.VirtualMachine
		wantInstalledInstances []string
	}{
		{
			name:       "no nodes present, 1 found",
			presentVMs: []types.Server{},
			foundAzureVMs: []*armcompute.VirtualMachine{
				{
					ID: aws.String((&arm.ResourceID{
						SubscriptionID:    "testsub",
						ResourceGroupName: "rg",
						Name:              "testvm",
					}).String()),
					Name:     aws.String("testvm"),
					Location: aws.String("westcentralus"),
					Tags: map[string]*string{
						"teleport": aws.String("yes"),
					},
					Properties: &armcompute.VirtualMachineProperties{
						VMID: aws.String("test-vmid"),
					},
				},
			},
			wantInstalledInstances: []string{"testvm"},
		},
		{
			name: "nodes present, instance filtered",
			presentVMs: []types.Server{
				&types.ServerV2{
					Kind: types.KindNode,
					Metadata: types.Metadata{
						Name: "name",
						Labels: map[string]string{
							"teleport.internal/subscription-id": "testsub",
							"teleport.internal/vm-id":           "test-vmid",
						},
						Namespace: defaults.Namespace,
					},
				},
			},
			foundAzureVMs: []*armcompute.VirtualMachine{
				{
					ID: aws.String((&arm.ResourceID{
						SubscriptionID:    "testsub",
						ResourceGroupName: "rg",
						Name:              "testvm",
					}).String()),
					Location: aws.String("westcentralus"),
					Tags: map[string]*string{
						"teleport": aws.String("yes"),
					},
					Properties: &armcompute.VirtualMachineProperties{
						VMID: aws.String("test-vmid"),
					},
				},
			},
		},
		{
			name: "nodes present, instance not filtered",
			presentVMs: []types.Server{
				&types.ServerV2{
					Kind: types.KindNode,
					Metadata: types.Metadata{
						Name: "name",
						Labels: map[string]string{
							"teleport.internal/subscription-id": "testsub",
							"teleport.internal/vm-id":           "alternate-vmid",
						},
						Namespace: defaults.Namespace,
					},
				},
			},
			foundAzureVMs: []*armcompute.VirtualMachine{
				{
					ID: aws.String((&arm.ResourceID{
						SubscriptionID:    "testsub",
						ResourceGroupName: "rg",
						Name:              "testvm",
					}).String()),
					Name:     aws.String("testvm"),
					Location: aws.String("westcentralus"),
					Tags: map[string]*string{
						"teleport": aws.String("yes"),
					},
					Properties: &armcompute.VirtualMachineProperties{
						VMID: aws.String("test-vmid"),
					},
				},
			},
			wantInstalledInstances: []string{"testvm"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClients := cloud.TestCloudClients{
				AzureVirtualMachines: &mockAzureClient{
					vms: tc.foundAzureVMs,
				},
				AzureRunCommand: &mockAzureRunCommandClient{},
			}

			ctx := context.Background()
			testAuthServer, err := auth.NewTestAuthServer(auth.TestAuthServerConfig{
				Dir: t.TempDir(),
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, testAuthServer.Close()) })

			tlsServer, err := testAuthServer.NewTestTLSServer()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, tlsServer.Close()) })

			// Auth client for discovery service.
			identity := auth.TestServerID(types.RoleDiscovery, "hostID")
			authClient, err := tlsServer.NewClient(identity)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, authClient.Close()) })

			for _, instance := range tc.presentVMs {
				_, err := tlsServer.Auth().UpsertNode(ctx, instance)
				require.NoError(t, err)
			}

			logger := logrus.New()
			emitter := &mockEmitter{}
			installer := &mockAzureInstaller{}
			reporter := &mockUsageReporter{}
			tlsServer.Auth().SetUsageReporter(reporter)
			server, err := New(authz.ContextWithUser(context.Background(), identity.I), &Config{
				Clients:     &testClients,
				AccessPoint: tlsServer.Auth(),
				AzureMatchers: []types.AzureMatcher{{
					Types:          []string{"vm"},
					Subscriptions:  []string{"testsub"},
					ResourceGroups: []string{"testrg"},
					Regions:        []string{"westcentralus"},
					ResourceTags:   types.Labels{"teleport": {"yes"}},
				}},
				Emitter: emitter,
				Log:     logger,
			})

			require.NoError(t, err)
			server.azureInstaller = installer
			emitter.server = server
			emitter.t = t

			r, w := io.Pipe()
			t.Cleanup(func() {
				require.NoError(t, r.Close())
				require.NoError(t, w.Close())
			})

			go server.Start()
			t.Cleanup(server.Stop)

			if len(tc.wantInstalledInstances) > 0 {
				require.Eventually(t, func() bool {
					instances := installer.GetInstalledInstances()
					slices.Sort(instances)
					return slices.Equal(tc.wantInstalledInstances, instances) && reporter.EventsCount() == len(tc.wantInstalledInstances)
				}, 500*time.Millisecond, 50*time.Millisecond)
			} else {
				require.Never(t, func() bool {
					return len(installer.GetInstalledInstances()) > 0 || reporter.EventsCount() > 0
				}, 500*time.Millisecond, 50*time.Millisecond)
			}
		})

	}
}

type mockGCPClient struct {
	vms []*gcp.Instance
}

func (m *mockGCPClient) ListInstances(_ context.Context, _, _ string) ([]*gcp.Instance, error) {
	return m.vms, nil
}

func (m *mockGCPClient) StreamInstances(_ context.Context, _, _ string) stream.Stream[*gcp.Instance] {
	return stream.Slice(m.vms)
}

func (m *mockGCPClient) GetInstance(_ context.Context, _ *gcp.InstanceRequest) (*gcp.Instance, error) {
	return nil, trace.NotFound("disabled for test")
}

func (m *mockGCPClient) AddSSHKey(_ context.Context, _ *gcp.SSHKeyRequest) error {
	return nil
}

func (m *mockGCPClient) RemoveSSHKey(_ context.Context, _ *gcp.SSHKeyRequest) error {
	return nil
}

type mockGCPInstaller struct {
	mu                 sync.Mutex
	installedInstances []string
}

func (m *mockGCPInstaller) Run(_ context.Context, req server.GCPRunRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range req.Instances {
		m.installedInstances = append(m.installedInstances, inst.Name)
	}
	return nil
}

func (m *mockGCPInstaller) GetInstalledInstances() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.installedInstances
}

func TestGCPVMDiscovery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                   string
		presentVMs             []types.Server
		foundGCPVMs            []*gcp.Instance
		wantInstalledInstances []string
	}{
		{
			name:       "no nodes present, 1 found",
			presentVMs: []types.Server{},
			foundGCPVMs: []*gcp.Instance{
				{
					ProjectID: "myproject",
					Zone:      "myzone",
					Name:      "myinstance",
					Labels: map[string]string{
						"teleport": "yes",
					},
				},
			},
			wantInstalledInstances: []string{"myinstance"},
		},
		{
			name: "nodes present, instance filtered",
			presentVMs: []types.Server{
				&types.ServerV2{
					Kind: types.KindNode,
					Metadata: types.Metadata{
						Name: "name",
						Labels: map[string]string{
							"teleport.internal/project-id": "myproject",
							"teleport.internal/zone":       "myzone",
							"teleport.internal/name":       "myinstance",
						},
						Namespace: defaults.Namespace,
					},
				},
			},
			foundGCPVMs: []*gcp.Instance{
				{
					ProjectID: "myproject",
					Zone:      "myzone",
					Name:      "myinstance",
					Labels: map[string]string{
						"teleport": "yes",
					},
				},
			},
		},
		{
			name: "nodes present, instance not filtered",
			presentVMs: []types.Server{
				&types.ServerV2{
					Kind: types.KindNode,
					Metadata: types.Metadata{
						Name: "name",
						Labels: map[string]string{
							"teleport.internal/project-id": "myproject",
							"teleport.internal/zone":       "myzone",
							"teleport.internal/name":       "myotherinstance",
						},
						Namespace: defaults.Namespace,
					},
				},
			},
			foundGCPVMs: []*gcp.Instance{
				{
					ProjectID: "myproject",
					Zone:      "myzone",
					Name:      "myinstance",
					Labels: map[string]string{
						"teleport": "yes",
					},
				},
			},
			wantInstalledInstances: []string{"myinstance"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClients := cloud.TestCloudClients{
				GCPInstances: &mockGCPClient{
					vms: tc.foundGCPVMs,
				},
			}

			ctx := context.Background()
			testAuthServer, err := auth.NewTestAuthServer(auth.TestAuthServerConfig{
				Dir: t.TempDir(),
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, testAuthServer.Close()) })

			tlsServer, err := testAuthServer.NewTestTLSServer()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, tlsServer.Close()) })

			// Auth client for discovery service.
			identity := auth.TestServerID(types.RoleDiscovery, "hostID")
			authClient, err := tlsServer.NewClient(identity)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, authClient.Close()) })

			for _, instance := range tc.presentVMs {
				_, err := tlsServer.Auth().UpsertNode(ctx, instance)
				require.NoError(t, err)
			}

			logger := logrus.New()
			emitter := &mockEmitter{}
			installer := &mockGCPInstaller{}
			reporter := &mockUsageReporter{}
			tlsServer.Auth().SetUsageReporter(reporter)
			server, err := New(authz.ContextWithUser(context.Background(), identity.I), &Config{
				Clients:     &testClients,
				AccessPoint: tlsServer.Auth(),
				GCPMatchers: []types.GCPMatcher{{
					Types:      []string{"gce"},
					ProjectIDs: []string{"myproject"},
					Locations:  []string{"myzone"},
					Labels:     types.Labels{"teleport": {"yes"}},
				}},
				Emitter: emitter,
				Log:     logger,
			})

			require.NoError(t, err)
			server.gcpInstaller = installer
			emitter.server = server
			emitter.t = t

			r, w := io.Pipe()
			t.Cleanup(func() {
				require.NoError(t, r.Close())
				require.NoError(t, w.Close())
			})

			go server.Start()
			t.Cleanup(server.Stop)

			if len(tc.wantInstalledInstances) > 0 {
				require.Eventually(t, func() bool {
					instances := installer.GetInstalledInstances()
					slices.Sort(instances)
					return slices.Equal(tc.wantInstalledInstances, instances) && reporter.EventsCount() == len(tc.wantInstalledInstances)
				}, 500*time.Millisecond, 50*time.Millisecond)
			} else {
				require.Never(t, func() bool {
					return len(installer.GetInstalledInstances()) > 0 || reporter.EventsCount() > 0
				}, 500*time.Millisecond, 50*time.Millisecond)
			}

		})
	}
}

// TestServer_onCreate tests the update of the discovery_group of a resource
// when it differs from the one in the database.
// TODO(tigrato): DELETE in 14.0.0
func TestServer_onCreate(t *testing.T) {
	_, awsRedshiftDB := makeRedshiftCluster(t, "aws-redshift", "us-east-1", "test")
	accessPoint := &fakeAccessPoint{}
	s := &Server{
		Config: &Config{
			AccessPoint: accessPoint,
			Log:         logrus.New(),
		},
	}

	t.Run("onCreate update kube", func(t *testing.T) {
		err := s.onKubeCreate(context.Background(), mustConvertEKSToKubeCluster(t, eksMockClusters[0], "test-cluster"))
		require.NoError(t, err)
		require.True(t, accessPoint.updateKube)
	})

	t.Run("onCreate update database", func(t *testing.T) {
		err := s.onDatabaseCreate(context.Background(), awsRedshiftDB)
		require.NoError(t, err)
		require.True(t, accessPoint.updateDatabase)
	})
}

func TestEmitUsageEvents(t *testing.T) {
	t.Parallel()
	testClients := cloud.TestCloudClients{
		AzureVirtualMachines: &mockAzureClient{},
		AzureRunCommand:      &mockAzureRunCommandClient{},
	}
	testAuthServer, err := auth.NewTestAuthServer(auth.TestAuthServerConfig{
		Dir: t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, testAuthServer.Close()) })

	tlsServer, err := testAuthServer.NewTestTLSServer()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tlsServer.Close()) })

	// Auth client for discovery service.
	identity := auth.TestServerID(types.RoleDiscovery, "hostID")
	authClient, err := tlsServer.NewClient(identity)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, authClient.Close()) })

	reporter := &mockUsageReporter{}
	tlsServer.Auth().SetUsageReporter(reporter)

	server, err := New(authz.ContextWithUser(context.Background(), identity.I), &Config{
		Clients:     &testClients,
		AccessPoint: tlsServer.Auth(),
		AzureMatchers: []types.AzureMatcher{{
			Types:          []string{"vm"},
			Subscriptions:  []string{"testsub"},
			ResourceGroups: []string{"testrg"},
			Regions:        []string{"westcentralus"},
			ResourceTags:   types.Labels{"teleport": {"yes"}},
		}},
		Emitter: &mockEmitter{},
	})
	require.NoError(t, err)

	require.Equal(t, 0, reporter.EventsCount())
	// Check that events are emitted for new instances.
	event := &usageeventsv1.ResourceCreateEvent{}
	require.NoError(t, server.emitUsageEvents(map[string]*usageeventsv1.ResourceCreateEvent{
		"inst1": event,
		"inst2": event,
	}))
	require.Equal(t, 2, reporter.EventsCount())
	// Check that events for duplicate instances are discarded.
	require.NoError(t, server.emitUsageEvents(map[string]*usageeventsv1.ResourceCreateEvent{
		"inst1": event,
		"inst3": event,
	}))
	require.Equal(t, 3, reporter.EventsCount())
}

type fakeAccessPoint struct {
	auth.DiscoveryAccessPoint
	updateKube     bool
	updateDatabase bool

	upsertedServerInfos chan types.ServerInfo
}

func newFakeAccessPoint() *fakeAccessPoint {
	return &fakeAccessPoint{
		upsertedServerInfos: make(chan types.ServerInfo),
	}
}

func (f *fakeAccessPoint) CreateDatabase(ctx context.Context, database types.Database) error {
	return trace.AlreadyExists("already exists")
}

func (f *fakeAccessPoint) UpdateDatabase(ctx context.Context, database types.Database) error {
	f.updateDatabase = true
	return nil
}

func (f *fakeAccessPoint) CreateKubernetesCluster(ctx context.Context, cluster types.KubeCluster) error {
	return trace.AlreadyExists("already exists")
}

// UpdateKubernetesCluster updates existing kubernetes cluster resource.
func (f *fakeAccessPoint) UpdateKubernetesCluster(ctx context.Context, cluster types.KubeCluster) error {
	f.updateKube = true
	return nil
}

func (f *fakeAccessPoint) UpsertServerInfo(ctx context.Context, si types.ServerInfo) error {
	f.upsertedServerInfos <- si
	return nil
}

func (f *fakeAccessPoint) NewWatcher(ctx context.Context, watch types.Watch) (types.Watcher, error) {
	return newFakeWatcher(), nil
}

type fakeWatcher struct {
}

func newFakeWatcher() fakeWatcher {

	return fakeWatcher{}
}

func (m fakeWatcher) Events() <-chan types.Event {
	return make(chan types.Event)
}

func (m fakeWatcher) Done() <-chan struct{} {
	return make(chan struct{})
}

func (m fakeWatcher) Close() error {
	return nil
}

func (m fakeWatcher) Error() error {
	return nil
}
