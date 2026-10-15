#!/usr/bin/env python3
"""Exercise a built NexShell binary through a real Unix pseudo-terminal.

Requires Python 3.9+ and the platform's /bin/cat and /bin/sleep. All shell
history and command activity use a temporary home directory.
"""

import argparse
import errno
import os
from pathlib import Path
import select
import signal
import tempfile
import time


def verify(binary, transcript_path=None):
    import pty

    transcript = bytearray()
    position = 0
    pid = None
    master = None

    def wait_for(marker, timeout=15):
        nonlocal position
        deadline = time.monotonic() + timeout
        while marker not in transcript[position:]:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise AssertionError(
                    f"timed out awaiting {marker!r}: {bytes(transcript)!r}"
                )
            ready, _, _ = select.select([master], [], [], remaining)
            if not ready:
                continue
            try:
                chunk = os.read(master, 65536)
            except OSError as exc:
                if exc.errno == errno.EIO:
                    chunk = b""
                else:
                    raise
            if not chunk:
                raise AssertionError(
                    f"terminal closed awaiting {marker!r}: {bytes(transcript)!r}"
                )
            transcript.extend(chunk)
        position = transcript.index(marker, position) + len(marker)

    def send(command):
        os.write(master, command)

    def wait_foreground():
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            group = os.tcgetpgrp(master)
            if group != pid:
                return group
            time.sleep(0.005)
        raise AssertionError("child process never received foreground terminal")

    try:
        with tempfile.TemporaryDirectory(prefix="nexshell-pty-home-") as home:
            pid, master = pty.fork()
            if pid == 0:
                env = os.environ.copy()
                env["HOME"] = home
                os.chdir(home)
                os.execve(str(binary), [str(binary)], env)
            prompt = "❯ ".encode()
            wait_for(prompt)

            send(b"/bin/cat\n")
            wait_foreground()
            send(b"pty-stdin-payload\n")
            # One copy comes from terminal echo, the other from the child.
            wait_for(b"pty-stdin-payload\r\n")
            wait_for(b"pty-stdin-payload\r\n")
            send(b"\x04")
            wait_for(prompt)
            if os.tcgetpgrp(master) != pid:
                raise AssertionError("shell lost terminal after stdin EOF")

            send(b"/bin/sleep 60 | /bin/cat\n")
            group = wait_foreground()
            send(b"\x03")
            wait_for(prompt)
            if os.tcgetpgrp(master) != pid:
                raise AssertionError("shell lost terminal after Ctrl+C")
            try:
                os.killpg(group, 0)
            except ProcessLookupError:
                pass
            else:
                raise AssertionError("interrupted pipeline process group survived")

            send(b"echo resumed-after-interrupt\n")
            wait_for(b"\r\nresumed-after-interrupt\r\n")
            wait_for(prompt)
            send(b"echo discarded-before-newline")
            send(b"\x03")
            wait_for(prompt)
            send(b"echo resumed-after-prompt-interrupt\n")
            wait_for(b"\r\nresumed-after-prompt-interrupt\r\n")
            wait_for(prompt)

            send(b"exit 0\n")
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                waited, status = os.waitpid(pid, os.WNOHANG)
                if waited:
                    pid = None
                    code = os.waitstatus_to_exitcode(status)
                    if code != 0:
                        raise AssertionError(f"shell exit status was {code}")
                    break
                time.sleep(0.005)
            else:
                raise AssertionError("shell failed to exit")
            print(
                "PASS: Unix REPL terminal stdin/EOF, pipeline Ctrl+C cleanup, "
                "prompt recovery, prompt Ctrl+C, clean exit"
            )
    finally:
        if pid is not None:
            # Clean up both the shell and any foreground pipeline if a check
            # fails; each belongs to the private session created by pty.fork.
            try:
                group = os.tcgetpgrp(master)
                if group > 0 and group != os.getpgrp():
                    os.killpg(group, signal.SIGKILL)
            except (OSError, TypeError):
                pass
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            os.waitpid(pid, 0)
        if master is not None:
            os.close(master)
        if transcript_path is not None:
            transcript_path.parent.mkdir(parents=True, exist_ok=True)
            transcript_path.write_bytes(transcript)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path, help="path to the built NexShell binary")
    parser.add_argument("--transcript", type=Path, help="optional terminal transcript file")
    args = parser.parse_args()
    if os.name != "posix":
        parser.error("this test requires a Unix pseudo-terminal")
    for tool in ("/bin/cat", "/bin/sleep"):
        if not os.access(tool, os.X_OK):
            parser.error(f"required terminal fixture is unavailable: {tool}")
    binary = args.binary.resolve(strict=True)
    if not os.access(binary, os.X_OK):
        parser.error(f"binary is not executable: {binary}")
    verify(binary, args.transcript)


if __name__ == "__main__":
    main()
