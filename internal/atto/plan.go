package atto

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Hunk is one declared change in snapshot coordinates. Start and End are
// 1-based inclusive; an insertion has End == Start-1. Tail insertions carry
// Kind "ins.tail" and are resolved against the snapshot's line count.
type Hunk struct {
	Kind  string
	Start int
	End   int
	Body  []string
	Line  int
}

type Plan struct {
	File  string
	Rev   string
	Hunks []Hunk
}

func parsePlan(text string) (*Plan, error) {
	p := &Plan{}
	var cur *Hunk
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	lineNo := 0
	flush := func() {
		if cur != nil {
			p.Hunks = append(p.Hunks, *cur)
			cur = nil
		}
	}
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if cur != nil && strings.HasPrefix(line, "+") {
			cur.Body = append(cur.Body, line[1:])
			continue
		}
		flush()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "file "):
			if len(p.Hunks) > 0 {
				return nil, fmt.Errorf("plan line %d: file must come before hunks", lineNo)
			}
			p.File = strings.TrimSpace(line[len("file "):])
			if p.File == "" {
				return nil, fmt.Errorf("plan line %d: file needs a path", lineNo)
			}
		case strings.HasPrefix(line, "rev "):
			if len(p.Hunks) > 0 {
				return nil, fmt.Errorf("plan line %d: rev must come before hunks", lineNo)
			}
			p.Rev = strings.TrimSpace(line[len("rev "):])
			if p.Rev == "" {
				return nil, fmt.Errorf("plan line %d: rev needs a snapshot id", lineNo)
			}
		case strings.HasPrefix(line, "SWAP "):
			h, err := parseSwap(line, lineNo)
			if err != nil {
				return nil, err
			}
			cur = h
		case strings.HasPrefix(line, "DEL "):
			h, err := parseDel(line, lineNo)
			if err != nil {
				return nil, err
			}
			cur = h
		case strings.HasPrefix(line, "INS.PRE "):
			h, err := parseIns(line, "INS.PRE ", lineNo)
			if err != nil {
				return nil, err
			}
			cur = h
		case strings.HasPrefix(line, "INS.POST "):
			h, err := parseIns(line, "INS.POST ", lineNo)
			if err != nil {
				return nil, err
			}
			cur = h
		case strings.HasPrefix(line, "INS.HEAD:"):
			cur = &Hunk{Kind: "ins.head", Start: 1, End: 0, Line: lineNo}
		case strings.HasPrefix(line, "INS.TAIL:"):
			cur = &Hunk{Kind: "ins.tail", Line: lineNo}
		default:
			return nil, fmt.Errorf("plan line %d: %q is not a directive or a + body line", lineNo, line)
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if p.File == "" {
		return nil, fmt.Errorf("plan has no file")
	}
	if p.Rev == "" {
		return nil, fmt.Errorf("plan has no rev; read the file first")
	}
	if len(p.Hunks) == 0 {
		return nil, fmt.Errorf("plan has no hunks")
	}
	for i := range p.Hunks {
		if err := validateBody(&p.Hunks[i]); err != nil {
			return nil, err
		}
	}
	if err := validateOverlap(p.Hunks); err != nil {
		return nil, err
	}
	return p, nil
}

func parseRange(s string, lineNo int) (int, int, error) {
	parts := strings.Split(s, ".=")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("plan line %d: range %q must be A.=B", lineNo, s)
	}
	a, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("plan line %d: bad start in %q", lineNo, s)
	}
	b, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("plan line %d: bad end in %q", lineNo, s)
	}
	if a < 1 || b < a {
		return 0, 0, fmt.Errorf("plan line %d: range %q must be 1 <= A <= B", lineNo, s)
	}
	return a, b, nil
}

func parseSwap(line string, lineNo int) (*Hunk, error) {
	s := strings.TrimSpace(line[len("SWAP "):])
	if !strings.HasSuffix(s, ":") {
		return nil, fmt.Errorf("plan line %d: SWAP must end with ':'", lineNo)
	}
	a, b, err := parseRange(strings.TrimSuffix(s, ":"), lineNo)
	if err != nil {
		return nil, err
	}
	return &Hunk{Kind: "swap", Start: a, End: b, Line: lineNo}, nil
}

func parseDel(line string, lineNo int) (*Hunk, error) {
	s := strings.TrimSpace(line[len("DEL "):])
	if strings.HasSuffix(s, ":") {
		return nil, fmt.Errorf("plan line %d: DEL takes no body and no ':'", lineNo)
	}
	a, b, err := parseRange(s, lineNo)
	if err != nil {
		return nil, err
	}
	return &Hunk{Kind: "del", Start: a, End: b, Line: lineNo}, nil
}

func parseIns(line, prefix string, lineNo int) (*Hunk, error) {
	s := strings.TrimSpace(line[len(prefix):])
	if !strings.HasSuffix(s, ":") {
		return nil, fmt.Errorf("plan line %d: %s must end with ':'", lineNo, strings.TrimSpace(prefix))
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, ":"))
	if err != nil || n < 1 {
		return nil, fmt.Errorf("plan line %d: %s needs a line number >= 1", lineNo, strings.TrimSpace(prefix))
	}
	if prefix == "INS.PRE " {
		return &Hunk{Kind: "ins.pre", Start: n, End: n - 1, Line: lineNo}, nil
	}
	return &Hunk{Kind: "ins.post", Start: n + 1, End: n, Line: lineNo}, nil
}

func validateBody(h *Hunk) error {
	switch h.Kind {
	case "swap":
		// An empty body is a delete. That is a useful shorthand, not a mistake.
	case "del":
		if len(h.Body) > 0 {
			return fmt.Errorf("plan line %d: DEL does not take body lines", h.Line)
		}
	case "ins.pre", "ins.post", "ins.head", "ins.tail":
		if len(h.Body) == 0 {
			return fmt.Errorf("plan line %d: insert has no body lines", h.Line)
		}
	}
	return nil
}

// validateOverlap rejects hunks that touch the same snapshot lines. Two
// insertions at one point are ambiguous about order, so they are rejected too.
func validateOverlap(hunks []Hunk) error {
	for i := 0; i < len(hunks); i++ {
		for j := i + 1; j < len(hunks); j++ {
			a, b := hunks[i], hunks[j]
			if a.Kind == "ins.tail" || b.Kind == "ins.tail" {
				continue
			}
			if overlaps(a, b) {
				return fmt.Errorf("plan lines %d and %d: hunks overlap", a.Line, b.Line)
			}
		}
	}
	return nil
}

func overlaps(a, b Hunk) bool {
	aIns := a.End < a.Start
	bIns := b.End < b.Start
	switch {
	case aIns && bIns:
		return a.Start == b.Start
	case aIns:
		return a.Start >= b.Start && a.Start <= b.End+1
	case bIns:
		return b.Start >= a.Start && b.Start <= a.End+1
	default:
		return a.Start <= b.End && b.Start <= a.End
	}
}

func (p *Plan) resolveTail(n int) {
	for i := range p.Hunks {
		if p.Hunks[i].Kind == "ins.tail" {
			p.Hunks[i].Start = n + 1
			p.Hunks[i].End = n
		}
	}
}
