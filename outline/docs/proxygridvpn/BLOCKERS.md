# ProxyGridVPN release blockers (2026-11-14)

What is missing for each store-ready deliverable, and the exact steps to finish it. Everything
that could be done without these items is done: development-signed Apple builds, Windows and
Android CI builds, tests.

## macOS: public, notarized .dmg

**Have:** `ProxyGridVPN-1.0.0-dev-macos-universal.dmg`, signed with an *Apple Development*
certificate of team 797NU6L392, with the VPN extension and app group. It runs only on Macs
registered to the team.

**Missing (two items):**

1. **A "Developer ID Application" certificate** for team 797NU6L392. The build Mac has only
   Apple Development identities (`security find-identity -v -p codesigning`).
2. **System-extension packaging of the VPN provider.** For Developer ID distribution Apple
   supports Network Extension providers only as *system extensions*
   (TN3134; entitlement `packet-tunnel-provider-systemextension`). The app ships the provider as an
   *app extension* (`Contents/PlugIns/VpnExtension.appex`), which is supported only through the
   Mac App Store. This is a code change, not a signing setting.

**Option A: Mac App Store (no code change).**
1. App Store Connect > Apps > + > New App: platform macOS, bundle ID `net.proxygrid.vpn.macos`
   (already registered by the development build), name "ProxyGridVPN". Needs the owner's approval
   (creates a listing).
2. Certificates: create "Apple Distribution" and "Mac Installer Distribution" certificates.
3. `npm run action client/src/cordova/setup macos -- --buildMode=release --versionName=<v> --buildNumber=<n>`
4. Xcode: archive the `Outline` scheme for "My Mac (Mac Catalyst)" with team 797NU6L392, then
   Distribute App > App Store Connect.

**Option B: direct download (Developer ID).**
1. developer.apple.com > Certificates > + > "Developer ID Application" (account holder only);
   install it in the build Mac's login keychain.
2. Engineering: move the provider to a system extension. The extension goes in
   `Contents/Library/SystemExtensions`, the host app activates it with
   `OSSystemExtensionRequest.activationRequest`, the entitlement becomes
   `packet-tunnel-provider-systemextension`, and the app gets
   `com.apple.developer.system-extension.install`.
3. Export following Apple's "Exporting a Developer ID Network Extension" guidance, then
   `xcrun notarytool submit <dmg> --keychain-profile <profile> --wait` and
   `xcrun stapler staple <dmg>`. Notarization needs an app-specific password or API key stored
   with `notarytool store-credentials`, entered by the owner.

## iOS: App Store / TestFlight .ipa

**Have:** `ProxyGridVPN-1.0.0-dev-ios.ipa`, development-signed, installable on the 3 devices
registered to team 797NU6L392.

**Missing:** an App Store Connect app record and an Apple Distribution certificate.

**Steps:**
1. App Store Connect > Apps > + > New App: iOS, bundle ID `net.proxygrid.vpn.ios`, name
   "ProxyGridVPN" (owner's approval: creates a listing).
2. `npm run action client/src/cordova/setup ios -- --buildMode=release --versionName=<v> --buildNumber=<n>`
3. Archive the `Outline` scheme for "Any iOS Device" and Distribute App > App Store Connect
   (automatic signing creates the distribution profiles for the app and
   `net.proxygrid.vpn.ios.VpnExtension`).
4. App Review needs a VPN privacy description and an account for review (Mode A) or a test key
   (Mode B).

## Android: signed release .aab (and release .apk)

**Have:** `ProxyGridVPN-app-debug.apk` from CI (debug-signed).

**Missing:** an upload keystore, which the release build reads from CI secrets
(`client/src/cordova/build.action.mjs`: PKCS#12, alias `privatekey`).

**Steps (owner runs them; the keystore and password must stay private):**

```bash
keytool -genkeypair -v -storetype PKCS12 -keystore proxygridvpn-upload.p12 -alias privatekey -keyalg RSA -keysize 4096 -validity 10000 -dname "CN=ProxyGrid"
```

```bash
base64 -i proxygridvpn-upload.p12 | gh secret set ANDROID_KEY_STORE_CONTENTS --repo thosuperman/outline-apps
```

```bash
gh secret set ANDROID_KEY_STORE_PASSWORD --repo thosuperman/outline-apps
```

```bash
gh workflow run proxygridvpn_builds.yml --repo thosuperman/outline-apps --ref feat/gridvpn-client -f version=1.0.0
```

The Android job then produces `app-release.aab` and a universal `.apk` (artifact
`ProxyGridVPN-android`). Keep a backup of the `.p12`: Play App Signing needs the same upload key for
every update. Publishing to Play needs a Play Console listing (owner's approval).

## Windows: signed .exe, and .msi

**Have:** `ProxyGridVPN-<version>-ia32.exe` and `-arm64.exe` NSIS installers from CI, unsigned, and
the routing service and DNS-leak helper rebuilt from source.

**Missing (signing):** an OV or EV Authenticode code-signing certificate. CAs now require the key
in hardware; the existing pipeline signs with a key in Google Cloud KMS (HSM)
(`client/electron/windows/sign_windows_executable.action.mjs`).

**Steps:**
1. Buy an OV/EV code-signing certificate issued to ProxyGrid, with the key generated in a
   Google Cloud KMS HSM key ring (or adapt the signing action to the CA's cloud HSM).
2. Set CI secrets `WINDOWS_SIGNING_CERT_TYPE=gcp-hsm`, `WINDOWS_SIGNING_CERT_PASSWORD` (GCP access
   token), `WINDOWS_SIGNING_GCP_KEYRING`, `WINDOWS_SIGNING_GCP_PRIVATE_KEY`,
   `WINDOWS_SIGNING_GCP_PUBLIC_CERT`, and pass them as env to the Windows build step.

**Missing (.msi):** the MSI produced with `PROXYGRIDVPN_WINDOWS_MSI=1` does not install the TAP
driver (that is done by `custom_install_steps.nsh`, which only the NSIS installer runs), so it
cannot connect. Shipping it needs a WiX custom action that runs `add_tap_device.bat` and
`install_windows_service.bat` elevated at install time and removes them at uninstall. Until then,
ship the `.exe` only.

## Live VPN tests

Not run on any device: they change the machine's routing and need keys entered by the owner, and
the owner asked not to run network tests while they work. Steps are in TESTING.md ("Device test
checklist").

## ProxyGrid account mode (Mode A) end to end

The ProxyGrid backend is not deployed, so `/vpn/*`, dynamic keys and Cognito sign-in cannot be
exercised. When it is deployed, set repository variables `PROXYGRID_COGNITO_REGION`,
`PROXYGRID_USER_POOL_ID`, `PROXYGRID_USER_POOL_CLIENT_ID` (public values) and rebuild. Mode B
(own servers) works without any of this.
