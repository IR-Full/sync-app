#!/usr/bin/env python3
"""Split a protoc-gen-go(-grpc) file into the three-file layout this repo uses.

`protoc-gen-go` emits one file per .proto. This repo keeps generated Go in the
same shape as everything else it writes by hand — declarations separated by kind:

    <base>.types.go      every `type` declaration (message structs, interfaces)
    <base>.constants.go  every `const` and `var` (the raw descriptor, goTypes, …)
    <base>.go            every `func` (accessors, Reset/String/ProtoReflect, init)

Without this, regenerating overwrites `<base>.go` with the whole file while the
other two keep their copies of the same declarations, and the package stops
compiling with a wall of "redeclared in this block". That is exactly what
happened the last time someone tried, which is why the generated descriptor sat
on the pre-rename path for so long.

Imports are derived from the source rather than hardcoded: the three buckets need
different ones (the grpc stubs' interfaces pull in context and grpc; the
descriptor pulls in sync and protoreflect), and a fixed header would either
under- or over-import depending on which file is being split.

Usage:  python scripts/split-pb.py <generated.go> <target-dir> <base-name>
"""
from __future__ import annotations

import io
import re
import sys

# A top-level declaration starts at column 0 with one of these keywords. Inside a
# generated file nothing else sits at column 0 except comments, which attach to
# whatever follows them.
DECL = re.compile(r"^(type|const|var|func)\b")
KIND_OF = {"type": "types", "const": "constants", "var": "constants", "func": "funcs"}
SUFFIX = {"types": ".types.go", "constants": ".constants.go", "funcs": ".go"}

# `name "path"` or `"path"` inside an import block.
IMPORT_LINE = re.compile(r'^\s*(?:(\w+|\.|_)\s+)?"([^"]+)"\s*$')


def parse_imports(lines: list[int], start: int) -> tuple[list[tuple[str, str]], int]:
    """Returns [(local-name, path)] and the index just past the import block."""
    i = start
    while i < len(lines) and not lines[i].startswith("import"):
        if DECL.match(lines[i]):
            return [], start  # no imports at all
        i += 1
    if i >= len(lines):
        return [], start

    imports: list[tuple[str, str]] = []
    if lines[i].startswith("import ("):
        i += 1
        while i < len(lines) and not lines[i].startswith(")"):
            m = IMPORT_LINE.match(lines[i])
            if m:
                path = m.group(2)
                imports.append((m.group(1) or path.rsplit("/", 1)[-1], path))
            i += 1
        i += 1
    else:  # single-line `import name "path"`
        m = IMPORT_LINE.match(lines[i][len("import"):])
        if m:
            path = m.group(2)
            imports.append((m.group(1) or path.rsplit("/", 1)[-1], path))
        i += 1
    return imports, i


def render_imports(imports: list[tuple[str, str]], body: str) -> str:
    """Emits only the imports the body actually references."""
    used = [(name, path) for name, path in imports
            if name in ("_", ".") or re.search(rf"\b{re.escape(name)}\.", body)]
    if not used:
        return ""
    if len(used) == 1:
        name, path = used[0]
        return f'import {name} "{path}"\n'
    # gofmt -w afterwards regroups these; one block keeps the script simple.
    inner = "".join(f'\t{name} "{path}"\n' for name, path in used)
    return f"import (\n{inner})\n"


def main() -> int:
    if len(sys.argv) != 4:
        print(__doc__, file=sys.stderr)
        return 2
    src, target_dir, base = sys.argv[1], sys.argv[2], sys.argv[3]

    source = io.open(src, encoding="utf-8", newline="").read().replace("\r\n", "\n")
    lines = source.split("\n")

    # Everything before `package` is the file's doc comment plus the "Code
    # generated" banner; it belongs to the funcs file, which keeps the base name.
    pkg_at = next(i for i, ln in enumerate(lines) if ln.startswith("package "))
    package = lines[pkg_at].split()[1]
    preamble = "\n".join(lines[:pkg_at]).rstrip("\n")

    imports, i = parse_imports(lines, pkg_at + 1)
    while i < len(lines) and not DECL.match(lines[i]):
        i += 1

    buckets: dict[str, list[str]] = {"types": [], "constants": [], "funcs": []}
    pending: list[str] = []  # comments/blanks waiting for the decl they document
    current: list[str] | None = None

    for line in lines[i:]:
        match = DECL.match(line)
        if match:
            current = buckets[KIND_OF[match.group(1)]]
            current.extend(pending)
            pending = []
            current.append(line)
            continue
        if current is None:
            pending.append(line)
            continue
        # A blank line, or a comment after one, starts the next doc block: hold it
        # back so it travels with its declaration instead of trailing the previous.
        if not line.strip() or (line.startswith("//") and pending and not pending[-1].strip()):
            pending.append(line)
            continue
        current.extend(pending)
        pending = []
        current.append(line)

    for kind, decls in buckets.items():
        body = "\n".join(decls).strip("\n")
        lead = preamble + "\n\n" if (kind == "funcs" and preamble) else ""
        text = f"{lead}package {package}\n\n{render_imports(imports, body)}\n{body}\n"
        path = f"{target_dir}/{base}{SUFFIX[kind]}"
        io.open(path, "w", encoding="utf-8", newline="\n").write(text)
        print(f"wrote {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
