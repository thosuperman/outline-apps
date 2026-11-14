# ProxyGrid VPN app identity

Product name: **ProxyGrid VPN** (chosen 2026-11-14 after the naming check below). Tagline
"Your Network. Your Freedom.", website https://proxygrid.net/vpn.

ProxyGrid VPN installs alongside the official Outline app, so every OS-level identifier differs
from Outline's. Internal Java/Swift package and module names (`org.outline.vpn`,
`OutlineAppleLib`, `Tun2socks`, the `OutlineService.exe` file name, ...) are **not** renamed:
users don't see them, and keeping them makes upstream fixes easy to merge.

| What | Outline (upstream) | ProxyGrid VPN | Where it is set |
|---|---|---|---|
| Android application ID | `org.outline.android.client` | `net.proxygrid.vpn` | `client/config.xml` (`widget@id`) |
| iOS bundle ID | `org.outline.ios.client` | `net.proxygrid.vpn.ios` | `src/cordova/apple/xcode/Outline.xcodeproj` |
| iOS VPN extension | `org.outline.ios.client.VpnExtension` | `net.proxygrid.vpn.ios.VpnExtension` | `OutlineLib.xcodeproj` |
| macOS bundle ID | `org.outline.macos.client` | `net.proxygrid.vpn.macos` | `Outline.xcodeproj` |
| macOS VPN extension | `org.outline.macos.client.VpnExtension` | `net.proxygrid.vpn.macos.VpnExtension` | `OutlineLib.xcodeproj` |
| Apple app group | `group.org.getoutline.client` | `group.net.proxygrid.vpn` | entitlements, `OutlinePlugin.swift`, `PacketTunnelProvider.m` |
| Desktop appId / package | electron default / `outline-client` | `net.proxygrid.vpn.desktop` / `proxygrid-vpn` | `client/electron/electron-builder.json` |
| Desktop data directory | `<config>/org.getoutline.client` | `<config>/net.proxygrid.vpn` | `client/go/outline/client.go` |
| Windows service / pipe | `OutlineService` / `OutlineServicePipe` | `ProxyGridVPNService` / `ProxyGridVPNServicePipe` | `electron/windows/OutlineService`, `install_windows_service.bat`, `custom_install_steps.nsh`, `routing_service.ts` |
| Windows TAP adapter / subnet | `outline-tap0` / `10.0.85.0/24` | `proxygrid-tap0` / `10.0.86.0/24` | `add_tap_device.bat`, `go_vpn_tunnel.ts`, `OutlineService.cs`, `smartdnsblock.cpp` |
| Windows firewall (WFP) provider | `Outline` | `ProxyGrid VPN` | `smartdnsblock.cpp` |
| Linux TUN / address / routing | `outline-tun1`, `10.0.85.5`, table 7113, mark 0x711E | `proxygrid-tun1`, `10.0.86.5`, table 7124, mark 0x7C6D | `electron/vpn_service.ts` |
| Linux install path / executable | `/opt/Outline/Outline` | `/opt/ProxyGrid VPN/proxygrid-vpn` | electron-builder, `debian/after_install.sh` |
| App URL scheme | — | `proxygridvpn://` | Info.plist, `config.xml`, Electron |
| Access-key schemes | `ss://`, `ssconf://` | unchanged (compatibility) | — |

The Windows helpers (`OutlineService.exe`, `smartdnsblock.exe`) have these names compiled in.
CI rebuilds them from source on every Windows build; the committed binaries are refreshed from CI
output so local builds match.

## App links

- `proxygridvpn://add?key=<url-encoded ss:// or ssconf:// key>` opens the app and offers to add
  the key (the usual confirmation dialog), for example from an "Open in app" button on
  proxygrid.net.
- `proxygridvpn://open` brings the app to the front.

Both apps may register `ss://`/`ssconf://`; the OS asks the user which app opens a key. That is
intentional: My Own Server mode accepts standard Outline keys.

## Signing

- Apple: team `797NU6L392` ("Phu Duc", paid Individual) signs the apps and Network Extensions.
  `C4883CK9TX` is a free Personal Team and cannot sign Network Extensions.
- Android: an upload keystore kept outside Git (CI secrets `ANDROID_KEY_STORE_CONTENTS`,
  `ANDROID_KEY_STORE_PASSWORD`).
- Windows: Authenticode certificate provided to CI as a secret. Unsigned builds work but show
  SmartScreen warnings.

No certificate, private key, keystore or provisioning profile is committed.

## Naming check (2026-11-14)

"GridVPN" was checked first and found risky: an existing VPN app called "Grid VPN" has a live
privacy-policy page (sites.google.com/view/gridvpn, typical of a Google Play listing), "SavGridVPN"
is on Google Play, and the USPTO mark GRIDLOCK covers VPN services. No registered GRIDVPN mark was
found (not a formal legal search). The owner chose "ProxyGrid VPN", which matches the owned domain
proxygrid.net. A formal trademark search is still recommended before store submission.
