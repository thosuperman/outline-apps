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

# Regenerates every ProxyGrid VPN app icon, tray icon and splash image from the SVG sources in this
# directory (mark.svg, foreground.svg, glyph.svg). Run from anywhere:
#   client/resources/gridvpn/generate.sh
# Requires node (puppeteer, already a dev dependency), python3, sips and iconutil (macOS).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
CLIENT="$(cd "$HERE/../.." && pwd)"
RENDER="node $HERE/render.mjs"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/gridvpn-icons.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

MARK="$HERE/mark.svg"
FG="$HERE/foreground.svg"
GLYPH="$HERE/glyph.svg"

# --- Android launcher icons (legacy square icons used by Cordova) ---
A="$CLIENT/resources/icons/android"
$RENDER "$MARK" 48 "$A/icon_mdpi.png" 72 "$A/icon_hdpi.png" 96 "$A/icon_xhdpi.png" \
  144 "$A/icon_xxhdpi.png" 192 "$A/icon_xxxhdpi.png"

# Android notification icon: white glyph on transparent (Android tints it).
sed 's/#000/#fff/g' "$GLYPH" > "$TMP/glyph-white.svg"
$RENDER "$TMP/glyph-white.svg" 49 "$CLIENT/src/cordova/plugin/android/res/drawable/small_icon.png"

# --- iOS icons ---
I="$CLIENT/resources/icons/ios"
args=()
for f in "$I"/*.png; do
  size=$(sips -g pixelWidth "$f" | awk '/pixelWidth/{print $2}')
  args+=("$size" "$f")
done
$RENDER "$MARK" "${args[@]}"

# --- macOS (Cordova xcassets and Icon Composer layer) ---
O="$CLIENT/resources/icons/osx"
$RENDER "$MARK" 16 "$O/icon-16.png" 32 "$O/icon-32.png" 64 "$O/icon-64.png" \
  128 "$O/icon-128.png" 256 "$O/icon-256.png" 512 "$O/icon-512.png" 1024 "$O/icon-1024.png"
$RENDER "$FG" 1024 "$CLIENT/src/cordova/apple/xcode/AppIcon.icon/Assets/icon-1024.png"

# macOS menu-bar (status item) template images: black glyph, the system tints it.
SB="$CLIENT/src/cordova/apple/xcode/Outline/Classes/AppKitBridge/Resources/Assets.xcassets"
$RENDER "$GLYPH" 33 "$SB/status_bar_button_image.imageset/outline-black-off-2x.png"
cat > "$TMP/glyph-on.svg" <<'SVG'
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024">
  <path d="M512 150 L800 256 V484 C800 662 680 788 512 854 C344 788 224 662 224 484 V256 Z" fill="#000"/>
</svg>
SVG
$RENDER "$TMP/glyph-on.svg" 33 "$SB/status_bar_button_image_connected.imageset/outline-black-on-2x.png"

# --- Electron (Windows/Linux) ---
E="$CLIENT/electron/icons"
$RENDER "$MARK" 16 "$E/png/16x16.png" 24 "$E/png/24x24.png" 32 "$E/png/32x32.png" \
  48 "$E/png/48x48.png" 64 "$E/png/64x64.png" 128 "$E/png/128x128.png" \
  256 "$E/png/256x256.png" 512 "$E/png/512x512.png" 1024 "$E/png/1024x1024.png"
cp "$E/png/1024x1024.png" "$CLIENT/resources/electron/icon.png"
python3 "$HERE/make_ico.py" "$E/win/icon.ico" "$E/png/16x16.png" "$E/png/24x24.png" \
  "$E/png/32x32.png" "$E/png/48x48.png" "$E/png/64x64.png" "$E/png/128x128.png" "$E/png/256x256.png"

ICONSET="$TMP/icon.iconset"
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
  $RENDER "$MARK" "$s" "$ICONSET/icon_${s}x${s}.png" "$((s * 2))" "$ICONSET/icon_${s}x${s}@2x.png"
done
iconutil -c icns -o "$E/mac/icon.icns" "$ICONSET"

# Tray icons (Electron): colored mark when connected, gray when disconnected.
sed -e 's/#6E6EE6/#9AA3AF/' -e 's/#3A3AB0/#6B7482/' -e 's/#2DD4A0/#CBD0D8/g' -e 's/#0E9F6E/#CBD0D8/' \
  "$MARK" > "$TMP/mark-off.svg"
$RENDER "$MARK" 1024 "$CLIENT/resources/tray/connected.png"
$RENDER "$TMP/mark-off.svg" 1024 "$CLIENT/resources/tray/disconnected.png"

# --- Splash screens: centered mark on the brand background ---
for spec in "1728 $CLIENT/resources/splashscreen/android/splash_screen_icon_xxxhdpi.png" \
            "2733 $CLIENT/resources/splashscreen/ios/splashscreen@2x~universal~anyany.png"; do
  size=${spec%% *}
  out=${spec#* }
  cat > "$TMP/splash.svg" <<SVG
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 $size $size">
  <rect width="$size" height="$size" fill="#0B0D10"/>
  <svg x="$((size * 3 / 8))" y="$((size * 3 / 8))" width="$((size / 4))" height="$((size / 4))" viewBox="0 0 1024 1024">
    $(sed -e '1d' -e '$d' "$MARK")
  </svg>
</svg>
SVG
  $RENDER "$TMP/splash.svg" "$size" "$out"
done

# --- Web UI assets ---
W="$CLIENT/web/assets"
$RENDER "$MARK" 137 "$W/logo-nav.png" 204 "$W/brand-logo-dark.png" 193 "$W/brand-logo.png"
$RENDER "$MARK" 64 "$TMP/fav64.png" 32 "$TMP/fav32.png" 16 "$TMP/fav16.png"
python3 "$HERE/make_ico.py" "$CLIENT/web/favicon.ico" "$TMP/fav16.png" "$TMP/fav32.png" "$TMP/fav64.png"

echo "ProxyGrid VPN icons regenerated."
