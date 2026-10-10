"""The release contract: what a Facet release holds, and how its manifest reads.

A release has two products for each target: core (the facet command) and app
(the Facet App with Compa's kernel). Both installers read manifest.tsv to know
every archive and the SHA-256 of every file in it.
"""
import hashlib
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile
import urllib.request
import zipfile


TARGETS = ("windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64")
PRODUCTS = ("core", "app")
INSTALLERS = ("install.ps1", "install.sh")
MANIFEST_FORMAT = "facet-release-v1"
HASH = re.compile(r"[0-9a-f]{64}")
COMMIT = re.compile(r"[0-9a-f]{40}(?:[0-9a-f]{24})?")

# The App ships compa-kernel unchanged from Compa's own release, checked
# against these digests: each archive's, as Compa's SHA256SUMS lists it, and
# the kernel's inside it.
COMPA_VERSION = "0.0.1"
COMPA_RELEASE = f"https://github.com/kayiik/compa/releases/download/v{COMPA_VERSION}/"
COMPA_ARCHIVES = {
    "windows/amd64": ("compa_0.0.1_windows_amd64.zip", "6f03a6a9a600e3a2203626934130483b7b3b4e03385480cdb36051ef8c13a9fe",
                      "2cce714fcb41407dc8fdfdbb4cd213ad096b082b13916c1f85a2f581cd897a03"),
    "windows/arm64": ("compa_0.0.1_windows_arm64.zip", "855f598c7f7c8aacb81b6f265883479b0426056214e5adffde330daaa79cf04e",
                      "c30c73a6961b9981f32f855d07196aeb7a5e36c1b30f9ba07f6ebec34ca40ad3"),
    "linux/amd64": ("compa_0.0.1_linux_amd64.tar.gz", "a47de233e1f75f92b6ae8080727a839ad91d19ae3dea5bdafcd618bdf0a22619",
                    "a3df6064859409fb21f5bd1abc70241fce8cd739372a3bfc8ec1cbd2e07ad394"),
    "linux/arm64": ("compa_0.0.1_linux_arm64.tar.gz", "dfbeca13c25f39eec84deb54c1af8623d77d7ba82e811b2a43aa2195ffc8ffa8",
                    "e17555f48e87d6a89856f767483e7a287eaa66a8317da7e83f63b3a6f21d0c2c"),
    "darwin/amd64": ("compa_0.0.1_darwin_amd64.tar.gz", "01230fde08b74abaed83373d009746eed66ec10d008dee483e478b49c98ef8c6",
                     "cefddaa9824acc17df77f6c7ec3f8429c80d09165e89156fd8c26709c905e93f"),
    "darwin/arm64": ("compa_0.0.1_darwin_arm64.tar.gz", "46f14928df11e32b07c50c962c31ad0a74495c0d49a306296bff365ba16cc143",
                     "efe6e5a58c643202276a9b400940d7bc6c5828b1d74879aca5f936e6fc8bfe4a"),
}
COMPA_TEXTS = (("LICENSE", "kernel/LICENSE", "4373e3ecad19c9f494f5faee7e02c4595e64344714e40eb21e34dbfba231a02f"),
               ("NOTICE", "kernel/NOTICE", "65d2feac59096d8a88c46bc3e5417c42a3ff1664578926994ca4543ed09ca56f"))
COMPA_NOTICE = (
    "\n## Compa's kernel\n\n"
    "Shipped unchanged from Compa's own release, with its LICENSE and NOTICE in kernel/:\n"
    f"Compa {COMPA_VERSION} \u2014 https://github.com/kayiik/compa. kernel/compa-kernel records these modules:\n\n"
)


def exe(target):
    return ".exe" if target.startswith("windows/") else ""


def core_members(target):
    """The files a core archive holds, named as they are installed."""
    return (f"bin/facet{exe(target)}", "LICENSE", "THIRD_PARTY_NOTICES.md")


def app_members(target):
    """The files an app archive holds, named as they are installed."""
    return (f"bin/facet-app{exe(target)}", f"kernel/compa-kernel{exe(target)}", "kernel/LICENSE", "kernel/NOTICE",
            "app/LICENSE", "app/THIRD_PARTY_NOTICES.md")


