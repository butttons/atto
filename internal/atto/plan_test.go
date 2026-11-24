package atto

import (
	"strings"
	"testing"
)

func TestParsePlanReadsTheGrammar(t *testing.T) {
	plan := `# a comment
file src/x.rs
rev abc123

SWAP 2.=3:
+new two
++literal plus

DEL 9.=10

INS.POST 4:
+after four

INS.TAIL:
+tail
`
	p, err := parsePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if p.File != "src/x.rs" || p.Rev != "abc123" {
		t.Fatalf("header = %q %q", p.File, p.Rev)
	}
	if len(p.Hunks) != 4 {
		t.Fatalf("hunks = %d", len(p.Hunks))
	}
	if got := p.Hunks[0].Body[1]; got != "+literal plus" {
		t.Fatalf("++ escape produced %q", got)
	}
	if p.Hunks[1].Kind != "del" || p.Hunks[1].Start != 9 || p.Hunks[1].End != 10 {
		t.Fatalf("del = %+v", p.Hunks[1])
	}
	if p.Hunks[3].Kind != "ins.tail" {
		t.Fatalf("tail = %+v", p.Hunks[3])
	}
}

func TestParsePlanRejectsMissingPlus(t *testing.T) {
	_, err := parsePlan("file x\nrev r\nSWAP 1.=1:\nnot prefixed\n")
	if err == nil || !strings.Contains(err.Error(), "not a directive") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePlanRejectsOverlap(t *testing.T) {
	_, err := parsePlan("file x\nrev r\nSWAP 2.=4:\n+x\nSWAP 4.=5:\n+y\n")
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePlanRejectsTwoInsertionsAtOnePoint(t *testing.T) {
	_, err := parsePlan("file x\nrev r\nINS.PRE 2:\n+a\nINS.POST 1:\n+b\n")
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePlanRejectsTailWithoutBody(t *testing.T) {
	_, err := parsePlan("file x\nrev r\nINS.TAIL:\n")
	if err == nil || !strings.Contains(err.Error(), "no body") {
		t.Fatalf("err = %v", err)
	}
}

func TestParsePlanRejectsMissingRev(t *testing.T) {
	_, err := parsePlan("file x\nSWAP 1.=1:\n+y\n")
	if err == nil || !strings.Contains(err.Error(), "no rev") {
		t.Fatalf("err = %v", err)
	}
}
