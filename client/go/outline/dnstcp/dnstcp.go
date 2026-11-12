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

// Package dnstcp provides a [packetrelay.PacketRelay] that answers DNS queries received over
// UDP by forwarding them to a resolver over a persistent TCP connection.
//
// # Why
//
// Forwarding each intercepted UDP query on its own UDP flow to the proxy creates a new flow per
// query, which trips the per-destination flow limits of some NAT gateways and breaks name
// resolution while the VPN stays up. Multiplexing the queries on one UDP flow avoids that, but it
// pins the source port that the proxy uses towards the resolver, weakening the protection that
// source port randomization gives against forged responses (RFC 5452).
//
// TCP has neither problem. One connection carries all the queries, and off-path attackers cannot
// inject responses into a TCP stream. This package follows RFC 7766: it reuses the connection
// (Section 6.2.1), pipelines queries (Section 6.2.1.1), accepts responses in any order
// (Section 7) and closes the connection when it is idle (Section 6.2.3).
//
// # How
//
//   - Each query gets a message ID that is unique among the queries in flight. The original ID is
//     restored on the response. A response must also carry the query's question.
//   - Responses larger than the client can receive over UDP (its EDNS buffer size, at most
//     1232 bytes, or 512 bytes without EDNS) are replaced with a truncated response, so that the
//     client retries over TCP.
//   - If a connection fails before it delivers any response, TCP is considered unavailable for
//     [Config.RetryAfter], and queries go to the fallback relay instead. If a connection that
//     worked fails, its queries are resent once on a new connection.
//   - If a query is not answered within [Config.QueryTimeout] and nothing was received on its
//     connection since it was sent, the connection is considered stuck and is replaced.
package dnstcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"golang.getoutline.org/sdk/network/packetrelay"
	"golang.getoutline.org/sdk/transport"
)

const (
	dnsHeaderLen = 12
	// maxPending bounds the number of queries in flight, and therefore memory use.
	maxPending = 2048

	dnsFlagsByte    = 2           // The header byte with the QR and TC bits.
	dnsResponseBit  = uint8(0x80) // QR
	dnsTruncatedBit = uint8(0x02) // TC
	dnsRCodeByte    = 3
	dnsRCodeMask    = uint8(0x0f)

	dnsTypeOPT = 41
	// minUDPSize is the largest response every DNS client accepts over UDP (RFC 1035).
	minUDPSize = 512
	// maxUDPSize caps the response size sent over UDP. It is the EDNS buffer size recommended
	// by DNS Flag Day 2020 to avoid IP fragmentation.
	maxUDPSize = 1232
)

var (
	// ErrTooManyQueries is returned by SendPacket when too many queries are in flight.
	ErrTooManyQueries = errors.New("too many DNS queries in flight")
	errWrongResolver  = errors.New("destination is not the configured resolver")
	errStuck          = errors.New("connection did not answer")
)

// Config configures a [Relay]. Zero values select the defaults.
type Config struct {
	// QueryTimeout is how long a query waits for its response. Defaults to 3s.
	QueryTimeout time.Duration
	// DialTimeout bounds connecting to the resolver and each write. Defaults to 5s.
	DialTimeout time.Duration
	// IdleTimeout is how long a connection without queries in flight stays open. Defaults to 20s.
	IdleTimeout time.Duration
	// RetryAfter is how long queries go to the fallback relay after TCP fails. Defaults to 5s.
	RetryAfter time.Duration
	// AssociationTimeout is the lifetime of an association. Defaults to 5s.
	AssociationTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.QueryTimeout <= 0 {
		c.QueryTimeout = 3 * time.Second
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = 20 * time.Second
	}
	if c.RetryAfter <= 0 {
		c.RetryAfter = 5 * time.Second
	}
	if c.AssociationTimeout <= 0 {
		c.AssociationTimeout = 5 * time.Second
	}
	return c
}

// DialFunc connects to the resolver.
type DialFunc func(ctx context.Context) (transport.StreamConn, error)

// Relay is a [packetrelay.PacketRelay] for DNS queries. See the package documentation.
//
// Multiple goroutines can simultaneously invoke methods on a Relay.
type Relay struct {
	dial     DialFunc
	resolver netip.AddrPort
	fallback packetrelay.PacketRelay
	cfg      Config

	mu           sync.Mutex
	conn         *conn // The current connection, or nil.
	pending      map[uint16]*query
	tcpDownUntil time.Time // Until when queries go to the fallback relay.

	randID func() uint16 // Returns a random ID. Replaceable in tests.
}

