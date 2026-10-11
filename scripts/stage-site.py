"""Stage the website: site/ from this checkout, with the installers of a
published stable release, which this checkout's history must contain.

    python scripts/stage-site.py --release-dir DIR --out NEW_DIR --tag vX.Y.Z

DIR holds the release's install.ps1, install.sh, SHA256SUMS and
build-manifest.json, as downloaded. The page names the release in place of
@TAG@ and @VERSION@.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys

sys.dont_write_bytecode = True

RELEASE_FILES = ("install.ps1", "install.sh", "SHA256SUMS", "build-manifest.json")


def site_files(page):
    """Every file under page, relative to it; links and other kinds are refused."""
    files = []
    for path in sorted(page.rglob("*")):
        if path.is_symlink() or not (path.is_dir() or path.is_file()):
            raise ValueError("Site input is not a regular file: " + path.relative_to(page).as_posix())
        if path.is_file():
            files.append(path.relative_to(page))
    return files


def stage_site(release, page, output, tag, contains):
    """contains(commit) answers whether this checkout's history includes the release's commit."""
    if not re.fullmatch(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)", tag):
        raise ValueError("Only a stable release may replace the public installers")
    checksums = {}
    for line in (release / "SHA256SUMS").read_text(encoding="utf-8").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        if not match or match[2] in checksums:
            raise ValueError("Invalid release checksum inventory")
        checksums[match[2]] = match[1]
    for name in ("install.ps1", "install.sh", "build-manifest.json"):
        path = release / name
        if not path.is_file() or path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != checksums.get(name):
            raise ValueError("Release download is missing or corrupt: " + name)
    manifest = json.loads((release / "build-manifest.json").read_text(encoding="utf-8"))
    commit = manifest.get("commit")
    if (manifest.get("version") != tag[1:] or not isinstance(commit, str)
            or not re.fullmatch(r"[0-9a-f]{40}(?:[0-9a-f]{24})?", commit)):
        raise ValueError("The release manifest does not match the tag")
    if not contains(commit):
        raise ValueError("This checkout does not contain the release's source commit")
    if output.exists():
        raise ValueError("Choose a new site staging directory")
    pages = site_files(page)
    if Path("index.html") not in pages:
        raise ValueError("The site has no index.html")
    if any(relative.as_posix() in RELEASE_FILES for relative in pages):
        raise ValueError("The site must not carry its own installers or checksums")
    index = (page / "index.html").read_text(encoding="utf-8")
    if "@TAG@" not in index or "@VERSION@" not in index:
        raise ValueError("The page names no release")
    output.mkdir(parents=True)
    for relative in pages:
        (output / relative).parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(page / relative, output / relative)
    for name in RELEASE_FILES:
        shutil.copyfile(release / name, output / name)
    (output / "index.html").write_text(index.replace("@TAG@", tag).replace("@VERSION@", tag[1:]),
                                       encoding="utf-8", newline="\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--release-dir", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--tag", required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]

    def contains(commit):
        return subprocess.run(["git", "merge-base", "--is-ancestor", commit, "HEAD"], cwd=root,
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0

    stage_site(args.release_dir, root / "site", args.out, args.tag, contains)
    print("Staged the site with the verified installers of " + args.tag)


if __name__ == "__main__":
    main()
