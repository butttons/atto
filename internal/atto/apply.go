package atto

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strings"
)

type appliedHunk struct {
	Hunk      Hunk
	LiveStart int // 0-based index of the first replaced line
	OldLen    int // number of live lines replaced; 0 for an insertion
}

type conflict struct {
	Hunk       Hunk
	Reason     string
	Expected   int   // 1-based line where the snapshot said the hunk lives
	Candidates []int // 1-based live lines when the block matched more than once
}

type patchResult struct {
	Path   string
	OldRev string
	NewRev string
	Diff   string
	Spans  []string
}

// reconcile maps every hunk from snapshot coordinates to live coordinates.
// It never guesses: a block that is absent or ambiguous is a conflict.
func reconcile(hunks []Hunk, snap, live []string) ([]appliedHunk, []conflict) {
	var mapped []appliedHunk
	var conflicts []conflict
	for _, h := range hunks {
		oldStart := h.Start - 1
		oldEnd := h.End
		if oldStart < 0 || oldEnd > len(snap) || oldStart > oldEnd {
			conflicts = append(conflicts, conflict{
				Hunk:     h,
				Reason:   fmt.Sprintf("range %d..%d is outside the snapshot (%d lines)", h.Start, h.End, len(snap)),
				Expected: h.Start,
			})
			continue
		}
		old := snap[oldStart:oldEnd]
		before := snap[max(0, oldStart-3):oldStart]
		after := snap[oldEnd:min(len(snap), oldEnd+3)]

		if len(old) == 0 {
			// An insertion has no old text to find. Its context is the anchor.
			if contextAt(live, oldStart, before, after) {
				mapped = append(mapped, appliedHunk{Hunk: h, LiveStart: oldStart})
				continue
			}
			ctx := concat(before, after)
			if len(ctx) == 0 {
				mapped = append(mapped, appliedHunk{Hunk: h, LiveStart: oldStart})
				continue
			}
			matches := findBlock(live, ctx)
			if len(matches) == 1 {
				mapped = append(mapped, appliedHunk{Hunk: h, LiveStart: matches[0] + len(before)})
				continue
			}
			conflicts = append(conflicts, conflict{
				Hunk:       h,
				Reason:     "insertion context is not unique in the live file",
				Expected:   oldStart + 1,
				Candidates: lineNumbers(matches),
			})
			continue
		}

		if matchesAt(live, oldStart, old) {
			mapped = append(mapped, appliedHunk{Hunk: h, LiveStart: oldStart, OldLen: len(old)})
			continue
		}
		ctx := concat(before, old, after)
		if matches := findBlock(live, ctx); len(matches) == 1 {
			mapped = append(mapped, appliedHunk{Hunk: h, LiveStart: matches[0] + len(before), OldLen: len(old)})
			continue
		}
		matches := findBlock(live, old)
		if len(matches) == 1 {
			mapped = append(mapped, appliedHunk{Hunk: h, LiveStart: matches[0], OldLen: len(old)})
			continue
		}
		reason := "old block is not in the live file"
		if len(matches) > 1 {
			reason = fmt.Sprintf("old block matches %d places in the live file", len(matches))
		}
		conflicts = append(conflicts, conflict{
			Hunk:       h,
			Reason:     reason,
			Expected:   oldStart + 1,
			Candidates: lineNumbers(matches),
		})
	}

	if len(conflicts) == 0 {
		sorted := slices.Clone(mapped)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].LiveStart < sorted[j].LiveStart })
		for i := 1; i < len(sorted); i++ {
			prev := sorted[i-1]
			next := sorted[i]
			if prev.LiveStart+prev.OldLen > next.LiveStart ||
				(prev.LiveStart == next.LiveStart && (prev.OldLen == 0 || next.OldLen == 0)) {
				conflicts = append(conflicts, conflict{
					Hunk:     next.Hunk,
					Reason:   "two hunks land on the same live lines after relocation",
					Expected: next.LiveStart + 1,
				})
			}
		}
	}
	return mapped, conflicts
}

