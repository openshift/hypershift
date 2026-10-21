"""Exercise the real verification phase rules with hermetic leaf recipes."""

import os
from pathlib import Path
import re
import subprocess
import sys
import textwrap

import pytest


MAKEFILE = Path(__file__).resolve().parents[1] / "Makefile"
PHASES = ("verify", "verify-parallel", "verify-ci")


@pytest.fixture
def verification(tmp_path):
    source = MAKEFILE.read_text()
    rules = []
    leaves = set()
    for phase in PHASES:
        rule = re.search(rf"^{phase}:.*(?:\n\t.*)*", source, re.MULTILINE).group()
        rules.append(rule)
        leaves.update(rule.splitlines()[0].split(":", 1)[1].split("#", 1)[0].split())
    leaves.update(("verify-capi-sync", "verify-git-clean"))
    leaves.difference_update(PHASES)
    worker = tmp_path / "worker.py"
    worker.write_text(textwrap.dedent("""\
        import os
        from pathlib import Path
        import sys
        import time

        target = sys.argv[1]
        with open('events', 'a') as events:
            events.write(f'{target} {os.environ.get("VERIFY_PHASE", "serial")}\\n')
        if target == os.environ.get('FAIL_TARGET'):
            sys.exit(1)
        if target == 'verify-capi-sync':
            Path('synced').touch()
        if target in ('verify-capi-decoupling', 'lint') and os.environ.get('RENDEZVOUS'):
            assert Path('synced').exists(), 'reader started before sync completed'
            Path(target).touch()
            deadline = time.monotonic() + 10
            while not all(Path(reader).exists() for reader in ('verify-capi-decoupling', 'lint')):
                if time.monotonic() > deadline:
                    sys.exit('readers were serialized')
                time.sleep(0.01)
        """))
    fixture = tmp_path / "Makefile"
    fixture.write_text(
        ".PHONY: " + " ".join((*PHASES, *sorted(leaves))) + "\n"
        + "verify-parallel: export VERIFY_PHASE = parallel\n"
        + "\n".join(rules) + "\n"
        + "\n".join(
            f"{leaf}:\n\t@{sys.executable} {worker} {leaf}"
            for leaf in sorted(leaves)
        ) + "\n"
    )

    def run(target="verify", fail_target="", rendezvous=False, jobs="2"):
        environment = os.environ.copy()
        environment.pop("MAKEFLAGS", None)
        environment.pop("MFLAGS", None)
        environment.pop("VERIFY_PHASE", None)
        environment.update(FAIL_TARGET=fail_target, RENDEZVOUS="1" if rendezvous else "")
        result = subprocess.run(
            ["make", "--no-print-directory", f"-j{jobs}", target],
            cwd=tmp_path, env=environment, capture_output=True, text=True, timeout=30,
        )
        events = (tmp_path / "events").read_text().splitlines()
        return result, events

    return run


@pytest.mark.parametrize("jobs", ["1", "2", "8"])
def test_verify_sequences_sync_before_parallel_readers(verification, jobs):
    result, events = verification(rendezvous=True, jobs=jobs)
    assert result.returncode == 0, result.stdout + result.stderr
    assert events.count("verify-capi-sync serial") == 1
    sync = events.index("verify-capi-sync serial")
    assert events.index("update serial") < sync
    for reader in ("verify-capi-decoupling", "lint"):
        assert sync < events.index(f"{reader} parallel")
    assert events[-1] == "verify-git-clean serial"


@pytest.mark.parametrize("failed", ["update", "verify-capi-sync", "verify-capi-decoupling", "lint"])
def test_verify_propagates_failure(verification, failed):
    result, events = verification(fail_target=failed)
    assert result.returncode != 0
    assert not any(event.startswith("verify-git-clean ") for event in events)
    if failed in ("update", "verify-capi-sync"):
        assert not any(event.endswith(" parallel") for event in events)


@pytest.mark.parametrize("target", ["verify-capi-decoupling", "lint", "verify-parallel"])
def test_standalone_readers_do_not_force_sync(verification, target):
    result, events = verification(target=target)
    assert result.returncode == 0, result.stdout + result.stderr
    assert not any(event.startswith("verify-capi-sync ") for event in events)
