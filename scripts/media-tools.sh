#!/usr/bin/env bash
set -euo pipefail

tools="$1"
notices="$2"
cache="$PWD/third_party/downloads"
mkdir -p "$tools" "$notices" "$cache"

# Pin both the upstream versions and the bytes used for release builds.
ffmpeg_version=9.0.2
yt_dlp_version=2026.10.26
deno_version=v2.9.8
ffmpeg_flags=(--disable-autodetect --disable-doc --disable-ffplay --enable-zlib --enable-version3)
case "$(uname -s)-$(uname -m)" in
    Darwin-arm64)
        export MACOSX_DEPLOYMENT_TARGET=14.2
        ffmpeg_flags+=(--enable-securetransport)
        yt_dlp_asset=yt-dlp_macos
        yt_dlp_sha=0f922c15ccc0b83eefa1c8bc9104e434318c4bf16e408e1ee93b7745a6efe0aa
        deno_asset=deno-aarch64-apple-darwin.zip
        deno_sha=9f0bf660393da2eaff044cdc7444cb08c73f235849b61d224d825a194ce7c98e
        jobs="$(sysctl -n hw.ncpu)"
        ;;
    Linux-x86_64)
        ffmpeg_flags+=(--enable-openssl)
        yt_dlp_asset=yt-dlp_linux
        yt_dlp_sha=767ba9dce7052aa196f8b12652f433d76de128ba168966fa84b6394d5e6c6795
        deno_asset=deno-x86_64-unknown-linux-gnu.zip
        deno_sha=e890975bc2549807bbcc0b584ffafda72cc7c93349dd99c80483dd784f6c4609
        jobs="$(nproc)"
        ;;
    *) echo "Media tools are packaged for Apple Silicon macOS and Linux x86_64." >&2; exit 1 ;;
esac

tmp="$(mktemp -d "$cache/.media.XXXXXX")"
trap 'rm -rf -- "$tmp"' EXIT

fetch() {
    local file="$1" sha="$2" url="$3"
    if [[ ! -f "$file" ]] || ! printf '%s  %s\n' "$sha" "$file" | shasum -a 256 -c --status; then
        curl -fsSL --retry 3 --connect-timeout 20 --max-time 180 -o "$tmp/download" "$url"
        printf '%s  %s\n' "$sha" "$tmp/download" | shasum -a 256 -c
        mv -f "$tmp/download" "$file"
    fi
}

ffmpeg_source="$cache/ffmpeg-$ffmpeg_version.tar.xz"
fetch "$ffmpeg_source" 8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e \
    "https://ffmpeg.org/releases/ffmpeg-$ffmpeg_version.tar.xz"
ffmpeg_build="$cache/ffmpeg-$ffmpeg_version-$(uname -s)-$(uname -m)"
if [[ ! -d "$ffmpeg_build" ]]; then
    tar -xf "$ffmpeg_source" -C "$tmp"
    mv "$tmp/ffmpeg-$ffmpeg_version" "$ffmpeg_build"
fi
(
    cd "$ffmpeg_build"
    printf '%s\n' "MACOSX_DEPLOYMENT_TARGET=${MACOSX_DEPLOYMENT_TARGET:-} ./configure ${ffmpeg_flags[*]}" > "$tmp/configure"
    if ! cmp -s "$tmp/configure" .tala-configure; then
        rm -f .tala-configure
        ./configure "${ffmpeg_flags[@]}"
        cp "$tmp/configure" .tala-configure
    fi
    make -j "$jobs" ffmpeg ffprobe
)
install -m 755 "$ffmpeg_build/ffmpeg" "$ffmpeg_build/ffprobe" "$tools"
cp "$ffmpeg_build/COPYING.LGPLv3" "$notices/FFmpeg-LGPLv3.txt"
cp "$ffmpeg_build/COPYING.GPLv3" "$notices/GPLv3.txt"
cp "$ffmpeg_build/.tala-configure" "$notices/FFmpeg-build.txt"
cp "$ffmpeg_source" "$notices/"

yt_dlp_url="https://github.com/yt-dlp/yt-dlp/releases/download/$yt_dlp_version"
yt_dlp_binary="$cache/yt-dlp-$yt_dlp_version-${yt_dlp_asset#yt-dlp_}"
fetch "$yt_dlp_binary" "$yt_dlp_sha" "$yt_dlp_url/$yt_dlp_asset"
install -m 755 "$yt_dlp_binary" "$tools/yt-dlp"
yt_dlp_raw="https://raw.githubusercontent.com/yt-dlp/yt-dlp/$yt_dlp_version"
fetch "$cache/yt-dlp-$yt_dlp_version-LICENSE" 7e12e5df4bae12cb21581ba157ced20e1986a0508dd10d0e8a4ab9a4cf94e85c "$yt_dlp_raw/LICENSE"
fetch "$cache/yt-dlp-$yt_dlp_version-THIRD_PARTY_LICENSES.txt" 472aefe951c7db35e1657c1d13fd337140511ed6f2b329205105ad441c5a02b7 "$yt_dlp_raw/THIRD_PARTY_LICENSES.txt"
cp "$cache/yt-dlp-$yt_dlp_version-LICENSE" "$notices/yt-dlp-LICENSE.txt"
cp "$cache/yt-dlp-$yt_dlp_version-THIRD_PARTY_LICENSES.txt" "$notices/yt-dlp-THIRD_PARTY_LICENSES.txt"

deno_zip="$cache/deno-$deno_version-${deno_asset#deno-}"
fetch "$deno_zip" "$deno_sha" "https://github.com/denoland/deno/releases/download/$deno_version/$deno_asset"
unzip -p "$deno_zip" deno > "$tmp/deno"
install -m 755 "$tmp/deno" "$tools/deno"
fetch "$cache/deno-$deno_version-LICENSE.md" f62497fffecc0852960c8d3e6934b9db86d16396e9b604072e923892cae3a588 \
    "https://raw.githubusercontent.com/denoland/deno/$deno_version/LICENSE.md"
cp "$cache/deno-$deno_version-LICENSE.md" "$notices/Deno-LICENSE.md"

cat > "$notices/Media-tools.txt" <<EOF
FFmpeg $ffmpeg_version (LGPLv3 or later)
The source archive and the build configuration are included alongside this file.

yt-dlp $yt_dlp_version (upstream standalone executable, GPLv3 or later)
Source: $yt_dlp_url/yt-dlp.tar.gz
Build and dependency versions: https://github.com/yt-dlp/yt-dlp/tree/$yt_dlp_version/bundle
See yt-dlp-THIRD_PARTY_LICENSES.txt for the included components and their sources.

Deno $deno_version (MIT)
Source: https://github.com/denoland/deno/tree/$deno_version
EOF
