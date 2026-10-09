"""Drive the real Bash installer with arrow/space keys through a terminal.

Cancel at the install summary: no downloads, package managers or project writes.
"""
import os
from pathlib import Path
import select
import struct
import tempfile
import time


def main():
    import fcntl
    import pty
    import termios

    repo = Path(__file__).resolve().parent.parent
    with tempfile.TemporaryDirectory(prefix="facet menu ") as tmp:
        home = Path(tmp) / "home"
        home.mkdir()
        pid, fd = pty.fork()
        if pid == 0:
            os.execve("/bin/bash", ["bash", str(repo / "install.sh")],
                      dict(os.environ, HOME=str(home), TERM="xterm-256color", FACET_PLAIN="0", NO_COLOR=""))
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
        output = b""
        # Fresh profile: the component menu comes first (remotion and piper are
        # preselected). Move to piper, toggle it off, confirm, then decline.
        prompts = [(b"Space: toggle", b"\x1b[B \n"),
                   (b"Continue?", b"n\n")]
        index = 0
        deadline = time.monotonic() + 30
        try:
            while time.monotonic() < deadline:
                if select.select([fd], [], [], .1)[0]:
                    try:
                        output += os.read(fd, 65536)
                    except OSError:
                        pass
                if index < len(prompts) and prompts[index][0] in output:
                    os.write(fd, prompts[index][1])
                    index += 1
                done, status = os.waitpid(pid, os.WNOHANG)
                if done:
                    break
            else:
                os.kill(pid, 9)
                os.waitpid(pid, 0)
                raise AssertionError(f"menu stalled: {output!r}")
        finally:
            os.close(fd)
        assert os.waitstatus_to_exitcode(status) != 0 and index == 2, output
        assert b"Selected: remotion" in output, output
        assert b"Installation cancelled" in output, output
        assert not (home / ".facet" / "runtimes").exists() and not (home / ".facet" / "current").exists()
        assert b"\x1b[?25h" in output, "cursor visibility not restored"
        print("PASS: real terminal arrow selection, Space multi-select, answer summary, cancellation and cursor restoration.")


if __name__ == "__main__":
    main()