var _ packetrelay.PacketRelay = (*Relay)(nil)

// conn is a connection to the resolver. Its fields are protected by Relay.mu, except sc after
// ready is closed and the write lock.
type conn struct {
	ready chan struct{} // Closed when the dial finishes.
	sc    transport.StreamConn
	wmu   sync.Mutex // Serializes writes.

	closed     bool
	answered   bool        // Whether the connection delivered a response.
	lastAnswer time.Time   // When it last delivered a response.
	idle       *time.Timer // Closes the connection when it is idle.
}

// query is a query in flight.
type query struct {
	assoc    *association
	id       uint16 // The ID sent to the resolver.
	origID   [2]byte
	payload  []byte // A copy of the original query.
	question []byte // See parseQuestion. Nil if the query could not be parsed.
	nameLen  int
	maxUDP   int // The largest response the client accepts over UDP.
	conn     *conn
	resent   bool
	sentAt   time.Time
	timer    *time.Timer
}

// NewRelay creates a [Relay] that sends queries for resolver over connections made by dial.
// Queries go to fallback when TCP fails. If fallback is nil, they are answered with a
// truncated response instead.
func NewRelay(dial DialFunc, resolver netip.AddrPort, fallback packetrelay.PacketRelay, cfg Config) (*Relay, error) {
	if dial == nil {
		return nil, errors.New("dial must not be nil")
	}
	if !resolver.IsValid() {
		return nil, errors.New("invalid resolver address")
	}
	return &Relay{
		dial:     dial,
		resolver: resolver,
		fallback: fallback,
		cfg:      cfg.withDefaults(),
		pending:  make(map[uint16]*query),
		randID:   randomID,
	}, nil
}

// NewAssociation implements [packetrelay.PacketRelay]. The returned association is virtual:
// it does not open a socket. It closes itself after [Config.AssociationTimeout].
func (r *Relay) NewAssociation() (packetrelay.PacketSender, packetrelay.PacketReceiver, error) {
	a := &association{
		relay:     r,
		responses: make(chan response, 4),
		done:      make(chan struct{}),
	}
	a.mu.Lock()
	a.expiry = time.AfterFunc(r.cfg.AssociationTimeout, func() { _ = a.Close() })
	a.mu.Unlock()
	return a, a, nil
}

// Reset closes the connection, retires the queries in flight and tries TCP again. Call it
// when the underlying network changes.
func (r *Relay) Reset() {
	r.mu.Lock()
	c := r.conn
	r.conn = nil
	r.tcpDownUntil = time.Time{}
	for id, q := range r.pending {
		delete(r.pending, id)
		q.timer.Stop()
	}
	sc := r.closeLocked(c)
	r.mu.Unlock()
	if sc != nil {
		_ = sc.Close()
	}
}

// closeLocked marks c closed and returns its stream to close, if any.
func (r *Relay) closeLocked(c *conn) transport.StreamConn {
	if c == nil || c.closed {
		return nil
	}
	c.closed = true
	if r.conn == c {
		r.conn = nil
	}
	if c.idle != nil {
		c.idle.Stop()
	}
	return c.sc
}

// send forwards the query p from a.
func (r *Relay) send(a *association, p []byte, dst netip.AddrPort) error {
	if len(p) < dnsHeaderLen {
		return errors.New("invalid DNS message: too short")
	}
	if dst.Addr().Unmap() != r.resolver.Addr().Unmap() || dst.Port() != r.resolver.Port() {
		return errWrongResolver
	}

	r.mu.Lock()
	if time.Now().Before(r.tcpDownUntil) {
		r.mu.Unlock()
		r.sendFallback(a, p)
		return nil
	}
	if len(r.pending) >= maxPending {
		r.mu.Unlock()
		return ErrTooManyQueries
	}
	q := &query{assoc: a, id: r.newIDLocked(), payload: append([]byte(nil), p...), sentAt: time.Now()}
	copy(q.origID[:], p[:2])
	q.question, q.nameLen = parseQuestion(q.payload)
	q.maxUDP = maxUDPResponse(q.payload)
	q.conn = r.connLocked()
	r.pending[q.id] = q
	q.timer = time.AfterFunc(r.cfg.QueryTimeout, func() { r.timeout(q) })
	c := q.conn
	r.mu.Unlock()

	go r.write(c, q)
	return nil
}

