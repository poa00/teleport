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
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/auth/authclient"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/utils"
)

func TestMain(m *testing.M) {
	utils.InitLogger(utils.LoggingForCLI, slog.LevelDebug)
	os.Exit(m.Run())
}

type testPack struct {
	vnetIPv6Prefix tcpip.Address
	dnsIPv6        tcpip.Address
	manager        *Manager

	testStack        *stack.Stack
	testLinkEndpoint *channel.Endpoint
	localAddress     tcpip.Address
}

func newTestPack(t *testing.T, ctx context.Context, clock clockwork.FakeClock, appProvider AppProvider) *testPack {
	// Create two sides of an emulated TUN interface: writes to one can be read on the other, and vice versa.
	tun1, tun2 := newSplitTUN()

	// Create an isolated userspace networking stack that can be manipulated from test code. It will be
	// connected to the VNet over the TUN interface. This emulates the host networking stack.
	// This is a completely separate gvisor network stack than the one that will be created in NewManager -
	// the two will be connected over a fake TUN interface. This exists so that the test can setup IP routes
	// without affecting the host running the Test.
	testStack, testLinkEndpoint, err := createStack()
	require.NoError(t, err)

	errIsOK := func(err error) bool {
		return err == nil || errors.Is(err, context.Canceled) || utils.IsOKNetworkError(err) || errors.Is(err, errFakeTUNClosed)
	}

	utils.RunTestBackgroundTask(ctx, t, &utils.TestBackgroundTask{
		Name: "test network stack",
		Task: func(ctx context.Context) error {
			if err := forwardBetweenTunAndNetstack(ctx, tun1, testLinkEndpoint); !errIsOK(err) {
				return trace.Wrap(err)
			}
			return nil
		},
		Terminate: func() error {
			testLinkEndpoint.Close()
			return trace.Wrap(tun1.Close())
		},
	})

	// Assign a local IP address to the test stack.
	localAddress, err := randomULAAddress()
	require.NoError(t, err)
	protocolAddr, err := protocolAddress(localAddress)
	require.NoError(t, err)
	tcpErr := testStack.AddProtocolAddress(nicID, protocolAddr, stack.AddressProperties{})
	require.Nil(t, tcpErr)

	// Route the VNet range to the TUN interface - this emulates the route that will be installed on the host.
	vnetIPv6Prefix, err := IPv6Prefix()
	require.NoError(t, err)
	subnet, err := tcpip.NewSubnet(vnetIPv6Prefix, tcpip.MaskFromBytes(net.CIDRMask(64, 128)))
	require.NoError(t, err)
	testStack.SetRouteTable([]tcpip.Route{{
		Destination: subnet,
		NIC:         nicID,
	}})

	dnsIPv6 := ipv6WithSuffix(vnetIPv6Prefix, []byte{2})

	tcpAppResolver := NewTCPAppResolver(appProvider, withClock(clock))

	// Create the VNet and connect it to the other side of the TUN.
	manager, err := NewManager(&Config{
		TUNDevice:                tun2,
		IPv6Prefix:               vnetIPv6Prefix,
		DNSIPv6:                  dnsIPv6,
		TCPHandlerResolver:       tcpAppResolver,
		upstreamNameserverSource: noUpstreamNameservers{},
	})
	require.NoError(t, err)

	utils.RunTestBackgroundTask(ctx, t, &utils.TestBackgroundTask{
		Name: "VNet",
		Task: func(ctx context.Context) error {
			if err := manager.Run(ctx); !errIsOK(err) {
				return trace.Wrap(err)
			}
			return nil
		},
	})

	return &testPack{
		vnetIPv6Prefix:   vnetIPv6Prefix,
		dnsIPv6:          dnsIPv6,
		manager:          manager,
		testStack:        testStack,
		testLinkEndpoint: testLinkEndpoint,
		localAddress:     localAddress,
	}
}

// dialIPPort dials the VNet address [addr] from the test virtual netstack.
func (p *testPack) dialIPPort(ctx context.Context, addr tcpip.Address, port uint16) (*gonet.TCPConn, error) {
	conn, err := gonet.DialTCPWithBind(
		ctx,
		p.testStack,
		tcpip.FullAddress{
			NIC:      nicID,
			Addr:     p.localAddress,
			LinkAddr: p.testLinkEndpoint.LinkAddress(),
		},
		tcpip.FullAddress{
			NIC:      nicID,
			Addr:     addr,
			Port:     port,
			LinkAddr: p.manager.linkEndpoint.LinkAddress(),
		},
		ipv6.ProtocolNumber,
	)
	return conn, trace.Wrap(err)
}

