"""Print the release after package.json's: the same version with its patch
number one higher, for the installer tests' upgrade.

    python scripts/next-version.py
"""
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from release_contract import release_version  # noqa: E402


def next_version(version):
    major, minor, patch = release_version(version).split("-", 1)[0].split(".")
    return f"{major}.{minor}.{int(patch) + 1}"


if __name__ == "__main__":
    package = json.loads((Path(__file__).resolve().parent.parent / "package.json").read_text(encoding="utf-8"))
    print(next_version(package["version"]))
