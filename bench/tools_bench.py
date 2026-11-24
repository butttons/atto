#!/usr/bin/env python3
"""Compare the real editing tools on the same tasks.

Deterministic, model-free: each task names a target span and the new text.
Every adapter uses its tool's own read and its own edit call. A failure is
retried after a re-read, the way an agent would recover, and attempts are
counted. The stale case is a safe-failure test: the file changes inside the
target after the read, and the only correct outcome is a refusal with zero
writes.

Tools:
  exact         pi/Claude-Code semantics: exact old text, unique, in-process
  python        the s.replace workaround, as a real subprocess
  python-first  s.replace without a uniqueness check
  hashline      the real Rust CLI (anchors)
  snapedit      the real Node CLI (snapshot + 3-way merge)
  atto          our binary (snapshot rev + line span)
  atto-stat     our binary when the agent already has the lines

Build the external tools first; see bench/README.md. Pass their paths:
  --hashline /path/to/hashline
  --snapedit-cli /path/to/snapedit/src/cli.js
"""

import argparse
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, replace
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]

TOOLS = ["exact", "python", "python-first", "hashline", "snapedit", "atto", "atto-stat"]
SCENARIOS = ["plain", "typo", "drift", "stale", "dup", "large"]


@dataclass
class Task:
    name: str
    scenario: str
    ext: str
    content: str
    start: int
    end: int
    old: str
    new: str
    expected: str
    stale: str = ""
    drift_line: str = ""

    @property
    def lines(self):
        return self.content.splitlines()


def replace_span(lines, start, end, body):
    return lines[: start - 1] + body + lines[end:]


def perturb(text):
    first, *rest = text.split("\n")
    stripped = first.lstrip(" ")
    indent = len(first) - len(stripped)
    first = (" " * (indent + 2 if indent < 4 else indent - 2)) + stripped
    return "\n".join([first] + rest)


def pick(lines, needle, after=10):
    candidates = [
        (i, line)
        for i, line in enumerate(lines, 1)
        if i > after and needle in line and len(line.strip()) > 8
    ]
    for i, line in candidates:
        if lines.count(line) == 1:
            return i
    if candidates:
        return candidates[0][0]
    raise RuntimeError(f"no line containing {needle!r}")


def base_task(path, ext, comment, scenario):
    content = Path(path).read_text()
    lines = content.splitlines()
    start = pick(lines, "return" if ext != "py" else "def ")
    old = lines[start - 1]
    new = old + comment
    body = new.split("\n")
    expected = "\n".join(replace_span(lines, start, start, body)) + "\n"
    task = Task(
        name=f"{Path(path).stem}-{scenario}",
        scenario=scenario,
        ext=ext,
        content=content,
        start=start,
        end=start,
        old=old,
        new=new,
        expected=expected,
    )
    if scenario == "drift":
        line = "// drift" if ext != "py" else "# drift"
        task.drift_line = line
        task.expected = line + "\n" + expected
    if scenario == "stale":
        stale_lines = replace_span(lines, start, start, ["// changed by another tool"])
        task.stale = "\n".join(stale_lines) + "\n"
    return task


def dup_task():
    content = "fn one() {\n    let x = 1;\n}\nfn two() {\n    let x = 1;\n}\n"
    lines = content.splitlines()
    start = 5
    old = "    let x = 1;"
    new = "    let x = 2;"
    expected = "\n".join(replace_span(lines, start, start, [new])) + "\n"
    return Task("dup", "dup", "rs", content, start, start, old, new, expected)


def large_task():
    block = [f"fn f{i}() {{ let v = {i}; }}" for i in range(10)]
    lines = []
    for _ in range(500):
        lines.extend(block)
    lines[3999] = "fn unique_marker() { let v = 42; }"
    content = "\n".join(lines) + "\n"
    start = 4000
    old = lines[start - 1]
    new = old + " // bench"
    expected = "\n".join(replace_span(lines, start, start, [new])) + "\n"
    return Task("large", "large", "rs", content, start, start, old, new, expected)


