package atto

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// A sub block is text replacement stated as the exact lines to find and the
// lines that replace them. It exists because that is what agents actually do:
// measured across two months of real sessions, 78% of the edits that fell back
// to Python were a literal replace(), and atto could only offer line spans.
// The anchor is the text, so no line arithmetic is required; the count is
// checked, so a replace that matches nothing or too much is refused instead of
// silently doing the wrong thing.
type subBlock struct {
	Old  []string
	New  []string
	Line int
}

// readSubBlocks takes the blocks from stdin, which is what a model can write
// without quoting an argument across many lines.
func readSubBlocks() ([]subBlock, error) {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	return parseSub(string(b))
}

// describeSubs renders the intent for the journal: the journal records what was
// asked for, not just that something was written.
func describeSubs(blocks []subBlock) string {
	var b strings.Builder
	for i, blk := range blocks {
		if i > 0 {
			b.WriteString("@@\n")
		}
		for _, l := range blk.Old {
			b.WriteString("-" + l + "\n")
		}
		for _, l := range blk.New {
			b.WriteString("+" + l + "\n")
		}
	}
	return b.String()
}
func parseSub(text string) ([]subBlock, error) {
	var blocks []subBlock
	var cur *subBlock
	flush := func() {
		if cur != nil {
			blocks = append(blocks, *cur)
			cur = nil
		}
	}
	for i, raw := range strings.Split(text, "\n") {
		lineNo := i + 1
		switch {
		case raw == "@@":
			flush()
		case strings.HasPrefix(raw, "-"):
			if cur != nil && len(cur.New) > 0 {
				flush()
			}
			if cur == nil {
				cur = &subBlock{Line: lineNo}
			}
			cur.Old = append(cur.Old, raw[1:])
		case strings.HasPrefix(raw, "+"):
			if cur == nil {
				return nil, fmt.Errorf("sub line %d: '+' before any '-' line; a block starts with the lines to find", lineNo)
			}
			cur.New = append(cur.New, raw[1:])
		case strings.TrimSpace(raw) == "":
			// Blank lines are layout. To match a blank line, write '-' alone.
		default:
			return nil, fmt.Errorf("sub line %d: %q is not a '-' line to find, a '+' line to write, or '@@'", lineNo, raw)
		}
	}
	flush()
	if len(blocks) == 0 {
		return nil, fmt.Errorf("sub read no blocks; each block is '-' lines to find then '+' lines to write")
	}
	for _, b := range blocks {
		if len(b.Old) == 0 {
			return nil, fmt.Errorf("sub line %d: block has no '-' lines", b.Line)
		}
	}
	return blocks, nil
}

// resolveSubs turns blocks into swap hunks in snapshot coordinates. The count
// discipline is the point: one match by default, every match only when asked,
// and a stated count that must be exact. Python's replace() silently matches
// nothing and silently matches everything; both are refusals here.
func resolveSubs(blocks []subBlock, snap []string, all bool, expect int) ([]Hunk, int, error) {
	var hunks []Hunk
	replaced := 0
	for _, b := range blocks {
		pos := findBlock(snap, b.Old)
		if len(pos) == 0 {
			return nil, 0, fmt.Errorf(
				"sub line %d: those %d line(s) do not appear in the snapshot; nothing written\n"+
					"the '-' lines must match byte for byte, including indentation and trailing spaces",
				b.Line, len(b.Old))
		}
		for i := 1; i < len(pos); i++ {
			if pos[i] < pos[i-1]+len(b.Old) {
				return nil, 0, fmt.Errorf(
					"sub line %d: matches overlap at lines %v, so which lines belong to a match is undecidable; "+
						"add surrounding lines to the '-' block", b.Line, lineNumbers(pos[:i+1]))
			}
		}
		var chosen []int
		switch {
		case expect >= 0:
			if len(pos) != expect {
				return nil, 0, fmt.Errorf(
					"sub line %d: expected %d match(es) but found %d (lines %v); nothing written",
					b.Line, expect, len(pos), lineNumbers(pos))
			}
			chosen = pos
		case all, len(pos) == 1:
			chosen = pos
		default:
			return nil, 0, fmt.Errorf(
				"sub line %d: found %d matches (lines %v); nothing written\n"+
					"add surrounding lines to the '-' block to pin one, or state the count with --expect %d / --all",
				b.Line, len(pos), lineNumbers(pos), len(pos))
		}
		for _, p := range chosen {
			hunks = append(hunks, Hunk{Kind: "swap", Start: p + 1, End: p + len(b.Old), Body: b.New, Line: b.Line})
		}
		replaced += len(chosen)
	}
	sort.SliceStable(hunks, func(i, j int) bool { return hunks[i].Start < hunks[j].Start })
	if err := validateOverlap(hunks); err != nil {
		return nil, 0, err
	}
	return hunks, replaced, nil
}
