package atto

import (
	"os"
	"strings"
	"testing"
)

// The bug this verb exists for: Python's replace() silently does nothing when
// the text is absent, and silently replaces every copy when it is present.
// Both have to be refusals, not quiet wrong answers.
func TestSubRefusesAbsentText(t *testing.T) {
	blocks, err := parseSub("-not there\n+replacement\n")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = resolveSubs(blocks, []string{"alpha", "bravo"}, false, -1)
	if err == nil {
		t.Fatal("absent text was accepted")
	}
	if !strings.Contains(err.Error(), "do not appear") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestSubRefusesAmbiguousMatchWithoutACount(t *testing.T) {
	blocks, err := parseSub("-dup\n+DUP\n")
	if err != nil {
		t.Fatal(err)
	}
	snap := []string{"dup", "a", "dup", "b", "dup"}
	_, _, err = resolveSubs(blocks, snap, false, -1)
	if err == nil {
		t.Fatal("three matches were replaced without being stated")
	}
	for _, want := range []string{"found 3 matches", "lines [1 3 5]", "--expect 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q is missing %q", err, want)
		}
	}
}

func TestSubExpectMustEqualTheActualCount(t *testing.T) {
	blocks, err := parseSub("-dup\n+DUP\n")
	if err != nil {
		t.Fatal(err)
	}
	snap := []string{"dup", "a", "dup", "b", "dup"}
	if _, _, err := resolveSubs(blocks, snap, false, 2); err == nil {
		t.Fatal("expected 2 when there are 3")
	}
	_, n, err := resolveSubs(blocks, snap, false, 3)
	if err != nil {
		t.Fatalf("expect 3 rejected: %v", err)
	}
	if n != 3 {
		t.Fatalf("replaced %d, want 3", n)
	}
}

func TestSubAllReplacesEveryMatch(t *testing.T) {
	blocks, err := parseSub("-dup\n+DUP\n")
	if err != nil {
		t.Fatal(err)
	}
	hunks, n, err := resolveSubs(blocks, []string{"dup", "a", "dup"}, true, -1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(hunks) != 2 {
		t.Fatalf("replaced %d in %d hunks, want 2 in 2", n, len(hunks))
	}
	if hunks[0].Start != 1 || hunks[0].End != 1 || hunks[1].Start != 3 {
		t.Fatalf("hunks landed at %d..%d and %d", hunks[0].Start, hunks[0].End, hunks[1].Start)
	}
}

// Overlapping matches cannot be attributed to one occurrence or another, so the
// only honest answer is to refuse and ask for more context.
func TestSubRefusesOverlappingMatches(t *testing.T) {
	blocks, err := parseSub("-a\n-a\n+X\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveSubs(blocks, []string{"a", "a", "a"}, true, -1); err == nil {
		t.Fatal("overlapping matches were replaced")
	}
}

func TestParseSubSeparatesBlocks(t *testing.T) {
	blocks, err := parseSub("-one\n+ONE\n@@\n-two\n-three\n+TWO\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (the second holds both later lines)", len(blocks))
	}
	if len(blocks[0].Old) != 1 || len(blocks[1].Old) != 2 {
		t.Fatalf("block shapes %v and %v", blocks[0].Old, blocks[1].Old)
	}
}

// A '-' after a '+' starts a new block, so '@@' is optional and a model that
// omits it still gets the batch it meant.
func TestParseSubSplitsOnMinusAfterPlus(t *testing.T) {
	blocks, err := parseSub("-one\n+ONE\n-two\n+TWO\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
}

func TestParseSubRejectsBodiesWithoutAnAnchor(t *testing.T) {
	if _, err := parseSub("+orphan\n"); err == nil {
		t.Fatal("a '+' with no '-' was accepted")
	}
	if _, err := parseSub(""); err == nil {
		t.Fatal("empty input was accepted")
	}
	if _, err := parseSub("-fine\n+garbage\nnot a directive\n"); err == nil {
		t.Fatal("an unknown line was accepted")
	}
}

func TestParseSubAllowsEmptyLinesInABlock(t *testing.T) {
	blocks, err := parseSub("-a\n-\n+b\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || len(blocks[0].Old) != 2 || blocks[0].Old[1] != "" {
		t.Fatalf("blank line not part of the block: %#v", blocks)
	}
}

// The end-to-end shape: several blocks, one write, one rev, one journal entry.
func TestSubAppliesBlockBatchInOneTransaction(t *testing.T) {
	abs, rev := fixture(t, "name: app\nport: 80\ndebug: false\n")
	blocks, err := parseSub("-name: app\n+name: svc\n@@\n-port: 80\n+port: 8080\n")
	if err != nil {
		t.Fatal(err)
	}
	snapLines := []string{"name: app", "port: 80", "debug: false"}
	hunks, n, err := resolveSubs(blocks, snapLines, false, -1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("replaced %d, want 2", n)
	}
	res, err := applyPlan(abs, &Plan{File: abs, Rev: rev, Hunks: hunks}, "sub")
	if err != nil {
		t.Fatal(err)
	}
	if got := readString(t, abs); got != "name: svc\nport: 8080\ndebug: false\n" {
		t.Fatalf("file reads %q", got)
	}
	if len(res.Spans) != 2 {
		t.Fatalf("spans %v, want 2", res.Spans)
	}
}

func TestSubDryRunWritesNothing(t *testing.T) {
	abs, rev := fixture(t, "keep: 1\nchange: 2\n")
	blocks, err := parseSub("-change: 2\n+change: 3\n")
	if err != nil {
		t.Fatal(err)
	}
	hunks, _, err := resolveSubs(blocks, []string{"keep: 1", "change: 2"}, false, -1)
	if err != nil {
		t.Fatal(err)
	}
	res, err := applyPlan(abs, &Plan{File: abs, Rev: rev, Hunks: hunks, Dry: true}, "sub")
	if err != nil {
		t.Fatal(err)
	}
	if got := readString(t, abs); got != "keep: 1\nchange: 2\n" {
		t.Fatalf("dry run wrote the file: %q", got)
	}
	if !strings.Contains(res.Diff, "would apply") {
		t.Fatalf("dry run diff does not say so: %q", res.Diff)
	}
	if res.NewRev == "" {
		t.Fatal("dry run reported no prospective rev")
	}
}

// Content anchoring is the reason this verb survives drift: the file moved
// underneath the snapshot and the edit still lands on the text it named.
func TestSubLandsOnTextAfterTheFileMoves(t *testing.T) {
	abs, rev := fixture(t, "head\ntarget\ntail\n")
	blocks, err := parseSub("-target\n+TARGET\n")
	if err != nil {
		t.Fatal(err)
	}
	// The file gains three lines above the target after the snapshot was taken.
	if err := os.WriteFile(abs, []byte("a\nb\nc\nhead\ntarget\ntail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hunks, _, err := resolveSubs(blocks, []string{"head", "target", "tail"}, false, -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyPlan(abs, &Plan{File: abs, Rev: rev, Hunks: hunks}, "sub"); err != nil {
		t.Fatalf("anchored sub refused after drift: %v", err)
	}
	if got := readString(t, abs); got != "a\nb\nc\nhead\nTARGET\ntail\n" {
		t.Fatalf("landed wrong: %q", got)
	}
}
