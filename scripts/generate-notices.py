#!/usr/bin/env python3
"""Generate the third-party notices that ship beside a Facet release binary.

The output begins with the repository's hand-maintained THIRD_PARTY_NOTICES.md,
reproduced unchanged, and then appends the complete license and notice texts
of every Go module linked into the binary for one target platform, followed by
the Go toolchain's own license (the standard library and runtime are linked
into every Go binary).

The module set comes from `go list -deps` for the requested GOOS/GOARCH with
cgo disabled, as release builds are, so it is exactly the set of packages
compiled into that binary rather than everything go.mod happens to mention.
License texts are read from the module cache (`go env GOMODCACHE`). A linked
module without a license file is an error, never a silent omission.

The output is deterministic: modules are sorted, line endings are normalized
to LF, and nothing time- or host-dependent is written.

Usage:
  python scripts/generate-notices.py --goos linux --goarch amd64 --out NOTICES.md
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import textwrap

REPO = Path(__file__).resolve().parent.parent
DEFAULT_PACKAGE = "./cmd/facet"
DEFAULT_NOTICES = REPO / "THIRD_PARTY_NOTICES.md"

# Root-directory file name prefixes, compared case-insensitively. "LICENCE" is
# not a typo: some modules use the British spelling (for example LICENCE.md).
LICENSE_PREFIXES = ("license", "licence", "copying")
NOTICE_PREFIXES = ("notice",)
# A source file that happens to start with a license prefix (license.go) is
# code, not a license text.
NOT_LICENSE_SUFFIXES = {
    ".go", ".s", ".c", ".h", ".cc", ".py", ".js", ".ts", ".json", ".yaml",
    ".yml", ".toml", ".sh", ".bat", ".ps1", ".mod", ".sum",
}
GO_TOOLCHAIN_FILES = ("LICENSE", "PATENTS")
TARGET_PATTERN = re.compile(r"^[a-z0-9]+$")


class NoticesError(Exception):
    """A notices file cannot be produced faithfully."""


def run_go(arguments, env=None, cwd=REPO):
    try:
        result = subprocess.run(
            ["go", *arguments], cwd=cwd, env=env, capture_output=True,
            text=True, encoding="utf-8", errors="replace", timeout=600,
        )
    except FileNotFoundError as error:
        raise NoticesError("the go toolchain is required but was not found on PATH") from error
    except subprocess.TimeoutExpired as error:
        raise NoticesError(f"go {' '.join(arguments)} timed out") from error
    if result.returncode != 0:
        raise NoticesError(f"go {' '.join(arguments)} failed:\n{result.stderr.strip()}")
    return result.stdout


def target_environment(goos, goarch):
    for name, value in (("goos", goos), ("goarch", goarch)):
        if not TARGET_PATTERN.match(value or ""):
            raise NoticesError(f"invalid --{name} value: {value!r}")
    return dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")


def parse_json_stream(text):
    """Decode the concatenated JSON objects that `go list -json` prints."""
    decoder = json.JSONDecoder()
    values, index = [], 0
    while True:
        while index < len(text) and text[index].isspace():
            index += 1
        if index >= len(text):
            return values
        value, index = decoder.raw_decode(text, index)
        values.append(value)


def escape_module_path(path):
    """Apply the module cache's case encoding (Upper -> !upper)."""
    return re.sub(r"[A-Z]", lambda match: "!" + match.group(0).lower(), path)


def linked_modules(package, goos, goarch, cwd=REPO):
    """Return the non-standard, non-main modules linked into `package`.

    Each entry is a dict with path, version, dir and an optional replacement
    description, sorted by module path and version.
    """
    env = target_environment(goos, goarch)
    packages = parse_json_stream(run_go(["list", "-deps", "-json", package], env=env, cwd=cwd))
    if not packages:
        raise NoticesError(f"go list returned no packages for {package}")
    modules = {}
    for entry in packages:
        if entry.get("Standard"):
            continue
        module = entry.get("Module")
        if not module:
            raise NoticesError(f"package {entry.get('ImportPath')} is not provided by any module; its license cannot be attributed")
        if module.get("Main"):
            continue
        replace = module.get("Replace") or {}
        key = (module["Path"], module.get("Version", ""))
        modules[key] = {
            "path": module["Path"],
            "version": module.get("Version", ""),
            "dir": replace.get("Dir") or module.get("Dir") or "",
            "replace": " ".join(filter(None, [replace.get("Path", ""), replace.get("Version", "")])),
        }
    return [modules[key] for key in sorted(modules)]


def module_directory(module, gomodcache):
    candidates = []
    if module.get("dir"):
        candidates.append(Path(module["dir"]))
    if gomodcache and module.get("version") and not module.get("replace"):
        candidates.append(Path(gomodcache) / f"{escape_module_path(module['path'])}@{escape_module_path(module['version'])}")
    for candidate in candidates:
        if candidate.is_dir():
            return candidate
    raise NoticesError(
        f"module {module['path']} {module['version']} is not in the module cache "
        f"({gomodcache}); run `go mod download` first"
    )


