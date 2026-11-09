#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ "$(uname -s)" != Darwin ]]; then
    echo "The desktop bundle requires macOS." >&2
    exit 1
fi

go_tool="${TALA_GO:-go}"
mkdir -p dist
stage="$(mktemp -d dist/.tala-build.XXXXXX)"
bundle="$stage/Tala.app"
resources="$bundle/Contents/Resources"
mkdir -p "$bundle/Contents/MacOS" "$resources"
cp tala "$bundle/Contents/MacOS/tala"
cp LICENSE "$resources/Tala-LICENSE.txt"
cp third_party/whisper.cpp/LICENSE "$resources/whisper.cpp-LICENSE.txt"
cp third_party/portaudio/LICENSE.txt "$resources/PortAudio-LICENSE.txt"
cp internal/dashboard/assets/Manrope-OFL.txt "$resources/Manrope-OFL.txt"
cp "$("$go_tool" list -m -f '{{.Dir}}' github.com/gordonklaus/portaudio)/LICENSE" "$resources/Go-PortAudio-LICENSE.txt"
cp "$("$go_tool" list -m -f '{{.Dir}}' golang.org/x/sync)/LICENSE" "$resources/Go-x-sync-LICENSE.txt"

cat > "$bundle/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key>
  <string>io.github.ajtn05.tala.next</string>
  <key>CFBundleName</key>
  <string>Tala</string>
  <key>CFBundleExecutable</key>
  <string>tala</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleVersion</key>
  <string>1</string>
  <key>CFBundleShortVersionString</key>
  <string>0.1.0</string>
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
rmdir "$stage"
echo 'Built dist/Tala.app'
