package atto

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const version = "0.1.0"

const readCap = 2000

const usage = `atto — precise, agent-first file editing

usage:
  atto read FILE [FROM:TO]   numbered lines + snapshot id
  atto stat FILE             snapshot id, line count, byte count
  atto patch                 apply a plan from stdin (see CONTRACT.md)
  atto undo FILE             revert the last transaction, if the file still matches
  atto log [FILE]            recent transactions
  atto version
`

// Run executes one atto command and returns the process exit code.
func Run(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "read":
		err = cmdRead(args[1:])
	case "stat":
		err = cmdStat(args[1:])
	case "patch":
		err = cmdPatch(args[1:])
	case "undo":
		err = cmdUndo(args[1:])
	case "log":
		err = cmdLog(args[1:])
	case "version", "--version", "-V":
		fmt.Printf("atto %s\n", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "atto: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "atto: %v\n", err)
		return 1
	}
	return 0
}

func cmdRead(args []string) error {
	path, from, to, hasRange, err := parseReadArgs(args)
	if err != nil {
		return err
	}
	abs, err := resolvePath(path)
	if err != nil {
		return err
	}
	raw, err := readFile(abs)
	if err != nil {
		return err
	}
	text, _, _, err := decode(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", abs, err)
	}
	lines, _ := splitLines(text)
	rev := snapshotRev(abs, raw)
	if err := storeSnapshot(rev, raw); err != nil {
		return fmt.Errorf("store snapshot: %w", err)
	}
	if !hasRange {
		from, to = 1, len(lines)
	}
	if len(lines) == 0 {
		from, to = 1, 0
	}
	if from < 1 || to > len(lines) || (len(lines) > 0 && from > to) {
		return fmt.Errorf("range %d:%d is outside the file (%d lines)", from, to, len(lines))
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	fmt.Fprintf(out, "file %s\n", abs)
	fmt.Fprintf(out, "rev %s\n", rev)
	fmt.Fprintf(out, "lines %d\n", len(lines))
	if !hasRange && len(lines) > readCap {
		to = readCap
		fmt.Fprintf(out, "showing %d..%d\n", from, to)
		fmt.Fprintf(out, "truncated %d/%d\n", to, len(lines))
	} else if hasRange {
		fmt.Fprintf(out, "showing %d..%d\n", from, to)
	}
	for n := from; n <= to; n++ {
		fmt.Fprintf(out, "%d|%s\n", n, lines[n-1])
	}
	return nil
}

func parseReadArgs(args []string) (path string, from, to int, hasRange bool, err error) {
	if len(args) < 1 || len(args) > 2 {
		return "", 0, 0, false, fmt.Errorf("read needs FILE [FROM:TO]")
	}
	path = args[0]
	if len(args) == 1 {
		return path, 0, 0, false, nil
	}
	a, b, ok := strings.Cut(args[1], ":")
	if !ok {
		return "", 0, 0, false, fmt.Errorf("range %q must be FROM:TO", args[1])
	}
	from, err = strconv.Atoi(a)
	if err != nil || from < 1 {
		return "", 0, 0, false, fmt.Errorf("bad range start %q", a)
	}
	to, err = strconv.Atoi(b)
	if err != nil || to < from {
		return "", 0, 0, false, fmt.Errorf("bad range end %q", b)
	}
	return path, from, to, true, nil
}

func cmdStat(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("stat needs FILE")
	}
	abs, err := resolvePath(args[0])
	if err != nil {
		return err
	}
	raw, err := readFile(abs)
	if err != nil {
		return err
	}
	text, _, _, err := decode(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", abs, err)
	}
	lines, _ := splitLines(text)
	rev := snapshotRev(abs, raw)
	if err := storeSnapshot(rev, raw); err != nil {
		return fmt.Errorf("store snapshot: %w", err)
	}
	fmt.Printf("file %s\n", abs)
	fmt.Printf("rev %s\n", rev)
	fmt.Printf("lines %d\n", len(lines))
	fmt.Printf("bytes %d\n", len(raw))
	return nil
}

func cmdPatch(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("patch reads the plan from stdin and takes no arguments")
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	plan, err := parsePlan(string(b))
	if err != nil {
		return err
	}
	abs, err := resolvePath(plan.File)
	if err != nil {
		return err
	}
	res, err := applyPlan(abs, plan, string(b))
	if err != nil {
		return err
	}
	fmt.Print(res.Diff)
	for _, span := range res.Spans {
		fmt.Printf("span %s\n", span)
	}
	return nil
}

func cmdUndo(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("undo needs FILE")
	}
	abs, err := resolvePath(args[0])
	if err != nil {
		return err
	}
	entries, err := readJournal()
	if err != nil {
		return err
	}
	var last *JournalEntry
	for i := range entries {
		if entries[i].Path == abs {
			last = &entries[i]
		}
	}
	if last == nil {
		return fmt.Errorf("no transaction for %s", abs)
	}
	lock, err := lockPath(abs)
	if err != nil {
		return err
	}
	defer lock.Close()
	current, err := readFile(abs)
	if err != nil {
		return err
	}
	if got := snapshotRev(abs, current); got != last.Post {
		return fmt.Errorf("%s changed since that edit (%s now, %s after the edit); not undoing", abs, got, last.Post)
	}
	pre, err := loadSnapshot(last.Pre)
	if err != nil {
		return err
	}
	if got := snapshotRev(abs, pre); got != last.Pre {
		return fmt.Errorf("journal entry for %s points at snapshot %s, which belongs to another file", abs, last.Pre)
	}
	if err := atomicWrite(abs, pre, fileMode(abs)); err != nil {
		return err
	}
	if err := appendJournal(JournalEntry{
		Time: now(),
		Path: abs,
		Pre:  last.Post,
		Post: last.Pre,
		Plan: "undo",
	}); err != nil {
		return fmt.Errorf("wrote the file but could not journal the undo: %w", err)
	}
	fmt.Printf("undone %s %s -> %s\n", abs, last.Post, last.Pre)
	return nil
}

func cmdLog(args []string) error {
	var filter string
	if len(args) > 1 {
		return fmt.Errorf("log takes at most one FILE")
	}
	if len(args) == 1 {
		abs, err := resolvePath(args[0])
		if err != nil {
			return err
		}
		filter = abs
	}
	entries, err := readJournal()
	if err != nil {
		return err
	}
	shown := 0
	for i := len(entries) - 1; i >= 0 && shown < 20; i-- {
		e := entries[i]
		if filter != "" && e.Path != filter {
			continue
		}
		fmt.Printf("%s  %s  %s -> %s\n", e.Time, e.Path, e.Pre, e.Post)
		shown++
	}
	if shown == 0 {
		fmt.Println("no transactions")
	}
	return nil
}
