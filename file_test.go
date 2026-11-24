package main

import (
	"bytes"
	"testing"
)

func TestSplitAndEncodeRoundTrip(t *testing.T) {
	cases := []string{
		"",
		"\n",
		"a",
		"a\n",
		"a\nb",
		"a\nb\n",
		"a\n\n",
		"\n\n",
	}
	for _, in := range cases {
		lines, trailing := splitLines(in)
		out := string(encode(lines, false, false, trailing))
		if out != in {
			t.Fatalf("%q -> %q (lines=%q trailing=%v)", in, out, lines, trailing)
		}
	}
}

func TestDecodeEncodePreservesCRLFAndBOM(t *testing.T) {
	in := []byte("\xEF\xBB\xBFa\r\nb\r\n")
	text, bom, crlf, err := decode(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bom || !crlf {
		t.Fatalf("bom=%v crlf=%v", bom, crlf)
	}
	lines, trailing := splitLines(text)
	out := encode(lines, bom, crlf, trailing)
	if !bytes.Equal(out, in) {
		t.Fatalf("%q != %q", out, in)
	}
}

func TestDecodeRejectsBinary(t *testing.T) {
	if _, _, _, err := decode([]byte{0xff, 0xfe, 0x00}); err == nil {
		t.Fatal("expected a UTF-8 error")
	}
}

func TestTrailingNewlineIsAFileProperty(t *testing.T) {
	// Replacing the last line of a file without a trailing newline must not
	// add one; the edit is about the line, not the file's shape.
	abs, rev := fixture(t, "a\nb")
	plan := mustPlan(t, abs, rev, "SWAP 2.=2:\n+B\n")
	if _, err := applyPlan(abs, plan, ""); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, abs); got != "a\nB" {
		t.Fatalf("got %q", got)
	}
}