def compa_members(target):
    """(member, destination, SHA-256) of each file the app takes from Compa's release for target."""
    kernel = (f"compa-kernel{exe(target)}", f"kernel/compa-kernel{exe(target)}", COMPA_ARCHIVES[target][2])
    return (kernel, *COMPA_TEXTS)


def compa_archive(target, downloads):
    """Compa's release archive for target, fetched once into downloads and checked against its pin."""
    asset, expected, _ = COMPA_ARCHIVES[target]
    path = Path(downloads) / asset
    if not path.is_file():
        partial = Path(downloads) / (asset + ".part")
        with urllib.request.urlopen(COMPA_RELEASE + asset, timeout=120) as response, partial.open("wb") as out:
            for block in iter(lambda: response.read(1024 * 1024), b""):
                out.write(block)
        partial.replace(path)
    if sha256_file(path) != expected:
        raise RuntimeError(f"{asset} differs from the pinned Compa release")
    return path


def take_compa(target, downloads, destination):
    """[(path, archive name)] of the files the app takes from Compa's release for target, each checked against its pin."""
    archive_path = compa_archive(target, downloads)
    destination = Path(destination)
    destination.mkdir(parents=True, exist_ok=True)
    taken = []
    with (zipfile.ZipFile(archive_path) if archive_path.suffix == ".zip" else tarfile.open(archive_path)) as archive:
        for member, name, expected in compa_members(target):
            if isinstance(archive, zipfile.ZipFile):
                data = archive.read(member)
            else:
                with archive.extractfile(member) as stream:
                    data = stream.read()
            if hashlib.sha256(data).hexdigest() != expected:
                raise RuntimeError(f"{member} in {archive_path.name} differs from its pin")
            path = destination / name.rsplit("/", 1)[-1]
            path.write_bytes(data)
            path.chmod(0o755 if name.startswith("kernel/compa-kernel") else 0o644)
            taken.append((path, name))
    return taken


def sha256_file(path):
    result = hashlib.sha256()
    with Path(path).open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def release_version(value):
    if not isinstance(value, str) or len(value) > 64 or not re.fullmatch(
        r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?", value
    ):
        raise ValueError("Version must be a plain semantic version without a v prefix")
    if "-" in value:
        for identifier in value.split("-", 1)[1].split("."):
            if not identifier or (identifier.isdigit() and len(identifier) > 1 and identifier.startswith("0")):
                raise ValueError("Invalid prerelease identifier")
    return value


def safe_member(name):
    if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z0-9._/-]+", name):
        raise ValueError("Archive paths must use portable ASCII names")
    path = PurePosixPath(name)
    if path.is_absolute() or str(path) != name or not path.parts:
        raise ValueError("Archive path is not canonical and relative")
    reserved = {"con", "prn", "aux", "nul"} | {f"{prefix}{i}" for prefix in ("com", "lpt") for i in range(1, 10)}
    for part in path.parts:
        if part in (".", "..") or part.endswith(".") or part.split(".", 1)[0].casefold() in reserved:
            raise ValueError("Unsafe portable archive path")
    return name


def archive_name(product, version, target):
    release_version(version)
    if product not in PRODUCTS or target not in TARGETS:
        raise ValueError("Unsupported release product or target")
    suffix = ".zip" if target.startswith("windows/") else ".tar.gz"
    return f"facet-{product}_{version}_{target.replace('/', '_')}{suffix}"


def validate_member_set(names):
    names = list(names)
    folded = {safe_member(name).casefold() for name in names}
    if len(folded) != len(names):
        raise ValueError("Duplicate or case-colliding archive members")
    for name in names:
        for parent in PurePosixPath(name).parents:
            if str(parent) != "." and str(parent).casefold() in folded:
                raise ValueError("An archive file conflicts with a parent directory")


def release_tsv(version, commit, entries):
    release_version(version)
    if not COMMIT.fullmatch(commit):
        raise ValueError("Invalid release source commit")
    lines = [f"format\t{MANIFEST_FORMAT}", f"version\t{version}", f"commit\t{commit}"]
    for product, target, archive, files in entries:
        lines.append("\t".join(("archive", product, target, archive)))
        for name, digest in sorted(files.items()):
            lines.append("\t".join(("file", product, target, name, digest)))
    text = "\n".join(lines) + "\n"
    parse_release_tsv(text)
    return text


