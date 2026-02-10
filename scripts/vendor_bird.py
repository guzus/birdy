#!/usr/bin/env python3
"""
Download the upstream `bird` CLI binaries and stage them for GoReleaser archives.

Outputs:
  bundled/bird/bird_<os>_<arch>[.exe]

Configuration:
  - BIRD_VERSION: release tag like "v0.8.0" (default: latest)
  - BIRD_REPO: GitHub repo (default: steipete/bird)
  - BIRDY_BUNDLE_WINDOWS: "1" to require Windows asset (default: "0" = best-effort)
"""

from __future__ import annotations

import io
import json
import os
import shutil
import stat
import sys
import tarfile
import tempfile
import zipfile
from pathlib import Path
from urllib.request import Request, urlopen


def _http_json(url: str) -> dict:
    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN") or ""
    req = Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "User-Agent": "birdy-vendor-bird",
            **({"Authorization": f"Bearer {token}"} if token else {}),
        },
    )
    with urlopen(req, timeout=60) as resp:
        return json.loads(resp.read().decode("utf-8"))


def _download(url: str) -> bytes:
    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN") or ""
    req = Request(
        url,
        headers={
            "User-Agent": "birdy-vendor-bird",
            **({"Authorization": f"Bearer {token}"} if token else {}),
        },
    )
    with urlopen(req, timeout=120) as resp:
        return resp.read()


def _token_match(name: str, tokens: list[str]) -> bool:
    n = name.lower()
    return any(t in n for t in tokens)


def _pick_asset(assets: list[dict], goos: str, goarch: str) -> dict | None:
    os_tokens = {
        "darwin": ["darwin", "macos", "mac", "osx"],
        "linux": ["linux"],
        "windows": ["windows", "win"],
    }[goos]
    arch_tokens = {
        "amd64": ["amd64", "x86_64", "x64"],
        "arm64": ["arm64", "aarch64"],
    }[goarch]

    matches = []
    for a in assets:
        name = a.get("name") or ""
        lower = name.lower()
        if lower.endswith((".sha256", ".sha256sum", ".sig", ".asc", ".txt", ".json")):
            continue
        if _token_match(name, os_tokens) and _token_match(name, arch_tokens):
            matches.append(a)

    if not matches:
        return None

    # Prefer archives over raw binaries, and prefer smaller/more standard formats.
    def score(a: dict) -> tuple[int, int]:
        name = (a.get("name") or "").lower()
        # Lower is better.
        fmt = 0
        if name.endswith(".tar.gz") or name.endswith(".tgz"):
            fmt = 0
        elif name.endswith(".zip"):
            fmt = 1
        else:
            fmt = 2
        size = int(a.get("size") or 0)
        return (fmt, size)

    return sorted(matches, key=score)[0]


def _extract_to_dir(payload: bytes, asset_name: str, out_dir: Path) -> None:
    asset_name = asset_name.lower()
    if asset_name.endswith(".tar.gz") or asset_name.endswith(".tgz"):
        with tarfile.open(fileobj=io.BytesIO(payload), mode="r:gz") as tf:
            # Basic path traversal guard.
            for m in tf.getmembers():
                p = (out_dir / m.name).resolve()
                if not str(p).startswith(str(out_dir.resolve())):
                    raise RuntimeError(f"unsafe tar member path: {m.name}")
            tf.extractall(out_dir)
        return
    if asset_name.endswith(".zip"):
        with zipfile.ZipFile(io.BytesIO(payload)) as zf:
            # Basic path traversal guard.
            for n in zf.namelist():
                p = (out_dir / n).resolve()
                if not str(p).startswith(str(out_dir.resolve())):
                    raise RuntimeError(f"unsafe zip member path: {n}")
            zf.extractall(out_dir)
        return

    # Raw binary: write as-is.
    (out_dir / "bird").write_bytes(payload)


def _find_bird_binary(root: Path, want_windows: bool) -> Path | None:
    preferred = ["bird.exe"] if want_windows else ["bird"]
    for p in root.rglob("*"):
        if not p.is_file():
            continue
        if p.name in preferred:
            return p

    # Fallback heuristics: pick a single file in the root, or any executable-looking file.
    files = [p for p in root.rglob("*") if p.is_file()]
    if len(files) == 1:
        return files[0]

    if not want_windows:
        for p in files:
            try:
                if (p.stat().st_mode & stat.S_IXUSR) != 0 and p.name.lower().startswith("bird"):
                    return p
            except OSError:
                continue

    return None


def _ensure_executable(path: Path) -> None:
    try:
        mode = path.stat().st_mode
        path.chmod(mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    except OSError:
        # Best-effort; on some systems this may fail (or be irrelevant on Windows).
        pass


def main() -> int:
    repo = os.environ.get("BIRD_REPO", "steipete/bird")
    version = os.environ.get("BIRD_VERSION", "").strip()
    require_windows = os.environ.get("BIRDY_BUNDLE_WINDOWS", "1").strip() == "1"

    if version:
        rel = _http_json(f"https://api.github.com/repos/{repo}/releases/tags/{version}")
    else:
        rel = _http_json(f"https://api.github.com/repos/{repo}/releases/latest")

    assets = rel.get("assets") or []
    if not assets:
        print("vendor_bird: no assets found in upstream release", file=sys.stderr)
        return 1

    targets = [
        ("darwin", "amd64", True),
        ("darwin", "arm64", True),
        ("linux", "amd64", True),
        ("linux", "arm64", True),
        ("windows", "amd64", require_windows),
        ("windows", "arm64", require_windows),
    ]

    out_root = Path("bundled/bird")
    out_root.mkdir(parents=True, exist_ok=True)

    for goos, goarch, required in targets:
        asset = _pick_asset(assets, goos, goarch)
        if asset is None:
            msg = f"vendor_bird: no matching asset for {goos}/{goarch}"
            if required:
                print(msg, file=sys.stderr)
                return 1
            print(msg + " (skipping)", file=sys.stderr)
            continue

        url = asset.get("browser_download_url")
        name = asset.get("name") or ""
        if not url or not name:
            print(f"vendor_bird: invalid asset for {goos}/{goarch}", file=sys.stderr)
            return 1

        print(f"vendor_bird: downloading {name} for {goos}/{goarch}", file=sys.stderr)
        payload = _download(url)

        with tempfile.TemporaryDirectory() as td:
            tmp = Path(td)
            _extract_to_dir(payload, name, tmp)
            want_windows = goos == "windows"
            bird_path = _find_bird_binary(tmp, want_windows=want_windows)
            if bird_path is None:
                msg = f"vendor_bird: could not locate bird binary after extracting {name}"
                if required:
                    print(msg, file=sys.stderr)
                    return 1
                print(msg + " (skipping)", file=sys.stderr)
                continue

            dst = out_root / f"bird_{goos}_{goarch}{'.exe' if want_windows else ''}"
            shutil.copy2(bird_path, dst)
            _ensure_executable(dst)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
