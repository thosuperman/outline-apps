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

package dnstcp

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.getoutline.org/sdk/network/dnsintercept"
	"golang.getoutline.org/sdk/network/packetrelay"
	"golang.getoutline.org/sdk/transport"
)

var (
	resolver     = netip.MustParseAddrPort("1.1.1.1:53")
	fakeResolver = netip.MustParseAddrPort("169.254.113.53:53")
)

// tcpResolver is a DNS-over-TCP server on the loopback interface. It answers each query with a
// copy that has the QR bit set, after a random short delay, so responses arrive out of order.
// The first label of the query name selects other behaviors:
//   - "silent": no response.
//   - "big": a response of 1500 bytes.
//   - "dropconn": close the connection the first time the name is seen, answer afterwards.
type tcpResolver struct {
	ln        net.Listener
	accepted  atomic.Int32
	ended     atomic.Int32 // Connections that the client closed.
	closeAll  atomic.Bool  // Close every connection on accept.
	seenMu    sync.Mutex
	seenNames map[string]int
}

func newTCPResolver(t *testing.T) *tcpResolver {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &tcpResolver{ln: ln, seenNames: make(map[string]int)}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.accepted.Add(1)
			if s.closeAll.Load() {
				c.Close()
				continue
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *tcpResolver) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	var wmu sync.Mutex
	for {
		var l [2]byte
		if _, err := io.ReadFull(br, l[:]); err != nil {
			s.ended.Add(1)
			return
		}
		q := make([]byte, binary.BigEndian.Uint16(l[:]))
		if _, err := io.ReadFull(br, q); err != nil {
			return
		}
		label := firstLabel(q)
		s.seenMu.Lock()
		s.seenNames[label]++
		seen := s.seenNames[label]
		s.seenMu.Unlock()
		switch {
		case strings.HasPrefix(label, "silent"):
			continue
		case strings.HasPrefix(label, "dropconn") && seen == 1:
			return
		}
		resp := append([]byte(nil), q...)
		resp[dnsFlagsByte] |= dnsResponseBit
		if strings.HasPrefix(label, "big") {
			resp = append(resp, make([]byte, 1500-len(resp))...)
		}
		go func() {
			time.Sleep(time.Duration(rand.IntN(5)) * time.Millisecond)
			frame := binary.BigEndian.AppendUint16(nil, uint16(len(resp)))
			wmu.Lock()
			defer wmu.Unlock()
			c.Write(append(frame, resp...))
		}()
	}
}

func firstLabel(q []byte) string {
	if len(q) <= dnsHeaderLen || int(q[dnsHeaderLen]) >= len(q)-dnsHeaderLen {
		return ""
	}
	return string(q[dnsHeaderLen+1 : dnsHeaderLen+1+int(q[dnsHeaderLen])])
}

// dialer returns a DialFunc to s that counts its calls.
func (s *tcpResolver) dialer(count *atomic.Int32) DialFunc {
	return func(ctx context.Context) (transport.StreamConn, error) {
		count.Add(1)
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", s.ln.Addr().String())
		if err != nil {
			return nil, err
		}
		return c.(*net.TCPConn), nil
	}
}

// echoRelay is a fallback PacketRelay whose associations echo queries back as responses.
type echoRelay struct{ assocs atomic.Int32 }

type echoAssoc struct {
	ch   chan []byte
	done chan struct{}
	once sync.Once
}

func (e *echoRelay) NewAssociation() (packetrelay.PacketSender, packetrelay.PacketReceiver, error) {
	e.assocs.Add(1)
	a := &echoAssoc{ch: make(chan []byte, 4), done: make(chan struct{})}
	return a, a, nil
}

func (a *echoAssoc) SendPacket(p []byte, _ netip.AddrPort) error {
	resp := append([]byte(nil), p...)
	resp[dnsFlagsByte] |= dnsResponseBit
	select {
	case a.ch <- resp:
	case <-a.done:
		return packetrelay.ErrClosed
	}
	return nil
}