func (p *testPack) dialUDP(ctx context.Context, addr tcpip.Address, port uint16) (net.Conn, error) {
	conn, err := gonet.DialUDP(
		p.testStack,
		&tcpip.FullAddress{
			NIC:      nicID,
			Addr:     p.localAddress,
			LinkAddr: p.testLinkEndpoint.LinkAddress(),
		},
		&tcpip.FullAddress{
			NIC:      nicID,
			Addr:     addr,
			Port:     port,
			LinkAddr: p.manager.linkEndpoint.LinkAddress(),
		},
		ipv6.ProtocolNumber,
	)
	return conn, trace.Wrap(err)
}

func (p *testPack) lookupHost(ctx context.Context, host string) ([]string, error) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := p.dialUDP(ctx, p.dnsIPv6, 53)
			return conn, err
		},
	}
	return resolver.LookupHost(ctx, host)
}

func (p *testPack) dialHost(ctx context.Context, host string) (net.Conn, error) {
	addrs, err := p.lookupHost(ctx, host)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	var allErrs []error
	for _, addr := range addrs {
		netIP := net.ParseIP(addr)
		ip := tcpip.AddrFromSlice(netIP)
		conn, err := p.dialIPPort(ctx, ip, 123)
		if err != nil {
			allErrs = append(allErrs, trace.Wrap(err, "dialing %s", addr))
			continue
		}
		return conn, nil
	}
	return nil, trace.Wrap(trace.NewAggregate(allErrs...), "dialing %s", host)
}

type noUpstreamNameservers struct{}

func (n noUpstreamNameservers) UpstreamNameservers(ctx context.Context) ([]string, error) {
	return nil, nil
}

type echoAppProvider struct {
	profiles       []string
	clients        map[string]map[string]*client.ClusterClient
	dialOpts       DialOptions
	reissueAppCert func() tls.Certificate
}

// newEchoAppProvider returns an app provider with the list of named apps in each profile and leaf cluster.
func newEchoAppProvider(apps map[string]map[string][]string, dialOpts DialOptions, reissueAppCert func() tls.Certificate) *echoAppProvider {
	p := &echoAppProvider{
		clients:        make(map[string]map[string]*client.ClusterClient, len(apps)),
		dialOpts:       dialOpts,
		reissueAppCert: reissueAppCert,
	}
	for profileName, leafClusters := range apps {
		p.profiles = append(p.profiles, profileName)
		p.clients[profileName] = make(map[string]*client.ClusterClient, len(leafClusters))
		for leafClusterName, apps := range leafClusters {
			clusterName := profileName
			if leafClusterName != "" {
				clusterName = leafClusterName
			}
			p.clients[profileName][leafClusterName] = &client.ClusterClient{
				AuthClient: &echoAppAuthClient{
					clusterName: clusterName,
					apps:        apps,
				},
			}
		}
	}
	return p
}

// ListProfiles lists the names of all profiles saved for the user.
func (p *echoAppProvider) ListProfiles() ([]string, error) {
	return p.profiles, nil
}

// GetCachedClient returns a [*client.ClusterClient] for the given profile and leaf cluster.
// [leafClusterName] may be empty when requesting a client for the root cluster. Returned clients are
// expected to be cached, as this may be called frequently.
func (p *echoAppProvider) GetCachedClient(ctx context.Context, profileName, leafClusterName string) (*client.ClusterClient, error) {
	c, ok := p.clients[profileName][leafClusterName]
	if !ok {
		return nil, trace.NotFound("no client for %s:%s", profileName, leafClusterName)
	}
	return c, nil
}

func (p *echoAppProvider) ReissueAppCert(ctx context.Context, profileName, leafClusterName string, app types.Application) (tls.Certificate, error) {
	return p.reissueAppCert(), nil
}

func (p *echoAppProvider) GetDialOptions(ctx context.Context, profileName string) (*DialOptions, error) {
	return &p.dialOpts, nil
}

// echoAppAuthClient is a fake auth client that answers GetResources requests with a static list of apps and
// basic/faked predicate filtering.
type echoAppAuthClient struct {
	authclient.ClientI
	clusterName string
	apps        []string
}

