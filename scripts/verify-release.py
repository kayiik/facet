"""Verify a Facet release folder before it is published.

    python scripts/verify-release.py --directory DIR [--tag vX.Y.Z] [--commit SHA] [--smoke]

Checks SHA256SUMS, manifest.tsv and build-manifest.json against each other
and against every file in every archive. With --smoke, unpacks this
platform's core and app into a programs folder and runs facet, facet-app and
the kernel.
"""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import platform
import subprocess
import sys
import tarfile
import tempfile
import zipfile

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from release_contract import COMPA_VERSION, INSTALLERS, PRODUCTS, parse_release_tsv, release_version, sha256_file


def fail(message):
    raise SystemExit(f"verify-release: {message}")


def native_target():
    system = {"Windows": "windows", "Linux": "linux", "Darwin": "darwin"}.get(platform.system())
    arch = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
    if not system or not arch:
        fail(f"no release target for {platform.system()} {platform.machine()}")
    return f"{system}/{arch}"


def members(path):
    """{name: (bytes digest, mode)} of an archive's files; anything else fails."""
    found = {}
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            for info in archive.infolist():
                if info.is_dir() or (info.external_attr >> 16) & 0o170000 not in (0, 0o100000):
                    fail(f"{path.name}: {info.filename} is not a regular file")
                found[info.filename] = (hashlib.sha256(archive.read(info)).hexdigest(), (info.external_attr >> 16) & 0o777)
    else:
        with tarfile.open(path) as archive:
            for info in archive.getmembers():
                if not info.isfile():
                    fail(f"{path.name}: {info.name} is not a regular file")
                with archive.extractfile(info) as stream:
                    found[info.name] = (hashlib.sha256(stream.read()).hexdigest(), info.mode & 0o777)
    return found


def verify(directory, tag=None, commit=None):
    sums = {}
    for line in (directory / "SHA256SUMS").read_text(encoding="utf-8").splitlines():
        digest, name = line.split("  ", 1)
        sums[name] = digest
    manifest = parse_release_tsv((directory / "manifest.tsv").read_text(encoding="utf-8"))
    build = json.loads((directory / "build-manifest.json").read_text(encoding="utf-8"))
    version = release_version(manifest["version"])
    if build["version"] != version or (tag and tag != "v" + version):
        fail("the manifests and the tag name different versions")
    if build["commit"] != manifest["commit"] or (commit and commit != manifest["commit"]):
        fail("the manifests and the source commit disagree")
    expected = sorted(list(manifest["archives"].values()) + list(INSTALLERS) + ["build-manifest.json", "manifest.tsv"])
    present = sorted(p.name for p in directory.iterdir() if p.is_file() and p.name != "SHA256SUMS")
    if sorted(sums) != expected or present != expected:
        fail(f"the release holds {present}; SHA256SUMS lists {sorted(sums)}; the manifest wants {expected}")
    for name, digest in sums.items():
        if sha256_file(directory / name) != digest:
            fail(f"{name} does not match SHA256SUMS")
    for (product, target), name in manifest["archives"].items():
        found = members(directory / name)
        wanted = manifest["files"][(product, target)]
        if sorted(found) != sorted(wanted):
            fail(f"{name} holds {sorted(found)}, not {sorted(wanted)}")
        for member, (digest, mode) in found.items():
            if digest != wanted[member]:
                fail(f"{name}: {member} does not match manifest.tsv")
            if PurePosixPath(member).parts[0] == "bin" and mode & 0o111 == 0:
                fail(f"{name}: {member} is not executable")
        binary = next(m for m in wanted if m.startswith("bin/"))
        if build[f"{product}_binaries"].get(target) != wanted[binary]:
            fail(f"build-manifest.json and {name} disagree about {binary}")
    for product in PRODUCTS:
        if sorted(build[f"{product}_binaries"]) != sorted(build["targets"]):
            fail(f"build-manifest.json lists {product} binaries for other targets than it built")
    return manifest


def smoke(directory, manifest):
    target = native_target()
    version = manifest["version"]
    with tempfile.TemporaryDirectory(prefix="facet-verify-") as temporary:
        programs = Path(temporary) / "Facet"
        programs.mkdir()
        for product in PRODUCTS:
            name = manifest["archives"].get((product, target))
            if not name:
                fail(f"the release has no {product} for {target}")
            path = directory / name
            if path.suffix == ".zip":
                with zipfile.ZipFile(path) as archive:
                    archive.extractall(programs)
            else:
                with tarfile.open(path) as archive:
                    archive.extractall(programs, filter="data")
        exe = ".exe" if target.startswith("windows/") else ""
        checks = (
            ([programs / "bin" / f"facet{exe}", "version"], f"facet v{version}"),
            ([programs / "bin" / f"facet-app{exe}", "version"], f"facet-app {version}"),
            ([programs / "kernel" / f"compa-kernel{exe}", "version"], f"compa-kernel {COMPA_VERSION}"),
        )
        for command, wanted in checks:
            out = subprocess.run([str(part) for part in command], capture_output=True, text=True, timeout=120)
            if out.returncode != 0 or wanted not in out.stdout:
                fail(f"{command[0].name} {' '.join(command[1:])} printed {out.stdout.strip()!r}, want {wanted!r}")
        report = subprocess.run([str(programs / "bin" / f"facet{exe}"), "components", "--json"],
                                capture_output=True, text=True, timeout=300)
        if report.returncode != 0 or not json.loads(report.stdout).get("ok"):
            fail(f"facet components failed in the unpacked programs folder: {report.stderr.strip()}")
    print(f"Smoke: facet, facet-app and compa-kernel {COMPA_VERSION} run from the {target} archives")


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--tag")
    parser.add_argument("--commit")
    parser.add_argument("--smoke", action="store_true")
    args = parser.parse_args()
    manifest = verify(args.directory, args.tag, args.commit)
    print(f"Verified Facet {manifest['version']}: {len(manifest['archives'])} archives, "
          f"{sum(len(files) for files in manifest['files'].values())} files")
    if args.smoke:
        smoke(args.directory, manifest)


if __name__ == "__main__":
    main()