// connLocked returns the current connection, starting a new one if needed.
func (r *Relay) connLocked() *conn {
	if r.conn != nil && !r.conn.closed {
		return r.conn
	}
	c := &conn{ready: make(chan struct{})}
	r.conn = c
	go r.dialConn(c)
	return c
}

func (r *Relay) dialConn(c *conn) {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.DialTimeout)
	defer cancel()
	sc, err := r.dial(ctx)

	r.mu.Lock()
	if err != nil {
		r.mu.Unlock()
		slog.Warn("DNS over TCP: failed to connect to the resolver", "err", err)
		r.tcpFailed(c)
		close(c.ready)
		return
	}
	if c.closed { // Reset while dialing.
		r.mu.Unlock()
		close(c.ready)
		_ = sc.Close()
		return
	}
	c.sc = sc
	r.mu.Unlock()
	close(c.ready)
	go r.readLoop(c)
}

// write sends q on c.
func (r *Relay) write(c *conn, q *query) {
	<-c.ready
	r.mu.Lock()
	sc, closed, current := c.sc, c.closed, r.pending[q.id] == q && q.conn == c
	r.mu.Unlock()
	if sc == nil || closed || !current {
		return // The query was moved, answered or retired.
	}
	frame := make([]byte, 2+len(q.payload))
	binary.BigEndian.PutUint16(frame, uint16(len(q.payload)))
	copy(frame[2:], q.payload)
	binary.BigEndian.PutUint16(frame[2:], q.id)

	c.wmu.Lock()
	_ = sc.SetWriteDeadline(time.Now().Add(r.cfg.DialTimeout))
	_, err := sc.Write(frame)
	c.wmu.Unlock()
	if err != nil {
		r.connFailed(c, err)
	}
}

func (r *Relay) readLoop(c *conn) {
	br := bufio.NewReader(c.sc)
	var lenBuf [2]byte
	for {
		if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
			r.connFailed(c, err)
			return
		}
		msg := make([]byte, binary.BigEndian.Uint16(lenBuf[:]))
		if _, err := io.ReadFull(br, msg); err != nil {
			r.connFailed(c, err)
			return
		}
		r.handleResponse(c, msg)
	}
}

// handleResponse delivers msg to the query it answers, if any.
func (r *Relay) handleResponse(c *conn, msg []byte) {
	if len(msg) < dnsHeaderLen {
		return
	}
	id := binary.BigEndian.Uint16(msg)
	r.mu.Lock()
	q := r.pending[id]
	if q == nil || q.conn != c || !q.answeredBy(msg) {
		r.mu.Unlock()
		return // Late, duplicate or unsolicited.
	}
	delete(r.pending, id)
	q.timer.Stop()
	c.answered = true
	c.lastAnswer = time.Now()
	r.armIdleLocked(c)
	r.mu.Unlock()

	var resp []byte
	if len(msg) > q.maxUDP {
		resp = truncatedResponse(q.payload)
	} else {
		resp = msg
		copy(resp[:2], q.origID[:])
	}
	q.assoc.deliver(resp, r.resolver)
}

// armIdleLocked closes c after the idle timeout if no query is in flight on it then.
func (r *Relay) armIdleLocked(c *conn) {
	if c.closed {
		return
	}
	if c.idle == nil {
		c.idle = time.AfterFunc(r.cfg.IdleTimeout, func() { r.closeIfIdle(c) })
	} else {
		c.idle.Reset(r.cfg.IdleTimeout)
	}
}

func (r *Relay) closeIfIdle(c *conn) {
	r.mu.Lock()
	for _, q := range r.pending {
		if q.conn == c {
			r.mu.Unlock()
			return // Not idle: the next response re-arms the timer.
		}
	}
	sc := r.closeLocked(c)
	r.mu.Unlock()
	if sc != nil {
		_ = sc.Close()
	}
}

// connFailed handles the failure of c. If c never delivered a response, TCP is considered
// unavailable. Otherwise its queries are resent once on a new connection.
func (r *Relay) connFailed(c *conn, err error) {
	r.mu.Lock()
	if c.closed {
		r.mu.Unlock()
		return
	}
	if !c.answered {
		r.mu.Unlock()
		slog.Warn("DNS over TCP: connection failed before any response", "err", err)
		r.tcpFailed(c)
		return
	}
	sc := r.closeLocked(c)
	var resend []*query
	var next *conn
	for id, q := range r.pending {
		if q.conn != c {
			continue
		}
		if q.resent {
			delete(r.pending, id)
			q.timer.Stop()
			continue
		}
		if next == nil {
			next = r.connLocked()
		}
		q.resent = true
		q.conn = next
		resend = append(resend, q)
	}
	r.mu.Unlock()
	if sc != nil {
		_ = sc.Close()
	}
	for _, q := range resend {
		go r.write(next, q)
	}
}