func (c *echoAppAuthClient) GetResources(ctx context.Context, req *proto.ListResourcesRequest) (*proto.ListResourcesResponse, error) {
	resp := &proto.ListResourcesResponse{}
	for _, app := range c.apps {
		// Poor-man's predicate expression filter.
		appPublicAddr := app + "." + c.clusterName
		if !strings.Contains(req.PredicateExpression, appPublicAddr) {
			continue
		}
		resp.Resources = append(resp.Resources, &proto.PaginatedResource{
			Resource: &proto.PaginatedResource_AppServer{
				AppServer: &types.AppServerV3{
					Kind: types.KindAppServer,
					Metadata: types.Metadata{
						Name: app,
					},
					Spec: types.AppServerSpecV3{
						App: &types.AppV3{
							Metadata: types.Metadata{
								Name: app,
							},
							Spec: types.AppSpecV3{
								PublicAddr: app,
							},
						},
					},
				},
			},
		})
	}
	resp.TotalCount = int32(len(resp.Resources))
	return resp, nil
}

func TestDialFakeApp(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	clock := clockwork.NewFakeClockAt(time.Now())

	ca := newSelfSignedCA(t)

	roots := x509.NewCertPool()
	caX509, err := x509.ParseCertificate(ca.Certificate[0])
	require.NoError(t, err)
	roots.AddCert(caX509)

	const proxyCN = "testproxy"
	proxyCert := newServerCert(t, ca, proxyCN, clock.Now().Add(365*24*time.Hour))

	proxyTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{proxyCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    roots,
	}

	listener, err := tls.Listen("tcp", "localhost:0", proxyTLSConfig)
	require.NoError(t, err)

	// Run a fake web proxy that will accept any client connection and echo the input back.
	utils.RunTestBackgroundTask(ctx, t, &utils.TestBackgroundTask{
		Name: "web proxy",
		Task: func(ctx context.Context) error {
			for {
				conn, err := listener.Accept()
				if err != nil {
					if utils.IsOKNetworkError(err) {
						return nil
					}
					return trace.Wrap(err)
				}
				go func() {
					err := utils.ProxyConn(ctx, conn, conn)
					if errors.Is(err, context.Canceled) {
						err = nil
					}
					assert.NoError(t, err)
				}()
			}
		},
		Terminate: func() error {
			if err := listener.Close(); !utils.IsOKNetworkError(err) {
				return trace.Wrap(err)
			}
			return nil
		},
	})

	dialOpts := DialOptions{
		WebProxyAddr:          listener.Addr().String(),
		RootClusterCACertPool: roots,
		SNI:                   proxyCN,
	}

	const appCertLifetime = time.Hour
	reissueClientCert := func() tls.Certificate {
		return newClientCert(t, ca, "testclient", clock.Now().Add(appCertLifetime))
	}

	appProvider := newEchoAppProvider(map[string]map[string][]string{
		"root1.example.com": map[string][]string{
			"":                 {"echo1", "echo2"},
			"leaf.example.com": {"echo1"},
		},
		"root2.example.com": map[string][]string{
			"":                  {"echo1", "echo2"},
			"leaf2.example.com": {"echo1"},
		},
	}, dialOpts, reissueClientCert)

	validAppNames := []string{
		"echo1.root1.example.com",
		"echo2.root1.example.com",
		"echo1.root2.example.com",
		"echo2.root2.example.com",
		// Leaf clusters not yet supported.
	}

	invalidAppNames := []string{
		"not.an.app.example.com.",
		"echo1.leaf1.example.com.",
		"echo1.leaf2.example.com.",
	}

	p := newTestPack(t, ctx, clock, appProvider)

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		for _, app := range validAppNames {
			app := app
			t.Run(app, func(t *testing.T) {
				t.Parallel()

				// Connect to each app 3 times, advancing the clock past the cert lifetime between each
				// connection to trigger a cert refresh.
				for i := 0; i < 3; i++ {
					t.Run(fmt.Sprint(i), func(t *testing.T) {
						conn, err := p.dialHost(ctx, app)
						require.NoError(t, err)
						t.Cleanup(func() { require.NoError(t, conn.Close()) })

						testEchoConnection(t, conn)
					})

					clock.Advance(2 * appCertLifetime)
				}
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		for _, app := range invalidAppNames {
			app := app
			t.Run("invalid/"+app, func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
				_, err := p.lookupHost(ctx, app)
				require.Error(t, err, "asdf")
			})
		}
	})
}

