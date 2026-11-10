#!/usr/bin/env bash
set -euo pipefail
destination="$1"
go_tool="${TALA_GO:-go}"
mkdir -p "$destination"
cp LICENSE "$destination/Tala-LICENSE.txt"
cp third_party/whisper.cpp/LICENSE "$destination/whisper.cpp-LICENSE.txt"
cp internal/transcriber/assets/Silero-VAD-LICENSE.txt "$destination/Silero-VAD-LICENSE.txt"
cp third_party/portaudio/LICENSE.txt "$destination/PortAudio-LICENSE.txt"
cp "$("$go_tool" list -m -f '{{.Dir}}' github.com/gordonklaus/portaudio)/LICENSE" "$destination/Go-PortAudio-LICENSE.txt"
cp "$("$go_tool" list -m -f '{{.Dir}}' golang.org/x/sync)/LICENSE" "$destination/Go-x-sync-LICENSE.txt"
cp "$("$go_tool" env GOROOT)/LICENSE" "$destination/Go-LICENSE.txt"
