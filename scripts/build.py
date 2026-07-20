#!/usr/bin/env python3
"""Cross-compile the sdb-mcp server for Windows, macOS, and Linux.

All binaries are written to the dist/ directory, named
`sdb-mcp-<os>-<arch>` (with a `.exe` suffix on Windows).

Usage:
    python scripts/build.py                 # build every target
    python scripts/build.py windows linux   # build only the named OSes
    python scripts/build.py --clean         # wipe dist/ before building
"""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

# Repo root is the parent of this script's directory (scripts/).
ROOT = Path(__file__).resolve().parent.parent
DIST = ROOT / "dist"
PKG = "./cmd/sdb-mcp"
BINARY = "sdb-mcp"
DEFAULT_VERSION = "0.1.0"

# (GOOS, GOARCH) build matrix.
TARGETS = [
    ("windows", "amd64"),
    ("windows", "arm64"),
    ("darwin", "amd64"),   # Intel Macs
    ("darwin", "arm64"),   # Apple Silicon
    ("linux", "amd64"),
    ("linux", "arm64"),
]

def ldflags(version: str) -> str:
    # Strip symbol/debug tables for smaller release binaries and inject the
    # build version into main.version.
    return f"-s -w -X main.version={version}"


def output_name(goos: str, goarch: str) -> str:
    suffix = ".exe" if goos == "windows" else ""
    return f"{BINARY}-{goos}-{goarch}{suffix}"


def build(goos: str, goarch: str, version: str) -> Path:
    out = DIST / output_name(goos, goarch)
    env = os.environ.copy()
    # CGO_ENABLED=0 produces static binaries and lets us cross-compile without
    # a per-target C toolchain.
    env.update(GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
    # -trimpath drops local filesystem paths for reproducible builds.
    cmd = ["go", "build", "-trimpath", "-ldflags", ldflags(version), "-o", str(out), PKG]
    print(f"  building {goos}/{goarch} -> dist/{out.name}")
    subprocess.run(cmd, cwd=ROOT, env=env, check=True)
    return out


def human_size(num_bytes: int) -> str:
    size = float(num_bytes)
    for unit in ("B", "KB", "MB", "GB"):
        if size < 1024:
            return f"{size:.1f} {unit}"
        size /= 1024
    return f"{size:.1f} TB"


def main() -> int:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "os",
        nargs="*",
        choices=["windows", "darwin", "linux"],
        help="limit the build to these operating systems (default: all)",
    )
    parser.add_argument(
        "--clean",
        action="store_true",
        help="remove dist/ before building",
    )
    parser.add_argument(
        "--version",
        default=DEFAULT_VERSION,
        help=f"version string injected into the binary (default: {DEFAULT_VERSION})",
    )
    args = parser.parse_args()

    if shutil.which("go") is None:
        print("error: 'go' toolchain not found on PATH", file=sys.stderr)
        return 1

    targets = TARGETS
    if args.os:
        wanted = set(args.os)
        targets = [t for t in TARGETS if t[0] in wanted]

    if args.clean and DIST.exists():
        print(f"cleaning {DIST}")
        shutil.rmtree(DIST)

    DIST.mkdir(parents=True, exist_ok=True)

    print(f"building {len(targets)} target(s) (version {args.version}) into {DIST}")
    built: list[Path] = []
    for goos, goarch in targets:
        try:
            built.append(build(goos, goarch, args.version))
        except subprocess.CalledProcessError as exc:
            print(
                f"error: build failed for {goos}/{goarch} (exit {exc.returncode})",
                file=sys.stderr,
            )
            return exc.returncode

    print("\ndone:")
    for path in built:
        print(f"  {path.relative_to(ROOT)}  ({human_size(path.stat().st_size)})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
