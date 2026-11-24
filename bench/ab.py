#!/usr/bin/env python3
"""Live A/B of the same tasks, same model, with and without the atto gate.

    python3 bench/ab.py --runs 3

Condition A (stock) gets read, bash, edit, write.
Condition B (atto)  gets read, bash, atto_read, atto_find, atto_edit, atto_undo.

Success is the file matching the expected bytes, not the model saying it is done.
Each run gets a fresh directory, a fresh session, and no context files, skills or
MCP, so the two conditions differ only in the editing tools.

Condition A must not see the extension. pi installs whatever is in settings on
startup, so moving the package directory aside does not isolate anything; it is
fetched again. A throwaway config directory does, with the real auth.json linked
in so the provider still works.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]
EXTENSION = REPO / "pi/atto.ts"
CONFIG_DIR = Path(tempfile.gettempdir()) / "atto-ab-conf"

STOCK_TOOLS = "read,bash,edit,write"
ATTO_TOOLS = "read,bash,atto_read,atto_find,atto_sub,atto_edit,atto_undo"

# Things a model reaches for when it is not using the edit tool. Counted, not judged.
FALLBACK_MARKERS = ("sed -i", "perl -pi", "python", "tee ", "apply_patch", "cat >", "echo >")


@dataclass
class Task:
    tid: str
    filename: str
    content: str
    prompt: str
    expected: str


@dataclass
class Run:
    task: str
    condition: str
    attempt: int
    ok: bool
    calls: int
    call_names: list[str] = field(default_factory=list)
    tool_errors: int = 0
    blocked: int = 0
    fallbacks: int = 0
    tokens_in: int = 0
    tokens_out: int = 0
    seconds: float = 0.0
    note: str = ""


def tasks() -> list[Task]:
    """Twenty edits of the shapes an agent actually performs, small enough to
    verify by comparing bytes."""
    t: list[Task] = []

    t.append(Task(
        "replace-line-by-number", "app.rs", "alpha\nbravo\ncharlie\n",
        "In app.rs, replace line 2 with the single line BRAVO.",
        "alpha\nBRAVO\ncharlie\n"))
    t.append(Task(
        "replace-line-by-content", "conf.ini", "[core]\nretries=3\ntimeout=30\nverbose=false\n",
        "In conf.ini, change the value of retries from 3 to 5.",
        "[core]\nretries=5\ntimeout=30\nverbose=false\n"))
    t.append(Task(
        "insert-after-line", "notes.md", "# Title\n\nFirst point.\nSecond point.\n",
        "In notes.md, insert a new bullet line '- Third point.' immediately after 'Second point.'",
        "# Title\n\nFirst point.\nSecond point.\n- Third point.\n"))
    t.append(Task(
        "insert-at-top", "run.sh", "echo two\necho three\n",
        "In run.sh, add the line 'echo one' at the very top of the file.",
        "echo one\necho two\necho three\n"))
    t.append(Task(
        "append-at-end", "list.txt", "apple\nbanana\n",
        "In list.txt, append a new final line 'cherry'.",
        "apple\nbanana\ncherry\n"))
    t.append(Task(
        "delete-one-line", "servers.txt", "alpha\nbeta-stale\ngamma\n",
        "In servers.txt, delete the line 'beta-stale' and nothing else.",
        "alpha\ngamma\n"))
    t.append(Task(
        "delete-block", "cfg.yaml", "name: app\ncache: enabled\ncache_ttl: 60\ncache_size: 10\nport: 80\n",
        "In cfg.yaml, delete the three cache lines entirely, leaving name and port.",
        "name: app\nport: 80\n"))
    t.append(Task(
        "replace-block-with-line", "main.go",
        "package main\n\nfunc a() {\n\treturn\n}\n\nfunc b() {\n\treturn\n}\n",
        "In main.go, replace the entire body of func a so that it contains only the line '\tpanic(\"todo\")' instead of '\treturn'.",
        "package main\n\nfunc a() {\n\tpanic(\"todo\")\n}\n\nfunc b() {\n\treturn\n}\n"))
    t.append(Task(
        "fix-typo", "readme.md", "# Projcet\n\nThis projcet does things.\n",
        "In readme.md, fix the misspelled word 'Projcet' in the heading only, leaving the other occurrence alone.",
        "# Project\n\nThis projcet does things.\n"))
    t.append(Task(
        "rename-identifier", "util.py", "def total(a, b):\n    return a + b\n\nresult = total(1, 2)\n",
        "In util.py, rename the function total to add everywhere it appears.",
        "def add(a, b):\n    return a + b\n\nresult = add(1, 2)\n"))
    t.append(Task(
        "change-json-value", "package.json", '{\n  "name": "x",\n  "version": "1.0.0"\n}\n',
        'In package.json, change the version value from "1.0.0" to "1.1.0".',
        '{\n  "name": "x",\n  "version": "1.1.0"\n}\n'))
    t.append(Task(
        "change-struct-field", "model.go", "type User struct {\n\tID   int\n\tName string\n}\n",
        "In model.go, add a new field 'Email string' to the User struct, after the Name field. Keep it gofmt aligned.",
        "type User struct {\n\tID    int\n\tName  string\n\tEmail string\n}\n"))
    t.append(Task(
        "add-comment-above", "calc.py", "x = 1\ny = 2\nz = x + y\n",
        "In calc.py, add the comment line '# sum of both inputs' immediately above the line 'z = x + y'.",
        "x = 1\ny = 2\n# sum of both inputs\nz = x + y\n"))
    t.append(Task(
        "change-signature", "api.ts", "export function load(id) {\n  return id;\n}\n",
        "In api.ts, change the load function so it takes two parameters, id and options.",
        "export function load(id, options) {\n  return id;\n}\n"))
    t.append(Task(
        "swap-two-lines", "order.txt", "first\nsecond\nthird\n",
        "In order.txt, swap the lines 'first' and 'second' so second comes first.",
        "second\nfirst\nthird\n"))
    t.append(Task(
        "quotes-and-backslashes", "esc.txt", 'path = "C:\\\\tmp\\\\x"\nlabel = "old"\n',
        "In esc.txt, change the value of label from old to new, and make the new value the literal text: new \\ path",
        'path = "C:\\\\tmp\\\\x"\nlabel = "new \\ path"\n'))
    t.append(Task(
        "two-edits-one-file", "multi.txt", "one\ntwo\nthree\nfour\n",
        "In multi.txt, change the line 'one' to 'ONE' and the line 'four' to 'FOUR', leaving two and three alone.",
        "ONE\ntwo\nthree\nFOUR\n"))
    t.append(Task(
        "duplicate-line-disambiguation", "dup.txt", "start\nvalue\nmiddle\nvalue\nend\n",
        "In dup.txt, change only the second occurrence of the line 'value' (the one after 'middle') to 'changed'.",
        "start\nvalue\nmiddle\nchanged\nend\n"))
    t.append(Task(
        "trailing-newline-preserved", "tail.txt", "a\nb\nc\n",
        "In tail.txt, change 'c' to 'cc', and do not add or remove any other line.",
        "a\nb\ncc\n"))
    t.append(Task(
        "large-file-far-line", "big.txt",
        "".join(f"line {i}\n" for i in range(1, 401)),
        "In big.txt, change the line 'line 387' to 'line 387 changed'.",
        "".join(("line 387 changed\n" if i == 387 else f"line {i}\n") for i in range(1, 401))))

    # The failure modes atto exists for. Exact-match editing is at its weakest
    # here: an ambiguous old string, a long line to retype, numbering that shifts
    # after the first edit, a file that moves under the model, whitespace-only
    # differences, and a deletion whose neighbours must survive.
    t.append(Task(
        "third-of-three-duplicates", "dup3.txt", "a\nX\nb\nX\nc\nX\nd\n",
        "In dup3.txt, change only the third occurrence of the line 'X' (the one after 'c') to 'Y', leaving the other two alone.",
        "a\nX\nb\nX\nc\nY\nd\n"))
    t.append(Task(
        "retype-long-line", "long.py",
        'config = {"retries": 3, "timeout": 30, "verbose": False, "endpoint": "https://example.invalid/old", "name": "worker"}\n',
        'In long.py, change the endpoint URL path from /old to /new, changing nothing else on the line.',
        'config = {"retries": 3, "timeout": 30, "verbose": False, "endpoint": "https://example.invalid/new", "name": "worker"}\n'))
    t.append(Task(
        "numbering-shifts-after-insert", "seq.txt", "one\ntwo\nthree\nfour\n",
        "In seq.txt, first insert a new line 'header' at the very top. Then change the line 'three' to 'THREE'.",
        "header\none\ntwo\nTHREE\nfour\n"))
    t.append(Task(
        "file-moves-under-the-model", "drift.txt", "alpha\nbeta\ngamma\n",
        "In drift.txt: read the file, then run this bash command: echo note >> drift.txt\nThen change the line 'beta' to 'BETA'.",
        "alpha\nBETA\ngamma\nnote\n"))
    t.append(Task(
        "whitespace-only-change", "indent.txt", "a\n    b\nc\n",
        "In indent.txt, change the indentation of the line that contains only 'b' from four spaces to two spaces, changing nothing else.",
        "a\n  b\nc\n"))
    t.append(Task(
        "delete-middle-line-exactly", "splice.txt", "head\nA\nB\nC\ntail\n",
        "In splice.txt, delete only the line 'B', leaving head, A, C and tail exactly as they are.",
        "head\nA\nC\ntail\n"))

    # The shapes that used to send the model to Python: replace every copy, and
    # several scattered changes in one pass. A literal replace() is the operation
    # the tool list has to be able to express, or the model leaves for a shell.
    t.append(Task(
        "replace-every-occurrence", "q.md", "TODO\nbody\nTODO\nmore\nTODO\n",
        "In q.md, replace every occurrence of the line 'TODO' with '- [ ] task', and change nothing else.",
        "- [ ] task\nbody\n- [ ] task\nmore\n- [ ] task\n"))
    t.append(Task(
        "scattered-edits-one-pass", "svc.yaml",
        "name: old\nport: 80\nhost: a\ndebug: false\nretries: 1\n",
        "In svc.yaml make three changes: set name to 'svc', set port to 8080, and set debug to true. "
        "Change nothing else.",
        "name: svc\nport: 8080\nhost: a\ndebug: true\nretries: 1\n"))
    t.append(Task(
        "replace-multiline-block", "compose.yaml",
        'services:\n  app:\n    image: v1\n    restart: always\n    ports:\n      - "80:80"\n',
        ("In compose.yaml, replace the two lines '    image: v1' and '    restart: always' "
         "with the three lines '    image: v2', '    restart: unless-stopped' and "
         "'    pull_policy: always', in that order. Change nothing else."),
        'services:\n  app:\n    image: v2\n    restart: unless-stopped\n    pull_policy: always\n'
        '    ports:\n      - "80:80"\n'))
    return t


def parse_stream(stdout: str) -> dict:
    """Read pi's json event stream for tool calls, errors, and usage."""
    out = {"calls": 0, "names": [], "errors": 0, "blocked": 0, "fallbacks": 0, "tin": 0, "tout": 0}
    for line in stdout.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            d = json.loads(line)
        except json.JSONDecodeError:
            continue
        if d.get("type") != "message_end":
            continue
        msg = d.get("message", {})
        role = msg.get("role")
        if role == "assistant":
            for part in msg.get("content", []):
                if isinstance(part, dict) and part.get("type") == "toolCall":
                    out["calls"] += 1
                    name = part.get("name", "?")
                    out["names"].append(name)
                    blob = json.dumps(part.get("arguments", {}))
                    if name == "bash" and any(m in blob for m in FALLBACK_MARKERS):
                        out["fallbacks"] += 1
            usage = msg.get("usage") or {}
            out["tin"] += int(usage.get("input", 0)) + int(usage.get("cacheRead", 0))
            out["tout"] += int(usage.get("output", 0))
        elif role == "toolResult":
            text = "".join(p.get("text", "") for p in msg.get("content", []) if isinstance(p, dict))
            if msg.get("isError"):
                out["errors"] += 1
            if text.startswith("Blocked:"):
                out["blocked"] += 1
    return out


