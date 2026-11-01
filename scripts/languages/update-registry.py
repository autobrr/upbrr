#!/usr/bin/env python3
"""Extract language names/scopes from a downloaded IANA language-subtag registry."""

import pathlib
import re
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
records = []
for block in source.split("%%"):
    fields = {}
    last = None
    for line in block.splitlines():
        if line.startswith(" ") and last:
            fields[last][-1] += " " + line.strip()
        elif ": " in line:
            key, value = line.split(": ", 1)
            fields.setdefault(key, []).append(value)
            last = key
    code = fields.get("Subtag", [""])[0]
    if fields.get("Type") == ["language"] and re.fullmatch("[a-z]{2,3}", code):
        names = fields["Description"]
        if any("|" in name or "\t" in name for name in names):
            raise ValueError(f"unexpected language-name separator: {code}")
        records.append((code, fields.get("Scope", [""])[0], fields.get("Preferred-Value", [""])[0], "|".join(names)))

print("# https://www.iana.org/assignments/language-subtag-registry/language-subtag-registry")
print("# " + source.splitlines()[0])
print("# Regenerate: python3 scripts/languages/update-registry.py <downloaded-registry>")
for record in sorted(records):
    print("\t".join(record))