def make_tasks():
    tasks = []
    tasks.append(base_task(REPO / "bench/fixtures/sample.rs", "rs", " // bench", "plain"))
    tasks.append(base_task(REPO / "bench/fixtures/sample.rs", "rs", " // bench", "typo"))
    tasks.append(base_task(REPO / "bench/fixtures/sample.rs", "rs", " // bench", "drift"))
    tasks.append(base_task(REPO / "bench/fixtures/sample.rs", "rs", " // bench", "stale"))
    tasks.append(base_task(REPO / "main.go", "go", " // bench", "plain"))
    tasks.append(base_task(REPO / "main.go", "go", " // bench", "typo"))
    tasks.append(base_task(REPO / "main.go", "go", " // bench", "drift"))
    tasks.append(base_task(REPO / "bench/bench.py", "py", "  # bench", "plain"))
    tasks.append(base_task(REPO / "bench/bench.py", "py", "  # bench", "typo"))
    tasks.append(base_task(REPO / "bench/bench.py", "py", "  # bench", "drift"))
    tasks.append(dup_task())
    tasks.append(large_task())
    return tasks


def run(cmd, cwd, env_extra=None, input_text=None):
    env = {**os.environ, **(env_extra or {})}
    t = time.perf_counter()
    p = subprocess.run(
        cmd, cwd=cwd, input=input_text, text=True, capture_output=True, env=env
    )
    return p, (time.perf_counter() - t) * 1000


def read_text(path):
    return Path(path).read_text()


def write_text(path, text):
    Path(path).write_text(text)


def target_line(task, attempt):
    # A drift inserts one line at the head. The first read predates it, so the
    # first attempt still speaks in the old snapshot's coordinates; a retry
    # re-reads the drifted file and shifts by one.
    return task.start + (1 if task.scenario == "drift" and attempt > 1 else 0)


def old_for_attempt(task, attempt):
    if task.scenario == "typo" and attempt == 1:
        return perturb(task.old)
    return task.old


def context_window(lines, start, end, old_target, new_target, radius):
    a = max(0, start - 1 - radius)
    b = min(len(lines), end + radius)
    old_block = lines[a : start - 1] + old_target + lines[end:b]
    new_block = lines[a : start - 1] + new_target + lines[end:b]
    return "\n".join(old_block), "\n".join(new_block)


def choose_exact(task, attempt, lines):
    """The block the agent would send, with context if the block is not unique.

    On the typo attempt the context extension keeps the mistyped target line:
    adding context must not silently repair the reproduction error.
    """
    old_target = old_for_attempt(task, attempt).split("\n")
    new_target = task.new.split("\n")
    old = "\n".join(old_target)
    new = task.new
    if "\n".join(lines).count(old) == 1:
        return old, new
    start = target_line(task, attempt)
    end = start + (task.end - task.start)
    for radius in (1, 2, 3):
        o, n = context_window(lines, start, end, old_target, new_target, radius)
        if "\n".join(lines).count(o) == 1:
            return o, n
    return old, new


# --------------------------------------------------------------------------
# adapters


def prepare_exact(workdir, task, attempt):
    path = Path(workdir) / f"f.{task.ext}"
    line = target_line(task, attempt)
    lines = read_text(path).splitlines()
    lo = max(1, line - 3)
    hi = min(len(lines), line + (task.end - task.start) + 3)
    return {"read": "\n".join(lines[lo - 1 : hi])}, len("\n".join(lines[lo - 1 : hi]))


def edit_exact(workdir, task, attempt, state):
    path = Path(workdir) / f"f.{task.ext}"
    text = read_text(path)
    old, new = choose_exact(task, attempt, text.splitlines())
    payload = json.dumps(
        {"path": path.name, "edits": [{"oldText": old, "newText": new}]}
    )
    if text.count(old) != 1:
        return False, len(payload), 0.0, f"old text occurs {text.count(old)} times"
    write_text(path, text.replace(old, new, 1))
    return True, len(payload), 0.0, ""


