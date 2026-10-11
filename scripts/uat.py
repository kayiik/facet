"""Run the end-to-end test of an installed facet in a clean Ubuntu container.

    python scripts/uat.py --release DIR [--evidence DIR] [--keep-image]

Builds an image from scripts/uat.Dockerfile, in which the release's own
install.sh installs facet for one person in bundle mode, wired into OpenCode,
with the default components; the image build downloads them. Then runs the
journey, scripts/uat-journey.mjs, in a container with no network, copies its
evidence out (into an empty folder, or a new temporary one), and exits
non-zero unless every check passed.

Needs Docker, and a release with a linux build for the Docker engine's
architecture: python scripts/build-release.py --out DIR --targets linux/amd64.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import uuid

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parent.parent
# What the image needs of the journey.
JOURNEY = ("uat-journey.mjs", "uat-media.mjs", "uat-sanitize.mjs", "uat-wiring.mjs", "probe-artifact.mjs", "probe-artifact.sh")
EVIDENCE = "/home/facet/uat-evidence"


def docker(*args, check=True, capture=False):
    return subprocess.run(["docker", *args], check=check, text=True, capture_output=capture)


def verify_release(directory):
    spec = importlib.util.spec_from_file_location("verify_release", ROOT / "scripts" / "verify-release.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.verify(directory)


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--release", type=Path, required=True, help="a release folder that build-release.py wrote")
    parser.add_argument("--evidence", type=Path, help="an empty folder for the journey's evidence")
    parser.add_argument("--keep-image", action="store_true", help="keep the image after the run")
    args = parser.parse_args()
    release = args.release.resolve()
    manifest = verify_release(release)
    arch = docker("version", "--format", "{{.Server.Arch}}", capture=True).stdout.strip()
    target = "linux/" + {"x86_64": "amd64", "aarch64": "arm64"}.get(arch, arch)
    archive = manifest["archives"].get(("core", target))
    if not archive:
        raise SystemExit(f"uat: the release has no core build for {target}; "
                         f"build one with scripts/build-release.py --targets {target}")
    evidence = args.evidence.resolve() if args.evidence else Path(tempfile.mkdtemp(prefix="facet-uat-evidence-"))
    evidence.mkdir(parents=True, exist_ok=True)
    if any(evidence.iterdir()):
        raise SystemExit(f"uat: {evidence} is not empty")
    run = uuid.uuid4().hex[:12]
    image, container = f"facet-uat:{run}", f"facet-uat-{run}"
    with tempfile.TemporaryDirectory(prefix="facet-uat-context-") as temporary:
        context = Path(temporary)
        shutil.copyfile(ROOT / "scripts" / "uat.Dockerfile", context / "Dockerfile")
        # Only what install.sh reads for this target; it fetches nothing else.
        (context / "release").mkdir()
        for name in ("install.sh", "SHA256SUMS", "build-manifest.json", "manifest.tsv", archive):
            shutil.copyfile(release / name, context / "release" / name)
        (context / "uat").mkdir()
        for name in JOURNEY:
            shutil.copyfile(ROOT / "scripts" / name, context / "uat" / name)
        docker("build", "--pull", "--progress=plain", "--tag", image, str(context))
    try:
        # No network: the image holds everything. The browser wants more
        # shared memory than Docker's default, and an init reaps its children.
        journey = docker("run", "--name", container, "--network", "none", "--init", "--shm-size", "1g", image, check=False)
        copied = docker("cp", f"{container}:{EVIDENCE}/.", str(evidence), check=False)
    finally:
        docker("rm", "--force", container, check=False, capture=True)
        if not args.keep_image:
            docker("image", "rm", "--force", image, check=False, capture=True)
    try:
        checks = json.loads((evidence / "checks.json").read_text(encoding="utf-8"))
    except (OSError, ValueError):
        raise SystemExit(f"uat: the journey left no checks.json (docker run exited {journey.returncode}, "
                         f"docker cp exited {copied.returncode})")
    for check in checks["checks"]:
        print(("PASS " if check["passed"] else "FAIL ") + check["name"] + ("" if check["passed"] else ": " + check.get("error", "")))
    print(f"Facet {manifest['version']} on {target}: {'passed' if checks['passed'] else 'FAILED'}; evidence in {evidence}")
    if journey.returncode != 0 or not checks["passed"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
