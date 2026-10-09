"""Test curl | bash with a real controlling terminal and an offline fixture."""
import hashlib
import os
from pathlib import Path
import select
import subprocess
import tempfile
import time
import zipfile

REPO = Path(__file__).resolve().parent.parent
DIGEST = "ee3ceddbd50847bac8b16ae65b4d8e3677deb76933baaa86091945567fc65f09"


def main():
    if os.name == "nt":
        raise SystemExit("Run test-bootstrap.ps1 on Windows")
    import pty
    with tempfile.TemporaryDirectory(prefix="facet bootstrap tests ") as root:
        root = Path(root)
        archive = root / "fixture.zip"
        with zipfile.ZipFile(archive, "w") as z:
            z.writestr("install.sh", '#!/bin/bash\nprintf "Fixture prompt: "\nread -r answer\n[[ "$answer" == answer ]] || exit 7\nprintf "%s" "$PWD" > "$MARKER"\nexit "${CHILD_EXIT:-0}"\n')
            for name in ["install.ps1", "installer/manifest.tsv", "installer/verify.html"]:
                z.writestr(name, "fixture")
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        original = (REPO / "docs/install.sh").read_text()
        bootstrap = root / "bootstrap"
        curl = root / "curl"
        curl.write_text('#!/bin/bash\nif [[ "$*" == *"https://kayiik.github.io/facet/install.sh"* ]]; then cat "$BOOTSTRAP"; exit; fi\nwhile (($#)); do if [[ "$1" == -o ]]; then cp "$ARCHIVE" "$2"; exit; fi; shift; done\nexit 3\n')
        curl.chmod(0o755)
        marker = root / "marker"
        scratch = root / "scratch"
        scratch.mkdir()
        env = dict(os.environ, PATH=str(root) + ":" + os.environ["PATH"], BOOTSTRAP=str(bootstrap), ARCHIVE=str(archive), MARKER=str(marker), TMPDIR=str(scratch))
        command = "set -o pipefail; curl -fsSL https://kayiik.github.io/facet/install.sh | sh"
        for mode, child_exit in [("success", 0), ("checksum", 0), ("child-failure", 9)]:
            bootstrap.write_text(original.replace(DIGEST, "0" * 64 if mode == "checksum" else digest))
            marker.unlink(missing_ok=True)
            pid, fd = pty.fork()
            if pid == 0:
                os.chdir(root)
                os.execve("/bin/bash", ["bash", "-c", command], dict(env, CHILD_EXIT=str(child_exit)))
            output = b""
            answered = False
            status = None
            deadline = time.monotonic() + 30
            try:
                while time.monotonic() < deadline:
                    if select.select([fd], [], [], 0.1)[0]:
                        try:
                            output += os.read(fd, 65536)
                        except OSError:
                            pass
                    if b"Fixture prompt:" in output and not answered:
                        os.write(fd, b"answer\n")
                        answered = True
                    done, status = os.waitpid(pid, os.WNOHANG)
                    if done:
                        break
                else:
                    os.kill(pid, 9)
                    os.waitpid(pid, 0)
                    raise AssertionError(f"bootstrap hung: {output!r}")
            finally:
                os.close(fd)
            code = os.waitstatus_to_exitcode(status)
            assert code == (0 if mode == "success" else 9 if mode == "child-failure" else 1), (mode, code, output)
            assert marker.exists() == (mode != "checksum"), (mode, output)
            if marker.exists():
                assert Path(marker.read_text()).resolve() == root.resolve(), "bootstrap changed user's working directory"
            assert not list(scratch.iterdir()), "temporary files leaked"
        result = subprocess.run(["bash", "-c", command], env=env, capture_output=True, start_new_session=True)
        assert result.returncode != 0 and b"needs a terminal" in result.stderr, result
        print("PASS: curl | bash with TTY prompts, checksum rejection, exit propagation, cleanup, and no-TTY failure.")


if __name__ == "__main__":
    main()
