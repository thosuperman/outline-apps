// Copyright 2026 The Outline Authors
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

package configregistry

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.getoutline.org/sdk/transport"
)

// countingPacketListener counts the UDP sockets opened to the proxy. Its sockets answer every
// packet with a copy that has the DNS response bit set.
type countingPacketListener struct {
	sockets atomic.Int32
}

func (l *countingPacketListener) ListenPacket(ctx context.Context) (net.PacketConn, error) {
	l.sockets.Add(1)
	return &echoPacketConn{responses: make(chan echoPacket, 256), closed: make(chan struct{})}, nil
}

type echoPacket struct {
	payload []byte
	from    net.Addr
}

type echoPacketConn struct {
	responses chan echoPacket
	closed    chan struct{}
	closeOnce sync.Once

	mu       sync.Mutex
	deadline time.Time
}

func (c *echoPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	resp := append([]byte(nil), p...)
	resp[2] |= 0x80 // QR
	select {
	case c.responses <- echoPacket{resp, addr}:
	case <-c.closed:
		return 0, net.ErrClosed
	}
	return len(p), nil
}

func (c *echoPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	timeout := make(<-chan time.Time)
	if !deadline.IsZero() {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case resp := <-c.responses:
		return copy(p, resp.payload), resp.from, nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	case <-timeout:
		return 0, nil, os.ErrDeadlineExceeded
	}
}

func (c *echoPacketConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *echoPacketConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	return nil
}

func (c *echoPacketConn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *echoPacketConn) SetWriteDeadline(t time.Time) error { return nil }
func (c *echoPacketConn) LocalAddr() net.Addr                { return &net.UDPAddr{IP: net.IPv4zero} }

// tcpDNSServer is a DNS-over-TCP server on the loopback interface that echoes each query with
// the response bit set.
func tcpDNSServer(t *testing.T) net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					var l [2]byte
					if _, err := io.ReadFull(br, l[:]); err != nil {
						return
					}
					msg := make([]byte, 2+binary.BigEndian.Uint16(l[:]))
					copy(msg, l[:])
					if _, err := io.ReadFull(br, msg[2:]); err != nil {
						return
					}
					msg[2+2] |= 0x80 // QR
					if _, err := c.Write(msg); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln
}

type packetQueue chan []byte

func (q packetQueue) HandlePacket(p []byte, _ netip.AddrPort) error {
	q <- append([]byte(nil), p...)
	return nil
}

// queryOutlineDNS sends a DNS query to the intercepted resolver from a new association, as the
// OS does from a new socket, and returns the response.
func queryOutlineDNS(t *testing.T, relay *PacketRelay, id byte) []byte {
	t.Helper()
	sender, receiver, err := relay.NewAssociation()
	require.NoError(t, err)
	defer sender.Close()
	responses := make(packetQueue, 1)
	go receiver.ReceivePackets(responses)
	query := []byte{0, id, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	require.NoError(t, sender.SendPacket(query, linkLocalDNS))
	select {
	case resp := <-responses:
		require.Equal(t, query[:2], resp[:2])
		return resp
	case <-time.After(8 * time.Second):
		require.FailNow(t, "no DNS response")
		return nil
	}
}

func isTruncatedDNS(resp []byte) bool { return resp[2]&0x02 != 0 }

// recordingDialer dials ln whatever the address, and records the addresses.
type recordingDialer struct {
	ln    net.Listener
	mu    sync.Mutex
	addrs []string
	fail  bool
}

func (d *recordingDialer) dial(ctx context.Context, addr string) (transport.StreamConn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, addr)
	fail := d.fail
	d.mu.Unlock()
	if fail {
		return nil, errors.New("TCP blocked")
	}
	var nd net.Dialer
	c, err := nd.DialContext(ctx, "tcp", d.ln.Addr().String())
	if err != nil {
		return nil, err
	}
	return c.(*net.TCPConn), nil
}

func (d *recordingDialer) dialed() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.addrs)
}

func newOutlineDNSPair(t *testing.T, d *recordingDialer, l *countingPacketListener) *TransportPair {
	t.Helper()
	pair, err := wrapTransportPairWithOutlineDNS(
		&Dialer[transport.StreamConn]{ConnectionProviderInfo{ConnTypeTunneled, "proxy:1234"}, d.dial},
		&PacketListener{ConnectionProviderInfo{ConnTypeTunneled, "proxy:1234"}, l},
	)
	require.NoError(t, err)
	return pair
}

// Regression test: every DNS query used to open a new UDP socket, and so a new UDP flow, to the
// proxy. NAT gateways limit the UDP flows to one destination, so a burst of queries would break
// name resolution for as long as the OS kept retrying. Queries now share one TCP connection.
func TestOutlineDNS_BurstOfUDPQueriesUsesOneTCPConnection(t *testing.T) {
	d := &recordingDialer{ln: tcpDNSServer(t)}
	l := &countingPacketListener{}
	pair := newOutlineDNSPair(t, d, l)
	// The session starts, which checks UDP connectivity (one socket).
	pair.PacketRelay.NotifyNetworkChanged()
	require.Eventually(t, func() bool { return l.sockets.Load() == 1 }, 5*time.Second, time.Millisecond)
	time.Sleep(100 * time.Millisecond) // Let the check finish.

	const queries = 300
	var wg sync.WaitGroup
	for i := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.False(t, isTruncatedDNS(queryOutlineDNS(t, pair.PacketRelay, byte(i))))
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), l.sockets.Load(), "DNS must not open UDP sockets")
	dialed := d.dialed()
	require.Len(t, dialed, 1, "all queries must share one TCP connection")
	resolver, err := netip.ParseAddrPort(dialed[0])
	require.NoError(t, err)
	require.Contains(t, outlineDNSResolvers, resolver, "queries must go to the remote resolver")
}

func TestOutlineDNS_FallsBackWhenTCPFails(t *testing.T) {
	d := &recordingDialer{ln: tcpDNSServer(t), fail: true}
	l := &countingPacketListener{}
	pair := newOutlineDNSPair(t, d, l)

	// Before UDP is known to work, the fallback answers with a truncated response.
	require.True(t, isTruncatedDNS(queryOutlineDNS(t, pair.PacketRelay, 1)))
	require.Zero(t, l.sockets.Load())

	// Once the UDP check passes, the fallback forwards queries over UDP. (The network change
	// also lets the relay retry TCP, which still fails.)
	pair.PacketRelay.NotifyNetworkChanged()
	require.Eventually(t, func() bool {
		return !isTruncatedDNS(queryOutlineDNS(t, pair.PacketRelay, 2))
	}, 8*time.Second, 10*time.Millisecond)
	require.Positive(t, l.sockets.Load())
}

func TestOutlineDNS_TCPQueriesToTheInterceptedResolverAreRedirected(t *testing.T) {
	d := &recordingDialer{ln: tcpDNSServer(t)}
	pair := newOutlineDNSPair(t, d, &countingPacketListener{})
	conn, err := pair.StreamDialer.Dial(context.Background(), linkLocalDNS.String())
	require.NoError(t, err)
	conn.Close()
	resolver, err := netip.ParseAddrPort(d.dialed()[0])
	require.NoError(t, err)
	require.Contains(t, outlineDNSResolvers, resolver)
}
