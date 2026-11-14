// Copyright 2026 ProxyGrid
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

package outline

// End-to-end tests of the client transport against a real (in-process) Shadowsocks server on
// the loopback interface: Outline access-key parsing, AEAD encryption, TCP and UDP relaying,
// the link-local DNS interceptor with its dedup/retry exchanger, server restarts and cleanup.
// Nothing leaves the machine and no system network settings are touched: the server sends
// every DNS query (UDP port 53) to a local fake resolver and only serves loopback targets.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.getoutline.org/sdk/transport/shadowsocks"
	"golang.org/x/net/dns/dnsmessage"
)

const (
	e2eCipher = "chacha20-ietf-poly1305"
	e2eSecret = "gridvpn-e2e-secret"
)

var linkLocalDNSAddr = netip.MustParseAddrPort("169.254.113.53:53")

// --- local targets ---

func startTCPEcho(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return l.Addr().String()
}

func startUDPEcho(t *testing.T) netip.AddrPort {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pc.WriteTo(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).AddrPort()
}

// fakeDNS answers every A query with 10.1.2.3 and counts the queries it receives.
type fakeDNS struct {
	addr    netip.AddrPort
	queries atomic.Int32
	delay   time.Duration
}

func startFakeDNS(t *testing.T, delay time.Duration) *fakeDNS {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { pc.Close() })
	f := &fakeDNS{addr: pc.LocalAddr().(*net.UDPAddr).AddrPort(), delay: delay}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			f.queries.Add(1)
			query := append([]byte(nil), buf[:n]...)
			go func() {
				time.Sleep(f.delay)
				if resp, err := answerA(query); err == nil {
					pc.WriteTo(resp, addr)
				}
			}()
		}
	}()
	return f
}

func answerA(query []byte) ([]byte, error) {
	var p dnsmessage.Parser
	hdr, err := p.Start(query)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: hdr.ID, Response: true, RecursionAvailable: true, RecursionDesired: hdr.RecursionDesired})
	b.StartQuestions()
	b.Question(q)
	b.StartAnswers()
	if q.Type == dnsmessage.TypeA {
		b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.AResource{A: [4]byte{10, 1, 2, 3}})
	}
	return b.Finish()
}

func newQuery(t *testing.T, id uint16, name string) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	b.StartQuestions()
	require.NoError(t, b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}))
	msg, err := b.Finish()
	require.NoError(t, err)
	return msg
}

// --- minimal Shadowsocks server ---

type ssServer struct {
	t         *testing.T
	key       *shadowsocks.EncryptionKey
	dns       netip.AddrPort
	tcp       net.Listener
	udp       net.PacketConn
	udpAssocs atomic.Int32
	wg        sync.WaitGroup
}

func startSSServer(t *testing.T, dns netip.AddrPort, addr string) *ssServer {
	key, err := shadowsocks.NewEncryptionKey(e2eCipher, e2eSecret)
	require.NoError(t, err)
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	tl, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	ul, err := net.ListenPacket("udp", tl.Addr().String())
	require.NoError(t, err)
	s := &ssServer{t: t, key: key, dns: dns, tcp: tl, udp: ul}
	s.wg.Add(2)
	go s.serveTCP()
	go s.serveUDP()
	t.Cleanup(s.Close)
	return s
}

func (s *ssServer) Addr() string { return s.tcp.Addr().String() }

func (s *ssServer) Close() {
	s.tcp.Close()
	s.udp.Close()
	s.wg.Wait()
}

// readSocksAddr parses a SOCKS5 address (ATYP, address, port).
func readSocksAddr(r io.Reader) (netip.AddrPort, error) {
	var atyp [1]byte
	if _, err := io.ReadFull(r, atyp[:]); err != nil {
		return netip.AddrPort{}, err
	}
	var ip netip.Addr
	switch atyp[0] {
	case 1:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return netip.AddrPort{}, err
		}
		ip = netip.AddrFrom4(b)
	case 4:
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return netip.AddrPort{}, err
		}
		ip = netip.AddrFrom16(b)
	default:
		return netip.AddrPort{}, fmt.Errorf("unsupported ATYP %d", atyp[0])
	}
	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil {
		return netip.AddrPort{}, err
	}
	return netip.AddrPortFrom(ip.Unmap(), binary.BigEndian.Uint16(port[:])), nil
}