def license_files(directory):
    """Split a module root's legal files into (licenses, notices), sorted."""
    licenses, notices = [], []
    for entry in sorted(Path(directory).iterdir(), key=lambda item: (item.name.lower(), item.name)):
        if not entry.is_file():
            continue
        name = entry.name.lower()
        if Path(name).suffix in NOT_LICENSE_SUFFIXES:
            continue
        if name.startswith(LICENSE_PREFIXES):
            licenses.append(entry)
        elif name.startswith(NOTICE_PREFIXES):
            notices.append(entry)
    return licenses, notices


def read_text(path):
    data = Path(path).read_bytes()
    if b"\0" in data:
        raise NoticesError(f"{path} is not a text file")
    if data.startswith(b"\xef\xbb\xbf"):
        data = data[3:]
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        text = data.decode("latin-1")
    return text.replace("\r\n", "\n").replace("\r", "\n").rstrip()


def fenced(text):
    """Wrap text in a code fence longer than any backtick run it contains."""
    longest = max((len(run) for run in re.findall(r"`+", text)), default=0)
    fence = "`" * max(3, longest + 1)
    return f"{fence}text\n{text}\n{fence}"


def legal_sections(files):
    return [f"#### {path.name}\n\n{fenced(read_text(path))}" for path in files]


def module_section(module, gomodcache):
    directory = module_directory(module, gomodcache)
    licenses, notices = license_files(directory)
    if not licenses:
        raise NoticesError(
            f"linked module {module['path']} {module['version']} has no license file "
            f"(LICENSE*, LICENCE* or COPYING*) in {directory}; it cannot be redistributed without one"
        )
    title = f"### {module['path']} {module['version']}".rstrip()
    if module.get("replace"):
        title += f" (replaced by {module['replace']})"
    return "\n\n".join([title, *legal_sections(licenses + notices)])


def go_toolchain_section(env):
    goroot, goversion = run_go(["env", "GOROOT", "GOVERSION"], env=env).splitlines()[:2]
    files = [Path(goroot) / name for name in GO_TOOLCHAIN_FILES if (Path(goroot) / name).is_file()]
    if not files or files[0].name != "LICENSE":
        raise NoticesError(
            f"the Go toolchain at {goroot} has no LICENSE file; build releases with an "
            "official Go distribution so its license can be reproduced"
        )
    heading = f"### Go standard library and runtime ({goversion.strip()})"
    return "\n\n".join([heading, *legal_sections(files)])


def generate(goos, goarch, package=DEFAULT_PACKAGE, notices=DEFAULT_NOTICES, cwd=REPO):
    """Return the complete notices text for one target platform."""
    notices = Path(notices)
    if not notices.is_file():
        raise NoticesError(f"hand-maintained notices file not found: {notices}")
    env = target_environment(goos, goarch)
    gomodcache = run_go(["env", "GOMODCACHE"], env=env, cwd=cwd).strip()
    modules = linked_modules(package, goos, goarch, cwd=cwd)
    binary = package.rstrip("/").split("/")[-1] or package
    intro = f"## Go modules linked into the `{binary}` binary ({goos}/{goarch})\n\n" + textwrap.fill(
        f"Generated by `scripts/generate-notices.py` from `go list -deps {package}` "
        f"for {goos}/{goarch}. Each entry names a linked module and its version, "
        "followed by the full text of every license and notice file that module "
        "ships. The Go standard library and runtime, linked into every Go binary, "
        "are listed last.",
        width=79, break_long_words=False, break_on_hyphens=False,
    )
    sections = [module_section(module, gomodcache) for module in modules]
    sections.append(go_toolchain_section(env))
    return "\n\n".join([read_text(notices), intro, *sections]) + "\n", modules


def write_atomically(path, text):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    handle, temporary = tempfile.mkstemp(prefix=".notices-", dir=path.parent)
    try:
        with os.fdopen(handle, "w", encoding="utf-8", newline="\n") as stream:
            stream.write(text)
        os.replace(temporary, path)
    except BaseException:
        Path(temporary).unlink(missing_ok=True)
        raise


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--goos", required=True, help="target operating system, as GOOS")
    parser.add_argument("--goarch", required=True, help="target architecture, as GOARCH")
    parser.add_argument("--pkg", default=DEFAULT_PACKAGE, help=f"main package of the binary (default {DEFAULT_PACKAGE})")
    parser.add_argument("--out", required=True, help="notices file to write")
    parser.add_argument("--notices", default=str(DEFAULT_NOTICES), help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    try:
        text, modules = generate(args.goos, args.goarch, args.pkg, args.notices)
        write_atomically(args.out, text)
    except NoticesError as error:
        print(f"generate-notices: error: {error}", file=sys.stderr)
        return 1
    listed = ", ".join(f"{module['path']} {module['version']}" for module in modules) or "none"
    print(f"Wrote {args.out}: {len(modules)} linked Go module(s) for {args.goos}/{args.goarch}: {listed}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
