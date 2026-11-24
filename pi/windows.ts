/**
 * What a model has actually been shown.
 *
 * A rev proves the file has not moved; it does not prove the model ever looked
 * at the lines it is changing. Those are different failures, and the second one
 * writes a valid patch for the wrong place: addresses held in memory drift after
 * the first edit, and a one-line read authorizes nothing but still hands back a
 * whole-file rev.
 *
 * A grant records the lines a tool returned, keyed by path and rev. The gate
 * lets a change through only when every line it touches is inside a grant for
 * the rev being used. Lines outside are refused with the ranges that were shown,
 * so recovering costs one read, not a guess.
 */

/** 1-based inclusive line ranges that a model has been shown. */
export type Region = { from: number; to: number };

export type Grant = { rev: string; regions: Region[] };

export const WHOLE: Region = { from: 1, to: Number.POSITIVE_INFINITY };

export function wholeFile(): Region[] {
  return [WHOLE];
}

export type Verdict =
  | { ok: true }
  | { ok: false; reason: "rev" | "region"; start: number; end: number; regions: Region[] };

/** covers reports whether [from, to] sits inside the union of regions. */
export function covers(regions: Region[], from: number, to: number): boolean {
  if (to < from) return true;
  let reach = from - 1;
  for (const r of [...regions].sort((a, b) => a.from - b.from)) {
    if (r.to < from) continue;
    if (r.from > reach + 1) break;
    reach = Math.max(reach, r.to);
    if (reach >= to) return true;
  }
  return reach >= to;
}

/**
 * check rejects a change that reaches outside the grant. An insertion (end <
 * start) touches only the boundary, so it passes when either neighbour was
 * shown: inserting at the top of a block you just read is normal.
 */
export function check(
  grant: Grant | undefined,
  rev: string,
  changes: { start: number; end: number }[],
): Verdict {
  if (!grant || grant.rev !== rev) {
    return { ok: false, reason: "rev", start: 0, end: 0, regions: [] };
  }
  for (const c of changes) {
    if (c.end < c.start) {
      const before = c.start > 1 && covers(grant.regions, c.start - 1, c.start - 1);
      const after = covers(grant.regions, c.start, c.start);
      if (before || after) continue;
      return { ok: false, reason: "region", start: c.start, end: c.end, regions: grant.regions };
    }
    if (!covers(grant.regions, c.start, c.end)) {
      return { ok: false, reason: "region", start: c.start, end: c.end, regions: grant.regions };
    }
  }
  return { ok: true };
}

/** describe renders a region for an error message. */
export function describe(regions: Region[]): string {
  if (regions.length === 0) return "no lines were shown";
  return regions
    .map((r) => (r.to === Number.POSITIVE_INFINITY ? `${r.from}..end` : r.from === r.to ? `${r.from}` : `${r.from}..${r.to}`))
    .join(", ");
}

/**
 * parseShown reads the window a `read` actually printed: an explicit range, or
 * the first N lines when the output was truncated at the read cap.
 */
export function parseShown(stdout: string, requested?: { from?: number; to?: number }): Region[] {
  const truncated = /^truncated (\d+)\/(\d+)$/m.exec(stdout);
  if (truncated) return [{ from: 1, to: Number(truncated[1]) }];
  const lines = /^lines (\d+)$/m.exec(stdout);
  const total = lines ? Number(lines[1]) : 0;
  if (requested && requested.from !== undefined) {
    const from = requested.from;
    const to = requested.to ?? requested.from;
    return total === 0 ? wholeFile() : [{ from, to }];
  }
  return wholeFile();
}

/** parseMatches reads the line numbers a `find` printed. */
export function parseMatches(stdout: string): Region[] {
  const regions: Region[] = [];
  for (const line of stdout.split("\n")) {
    const m = /^(\d+)\|/.exec(line);
    if (m) regions.push({ from: Number(m[1]), to: Number(m[1]) });
  }
  return regions;
}

/** parseSpans reads the spans an edit wrote, so chaining stays possible. */
export function parseSpans(stdout: string): Region[] {
  const regions: Region[] = [];
  for (const line of stdout.split("\n")) {
    const m = /^span (\d+)\.\.(\d+)$/.exec(line);
    if (!m) continue;
    const from = Number(m[1]);
    const to = Number(m[2]);
    if (to >= from) regions.push({ from, to });
  }
  return regions;
}