def run_one(task: Task, condition: str, attempt: int, args) -> Run:
    workdir = Path(tempfile.mkdtemp(prefix=f"ab-{condition}-{task.tid}-"))
    path = workdir / task.filename
    path.write_text(task.content, encoding="utf-8")

    cmd = [
        "pi", "--print", "--mode", "json", "--no-session",
        "--no-context-files", "--no-mcp", "--no-skills",
        "--provider", args.provider, "--model", args.model,
        "--tools", STOCK_TOOLS if condition == "stock" else ATTO_TOOLS,
    ]
    if condition == "atto":
        cmd += ["--extension", str(EXTENSION)]

    env = dict(os.environ)
    env["PI_CODING_AGENT_DIR"] = str(CONFIG_DIR)
    if condition == "atto":
        env["ATTO_BIN"] = str(REPO / "atto")

    started = time.time()
    note = ""
    stdout = ""
    try:
        proc = subprocess.run(
            cmd, cwd=workdir, input=task.prompt, text=True,
            capture_output=True, timeout=args.timeout, env=env,
        )
        stdout = proc.stdout
        if proc.returncode != 0:
            note = f"exit {proc.returncode}: {proc.stderr.strip().splitlines()[-1][:120] if proc.stderr.strip() else 'no stderr'}"
    except subprocess.TimeoutExpired:
        note = "timeout"

    wall = time.time() - started
    got = path.read_text(encoding="utf-8") if path.exists() else "<missing>"
    parsed = parse_stream(stdout)
    ok = got == task.expected

    if not ok and not note:
        missing = 0 if got == "<missing>" else len(task.expected.splitlines()) - len(got.splitlines())
        note = "wrong content" + (f" ({missing} lines off)" if missing else "")

    shutil.rmtree(workdir, ignore_errors=True)
    return Run(
        task=task.tid, condition=condition, attempt=attempt, ok=ok,
        calls=parsed["calls"], call_names=parsed["names"],
        tool_errors=parsed["errors"], blocked=parsed["blocked"], fallbacks=parsed["fallbacks"],
        tokens_in=parsed["tin"], tokens_out=parsed["tout"],
        seconds=round(wall, 2), note=note,
    )


