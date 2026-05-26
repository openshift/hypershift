#!/usr/bin/env python3
"""Verify that docs/mkdocs.yml nav entries are sorted alphabetically.

Index pages (index.md) are exempt from sorting and must appear first
in each section.

Use --max-depth to control how many nav levels are checked:
  1  = only top-level entries (default)
  -1 = all levels (unlimited)
"""

import argparse
import os
import sys

try:
    import yaml
except ImportError:
    print("ERROR: PyYAML is required but not installed.")
    print("Install with: pip install pyyaml")
    sys.exit(1)

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
DOCS_DIR = os.path.join(SCRIPT_DIR, '..', 'docs', 'content')


class _SafeLoaderIgnoreUnknown(yaml.SafeLoader):
    """SafeLoader that ignores unknown YAML tags (e.g. !!python/name)."""
    pass


_SafeLoaderIgnoreUnknown.add_multi_constructor(
    'tag:yaml.org,2002:python/',
    lambda loader, suffix, node: None,
)


def get_title_from_file(filepath):
    """Read title from markdown file frontmatter or first H1."""
    full_path = os.path.join(DOCS_DIR, filepath)
    if not os.path.exists(full_path):
        return os.path.splitext(os.path.basename(filepath))[0].replace('-', ' ').title()

    with open(full_path) as f:
        content = f.read()

    # Check frontmatter title
    if content.startswith('---'):
        end = content.find('---', 3)
        if end != -1:
            for line in content[3:end].strip().split('\n'):
                if line.startswith('title:'):
                    return line[6:].strip().strip('"').strip("'")

    # Check first H1 heading
    for line in content.split('\n'):
        if line.startswith('# '):
            return line[2:].strip()

    return os.path.splitext(os.path.basename(filepath))[0].replace('-', ' ').title()


def is_index_entry(entry):
    """Check if an entry is an index page."""
    path = None
    if isinstance(entry, str):
        path = entry
    elif isinstance(entry, dict):
        value = list(entry.values())[0]
        if isinstance(value, str):
            path = value
    if path:
        basename = os.path.basename(path)
        return basename == 'index.md' or basename == '_index.md' or basename.endswith('-index.md')
    return False


def get_display_title(entry):
    """Get the display title for a nav entry."""
    if isinstance(entry, str):
        return get_title_from_file(entry)
    elif isinstance(entry, dict):
        return list(entry.keys())[0]
    return ""


def check_section_order(entries, path="", max_depth=1):
    """Check that entries in a section are sorted alphabetically.

    max_depth controls how many levels deep to verify:
      1  = only this level (default)
     -1  = unlimited (all levels)

    Returns a list of error messages.
    """
    errors = []
    regular_entries = []
    section = path if path else "root"

    seen_non_index = False
    for i, entry in enumerate(entries, start=1):
        if is_index_entry(entry):
            if seen_non_index:
                errors.append("")
                errors.append(
                    "Section '{}' has an index entry after non-index entries (position {}).".format(section, i)
                )
            continue
        seen_non_index = True
        title = get_display_title(entry)
        regular_entries.append((title, entry))

    titles = [t for t, _ in regular_entries]
    sorted_titles = sorted(titles, key=str.casefold)

    if titles != sorted_titles:
        errors.append("")
        errors.append("Section '{}' is not sorted alphabetically:".format(section))
        errors.append("  Current order:")
        for t in titles:
            errors.append("    - {}".format(t))
        errors.append("  Expected order:")
        for t in sorted_titles:
            errors.append("    - {}".format(t))

    # Recursively check subsections if depth allows
    if max_depth != 1:
        for title, entry in regular_entries:
            if isinstance(entry, dict):
                value = list(entry.values())[0]
                if isinstance(value, list):
                    sub_path = "{} > {}".format(path, title) if path else title
                    next_depth = max_depth - 1 if max_depth > 0 else max_depth
                    errors.extend(check_section_order(value, path=sub_path, max_depth=next_depth))

    return errors


def main():
    parser = argparse.ArgumentParser(description="Verify docs nav alphabetical order.")
    parser.add_argument(
        '--max-depth', type=int, default=1,
        help='How many nav levels to verify (1=top only, -1=unlimited). Default: 1',
    )
    args = parser.parse_args()
    if args.max_depth != -1 and args.max_depth < 1:
        parser.error("--max-depth must be -1 (unlimited) or a positive integer")

    mkdocs_path = os.path.join(SCRIPT_DIR, '..', 'docs', 'mkdocs.yml')

    with open(mkdocs_path) as f:
        config = yaml.load(f, Loader=_SafeLoaderIgnoreUnknown)

    nav = config.get('nav', [])

    howto_section = None
    for entry in nav:
        if isinstance(entry, dict) and 'How-to guides' in entry:
            howto_section = entry['How-to guides']
            break

    if howto_section is None:
        print("ERROR: Could not find 'How-to guides' section in nav")
        sys.exit(1)

    errors = check_section_order(howto_section, "How-to guides", max_depth=args.max_depth)

    if errors:
        print("ERROR: Docs nav is not sorted alphabetically:")
        for line in errors:
            print(line)
        print("")
        print("Please sort the nav entries in docs/mkdocs.yml alphabetically.")
        print("Index pages (index.md) should appear first in each section.")
        sys.exit(1)

    print("OK: Docs nav entries are sorted alphabetically.")


if __name__ == '__main__':
    main()
