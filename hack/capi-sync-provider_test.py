"""Run the sync script with hermetic tools; set CAPI_SYNC_TEST_BASH for Bash 3.2."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import textwrap

import pytest


ROOT = Path(__file__).resolve().parents[1]


@pytest.mark.parametrize("provider,packages", [
    ("agent", {
        "v1alpha1": [],
        "v1beta1": [],
    }),
    ("aws", {
        "api/v1beta1": ["sigs.k8s.io/cluster-api-provider-aws/v2/api/"],
        "api/v1beta2": [],
        "exp/api/v1beta1": ["sigs.k8s.io/cluster-api-provider-aws/v2/api/"],
        "exp/api/v1beta2": ["sigs.k8s.io/cluster-api-provider-aws/v2/api/"],
    }),
    ("openstack", {
        "pkg/utils/optional": [],
        "pkg/utils/errors": [],
        "api/v1alpha1": [
            "sigs.k8s.io/cluster-api-provider-openstack/api/",
            "sigs.k8s.io/cluster-api-provider-openstack/pkg/utils/",
        ],
        "api/v1beta1": ["sigs.k8s.io/cluster-api-provider-openstack/pkg/utils/"],
    }),
], ids=[
    "When prefixes are empty, it should pass no optional arguments",
    "When prefixes change between packages, it should reset optional arguments",
    "When multiple prefixes are allowed, it should preserve argument boundaries",
])
def test_sync_preserves_copy_arguments_and_regenerates_tree(tmp_path, provider, packages):
    root = tmp_path / "repo with spaces [glob]"
    hack = root / "hack"
    hack.mkdir(parents=True)
    for script in ("capi-sync-provider.sh", "capi-workspace-run.sh"):
        shutil.copy2(ROOT / "hack" / script, hack / script)
    vendor = hack / "capi-vendor" / provider
    vendor.mkdir(parents=True)
    shutil.copy2(ROOT / "hack/capi-vendor" / provider / "vendor_imports.go", vendor)
    (root / "go.mod").write_text("module example.com/sync-test\n\ngo 1.26.0\n")
    destination = root / "pkg/capi" / provider
    destination.mkdir(parents=True)
    (destination / "go.mod").write_text("module example.com/provider\n")
    (destination / "stale.go").touch()
    source = tmp_path / "module cache with spaces [glob]"
    source.mkdir()
    tools = tmp_path / "bin"
    tools.mkdir()
    calls = tmp_path / "calls.jsonl"
    go = tools / "go"
    go.write_text(f"#!{sys.executable}\n" + textwrap.dedent("""\
        import json
        import os
        from pathlib import Path
        import sys

        arguments = sys.argv[1:]
        with open(os.environ['CAPI_SYNC_CALLS'], 'a') as calls:
            calls.write(json.dumps(arguments) + '\\n')
        if arguments[:3] == ['mod', 'download', '-json']:
            print(json.dumps({'Dir': os.environ['CAPI_SYNC_SOURCE']}))
        elif arguments[0] == 'run':
            if '--localize' in arguments:
                destination = Path(arguments[arguments.index('--dir') + 1]) / 'capicore'
            else:
                destination = Path(arguments[arguments.index('--dst') + 1])
            destination.mkdir(parents=True)
            (destination / 'types.go').write_text('package types\\n')
        elif arguments[:2] not in (['mod', 'tidy'], ['list', '-m']):
            sys.exit('unexpected go invocation: ' + repr(arguments))
        """))
    go.chmod(0o755)
    controller = tools / "controller-gen"
    controller.write_text("#!/bin/sh\nexit 0\n")
    controller.chmod(0o755)
    environment = os.environ.copy()
    environment.update(
        PATH=str(tools) + os.pathsep + environment["PATH"],
        CONTROLLER_GEN=str(controller), CAPI_SYNC_CALLS=str(calls),
        CAPI_SYNC_SOURCE=str(source),
    )
    result = subprocess.run(
        [environment.get("CAPI_SYNC_TEST_BASH", "bash"), str(hack / "capi-sync-provider.sh"), provider],
        env=environment, capture_output=True, text=True, timeout=30,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    invocations = [json.loads(line) for line in calls.read_text().splitlines()]
    copies = [arguments[2:] for arguments in invocations if arguments[0] == "run" and "--src" in arguments]
    assert copies == [
        ["--src", str(source / package), "--dst", str(destination / package)]
        + [argument for prefix in prefixes for argument in ("--allow", prefix)]
        for package, prefixes in packages.items()
    ]
    assert not (destination / "stale.go").exists()
    assert (destination / "go.mod").read_text() == "module example.com/provider\n"
    for package in packages:
        assert (destination / package / "types.go").exists()
    assert (destination / "capicore/types.go").exists()
    assert invocations[-1] == ["mod", "tidy"]
    assert f"Done syncing {provider}" in result.stdout
