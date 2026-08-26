#!/usr/bin/env python3
"""Verify that paired Tekton PipelineRun files (PR-branch vs main-branch pipeline)
stay in sync.

Each pull-request PipelineRun that uses a local pipeline annotation
(pipelinesascode.tekton.dev/pipeline) has a paired "-from-main" variant that
resolves the same pipeline via a git resolver pointing at the main branch.
The two files in each pair MUST be identical except for the expected
differences:
  1. metadata.name  (different suffix)
  2. The pipelinesascode.tekton.dev/pipeline annotation (present vs absent)
  3. The CEL filter's .tekton guard  (".tekton/***".pathChanged() vs negated)
  4. spec.pipelineRef  (name-based vs git resolver)

This script exits 0 if all pairs are consistent, 1 otherwise.
"""
import sys
import os
import re
import copy

# Requires PyYAML – available in most CI images; stdlib fallback not attempted.
try:
    import yaml
except ImportError:
    print("ERROR: PyYAML is required.  pip install pyyaml", file=sys.stderr)
    sys.exit(2)


TEKTON_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                          ".tekton")

# Annotation key constants to reduce typo risk.
ANN_PIPELINE = "pipelinesascode.tekton.dev/pipeline"
ANN_CEL_EXPRESSION = "pipelinesascode.tekton.dev/on-cel-expression"

# Expected pipeline source for the git-resolver variant.
EXPECTED_GIT_RESOLVER = {
    "url": "https://github.com/openshift/hypershift.git",
    "revision": "main",
    "pathInRepo": ".tekton/pipelines/common-operator-build.yaml",
}


def find_pairs():
    """Return list of (original_path, from_main_path) tuples."""
    pairs = []
    for name in sorted(os.listdir(TEKTON_DIR)):
        if name.endswith("-pull-request.yaml"):
            from_main = name.replace("-pull-request.yaml",
                                     "-pull-request-from-main.yaml")
            from_main_path = os.path.join(TEKTON_DIR, from_main)
            if os.path.exists(from_main_path):
                pairs.append((os.path.join(TEKTON_DIR, name), from_main_path))
    return pairs


def find_orphaned_from_main():
    """Return list of -from-main filenames without a matching -pull-request.yaml."""
    orphans = []
    for name in sorted(os.listdir(TEKTON_DIR)):
        if name.endswith("-pull-request-from-main.yaml"):
            original = name.replace("-pull-request-from-main.yaml",
                                    "-pull-request.yaml")
            if not os.path.exists(os.path.join(TEKTON_DIR, original)):
                orphans.append(name)
    return orphans


def load(path):
    try:
        with open(path) as f:
            return yaml.safe_load(f)
    except (OSError, yaml.YAMLError) as exc:
        print(f"ERROR: failed to load {path}: {exc}", file=sys.stderr)
        sys.exit(1)


def normalise_cel(cel_text):
    """Strip the .tekton/*** guard clause from a CEL expression to get the
    'base' filter that should be identical between the two files."""
    if cel_text is None:
        return ""
    # Remove the guard lines (handles both && ".tekton/***".pathChanged()
    # and && !".tekton/***".pathChanged())
    normalized = re.sub(
        r'\s*&&\s*!?"\s*\.tekton/\*\*\*\s*"\.pathChanged\(\)\s*', ' ',
        cel_text,
    )
    # Collapse whitespace for comparison.
    return " ".join(normalized.split())


def normalise_pipelineref(doc):
    """Return a copy of the doc with pipelineRef normalised out so we can
    compare the rest."""
    d = copy.deepcopy(doc)
    d.get("spec", {}).pop("pipelineRef", None)
    return d


