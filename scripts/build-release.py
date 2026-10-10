"""Build a Facet release: the core and app products for every target.

    python scripts/build-release.py --out /abs/empty/dir [--version X.Y.Z]
        [--targets linux/amd64 ...] [--downloads DIR]

Writes one archive per product and target, manifest.tsv (every archive and
the SHA-256 of every file in it, which the installers read),
build-manifest.json, the installers and SHA256SUMS. Built from a clean
checkout: the static files are the committed bytes of HEAD.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import zipfile

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from release_contract import (COMPA_NOTICE, INSTALLERS, PRODUCTS, TARGETS, app_members, archive_name, binary_modules,
                              core_members, git_blob_bytes, release_tsv, release_version, sha256_file, take_compa,
                              validate_member_set)

ROOT = Path(__file__).resolve().parent.parent
# Compiles Windows version information and an application manifest into a
# resource object at build time; pinned, and verified against the Go
# checksum database. Nothing from it is linked into the programs.
GO_WINRES = "github.com/tc-hib/go-winres@v0.3.3"
COMMANDS = {
    "core": ("./cmd/facet", "facet", "Facet", "Facet Toolkit"),
    "app": ("./cmd/facet-app", "facet-app", "Facet App", "Facet App"),
}


def run(*args, **kwargs):
    return subprocess.check_output(args, cwd=ROOT, text=True, **kwargs).strip()


def package_version():
    return release_version(json.loads((ROOT / "package.json").read_text(encoding="utf-8"))["version"])


def notices_module():
    spec = importlib.util.spec_from_file_location("generate_notices", ROOT / "scripts" / "generate-notices.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def member_mode(name):
    return 0o755 if name.startswith(("bin/", "kernel/compa-kernel")) else 0o644


def write_archive(path, files):
    if any(source.is_symlink() or not source.is_file() for source, _ in files):
        raise RuntimeError("Archive inputs must be ordinary files, not links")
    validate_member_set([name for _, name in files])
    if path.suffix == ".zip":
        with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as archive:
            for source, name in files:
                info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
                info.compress_type = zipfile.ZIP_DEFLATED
                info.external_attr = (0o100000 | member_mode(name)) << 16
                archive.writestr(info, source.read_bytes())
    else:
        with tarfile.open(path, "w:gz") as archive:
            for source, name in files:
                info = archive.gettarinfo(str(source), arcname=name)
                info.uid = info.gid = 0
                info.uname = info.gname = ""
                info.mtime = 0
                info.mode = member_mode(name)
                with source.open("rb") as stream:
                    archive.addfile(info, stream)


def build(product, target, version, out, work):
    """Build one command for target, with Windows version information."""
    package, name, product_name, description = COMMANDS[product]
    system, arch = target.split("/")
    binary = out / (name + (".exe" if system == "windows" else ""))
    env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0", GOWORK="off", GOFLAGS="-mod=readonly")
    resource = None
    if system == "windows":
        rsrc = work / "rsrc" / product / arch
        rsrc.mkdir(parents=True, exist_ok=True)
        subprocess.run(["go", "run", GO_WINRES, "simply", "--arch", arch, "--out", str(rsrc / "rsrc"),
                        "--manifest", "cli", "--product-name", product_name, "--file-description", description,
                        "--product-version", version, "--file-version", version,
                        "--original-filename", binary.name, "--copyright", "Facet Contributors. AGPL-3.0-or-later."],
                       cwd=ROOT, env=dict(os.environ, CGO_ENABLED="0"), check=True)
        # Linked because it sits in the package directory; removed again.
        resource = ROOT / package / f"rsrc_windows_{arch}.syso"
        resource.write_bytes((rsrc / f"rsrc_windows_{arch}.syso").read_bytes())
    try:
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", f"-ldflags=-s -w -X main.Version={version}",
                        "-o", str(binary), package], cwd=ROOT, env=env, check=True)
    finally:
        if resource is not None:
            resource.unlink(missing_ok=True)
    binary.chmod(0o755)
    return binary, env


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--downloads", type=Path, help="folder that keeps Compa's release archives between builds")
    parser.add_argument("--targets", nargs="+", choices=TARGETS, default=list(TARGETS))
    parser.add_argument("--version", help="release version, without a v prefix (default: package.json's)")
    args = parser.parse_args()
    version = release_version(args.version) if args.version else package_version()
    if len(set(args.targets)) != len(args.targets):
        raise SystemExit("Duplicate target")
    if run("git", "status", "--porcelain"):
        raise SystemExit("Packaging requires a clean checkout")
    commit = run("git", "rev-parse", "HEAD")
    out = args.out
    if not out.is_absolute() or out.resolve() == ROOT or ROOT in out.resolve().parents:
        raise SystemExit("--out must be absolute and outside the checkout")
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        raise SystemExit("Output directory must be empty")
    notices = notices_module()
    archives, entries, binaries = [], [], {product: {} for product in PRODUCTS}
    with tempfile.TemporaryDirectory(prefix="facet-release-") as temporary:
        work = Path(temporary)
        static = work / "static"
        static.mkdir()
        license_file = static / "LICENSE"
        license_file.write_bytes(git_blob_bytes(ROOT, commit, ROOT / "LICENSE"))
        hand_notices = static / "THIRD_PARTY_NOTICES.md"
        hand_notices.write_bytes(git_blob_bytes(ROOT, commit, ROOT / "THIRD_PARTY_NOTICES.md"))
        downloads = args.downloads.resolve() if args.downloads else work / "downloads"
        downloads.mkdir(parents=True, exist_ok=True)

        def archive(product, target, files):
            name = archive_name(product, version, target)
            write_archive(out / name, files)
            entries.append((product, target, name, {relative: sha256_file(source) for source, relative in files}))
            archives.append(name)

        for target in args.targets:
            place = work / target.replace("/", "_")
            place.mkdir()
            system, arch = target.split("/")
            core, _ = build("core", target, version, place, work)
            app, env = build("app", target, version, place, work)
            binaries["core"][target], binaries["app"][target] = sha256_file(core), sha256_file(app)
            core_notices = place / "core-notices.md"
            text, _ = notices.generate(system, arch, "./cmd/facet", hand_notices)
            core_notices.write_text(text, encoding="utf-8", newline="\n")
            archive("core", target, [(core, f"bin/{core.name}"), (license_file, "LICENSE"),
                                     (core_notices, "THIRD_PARTY_NOTICES.md")])
            compa = take_compa(target, downloads, place / "compa")
            kernel = next(path for path, name in compa if name.startswith("kernel/compa-kernel"))
            app_notices = place / "app-notices.md"
            text, _ = notices.generate(system, arch, "./cmd/facet-app", hand_notices)
            text += COMPA_NOTICE + "".join(f"- {path} {module_version}\n" for path, module_version in binary_modules(kernel, env))
            app_notices.write_text(text, encoding="utf-8", newline="\n")
            archive("app", target, [(app, f"bin/{app.name}"), *compa, (license_file, "app/LICENSE"),
                                    (app_notices, "app/THIRD_PARTY_NOTICES.md")])
            for product, members in (("core", core_members(target)), ("app", app_members(target))):
                if sorted(entries[-2 if product == "core" else -1][3]) != sorted(members):
                    raise SystemExit(f"{product} for {target} does not hold exactly its contract's files")
    for name in INSTALLERS:
        (out / name).write_bytes(git_blob_bytes(ROOT, commit, ROOT / name))
    manifest = {
        "version": version, "commit": commit, "go": run("go", "version"),
        "targets": args.targets, "products": list(PRODUCTS),
        "core_binaries": binaries["core"], "app_binaries": binaries["app"],
        "archives": archives, "installers": list(INSTALLERS),
    }
    (out / "build-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8", newline="\n")
    (out / "manifest.tsv").write_text(release_tsv(version, commit, entries), encoding="utf-8", newline="\n")
    names = archives + list(INSTALLERS) + ["build-manifest.json", "manifest.tsv"]
    (out / "SHA256SUMS").write_text("".join(sha256_file(out / name) + "  " + name + "\n" for name in sorted(names)),
                                    encoding="utf-8", newline="\n")
    if run("git", "status", "--porcelain"):
        raise SystemExit("The checkout changed while packaging; these files must not be published")
    print(f"Built Facet {version}: core and app for {', '.join(args.targets)}")


if __name__ == "__main__":
    main()
