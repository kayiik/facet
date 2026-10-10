"""Tests for the release contract both installers rely on."""
import sys
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
import release_contract as rc  # noqa: E402

COMMIT = "a" * 40


def entries(version="1.2.3", targets=("linux/amd64", "windows/amd64")):
    out = []
    for target in targets:
        for product, members in (("core", rc.core_members(target)), ("app", rc.app_members(target))):
            out.append((product, target, rc.archive_name(product, version, target), {m: "b" * 64 for m in members}))
    return out


class ReleaseContractTest(unittest.TestCase):
    def test_archive_names(self):
        self.assertEqual(rc.archive_name("core", "0.0.2", "windows/arm64"), "facet-core_0.0.2_windows_arm64.zip")
        self.assertEqual(rc.archive_name("app", "0.0.2", "darwin/amd64"), "facet-app_0.0.2_darwin_amd64.tar.gz")
        for product, target in (("bundle", "linux/amd64"), ("core", "plan9/amd64")):
            with self.assertRaises(ValueError):
                rc.archive_name(product, "0.0.2", target)

    def test_versions(self):
        for good in ("0.0.1", "1.20.3", "0.0.2-rc.1"):
            self.assertEqual(rc.release_version(good), good)
        for bad in ("v0.0.1", "0.1", "01.0.0", "0.0.1-01", "0.0.1-"):
            with self.assertRaises(ValueError):
                rc.release_version(bad)

    def test_unsafe_members(self):
        for bad in ("../x", "/abs", "a/./b", "bin/CON.exe", "bin/fac et", "bin/x."):
            with self.assertRaises(ValueError):
                rc.safe_member(bad)
        with self.assertRaises(ValueError):
            rc.validate_member_set(["bin/facet", "BIN/FACET"])
        with self.assertRaises(ValueError):
            rc.validate_member_set(["kernel", "kernel/LICENSE"])

    def test_manifest_round_trip(self):
        text = rc.release_tsv("1.2.3", COMMIT, entries())
        parsed = rc.parse_release_tsv(text)
        self.assertEqual(parsed["version"], "1.2.3")
        self.assertEqual(parsed["archives"][("app", "windows/amd64")], "facet-app_1.2.3_windows_amd64.zip")
        self.assertIn("kernel/compa-kernel.exe", parsed["files"][("app", "windows/amd64")])
        self.assertIn("bin/facet", parsed["files"][("core", "linux/amd64")])

    def test_manifest_refuses_other_files(self):
        extra = entries()
        extra[0][3]["bin/other"] = "c" * 64
        with self.assertRaises(ValueError):
            rc.release_tsv("1.2.3", COMMIT, extra)
        text = rc.release_tsv("1.2.3", COMMIT, entries()).replace("facet-release-v1", "facet-release-v0")
        with self.assertRaises(ValueError):
            rc.parse_release_tsv(text)

    def test_products_do_not_share_files(self):
        for target in rc.TARGETS:
            core, app = set(rc.core_members(target)), set(rc.app_members(target))
            self.assertFalse(core & app, target)
            rc.validate_member_set(core | app)

    def test_compa_pins_cover_every_target(self):
        for target in rc.TARGETS:
            asset, archive_digest, kernel_digest = rc.COMPA_ARCHIVES[target]
            self.assertIn(rc.COMPA_VERSION, asset)
            self.assertRegex(archive_digest, rc.HASH)
            self.assertRegex(kernel_digest, rc.HASH)
            self.assertEqual(rc.compa_members(target)[0][1], "kernel/compa-kernel" + rc.exe(target))


if __name__ == "__main__":
    unittest.main()
