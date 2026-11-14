// Copyright 2025 The Outline Authors
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
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"sync/atomic"
	"time"

	"localhost/client/go/outline/connectivity"
	"localhost/client/go/outline/dnsx"

	"golang.getoutline.org/sdk/network/dnsintercept"
	"golang.getoutline.org/sdk/network/dnstruncate"
	"golang.getoutline.org/sdk/network/packetrelay"
	"golang.getoutline.org/sdk/transport"
)

// A list of public DNS resolvers that the VPN can use.
var outlineDNSResolvers = []netip.AddrPort{
	netip.MustParseAddrPort("1.1.1.1:53"),        // Cloudflare
	netip.MustParseAddrPort("9.9.9.9:53"),        // Quad9
	netip.MustParseAddrPort("208.67.222.222:53"), // OpenDNS
	netip.MustParseAddrPort("208.67.220.220:53"), // OpenDNS
}

// A hard-coded link-local address for DNS interception.
//
// TODO: make this configurable via a new VpnConfig
var linkLocalDNS = netip.MustParseAddrPort("169.254.113.53:53")

// wrapTransportPairWithOutlineDNS intercepts DNS over TCP and UDP at a link-local address and forwards them to the remote resolver.
//
// It also checks for UDP connectivity.
//   - If UDP is available, it forwards DNS queries to the specified resolverAddr.
//   - If UDP is blocked, it sends back a truncated DNS response.
//     This forces the OS to retry the DNS query over TCP.
func wrapTransportPairWithOutlineDNS(sd *Dialer[transport.StreamConn], pl *PacketListener) (*TransportPair, error) {
	// Randomly selects a DNS resolver for the VPN session
	remoteDNS := outlineDNSResolvers[rand.IntN(len(outlineDNSResolvers))]

	// Intercept DNS for StreamDialer: remap TCP connections to linkLocalDNS → remoteDNS.
	sdForward := func(ctx context.Context, addr string) (transport.StreamConn, error) {
		if dst, err := netip.ParseAddrPort(addr); err == nil && dst.Addr().Unmap() == linkLocalDNS.Addr() && dst.Port() == linkLocalDNS.Port() {
			addr = remoteDNS.String()
		}
		return sd.Dial(ctx, addr)
	}

	baseListener, err := packetrelay.NewPacketRelayFromPacketListener(pl.PacketListener, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create base PacketRelay: %w", err)
	}
	// Forward relay: intercept DNS at link-local address, forward to remote resolver.
	// DNS gets a shorter 5s timeout on its own independent listener.
	dnsListener, err := packetrelay.NewPacketRelayFromPacketListener(pl.PacketListener, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS PacketRelay: %w", err)
	}
	// Each distinct query keeps its own short-lived association, so the proxy sends it from a
	// fresh, unpredictable source port (RFC 5452). Copies of a query in flight, such as OS
	// retransmissions, share one exchange instead of opening one association each, and unanswered
	// queries are retried a bounded number of times.
	dnsExchanger := dnsx.NewDedupExchanger(
		dnsx.NewRetryExchanger(dnsintercept.NewPacketRelayExchanger(dnsListener, remoteDNS),
			dnsx.RetryOptions{Attempts: 3, Interval: time.Second}),
		dnsx.DedupOptions{Timeout: 5 * time.Second})
	relayForward := dnsintercept.New(baseListener, linkLocalDNS, dnsExchanger)
	// Truncate relay: intercept DNS at link-local address, return truncated response (forces TCP retry).
	// Non-DNS traffic passes through to baseListener.
	dnsTruncRelay, err := dnstruncate.NewPacketRelay()
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS truncate relay: %w", err)
	}
	relayTrunc := dnsintercept.NewInterceptDNSPacketRelay(dnsTruncRelay, baseListener, linkLocalDNS, remoteDNS)
	// Delegate relay starts with truncate (UDP unverified), switches to forward when UDP is healthy.
	relayMain, err := packetrelay.NewDelegatePacketRelay(relayTrunc)
	if err != nil {
		return nil, fmt.Errorf("failed to create delegate PacketRelay: %w", err)
	}

	// Each network change starts a UDP check that can take seconds. Checks started on an older
	// network can finish after newer ones, so only the most recent check may switch the relay;
	// otherwise a stale failure would leave DNS in truncate mode on a healthy network.
	var checkGen atomic.Uint64
	onNetworkChanged := func() {
		gen := checkGen.Add(1)
		go func() {
			err := connectivity.CheckUDPConnectivity(pl)
			if checkGen.Load() != gen {
				slog.Debug("discarding superseded UDP check", "err", err)
				return
			}
			if err == nil {
				slog.Info("remote device UDP is healthy")
				relayMain.SetRelay(relayForward)
			} else {
				slog.Warn("remote device UDP is not healthy", "err", err)
				relayMain.SetRelay(relayTrunc)
			}
		}()
	}

	return &TransportPair{
		&Dialer[transport.StreamConn]{sd.ConnectionProviderInfo, sdForward},
		&PacketRelay{pl.ConnectionProviderInfo, relayMain, onNetworkChanged},
	}, nil
}
