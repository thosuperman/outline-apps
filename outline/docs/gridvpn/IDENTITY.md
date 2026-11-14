# GridVPN app identity

GridVPN must install alongside the official Outline app, so every OS-level identifier differs
from Outline's. Internal Java/Swift package and module names (`org.outline.vpn`,
`OutlineAppleLib`, `Tun2socks`, ...) are **not** renamed: they are not user-visible, and keeping
them makes upstream fixes easy to merge.

> Status: the identifiers below are the proposal. They are applied only after the product name
> is confirmed (see "Naming check" below), because bundle IDs and package names cannot be
> changed after a store listing exists.

| Platform | Outline (upstream) | GridVPN (proposed) | Where it is set |
|---|---|---|---|
| Android application ID | `org.outline.android.client` | `net.proxygrid.vpn` | `client/config.xml` (`widget@id`) |
| iOS bundle ID | `org.outline.ios.client` | `net.proxygrid.vpn.ios` | `src/cordova/apple/xcode/Outline.xcodeproj` |
| iOS VPN extension | `org.outline.ios.client.VpnExtension` | `net.proxygrid.vpn.ios.VpnExtension` | `OutlineLib.xcodeproj` |
| macOS bundle ID | `org.outline.macos.client` | `net.proxygrid.vpn.macos` | `Outline.xcodeproj` |
| macOS VPN extension | `org.outline.macos.client.VpnExtension` | `net.proxygrid.vpn.macos.VpnExtension` | `OutlineLib.xcodeproj` |
| Apple app group | `group.org.getoutline.client` | `group.net.proxygrid.vpn` | entitlements, `OutlinePlugin.swift`, `PacketTunnelProvider.m` |
| Windows/Linux appId | `org.outline.client`-style electron-builder appId | `net.proxygrid.vpn.desktop` | `client/electron/electron-builder.json` |
| Windows TAP device / service | `outline-tap0`, `OutlineService` | `gridvpn-tap0`, `GridVPNService` | `electron/go_vpn_tunnel.ts`, `electron/windows/*` |
| App URL scheme | — (Outline registers `ss`, `ssconf`) | `gridvpn://` for account deep links | `config.xml`, Info.plist, electron-builder |
| Access-key schemes | `ss://`, `ssconf://` | unchanged (compatibility) | — |

Both apps may register `ss://`/`ssconf://`; the OS asks the user which app opens a key. That is
intentional: GridVPN's "My Own Server" mode accepts standard Outline keys.

## Signing

- Apple: team `797NU6L392` ("Phu Duc", paid Individual) signs the apps and Network Extensions.
  `C4883CK9TX` is a free Personal Team and cannot sign Network Extensions.
- Android: an upload keystore kept outside Git (path and passwords via environment variables
  in CI secrets).
- Windows: Authenticode certificate, provided to CI as a secret. Unsigned builds work but show
  SmartScreen warnings.

No certificate, private key, keystore or provisioning profile is committed.

## Naming check (2026-11-14)

Before applying the identifiers, "GridVPN" was checked for conflicts:

- An existing VPN app called "Grid VPN" has a live privacy-policy page
  (sites.google.com/view/gridvpn), which is typical of a Google Play listing.
- "SavGridVPN" is published on Google Play.
- The USPTO mark GRIDLOCK covers VPN services; "Off The Grid VPN" exists.
- No registered GRIDVPN trademark was found. This was not a formal legal search.

Because of the store-name clash risk, alternatives were proposed (recommended: "ProxyGrid
VPN", matching the owned domain proxygrid.net). Identifiers are applied after the owner picks.
