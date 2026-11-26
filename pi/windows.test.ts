import assert from "node:assert/strict";
import { test } from "node:test";
import {
  check,
  covers,
  describe,
  parseMatches,
  parseShown,
  parseSpans,
  refusal,
  wholeFile,
  type Grant,
} from "./windows.ts";

const grant = (from: number, to: number, rev = "aaa"): Grant => ({ rev, regions: [{ from, to }] });

test("covers a span inside one region", () => {
  assert.equal(covers([{ from: 10, to: 20 }], 12, 18), true);
  assert.equal(covers([{ from: 10, to: 20 }], 10, 20), true);
  assert.equal(covers([{ from: 10, to: 20 }], 9, 12), false);
  assert.equal(covers([{ from: 10, to: 20 }], 18, 21), false);
});

test("covers needs the union, not one region", () => {
  const regions = [
    { from: 1, to: 5 },
    { from: 6, to: 9 },
  ];
  assert.equal(covers(regions, 3, 8), true);
  assert.equal(covers(regions, 5, 6), true);
});

test("a gap blocks a span that crosses it", () => {
  const regions = [
    { from: 1, to: 5 },
    { from: 7, to: 9 },
  ];
  assert.equal(covers(regions, 3, 8), false);
  assert.equal(covers(regions, 6, 6), false);
});

test("out-of-order regions are sorted", () => {
  const regions = [
    { from: 7, to: 9 },
    { from: 1, to: 5 },
  ];
  assert.equal(covers(regions, 1, 9) && false, false, "6 is a gap");
  assert.equal(covers(regions, 2, 4), true);
});

test("changing lines outside the window is refused", () => {
  const v = check(grant(10, 20), "aaa", [{ start: 12, end: 14 }]);
  assert.equal(v.ok, true);

  const outside = check(grant(10, 20), "aaa", [{ start: 21, end: 22 }]);
  assert.equal(outside.ok, false);
  assert.equal(outside.ok === false && outside.reason, "region");
});

// The CONTRACT.md failure: one line read, a four-line block replaced.
test("a one-line read does not authorize a block", () => {
  const v = check(grant(142, 142), "aaa", [{ start: 142, end: 145 }]);
  assert.equal(v.ok, false);
  assert.equal(v.ok === false && v.start, 142);
});

test("a stale rev is refused even if the region looks right", () => {
  const v = check(grant(1, 10, "old"), "new", [{ start: 2, end: 3 }]);
  assert.equal(v.ok, false);
  assert.equal(v.ok === false && v.reason, "rev");
});

test("no grant for the path is a rev refusal", () => {
  const v = check(undefined, "aaa", [{ start: 1, end: 1 }]);
  assert.equal(v.ok, false);
  assert.equal(v.ok === false && v.reason, "rev");
});

// Editing twice in a row must stay possible without re-reading the file.
test("insertion at the top of a block you read is allowed", () => {
  const v = check(grant(5, 9), "aaa", [{ start: 5, end: 4 }]);
  assert.equal(v.ok, true);
});

test("insertion at the bottom of a block you read is allowed", () => {
  const v = check(grant(5, 9), "aaa", [{ start: 10, end: 9 }]);
  assert.equal(v.ok, true, "line 9 is the shown neighbour");
});

test("insertion far away from what you read is refused", () => {
  const v = check(grant(5, 9), "aaa", [{ start: 40, end: 39 }]);
  assert.equal(v.ok, false);
});

test("whole-file read authorizes anything", () => {
  assert.equal(check({ rev: "aaa", regions: wholeFile() }, "aaa", [{ start: 1, end: 9999 }]).ok, true);
});

test("describe is readable", () => {
  assert.equal(describe([]), "no lines were shown");
  assert.equal(describe([{ from: 1, to: Number.POSITIVE_INFINITY }]), "1..end");
  assert.equal(describe([{ from: 7, to: 7 }]), "7");
  assert.equal(describe([{ from: 7, to: 9 }]), "7..9");
});

test("parseShown reads a full read as the whole file", () => {
  const out = "file /x\nrev aaa\nlines 335\n1|one\n";
  assert.deepEqual(parseShown(out), wholeFile());
});

test("parseShown reads an explicit range", () => {
  const out = "file /x\nrev aaa\nlines 335\nshowing 40..42\n40|a\n41|b\n42|c\n";
  assert.deepEqual(parseShown(out, { from: 40, to: 42 }), [{ from: 40, to: 42 }]);
});

// A truncated read only showed the first N lines, however much the model asked for.
test("parseShown trusts the truncation line over the request", () => {
  const out = "file /x\nrev aaa\nlines 5000\nshowing 1..2000\ntruncated 2000/5000\n1|a\n";
  assert.deepEqual(parseShown(out), [{ from: 1, to: 2000 }]);
});

test("parseMatches reads find output", () => {
  const out = "file /x\nrev aaa\nlines 20\nmatches 2\n4|foo\n11|foo\n";
  assert.deepEqual(parseMatches(out), [
    { from: 4, to: 4 },
    { from: 11, to: 11 },
  ]);
});

test("parseSpans grants the span plus the diff context that was shown", () => {
  const out = "applied /x\nrev aaa -> bbb\n@@ -1,3 +1,3 @@\n a\n-b\n+c\n d\nspan 2..2\n";
  assert.deepEqual(parseSpans(out), [{ from: 1, to: 5 }]);
});

test("parseSpans never grants below line 1", () => {
  const out = "applied /x\nrev aaa -> bbb\nspan 1..2\n";
  assert.deepEqual(parseSpans(out), [{ from: 1, to: 5 }]);
});

// A read past EOF is clamped by atto, so the request authorizes more lines than
// were printed. Granting the request would let the model edit lines it never saw.
test("parseShown trusts the printed window over the requested one", () => {
  const out = "file /x\nrev aaa\nlines 239\nshowing 228..239\nclamped 240->239\n228|a\n";
  assert.deepEqual(parseShown(out, { from: 228, to: 240 }), [{ from: 228, to: 239 }]);
});

// The refusal has to name the read, not describe it: a model told to "read the
// range you are changing" guesses the numbers and burns a second call.
test("refusal names the exact read that would authorize the edit", () => {
  const msg = refusal("/w/a.ts", "aaa", {
    reason: "region",
    start: 40,
    end: 60,
    regions: [{ from: 12, to: 38 }],
  });
  assert.match(msg, /nothing was written/);
  assert.match(msg, /lines 40\.\.60 had not been shown to you/);
  assert.match(msg, /this session showed 12\.\.38/);
  assert.match(msg, /atto_read \{path: "\/w\/a\.ts", from: 40, to: 60\}/);
});

// An insertion before line N is authorized by seeing line N itself.
test("refusal points an insertion at the line it sits before", () => {
  const msg = refusal("/w/a.ts", "aaa", { reason: "region", start: 7, end: 6, regions: [] });
  assert.match(msg, /the insertion before line 7/);
  assert.match(msg, /atto_read \{path: "\/w\/a\.ts", from: 7, to: 7\}/);
});

// A lost rev is a different recovery: no range read fixes it, a re-read does.
test("refusal for a replaced rev asks for the file again", () => {
  const msg = refusal("/w/a.ts", "aaa", { reason: "rev", start: 0, end: 0, regions: [] });
  assert.match(msg, /rev aaa was not issued for this file in this session/);
  assert.match(msg, /atto_read, or locate the text with atto_find/);
});