def parse_release_tsv(text):
    result = {"archives": {}, "files": {}}
    seen_archives = set()
    folded_files = {}
    for line in text.splitlines():
        parts = line.split("\t")
        if len(parts) == 2 and parts[0] in ("format", "version", "commit"):
            key, value = parts
            if key in result:
                raise ValueError("Duplicate manifest metadata")
            result[key] = value
        elif len(parts) == 4 and parts[0] == "archive":
            _, product, target, name = parts
            key = (product, target)
            if key in result["archives"] or name.casefold() in seen_archives:
                raise ValueError("Duplicate release archive")
            safe_member(name)
            if PurePosixPath(name).name != name:
                raise ValueError("Release archive must be a filename")
            result["archives"][key] = name
            result["files"][key] = {}
            folded_files[key] = set()
            seen_archives.add(name.casefold())
        elif len(parts) == 5 and parts[0] == "file":
            _, product, target, name, digest = parts
            key = (product, target)
            safe_member(name)
            if key not in result["archives"] or name.casefold() in folded_files[key] or not HASH.fullmatch(digest):
                raise ValueError("Unknown archive, duplicate member or invalid digest")
            result["files"][key][name] = digest
            folded_files[key].add(name.casefold())
        else:
            raise ValueError("Invalid release manifest record")
    if result.get("format") != MANIFEST_FORMAT or not COMMIT.fullmatch(result.get("commit", "")):
        raise ValueError("Invalid manifest format or source commit")
    release_version(result.get("version"))
    if not result["archives"]:
        raise ValueError("No release archives")
    installed = {}
    for key, name in result["archives"].items():
        if name != archive_name(key[0], result["version"], key[1]) or not result["files"][key]:
            raise ValueError("Archive identity or file inventory is invalid")
        expected = core_members(key[1]) if key[0] == "core" else app_members(key[1])
        if sorted(result["files"][key]) != sorted(expected):
            raise ValueError(f"{name} does not hold exactly the files of its product")
        validate_member_set(result["files"][key])
        # Both products install into one folder, so no file may be in both.
        for member in result["files"][key]:
            owner = installed.setdefault((key[1], member.casefold()), key[0])
            if owner != key[0]:
                raise ValueError(f"{member} is in more than one product for {key[1]}")
    return result


def git_blob(root, revision, source):
    root = Path(root).absolute()
    relative = Path(source).absolute().relative_to(root).as_posix()
    safe_member(relative)
    entry = subprocess.check_output(["git", "-C", str(root), "ls-tree", revision, "--", relative]).rstrip(b"\n")
    if b"\t" not in entry:
        raise ValueError("Source file is absent from the claimed commit: " + relative)
    header, recorded = entry.split(b"\t", 1)
    mode, kind, oid = header.decode("ascii").split()
    if recorded.decode("utf-8") != relative or kind != "blob" or mode not in ("100644", "100755"):
        raise ValueError("Release source must be a regular tracked blob: " + relative)
    return subprocess.check_output(["git", "-C", str(root), "cat-file", "blob", oid]), mode == "100755"


def git_blob_bytes(root, revision, source):
    return git_blob(root, revision, source)[0]


def binary_modules(binary, env):
    """(module path, version) for each module a Go binary records in its build information."""
    env = {key: value for key, value in env.items() if key != "GOFLAGS"}
    info = subprocess.check_output(["go", "version", "-m", str(binary)], env=env, text=True)
    deps = []
    for line in info.splitlines()[1:]:
        fields = line.strip().split("\t")
        if fields[0] == "dep" and len(fields) >= 3:
            deps.append((fields[1], fields[2]))
        elif fields[0] == "=>" and len(fields) >= 3 and deps:
            deps[-1] = (fields[1], fields[2])
    if not deps:
        raise RuntimeError(f"{Path(binary).name} records no compiled modules")
    return sorted(set(deps))
