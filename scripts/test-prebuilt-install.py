"""Release package and installer lifecycle test. No host authentication,
media-provider calls, system package installs or PATH changes.

Always: checks the platform archive and the installer archive against the 2.0
release contract (one `facet` binary, the allowlisted composer sources under
dependencies/remotion-composer, generated third-party notices, the four
installer files, checksums) and
runs the binary when the archive targets this machine.

With FACET_INSTALL_SMOKE=1 it also runs the platform's real installer, taken
from the installer archive, against the native archive. Every installer run
gets a throwaway user profile: HOME, USERPROFILE, APPDATA, LOCALAPPDATA and the
CLI configuration variables point into a temporary directory, for the child
process only. Covered: install, reuse, repair of a modified runtime, update to
a second build and rollback (when Go is available to build it), the
interactive plain menus (Windows), wiring through `facet wire` into a scratch
project, uninstall with and without --purge, a v1 runtime left untouched, and
rejection of bad checksums, unsafe archives and invalid options. Every run
uses --no-path and no optional components. FFmpeg must be on PATH unless
FACET_INSTALL_SKIP_MEDIA=1, which passes --skip-verify.

On Windows, FACET_TEST_POWERSHELL selects the host (default pwsh; CI also runs
powershell.exe, Windows PowerShell 5.1).

With --source, the same checks and lifecycle run against the source archive,
which the installers build facet from (always on Windows, with --from-source
on Linux and macOS). The archive must hold the Go module, its vendored modules,
the Windows resource objects, the composer and every platform's notices, and
nothing else. The installers build with the pinned Go download, taken from
FACET_TEST_TOOLCHAIN or fetched once from go.dev and checked against
installer/manifest.tsv. When the release directory has source-builds.json
(written by package-release.py --source), the installed facet must be byte for
byte the binary the packager built: Go builds are reproducible.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import shutil
import stat
import subprocess
import sys
import tempfile
import urllib.request
import zipfile

REPO = Path(__file__).resolve().parent.parent
RUN_TIMEOUT = 900
INSTALLER_FILES = {"install.ps1", "install.sh", "installer/manifest.tsv", "installer/verify.html"}
COMPOSER_METADATA = ("package.json", "package-lock.json", "tsconfig.json", "composer-manifest.json")
PLATFORMS = [(goos, goarch) for goos in ("windows", "linux", "darwin") for goarch in ("amd64", "arm64")]
ISOLATED_VARIABLES = (
    "FACET_ACTION", "FACET_VERSION", "FACET_COMPONENTS", "FACET_WIRE", "FACET_SCOPE", "FACET_PROJECT",
    "FACET_YES", "FACET_NO_PATH", "FACET_SKIP_VERIFY", "FACET_PURGE", "FACET_PLAIN", "FACET_LOG_DIR",
    "FACET_HOME", "FACET_REMOTION_COMPOSER", "FACET_FROM_SOURCE",
    "CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
)
VALUE_FLAGS = {
    "action": ("-Action", "--action"),
    "version": ("-Version", "--version"),
    "components": ("-Components", "--components"),
    "archive": ("-ArchivePath", "--archive"),
    "checksums": ("-ChecksumPath", "--checksums"),
    "toolchain": ("-ToolchainPath", "--toolchain"),
    "wire": ("-Wire", "--wire"),
    "scope": ("-Scope", "--scope"),
    "project": ("-ProjectDir", "--project"),
}
SWITCH_FLAGS = {
    "yes": ("-NonInteractive", "--yes"),
    "no_path": ("-NoPath", "--no-path"),
    "skip_verify": ("-SkipVerify", "--skip-verify"),
    "purge": ("-Purge", "--purge"),
}


def sha256(path):
    digest = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def host_platform():
    machine = platform.machine().lower()
    arch = {"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(machine, machine)
    return {"win32": "windows", "darwin": "darwin"}.get(sys.platform, "linux"), arch


def composer_names():
    manifest = json.loads((REPO / "remotion-composer" / "composer-manifest.json").read_text(encoding="utf-8"))
    return {f"dependencies/remotion-composer/{name}" for name in (*COMPOSER_METADATA, *manifest["allowedSourcePaths"])}


def notices_heading(os_name, arch):
    return f"## Go modules linked into the `facet` binary ({os_name}/{arch})"


def check_installer(installer, version):
    with zipfile.ZipFile(installer) as z:
        names = {name for name in z.namelist() if not name.endswith("/")}
        assert names == INSTALLER_FILES, f"{installer.name} ships {sorted(names)}"
        rows = [line.split("\t") for line in z.read("installer/manifest.tsv").decode("utf-8").splitlines()]
        assert [row[2] for row in rows if row[:2] == ["release", "facet"]] == [version], "installer manifest is not stamped with the release version"
        assert z.read("install.ps1").isascii(), "install.ps1 must stay ASCII for Windows PowerShell 5.1"
        assert b"\r\n" not in z.read("install.sh"), "install.sh must use LF line endings"


def check_checksums(release, sums, installer, updated_installer):
    for line in (release / sums).read_text(encoding="utf-8").splitlines():
        digest, name = line.split()
        if updated_installer and name == installer.name:
            digest, name = (release / "installer-checksums.txt").read_text(encoding="utf-8").split()
        assert sha256(release / name) == digest, f"checksum mismatch: {name}"


def check_archives(release, version, os_name, arch, updated_installer):
    suffix = ".exe" if os_name == "windows" else ""
    archive = release / f"facet-{version}-{os_name}-{arch}.zip"
    installer = release / f"facet-installer-{version}.zip"
    expected = {"bin/facet" + suffix, "LICENSE", "THIRD_PARTY_NOTICES.md"} | composer_names()
    with zipfile.ZipFile(archive) as z:
        names = {name for name in z.namelist() if not name.endswith("/")}
        assert names == expected, f"{archive.name}: unexpected {sorted(names - expected)}, missing {sorted(expected - names)}"
        assert notices_heading(os_name, arch) in z.read("THIRD_PARTY_NOTICES.md").decode("utf-8"), "notices were not generated for this platform"
        assert z.getinfo("bin/facet" + suffix).external_attr >> 16 & 0o111, "binary is not executable"
    check_installer(installer, version)
    check_checksums(release, f"checksums-{os_name}-{arch}.txt", installer, updated_installer)
    return archive, installer


def check_source_archive(release, version, updated_installer):
    """The source archive holds what a build needs and what a runtime ships
    beside the binary, and nothing else: no binary, no tests, no test data."""
    archive = release / f"facet-{version}-source.zip"
    installer = release / f"facet-installer-{version}.zip"
    notices = {f"notices/{goos}-{goarch}.md" for goos, goarch in PLATFORMS}
    with zipfile.ZipFile(archive) as z:
        names = {name for name in z.namelist() if not name.endswith("/")}
        outside = {name for name in names if not name.startswith("source/")}
        expected = {"LICENSE"} | composer_names() | notices
        assert outside == expected, f"{archive.name}: unexpected {sorted(outside - expected)}, missing {sorted(expected - outside)}"
        for required in ("source/go.mod", "source/go.sum", "source/capability.go", "source/cmd/facet/main.go", "source/vendor/modules.txt",
                         "source/cmd/facet/rsrc_windows_amd64.syso", "source/cmd/facet/rsrc_windows_arm64.syso"):
            assert required in names, f"{archive.name} lacks {required}"
        tests = sorted(name for name in names if name.endswith("_test.go") or "testdata" in PurePosixPath(name).parts)
        assert not tests, f"{archive.name} ships tests: {tests[:5]}"
        executable = sorted(info.filename for info in z.infolist() if info.external_attr >> 16 & 0o111)
        assert not executable, f"{archive.name} ships executables: {executable[:5]}"
        for goos, goarch in PLATFORMS:
            assert notices_heading(goos, goarch) in z.read(f"notices/{goos}-{goarch}.md").decode("utf-8"), f"notices for {goos}/{goarch}"
    check_installer(installer, version)
    check_checksums(release, "checksums-source.txt", installer, updated_installer)
    builds = release / "source-builds.json"
    expected_builds = None
    if builds.exists():
        data = json.loads(builds.read_text(encoding="utf-8"))
        assert data["version"] == version and set(data["builds"]) == {f"{goos}/{goarch}" for goos, goarch in PLATFORMS}, data
        expected_builds = data["builds"]
    return archive, installer, expected_builds


def pinned_toolchain(os_name, arch, temp):
    """The Go download the installers build with, from FACET_TEST_TOOLCHAIN or
    fetched once from go.dev, checked against installer/manifest.tsv."""
    rows = [line.split("\t") for line in (REPO / "installer" / "manifest.tsv").read_text(encoding="utf-8").splitlines()]
    row = next(row for row in rows if row[:2] == ["toolchain", "go"])
    expected = dict(pair.split(":") for pair in row[{"windows": 4, "linux": 5, "darwin": 6}[os_name]].split())[arch]
    name = f"go{row[2]}.{os_name}-{arch}." + ("zip" if os_name == "windows" else "tar.gz")
    path = Path(os.environ["FACET_TEST_TOOLCHAIN"]) if os.environ.get("FACET_TEST_TOOLCHAIN") else temp / name
    if not path.exists():
        with urllib.request.urlopen(row[3] + name, timeout=600) as response, open(path, "wb") as stream:
            shutil.copyfileobj(response, stream)
    assert sha256(path) == expected, f"{path} is not the pinned {name}"
    return path


class Lifecycle:
    def __init__(self, os_name, arch, version, archive, checksums, scripts, root, toolchain=None, builds=None):
        self.os_name, self.arch, self.version = os_name, arch, version
        self.windows = os_name == "windows"
        self.suffix = ".exe" if self.windows else ""
        self.archive, self.checksums, self.scripts, self.root = archive, checksums, scripts, root
        # A source archive is built by the installer, with this Go download;
        # builds holds the packager's SHA-256 of each platform's facet.
        self.source = archive.name.endswith("-source.zip")
        self.toolchain, self.builds = toolchain, builds
        self.skip_media = os.environ.get("FACET_INSTALL_SKIP_MEDIA") == "1"
        self.shell = os.environ.get("FACET_TEST_POWERSHELL", "pwsh")
        self.home = None
        # FACET_HOME for the installer, when a test moves Facet's home folder.
        self.moved_home = None

    def new_home(self, name):
        self.home = self.root / name
        self.moved_home = None
        for directory in (self.home / "AppData" / "Roaming", self.home / "AppData" / "Local", self.home / ".config"):
            directory.mkdir(parents=True)
        return self.home

    @property
    def facet_home(self):
        return self.moved_home or self.home / ".facet"

    def runtime(self, version=None):
        return self.facet_home / "runtimes" / f"{version or self.version}-{self.os_name}-{self.arch}"

    def environment(self):
        env = {key: value for key, value in os.environ.items() if key not in ISOLATED_VARIABLES}
        env.update(HOME=str(self.home), USERPROFILE=str(self.home),
                   APPDATA=str(self.home / "AppData" / "Roaming"), LOCALAPPDATA=str(self.home / "AppData" / "Local"))
        if self.moved_home:
            env.update(FACET_HOME=str(self.moved_home))
        if not self.windows:
            env.update(XDG_CONFIG_HOME=str(self.home / ".config"), XDG_DATA_HOME=str(self.home / ".local" / "share"),
                       XDG_CACHE_HOME=str(self.home / ".cache"))
        return env

    def install(self, expect_success=True, interactive=None, **options):
        options.setdefault("archive", self.archive)
        options.setdefault("checksums", self.checksums)
        if options.get("archive") is None:
            options.pop("archive")
            options.pop("checksums", None)
        if options.get("action") in ("rollback", "uninstall"):
            options.pop("archive", None)
            options.pop("checksums", None)
        else:
            options.setdefault("components", "none")
            options.setdefault("skip_verify", self.skip_media)
            if self.toolchain:
                options.setdefault("toolchain", self.toolchain)
        options.setdefault("no_path", True)
        options.setdefault("yes", interactive is None)
        index = 0 if self.windows else 1
        if self.windows:
            command = [self.shell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(self.scripts / "install.ps1")]
        else:
            command = ["bash", str(self.scripts / "install.sh")]
        for key, value in options.items():
            if key in SWITCH_FLAGS:
                if value:
                    command.append(SWITCH_FLAGS[key][index])
            elif value is not None:
                command += [VALUE_FLAGS[key][index], str(value)]
        result = subprocess.run(command, input=interactive or "", text=True, capture_output=True, env=self.environment(),
                                cwd=self.root, timeout=RUN_TIMEOUT)
        output = result.stdout + result.stderr
        if (result.returncode == 0) != expect_success:
            raise AssertionError(f"installer exited {result.returncode} for {command[-12:]}:\n{output}")
        return output

    def current(self):
        path = self.facet_home / "current"
        if not os.path.lexists(path):
            return None
        info = os.lstat(path)
        if self.windows:
            assert getattr(info, "st_reparse_tag", 0) == stat.IO_REPARSE_TAG_MOUNT_POINT, "~/.facet/current is not a junction"
        else:
            assert stat.S_ISLNK(info.st_mode), "~/.facet/current is not a symbolic link"
        return Path(os.path.realpath(path))

    def assert_active(self, version):
        runtime = self.runtime(version)
        assert self.current() == Path(os.path.realpath(runtime)), f"current is {self.current()}, expected {runtime}"
        facet = self.facet_home / "current" / "bin" / ("facet" + self.suffix)
        reported = subprocess.run([str(facet), "version"], capture_output=True, text=True, timeout=60, env=self.environment())
        assert reported.returncode == 0 and reported.stdout.strip() == f"facet v{version}", reported
        record = json.loads((runtime / "components.json").read_text(encoding="utf-8"))
        assert record["version"] == version and record["components"] == [], record
        # The composer is a media dependency of the runtime, beside the others.
        assert (runtime / "dependencies" / "remotion-composer" / "package.json").is_file(), "composer not under dependencies/"
        for retired in ("bin", "bundle"):
            assert not (self.facet_home / retired).exists(), f"retired ~/.facet/{retired} layout created"
            assert not (runtime / "bundle").exists(), "retired <runtime>/bundle layout created"

    def wired_versions(self):
        registry = self.facet_home / "wiring.json"
        if not registry.exists():
            return {}
        return {(w["cli"], w["scope"]): w["facet_version"] for w in json.loads(registry.read_text(encoding="utf-8"))["wirings"]}

    def assert_no_profile_edits(self):
        if self.windows:
            return
        for profile in (".profile", ".bashrc", ".bash_profile", ".bash_login", ".zshrc", ".zprofile", ".zshenv",
                        ".config/fish/config.fish", ".config/fish/conf.d/facet.fish"):
            assert not (self.home / profile).exists(), f"--no-path wrote {profile}"


def user_path():
    """The Windows user PATH, read only, to prove -NoPath leaves it alone."""
    import winreg
    try:
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, "Environment") as key:
            return winreg.QueryValueEx(key, "Path")[0]
    except FileNotFoundError:
        return None


def build_variant(life, version, out):
    """A second release of this platform, differing in its version. The
    installer stamps the version when it builds from source, so the same
    source archive serves under the other version's name; a prebuilt variant
    is rebuilt here with Go."""
    if life.source:
        variant = out / f"facet-{version}-source.zip"
        shutil.copyfile(life.archive, variant)
    else:
        if not shutil.which("go"):
            return None
        binary = out / ("facet" + life.suffix)
        subprocess.run(["go", "build", "-trimpath", "-ldflags", f"-X main.Version={version}", "-o", str(binary), "./cmd/facet"],
                       cwd=REPO, env=dict(os.environ, GOOS=life.os_name, GOARCH=life.arch, CGO_ENABLED="0"), check=True, timeout=RUN_TIMEOUT)
        variant = out / f"facet-{version}-{life.os_name}-{life.arch}.zip"
        with zipfile.ZipFile(life.archive) as source, zipfile.ZipFile(variant, "w") as target:
            for info in source.infolist():
                target.writestr(info, binary.read_bytes() if info.filename == "bin/facet" + life.suffix else source.read(info))
    sums = out / f"checksums-{version}.txt"
    sums.write_text(f"{sha256(variant)}  {variant.name}\n", encoding="utf-8")
    return variant, sums


def lifecycle(life, temp):
    path_before = user_path() if life.windows else None
    home = life.new_home("home")
    v1 = home / ".facet" / "releases" / f"1.1.0-{life.os_name}-{life.arch}"
    v1.mkdir(parents=True)
    (v1 / "marker.txt").write_text("v1 runtime", encoding="utf-8")

    output = life.install()
    life.assert_active(life.version)
    if life.source:
        assert "Build Facet" in output and "Remove Go and the build cache" in output, output
        if life.builds:
            built = sha256(life.runtime() / "bin" / ("facet" + life.suffix))
            expected = life.builds[f"{life.os_name}/{life.arch}"]
            assert built == expected, f"the installer built facet {built}; the packager built {expected} from the same source"
            print(f"Reproducible: the installed facet is the packager's build ({expected}).")
    assert "v1 project integrations are separate" in output and "--action uninstall" in output, output
    life.assert_no_profile_edits()
    doctor = subprocess.run([str(life.facet_home / "current" / "bin" / ("facet" + life.suffix)), "doctor"], cwd=temp,
                            capture_output=True, text=True, timeout=120, env=life.environment())
    assert doctor.returncode == 0, doctor

    output = life.install()
    assert "Reusing installed runtime" in output or "reusing" in output.lower(), output
    assert sorted(p.name for p in (life.facet_home / "runtimes").iterdir() if not p.name.startswith(".")) == [life.runtime().name]

    if life.windows:
        # Plain menus with numbered answers: 1 selects install in the action
        # menu (the active runtime exists), then no components, confirmation,
        # and no wiring when CLIs are detected.
        output = life.install(interactive="1\nnone\ny\nnone\n", components=None)
        assert "What should the installer do?" in output and "Action:      install" in output, output
        life.assert_active(life.version)

    binary = life.runtime() / "bin" / ("facet" + life.suffix)
    original = sha256(binary)
    with open(binary, "ab") as stream:
        stream.write(b"modified")
    life.install()
    assert sha256(binary) == original, "a modified runtime was not repaired"
    life.assert_active(life.version)

    variant = build_variant(life, f"{life.version}-rollback.1", temp)
    if variant:
        other = f"{life.version}-rollback.1"
        life.install(action="update", version=other, archive=variant[0], checksums=variant[1])
        life.assert_active(other)
        assert life.runtime().is_dir(), "update removed the previous runtime"
        life.install(action="rollback")
        life.assert_active(life.version)
        life.install(action="rollback")
        life.assert_active(other)
        life.install(action="rollback", version=life.version)
        life.assert_active(life.version)
    else:
        print("Go is unavailable: update and rollback between two builds were not exercised.")
    life.install(action="rollback", version="9.9.9", expect_success=False)

    project = temp / "wired project"
    project.mkdir()
    output = life.install(wire="opencode", scope="project", project=project)
    skills = list((project / ".opencode").rglob("SKILL.md"))
    assert skills, f"facet wire installed no skills:\n{output}"
    assert '"facet"' in (project / "opencode.json").read_text(encoding="utf-8"), output
    registry = json.loads((life.facet_home / "wiring.json").read_text(encoding="utf-8"))
    assert any(w["cli"] == "opencode" and w["scope"] == "project" for w in registry["wirings"]), registry

    if variant:
        # Every install, update and rollback refreshes the recorded wirings,
        # so a CLI's Facet guidance always matches the active runtime.
        life.install(action="update", version=other, archive=variant[0], checksums=variant[1])
        life.assert_active(other)
        assert life.wired_versions() == {("opencode", "project"): other}, life.wired_versions()
        life.install(action="rollback")
        life.assert_active(life.version)
        assert life.wired_versions() == {("opencode", "project"): life.version}, life.wired_versions()
        # A third build: only the active runtime and the one before it are kept.
        third = build_variant(life, f"{life.version}-prune.1", temp)
        newest = f"{life.version}-prune.1"
        life.install(action="update", version=newest, archive=third[0], checksums=third[1])
        kept = sorted(p.name for p in (life.facet_home / "runtimes").iterdir() if not p.name.startswith("."))
        assert kept == sorted([life.runtime().name, life.runtime(newest).name]), f"runtimes kept: {kept}"
        life.install(action="rollback")
        life.assert_active(life.version)

    output = life.install(action="uninstall")
    assert not os.path.lexists(life.facet_home / "current"), "uninstall left ~/.facet/current"
    assert life.runtime().is_dir(), "uninstall without --purge removed the runtime"
    assert not list((project / ".opencode").rglob("SKILL.md")), f"uninstall left wired skills:\n{output}"
    config = project / "opencode.json"
    assert not config.exists() or '"facet"' not in config.read_text(encoding="utf-8"), output
    assert "v1 project integrations are separate" in output, output

    life.install()
    life.assert_active(life.version)
    life.install(action="uninstall", purge=True)
    assert not (life.facet_home / "runtimes").exists(), "--purge kept the runtimes"
    assert not os.path.lexists(life.facet_home / "current")
    assert (v1 / "marker.txt").read_text(encoding="utf-8") == "v1 runtime", "the v1 runtime was touched"
    life.assert_no_profile_edits()
    if life.windows:
        assert user_path() == path_before, "-NoPath changed the user PATH"


def moved_home(life, temp):
    """FACET_HOME moves the whole installation: nothing lands in ~/.facet."""
    home = life.new_home("moved")
    life.moved_home = temp / "moved facet home"
    life.install()
    life.assert_active(life.version)
    assert not (home / ".facet").exists(), "the installer wrote ~/.facet although FACET_HOME is set"
    life.install(action="uninstall", purge=True)
    assert not (life.facet_home / "runtimes").exists() and not os.path.lexists(life.facet_home / "current")


def rejections(life, temp):
    life.new_home("rejections")
    runtimes = life.facet_home / "runtimes"
    bad_sums = temp / "bad-sums.txt"
    bad_sums.write_text("0" * 64 + "  " + life.archive.name + "\n", encoding="utf-8")
    life.install(checksums=bad_sums, expect_success=False)
    assert not life.runtime().exists() and not os.path.lexists(life.facet_home / "current")
    for entry in ["../escaped.txt", "/absolute.txt", "C:/escape.txt", "symlink", "duplicate"]:
        unsafe = temp / "unsafe.zip"
        with zipfile.ZipFile(unsafe, "w") as z:
            if entry == "symlink":
                info = zipfile.ZipInfo("bin/link")
                info.create_system = 3
                info.external_attr = 0o120777 << 16
                z.writestr(info, "../../escaped.txt")
            elif entry == "duplicate":
                z.writestr("a.txt", "first")
                z.writestr("A.txt", "second")
            else:
                z.writestr(entry, "must not escape")
        sums = temp / "unsafe-sums.txt"
        sums.write_text(f"{sha256(unsafe)}  {life.archive.name}\n", encoding="utf-8")
        life.install(archive=unsafe, checksums=sums, expect_success=False)
        assert not life.runtime().exists() and not os.path.lexists(life.facet_home / "current"), entry
        leftovers = [p.name for p in runtimes.iterdir()] if runtimes.exists() else []
        assert not leftovers, f"{entry}: left {leftovers}"
        assert not (runtimes.parent / "escaped.txt").exists() and not (temp / "escaped.txt").exists(), entry
    life.install(checksums=None, expect_success=False)
    life.install(components="bogus", expect_success=False)
    life.install(action="rollback", expect_success=False)
    assert not os.path.lexists(life.facet_home / "current")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--os", required=True)
    parser.add_argument("--arch", required=True)
    parser.add_argument("--release-dir", required=True)
    parser.add_argument("--source", action="store_true", help="test the source archive and installs that build facet from it")
    parser.add_argument("--updated-installer", action="store_true", help="use the installer-only build checksum while keeping the product archive checksum")
    args = parser.parse_args()
    version = json.loads((REPO / "package.json").read_text(encoding="utf-8"))["version"]
    release = Path(args.release_dir).resolve()
    builds = None
    if args.source:
        archive, installer, builds = check_source_archive(release, version, args.updated_installer)
        checksums = release / "checksums-source.txt"
    else:
        archive, installer = check_archives(release, version, args.os, args.arch, args.updated_installer)
        checksums = release / f"checksums-{args.os}-{args.arch}.txt"
    native = host_platform() == (args.os, args.arch)
    with tempfile.TemporaryDirectory(prefix="facet package test ") as temp:
        temp = Path(temp)
        suffix = ".exe" if args.os == "windows" else ""
        if native and not args.source:
            with zipfile.ZipFile(archive) as z:
                z.extract("bin/facet" + suffix, temp / "extracted")
            binary = temp / "extracted" / "bin" / ("facet" + suffix)
            binary.chmod(0o755)
            reported = subprocess.check_output([str(binary), "version"], text=True, timeout=60).strip()
            assert reported == "facet v" + version, reported
        if os.environ.get("FACET_INSTALL_SMOKE") == "1":
            if not native:
                raise SystemExit(f"FACET_INSTALL_SMOKE needs a {args.os}/{args.arch} host")
            if os.environ.get("FACET_INSTALL_SKIP_MEDIA") != "1" and not (shutil.which("ffmpeg") and shutil.which("ffprobe")):
                raise SystemExit("FFmpeg and FFprobe must be on PATH for the lifecycle test (or set FACET_INSTALL_SKIP_MEDIA=1)")
            with zipfile.ZipFile(installer) as z:
                z.extractall(temp / "installer")
            toolchain = pinned_toolchain(args.os, args.arch, temp) if args.source else None
            life = Lifecycle(args.os, args.arch, version, archive, checksums, temp / "installer", temp, toolchain, builds)
            lifecycle(life, temp)
            moved_home(life, temp)
            rejections(life, temp)
            print("Installer lifecycle passed: install, reuse, repair, update/rollback, wiring refresh, pruning, "
                  f"FACET_HOME, uninstall, purge and rejections{' (built from source)' if args.source else ''}.")
    if args.source:
        print("Source archive, script installer package, composer contents and checksums passed.")
    else:
        print("Native binary, script installer package, composer contents and checksums passed.")


if __name__ == "__main__":
    main()
