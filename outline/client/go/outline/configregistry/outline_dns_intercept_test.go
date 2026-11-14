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
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.getoutline.org/sdk/transport"
)

// countingPacketListener counts the UDP sockets opened to the proxy. Each socket stands for a
// distinct source port on the proxy. Its sockets answer every packet after delay with a copy
// that has the DNS response bit set.
type countingPacketListener struct {
	delay   time.Duration
	sockets atomic.Int32
}

func (l *countingPacketListener) ListenPacket(ctx context.Context) (net.PacketConn, error) {
	l.sockets.Add(1)
	return &echoPacketConn{delay: l.delay, responses: make(chan echoPacket, 256), closed: make(chan struct{})}, nil
}

type echoPacket struct {
	payload []byte
	from    net.Addr
}

type echoPacketConn struct {
	delay     time.Duration
	responses chan echoPacket
	closed    chan struct{}
	closeOnce sync.Once

	mu       sync.Mutex
	deadline time.Time
}

func (c *echoPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	resp := append([]byte(nil), p...)
	resp[2] |= 0x80 // QR
	go func() {
		time.Sleep(c.delay)
		select {
		case c.responses <- echoPacket{resp, addr}:
		case <-c.closed:
		}
	}()
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

type packetQueue chan []byte

func (q packetQueue) HandlePacket(p []byte, _ netip.AddrPort) error {
	q <- append([]byte(nil), p...)
	return nil
}

func dnsQuery(id uint16, name string) []byte {
	q := make([]byte, 12, 64)
	binary.BigEndian.PutUint16(q, id)
	q[2] = 0x01                          // RD
	binary.BigEndian.PutUint16(q[4:], 1) // QDCOUNT
	q = append(q, byte(len(name)))
	q = append(q, name...)
	return append(q, 0, 0, 1, 0, 1)
}

// queryOutlineDNS sends query to the intercepted resolver from a new association, as the OS does
// from a new socket, and returns the response.
func queryOutlineDNS(t *testing.T, relay *PacketRelay, query []byte) []byte {
	t.Helper()
	sender, receiver, err := relay.NewAssociation()
	require.NoError(t, err)
	defer sender.Close()
	responses := make(packetQueue, 1)
	go receiver.ReceivePackets(responses)
	require.NoError(t, sender.SendPacket(query, linkLocalDNS))
	select {
	case resp := <-responses:
		require.Equal(t, query[:2], resp[:2], "the client's ID must be preserved")
		return resp
	case <-time.After(8 * time.Second):
		require.FailNow(t, "no DNS response")
		return nil
	}
}

func isTruncatedDNS(resp []byte) bool { return resp[2]&0x02 != 0 }

// newOutlineDNSPair returns the transport pair, with UDP checked and DNS forwarding over UDP.
func newOutlineDNSPair(t *testing.T, l *countingPacketListener) *TransportPair {
	t.Helper()
	sd := &Dialer[transport.StreamConn]{ConnectionProviderInfo{ConnTypeTunneled, "proxy:1234"},
		func(ctx context.Context, addr string) (transport.StreamConn, error) {
			return nil, errors.New("not used")
		}}
	pair, err := wrapTransportPairWithOutlineDNS(sd, &PacketListener{ConnectionProviderInfo{ConnTypeTunneled, "proxy:1234"}, l})
	require.NoError(t, err)
	pair.PacketRelay.NotifyNetworkChanged() // The session starts: the UDP check runs.
	require.Eventually(t, func() bool {
		return !isTruncatedDNS(queryOutlineDNS(t, pair.PacketRelay, dnsQuery(1, "check")))
	}, 5*time.Second, 10*time.Millisecond)
	return pair
}

// Each distinct query must keep its own socket, and so its own source port on the proxy: that
// port is part of the protection against forged responses (RFC 5452, Section 9.2).
func TestOutlineDNS_DistinctQueriesKeepTheirOwnSocket(t *testing.T) {
	l := &countingPacketListener{delay: 20 * time.Millisecond}
	pair := newOutlineDNSPair(t, l)
	before := l.sockets.Load()

	const queries = 100
	var wg sync.WaitGroup
	for i := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := dnsQuery(uint16(i), fmt.Sprintf("host%d", i))
			resp := queryOutlineDNS(t, pair.PacketRelay, q)
			require.Equal(t, q[12:], resp[12:])
		}()
	}
	wg.Wait()
	require.Equal(t, int32(queries), l.sockets.Load()-before, "one socket per distinct query")
}

// Regression test: every copy of a query used to open its own socket to the proxy. Copies that
// arrive while the query is in flight, such as OS retransmissions, now share one exchange.
func TestOutlineDNS_CopiesOfAQueryInFlightShareOneSocket(t *testing.T) {
	l := &countingPacketListener{delay: 300 * time.Millisecond}
	pair := newOutlineDNSPair(t, l)
	before := l.sockets.Load()

	const copies = 30
	var wg sync.WaitGroup
	for i := range copies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := dnsQuery(uint16(100+i), "retransmitted") // New socket, new ID, same question.
			resp := queryOutlineDNS(t, pair.PacketRelay, q)
			require.Equal(t, q[12:], resp[12:])
		}()
		time.Sleep(5 * time.Millisecond)
	}
	wg.Wait()
	require.Equal(t, int32(1), l.sockets.Load()-before)
}
