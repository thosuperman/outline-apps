#!/usr/bin/env bash
# Copyright 2026 ProxyGrid
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Applies the app's OS-level identity so it installs alongside the official Outline app.
# Run once, after the product name is confirmed (see docs/proxygridvpn/IDENTITY.md):
#
#   client/resources/proxygridvpn/apply_identity.sh <apple-prefix> <android-id> <desktop-app-id>
#   e.g. client/resources/proxygridvpn/apply_identity.sh net.proxygrid.vpn net.proxygrid.vpn net.proxygrid.vpn.desktop
#
# Apple:   <prefix>.ios / <prefix>.macos (+ .VpnExtension, .AppKitIntegration),
#          app group group.<prefix>
# Android: application ID <android-id>
# Desktop: electron-builder appId <desktop-app-id>
#
# Internal Java/Swift package and module names are intentionally left unchanged.
set -euo pipefail

if [ $# -ne 3 ]; then
  sed -n '17,27p' "$0"
  exit 2
fi
APPLE="$1"
ANDROID="$2"
DESKTOP="$3"
CLIENT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$CLIENT"

replace() { # file, from, to
  perl -0pi -e "s/\Q$2\E/$3/g" "$1"
}

# Apple bundle identifiers (order matters: longest first).
for f in src/cordova/apple/xcode/Outline.xcodeproj/project.pbxproj \
         src/cordova/apple/OutlineLib/OutlineLib.xcodeproj/project.pbxproj; do
  replace "$f" org.outline.macos.client "$APPLE.macos"
  replace "$f" org.outline.ios.client "$APPLE.ios"
  replace "$f" org.getoutline.client.VpnExtensionTest "$APPLE.VpnExtensionTest"
done

# Apple app group (entitlements and the two places that open the shared container).
for f in src/cordova/apple/OutlineLib/VpnExtension/VpnExtension.entitlements \
         src/cordova/apple/xcode/Outline/Outline.entitlements \
         src/cordova/plugin/apple/src/OutlinePlugin.swift \
         src/cordova/apple/OutlineLib/VpnExtension/Sources/PacketTunnelProvider.m; do
  replace "$f" group.org.getoutline.client "group.$APPLE"
done

# Android application ID.
replace config.xml 'id="org.outline.android.client"' "id=\"$ANDROID\""

# Desktop (Windows/Linux) app ID.
node -e '
  const fs = require("fs");
  const p = "electron/electron-builder.json";
  const c = JSON.parse(fs.readFileSync(p, "utf8"));
  c.appId = process.argv[1];
  fs.writeFileSync(p, JSON.stringify(c, null, 2) + "\n");
' "$DESKTOP"

echo "Identity applied. Review 'git diff', then register the new IDs and app group with the Apple"
echo "team before building. Windows service/TAP names are a separate change (see IDENTITY.md)."
