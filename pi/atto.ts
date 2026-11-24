/**
 * atto for pi — make the agent edit through atto, not through edit/Python/sed.
 *
 * What it does:
 *   - registers atto_read, atto_find, atto_edit, atto_undo
 *   - removes the built-in `edit` and `apply_patch` from the model's tool set
 *   - blocks `edit` and `apply_patch` if something still reaches them
 *   - blocks `write` when the file already exists
 *   - blocks shell commands that edit files (python heredocs, sed -i, tee, redirects)
 *   - records every block and every atto edit so the fallback rate is measurable
 *
 * The guard is the point. A prompt guideline is a request; this is a gate.
 *
 * Load:
 *   pi --extension /path/to/atto/pi/atto.ts
 *   # or copy/symlink into ~/.pi/agent/extensions/ and add it to settings.json
 *
 * Binary:
 *   ATTO_BIN=/abs/path/to/atto   (default: `atto` on PATH)
 *
 * Escape hatch:
 *   ATTO_ALLOW_SHELL_EDITS=1    (do not use for normal agent work)
 */

import { spawn } from "node:child_process";
import { appendFileSync, existsSync, mkdirSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { Type } from "@earendil-works/pi-ai";
import { withFileMutationQueue, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { isShellEdit } from "./guard.ts";
import { check, describe, parseMatches, parseShown, parseSpans, wholeFile, type Grant } from "./windows.ts";

type AttoResult = { code: number; stdout: string; stderr: string };

type Change = { start: number; end: number; text: string };

const stats = { reads: 0, edits: 0, undos: 0, blocked: 0, failed: 0, refused: 0 };

// Lines this session has shown the model, per path. A rev proves the file has
// not moved; it does not prove the model read the lines it is changing. This
// closes that gap, and it is per-session on purpose: a fresh session has shown
// nothing, so it must read before it edits.
const grants = new Map<string, Grant>();

function grantWindow(key: string, rev: string, regions: { from: number; to: number }[]): void {
  const existing = grants.get(key);
  if (existing && existing.rev === rev) {
    grants.set(key, { rev, regions: [...existing.regions, ...regions] });
    return;
  }
  grants.set(key, { rev, regions });
}

function attoBin(): string {
  return process.env.ATTO_BIN || "atto";
}

function stateFile(): string {
  return join(process.env.ATTO_HOME || join(homedir(), ".local", "state", "atto"), "pi.jsonl");
}

function log(entry: Record<string, unknown>): void {
  try {
    mkdirSync(join(stateFile(), ".."), { recursive: true });
    appendFileSync(stateFile(), JSON.stringify({ time: new Date().toISOString(), ...entry }) + "\n");
  } catch {
    // Metrics are best-effort; they must never block an edit.
  }
}

function runAtto(args: string[], cwd: string, input?: string): Promise<AttoResult> {
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
    child.stdin.end(input ?? "");
  });
}

function bodyLines(text: string): string[] {
  const lines = text.split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  return lines;
}

// atto's plan is line-oriented and literal. A leading + on content is escaped
// by the plan's own rule: the tool strips exactly one +.
function planFor(path: string, rev: string, changes: Change[]): string {
  let plan = `file ${path}\nrev ${rev}\n`;
  for (const change of changes) {
    if (!Number.isInteger(change.start) || change.start < 1) {
      throw new Error(`atto_edit: start must be a line >= 1 (got ${change.start})`);
    }
    if (!Number.isInteger(change.end) || change.end < change.start - 1) {
      throw new Error(`atto_edit: end must be >= start-1 (got start=${change.start}, end=${change.end})`);
    }
    if (change.end < change.start) {
      const lines = bodyLines(change.text);
      if (lines.length > 0) {
        plan += `INS.PRE ${change.start}:\n` + lines.map((l) => `+${l}\n`).join("");
      }
      continue;
    }
    if (change.text === "") {
      plan += `DEL ${change.start}.=${change.end}\n`;
      continue;
    }
    const lines = bodyLines(change.text);
    plan += `SWAP ${change.start}.=${change.end}:\n` + lines.map((l) => `+${l}\n`).join("");
  }
  return plan;
}

function parseNewRev(stdout: string): string | undefined {
  const m = stdout.match(/^rev\s+(\S+)\s+->\s+(\S+)/m);
  return m ? m[2] : undefined;
}

function parseRev(stdout: string): string | undefined {
  const m = stdout.match(/^rev\s+(\S+)/m);
  return m ? m[1] : undefined;
}

// A refused edit is the model's next prompt, so it says what was seen, what was
// not, and the one call that fixes it.
function refusal(
  path: string,
  rev: string,
  v: { reason: "rev" | "region"; start: number; end: number; regions: { from: number; to: number }[] },
): string {
  const head = `atto_edit: ${path}: nothing was written.`;
  const fix = "Read the range you are changing with atto_read, or locate the text with atto_find, then retry with the rev it returns.";
  if (v.reason === "rev") {
    return `${head} rev ${rev} was not issued for this file in this session, or your last edit replaced it. ${fix}`;
  }
  const where = v.end < v.start ? `the insertion before line ${v.start}` : `lines ${v.start}..${v.end}`;
  return `${head} ${where} had not been shown to you; this session showed ${describe(v.regions)}. ${fix}`;
}