// tcpFailed closes c, stops using TCP for a while, and moves the queries of c to the fallback.
func (r *Relay) tcpFailed(c *conn) {
	r.mu.Lock()
	sc := r.closeLocked(c)
	r.tcpDownUntil = time.Now().Add(r.cfg.RetryAfter)
	var moved []*query
	for id, q := range r.pending {
		if q.conn == c {
			delete(r.pending, id)
			q.timer.Stop()
			moved = append(moved, q)
		}
	}
	r.mu.Unlock()
	if sc != nil {
		_ = sc.Close()
	}
	for _, q := range moved {
		r.sendFallback(q.assoc, q.payload)
	}
}

// timeout gives up on q. If its connection received nothing since q was sent, the connection is
// considered stuck.
func (r *Relay) timeout(q *query) {
	r.mu.Lock()
	if r.pending[q.id] != q {
		r.mu.Unlock()
		return
	}
	delete(r.pending, q.id)
	c := q.conn
	stuck := c != nil && !c.closed && !c.lastAnswer.After(q.sentAt)
	if c != nil && !stuck {
		r.armIdleLocked(c)
	}
	r.mu.Unlock()
	if stuck {
		r.connFailed(c, errStuck)
	}
}

// sendFallback sends the query p from a to the fallback relay, or answers it with a truncated
// response if there is none.
func (r *Relay) sendFallback(a *association, p []byte) {
	if r.fallback == nil {
		a.deliver(truncatedResponse(p), r.resolver)
		return
	}
	sender, receiver, err := r.fallback.NewAssociation()
	if err != nil {
		slog.Warn("DNS over TCP: failed to create fallback association", "err", err)
		return
	}
	go func() {
		_ = receiver.ReceivePackets(fallbackHandler{a: a, sender: sender})
	}()
	time.AfterFunc(r.cfg.QueryTimeout, func() { _ = sender.Close() })
	if err := sender.SendPacket(p, r.resolver); err != nil {
		_ = sender.Close()
	}
}

// fallbackHandler forwards the first response of a fallback association to a.
type fallbackHandler struct {
	a      *association
	sender packetrelay.PacketSender
}

func (h fallbackHandler) HandlePacket(p []byte, source netip.AddrPort) error {
	h.a.deliver(append([]byte(nil), p...), source)
	_ = h.sender.Close()
	return nil
}

// newIDLocked returns a random ID that no query in flight is using.
func (r *Relay) newIDLocked() uint16 {
	for {
		id := r.randID()
		if _, used := r.pending[id]; !used {
			return id
		}
	}
}

func randomID() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint16(b[:])
}

// removeAll forgets the queries of a.
func (r *Relay) removeAll(a *association) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, q := range r.pending {
		if q.assoc == a {
			delete(r.pending, id)
			q.timer.Stop()
		}
	}
}

// answeredBy reports whether the DNS response p carries the question of q.
func (q *query) answeredBy(p []byte) bool {
	if q.question == nil {
		return true // The query could not be parsed: match by ID only.
	}
	question, nameLen := parseQuestion(p)
	if question == nil || len(question) != len(q.question) || nameLen != q.nameLen {
		return false
	}
	// The name compares case-insensitively, because resolvers may echo it in a different case.
	// QDCOUNT, QTYPE and QCLASS compare exactly.
	return bytes.Equal(question[:2], q.question[:2]) &&
		equalFoldASCII(question[2:nameLen], q.question[2:nameLen]) &&
		bytes.Equal(question[nameLen:], q.question[nameLen:])
}