func (a *echoAssoc) Close() error {
	a.once.Do(func() { close(a.done) })
	return nil
}

func (a *echoAssoc) ReceivePackets(h packetrelay.PacketHandler) error {
	for {
		select {
		case p := <-a.ch:
			if err := h.HandlePacket(p, resolver); err != nil {
				return err
			}
		case <-a.done:
			return packetrelay.ErrClosed
		}
	}
}

type collector chan []byte

func (c collector) HandlePacket(p []byte, _ netip.AddrPort) error {
	c <- append([]byte(nil), p...)
	return nil
}

func (c collector) next(t *testing.T, within time.Duration) []byte {
	t.Helper()
	select {
	case p := <-c:
		return p
	case <-time.After(within):
		require.FailNow(t, "no response", "waited %v", within)
		return nil
	}
}

func expectNothing(t *testing.T, c collector, wait time.Duration) {
	t.Helper()
	select {
	case p := <-c:
		require.FailNow(t, "unexpected response", "%x", p)
	case <-time.After(wait):
	}
}

// newQuery returns a query for the single-label name, with an EDNS OPT record advertising
// ednsSize if it is not zero.
func newQuery(id uint16, name string, ednsSize uint16) []byte {
	q := make([]byte, dnsHeaderLen, 64)
	binary.BigEndian.PutUint16(q, id)
	q[2] = 0x01                          // RD
	binary.BigEndian.PutUint16(q[4:], 1) // QDCOUNT
	q = append(q, byte(len(name)))
	q = append(q, name...)
	q = append(q, 0, 0, 1, 0, 1)
	if ednsSize != 0 {
		binary.BigEndian.PutUint16(q[10:], 1) // ARCOUNT
		q = append(q, 0, 0, dnsTypeOPT)
		q = binary.BigEndian.AppendUint16(q, ednsSize)
		q = append(q, 0, 0, 0, 0, 0, 0) // TTL and RDLENGTH
	}
	return q
}

func isTruncated(p []byte) bool { return p[dnsFlagsByte]&dnsTruncatedBit != 0 }

func open(t *testing.T, relay packetrelay.PacketRelay) (packetrelay.PacketSender, collector) {
	t.Helper()
	s, r, err := relay.NewAssociation()
	require.NoError(t, err)
	c := make(collector, 8)
	go r.ReceivePackets(c)
	t.Cleanup(func() { s.Close() })
	return s, c
}

// ask sends q on a new association and returns the response.
func ask(t *testing.T, relay packetrelay.PacketRelay, q []byte, dst netip.AddrPort) []byte {
	t.Helper()
	s, c := open(t, relay)
	require.NoError(t, s.SendPacket(q, dst))
	return c.next(t, 3*time.Second)
}

func pendingCount(r *Relay) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

func TestNewRelayValidatesArguments(t *testing.T) {
	_, err := NewRelay(nil, resolver, nil, Config{})
	require.Error(t, err)
	_, err = NewRelay(func(context.Context) (transport.StreamConn, error) { return nil, nil }, netip.AddrPort{}, nil, Config{})
	require.Error(t, err)
}

func TestQueriesSharePipelinedConnectionAndAcceptReordering(t *testing.T) {
	srv := newTCPResolver(t)
	var dials atomic.Int32
	fb := &echoRelay{}
	relay, err := NewRelay(srv.dialer(&dials), resolver, fb, Config{})
	require.NoError(t, err)

	const n = 300
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Every caller uses the same ID: responses must still reach the right caller.
			q := newQuery(0x4242, fmt.Sprintf("host%d", i), 0)
			resp := ask(t, relay, q, resolver)
			require.False(t, isTruncated(resp))
			require.Equal(t, q[:2], resp[:2], "the original ID must be restored")
			require.Equal(t, q[dnsHeaderLen:], resp[dnsHeaderLen:], "response for another query")
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), dials.Load(), "all queries must share one connection")
	require.Equal(t, int32(1), srv.accepted.Load())
	require.Zero(t, fb.assocs.Load())
	require.Zero(t, pendingCount(relay))
}