func socksAddr(a netip.AddrPort) []byte {
	var out []byte
	if a.Addr().Is4() {
		ip := a.Addr().As4()
		out = append([]byte{1}, ip[:]...)
	} else {
		ip := a.Addr().As16()
		out = append([]byte{4}, ip[:]...)
	}
	return binary.BigEndian.AppendUint16(out, a.Port())
}

// route sends DNS to the fake resolver and only allows loopback targets otherwise.
func (s *ssServer) route(dst netip.AddrPort) (netip.AddrPort, error) {
	if dst.Port() == 53 {
		return s.dns, nil
	}
	if !dst.Addr().IsLoopback() {
		return netip.AddrPort{}, fmt.Errorf("test server refuses non-loopback target %v", dst)
	}
	return dst, nil
}

func (s *ssServer) serveTCP() {
	defer s.wg.Done()
	for {
		c, err := s.tcp.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			r := shadowsocks.NewReader(c, s.key)
			dst, err := readSocksAddr(r)
			if err != nil {
				return
			}
			target, err := s.route(dst)
			if err != nil {
				return
			}
			tc, err := net.Dial("tcp", target.String())
			if err != nil {
				return
			}
			defer tc.Close()
			go func() { io.Copy(tc, r); tc.(*net.TCPConn).CloseWrite() }()
			io.Copy(shadowsocks.NewWriter(c, s.key), tc)
		}()
	}
}

func (s *ssServer) serveUDP() {
	defer s.wg.Done()
	type assoc struct{ conn *net.UDPConn }
	var mu sync.Mutex
	assocs := map[string]*assoc{}
	defer func() {
		mu.Lock()
		for _, a := range assocs {
			a.conn.Close()
		}
		mu.Unlock()
	}()
	buf := make([]byte, 65535)
	for {
		n, client, err := s.udp.ReadFrom(buf)
		if err != nil {
			return
		}
		plain, err := shadowsocks.Unpack(nil, buf[:n], s.key)
		if err != nil {
			continue
		}
		rd := bytes.NewReader(plain)
		dst, err := readSocksAddr(rd)
		if err != nil {
			continue
		}
		payload := plain[len(plain)-rd.Len():]
		target, err := s.route(dst)
		if err != nil {
			continue
		}
		mu.Lock()
		a, ok := assocs[client.String()]
		if !ok {
			// One server-side socket per client association, as outline-ss-server does.
			conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				mu.Unlock()
				continue
			}
			a = &assoc{conn: conn}
			assocs[client.String()] = a
			s.udpAssocs.Add(1)
			go func(client net.Addr, conn *net.UDPConn) {
				rbuf := make([]byte, 65535)
				out := make([]byte, 65535+128)
				for {
					rn, from, err := conn.ReadFromUDPAddrPort(rbuf)
					if err != nil {
						return
					}
					src := netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
					if src == s.dns {
						src = netip.MustParseAddrPort("1.1.1.1:53")
					}
					pkt, err := shadowsocks.Pack(out, append(socksAddr(src), rbuf[:rn]...), s.key)
					if err == nil {
						s.udp.WriteTo(pkt, client)
					}
				}
			}(client, conn)
		}
		mu.Unlock()
		a.conn.WriteToUDPAddrPort(payload, target)
	}
}

// --- client helpers ---

func ssKey(serverAddr string) string {
	userInfo := base64.RawURLEncoding.EncodeToString([]byte(e2eCipher + ":" + e2eSecret))
	return fmt.Sprintf("ss://%s@%s#GridVPN%%20E2E", userInfo, serverAddr)
}

func newE2EClient(t *testing.T, config string) *Client {
	result := (&ClientConfig{DataDir: t.TempDir()}).New("e2e", config)
	require.Nil(t, result.Error, "client config %q", config)
	return result.Client
}

type packetCollector struct{ ch chan packetIn }
type packetIn struct {
	payload []byte
	src     netip.AddrPort
}

func (p *packetCollector) HandlePacket(b []byte, src netip.AddrPort) error {
	p.ch <- packetIn{append([]byte(nil), b...), src}
	return nil
}