// The regexes live in guard.ts so they can be unit-tested without a model.
// Both false-positive classes that blocked real work came from this predicate.
function shellEdit(command: string, cwd: string): boolean {
  return isShellEdit(command, {
    cwd,
    scratchRoots: [tmpdir(), "/tmp", "/var/tmp"],
    resolvePath: resolve,
    allowShellEdits: process.env.ATTO_ALLOW_SHELL_EDITS === "1",
  });
}

function block(reason: string): { block: true; reason: string } {
  stats.blocked++;
  log({ kind: "block", reason });
  return { block: true, reason };
}

export default function attoExtension(pi: ExtensionAPI) {
  pi.registerTool({
    name: "atto_read",
    label: "atto read",
    description:
      "Read a file with 1-based line numbers and a snapshot rev. Call this before atto_edit; the rev is the guard that makes the edit precise.",
    promptSnippet: "Read a file with line numbers and a snapshot rev (atto_read)",
    promptGuidelines: [
      "Call atto_read on the region you will change; keep its rev for atto_edit.",
      "Use atto_read, not read, when you are about to edit.",
      "If you know the text you are changing, atto_find returns the line numbers without you having to count.",
    ],
    parameters: Type.Object({
      path: Type.String({ description: "File to read, relative to the workspace or absolute" }),
      from: Type.Optional(Type.Number({ description: "1-based first line (inclusive)" })),
      to: Type.Optional(Type.Number({ description: "1-based last line (inclusive)" })),
    }),
    async execute(_id, params, _signal, _update, ctx) {
      if (params.to !== undefined && params.from === undefined) {
        throw new Error("atto_read: pass from with to, or neither");
      }
      const args = ["read", params.path];
      if (params.from !== undefined) {
        args.push(`${params.from}:${params.to ?? params.from}`);
      }
      const result = await runAtto(args, ctx.cwd);
      if (result.code !== 0) throw new Error(result.stderr || result.stdout || "atto read failed");
      stats.reads++;
      const rev = parseRev(result.stdout);
      if (rev) {
        grantWindow(
          resolve(ctx.cwd, params.path),
          rev,
          parseShown(result.stdout, { from: params.from, to: params.to }),
        );
      }
      log({ kind: "read", path: params.path, bytes: result.stdout.length });
      return {
        content: [{ type: "text", text: result.stdout }],
        details: { path: params.path, rev },
      };
    },
  });

  pi.registerTool({
    name: "atto_find",
    label: "atto find",
    description:
      "Find literal text in a file and return the matching numbered lines plus a snapshot rev. Use it to get the line numbers for a change instead of counting lines yourself.",
    promptSnippet: "Find text and get line numbers plus a rev (atto_find)",
    promptGuidelines: [
      "Use atto_find to get line numbers for a change; never count lines from memory, offsets, or an earlier edit.",
      "atto_find matches literally and case-sensitively; the match count tells you if the text is ambiguous.",
      "The rev atto_find returns works with atto_edit.",
    ],
    parameters: Type.Object({
      path: Type.String({ description: "File to search, relative to the workspace or absolute" }),
      text: Type.String({ description: "Literal text to find, case-sensitive. Not a regex." }),
    }),
    async execute(_id, params, _signal, _update, ctx) {
      const result = await runAtto(["find", params.path, params.text], ctx.cwd);
      if (result.code !== 0) throw new Error(result.stderr || result.stdout || "atto find failed");
      stats.reads++;
      const rev = parseRev(result.stdout);
      if (rev) grantWindow(resolve(ctx.cwd, params.path), rev, parseMatches(result.stdout));
      log({ kind: "find", path: params.path, bytes: result.stdout.length });
      return {
        content: [{ type: "text", text: result.stdout }],
        details: { path: params.path, rev },
      };
    },
  });

  pi.registerTool({
    name: "atto_edit",
    label: "atto edit",
    description:
      "Apply one or more line-addressed changes to an existing file. Change {start,end} are 1-based inclusive; end=start-1 inserts before start; empty text deletes. All changes are one atomic transaction. Returns a diff, a new rev, and the new line spans.",
    promptSnippet: "Apply line-addressed changes to an existing file (atto_edit)",
    promptGuidelines: [
      "Never use edit, write, Python, sed, or perl to change an existing file; use atto_edit.",
      "Pass the rev from atto_read or atto_find. If atto_edit reports a conflict, re-read and retry with the new rev.",
      "You may only change lines this session has shown you: read the range, or find the text, then pass that rev.",
      "Put every independent change to a file in one atto_edit call; it is one transaction and one undo step.",
      "An empty text deletes start..end. To insert before line N, set start=N and end=N-1.",
      "A note about identical adjacent lines means you duplicated a block; fix it in the next call.",
    ],
    parameters: Type.Object({
      path: Type.String({ description: "Existing file to change" }),
      rev: Type.String({ description: "Snapshot rev returned by atto_read or atto_find" }),
      changes: Type.Array(
        Type.Object({
          start: Type.Number({ description: "1-based first line, inclusive" }),
          end: Type.Number({ description: "1-based last line, inclusive; start-1 inserts before start" }),
          text: Type.String({ description: "Replacement text. Empty deletes when end >= start." }),
        }),
        { minItems: 1 },
      ),
    }),
    async execute(_id, params, _signal, _update, ctx) {
      const key = resolve(ctx.cwd, params.path);
      const verdict = check(grants.get(key), params.rev, params.changes);
      if (!verdict.ok) {
        stats.refused++;
        log({ kind: "refuse", path: params.path, reason: verdict.reason, start: verdict.start, end: verdict.end });
        throw new Error(refusal(params.path, params.rev, verdict));
      }
      const plan = planFor(params.path, params.rev, params.changes);
      if (!plan.includes("SWAP ") && !plan.includes("DEL ") && !plan.includes("INS.")) {
        throw new Error("atto_edit: no effective changes");
      }
      return withFileMutationQueue(key, async () => {
        const result = await runAtto(["patch"], ctx.cwd, plan);
        if (result.code !== 0) {
          stats.failed++;
          log({ kind: "fail", path: params.path, error: (result.stderr || result.stdout).slice(0, 500) });
          throw new Error(result.stderr || result.stdout || "atto patch failed");
        }
        stats.edits++;
        const rev = parseNewRev(result.stdout);
        // The edit replaced the file, so the only lines still provably seen are
        // the ones it just wrote. Chaining is possible; anything else needs a read.
        if (rev) grantWindow(key, rev, parseSpans(result.stdout));
        log({ kind: "edit", path: params.path, bytes: plan.length });
        return {
          content: [{ type: "text", text: result.stdout }],
          details: { path: params.path, rev },
        };
      });
    },
  });

  pi.registerTool({
    name: "atto_undo",
    label: "atto undo",
    description: "Revert the last atto transaction for a file, but only if the file still matches the post-image.",
    parameters: Type.Object({
      path: Type.String({ description: "File to undo" }),
    }),
    async execute(_id, params, _signal, _update, ctx) {
      const result = await runAtto(["undo", params.path], ctx.cwd);
      if (result.code !== 0) throw new Error(result.stderr || result.stdout || "atto undo failed");
      stats.undos++;
      grants.delete(resolve(ctx.cwd, params.path));
      log({ kind: "undo", path: params.path });
      return {
        content: [{ type: "text", text: result.stdout }],
        details: { path: params.path },
      };
    },
  });

  pi.on("session_start", async (_event, ctx) => {
    const probe = await runAtto(["version"], ctx.cwd).catch(() => null);
    if (!probe || probe.code !== 0) {
      if (ctx.hasUI) {
        ctx.ui.notify(
          "atto is not installed or not on PATH; edit is left enabled. Set ATTO_BIN.",
          "warning",
        );
      }
      return;
    }
    const keep = pi.getActiveTools().filter((name) => name !== "edit" && name !== "apply_patch");
    for (const name of ["atto_read", "atto_find", "atto_edit", "atto_undo"]) {
      if (!keep.includes(name)) keep.push(name);
    }
    pi.setActiveTools(keep);
  });

  pi.on("tool_call", (event, ctx) => {
    if (event.toolName === "edit" || event.toolName === "apply_patch") {
      return block(
        "Blocked: existing files are edited only through atto. Call atto_read for a rev, then atto_edit.",
      );
    }
    if (event.toolName === "write") {
      const input = event.input as { path?: string };
      if (input.path && existsSync(resolve(ctx.cwd, input.path))) {
        return block(
          "Blocked: write on an existing file. Call atto_read for a rev, then atto_edit. write is only for new files.",
        );
      }
    }
    if (event.toolName === "bash") {
      const input = event.input as { command?: string };
      if (input.command && shellEdit(input.command, ctx.cwd)) {
        return block(
          "Blocked: use atto_read then atto_edit. Python, sed, perl, tee, and redirects do not edit files here; redirecting scratch output to /tmp or target/ is allowed.",
        );
      }
    }
  });

  pi.registerCommand("atto-stats", {
    description: "Show atto usage and blocked fallbacks for this session",
    handler: async (_args, ctx) => {
      const text = [
        `atto reads:   ${stats.reads}`,
        `atto edits:   ${stats.edits}`,
        `atto undos:   ${stats.undos}`,
        `blocked:      ${stats.blocked}`,
        `failed edits: ${stats.failed}`,
        `log:          ${stateFile()}`,
      ].join("\n");
      ctx.ui.notify(text, "info");
    },
  });
}
