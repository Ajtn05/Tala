#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

case "$(uname -s)" in
    Darwin)
        python3 - <<'PY'
from pathlib import Path
import subprocess
import tempfile

dist = Path("dist").resolve()
with tempfile.TemporaryDirectory(prefix=".package-", dir=dist) as directory:
    stage = Path(directory)
    volume = stage / "volume"
    volume.mkdir()
    subprocess.run(["ditto", dist / "Tala.app", volume / "Tala.app"], check=True)
    (volume / "Applications").symlink_to("/Applications")
    image = stage / "Tala.dmg"
    subprocess.run(["hdiutil", "create", "-volname", "Tala", "-srcfolder", volume, "-format", "UDZO", image], check=True)
    subprocess.run(["hdiutil", "verify", image], check=True)
    image.replace(dist / "Tala.dmg")
PY
        ;;
    Linux)
        mkdir -p dist
        stage="$(mktemp -d dist/.package.XXXXXX)"
        trap 'rm -r -- "$stage"' EXIT
        cp tala "$stage/tala"
        bash scripts/copy-notices.sh "$stage/licenses"
        archive="tala-linux-$(uname -m).tar.gz"
        tar -C "$stage" -czf "$stage/$archive" tala licenses
        mv -f "$stage/$archive" "dist/$archive"
        ;;
    *) echo "Packaging is supported on macOS and Linux." >&2; exit 1 ;;
esac