func TestIdleConnectionIsClosedAndReopened(t *testing.T) {
	srv := newTCPResolver(t)
	var dials atomic.Int32
	relay, err := NewRelay(srv.dialer(&dials), resolver, nil, Config{IdleTimeout: 50 * time.Millisecond})
	require.NoError(t, err)

	require.False(t, isTruncated(ask(t, relay, newQuery(1, "a", 0), resolver)))
	time.Sleep(150 * time.Millisecond)
	relay.mu.Lock()
	require.Nil(t, relay.conn, "the idle connection must be closed")
	relay.mu.Unlock()

	require.False(t, isTruncated(ask(t, relay, newQuery(2, "b", 0), resolver)))
	require.Equal(t, int32(2), dials.Load())
}

func TestQueriesAreResentWhenAWorkingConnectionDrops(t *testing.T) {
	srv := newTCPResolver(t)
	var dials atomic.Int32
	fb := &echoRelay{}
	relay, err := NewRelay(srv.dialer(&dials), resolver, fb, Config{})
	require.NoError(t, err)

	require.False(t, isTruncated(ask(t, relay, newQuery(1, "a", 0), resolver)))
	// The resolver drops the connection when it sees this query, as a resolver closing an idle
	// connection would. The query must be resent on a new connection.
	q := newQuery(2, "dropconn", 0)
	resp := ask(t, relay, q, resolver)
	require.False(t, isTruncated(resp))
	require.Equal(t, q[dnsHeaderLen:], resp[dnsHeaderLen:])
	require.Equal(t, int32(2), dials.Load())
	require.Zero(t, fb.assocs.Load(), "TCP worked, so the fallback must not be used")
}

func TestConnectionFailingBeforeAnyResponseUsesFallback(t *testing.T) {
	srv := newTCPResolver(t)
	srv.closeAll.Store(true) // As when the proxy cannot reach the resolver over TCP.
	var dials atomic.Int32
	fb := &echoRelay{}
	relay, err := NewRelay(srv.dialer(&dials), resolver, fb, Config{RetryAfter: time.Minute})
	require.NoError(t, err)

	q := newQuery(1, "a", 0)
	resp := ask(t, relay, q, resolver)
	require.False(t, isTruncated(resp))
	require.Equal(t, q[dnsHeaderLen:], resp[dnsHeaderLen:])
	require.Equal(t, int32(1), fb.assocs.Load())

	// TCP is considered down: further queries go straight to the fallback.
	require.False(t, isTruncated(ask(t, relay, newQuery(2, "b", 0), resolver)))
	require.Equal(t, int32(1), dials.Load())
	require.Equal(t, int32(2), fb.assocs.Load())
}

func TestTCPIsRetriedAfterRetryAfter(t *testing.T) {
	srv := newTCPResolver(t)
	srv.closeAll.Store(true)
	var dials atomic.Int32
	relay, err := NewRelay(srv.dialer(&dials), resolver, &echoRelay{}, Config{RetryAfter: 50 * time.Millisecond})
	require.NoError(t, err)

	ask(t, relay, newQuery(1, "a", 0), resolver)
	srv.closeAll.Store(false)
	time.Sleep(80 * time.Millisecond)
	ask(t, relay, newQuery(2, "b", 0), resolver)
	require.Equal(t, int32(2), dials.Load())
	relay.mu.Lock()
	defer relay.mu.Unlock()
	require.NotNil(t, relay.conn)
	require.True(t, relay.conn.answered)
}

func TestDialErrorUsesFallback(t *testing.T) {
	fb := &echoRelay{}
	dial := func(context.Context) (transport.StreamConn, error) { return nil, errors.New("no route") }
	relay, err := NewRelay(dial, resolver, fb, Config{})
	require.NoError(t, err)
	require.False(t, isTruncated(ask(t, relay, newQuery(1, "a", 0), resolver)))
	require.Equal(t, int32(1), fb.assocs.Load())
}

