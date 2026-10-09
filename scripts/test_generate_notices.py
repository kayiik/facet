"""Tests for scripts/generate-notices.py.

Run from the repository root with any of:
  python -m unittest scripts/test_generate_notices.py
  python -m unittest discover -s scripts -p test_generate_notices.py
  python scripts/test_generate_notices.py
(The discover form also works where an installed package named `scripts`
shadows this directory.)

The integration tests generate notices for the current platform and compare
them with an independent `go list` enumeration of the linked modules.
"""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parent
REPO = SCRIPTS.parent
GENERATOR = SCRIPTS / "generate-notices.py"
sys.dont_write_bytecode = True  # Keep scripts/ free of __pycache__ for the generator.


def load_generator():
    spec = importlib.util.spec_from_file_location("generate_notices", GENERATOR)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


notices = load_generator()


def go(*arguments, env=None):
    result = subprocess.run(["go", *arguments], cwd=REPO, env=env, capture_output=True, text=True, timeout=600)
    if result.returncode != 0:
        raise AssertionError(f"go {' '.join(arguments)} failed:\n{result.stderr}")
    return result.stdout


def normalized(path):
    data = Path(path).read_bytes().removeprefix(b"\xef\xbb\xbf").decode("utf-8", errors="replace")
    return data.replace("\r\n", "\n").replace("\r", "\n").rstrip()


@unittest.skipUnless(shutil.which("go"), "the go toolchain is required")
class CurrentPlatformNoticesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.goos, cls.goarch = go("env", "GOOS", "GOARCH").split()
        cls.temp = tempfile.TemporaryDirectory(prefix="facet-notices-test-")
        cls.out = Path(cls.temp.name) / "notices" / "THIRD_PARTY_NOTICES.md"
        cls.run_generator(cls.out)
        cls.text = cls.out.read_text(encoding="utf-8")
        # License texts may contain Markdown headings of their own; only the
        # structure outside the generated code fences describes the file.
        cls.outline = re.sub(r"^(`{3,})text\n.*?\n\1$", "", cls.text, flags=re.MULTILINE | re.DOTALL)
        # Independent enumeration of exactly what the release binary links.
        env = dict(os.environ, GOOS=cls.goos, GOARCH=cls.goarch, CGO_ENABLED="0")
        listing = go("list", "-deps", "-f", "{{if not .Standard}}{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}{{end}}", "./cmd/facet", env=env)
        cls.linked = sorted({line.strip() for line in listing.splitlines() if line.strip()})

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    @classmethod
    def run_generator(cls, out):
        result = subprocess.run(
            [sys.executable, str(GENERATOR), "--goos", cls.goos, "--goarch", cls.goarch, "--out", str(out)],
            cwd=REPO, capture_output=True, text=True, timeout=600,
        )
        if result.returncode != 0:
            raise AssertionError(f"generate-notices failed:\n{result.stdout}\n{result.stderr}")
        return result

    def headings(self):
        return re.findall(r"^### (.+)$", self.outline, re.MULTILINE)

    def section(self, heading):
        starts = sorted(self.text.index(f"\n### {name}\n") for name in self.headings())
        start = self.text.index(f"\n### {heading}\n")
        later = [index for index in starts if index > start]
        return self.text[start:later[0] if later else len(self.text)]

    def test_every_linked_module_appears_exactly_once(self):
        self.assertTrue(self.linked, "the facet binary is expected to link third-party Go modules")
        for module in self.linked:
            self.assertEqual(self.outline.count(f"\n### {module}\n"), 1, module)
        modules = sorted(" ".join(heading.split()[:2]) for heading in self.headings() if not heading.startswith("Go standard library"))
        self.assertEqual(modules, self.linked, "notices must list exactly the linked modules")

    def test_each_module_license_and_notice_text_is_reproduced(self):
        for module in self.linked:
            path, version = module.split()
            info = json.loads(go("mod", "download", "-json", f"{path}@{version}"))
            files = [entry for entry in Path(info["Dir"]).iterdir() if entry.is_file() and re.match(r"(?i)(licen[cs]e|copying|notice)", entry.name) and entry.suffix.lower() != ".go"]
            self.assertTrue(any(re.match(r"(?i)(licen[cs]e|copying)", entry.name) for entry in files), f"{module} ships no license file")
            section = self.section(module)
            for entry in files:
                self.assertIn(f"#### {entry.name}\n", section, f"{module}: {entry.name}")
                self.assertIn(normalized(entry), section, f"{module}: {entry.name} text")

    def test_repository_notices_come_first_unchanged(self):
        hand_maintained = normalized(REPO / "THIRD_PARTY_NOTICES.md")
        self.assertTrue(self.text.startswith(hand_maintained + "\n\n## Go modules linked into the `facet` binary"))

    def test_go_toolchain_license_is_included(self):
        goroot, goversion = go("env", "GOROOT", "GOVERSION").split()
        self.assertIn(f"\n### Go standard library and runtime ({goversion})\n", self.text)
        self.assertIn(normalized(Path(goroot) / "LICENSE"), self.text)

    def test_output_is_deterministic_and_lf_only(self):
        again = self.out.with_name("again.md")
        self.run_generator(again)
        self.assertEqual(again.read_bytes(), self.out.read_bytes())
        self.assertNotIn(b"\r", self.out.read_bytes())
        self.assertTrue(self.text.endswith("\n") and not self.text.endswith("\n\n"))


class LicenseDiscoveryTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="facet-notices-unit-")
        self.root = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def module(self, name, files):
        directory = self.root / name
        directory.mkdir()
        for file_name, content in files.items():
            (directory / file_name).write_bytes(content)
        return {"path": f"example.com/{name}", "version": "v1.0.0", "dir": str(directory), "replace": ""}

    def test_module_without_license_fails_clearly(self):
        module = self.module("unlicensed", {"README.md": b"no terms", "NOTICE": b"attribution only", "license.go": b"package license"})
        with self.assertRaises(notices.NoticesError) as raised:
            notices.module_section(module, str(self.root))
        self.assertIn("example.com/unlicensed v1.0.0 has no license file", str(raised.exception))

    def test_license_spellings_copying_and_notices_are_collected(self):
        module = self.module("varied", {
            "LICENCE.md": b"British spelling", "COPYING": b"GPL text", "LICENSE-APACHE": b"Apache text",
            "NOTICE.txt": b"Notice text", "license.go": b"package license", "README": b"ignored",
        })
        licenses, extra = notices.license_files(module["dir"])
        self.assertEqual([path.name for path in licenses], ["COPYING", "LICENCE.md", "LICENSE-APACHE"])
        self.assertEqual([path.name for path in extra], ["NOTICE.txt"])
        section = notices.module_section(module, str(self.root))
        self.assertTrue(section.startswith("### example.com/varied v1.0.0\n"))
        self.assertLess(section.index("#### LICENSE-APACHE"), section.index("#### NOTICE.txt"))
        self.assertNotIn("package license", section)

    def test_text_is_normalized_and_fenced_safely(self):
        path = self.root / "LICENSE"
        path.write_bytes(b"\xef\xbb\xbfLine one\r\nuses ```` fences\rend\r\n\r\n")
        self.assertEqual(notices.read_text(path), "Line one\nuses ```` fences\nend")
        self.assertEqual(notices.fenced("uses ```` fences"), "`````text\nuses ```` fences\n`````")
        path.write_bytes(b"binary\0data")
        with self.assertRaises(notices.NoticesError):
            notices.read_text(path)

    def test_module_cache_path_escaping_and_target_validation(self):
        self.assertEqual(notices.escape_module_path("github.com/BurntSushi/toml"), "github.com/!burnt!sushi/toml")
        for goos, goarch in [("linux;", "amd64"), ("linux", "AMD64"), ("", "amd64")]:
            with self.assertRaises(notices.NoticesError):
                notices.target_environment(goos, goarch)

    def test_json_stream_decoding(self):
        self.assertEqual(notices.parse_json_stream('{"a": 1}\n{"b": {"c": 2}}\n'), [{"a": 1}, {"b": {"c": 2}}])
        self.assertEqual(notices.parse_json_stream("  \n"), [])

    def test_cli_reports_errors_without_writing_output(self):
        out = self.root / "NOTICES.md"
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            self.assertEqual(notices.main(["--goos", "linux;", "--goarch", "amd64", "--out", str(out)]), 1)
            self.assertEqual(notices.main(["--goos", "linux", "--goarch", "amd64", "--out", str(out), "--notices", str(self.root / "missing.md")]), 1)
        self.assertIn("generate-notices: error: invalid --goos value", stderr.getvalue())
        self.assertIn("hand-maintained notices file not found", stderr.getvalue())
        self.assertFalse(out.exists())


if __name__ == "__main__":
    unittest.main()