func exchangePacket(t *testing.T, c *Client, dst netip.AddrPort, payload []byte) packetIn {
	sender, receiver, err := c.NewAssociation()
	require.NoError(t, err)
	defer sender.Close()
	col := &packetCollector{ch: make(chan packetIn, 4)}
	go receiver.ReceivePackets(col)
	require.NoError(t, sender.SendPacket(payload, dst))
	select {
	case got := <-col.ch:
		return got
	case <-time.After(10 * time.Second):
		t.Fatalf("no reply from %v", dst)
		return packetIn{}
	}
}

// --- tests ---

func TestE2E_OutlineKeyFormats(t *testing.T) {
	dns := startFakeDNS(t, 0)
	srv := startSSServer(t, dns.addr, "")
	echo := startTCPEcho(t)
	host, port, _ := net.SplitHostPort(srv.Addr())
	legacy := "ss://" + base64.StdEncoding.EncodeToString([]byte(e2eCipher+":"+e2eSecret+"@"+srv.Addr())) + "#Legacy"
	configs := map[string]string{
		"SIP002 ss:// key":    "transport: " + ssKey(srv.Addr()),
		"legacy base64 ss://": "transport: " + legacy,
		// Dynamic-key style YAML config (what an ssconf:// endpoint can return).
		"YAML tcpudp config": fmt.Sprintf("transport:\n  $type: tcpudp\n  tcp: &shared\n    $type: shadowsocks\n    endpoint: %s:%s\n    cipher: %s\n    secret: %s\n  udp: *shared\n", host, port, e2eCipher, e2eSecret),
	}
	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			c := newE2EClient(t, cfg)
			conn, err := c.DialStream(context.Background(), echo)
			require.NoError(t, err)
			defer conn.Close()
			_, err = conn.Write([]byte("hello " + name))
			require.NoError(t, err)
			got := make([]byte, len("hello "+name))
			_, err = io.ReadFull(conn, got)
			require.NoError(t, err)
			require.Equal(t, "hello "+name, string(got))
		})
	}
}

func TestE2E_WrongSecretIsRejected(t *testing.T) {
	dns := startFakeDNS(t, 0)
	srv := startSSServer(t, dns.addr, "")
	echo := startTCPEcho(t)
	userInfo := base64.RawURLEncoding.EncodeToString([]byte(e2eCipher + ":wrong-secret"))
	c := newE2EClient(t, fmt.Sprintf("transport: ss://%s@%s", userInfo, srv.Addr()))
	conn, err := c.DialStream(context.Background(), echo)
	if err == nil {
		defer conn.Close()
		conn.Write([]byte("x"))
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, err = conn.Read(make([]byte, 1))
	}
	require.Error(t, err, "a key with the wrong secret must not get data through")
}

func TestE2E_TCPAndUDPRelay(t *testing.T) {
	dns := startFakeDNS(t, 0)
	srv := startSSServer(t, dns.addr, "")
	echo := startTCPEcho(t)
	udpEcho := startUDPEcho(t)
	c := newE2EClient(t, "transport: "+ssKey(srv.Addr()))

	// TCP: a larger transfer, so AEAD chunking is exercised.
	conn, err := c.DialStream(context.Background(), echo)
	require.NoError(t, err)
	data := bytes.Repeat([]byte("gridvpn"), 40000)
	go conn.Write(data)
	got := make([]byte, len(data))
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err)
	require.Equal(t, data, got)
	conn.Close()

	// UDP.
	reply := exchangePacket(t, c, udpEcho, []byte("ping over udp"))
	require.Equal(t, "ping over udp", string(reply.payload))
	// The SDK may report IPv4 sources in IPv4-mapped form; compare the endpoint itself.
	require.Equal(t, udpEcho, netip.AddrPortFrom(reply.src.Addr().Unmap(), reply.src.Port()))
}