def python_script(task, attempt, path, unique=True):
    old, new = choose_exact(task, attempt, read_text(path).splitlines())
    guard = "assert s.count(old) == 1\n" if unique else ""
    return (
        "import pathlib\n"
        f"p = pathlib.Path({path.name!r})\n"
        "s = p.read_text()\n"
        f"old = {old!r}\n"
        f"new = {new!r}\n"
        f"{guard}"
        "p.write_text(s.replace(old, new, 1))\n"
    )


def prepare_python(workdir, task, attempt):
    return prepare_exact(workdir, task, attempt)


def edit_python(workdir, task, attempt, state, unique=True):
    path = Path(workdir) / f"f.{task.ext}"
    script = python_script(task, attempt, path, unique=unique)
    p, ms = run([sys.executable, "-c", script], workdir)
    if p.returncode != 0:
        tail = (p.stderr.strip().splitlines() or ["failed"])[-1]
        return False, len(script), ms, tail
    return True, len(script), ms, ""


def prepare_hashline(workdir, task, attempt, binary):
    path = Path(workdir) / f"f.{task.ext}"
    p, ms = run([binary, "read", path.name], workdir)
    hashes = {}
    for line in p.stdout.splitlines():
        m = re.match(r"^(\d+):([0-9a-f]+)\|", line)
        if m:
            hashes[int(m.group(1))] = m.group(2)
    return {"hashes": hashes, "read": len(p.stdout), "ms": ms, "rc": p.returncode, "out": p.stdout}


def edit_hashline(workdir, task, attempt, state, binary):
    if state["rc"] != 0:
        return False, 0, 0.0, state["out"].strip()[:200]
    line = target_line(task, attempt)
    end = line + (task.end - task.start)
    if line not in state["hashes"]:
        return False, 0, 0.0, f"no anchor for line {line}"
    head = f"SWAP {line}" + (f"..{end}" if end > line else "") + f":{state['hashes'][line]}"
    body = "\n".join("+" + l for l in task.new.split("\n"))
    patch = f"{head}:\n{body}\n"
    p, ms = run([binary, "patch", f"f.{task.ext}", patch], workdir)
    if p.returncode != 0:
        return False, len(patch), ms, (p.stderr or p.stdout).strip().splitlines()[-1][:200]
    return True, len(patch), ms, ""


