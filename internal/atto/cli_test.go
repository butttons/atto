package atto

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// capture runs a command with stdout and stderr swapped onto pipes, so a test
// can read what the agent would have seen.
func capture(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	or, ow, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	er, ew, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = ow, ew
	code = Run(args)
	os.Stdout, os.Stderr = oldOut, oldErr
	ow.Close()
	ew.Close()
	out, _ := io.ReadAll(or)
	errOut, _ := io.ReadAll(er)
	or.Close()
	er.Close()
	return string(out), string(errOut), code
}

// The usage text is the only place a verb is announced, so a verb that is
// announced and never dispatched is a bug the help text hides. `atto log` was
// documented in five files and reachable from none of them.
func TestEveryAdvertisedVerbIsDispatched(t *testing.T) {
	t.Setenv("ATTO_HOME", filepath.Join(t.TempDir(), ".atto"))
	verbs := regexp.MustCompile(`(?m)^  atto (\w+)`).FindAllStringSubmatch(usage, -1)
	if len(verbs) < 7 {
		t.Fatalf("parsed only %d verbs from the usage text", len(verbs))
	}
	for _, m := range verbs {
		verb := m[1]
		_, stderr, code := capture(t, verb)
		if strings.Contains(stderr, "unknown command") {
			t.Errorf("%q is in the usage text and not dispatched", verb)
		}
		if code == 2 && !strings.HasPrefix(verb, "help") {
			t.Errorf("%s exited 2 with no usage complaint: %q", verb, stderr)
		}
	}
}

func TestLogReportsRecentTransactions(t *testing.T) {
	abs, rev := fixture(t, "alpha\nbravo\n")
	plan := mustPlan(t, abs, rev, "SWAP 2.=2:\n+BRAVO\n")
	res, err := applyPlan(abs, plan, "SWAP 2.=2:\n+BRAVO\n")
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, code := capture(t, "log", abs)
	if code != 0 {
		t.Fatalf("log exited %d: %s", code, stderr)
	}
	for _, want := range []string{abs, rev, res.NewRev} {
		if !strings.Contains(out, want) {
			t.Errorf("log output %q is missing %q", out, want)
		}
	}
}

func TestLogWithoutHistoryIsNotAnError(t *testing.T) {
	t.Setenv("ATTO_HOME", filepath.Join(t.TempDir(), ".atto"))
	out, stderr, code := capture(t, "log")
	if code != 0 {
		t.Fatalf("log on an empty journal exited %d: %s", code, stderr)
	}
	if !strings.Contains(out, "no transactions") {
		t.Errorf("log on an empty journal printed %q", out)
	}
}

// A read that asks for a few lines past EOF is the mistake that actually
// happens: in one machine's session log all 25 refused ranges were this, and
// every one cost the model a second read. Clamp the window instead, print the
// one it showed, and say it clamped so the model's next request is informed.
func TestReadClampsRangePastEOF(t *testing.T) {
	abs, _ := fixture(t, "one\ntwo\nthree\n")
	out, stderr, code := capture(t, "read", abs, "2:99")
	if code != 0 {
		t.Fatalf("read 2:99 on a 3-line file exited %d: %q", code, stderr)
	}
	for _, want := range []string{"showing 2..3", "clamped 99->3"} {
		if !strings.Contains(out, want) {
			t.Errorf("clamped read is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "4|") {
		t.Errorf("clamped read printed a line that does not exist:\n%s", out)
	}
}

// A range with nothing in it is still a refusal: clamping from beyond EOF would
// hand back an empty window and call it success.
func TestReadRefusesRangeFromPastEOF(t *testing.T) {
	abs, _ := fixture(t, "one\ntwo\n")
	_, stderr, code := capture(t, "read", abs, "9:12")
	if code == 0 || !strings.Contains(stderr, "outside the file") {
		t.Fatalf("read 9:12 on a 2-line file: code=%d stderr=%q", code, stderr)
	}
}
