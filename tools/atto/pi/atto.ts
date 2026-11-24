/**
 * atto for pi — make the agent edit through atto, not through edit/Python/sed.
 *
 * What it does:
 *   - registers atto_read, atto_edit, atto_undo
 *   - removes the built-in `edit` and `apply_patch` from the model's tool set
 *   - blocks `edit` and `apply_patch` if something still reaches them
 *   - blocks `write` when the file already exists
 *   - blocks shell commands that edit files (python heredocs, sed -i, tee, redirects)
 *   - records every block and every atto edit so the fallback rate is measurable
 *
 * The guard is the point. A prompt guideline is a request; this is a gate.
 *
 * Load:
 *   pi --extension /path/to/tools/atto/pi/atto.ts
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
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { Type } from "@earendil-works/pi-ai";
import { withFileMutationQueue, type ExtensionAPI } from "@earendil-works/pi-coding-agent";

type AttoResult = { code: number; stdout: string; stderr: string };

type Change = { start: number; end: number; text: string };

const stats = { reads: 0, edits: 0, undos: 0, blocked: 0, failed: 0 };

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

const SHELL_EDIT_PATTERNS = [
  /\bpython[0-9.]*\s+-\s*<</,
  /\bpython[0-9.]*\s+-c\b[^\n]*\b(write_text|writeFileSync|\.replace\s*\()/,
  /\bsed\s+-i\b/,
  /\bperl\s+-[A-Za-z]*i\b/,
  /\bgit\s+apply\b/,
  /\bapply_patch\b/,
  /\bhashline\s+patch\b/,
  /\btee\b/,
  /\bnode\b[^\n]*writeFileSync/,
];

function isShellEdit(command: string): boolean {
  if (process.env.ATTO_ALLOW_SHELL_EDITS === "1") return false;
  if (SHELL_EDIT_PATTERNS.some((re) => re.test(command))) return true;
  // Redirects into a file, excluding scratch space. 2>&1 and &> are not writes.
  for (const match of command.matchAll(/(?:^|[^0-9&>])>>?\s*([^\s;|&]+)/g)) {
    const target = match[1];
    if (target === "/dev/null" || target.startsWith("/dev/")) continue;
    if (target.startsWith("/tmp/") || target.startsWith("/var/tmp/")) continue;
    if (target.startsWith("target/") || target.startsWith("./target/")) continue;
    return true;
  }
  return false;
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
      log({ kind: "read", path: params.path, bytes: result.stdout.length });
      return {
        content: [{ type: "text", text: result.stdout }],
        details: { path: params.path, rev: parseRev(result.stdout) },
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
      "Pass the rev from atto_read. If atto_edit reports a conflict, atto_read again and retry with the new rev.",
      "Put every independent change to a file in one atto_edit call; it is one transaction and one undo step.",
      "An empty text deletes start..end. To insert before line N, set start=N and end=N-1.",
    ],
    parameters: Type.Object({
      path: Type.String({ description: "Existing file to change" }),
      rev: Type.String({ description: "Snapshot rev returned by atto_read" }),
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
      const plan = planFor(params.path, params.rev, params.changes);
      if (!plan.includes("SWAP ") && !plan.includes("DEL ") && !plan.includes("INS.")) {
        throw new Error("atto_edit: no effective changes");
      }
      return withFileMutationQueue(resolve(ctx.cwd, params.path), async () => {
        const result = await runAtto(["patch"], ctx.cwd, plan);
        if (result.code !== 0) {
          stats.failed++;
          log({ kind: "fail", path: params.path, error: (result.stderr || result.stdout).slice(0, 500) });
          throw new Error(result.stderr || result.stdout || "atto patch failed");
        }
        stats.edits++;
        log({ kind: "edit", path: params.path, bytes: plan.length });
        return {
          content: [{ type: "text", text: result.stdout }],
          details: { path: params.path, rev: parseNewRev(result.stdout) },
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
    for (const name of ["atto_read", "atto_edit", "atto_undo"]) {
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
      if (input.command && isShellEdit(input.command)) {
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
