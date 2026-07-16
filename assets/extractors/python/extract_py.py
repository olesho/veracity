#!/usr/bin/env python3
"""Harness Python extractor (stdlib-only).

Reads a JSON request on stdin: {"subproject","root","files":[abs paths]} and
writes a JSON IR module list on stdout. Uses only the standard library `ast`
module, so it never resolves imports or needs the project's virtualenv.

NOTE: filled in by the docgen task; currently emits an empty module list.
"""
import json
import sys


def main() -> int:
    try:
        req = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        print(f"extract_py: invalid request: {exc}", file=sys.stderr)
        return 1
    _ = req
    json.dump({"modules": []}, sys.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
