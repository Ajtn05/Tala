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
whisper_tag="$(cat WHISPER_VERSION)"
whisper_source=third_party/whisper.cpp
whisper_build=third_party/build-whisper
source_tree "$whisper_source" https://github.com/ggml-org/whisper.cpp.git "$whisper_tag"
source_tree third_party/portaudio https://github.com/PortAudio/portaudio.git v19.7.0

whisper_flags=(-DGGML_NATIVE=OFF -DWHISPER_BUILD_EXAMPLES=OFF -DWHISPER_BUILD_TESTS=OFF)
audio_flags=(-DPA_BUILD_STATIC=ON -DPA_BUILD_SHARED=OFF -DPA_BUILD_EXAMPLES=OFF)
audio_libs=""
case "$(uname -s)" in
    Darwin)
        # Keep the pinned sources pristine and give Go a distinct library search path.
        whisper_source=third_party/whisper-metal
        whisper_build=third_party/build-whisper-metal
        source_tree "$whisper_source" "file://$PWD/third_party/whisper.cpp" "$whisper_tag"
        # Backport the Metal 4 tensor fix from whisper.cpp commit 5032008.
        patch="$PWD/scripts/patches/whisper-metal-tensor.patch"
        if ! git -C "$whisper_source" apply --reverse --check "$patch" 2>/dev/null; then
            git -C "$whisper_source" apply "$patch"
        fi
        whisper_flags+=(-DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON -DCMAKE_OSX_DEPLOYMENT_TARGET=14.2)
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

export CPATH="$PWD/$whisper_source/ggml/include:$PWD/$whisper_source/ggml/src${CPATH:+:$CPATH}"
cmake -S "$whisper_source" -B "$whisper_build" -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_SHARED_LIBS=OFF "${whisper_flags[@]}"
cmake --build "$whisper_build" --parallel
