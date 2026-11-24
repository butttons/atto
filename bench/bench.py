#!/usr/bin/env python3
"""Mechanical comparison of three ways an agent can land one edit.

This is not a model-in-the-loop benchmark. It measures the substrate: does the
edit land, how many bytes does the agent have to send, and how long does the
tool take. The "typo" case is a proxy for the dominant model failure: the old
text is reproduced with one whitespace character wrong. The "dup" case is the
other: the old text is real but not unique.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]


def read_lines(path):
    return Path(path).read_text().splitlines()


def write_lines(path, lines, trailing=True):
    text = "\n".join(lines)
    if trailing:
        text += "\n"
    Path(path).write_text(text)


def replace_lines(lines, start, end, body):
    # start/end are 1-based inclusive.
    return lines[: start - 1] + body + lines[end:]


def perturb(line):
    """Change the leading whitespace so the old text no longer matches."""
    stripped = line.lstrip(" ")
    indent = len(line) - len(stripped)
    return (" " * (indent + 2 if indent < 4 else indent - 2)) + stripped


def case_simple(src):
    lines = read_lines(src)
    start = next(i for i, l in enumerate(lines, 1) if "pub fn apply" in l)
    old = lines[start - 1]
    new = old + " // bench"
    return {
        "name": "simple",
        "content": "\n".join(lines) + "\n",
        "start": start,
        "end": start,
        "old": old,
        "new": new,
        "expected": replace_lines(lines, start, start, [new]),
    }


def case_block(src):
    lines = read_lines(src)
    start = next(i for i, l in enumerate(lines, 1) if "pub fn apply" in l)
    end = start + 5
    old_lines = lines[start - 1 : end]
    new_lines = [l + " // bench" for l in old_lines]
    return {
        "name": "block",
        "content": "\n".join(lines) + "\n",
        "start": start,
        "end": end,
        "old": "\n".join(old_lines),
        "new": "\n".join(new_lines),
        "expected": replace_lines(lines, start, end, new_lines),
    }


def case_typo(src):
    lines = read_lines(src)
    start = next(i for i, l in enumerate(lines, 1) if "pub fn apply" in l)
    true_line = lines[start - 1]
    return {
        "name": "typo",
        "content": "\n".join(lines) + "\n",
        "start": start,
        "end": start,
        "old": perturb(true_line),
        "new": true_line + " // bench",
        "expected": replace_lines(lines, start, start, [true_line + " // bench"]),
    }


def case_dup():
    content = "fn one() {\n    let x = 1;\n}\nfn two() {\n    let x = 1;\n}\n"
    lines = content.splitlines()
    old = "    let x = 1;"
    new = "    let x = 2;"
    start = 5  # the second occurrence, which is the one the agent means
    return {
        "name": "dup",
        "content": content,
        "start": start,
        "end": start,
        "old": old,
        "new": new,
        "expected": replace_lines(lines, start, start, [new]),
    }


def case_large():
    block = [f"fn f{i}() {{ let v = {i}; }}" for i in range(10)]
    lines = []
    for i in range(500):
        lines.extend(block)
    # A unique marker at line 4000 so the edit is unambiguous.
    lines[3999] = "fn unique_marker() { let v = 42; }"
    old = lines[3999]
    new = old + " // bench"
    return {
        "name": "large",
        "content": "\n".join(lines) + "\n",
        "start": 4000,
        "end": 4000,
        "old": old,
        "new": new,
        "expected": replace_lines(lines, 4000, 4000, [new]),
        "read_range": (3998, 4002),
    }


def run(cmd, cwd, input_text=None):
    t = time.perf_counter()
    p = subprocess.run(
        cmd,
        cwd=cwd,
        input=input_text,
        text=True,
        capture_output=True,
        env={**os.environ, "ATTO_HOME": str(Path(cwd) / ".atto")},
    )
    ms = (time.perf_counter() - t) * 1000
    return p, ms


def strategy_exact(workdir, case, path):
    """pi edit semantics: exact old text, must be unique."""
    text = Path(path).read_text()
    payload = json.dumps(
        {"path": path.name, "edits": [{"oldText": case["old"], "newText": case["new"]}]}
    )
    if text.count(case["old"]) != 1:
        n = text.count(case["old"])
        return False, f"old text occurs {n} times", len(payload), 0
    Path(path).write_text(text.replace(case["old"], case["new"], 1))
    return True, "", len(payload), 0


def python_script(case, path, unique):
    guard = "assert s.count(old) == 1\n" if unique else ""
    return (
        "python3 - <<'PY'\n"
        "import pathlib\n"
        f"p = pathlib.Path({str(path.name)!r})\n"
        "s = p.read_text()\n"
        f"old = {case['old']!r}\n"
        f"new = {case['new']!r}\n"
        f"{guard}"
        "p.write_text(s.replace(old, new, 1))\n"
        "PY\n"
    )


def strategy_python(workdir, case, path, unique=True):
    script = python_script(case, path, unique)
    p, ms = run(["bash", "-c", script], workdir)
    if p.returncode != 0:
        first = (p.stderr.strip().splitlines() or ["failed"])[-1]
        return False, first, len(script), ms
    return True, "", len(script), ms


def strategy_atto(workdir, case, path, binary):
    a, b = case["read_range"] if "read_range" in case else (max(1, case["start"] - 2), case["end"] + 2)
    total = len(Path(path).read_text().splitlines())
    a, b = max(1, a), min(total, b)
    p, read_ms = run([binary, "read", path.name, f"{a}:{b}"], workdir)
    if p.returncode != 0:
        return False, p.stderr.strip(), 0, read_ms, 0
    read_bytes = len(p.stdout)
    rev = next((l.split()[1] for l in p.stdout.splitlines() if l.startswith("rev ")), None)
    if not rev:
        return False, "no rev in read output", read_bytes, read_ms, 0
    body = "\n".join("+" + l for l in str(case["new"]).split("\n"))
    plan = f"file {path.name}\nrev {rev}\nSWAP {case['start']}.={case['end']}:\n{body}\n"
    p, patch_ms = run([binary, "patch"], workdir, plan)
    if p.returncode != 0:
        return False, p.stderr.strip(), len(plan), read_ms + patch_ms, read_bytes
    return True, "", len(plan), read_ms + patch_ms, read_bytes


def strategy_atto_stat(workdir, case, path, binary):
    """The agent already has the lines from its own read; only the rev is missing."""
    p, stat_ms = run([binary, "stat", path.name], workdir)
    if p.returncode != 0:
        return False, p.stderr.strip(), 0, stat_ms, 0
    stat_bytes = len(p.stdout)
    rev = next((l.split()[1] for l in p.stdout.splitlines() if l.startswith("rev ")), None)
    if not rev:
        return False, "no rev in stat output", stat_bytes, stat_ms, stat_bytes
    body = "\n".join("+" + l for l in str(case["new"]).split("\n"))
    plan = f"file {path.name}\nrev {rev}\nSWAP {case['start']}.={case['end']}:\n{body}\n"
    p, patch_ms = run([binary, "patch"], workdir, plan)
    if p.returncode != 0:
        return False, p.stderr.strip(), len(plan), stat_ms + patch_ms, stat_bytes
    return True, "", len(plan), stat_ms + patch_ms, stat_bytes


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--atto", default=str(REPO / "atto"))
    args = ap.parse_args()

    binary = str(Path(args.atto).resolve())
    if not Path(binary).exists():
        sys.exit(f"atto binary not found at {binary}; run: go build -o atto .")

    editor = REPO / "bench/fixtures/sample.rs"
    cases = [case_simple(editor), case_block(editor), case_typo(editor), case_dup(), case_large()]

    strategies = ["exact", "python", "python-first", "atto", "atto-stat"]
    rows = []
    for case in cases:
        for strategy in strategies:
            workdir = tempfile.mkdtemp(prefix="atto-bench-")
            try:
                path = Path(workdir) / "f.rs"
                path.write_text(case["content"])
                read_bytes = 0
                if strategy == "exact":
                    ok, err, payload, ms = strategy_exact(workdir, case, path)
                elif strategy == "python":
                    ok, err, payload, ms = strategy_python(workdir, case, path, unique=True)
                elif strategy == "python-first":
                    ok, err, payload, ms = strategy_python(workdir, case, path, unique=False)
                elif strategy == "atto-stat":
                    ok, err, payload, ms, read_bytes = strategy_atto_stat(workdir, case, path, binary)
                else:
                    ok, err, payload, ms, read_bytes = strategy_atto(workdir, case, path, binary)
                got = path.read_text().splitlines()
                if ok and got != case["expected"]:
                    ok = False
                    err = "landed in the wrong place"
                rows.append((case["name"], strategy, ok, payload, read_bytes, ms, err))
            finally:
                shutil.rmtree(workdir, ignore_errors=True)

    print("| case | strategy | ok | payload B | read B | ms | note |")
    print("|---|---|---|---|---|---|---|")
    for name, strategy, ok, payload, read_bytes, ms, err in rows:
        note = "" if ok else err.replace("|", "/").replace("\n", " ")[:90]
        print(f"| {name} | {strategy} | {'yes' if ok else 'NO'} | {payload} | {read_bytes} | {ms:.1f} | {note} |")

    print("\nsummary")
    for strategy in strategies:
        sub = [r for r in rows if r[1] == strategy]
        ok = sum(1 for r in sub if r[2])
        payload = sum(r[3] for r in sub)
        read = sum(r[4] for r in sub)
        ms = sum(r[5] for r in sub)
        print(f"  {strategy:14s} {ok}/{len(sub)} ok  edit {payload:6d} B  read {read:6d} B  {ms:6.1f} ms")


if __name__ == "__main__":
    main()
