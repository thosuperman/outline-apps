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
	"time"

	"localhost/client/go/outline/connectivity"
	"localhost/client/go/outline/dnstcp"

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
// DNS queries received over UDP are forwarded to the resolver over a persistent TCP connection
// through the tunnel (see the dnstcp package). One connection carries all the queries, so DNS
// does not open a new UDP flow per query, and responses cannot be forged by off-path attackers.
//
// If TCP to the resolver fails, queries fall back to UDP, depending on UDP connectivity:
//   - If UDP is available, each query is forwarded over UDP.
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
	// Fallback for when TCP to the resolver fails. If UDP is available, forward each query over
	// UDP. DNS gets a shorter 5s timeout on its own independent listener.
	dnsListener, err := packetrelay.NewPacketRelayFromPacketListener(pl.PacketListener, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS PacketRelay: %w", err)
	}
	// If UDP is not available, return a truncated response (forces the OS to retry over TCP).
	dnsTruncRelay, err := dnstruncate.NewPacketRelay()
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS truncate relay: %w", err)
	}
	// The fallback starts with truncate (UDP unverified), switches to UDP when UDP is healthy.
	dnsFallback, err := packetrelay.NewDelegatePacketRelay(dnsTruncRelay)
	if err != nil {
		return nil, fmt.Errorf("failed to create delegate PacketRelay: %w", err)
	}

	dialResolver := func(ctx context.Context) (transport.StreamConn, error) {
		return sd.Dial(ctx, remoteDNS.String())
	}
	dnsTCP, err := dnstcp.NewRelay(dialResolver, remoteDNS, dnsFallback, dnstcp.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS-over-TCP relay: %w", err)
	}
	// Intercept DNS at the link-local address and forward it over TCP to the remote resolver.
	// Non-DNS traffic passes through to baseListener.
	relayMain := dnsintercept.NewInterceptDNSPacketRelay(dnsTCP, baseListener, linkLocalDNS, remoteDNS)

	onNetworkChanged := func() {
		// The DNS connection is bound to the old network.
		dnsTCP.Reset()
		go func() {
			if err := connectivity.CheckUDPConnectivity(pl); err == nil {
				slog.Info("remote device UDP is healthy")
				dnsFallback.SetRelay(dnsListener)
			} else {
				slog.Warn("remote device UDP is not healthy", "err", err)
				dnsFallback.SetRelay(dnsTruncRelay)
			}
		}()
	}

	return &TransportPair{
		&Dialer[transport.StreamConn]{sd.ConnectionProviderInfo, sdForward},
		&PacketRelay{pl.ConnectionProviderInfo, relayMain, onNetworkChanged},
	}, nil
}