// equalFoldASCII reports whether a and b are equal, treating ASCII letters case-insensitively.
// Unlike bytes.EqualFold it compares raw bytes, which DNS names are.
func equalFoldASCII(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// questionEnd returns the offset just past the first question of the DNS message p, or -1 if
// p has no question or it cannot be parsed, for example because the name is compressed.
func questionEnd(p []byte) int {
	if len(p) < dnsHeaderLen || binary.BigEndian.Uint16(p[4:6]) == 0 {
		return -1
	}
	i := dnsHeaderLen
	for {
		if i >= len(p) {
			return -1
		}
		labelLen := int(p[i])
		if labelLen == 0 {
			i++
			break
		}
		if labelLen&0xc0 != 0 {
			return -1 // Compressed or reserved label type.
		}
		i += 1 + labelLen
	}
	if i+4 > len(p) { // QTYPE and QCLASS.
		return -1
	}
	return i + 4
}

// parseQuestion returns the QDCOUNT field followed by the first question (QNAME, QTYPE, QCLASS)
// of the DNS message p, and the offset where the QNAME ends in the returned slice. It returns nil
// if p has no question or the question cannot be parsed.
func parseQuestion(p []byte) (question []byte, nameLen int) {
	end := questionEnd(p)
	if end < 0 {
		return nil, 0
	}
	question = append(append([]byte(nil), p[4:6]...), p[dnsHeaderLen:end]...)
	return question, 2 + (end - 4) - dnsHeaderLen
}

// maxUDPResponse returns the largest response the client that sent query accepts over UDP: its
// EDNS buffer size (RFC 6891) between 512 and maxUDPSize, or 512 without EDNS.
func maxUDPResponse(query []byte) int {
	end := questionEnd(query)
	if end < 0 || binary.BigEndian.Uint16(query[10:12]) == 0 || binary.BigEndian.Uint16(query[4:6]) != 1 {
		return minUDPSize
	}
	// The OPT record has the root name (a single zero byte), TYPE 41, and the buffer size in
	// CLASS. It is the only additional record in a typical query.
	if end+5 > len(query) || query[end] != 0 || binary.BigEndian.Uint16(query[end+1:]) != dnsTypeOPT {
		return minUDPSize
	}
	size := int(binary.BigEndian.Uint16(query[end+3:]))
	return min(max(size, minUDPSize), maxUDPSize)
}

// truncatedResponse returns a response to query with the TC (truncated) bit set and only the
// question, which tells the client to retry over TCP.
func truncatedResponse(query []byte) []byte {
	end := questionEnd(query)
	if end < 0 {
		end = dnsHeaderLen
	}
	resp := append([]byte(nil), query[:end]...)
	resp[dnsFlagsByte] |= dnsResponseBit | dnsTruncatedBit
	resp[dnsRCodeByte] &^= dnsRCodeMask
	binary.BigEndian.PutUint16(resp[6:], 0)  // ANCOUNT
	binary.BigEndian.PutUint16(resp[8:], 0)  // NSCOUNT
	binary.BigEndian.PutUint16(resp[10:], 0) // ARCOUNT
	if end == dnsHeaderLen {
		binary.BigEndian.PutUint16(resp[4:], 0) // QDCOUNT
	}
	return resp
}

type response struct {
	payload []byte
	source  netip.AddrPort
}

// association is a virtual association for the queries of one caller.
type association struct {
	relay     *Relay
	responses chan response
	done      chan struct{}
	closeOnce sync.Once

	mu     sync.Mutex
	expiry *time.Timer // Closes the association after the association timeout.
}

var _ packetrelay.PacketSender = (*association)(nil)
var _ packetrelay.PacketReceiver = (*association)(nil)

// SendPacket implements [packetrelay.PacketSender].
func (a *association) SendPacket(p []byte, destination netip.AddrPort) error {
	select {
	case <-a.done:
		return packetrelay.ErrClosed
	default:
	}
	return a.relay.send(a, p, destination)
}

// Close implements [packetrelay.PacketSender]. It makes ReceivePackets return.
func (a *association) Close() error {
	err := packetrelay.ErrClosed
	a.closeOnce.Do(func() {
		close(a.done)
		a.mu.Lock()
		a.expiry.Stop()
		a.mu.Unlock()
		a.relay.removeAll(a)
		err = nil
	})
	return err
}

// ReceivePackets implements [packetrelay.PacketReceiver].
func (a *association) ReceivePackets(handler packetrelay.PacketHandler) error {
	for {
		select {
		case resp := <-a.responses:
			if err := handler.HandlePacket(resp.payload, resp.source); err != nil {
				return err
			}
		case <-a.done:
			return packetrelay.ErrClosed
		}
	}
}

// deliver queues a response for ReceivePackets. It drops the response if the association is
// closed or the caller is not keeping up.
func (a *association) deliver(payload []byte, source netip.AddrPort) {
	select {
	case <-a.done:
	case a.responses <- response{payload, source}:
	default:
	}
}
