/**
 * The shell-edit guard, as pure functions.
 *
 * Kept dependency-free and exported so it can be tested directly with
 * `node --experimental-strip-types --test guard.test.ts` instead of by
 * driving a model at it. Every case in guard.test.ts is a false positive or
 * false negative that actually happened.
 */

const SHELL_EDIT_PATTERNS = [
  /\bpython[0-9.]*\s+-\s*<</,
  // python -c is blocked when the inline code can write, not merely when it
  // mentions a file: `print(open('f').read())` is still allowed, and a path
  // holding a "w" is not a mode. Only a real mode argument counts.
  /\bpython[0-9.]*\s+-c\b[^\n]*(\bwrite_text\b|\.write\b|\.writeFileSync\b|open\s*\([^)]*,\s*['"][rwxabt+]{1,3}['"]|shutil\.|os\.remove|os\.rename|\.unlink\b|subprocess)/,
  /\bsed\s+-i\b/,
  /\bperl\s+-[A-Za-z]*i\b/,
  /\bgit\s+apply\b/,
  /\bapply_patch\b/,
  /\bhashline\s+patch\b/,
  /\btee\b/,
  /\bnode\b[^\n]*writeFileSync/,
];

// A redirect operator is `>` or `>>` that starts a token: preceded by start,
// whitespace, a separator, or a file descriptor digit. That excludes `=>`,
// `->`, `>=` and `<`, which are code, not redirects.
const REDIRECT = /(?<=^|[\s;&|(0-9])>>?\s*([^\s;|&]+)/g;

// Redirections inside quotes are text being passed to a command, not a write.
function stripQuoted(command: string): string {
  return command.replace(/'[^']*'|"(?:[^"\\]|\\.)*"/g, "''");
}

// A command may cd before it writes; resolve relative targets against the last
// directory it enters. The `(` alternative covers subshells: `(cd dist && ...
// > SHA256SUMS)` is a scratch write, and missing the cd made it look like a
// repo write. A heuristic, and it errs toward blocking.
export function redirectBase(command: string, cwd: string, resolvePath: (a: string, b: string) => string): string {
  let base = cwd;
  for (const m of command.matchAll(/(?:^|[;&|(]\s*)cd\s+([^\s;&|)]+)/g)) {
    base = resolvePath(base, m[1]);
  }
  return base;
}

export function isScratchPath(
  target: string,
  base: string,
  resolvePath: (a: string, b: string) => string,
  scratchRoots: string[],
): boolean {
  if (target === "/dev/null" || target.startsWith("/dev/")) return true;
  // macOS reports /tmp, the real path is /private/tmp.
  const real = (p: string) => resolvePath(p).replace(/^\/private(?=\/)/, "");
  const abs = real(resolvePath(base, target));
  for (const dir of scratchRoots) {
    const root = real(dir);
    if (abs === root || abs.startsWith(root + "/")) return true;
  }
  return /(^|\/)target\//.test(abs);
}

export interface GuardEnv {
  cwd: string;
  scratchRoots: string[];
  resolvePath: (a: string, b?: string) => string;
  allowShellEdits?: boolean;
}

export function isShellEdit(command: string, env: GuardEnv): boolean {
  if (env.allowShellEdits) return false;
  if (SHELL_EDIT_PATTERNS.some((re) => re.test(command))) return true;
  const base = redirectBase(command, env.cwd, env.resolvePath);
  for (const match of stripQuoted(command).matchAll(REDIRECT)) {
    if (isScratchPath(match[1], base, env.resolvePath, env.scratchRoots)) continue;
    return true;
  }
  return false;
}