def summarise(runs: list[Run], label: str) -> str:
    ok = sum(1 for r in runs if r.ok)
    calls = [r.calls for r in runs]
    secs = [r.seconds for r in runs]
    tin = sum(r.tokens_in for r in runs)
    tout = sum(r.tokens_out for r in runs)
    return "\n".join([
        f"{label}: {ok}/{len(runs)} landed",
        f"  calls   total {sum(calls)}  mean {statistics.mean(calls):.1f}  max {max(calls)}",
        f"  tokens  in {tin}  out {tout}",
        f"  time    mean {statistics.mean(secs):.1f}s  total {sum(secs):.0f}s",
        f"  tool errors {sum(r.tool_errors for r in runs)}  blocked {sum(r.blocked for r in runs)}  "
        f"fallbacks {sum(r.fallbacks for r in runs)}",
    ])


def prepare_config() -> None:
    """A config dir with nothing installed but a working provider login."""
    CONFIG_DIR.mkdir(parents=True, exist_ok=True)
    auth = CONFIG_DIR / "auth.json"
    real = Path.home() / ".pi/agent/auth.json"
    if auth.is_symlink() or auth.exists():
        auth.unlink()
    if real.exists():
        auth.symlink_to(real)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs", type=int, default=3)
    ap.add_argument("--provider", default="opencode-go")
    ap.add_argument("--model", default="glm-5.3-flash")
    ap.add_argument("--timeout", type=int, default=240)
    ap.add_argument("--tasks", default="")
    ap.add_argument("--out", default=str(REPO / "bench/ab-results.json"))
    ap.add_argument("--only", default="", help="stock or atto")
    args = ap.parse_args()

    prepare_config()

    selected = tasks()
    if args.tasks:
        want = set(args.tasks.split(","))
        selected = [t for t in selected if t.tid in want]

    conditions = ["stock", "atto"] if not args.only else [args.only]
    runs: list[Run] = []

    for condition in conditions:
        for task in selected:
            for attempt in range(1, args.runs + 1):
                r = run_one(task, condition, attempt, args)
                runs.append(r)
                mark = "ok  " if r.ok else "MISS"
                print(f"{mark} {condition:5} {task.tid:28} run{attempt} "
                      f"calls={r.calls:2} err={r.tool_errors} blk={r.blocked} "
                      f"{r.seconds:5.1f}s {r.note}", flush=True)

    print()
    for condition in conditions:
        print(summarise([r for r in runs if r.condition == condition], condition))
        print()

    per_task: dict[str, dict[str, int]] = {}
    for r in runs:
        per_task.setdefault(r.task, {}).setdefault(r.condition, 0)
        per_task[r.task][r.condition] += 1 if r.ok else 0

    Path(args.out).write_text(json.dumps({
        "provider": args.provider,
        "model": args.model,
        "runs_per_task": args.runs,
        "conditions": conditions,
        "tasks": per_task,
        "results": [r.__dict__ for r in runs],
    }, indent=2), encoding="utf-8")
    print(f"wrote {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
