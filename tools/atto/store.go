package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// attoHome is where snapshots and the journal live. State, not cache: undo
// depends on it surviving.
func attoHome() string {
	if h := os.Getenv("ATTO_HOME"); h != "" {
		return h
	}
	if s := os.Getenv("XDG_STATE_HOME"); s != "" {
		return filepath.Join(s, "atto")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".atto"
	}
	return filepath.Join(home, ".local", "state", "atto")
}

func snapshotPath(rev string) string {
	return filepath.Join(attoHome(), "snapshots", rev)
}

func storeSnapshot(rev string, raw []byte) error {
	p := snapshotPath(rev)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	// Content-addressed and verified on load, so a torn blob is caught by the
	// rev check instead of being trusted. No fsync here: this is on the read
	// path, and the durable write is the target file, not the cache.
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		os.Remove(p)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(p)
		return err
	}
	return nil
}

func loadSnapshot(rev string) ([]byte, error) {
	raw, err := os.ReadFile(snapshotPath(rev))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("snapshot %s is not in the store; re-read the file", rev)
		}
		return nil, err
	}
	return raw, nil
}

type JournalEntry struct {
	Time string `json:"time"`
	Path string `json:"path"`
	Pre  string `json:"pre"`
	Post string `json:"post"`
	Plan string `json:"plan"`
}

func journalPath() string {
	return filepath.Join(attoHome(), "journal.jsonl")
}

func appendJournal(e JournalEntry) error {
	if err := os.MkdirAll(attoHome(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(journalPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readJournal() ([]JournalEntry, error) {
	f, err := os.Open(journalPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []JournalEntry
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for s.Scan() {
		var e JournalEntry
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, s.Err()
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
