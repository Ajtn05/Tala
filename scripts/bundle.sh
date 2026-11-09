#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ "$(uname -s)" != Darwin ]]; then
    echo "The desktop bundle requires macOS." >&2
    exit 1
fi

version="$(cat VERSION)"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "VERSION must contain a numeric major.minor.patch version." >&2
    exit 1
fi
mkdir -p dist
stage="$(mktemp -d dist/.tala-build.XXXXXX)"
trap 'rm -r -- "$stage"' EXIT
bundle="$stage/Tala.app"
resources="$bundle/Contents/Resources"
mkdir -p "$bundle/Contents/MacOS" "$resources"
cp tala "$bundle/Contents/MacOS/tala"
bash scripts/copy-notices.sh "$resources"
cp internal/dashboard/assets/Manrope-OFL.txt "$resources/Manrope-OFL.txt"

# Build the Finder and Dock icon from the same artwork used on the product page.
iconset="$stage/Tala.iconset"
mkdir "$iconset"
for size in 16 32 128 256 512; do
    sips -z "$size" "$size" docs/assets/tala-icon.png --out "$iconset/icon_${size}x${size}.png" >/dev/null
    retina="$((size * 2))"
    sips -z "$retina" "$retina" docs/assets/tala-icon.png --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns -o "$resources/Tala.icns" "$iconset"

cat > "$bundle/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key>
  <string>io.github.ajtn05.tala.next</string>
  <key>CFBundleName</key>
  <string>Tala</string>
  <key>CFBundleIconFile</key>
  <string>Tala.icns</string>
  <key>CFBundleExecutable</key>
  <string>tala</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleVersion</key>
  <string>$version</string>
  <key>CFBundleShortVersionString</key>
  <string>$version</string>
  <key>LSMinimumSystemVersion</key>
  <string>14.2</string>
  <key>NSMicrophoneUsageDescription</key>
  <string>Tala uses the microphone to transcribe your recording on this Mac.</string>
  <key>NSAudioCaptureUsageDescription</key>
  <string>Tala uses system audio to transcribe your recording on this Mac.</string>
</dict>
</plist>
PLIST
codesign --force --sign - "$bundle"
codesign --verify --strict "$bundle"
if [[ -e dist/Tala.app ]]; then
    backup="$(mktemp -d dist/.previous.XXXXXX)"
    mv dist/Tala.app "$backup/Tala.app"
fi
mv "$bundle" dist/Tala.app
echo 'Built dist/Tala.app'