func applyMapped(live []string, mapped []appliedHunk) []string {
	out := slices.Clone(live)
	sort.SliceStable(mapped, func(i, j int) bool { return mapped[i].LiveStart > mapped[j].LiveStart })
	for _, m := range mapped {
		out = slices.Replace(out, m.LiveStart, m.LiveStart+m.OldLen, m.Hunk.Body...)
	}
	return out
}

func matchesAt(hay []string, pos int, needle []string) bool {
	if pos < 0 || pos+len(needle) > len(hay) {
		return false
	}
	return slices.Equal(hay[pos:pos+len(needle)], needle)
}

func contextAt(live []string, pos int, before, after []string) bool {
	if pos-len(before) < 0 || pos+len(after) > len(live) {
		return false
	}
	return slices.Equal(live[pos-len(before):pos], before) &&
		slices.Equal(live[pos:pos+len(after)], after)
}

func findBlock(hay, needle []string) []int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return nil
	}
	var out []int
	for i := 0; i+len(needle) <= len(hay); i++ {
		if slices.Equal(hay[i:i+len(needle)], needle) {
			out = append(out, i)
		}
	}
	return out
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func lineNumbers(indices []int) []int {
	out := make([]int, len(indices))
	for i, n := range indices {
		out[i] = n + 1
	}
	return out
}

func applyPlan(abs string, plan *Plan, rawPlan string) (*patchResult, error) {
	lock, err := lockPath(abs)
	if err != nil {
		return nil, err
	}
	defer lock.Close()

	liveRaw, err := readFile(abs)
	if err != nil {
		return nil, err
	}
	snapRaw, err := loadSnapshot(plan.Rev)
	if err != nil {
		return nil, err
	}
	if got := snapshotRev(abs, snapRaw); got != plan.Rev {
		return nil, fmt.Errorf("snapshot %s does not belong to %s; re-read the file", plan.Rev, abs)
	}
	liveText, liveBOM, liveCRLF, err := decode(liveRaw)
	if err != nil {
		return nil, err
	}
	snapText, _, _, err := decode(snapRaw)
	if err != nil {
		return nil, err
	}
	liveLines, liveTrail := splitLines(liveText)
	snapLines, _ := splitLines(snapText)
	plan.resolveTail(len(snapLines))

	var mapped []appliedHunk
	if slices.Equal(liveLines, snapLines) {
		for _, h := range plan.Hunks {
			oldStart := h.Start - 1
			oldEnd := h.End
			if oldStart < 0 || oldEnd > len(snapLines) || oldStart > oldEnd {
				return nil, fmt.Errorf("hunk at plan line %d: range %d..%d is outside the snapshot (%d lines)", h.Line, h.Start, h.End, len(snapLines))
			}
			mapped = append(mapped, appliedHunk{Hunk: h, LiveStart: oldStart, OldLen: oldEnd - oldStart})
		}
	} else {
		var conflicts []conflict
		mapped, conflicts = reconcile(plan.Hunks, snapLines, liveLines)
		if len(conflicts) > 0 {
			return nil, fmt.Errorf("%s", conflictReport(abs, plan.Rev, conflicts, snapLines, liveLines))
		}
	}

	newLines := applyMapped(liveLines, mapped)
	if slices.Equal(newLines, liveLines) {
		return nil, fmt.Errorf("plan makes no changes")
	}
	trailing := liveTrail || (len(liveLines) == 0 && len(newLines) > 0)
	newRaw := encode(newLines, liveBOM, liveCRLF, trailing)
	if bytes.Equal(newRaw, liveRaw) {
		return nil, fmt.Errorf("plan makes no changes")
	}
	if err := atomicWrite(abs, newRaw, fileMode(abs)); err != nil {
		return nil, err
	}
	newRev := snapshotRev(abs, newRaw)
	if err := storeSnapshot(newRev, newRaw); err != nil {
		return nil, err
	}
	if err := appendJournal(JournalEntry{
		Time: now(),
		Path: abs,
		Pre:  plan.Rev,
		Post: newRev,
		Plan: rawPlan,
	}); err != nil {
		return nil, fmt.Errorf("wrote the file but could not journal the transaction: %w", err)
	}
	diff, spans := renderDiff(abs, plan.Rev, newRev, liveLines, mapped)
	return &patchResult{Path: abs, OldRev: plan.Rev, NewRev: newRev, Diff: diff, Spans: spans}, nil
}

