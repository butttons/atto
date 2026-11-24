package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if os.Getenv("ATTO_HOME") == "" {
		t.Setenv("ATTO_HOME", filepath.Join(dir, ".atto"))
	}
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := resolvePath(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	rev := snapshotRev(abs, raw)
	if err := storeSnapshot(rev, raw); err != nil {
		t.Fatal(err)
	}
	return abs, rev
}

func mustPlan(t *testing.T, abs, rev, body string) *Plan {
	t.Helper()
	p, err := parsePlan(fmt.Sprintf("file %s\nrev %s\n%s", abs, rev, body))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func readString(t *testing.T, abs string) string {
	t.Helper()
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSwapAndInsert(t *testing.T) {
	abs, rev := fixture(t, "alpha\nbravo\ncharlie\ndelta\n")
	plan := mustPlan(t, abs, rev, "SWAP 2.=3:\n+bravo two\n+charlie two\nINS.POST 4:\n+echo\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, abs), "alpha\nbravo two\ncharlie two\ndelta\necho\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTailInsertAndPlusEscape(t *testing.T) {
	abs, rev := fixture(t, "a\nb\n")
	plan := mustPlan(t, abs, rev, "INS.TAIL:\n++literal\n+plain\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, abs), "a\nb\n+literal\nplain\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDriftAboveIsLocatedByContext(t *testing.T) {
	abs, rev := fixture(t, "alpha\nbravo\ncharlie\ndelta\n")
	if err := os.WriteFile(abs, []byte("zero\nalpha\nbravo\ncharlie\ndelta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := mustPlan(t, abs, rev, "SWAP 2.=2:\n+BRAVO\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, abs), "zero\nalpha\nBRAVO\ncharlie\ndelta\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestConflictInsideHunkWritesNothing(t *testing.T) {
	abs, rev := fixture(t, "alpha\nbravo\ncharlie\n")
	if err := os.WriteFile(abs, []byte("alpha\nCHANGED\ncharlie\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := mustPlan(t, abs, rev, "SWAP 2.=2:\n+bravo new\n")
	_, err := applyPlan(abs, plan, "")
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("err = %v", err)
	}
	if got := readString(t, abs); got != "alpha\nCHANGED\ncharlie\n" {
		t.Fatalf("file changed: %q", got)
	}
}

func TestAmbiguousBlockIsAConflict(t *testing.T) {
	// An eight-line pattern repeated twice: a three-line context window fits
	// wholly inside each copy, so the anchor itself is ambiguous.
	pattern := "A\nB\nC\nD\nE\nF\nG\nH\n"
	abs, rev := fixture(t, pattern+pattern)
	if err := os.WriteFile(abs, []byte("zero\n"+pattern+pattern), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := mustPlan(t, abs, rev, "SWAP 4.=4:\n+NEW D\n")
	_, err := applyPlan(abs, plan, "")
	if err == nil || !strings.Contains(err.Error(), "matches 2 places") {
		t.Fatalf("err = %v", err)
	}
	if got := readString(t, abs); got != "zero\n"+pattern+pattern {
		t.Fatalf("file changed: %q", got)
	}
}

func TestExpectedPositionWinsWhenBlockIsDuplicated(t *testing.T) {
	// The duplicate is below the hunk, so the line number is still true even
	// though the file as a whole changed.
	abs, rev := fixture(t, "one\nX\nY\nthree\none\nX\nY\nthree\n")
	if err := os.WriteFile(abs, []byte("one\nX\nY\nthree\none\nX\nY\nTHREE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := mustPlan(t, abs, rev, "SWAP 2.=3:\n+new X\n+new Y\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, abs), "one\nnew X\nnew Y\nthree\none\nX\nY\nTHREE\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUndoRestoresPreImage(t *testing.T) {
	abs, rev := fixture(t, "alpha\nbravo\n")
	plan := mustPlan(t, abs, rev, "SWAP 2.=2:\n+BRAVO\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if err := cmdUndo([]string{abs}); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, abs), "alpha\nbravo\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUndoRefusesWhenTheFileMovedOn(t *testing.T) {
	abs, rev := fixture(t, "alpha\nbravo\n")
	plan := mustPlan(t, abs, rev, "SWAP 2.=2:\n+BRAVO\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("alpha\nBRAVO\nlater\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdUndo([]string{abs}); err == nil {
		t.Fatal("expected undo to refuse")
	}
	if got := readString(t, abs); got != "alpha\nBRAVO\nlater\n" {
		t.Fatalf("undo wrote anyway: %q", got)
	}
}

func TestCRLFBOMSurviveAPatch(t *testing.T) {
	abs, rev := fixture(t, "\xEF\xBB\xBFone\r\ntwo\r\nthree\r\n")
	plan := mustPlan(t, abs, rev, "SWAP 2.=2:\n+TWO\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, abs), "\xEF\xBB\xBFone\r\nTWO\r\nthree\r\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSnapshotFromAnotherFileIsRejected(t *testing.T) {
	_, revA := fixture(t, "a\n")
	absB, _ := fixture(t, "b\n")
	plan := mustPlan(t, absB, revA, "SWAP 1.=1:\n+c\n")
	_, err := applyPlan(absB, plan, "")
	if err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("err = %v", err)
	}
}

func TestNoOpPlanDoesNotWrite(t *testing.T) {
	abs, rev := fixture(t, "a\nb\n")
	before, _ := os.Stat(abs)
	plan := mustPlan(t, abs, rev, "SWAP 1.=1:\n+a\n")
	_, err := applyPlan(abs, plan, "")
	if err == nil || !strings.Contains(err.Error(), "no changes") {
		t.Fatalf("err = %v", err)
	}
	after, _ := os.Stat(abs)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("no-op plan rewrote the file")
	}
}

func TestDeleteHunk(t *testing.T) {
	abs, rev := fixture(t, "a\nb\nc\nd\n")
	plan := mustPlan(t, abs, rev, "DEL 2.=3\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, abs), "a\nd\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestInsertBefore(t *testing.T) {
	abs, rev := fixture(t, "a\nb\n")
	plan := mustPlan(t, abs, rev, "INS.PRE 2:\n+x\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, abs), "a\nx\nb\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
