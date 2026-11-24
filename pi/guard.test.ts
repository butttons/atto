/**
 * Regression tests for the shell-edit guard.
 *
 * Run: node --experimental-strip-types --test guard.test.ts
 *
 * The "allowed" cases are all real false positives that blocked legitimate
 * work; the "blocked" cases are the bypasses the guard exists for.
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { isShellEdit, type GuardEnv } from "./guard.ts";

const REPO = "/Users/someone/project";

const env: GuardEnv = {
  cwd: REPO,
  scratchRoots: [tmpdir(), "/tmp", "/var/tmp"],
  resolvePath: resolve,
};

const allowed = [
  ["scratch redirect after cd", "cd /tmp/atto-verify && echo hello > out.log"],
  ["scratch redirect via relative cd", "cd /tmp && cd probe && cargo install x > build.log 2>&1"],
  ["absolute scratch redirect", "go build > /tmp/build.log 2>&1"],
  ["stderr merge", "cargo build 2>&1 | tail -5"],
  ["dev null", "echo hi > /dev/null 2>&1"],
  ["target dir", "cargo build 2> target/log.txt"],
  ["arrow function", "node -e 'const f = (n) => n + 1'"],
  ["unquoted arrow", "rg -n => src/"],
  ["greater-equal", "awk '$1 >= 3' file"],
  ["quoted redirect text", 'rg "a > b" file'],
  ["plain read", "git log --oneline | head -5"],
  ["python -c read only", "python3 -c \"print(open('f').read())\""],
  ["python -c counting", "python3 -c \"s=open('f').read(); print(s.count('a'))\""],
  // A read path containing a "w" is not a write mode; this blocked a real check.
  ["python -c read of a workflows path", `python3 -c "print(open('/repo/.github/workflows/x.yml').read())"`],
  ["python -c read of a workspace path", `python3 -c "print(open('/w/work/build.log').read())"`],
  // A subshell cd was invisible, so its redirect looked like a repo write.
  ["subshell cd scratch redirect", "(cd /tmp/reltest && sha256sum ./*.tar.gz > SHA256SUMS)"],
  // A cd after a newline was invisible too; multi-line commands are normal.
  ["newline cd then scratch write", "printf 'a\\n' > ab.txt\ncd /tmp/dt && printf 'b\\n' > note.txt"],
  // Deliberately allowed: a program's text is not classified. See what this
  // does not stop, in pi/README.md. Blocking these cost more than it caught.
  ["python heredoc", "python3 - <<'PY'\nprint(open('f').read())\nPY"],
  ["python -c write", "python3 -c \"open('a','w').write('x')\""],
  ["python -c pathlib write", "python3 -c \"from pathlib import Path; Path('a').write_text('x')\""],
] as const;

const blocked = [
  ["sed -i", "sed -i '' s/a/b/ main.go"],
  ["perl -pi", "perl -pi -e s/a/b/ main.go"],
  ["repo redirect", "echo hi > main.go"],
  ["repo append", `cd ${REPO} && echo hi >> README.md`],
  ["absolute escape from scratch", "cd /tmp && echo hi > /Users/someone/project/main.go"],
  ["dotdot escape from scratch", "cd /tmp && echo hi > ../../Users/someone/project/main.go"],
  ["tee", "echo x | tee main.go"],
  ["git apply", "git apply p.diff"],
  ["non-scratch relative", "echo hi > build/out.bin"],
] as const;

for (const [name, command] of allowed) {
  test(`allowed: ${name}`, () => {
    assert.equal(isShellEdit(command, env), false, command);
  });
}

for (const [name, command] of blocked) {
  test(`blocked: ${name}`, () => {
    assert.equal(isShellEdit(command, env), true, command);
  });
}

test("escape hatch disables the guard", () => {
  assert.equal(isShellEdit("python3 - <<'PY'", { ...env, allowShellEdits: true }), false);
});
