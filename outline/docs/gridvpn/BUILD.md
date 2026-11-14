# Building ProxyGrid VPN

All commands run from the `outline/` directory of the repository. Requirements are the same as
upstream (Node 22, Go 1.25, Xcode for Apple, JDK 17 + Android SDK/NDK for Android, zig for the
Windows/Linux Go cross-builds); see `client/AGENTS.md`.

## Build-time configuration

| Variable | Purpose | Default |
|---|---|---|
| `PROXYGRID_API_URL` | ProxyGrid API base | `https://api.proxygrid.net` |
| `PROXYGRID_COGNITO_REGION` | Cognito region of the ProxyGrid user pool | empty: ProxyGrid sign-in disabled, My Own Server still works |
| `PROXYGRID_USER_POOL_ID` | Cognito user pool ID | empty |
| `PROXYGRID_USER_POOL_CLIENT_ID` | Cognito app client ID (public, no secret) | empty |
| `SENTRY_DSN` | Crash reporting | empty: nothing is reported |

These values are public identifiers (the ProxyGrid website ships them too). They are passed at
build time so no deployment-specific value is committed. In CI they come from repository
variables.

## Reproducibility

- `package-lock.json` and `go.sum` pin every dependency; `npm ci` installs exactly the lock file.
- The Outline SDK is pinned by pseudo-version to the DNS Exchanger commit of outline-sdk#637
  (`v0.1.0-rc1.0.20260915234250-3f66d9c28049`), fetched through the Go module proxy; no local
  checkout or `replace` directive is needed.
- Icons and splash screens are generated from SVG sources by
  `client/resources/gridvpn/generate.sh` with the pinned puppeteer Chromium.
- Versions come from `--versionName` / `--buildNumber`, not from the build machine.

## Web UI only (browser, fake VPN)

```bash
npm run action client/web/start
```

## macOS (signed .app / .dmg)

```bash
npm run action client/go/build macos
npm run action client/src/cordova/setup macos -- --buildMode=release --versionName=1.0.0 --buildNumber=1
```

Then build the `Outline` scheme of `client/src/cordova/apple/client.xcworkspace` for
"My Mac (Mac Catalyst)" with team `797NU6L392`. `C4883CK9TX` is a free team and cannot sign the
Network Extension. A distributable, notarized `.dmg` needs a **Developer ID Application**
certificate for that team (not present on the build Mac on 2026-11-14) and notarization
credentials. Development-signed builds run only on Macs registered to the team.

For a local test build with a separate bundle prefix (does not modify the repository):
`/Volumes/DATA/Working/outline/gridvpn-build-mac.sh` (outside the repo).

## iOS (signed .ipa)

```bash
npm run action client/go/build ios
npm run action client/src/cordova/setup ios -- --buildMode=release --versionName=1.0.0 --buildNumber=1
```

Archive the `Outline` scheme for "Any iOS Device" and export. Development/ad-hoc exports need the
target iPhones registered to the team; App Store/TestFlight needs an App Store Connect record,
which requires the owner's approval.

## Android (.apk / .aab)

```bash
npm run action client/src/cordova/build android                     # debug .apk
ANDROID_KEY_STORE_CONTENTS=<base64 PKCS#12> ANDROID_KEY_STORE_PASSWORD=<password> \
  npm run action client/src/cordova/build android -- --buildMode=release --versionName=1.0.0 --buildNumber=1
```

The keystore is read from the environment and never stored in the repository. The app label is
set to ProxyGrid VPN by the setup step; the internal Cordova project name stays `Outline` because the
Apple Xcode project depends on it.

## Windows (.exe) and Linux (.deb)

```bash
npm run action client/electron/build windows -- --arch=ia32
npm run action client/electron/build linux -- --arch=x64
```

Release builds also need `--buildMode=release --versionName=… --autoUpdateUrl=…`. Outputs are
named `ProxyGrid-VPN-<version>-<arch>.<ext>`. Windows code signing uses
`client/electron/windows/electron_builder_signing_plugin.cjs` with credentials from the CI
environment. An MSI can be produced with `GRIDVPN_WINDOWS_MSI=1`, but it does not install the TAP
driver yet, so it cannot connect; ship the NSIS `.exe`.

## CI

`.github/workflows/gridvpn_builds.yml` runs the web and Go tests and builds the Windows and
Android packages on every push to `feat/gridvpn-client`, and release builds for `gridvpn-v*` tags.
Artifacts are uploaded as `ProxyGrid-VPN-windows-<arch>` and `ProxyGrid-VPN-android`.

## Identity

See [IDENTITY.md](IDENTITY.md). Bundle IDs, package names, the Apple app group and Windows service
names are applied after the product name is confirmed.
