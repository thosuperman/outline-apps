# ProxyGridVPN test results (2026-11-14)

Branch `feat/gridvpn-client`. Status words follow the launch brief: **verified** (tested with a
recorded result), **partially verified** (automated or build-level evidence only), **untested**.
No live VPN tunnel has been brought up on a device yet: that changes routing, needs keys entered
by the owner, and the owner asked not to run network tests while they work.

## Summary per platform

| Platform | Build | Status | What is verified | What is not |
|---|---|---|---|---|
| macOS | Signed (development) universal `.dmg`, VPN extension, app group | **Partially verified** | Builds and signs with `net.proxygrid.vpn.macos`; signature verifies; `VpnExtensionTest` passes; shared Go/web tests | Live connect, DNS, sleep/wake, Wi-Fi switch; public Developer ID `.dmg` blocked (BLOCKERS.md) |
| iOS | Signed (development) `.ipa`, 3 devices | **Partially verified** | Builds, archives and exports with `net.proxygrid.vpn.ios`; shared tests | On-device connect, background/foreground, network switch; App Store build blocked |
| Android | Debug-signed `.apk` (CI run 43702091164) | **Partially verified** | Builds in CI; `apksigner` verifies (debug cert); package, label, minSdk 29 and sign-in config checked with `aapt2`; shared tests | Device or emulator run, sign-in, connect; release `.aab` blocked on keystore |
| Windows | Unsigned release NSIS `.exe` 1.0.0, ia32 + arm64 (CI run 43702091164); helpers rebuilt from source | **Partially verified** | Builds in CI; helpers compiled with the new names; shared tests | Install, service and TAP setup, sign-in and connect on Windows; signing; `.msi` blocked |
| Shared Go core | — | **Verified** (automated) | See below | — |
| Web UI | — | **Verified** (automated + visual check in browser) | See below | Real Cognito sign-in in the app (backend is live; needs the owner to sign in) |

## Automated tests (verified)

| Area | Test | Result |
|---|---|---|
| Outline key compatibility | SIP002 `ss://`, legacy base64 `ss://` (incl. padded and standard-alphabet), YAML dynamic-key config through a real Shadowsocks server (`TestE2E_OutlineKeyFormats`, `configregistry`) | Pass |
| Wrong credentials | A key with a wrong secret gets no data through | Pass |
| TCP | ~500 KB echo through Shadowsocks, AEAD chunking | Pass |
| UDP | Datagram echo through Shadowsocks | Pass |
| DNS | Query to `169.254.113.53` answered, ID kept, source preserved | Pass |
| DNS bursts (RFC 5452 intact) | 20 overlapping copies: resolver hit once (was 20 before the fix), all answered; distinct queries keep their own socket | Pass |
| Network change | Stale UDP check cannot override a newer one (fails before the fix) | Pass |
| Interruption / reconnection | Server down then back on the same address: client recovers without being recreated | Pass |
| Resource cleanup | 5 session cycles return to the goroutine baseline | Pass |
| Packet relay | Drop-and-continue, transient errors, close handling (3 cases fail before the fix) | Pass |
| All Go packages | `go test -race ./client/go/...` | Pass (macOS local, Linux CI) |
| Apple | `VpnExtensionTest` (routes incl. IPv6 and DNS /32, error persistence, address selection) | Pass |
| Web UI | 114 Jasmine specs: Cognito SRP proof checked against a server-side SRP implementation, Cognito flows, ProxyGrid API, session refresh, account provisioning, quota, keychain storage, `proxygridvpn://` links, Mode B keys never sent to ProxyGrid, key reuse (no new key per launch), quota-suspended key blocks connect | Pass (local and CI) |

## CI

Workflow "ProxyGridVPN / Builds" on thosuperman/outline-apps. The latest run is recorded in the
final report and the GitHub Actions page; artifacts: `ProxyGridVPN-windows-ia32`,
`ProxyGridVPN-windows-arm64`, `ProxyGridVPN-windows-helpers`, `ProxyGridVPN-android`.

## Device test checklist (untested; needs the owner)

Run on each platform with one ProxyGrid key (Mode A, once the backend is live) and one own
Outline key (Mode B). Record pass/fail and timings; compare with v1.17.0 on the same Mac/network.

1. Install beside the official Outline app; both launch; neither breaks the other.
2. Connect / disconnect 10 times; status always matches reality (`curl https://ifconfig.co` shows
   the server IP when Connected, the local IP when Disconnected).
3. DNS: `dig example.com` works; 100 parallel lookups succeed (burst).
4. TCP and UDP: browse; a UDP app or `dig @1.1.1.1` through the tunnel.
5. Switch Wi-Fi ↔ cellular/Ethernet while connected; time to recover.
6. macOS: sleep 5 minutes, wake; traffic resumes without user action.
7. Mobile: background the app 10 minutes, foreground; status and traffic correct.
8. Kill the server (or block it) for 30 s; client shows reconnecting, recovers when back.
9. Leave connected 1 hour; no memory growth in the extension/service (Activity Monitor / adb
   `dumpsys meminfo` / Task Manager).
10. Mode B with no ProxyGrid account and no network access to proxygrid.net: own keys still
    connect.
