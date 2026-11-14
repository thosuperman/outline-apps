# ProxyGridVPN test results (2026-11-14)

Branch `feat/gridvpn-client`. "Automated" means it runs in `npm run action client/web/test`,
`go test ./client/go/...`, the Apple `VpnExtensionTest` suite, or CI
(`.github/workflows/proxygridvpn_builds.yml`). No live VPN was brought up on a device in this round:
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
| All Go packages | `go test -race ./client/go/...` | Pass locally (macOS) and in CI (Linux) |
| Web UI | 109 Jasmine specs in headless Chromium, incl. 44 new: `proxygridvpn://` app links, Cognito SRP proof checked against a server-side SRP implementation, Cognito flows, ProxyGrid API shapes and errors, session refresh (single-flight, revoked vs offline), account provisioning (reuse key, over quota, 401 retry, sign-out keeps own servers), keychain storage migration and write-through | Pass (local and CI) |
| Quota handling | Over-quota status blocks provisioning with an explanation; UI disables connect and links to upgrade; the backend's dynamic key also returns the "data limit reached" message | Pass (unit + visual check in browser) |
| Concurrency | Concurrent token refresh shares one request; identical DNS queries share one exchange; Electron starts serialized (code review) | Pass (unit) |

## macOS

| Area | Result |
|---|---|
| Build: signed universal app with Network Extension, team 797NU6L392 | **Built**: `ProxyGridVPN-1.0.0-dev-macos-universal.dmg`, bundle `net.proxygrid.vpn.macos`, app group `group.net.proxygrid.vpn`, `codesign --verify --deep --strict` OK, extension entitled `packet-tunnel-provider`, URL schemes ss/ssconf/proxygridvpn |
| `VpnExtensionTest` (tunnel settings incl. new IPv6 and DNS routes, error persistence, address selection) | Pass |
| Connect/disconnect, DNS, sleep/wake, Wi-Fi switch on a real Mac | Not yet tested (needs the owner to add a key and approve live tests) |

## iOS

| Area | Result |
|---|---|
| Build: signed `.ipa` with the VPN extension (development, 3 registered devices) | **Built**: `ProxyGridVPN-1.0.0-dev-ios.ipa`, bundle `net.proxygrid.vpn.ios`, app group `group.net.proxygrid.vpn` |
| On-device connect, background/foreground, network switching | Not yet tested |

## Android

| Area | Result |
|---|---|
| Build (CI) | Built: debug `.apk` (release `.aab` needs the keystore secrets) |
| Lifecycle fixes (teardown lock, stored status, callback re-registration, tile start) | Code review against the audit; no device test |
| On-device connect, Doze, reboot restore, Quick Settings tile | Not yet tested (no Android device or emulator run in this round) |

## Windows

| Area | Result |
|---|---|
| Build (CI, NSIS `.exe`, ia32 and arm64) | Built, unsigned (no Authenticode certificate configured) |
| Relay-exit restart, DNS via link-local resolver, connect/disconnect races, sleep/resume | Code review; `go vet` on the host; no Windows machine test |

## CI

"ProxyGridVPN / Builds" on thosuperman/outline-apps, run 43579536163 (commit ebf9c3b6, after the
rename to ProxyGridVPN and the identity change): all jobs pass. The Windows jobs also rebuild the
routing service and smartdnsblock from source with the new service, pipe and adapter names.

| Job | Result | Artifact |
|---|---|---|
| Web and Go tests (Ubuntu) | Pass: 109/109 web specs, all Go packages with `-race` | — |
| Windows ia32 | Built | `ProxyGridVPN-windows-ia32` → `ProxyGridVPN-0.0.0-debug-ia32.exe` (unsigned) |
| Windows helpers | Built from source | `ProxyGridVPN-windows-helpers` (OutlineService.exe, smartdnsblock.exe) |
| Windows arm64 | Built | `ProxyGridVPN-windows-arm64` → `ProxyGridVPN-0.0.0-debug-arm64.exe` (unsigned) |
| Android | Built | `ProxyGridVPN-android` → `ProxyGridVPN-app-debug.apk` (debug-signed) |

Earlier runs caught two real test problems (an unhandled rejection in a web spec and the e2e
cleanup check counting test-server goroutines) and one build bug (the Android label patch
targeted `strings.xml` instead of `cdv_strings.xml`); all three are fixed.

## Not yet tested (needs the owner)

- Any live tunnel on a real device or computer (routing changes; the owner enters keys).
- ProxyGrid sign-in against the real Cognito pool (identifiers not configured in builds yet; the
  backend is not deployed, so `/vpn/*` endpoints and dynamic keys cannot be exercised).
- Side-by-side install with the official Outline app (identifiers and Windows/Linux system names are
  now distinct; not yet tried on a machine).
