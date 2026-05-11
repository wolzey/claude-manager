#!/usr/bin/env python3
"""Idempotently add (or remove) Bash(cmgr *) and Bash(cmgr:*) permissions in ~/.claude/settings.json.

Usage: allow-cmgr.py [--remove]
"""
import json
import sys
from pathlib import Path

PATTERNS = ["Bash(cmgr *)", "Bash(cmgr:*)"]


def main() -> None:
    remove = "--remove" in sys.argv
    path = Path.home() / ".claude" / "settings.json"
    if not path.exists():
        data: dict = {}
    else:
        data = json.loads(path.read_text())

    perms = data.setdefault("permissions", {})
    allow = perms.setdefault("allow", [])

    if remove:
        for p in PATTERNS:
            if p in allow:
                allow.remove(p)
                print(f"removed: {p}")
    else:
        for p in PATTERNS:
            if p not in allow:
                allow.append(p)
                print(f"allow:   {p}")

    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(data, indent=2) + "\n")
    tmp.replace(path)


if __name__ == "__main__":
    main()
