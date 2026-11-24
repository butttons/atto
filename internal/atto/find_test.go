package atto

import (
	"fmt"
	"strings"
	"testing"
)

func TestFindLinesIsLiteralAndCaseSensitive(t *testing.T) {
	lines := []string{"alpha", "Bravo bravo", "charlie", "bravo"}

	got := findLines(lines, "bravo")
	if len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Fatalf("literal lower-case find = %v, want [2 4]", got)
	}
	got = findLines(lines, "Bravo")
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("literal upper-case find = %v, want [2]", got)
	}
	got = findLines(lines, "a")
	if len(got) != 4 || got[0] != 1 || got[3] != 4 {
		t.Fatalf("single-letter find = %v, want [1 2 3 4]", got)
	}
	if got := findLines(lines, "nope"); len(got) != 0 {
		t.Fatalf("absent text matched %v", got)
	}
}

// The fence-in-README bug: an insert whose body is the block already sitting
// at the insertion point. Atto used to apply this, and the duplicate was only
// visible in the diff.
func TestInsertOfAdjacentBlockIsRefused(t *testing.T) {
	body := "head\n```sh\ncmdx\n```\ntail\n"
	abs, rev := fixture(t, body)

	plan := mustPlan(t, abs, rev, "INS.PRE 2:\n+```sh\n+cmdx\n+```\n")
	if _, err := applyPlan(abs, plan, "plan"); err == nil {
		t.Fatal("duplicated block was applied")
	} else if !strings.Contains(err.Error(), "identical to lines 2..4") {
		t.Fatalf("unhelpful error: %v", err)
	}
	if got := readString(t, abs); got != body {
		t.Fatalf("file changed despite the refusal:\n%s", got)
	}
}

func TestDuplicateRefusalBlocksTheFollowingBlockToo(t *testing.T) {
	abs, rev := fixture(t, "head\nAAA\nBBB\ntail\n")

	plan := mustPlan(t, abs, rev, "INS.POST 2:\n+AAA\n+BBB\n")
	if _, err := applyPlan(abs, plan, "plan"); err == nil {
		t.Fatal("duplicated block after the anchor was applied")
	}
}

func TestAllowDuplicateIsExplicit(t *testing.T) {
	abs, rev := fixture(t, "head\nAAA\nBBB\ntail\n")

	plan := mustPlan(t, abs, rev, "INS.PRE 2:\n+AAA\n+BBB\n")
	plan.AllowDuplicate = true
	if _, err := applyPlan(abs, plan, "plan"); err != nil {
		t.Fatalf("explicit override refused the edit: %v", err)
	}
	if got := readString(t, abs); got != "head\nAAA\nBBB\nAAA\nBBB\ntail\n" {
		t.Fatalf("override wrote the wrong body:\n%s", got)
	}
}

// Inserting a line that happens to equal its neighbour is legal; the refusal
// is about blocks that duplicate text already present, not about coincidence.
func TestRepeatingAVisibleLineElsewhereIsAllowed(t *testing.T) {
	abs, rev := fixture(t, "head\n```\ntail\n")

	plan := mustPlan(t, abs, rev, "INS.TAIL:\n+```\n")
	res, err := applyPlan(abs, plan, "plan")
	if err != nil {
		t.Fatalf("closing fence at the tail was refused: %v", err)
	}
	if got := readString(t, abs); got != "head\n```\ntail\n```\n" {
		t.Fatalf("tail insert wrote:\n%s", got)
	}
	if len(res.Notes) != 0 {
		t.Fatalf("unexpected notes: %v", res.Notes)
	}
}

func TestBlankInsertIsNotADuplicate(t *testing.T) {
	abs, rev := fixture(t, "head\n\nsection\ntail\n")

	plan := mustPlan(t, abs, rev, "INS.PRE 2:\n+\n")
	if _, err := applyPlan(abs, plan, "plan"); err != nil {
		t.Fatalf("adding a blank line next to a blank line was refused: %v", err)
	}
}

func TestAdjacentDuplicateIntroducedByAnEditIsNoted(t *testing.T) {
	before := []string{"alpha", "## Verify", "beta"}
	after := []string{"alpha", "## Verify", "## Verify", "beta"}

	notes := duplicateNotes(before, after)
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want one", notes)
	}
	if !strings.Contains(notes[0], "lines 2..3") || !strings.Contains(notes[0], "## Verify") {
		t.Fatalf("note does not locate the duplicate: %q", notes[0])
	}
}

func TestPreExistingAdjacentDuplicatesAreNotNoted(t *testing.T) {
	before := []string{"a", "b", "b", "c"}
	after := []string{"a", "x", "b", "b", "c"}

	if notes := duplicateNotes(before, after); len(notes) != 0 {
		t.Fatalf("notes fired on duplicates that were already there: %v", notes)
	}
}

func TestFindAndPatchUseTheSameRev(t *testing.T) {
	abs, rev := fixture(t, "alpha\ntarget\nomega\n")
	if hits := findLines([]string{"alpha", "target", "omega"}, "target"); len(hits) != 1 || hits[0] != 2 {
		t.Fatalf("find = %v", hits)
	}

	plan := mustPlan(t, abs, rev, fmt.Sprintf("SWAP %d.=%d:\n+found\n", 2, 2))
	if _, err := applyPlan(abs, plan, "plan"); err != nil {
		t.Fatalf("patch with the find rev failed: %v", err)
	}
	if got := readString(t, abs); got != "alpha\nfound\nomega\n" {
		t.Fatalf("got:\n%s", got)
	}
}
