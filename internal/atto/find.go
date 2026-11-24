package atto

import (
	"strconv"
	"strings"
)

// findLines returns the 1-based numbers of lines containing needle. The match
// is literal and case-sensitive on purpose: the point of find is to hand back
// numbers a model would otherwise count by hand, not to guess at intent.
func findLines(lines []string, needle string) []int {
	hits := make([]int, 0, 8)
	for i, line := range lines {
		if strings.Contains(line, needle) {
			hits = append(hits, i+1)
		}
	}
	return hits
}

// duplicateInsert reports the block an insertion repeats verbatim, in snapshot
// coordinates. Pasting existing text back beside itself is never the edit
// anyone meant, and the diff makes it easy to miss afterwards.
//
// It only looks at windows flush against the insertion point: the block just
// before it, the block just after it, and every window straddling it. Repeating
// a line further away is left alone, because that is usually deliberate.
func duplicateInsert(h Hunk, snap []string) (string, bool) {
	if h.End >= h.Start || len(h.Body) == 0 || blankBody(h.Body) {
		return "", false
	}
	anchor := h.Start - 1
	n := len(h.Body)
	for i := anchor - n; i <= anchor; i++ {
		if i < 0 || i+n > len(snap) {
			continue
		}
		if equalLines(snap[i:i+n], h.Body) {
			return blockText(i+1, i+n), true
		}
	}
	return "", false
}

// blankBody exempts spacing edits: adding an empty line next to another empty
// line is untidy, not a duplicated block.
func blankBody(body []string) bool {
	for _, line := range body {
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
}

func blockText(from, to int) string {
	if from == to {
		return "line " + strconv.Itoa(from)
	}
	return "lines " + strconv.Itoa(from) + ".." + strconv.Itoa(to)
}

// duplicateNotes reports adjacent identical non-empty lines that an edit
// introduced. It is a note, not a refusal: two equal lines can be correct, but
// they are also the signature of a block pasted twice.
func duplicateNotes(before, after []string) []string {
	existing := map[string]int{}
	for i := 1; i < len(before); i++ {
		if before[i] == before[i-1] && strings.TrimSpace(before[i]) != "" {
			existing[before[i]]++
		}
	}
	var notes []string
	for i := 1; i < len(after) && len(notes) < 3; i++ {
		line := after[i]
		if line != after[i-1] || strings.TrimSpace(line) == "" {
			continue
		}
		if existing[line] > 0 {
			existing[line]--
			continue
		}
		notes = append(notes, "note: lines "+rangeText(i, i+1)+" are now identical: "+line)
	}
	return notes
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func rangeText(from, to int) string {
	if from == to {
		return strconv.Itoa(from)
	}
	return strconv.Itoa(from) + ".." + strconv.Itoa(to)
}
