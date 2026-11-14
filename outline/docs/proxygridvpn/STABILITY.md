# ProxyGridVPN client: stability fixes and root causes

Baseline for comparison: stable Outline v1.17.0 (`client_macos/v1.19.1` points to the same
commit). Each fix below is one commit on `feat/gridvpn-client`. "Regression" means the bug was
introduced after v1.17.0; "pre-existing" means v1.17.0 had it too.

## DNS (all platforms)

### DNS retransmissions exhausted the server's UDP associations (regression)

- **Symptom:** under bursts of DNS (page loads, app start), lookups time out after the server's
  per-client UDP association budget is used up.
- **Root cause:** since #2777 the DNS interceptor relays each intercepted packet on its own
  short-lived association. That keeps a fresh, unpredictable proxy source port per query
  (RFC 5452), but every *copy* of a query, such as each OS retransmission, also opens another
  association.
- **Fix:** answer intercepted queries through a dedup exchanger over a bounded retry exchanger
  over the SDK's packet-relay exchanger (the DNS Exchanger direction the Outline maintainers
  asked for, outline-sdk#637). Distinct queries still get one association each, so the RFC 5452
  protections are unchanged; identical queries in flight share one exchange whose response is
  validated (ID and question) and returned to each copy with its own ID; unanswered queries are
  retried at most twice, each on a fresh association.
- **Not done:** reusing one UDP association for all DNS, which upstream rejected because it
  weakens RFC 5452 source-port randomisation.
- **Evidence:** `TestE2E_DNSRetransmissionsShareOneExchange` sends 20 overlapping copies of a
  query through a real Shadowsocks server; the resolver sees it once and every copy is answered.

### A stale UDP health check could leave DNS in truncate mode (regression)

- **Root cause:** each network change starts a UDP check (up to ~8 s) in its own goroutine with
  no ordering. A check started on the old network could fail after a newer one succeeded and
  switch DNS to the truncate-to-TCP relay until the next path change. v1.17.0 ran the check
  synchronously.
- **Fix:** checks carry a generation number; only the latest may switch the relay.

## Windows / Linux (Electron)

### tun2socks stayed alive after its traffic relay died (regression)

- **Root cause:** after the move to the `vpn` RemoteDevice API, the Windows tun2socks binary
  relays in two goroutines and then only waits for a signal. When a relay returns (TAP device
  reset across sleep/resume, read error) the process does not exit, so the Electron side never
  restarts it: routes point at a dead device and all traffic is dropped while the UI says
  Connected. v1.17.0 exited on a copy error.
- **Fix:** exit with `ERR_DATA_TRANSMISSION_FAILURE` when either relay returns, after closing
  the devices and ending the session, so auto-restart runs.

### Windows DNS bypassed the interceptor (regression)

- **Root cause:** `tun.OpenTunDevice` pushes the `-tunDNS` servers to the TAP adapter over DHCP,
  overriding the static `169.254.113.53` set by `add_tap_device.bat`. With `1.1.1.1/9.9.9.9`
  there, DNS never reached the Go interceptor (which only answers `169.254.113.53`) and went out
  as raw UDP with no TCP fallback; on networks or servers without UDP, every lookup timed out.
  The `-dnsFallback` flag was still passed but no longer implemented.
- **Fix:** give the TAP adapter the link-local resolver address.

### Disconnect during connect left an orphan tunnel (pre-existing)

- **Root cause:** `disconnect()` set its flag only at the end and `connect()` never checked it.
  A connect still in its connectivity check or route setup went on to start tun2socks with routes
  applied, leaving a tunnel nothing tracked and the TAP device and service pipe held. Two
  overlapping StartProxying calls could also both pass the "already connected" check.
- **Fix:** set the flag first and back out (undoing routes) after every connect step; serialize
  starts; a stop bumps a generation counter so a start still resolving the server address
  cannot connect afterwards.

### Restarts raced each other and disconnect (pre-existing)

- **Root cause:** network-change and resume restarts ran concurrently ("already launched"), could
  restart tun2socks after a disconnect, and left rejections unhandled.
- **Fix:** restarts are queued one at a time and re-check the disconnected flag.

### Auto-restart stopped working after the first suspend (pre-existing)

- **Root cause:** `GoTun2socks.stopRequested` was set by `stop()` and never reset, so after one
  suspend or network change a later crash was not restarted.
- **Fix:** reset it on every start.

### powerMonitor listeners leaked (pre-existing)