func conflictReport(abs, rev string, conflicts []conflict, snap, live []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "conflict: %s changed since snapshot %s; nothing written", abs, rev)
	for _, c := range conflicts {
		fmt.Fprintf(&b, "\nhunk at plan line %d: %s %d..%d: %s", c.Hunk.Line, strings.ToUpper(c.Hunk.Kind), c.Hunk.Start, c.Hunk.End, c.Reason)
		if len(c.Candidates) > 0 {
			fmt.Fprintf(&b, " (candidate lines: %v)", c.Candidates)
		}
		fmt.Fprintf(&b, "\nsnapshot:")
		b.WriteString(numbered(snap, c.Hunk.Start, c.Hunk.End, 6))
		fmt.Fprintf(&b, "\nlive around line %d:", c.Expected)
		b.WriteString(numbered(live, c.Expected-3, c.Expected+6, 10))
	}
	return b.String()
}

func numbered(lines []string, from, to, limit int) string {
	if len(lines) == 0 {
		return " (empty)"
	}
	from = max(1, from)
	to = min(len(lines), to)
	if from > to {
		return " (out of range)"
	}
	if to-from+1 > limit {
		to = from + limit - 1
	}
	var b strings.Builder
	for n := from; n <= to; n++ {
		fmt.Fprintf(&b, "\n%5d| %s", n, lines[n-1])
	}
	if to < len(lines) {
		fmt.Fprintf(&b, "\n... (%d more lines)", len(lines)-to)
	}
	return b.String()
}

func renderDiff(path, oldRev, newRev string, live []string, mapped []appliedHunk) (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "applied %s\n", path)
	fmt.Fprintf(&b, "rev %s -> %s\n", oldRev, newRev)

	sorted := slices.Clone(mapped)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].LiveStart < sorted[j].LiveStart })
	var spans []string
	delta := 0
	shown := 0
	for _, m := range sorted {
		old := live[m.LiveStart : m.LiveStart+m.OldLen]
		if slices.Equal(old, m.Hunk.Body) {
			continue
		}
		pre := live[max(0, m.LiveStart-3):m.LiveStart]
		post := live[min(len(live), m.LiveStart+m.OldLen):min(len(live), m.LiveStart+m.OldLen+3)]
		blockLine := m.LiveStart - len(pre) + 1 + delta
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
			m.LiveStart-len(pre)+1, len(pre)+len(old)+len(post),
			blockLine, len(pre)+len(m.Hunk.Body)+len(post))
		for _, l := range pre {
			fmt.Fprintf(&b, " %s\n", l)
		}
		for _, l := range old {
			fmt.Fprintf(&b, "-%s\n", l)
		}
		for _, l := range m.Hunk.Body {
			fmt.Fprintf(&b, "+%s\n", l)
		}
		for _, l := range post {
			fmt.Fprintf(&b, " %s\n", l)
		}
		shown += len(pre) + len(old) + len(m.Hunk.Body) + len(post) + 1
		start := blockLine + len(pre)
		spans = append(spans, fmt.Sprintf("%d..%d", start, start+len(m.Hunk.Body)-1))
		delta += len(m.Hunk.Body) - len(old)
		if shown > 240 {
			fmt.Fprintf(&b, "... (diff truncated)\n")
			break
		}
	}
	return b.String(), spans
}