func TestWithoutFallbackTCPFailureAnswersTruncated(t *testing.T) {
	dial := func(context.Context) (transport.StreamConn, error) { return nil, errors.New("no route") }
	relay, err := NewRelay(dial, resolver, nil, Config{})
	require.NoError(t, err)
	q := newQuery(0xabcd, "a", 0)
	resp := ask(t, relay, q, resolver)
	require.True(t, isTruncated(resp), "the client must retry over TCP")
	require.Equal(t, q[:2], resp[:2])
}

func TestStuckConnectionIsReplaced(t *testing.T) {
	srv := newTCPResolver(t)
	var dials atomic.Int32
	relay, err := NewRelay(srv.dialer(&dials), resolver, nil, Config{QueryTimeout: 50 * time.Millisecond})
	require.NoError(t, err)

	require.False(t, isTruncated(ask(t, relay, newQuery(1, "a", 0), resolver)))
	s, c := open(t, relay)
	require.NoError(t, s.SendPacket(newQuery(2, "silent", 0), resolver))
	expectNothing(t, c, 150*time.Millisecond)
	require.Zero(t, pendingCount(relay))

	require.False(t, isTruncated(ask(t, relay, newQuery(3, "b", 0), resolver)))
	require.Equal(t, int32(2), dials.Load(), "the stuck connection must be replaced")
}

func TestLargeResponsesAreTruncatedForUDP(t *testing.T) {
	srv := newTCPResolver(t)
	var dials atomic.Int32
	relay, err := NewRelay(srv.dialer(&dials), resolver, nil, Config{})
	require.NoError(t, err)

	for _, edns := range []uint16{0, 512, 1232, 4096} {
		q := newQuery(7, fmt.Sprintf("big%d", edns), edns)
		resp := ask(t, relay, q, resolver)
		require.True(t, isTruncated(resp), "a 1500-byte response does not fit (EDNS %d)", edns)
		require.Equal(t, q[:2], resp[:2])
		require.Equal(t, uint16(1), binary.BigEndian.Uint16(resp[4:]), "the question is kept")
		require.Zero(t, binary.BigEndian.Uint16(resp[10:]), "no additional records")
	}
	small := newQuery(8, "small", 4096)
	resp := ask(t, relay, small, resolver)
	require.False(t, isTruncated(resp))
	require.Equal(t, small[dnsHeaderLen:], resp[dnsHeaderLen:])
}

func TestMaxUDPResponse(t *testing.T) {
	require.Equal(t, 512, maxUDPResponse(newQuery(1, "a", 0)))
	require.Equal(t, 512, maxUDPResponse(newQuery(1, "a", 100)))
	require.Equal(t, 1200, maxUDPResponse(newQuery(1, "a", 1200)))
	require.Equal(t, 1232, maxUDPResponse(newQuery(1, "a", 4096)))
	require.Equal(t, 512, maxUDPResponse([]byte{1, 2, 3}))
}

func TestResetClosesConnectionAndRetiresQueries(t *testing.T) {
	srv := newTCPResolver(t)
	var dials atomic.Int32
	relay, err := NewRelay(srv.dialer(&dials), resolver, nil, Config{QueryTimeout: time.Minute})
	require.NoError(t, err)

	require.False(t, isTruncated(ask(t, relay, newQuery(1, "a", 0), resolver)))
	s, c := open(t, relay)
	require.NoError(t, s.SendPacket(newQuery(2, "silent", 0), resolver))
	require.Eventually(t, func() bool { return pendingCount(relay) == 1 }, time.Second, time.Millisecond)

	relay.Reset() // The network changes.
	require.Zero(t, pendingCount(relay))
	require.Eventually(t, func() bool { return srv.ended.Load() == 1 }, time.Second, time.Millisecond,
		"Reset must close the old connection")
	expectNothing(t, c, 50*time.Millisecond)

	require.False(t, isTruncated(ask(t, relay, newQuery(3, "b", 0), resolver)))
	require.Equal(t, int32(2), dials.Load())
}

