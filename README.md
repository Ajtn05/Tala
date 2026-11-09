# Tala

Tala records and transcribes audio locally. The macOS dashboard supports microphone
and system audio, file and URL imports, editable sessions, and transcript exports.
The Linux CLI supports microphone and media transcription.

## Build

Install Go 1.25 or later, CMake, pkg-config, and the native compiler toolchain.
On Debian or Ubuntu, install `libasound2-dev` for microphone capture. The build
uses pinned versions of PortAudio and whisper.cpp. Media imports need FFmpeg and
ffprobe. URL imports also need yt-dlp.

```sh
make setup
make test
make build
```

On macOS 14.2 or later, use `make bundle` and open `dist/Tala.app`. This
development bundle has its own identity and requests microphone or system audio
access when recording.

## Model and storage

Download the verified Whisper Large v3 Turbo model from Settings or run
`./tala --ensure-model`. To use a copy of a verified model you already have,
select it in Settings or supply `--model /path/to/ggml-large-v3-turbo.bin` on the CLI.

On macOS, Tala stores settings and downloaded models under
`~/Library/Application Support/Tala/` and sessions under `~/Documents/Tala/`.
On Linux, it uses XDG data and config directories. `TALA_DATA_DIR` and
`TALA_OUTPUT_DIR` select separate directories for testing. This development
version starts with its own workspace. The previous checkout and its sessions
remain available separately.

```sh
./tala --record --duration 20s
./tala --transcribe recording.wav
./tala --list
./tala --export SESSION_ID --format srt
```

## Recording design

Capture adapters send timestamped PCM frames to the recorder. The recorder
groups and resamples frames, feeds a shared Whisper decoder, and saves finalized
segments with their source and timestamps. Stopping drains captured audio
before the session completes. Unit tests exercise draining, sample-rate
conversion, and cancellation on storage failure. To test real inference with
the upstream speech fixture, set `TALA_TEST_MODEL` before running `make test`.

Application code uses the MIT license. Third-party components retain their own
licenses. See the license files included with the macOS bundle.