func testEchoConnection(t *testing.T, conn net.Conn) {
	const testString = "1........."
	writeBuf := bytes.Repeat([]byte(testString), 10)
	readBuf := make([]byte, len(writeBuf))

	for i := 0; i < 10; i++ {
		written, err := conn.Write(writeBuf)
		for written < len(writeBuf) && err == nil {
			var n int
			n, err = conn.Write(writeBuf[written:])
			written += n
		}
		require.NoError(t, err)
		require.Equal(t, len(writeBuf), written)

		n, err := io.ReadFull(conn, readBuf)
		require.NoError(t, err)
		require.Equal(t, string(writeBuf), string(readBuf[:n]))
	}
}

func randomULAAddress() (tcpip.Address, error) {
	var bytes [16]byte
	bytes[0] = 0xfd
	if _, err := rand.Read(bytes[1:16]); err != nil {
		return tcpip.Address{}, trace.Wrap(err)
	}
	return tcpip.AddrFrom16(bytes), nil
}

var errFakeTUNClosed = errors.New("TUN closed")

type fakeTUN struct {
	name                            string
	writePacketsTo, readPacketsFrom chan []byte
	closed                          chan struct{}
	closeOnce                       func()
}

// newSplitTUN returns two fake TUN devices that are tied together: writes to one can be read on the other,
// and vice versa.
func newSplitTUN() (*fakeTUN, *fakeTUN) {
	aClosed := make(chan struct{})
	bClosed := make(chan struct{})
	ab := make(chan []byte)
	ba := make(chan []byte)
	return &fakeTUN{
			name:            "tun1",
			writePacketsTo:  ab,
			readPacketsFrom: ba,
			closed:          aClosed,
			closeOnce:       sync.OnceFunc(func() { close(aClosed) }),
		}, &fakeTUN{
			name:            "tun2",
			writePacketsTo:  ba,
			readPacketsFrom: ab,
			closed:          bClosed,
			closeOnce:       sync.OnceFunc(func() { close(bClosed) }),
		}
}

func (f *fakeTUN) BatchSize() int {
	return 1
}

// Write one or more packets to the device (without any additional headers).
// On a successful write it returns the number of packets written. A nonzero
// offset can be used to instruct the Device on where to begin writing from
// each packet contained within the bufs slice.
func (f *fakeTUN) Write(bufs [][]byte, offset int) (int, error) {
	if len(bufs) != 1 {
		return 0, trace.BadParameter("batchsize is 1")
	}
	packet := make([]byte, len(bufs[0][offset:]))
	copy(packet, bufs[0][offset:])
	select {
	case <-f.closed:
		return 0, errFakeTUNClosed
	case f.writePacketsTo <- packet:
	}
	return 1, nil
}

// Read one or more packets from the Device (without any additional headers).
// On a successful read it returns the number of packets read, and sets
// packet lengths within the sizes slice. len(sizes) must be >= len(bufs).
// A nonzero offset can be used to instruct the Device on where to begin
// reading into each element of the bufs slice.
func (f *fakeTUN) Read(bufs [][]byte, sizes []int, offset int) (n int, err error) {
	if len(bufs) != 1 {
		return 0, trace.BadParameter("batchsize is 1")
	}
	var packet []byte
	select {
	case <-f.closed:
		return 0, errFakeTUNClosed
	case packet = <-f.readPacketsFrom:
	}
	sizes[0] = copy(bufs[0][offset:], packet)
	return 1, nil
}

func (f *fakeTUN) Close() error {
	f.closeOnce()
	return nil
}

func newSelfSignedCA(t *testing.T) tls.Certificate {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-ca",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, pub, priv)
	require.NoError(t, err)

	return tls.Certificate{
		Certificate: [][]byte{certBytes},
		PrivateKey:  priv,
	}
}

func newServerCert(t *testing.T, ca tls.Certificate, cn string, expires time.Time) tls.Certificate {
	return newLeafCert(t, ca, cn, expires, x509.ExtKeyUsageServerAuth)
}

func newClientCert(t *testing.T, ca tls.Certificate, cn string, expires time.Time) tls.Certificate {
	return newLeafCert(t, ca, cn, expires, x509.ExtKeyUsageClientAuth)
}

func newLeafCert(t *testing.T, ca tls.Certificate, cn string, expires time.Time, keyUsage x509.ExtKeyUsage) tls.Certificate {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	caCert, err := x509.ParseCertificate(ca.Certificate[0])
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: cn,
		},
		NotBefore:   time.Now(),
		NotAfter:    expires,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{keyUsage},
		DNSNames:    []string{cn},
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, caCert, pub, ca.PrivateKey)
	require.NoError(t, err)

	return tls.Certificate{
		Certificate: [][]byte{certBytes},
		PrivateKey:  priv,
	}
}
