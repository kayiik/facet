"""Tests for staging the website with a release's installers."""
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location("stage_site", Path(__file__).resolve().parent / "stage-site.py")
stage_site = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(stage_site)
COMMIT = "c" * 40


class StageSiteTest(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="facet-stage-site-"))
        self.release = self.root / "release"
        self.page = self.root / "site"
        (self.page / "fonts").mkdir(parents=True)
        self.release.mkdir()
        (self.page / "index.html").write_text("<a href=\"tag/@TAG@\">Install @VERSION@</a>\n", encoding="utf-8")
        (self.page / "site.css").write_text("body {}\n", encoding="utf-8")
        (self.page / "fonts" / "face.woff2").write_bytes(b"woff2")
        self.write_release("0.0.2")

    def tearDown(self):
        for path in sorted(self.root.rglob("*"), reverse=True):
            path.unlink() if path.is_file() else path.rmdir()
        self.root.rmdir()

    def write_release(self, version, corrupt=None):
        (self.release / "install.ps1").write_text("# ps1\n", encoding="utf-8")
        (self.release / "install.sh").write_text("# sh\n", encoding="utf-8")
        (self.release / "build-manifest.json").write_text(json.dumps({"version": version, "commit": COMMIT}), encoding="utf-8")
        lines = []
        for name in ("build-manifest.json", "install.ps1", "install.sh"):
            digest = hashlib.sha256((self.release / name).read_bytes()).hexdigest()
            lines.append(("0" * 64 if name == corrupt else digest) + "  " + name)
        (self.release / "SHA256SUMS").write_text("\n".join(lines) + "\n", encoding="utf-8")

    def stage(self, tag="v0.0.2", contains=lambda commit: commit == COMMIT, name="out"):
        output = self.root / name
        stage_site.stage_site(self.release, self.page, output, tag, contains)
        return output

    def test_stages_the_page_with_the_release(self):
        output = self.stage()
        self.assertEqual((output / "index.html").read_text(encoding="utf-8"), "<a href=\"tag/v0.0.2\">Install 0.0.2</a>\n")
        self.assertEqual((output / "fonts" / "face.woff2").read_bytes(), b"woff2")
        for name in stage_site.RELEASE_FILES:
            self.assertEqual((output / name).read_bytes(), (self.release / name).read_bytes())

    def test_refuses_what_must_not_be_published(self):
        for tag in ("v0.0.2-rc.1", "0.0.2", "latest"):
            with self.assertRaises(ValueError, msg=tag):
                self.stage(tag=tag, name="out-" + tag)
        with self.assertRaises(ValueError):
            self.stage(tag="v0.0.3")
        with self.assertRaises(ValueError):
            self.stage(contains=lambda commit: False)
        self.write_release("0.0.2", corrupt="install.sh")
        with self.assertRaises(ValueError):
            self.stage()

    def test_refuses_a_page_with_its_own_installers(self):
        (self.page / "install.sh").write_text("# a stale copy\n", encoding="utf-8")
        with self.assertRaises(ValueError):
            self.stage()

    def test_refuses_a_page_that_names_no_release(self):
        (self.page / "index.html").write_text("<p>Facet</p>\n", encoding="utf-8")
        with self.assertRaises(ValueError):
            self.stage()

    def test_refuses_an_existing_output(self):
        (self.root / "out").mkdir()
        with self.assertRaises(ValueError):
            self.stage()


if __name__ == "__main__":
    unittest.main()
