#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

tag=v0.9.0
revision=cd959bae99ff4bed22bef2604f45241c09bbb8c8
source=third_party/llama.cpp
build=third_party/build-llama
if [[ ! -f "$source/CMakeLists.txt" ]]; then
    git clone --depth 1 --branch "$tag" https://github.com/ggml-org/llama.cpp.git "$source"
fi
if [[ "$(git -C "$source" rev-parse HEAD)" != "$revision" ]]; then
    echo "Expected llama.cpp $tag in $source." >&2
    exit 1
fi
cmake -S "$source" -B "$build" -DCMAKE_BUILD_TYPE=Release -DCMAKE_OSX_DEPLOYMENT_TARGET=14.2 \
    -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON \
    -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_SERVER=ON \
    -ULLAMA_BUILD_WEBUI -DLLAMA_BUILD_UI=OFF -DLLAMA_USE_PREBUILT_UI=OFF -DLLAMA_OPENSSL=OFF
cmake --build "$build" --target llama-server --parallel 6
