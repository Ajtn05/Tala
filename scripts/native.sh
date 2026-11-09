#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

source_tree() {
    local path="$1" url="$2" tag="$3"
    if [[ ! -f "$path/CMakeLists.txt" ]]; then
        git clone --depth 1 --branch "$tag" "$url" "$path"
    fi
}

mkdir -p third_party
source_tree third_party/whisper.cpp https://github.com/ggml-org/whisper.cpp.git "$(cat WHISPER_VERSION)"
source_tree third_party/portaudio https://github.com/PortAudio/portaudio.git v19.7.0

whisper_flags=(-DGGML_NATIVE=OFF -DWHISPER_BUILD_EXAMPLES=OFF -DWHISPER_BUILD_TESTS=OFF)
audio_flags=(-DPA_BUILD_STATIC=ON -DPA_BUILD_SHARED=OFF -DPA_BUILD_EXAMPLES=OFF)
audio_libs=""
case "$(uname -s)" in
    Darwin)
        whisper_flags+=(-DGGML_METAL=ON -DCMAKE_OSX_DEPLOYMENT_TARGET=14.2)
        if [[ "$(uname -m)" == arm64 ]]; then
            whisper_flags+=(-DGGML_CPU_ARM_ARCH=armv8.2-a+dotprod+fp16)
        fi
        audio_flags+=(-DCMAKE_OSX_DEPLOYMENT_TARGET=14.2)
        ;;
    Linux)
        audio_libs="$(pkg-config --libs alsa) -lpthread -ldl -lm"
        whisper_flags+=(-DGGML_METAL=OFF -DGGML_BLAS=OFF)
        audio_flags+=(-DPA_USE_ALSA=ON -DPA_USE_JACK=OFF -DPA_USE_OSS=OFF)
        ;;
    *) echo "Tala builds on macOS and Linux." >&2; exit 1 ;;
esac

cmake -S third_party/portaudio -B third_party/build-portaudio -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_POLICY_VERSION_MINIMUM=3.5 "${audio_flags[@]}"
cmake --build third_party/build-portaudio --parallel
cat > third_party/build-portaudio/portaudio-2.0.pc <<PC
Name: PortAudio
Description: PortAudio static build for Tala
Version: 19.7.0
Cflags: -I${PWD}/third_party/portaudio/include
Libs: -L${PWD}/third_party/build-portaudio -lportaudio ${audio_libs}
PC

export CPATH="$PWD/third_party/whisper.cpp/ggml/include:$PWD/third_party/whisper.cpp/ggml/src${CPATH:+:$CPATH}"
cmake -S third_party/whisper.cpp -B third_party/build-whisper -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_SHARED_LIBS=OFF "${whisper_flags[@]}"
cmake --build third_party/build-whisper --parallel