def prepare_snapedit(workdir, task, attempt, cli):
    path = Path(workdir) / f"f.{task.ext}"
    line = target_line(task, attempt)
    page = max(0, (line - 1) // 300)
    if cli.endswith(".js"):
        cmd = ["node", cli, "read", path.name, "--page", str(page), "--page-size", "300"]
    else:
        cmd = [cli, "read", path.name, "--page", str(page), "--page-size", "300"]
    p, ms = run(cmd, workdir, {"SNAPEDIT_DIR": str(Path(workdir) / ".snapedit")})
    try:
        data = json.loads(p.stdout)
    except Exception:
        return {"blob": None, "read": len(p.stdout), "ms": ms, "rc": p.returncode, "out": p.stdout}
    return {"blob": data.get("blob_id"), "read": len(p.stdout), "ms": ms, "rc": p.returncode, "out": p.stdout}


def edit_snapedit(workdir, task, attempt, state, cli):
    if not state["blob"]:
        return False, 0, 0.0, state["out"].strip()[:200]
    line = target_line(task, attempt)
    end = line + (task.end - task.start)
    path = f"f.{task.ext}"
    if cli.endswith(".js"):
        cmd = ["node", cli, "apply", path, state["blob"], str(line), str(end), "--content", task.new]
    else:
        cmd = [cli, "apply", path, state["blob"], str(line), str(end), "--content", task.new]
    p, ms = run(cmd, workdir, {"SNAPEDIT_DIR": str(Path(workdir) / ".snapedit")})
    payload = len(state["blob"]) + len(task.new) + 80
    try:
        data = json.loads(p.stdout)
    except Exception:
        data = {}
    if p.returncode != 0 or not data.get("ok"):
        note = data.get("hint") or data.get("kind") or (p.stdout or p.stderr).strip()
        return False, payload, ms, note.splitlines()[0][:200] if note else "refused"
    return True, payload, ms, ""


def prepare_atto(workdir, task, attempt, binary, stat_only):
    path = Path(workdir) / f"f.{task.ext}"
    line = target_line(task, attempt)
    if stat_only:
        p, ms = run([binary, "stat", path.name], workdir, {"ATTO_HOME": str(Path(workdir) / ".atto")})
    else:
        lo = max(1, line - 2)
        hi = min(len(read_text(path).splitlines()), line + (task.end - task.start) + 2)
        p, ms = run([binary, "read", path.name, f"{lo}:{hi}"], workdir, {"ATTO_HOME": str(Path(workdir) / ".atto")})
    rev = next((l.split()[1] for l in p.stdout.splitlines() if l.startswith("rev ")), None)
    return {"rev": rev, "read": len(p.stdout), "ms": ms, "rc": p.returncode, "out": p.stdout}


def edit_atto(workdir, task, attempt, state, binary):
    if not state["rev"]:
        return False, 0, 0.0, state["out"].strip()[:200]
    line = target_line(task, attempt)
    end = line + (task.end - task.start)
    body = "\n".join("+" + l for l in task.new.split("\n"))
    plan = f"file f.{task.ext}\nrev {state['rev']}\nSWAP {line}.={end}:\n{body}\n"
    p, ms = run([binary, "patch"], workdir, {"ATTO_HOME": str(Path(workdir) / ".atto")}, plan)
    if p.returncode != 0:
        return False, len(plan), ms, (p.stderr or p.stdout).strip().splitlines()[-1][:200]
    return True, len(plan), ms, ""


# --------------------------------------------------------------------------
# driver


def evaluate(tool, task, workdir, opts):
    path = Path(workdir) / f"f.{task.ext}"
    attempts = 0
    read_bytes = edit_bytes = 0
    ms = 0.0
    last_err = ""
    max_attempts = 1 if task.scenario == "stale" else 3
    for attempt in range(1, max_attempts + 1):
        attempts = attempt
        # A drift is an external change: it persists across a retry. Every
        # other failed attempt is reset, the way an agent would restore or
        # re-read a clean file.
        base = task.content
        if task.scenario == "drift" and attempt > 1:
            base = task.drift_line + "\n" + task.content
        write_text(path, base)
        if tool == "exact":
            state, rb = prepare_exact(workdir, task, attempt)
            if task.scenario == "drift" and attempt == 1:
                write_text(path, task.drift_line + "\n" + read_text(path))
            if task.scenario == "stale":
                write_text(path, task.stale)
            ok, eb, ems, err = edit_exact(workdir, task, attempt, state)
        elif tool in ("python", "python-first"):
            state, rb = prepare_python(workdir, task, attempt)
            if task.scenario == "drift" and attempt == 1:
                write_text(path, task.drift_line + "\n" + read_text(path))
            if task.scenario == "stale":
                write_text(path, task.stale)
            ok, eb, ems, err = edit_python(workdir, task, attempt, state, unique=(tool == "python"))
        elif tool == "hashline":
            state = prepare_hashline(workdir, task, attempt, opts.hashline)
            rb, read_ms = state["read"], state["ms"]
            if task.scenario == "drift" and attempt == 1:
                write_text(path, task.drift_line + "\n" + read_text(path))
            if task.scenario == "stale":
                write_text(path, task.stale)
            ok, eb, ems, err = edit_hashline(workdir, task, attempt, state, opts.hashline)
            ems += read_ms
        elif tool == "snapedit":
            state = prepare_snapedit(workdir, task, attempt, opts.snapedit_cli)
            rb, read_ms = state["read"], state["ms"]
            if task.scenario == "drift" and attempt == 1:
                write_text(path, task.drift_line + "\n" + read_text(path))
            if task.scenario == "stale":
                write_text(path, task.stale)
            ok, eb, ems, err = edit_snapedit(workdir, task, attempt, state, opts.snapedit_cli)
            ems += read_ms
        else:
            stat_only = tool == "atto-stat"
            state = prepare_atto(workdir, task, attempt, opts.atto, stat_only)
            rb, read_ms = state["read"], state["ms"]
            if task.scenario == "drift" and attempt == 1:
                write_text(path, task.drift_line + "\n" + read_text(path))
            if task.scenario == "stale":
                write_text(path, task.stale)
            ok, eb, ems, err = edit_atto(workdir, task, attempt, state, opts.atto)
            ems += read_ms
        read_bytes += rb
        edit_bytes += eb
        ms += ems
        last_err = err
        got = read_text(path)
        if task.scenario == "stale":
            return {
                "ok": False,
                "safe": got == task.stale,
                "attempts": attempt,
                "read": read_bytes,
                "edit": edit_bytes,
                "ms": ms,
                "err": err or "refused",
            }
        if got == task.expected:
            return {
                "ok": True,
                "safe": True,
                "attempts": attempt,
                "read": read_bytes,
                "edit": edit_bytes,
                "ms": ms,
                "err": "",
            }
        last_err = err or "landed in the wrong place"
    return {
        "ok": False,
        "safe": False,
        "attempts": attempts,
        "read": read_bytes,
        "edit": edit_bytes,
        "ms": ms,
        "err": last_err,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--atto", default=str(REPO / "atto"))
    ap.add_argument("--hashline", default="hashline")
    ap.add_argument("--snapedit-cli", default="snapedit")
    ap.add_argument("--tools", default=",".join(TOOLS))
    ap.add_argument("--scenarios", default=",".join(SCENARIOS))
    args = ap.parse_args()

    if not Path(args.atto).exists():
        sys.exit(f"atto binary not found: {args.atto}\ngo build -o atto .")
    selected = set(args.tools.split(","))
    if "hashline" in selected and shutil.which(args.hashline) is None and not Path(args.hashline).exists():
        sys.exit(f"hashline not found: {args.hashline}")
    if "snapedit" in selected and shutil.which(args.snapedit_cli) is None and not Path(args.snapedit_cli).exists():
        sys.exit(f"snapedit not found: {args.snapedit_cli} (pass --snapedit-cli)")

    tools = args.tools.split(",")
    scenarios = set(args.scenarios.split(","))
    tasks = [t for t in make_tasks() if t.scenario in scenarios]

    rows = []
    for task in tasks:
        for tool in tools:
            workdir = tempfile.mkdtemp(prefix="atto-cmp-")
            try:
                r = evaluate(tool, task, workdir, args)
                rows.append((task, tool, r))
            finally:
                shutil.rmtree(workdir, ignore_errors=True)

    print("| task | tool | result | attempts | read B | edit B | ms | note |")
    print("|---|---|---|---|---|---|---|---|")
    for task, tool, r in rows:
        if task.scenario == "stale":
            result = "safe" if r["safe"] else "WROTE"
        else:
            result = "ok" if r["ok"] else "FAIL"
        note = "" if r["ok"] or (task.scenario == "stale" and r["safe"]) else r["err"].replace("|", "/")
        print(
            f"| {task.name} | {tool} | {result} | {r['attempts']} | {r['read']} | {r['edit']} | {r['ms']:.1f} | {note[:90]} |"
        )

    print("\nsummary")
    for tool in tools:
        sub = [(t, r) for t, x, r in rows if x == tool]
        landed = sum(1 for t, r in sub if t.scenario != "stale" and r["ok"])
        total = sum(1 for t, r in sub if t.scenario != "stale")
        safe = sum(1 for t, r in sub if t.scenario == "stale" and r["safe"])
        stale = sum(1 for t, r in sub if t.scenario == "stale")
        attempts = sum(r["attempts"] for t, r in sub)
        rb = sum(r["read"] for t, r in sub)
        eb = sum(r["edit"] for t, r in sub)
        ms = sum(r["ms"] for t, r in sub)
        print(
            f"  {tool:12s} landed {landed}/{total}  safe {safe}/{stale}  attempts {attempts:3d}  read {rb:7d} B  edit {eb:7d} B  {ms:7.1f} ms"
        )


if __name__ == "__main__":
    main()
