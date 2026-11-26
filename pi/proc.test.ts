/**
 * Regression tests for the atto process plumbing.
 *
 * Run: node --experimental-strip-types --test proc.test.ts
 *
 * The case that matters is the child that dies before the parent stops writing
 * to it: `atto read` never touches stdin, and on the exe VMs that race raised
 * EPIPE on child.stdin, which Node turns into an uncaught exception. It killed
 * the whole pi process, not just the tool call.
 */

import assert from "node:assert/strict";
import { test } from "node:test";
import { runAtto } from "./proc.ts";

// A write larger than the pipe buffer is what makes the failure deterministic:
// a zero-length write can be dropped silently, a full one cannot.
const PLAN = "x".repeat(1024 * 1024);

function withBinary(bin: string, fn: () => Promise<void>): Promise<void> {
  const previous = process.env.ATTO_BIN;
  process.env.ATTO_BIN = bin;
  return fn().finally(() => {
    if (previous === undefined) delete process.env.ATTO_BIN;
    else process.env.ATTO_BIN = previous;
  });
}

test("a child that closes stdin before the write lands cannot kill the process", async () => {
  await withBinary("/bin/sh", async () => {
    const result = await runAtto(["-c", "exec 0<&-; sleep 0.2"], process.cwd(), PLAN);
    assert.equal(result.code, 0);
  });
});

test("no input writes nothing, so a read never races the child's exit", async () => {
  await withBinary("/bin/sh", async () => {
    const result = await runAtto(["-c", "exec 0<&-; sleep 0.2"], process.cwd());
    assert.equal(result.code, 0);
  });
});

test("a missing binary is a rejected promise, not a dead process", async () => {
  await withBinary("/nonexistent/atto-does-not-exist", async () => {
    await assert.rejects(() => runAtto(["read", "x.ts"], process.cwd()), /Set ATTO_BIN|ENOENT/);
  });
});

test("stdout and stderr come back whole", async () => {
  await withBinary("/bin/sh", async () => {
    const result = await runAtto(["-c", "printf 'out'; printf 'err' >&2"], process.cwd());
    assert.deepEqual(result, { code: 0, stdout: "out", stderr: "err" });
  });
});
