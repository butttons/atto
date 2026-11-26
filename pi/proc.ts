/**
 * atto process plumbing, kept out of the tool definitions so it can be tested
 * without a model. This is the one place a dead child can take pi down with it.
 */

import { spawn } from "node:child_process";

export type AttoResult = { code: number; stdout: string; stderr: string };

function attoBin(): string {
  return process.env.ATTO_BIN || "atto";
}

export function runAtto(args: string[], cwd: string, input?: string): Promise<AttoResult> {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(attoBin(), args, { cwd, env: process.env });
    let stdout = "";
    let stderr = "";
    const timer = setTimeout(() => child.kill("SIGKILL"), 15_000);
    child.stdout.on("data", (d) => (stdout += d.toString()));
    child.stderr.on("data", (d) => (stderr += d.toString()));
    child.on("error", (err) => {
      clearTimeout(timer);
      reject(
        new Error(
          `atto: ${err.message}. Set ATTO_BIN to the binary, or put it on PATH.`,
        ),
      );
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      resolvePromise({ code: code ?? 1, stdout, stderr });
    });
    // atto read, find and stat never touch stdin, so this write can land on a
    // pipe the child already closed. That EPIPE is emitted on child.stdin, and
    // an unhandled 'error' on a stream is an uncaught exception: it killed the
    // whole pi process on the exe VMs, not just the tool call. end(undefined)
    // writes nothing, and the listener makes the remaining writes harmless.
    child.stdin.on("error", () => {});
    child.stdin.end(input);
  });
}
