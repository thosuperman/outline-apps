# GridVPN test results (2026-11-14)

Branch `feat/gridvpn-client`. "Automated" means it runs in `npm run action client/web/test`,
`go test ./client/go/...`, the Apple `VpnExtensionTest` suite, or CI
(`.github/workflows/gridvpn_builds.yml`). No live VPN was brought up on a device in this round:
that changes the machine's routing and needs a key entered by the owner (see "Not yet tested").

## Shared (all platforms)

| Area | Test | Result |
|---|---|---|
| Outline key compatibility | SIP002 `ss://`, legacy base64 `ss://`, YAML dynamic-key config through a real Shadowsocks server (`TestE2E_OutlineKeyFormats`) | Pass |
| Wrong credentials | Key with a wrong secret gets no data through (`TestE2E_WrongSecretIsRejected`) | Pass |
| TCP | 280 KB echo through Shadowsocks, AEAD chunking (`TestE2E_TCPAndUDPRelay`) | Pass |
| UDP | Datagram echo through Shadowsocks | Pass |
| DNS | Query to the link-local interceptor `169.254.113.53` answered, ID kept, source preserved (`TestE2E_DNSInterceptedAtLinkLocalAddress`) | Pass |
| DNS under bursts | 20 overlapping copies of one query reach the resolver once; all 20 answered (`TestE2E_DNSRetransmissionsShareOneExchange`) | Pass |
| DNS dedup/retry unit tests | 19 cases incl. cancellation, timeouts, invalid responses, goroutine leaks (`dnsx`) | Pass |
| DNS interceptor wiring | Dedup + retry over the packet relay (`configregistry`) | Pass |
| Network interruption / reconnection | Server goes down, traffic fails, server returns on the same address, client recovers without being recreated (`TestE2E_ReconnectsAfterServerRestart`) | Pass |
| Resource cleanup | 5 session start/stop cycles return to the goroutine baseline (`TestE2E_SessionCleanupReleasesGoroutines`) | Pass (after making the test server expire idle associations) |
| Packet relay | Drops rejected packets and continues, survives transient read errors, stops on close, gives up on persistent errors (`vpn/relay_test.go`) | Pass |
| All Go packages | `go test -race ./client/go/...` | Pass locally (macOS); CI: see below |
| Web UI | 105 Jasmine specs in headless Chromium, incl. 40 new: Cognito SRP proof checked against a server-side SRP implementation, Cognito flows, ProxyGrid API shapes and errors, session refresh (single-flight, revoked vs offline), account provisioning (reuse key, over quota, 401 retry, sign-out keeps own servers), keychain storage migration and write-through | Pass (local and CI) |
| Quota handling | Over-quota status blocks provisioning with an explanation; UI disables connect and links to upgrade; the backend's dynamic key also returns the "data limit reached" message | Pass (unit + visual check in browser) |
| Concurrency | Concurrent token refresh shares one request; identical DNS queries share one exchange; Electron starts serialized (code review) | Pass (unit) |

## macOS

| Area | Result |
|---|---|
| Build: signed universal app with Network Extension, team 797NU6L392 | **Built**: `GridVPN-dev-macos-universal.dmg`, `codesign --verify --deep --strict` OK, extension entitled `packet-tunnel-provider` |
| `VpnExtensionTest` (tunnel settings incl. new IPv6 and DNS routes, error persistence, address selection) | Pass |
| Connect/disconnect, DNS, sleep/wake, Wi-Fi switch on a real Mac | Not yet tested (needs the owner to add a key and approve live tests) |

## iOS

| Area | Result |
|---|---|
| Build: signed `.ipa` with the VPN extension (development, 3 registered devices) | **Built**: `GridVPN-dev-ios.ipa` |
| On-device connect, background/foreground, network switching | Not yet tested |

## Android

| Area | Result |
|---|---|
| Build (CI) | See CI section |
| Lifecycle fixes (teardown lock, stored status, callback re-registration, tile start) | Code review against the audit; no device test |
| On-device connect, Doze, reboot restore, Quick Settings tile | Not yet tested (no Android device or emulator run in this round) |

## Windows

| Area | Result |
|---|---|
| Build (CI, NSIS `.exe`, ia32 and arm64) | See CI section |
| Relay-exit restart, DNS via link-local resolver, connect/disconnect races, sleep/resume | Code review; `go vet` on the host; no Windows machine test |

## CI

Run on the fork for `feat/gridvpn-client`: see the "GridVPN / Builds" workflow. Results are
recorded in the final report.

## Not yet tested (needs the owner)

- Any live tunnel on a real device or computer (routing changes; the owner enters keys).
- ProxyGrid sign-in against the real Cognito pool (identifiers not configured in builds yet; the
  backend is not deployed, so `/vpn/*` endpoints and dynamic keys cannot be exercised).
- Side-by-side install with the official Outline app (needs the final bundle IDs).
