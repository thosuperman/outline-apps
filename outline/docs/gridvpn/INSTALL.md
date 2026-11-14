# Installing ProxyGrid VPN test builds

ProxyGrid VPN uses its own app identities (`net.proxygrid.vpn.*`, see IDENTITY.md), separate from
the official Outline app, so both can be installed side by side.

## macOS (development-signed .dmg)

File: `ProxyGrid-VPN-1.0.0-dev-macos-universal.dmg` (Apple silicon and Intel).

1. Open the DMG and drag **ProxyGrid VPN** to Applications.
2. Open ProxyGrid VPN. On first connect, macOS asks to add a VPN configuration: allow it.
3. This build is signed with an Apple Development certificate of team 797NU6L392 and runs only on
   Macs registered to that team (Mac A and Mac B). Other Macs need a Developer ID build.

To remove: quit ProxyGrid VPN, delete it from Applications, and remove the "ProxyGrid VPN" VPN entry in
System Settings > VPN.

## iOS (development-signed .ipa)

File: `ProxyGrid-VPN-1.0.0-dev-ios.ipa`. The embedded profile covers the three devices registered to team
797NU6L392.

1. Connect the iPhone to the Mac, open Xcode > Window > Devices and Simulators, select the phone
   and drag the `.ipa` onto "Installed Apps" (or use Apple Configurator).
2. On the phone, enable Developer Mode if asked (Settings > Privacy & Security).
3. Open ProxyGrid VPN and allow the VPN configuration on first connect.

## Android (.apk)

File: `ProxyGrid-VPN-app-debug.apk` (CI artifact `ProxyGrid-VPN-android`).

1. Copy the APK to the phone and open it; allow installing from that source when asked.
2. Open ProxyGrid VPN and accept the VPN connection request on first connect.

Release builds are signed `.aab` (Play Store) and a universal `.apk`, produced by CI when the
keystore secrets are set.

## Windows (.exe)

File: `ProxyGrid-VPN-<version>-ia32.exe` or `-arm64.exe` (CI artifact `ProxyGrid-VPN-windows-<arch>`).

1. Run the installer. It needs administrator rights to install the ProxyGrid VPN network adapter (TAP
   driver) and routing service. Unsigned test builds show a SmartScreen warning: choose
   "More info" > "Run anyway".
2. Open ProxyGrid VPN from the Start menu.

The installer adds its own routing service (`ProxyGridVPNService`) and network adapter
(`proxygrid-tap0`, 10.0.86.x), so it can sit next to the official Outline app. Connect only one VPN
at a time.

## Opening keys from links

`proxygridvpn://add?key=<url-encoded key>` opens the app and offers to add the key.

## Using the app

- **ProxyGrid tab:** sign in with your ProxyGrid account (create one at proxygrid.net), pick a
  location, tap the power button. The plan and data used this period are shown below.
- **My Own Server tab:** tap **+** and paste an `ss://` or `ssconf://` key (any Outline key or
  self-hosted Outline server). Keys are stored in the system keychain/keystore.