- **Root cause:** listeners were added and removed with fresh `.bind(this)` functions, so removal
  never matched; every connect added two more, and old tunnels kept reacting to suspend.
- **Fix:** bind once and reuse the reference.

### Linux: one failed close broke every later connect (pre-existing)

- **Root cause:** `closeVPNNoLock` cleared the global connection only if `platform.Close()`
  succeeded; when NetworkManager did not confirm the TUN removal in time, every later
  `EstablishVPN` failed until the app restarted.
- **Fix:** always forget the connection, logging the close error.

## Android

### Teardown raced the start (regression)

- **Root cause:** `tearDownActiveTunnel` was not synchronized while `startTunnel` is. Since the
  health check moved after `establish()`, a revoke, service destroy or tile stop during that check
  (up to 10 s) closed the TUN descriptor under the start, which then crashed on
  `tunFd.getFd()`, or finished after the teardown and left the network callback registered with
  the stored status CONNECTED and no VPN (tile ON, reconnect at next boot).
- **Fix:** teardown takes the same lock; start re-checks the descriptor.

### Stored status was wrong after stops and lost after reboots (regression)

- **Root cause:** the status was stored before checking that a tunnel exists, so late network
  callbacks wrote CONNECTED after a stop, and `onLost` stored RECONNECTING, which the boot
  starter did not treat as connected (no restore after a reboot in airplane mode).
- **Fix:** store only for a live tunnel; restore from RECONNECTING too.

### Network callbacks piled up on server switches (regression)

- **Root cause:** switching servers called `requestNetwork` again with the same callback.
- **Fix:** unregister first.

### Tile start rebuilt a connection in progress (regression)

- **Fix:** ignore the tile's start while a tunnel is active or starting.

### The packet relay stopped on the first bad packet (regression, all platforms using it)

- **Root cause:** `RelayTraffic` used one `io.Copy`; the first write error (for example the
  network stack rejecting a malformed packet) or transient read error ended the relay and closed
  the device, black-holing traffic while the UI showed Connected. v1.17.0 skipped such packets.
- **Fix:** copy packet by packet, drop failed packets, bound transient read errors, stop only
  when either side is closed. Unit tests cover each case.

## macOS / iOS

### Stale remote device after stop (regression)

- **Root cause:** the provider never cleared `remoteDevice`, so a later start in the same process
  was treated as a restart, closed an already closed device again and could skip starting the
  packetFlow read loop: connected, but reading nothing.
- **Fix:** clear the device after every close; start the read loop exactly once per provider.

### Stop overlapping start (pre-existing)

- **Root cause:** a stop during `setTunnelNetworkSettings` let the start completion relay on the
  closed device and re-register the path observer, which then drove reconnects on a stopped
  tunnel.
- **Fix:** a stopped flag; late completions and network changes are ignored.

### IPv6 leak (pre-existing)

- **Root cause:** only IPv4 settings were applied, so IPv6 traffic (including connections to AAAA
  answers resolved through the tunnel) left through the physical interface.
- **Fix:** route IPv6 into the tunnel (lwIP is built with IPv6) and write packets with the
  protocol taken from the IP version nibble. Android already blocks IPv6 by not configuring it.

### Crash scanning interfaces (pre-existing)

- **Root cause:** `ifa_addr` was force-unwrapped; getifaddrs(3) allows NULL.
- **Fix:** skip interfaces without an address.

### Fake DNS address inside an excluded route (pre-existing, defensive)

- **Fix:** route `169.254.113.53/32` into the tunnel explicitly, as Android and Windows do.

### App-side start/stop could hang (pre-existing)

- **Root cause:** status observers were registered after `startTunnel` (inside a Task) and after
  `stopVPNTunnel`; `stopActiveVpn` on quit waited for a `.disconnected` event an idle VPN never
  posts.
- **Fix:** register observers first; for stop, re-check the status after the call.

### No wake handling (pre-existing)

- **Root cause:** after sleep on an unchanged network no path-change event fires, so the UDP mode
  was never re-checked while server-side associations may have expired.
- **Fix:** `wake()` notifies the remote device of a network change.

## Known remaining gaps

- The SDK reports some IPv4 UDP sources in IPv4-mapped form (`::ffff:a.b.c.d`); it is the same
  endpoint, but worth normalising upstream.
- Android IPv6 is blocked rather than tunnelled; tunnelling it needs server IPv6 egress.
- Apple sleep/wake and Wi-Fi/cellular switching need on-device verification (see TESTING.md).