func TestE2E_DNSInterceptedAtLinkLocalAddress(t *testing.T) {
	dns := startFakeDNS(t, 0)
	srv := startSSServer(t, dns.addr, "")
	c := newE2EClient(t, "transport: "+ssKey(srv.Addr()))
	require.NoError(t, c.StartSession())
	defer c.EndSession()
	// Let the session's UDP health check switch DNS to forwarding mode.
	require.Eventually(t, func() bool { return dns.queries.Load() > 0 }, 10*time.Second, 50*time.Millisecond)

	query := newQuery(t, 0x4242, "example.gridvpn.test.")
	var reply packetIn
	require.Eventually(t, func() bool {
		reply = exchangePacket(t, c, linkLocalDNSAddr, query)
		var p dnsmessage.Parser
		hdr, err := p.Start(reply.payload)
		return err == nil && !hdr.Truncated
	}, 10*time.Second, 100*time.Millisecond, "DNS should be forwarded once UDP is healthy")

	var p dnsmessage.Parser
	hdr, err := p.Start(reply.payload)
	require.NoError(t, err)
	require.Equal(t, uint16(0x4242), hdr.ID, "response keeps the query ID")
	require.True(t, hdr.Response)
	require.Equal(t, linkLocalDNSAddr.String(), reply.src.String(), "the answer comes from the address the OS asked")
	require.NoError(t, p.SkipAllQuestions())
	ans, err := p.AnswerHeader()
	require.NoError(t, err)
	require.Equal(t, dnsmessage.TypeA, ans.Type)
	a, err := p.AResource()
	require.NoError(t, err)
	require.Equal(t, [4]byte{10, 1, 2, 3}, a.A)
}

func TestE2E_DNSRetransmissionsShareOneExchange(t *testing.T) {
	// A slow resolver makes the copies overlap, like an OS retransmitting an unanswered query.
	dns := startFakeDNS(t, 300*time.Millisecond)
	srv := startSSServer(t, dns.addr, "")
	c := newE2EClient(t, "transport: "+ssKey(srv.Addr()))
	require.NoError(t, c.StartSession())
	defer c.EndSession()
	require.Eventually(t, func() bool { return dns.queries.Load() > 0 }, 10*time.Second, 50*time.Millisecond)
	time.Sleep(500 * time.Millisecond) // health-check answer delivered
	before := dns.queries.Load()

	const copies = 20
	var wg sync.WaitGroup
	answered := atomic.Int32{}
	for i := 0; i < copies; i++ {
		wg.Add(1)
		go func(id uint16) {
			defer wg.Done()
			reply := exchangePacket(t, c, linkLocalDNSAddr, newQuery(t, id, "burst.gridvpn.test."))
			var p dnsmessage.Parser
			if hdr, err := p.Start(reply.payload); err == nil && hdr.ID == id && !hdr.Truncated {
				answered.Add(1)
			}
		}(uint16(1000 + i))
	}
	wg.Wait()
	require.Equal(t, int32(copies), answered.Load(), "every copy gets an answer with its own ID")
	sent := dns.queries.Load() - before
	require.Less(t, sent, int32(copies), "identical in-flight queries must not each reach the resolver")
	t.Logf("%d copies of one query reached the resolver %d time(s)", copies, sent)
}

func TestE2E_ReconnectsAfterServerRestart(t *testing.T) {
	dns := startFakeDNS(t, 0)
	srv := startSSServer(t, dns.addr, "")
	addr := srv.Addr()
	echo := startTCPEcho(t)
	c := newE2EClient(t, "transport: "+ssKey(addr))

	roundTrip := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, err := c.DialStream(ctx, echo)
		if err != nil {
			return err
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := conn.Write([]byte("ok")); err != nil {
			return err
		}
		buf := make([]byte, 2)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return err
		}
		if string(buf) != "ok" {
			return errors.New("bad echo")
		}
		return nil
	}
	require.NoError(t, roundTrip())

	srv.Close() // server goes away (network loss, node restart)
	require.Error(t, roundTrip(), "no traffic while the server is down")

	// Same address comes back: the client recovers without being recreated.
	startSSServer(t, dns.addr, addr)
	require.Eventually(t, func() bool { return roundTrip() == nil }, 10*time.Second, 200*time.Millisecond)
	c.NotifyNetworkChanged() // must be safe to call at any time
}

func TestE2E_SessionCleanupReleasesGoroutines(t *testing.T) {
	dns := startFakeDNS(t, 0)
	srv := startSSServer(t, dns.addr, "")
	udpEcho := startUDPEcho(t)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		c := newE2EClient(t, "transport: "+ssKey(srv.Addr()))
		require.NoError(t, c.StartSession())
		exchangePacket(t, c, udpEcho, []byte("x"))
		require.NoError(t, c.EndSession())
	}
	// Associations close when the sender closes; give the relays a moment to unwind.
	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= baseline+10
	}, 15*time.Second, 200*time.Millisecond, "goroutines: baseline %d, now %d", baseline, runtime.NumGoroutine())
}
