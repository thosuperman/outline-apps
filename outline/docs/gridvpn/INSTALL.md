# Installing ProxyGrid VPN test builds

ProxyGrid VPN uses separate app identities from the official Outline app, so both can be installed
side by side. (The test builds below use a local `com.phuduc.gridvpn.dev.*` bundle prefix until
the final identifiers are confirmed.)

## macOS (development-signed .dmg)

File: `ProxyGrid-VPN-dev-macos-universal.dmg` (Apple silicon and Intel).

1. Open the DMG and drag **ProxyGrid VPN** to Applications.
2. Open ProxyGrid VPN. On first connect, macOS asks to add a VPN configuration: allow it.
3. This build is signed with an Apple Development certificate of team 797NU6L392 and runs only on
   Macs registered to that team (Mac A and Mac B). Other Macs need a Developer ID build.

To remove: quit ProxyGrid VPN, delete it from Applications, and remove the "ProxyGrid VPN" VPN entry in
System Settings > VPN.

## iOS (development-signed .ipa)

File: `ProxyGrid-VPN-dev-ios.ipa`. The embedded profile covers the three devices registered to team
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

Note: on Windows the routing service and network adapter still use Outline's internal names
(`OutlineService`, `outline-tap0`) until the identity step, so do not install the ProxyGrid VPN test
build on a PC that also runs the official Outline app.

## Using the app

- **ProxyGrid tab:** sign in with your ProxyGrid account (create one at proxygrid.net), pick a
  location, tap the power button. The plan and data used this period are shown below.
- **My Own Server tab:** tap **+** and paste an `ss://` or `ssconf://` key (any Outline key or
  self-hosted Outline server). Keys are stored in the system keychain/keystore.