func TestRejectsOtherDestinationsAndInvalidQueries(t *testing.T) {
	var dials atomic.Int32
	relay, err := NewRelay(newTCPResolver(t).dialer(&dials), resolver, nil, Config{})
	require.NoError(t, err)
	s, _ := open(t, relay)
	require.ErrorIs(t, s.SendPacket(newQuery(1, "a", 0), netip.MustParseAddrPort("8.8.8.8:53")), errWrongResolver)
	require.Error(t, s.SendPacket([]byte{1, 2, 3}, resolver))
	require.Zero(t, dials.Load())
}

func TestAssociationExpiresAndReleasesItsQueries(t *testing.T) {
	var dials atomic.Int32
	relay, err := NewRelay(newTCPResolver(t).dialer(&dials), resolver, nil,
		Config{QueryTimeout: time.Minute, AssociationTimeout: 50 * time.Millisecond})
	require.NoError(t, err)
	s, r, err := relay.NewAssociation()
	require.NoError(t, err)
	require.NoError(t, s.SendPacket(newQuery(1, "silent", 0), resolver))

	returned := make(chan error, 1)
	go func() { returned <- r.ReceivePackets(make(collector, 1)) }()
	select {
	case err := <-returned:
		require.ErrorIs(t, err, packetrelay.ErrClosed)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "ReceivePackets did not return")
	}
	require.ErrorIs(t, s.SendPacket(newQuery(2, "a", 0), resolver), packetrelay.ErrClosed)
	require.Zero(t, pendingCount(relay))
}

func TestAnsweredByMatchesTheQuestion(t *testing.T) {
	q := &query{payload: newQuery(1, "example", 0)}
	q.question, q.nameLen = parseQuestion(q.payload)

	resp := append([]byte(nil), q.payload...)
	require.True(t, q.answeredBy(resp))
	upper := append([]byte(nil), resp...)
	copy(upper[dnsHeaderLen+1:], "EXAMPLE")
	require.True(t, q.answeredBy(upper), "names compare case-insensitively")
	otherType := append([]byte(nil), resp...)
	otherType[len(otherType)-3] = 28
	require.False(t, q.answeredBy(otherType))
	require.False(t, q.answeredBy(resp[:dnsHeaderLen+3]))

	require.True(t, equalFoldASCII([]byte("Example"), []byte("eXAMPLE")))
	require.False(t, equalFoldASCII([]byte{0xfe}, []byte{0xff}))
	require.False(t, equalFoldASCII([]byte("["), []byte("{")))
}

// This is the behavior the package exists for: behind the SDK's DNS interceptor, which opens one
// association per DNS packet, a burst of queries uses one TCP connection and no UDP flows.
func TestBehindDNSInterceptorABurstUsesOneTCPConnection(t *testing.T) {
	srv := newTCPResolver(t)
	var dials atomic.Int32
	fb, other := &echoRelay{}, &echoRelay{}
	dnsTCP, err := NewRelay(srv.dialer(&dials), resolver, fb, Config{})
	require.NoError(t, err)
	relay := dnsintercept.NewInterceptDNSPacketRelay(dnsTCP, other, fakeResolver, resolver)

	const n = 200
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := newQuery(0x1111, fmt.Sprintf("host%d", i), 1232)
			resp := ask(t, relay, q, fakeResolver)
			require.False(t, isTruncated(resp))
			require.Equal(t, q[:2], resp[:2])
			require.Equal(t, q[dnsHeaderLen:], resp[dnsHeaderLen:])
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), dials.Load())
	require.Zero(t, fb.assocs.Load(), "no UDP fallback")
	require.Zero(t, other.assocs.Load(), "no non-DNS traffic")
}