def check_pair(orig_path, from_main_path):
    """Return list of error strings (empty = OK)."""
    errors = []
    orig = load(orig_path)
    fm = load(from_main_path)

    orig_name = os.path.basename(orig_path)
    fm_name = os.path.basename(from_main_path)

    # ---- 1.  Annotations ----
    orig_annot = dict(orig.get("metadata", {}).get("annotations", {}))
    fm_annot = dict(fm.get("metadata", {}).get("annotations", {}))

    # Original MUST have the pipeline annotation.
    if ANN_PIPELINE not in orig_annot:
        errors.append(f"{orig_name}: missing {ANN_PIPELINE} annotation")

    # From-main MUST NOT have the pipeline annotation.
    if ANN_PIPELINE in fm_annot:
        errors.append(f"{fm_name}: must NOT have {ANN_PIPELINE} annotation")

    # ---- 2.  CEL expression base must match ----
    orig_cel = orig_annot.get(ANN_CEL_EXPRESSION, "")
    fm_cel = fm_annot.get(ANN_CEL_EXPRESSION, "")

    # Original must contain the positive guard.
    if '".tekton/***".pathChanged()' not in orig_cel:
        errors.append(f"{orig_name}: CEL missing '.tekton/***'.pathChanged() guard")

    # From-main must contain the negated guard.
    if '!".tekton/***".pathChanged()' not in fm_cel:
        errors.append(f"{fm_name}: CEL missing negated '.tekton/***'.pathChanged() guard")

    # After stripping the guard, the base expression must be identical.
    orig_base = normalise_cel(orig_cel)
    fm_base = normalise_cel(fm_cel)
    if orig_base != fm_base:
        errors.append(
            f"CEL base mismatch between {orig_name} and {fm_name}:\n"
            f"  original: {orig_base}\n"
            f"  from-main: {fm_base}"
        )

    # ---- 3.  Compare everything else (minus expected diffs) ----
    # Strip annotations we already checked.
    for key in (ANN_CEL_EXPRESSION, ANN_PIPELINE):
        orig_annot.pop(key, None)
        fm_annot.pop(key, None)

    if orig_annot != fm_annot:
        errors.append(
            f"Annotation mismatch (excluding CEL and pipeline) between "
            f"{orig_name} and {fm_name}"
        )

    # Labels must match.
    orig_labels = orig.get("metadata", {}).get("labels", {})
    fm_labels = fm.get("metadata", {}).get("labels", {})
    if orig_labels != fm_labels:
        errors.append(f"Label mismatch between {orig_name} and {fm_name}")

    # Namespace must match.
    if orig.get("metadata", {}).get("namespace") != fm.get("metadata", {}).get("namespace"):
        errors.append(f"Namespace mismatch between {orig_name} and {fm_name}")

    # ---- 4.  spec (minus pipelineRef) must match ----
    orig_spec_data = normalise_pipelineref(orig).get("spec", {})
    fm_spec_data = normalise_pipelineref(fm).get("spec", {})
    if orig_spec_data != fm_spec_data:
        differing = sorted(
            k for k in set(orig_spec_data) | set(fm_spec_data)
            if orig_spec_data.get(k) != fm_spec_data.get(k)
        )
        errors.append(
            f"spec mismatch (excluding pipelineRef) between "
            f"{orig_name} and {fm_name}: differing fields: {', '.join(differing)}"
        )

    # ---- 5.  Validate from-main pipelineRef uses git resolver ----
    fm_ref = fm.get("spec", {}).get("pipelineRef", {})
    if fm_ref.get("resolver") != "git":
        errors.append(f"{fm_name}: pipelineRef.resolver must be 'git'")
    else:
        params = {p["name"]: p["value"]
                  for p in fm_ref.get("params", [])}
        for key, expected in EXPECTED_GIT_RESOLVER.items():
            if params.get(key) != expected:
                errors.append(
                    f"{fm_name}: pipelineRef param '{key}' = {params.get(key)!r}, "
                    f"expected {expected!r}"
                )

    return errors


def main():
    pairs = find_pairs()
    if not pairs:
        print("WARNING: no PipelineRun pairs found in .tekton/", file=sys.stderr)
        sys.exit(1)

    all_errors = []

    # Detect orphaned -from-main files without a matching pull-request file.
    for orphan in find_orphaned_from_main():
        all_errors.append(
            f"{orphan}: orphaned -from-main file with no matching "
            f"-pull-request.yaml"
        )

    for orig, fm in pairs:
        print(f"Checking pair: {os.path.basename(orig)} <-> {os.path.basename(fm)}")
        errs = check_pair(orig, fm)
        all_errors.extend(errs)

    if all_errors:
        print(f"\n{len(all_errors)} error(s) found:", file=sys.stderr)
        for e in all_errors:
            print(f"  - {e}", file=sys.stderr)
        sys.exit(1)

    print(f"\nAll {len(pairs)} pipeline pair(s) are consistent.")
    sys.exit(0)


if __name__ == "__main__":
    main()
