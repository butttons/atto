package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// resolvePath turns an agent-supplied path into the canonical absolute path
// used in snapshot ids and journal entries. Symlinks are resolved so a read
// through a link and a patch through its target share one identity.
func resolvePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("empty path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs), nil
}

// decode strips a UTF-8 BOM and normalises CRLF to LF. The line model is LF
// everywhere; the original shape is restored on write.
func decode(data []byte) (text string, bom bool, crlf bool, err error) {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		bom = true
		data = data[3:]
	}
	if !utf8.Valid(data) {
		return "", false, false, errors.New("file is not valid UTF-8 (atto edits text, not bytes)")
	}
	text = string(data)
	if strings.Contains(text, "\r\n") {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		crlf = true
	}
	return text, bom, crlf, nil
}

// encode is the inverse of decode. trailing says whether the file ends with a
// line break; it is a property of the file, not of any edited line.
func encode(lines []string, bom, crlf, trailing bool) []byte {
	text := strings.Join(lines, "\n")
	if trailing && len(lines) > 0 {
		text += "\n"
	}
	if crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	if bom {
		return append([]byte{0xEF, 0xBB, 0xBF}, []byte(text)...)
	}
	return []byte(text)
}

// splitLines follows the same convention as read: a trailing newline does not
// create a phantom final line. "" has zero lines; "\n" has one empty line.
func splitLines(text string) ([]string, bool) {
	if text == "" {
		return nil, false
	}
	trailing := strings.HasSuffix(text, "\n")
	if trailing {
		text = text[:len(text)-1]
	}
	return strings.Split(text, "\n"), trailing
}

// snapshotRev binds a snapshot to a path so a revision from one file cannot be
// replayed against another.
func snapshotRev(abs string, raw []byte) string {
	h := sha256.New()
	h.Write([]byte(abs))
	h.Write([]byte{0})
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// atomicWrite replaces the file without any reader observing a partial image.
// The temp file lives in the same directory so rename is atomic.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".atto-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

func fileMode(path string) fs.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return 0o644
	}
	return info.Mode().Perm()
}

func readFile(path string) ([]byte, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	return os.ReadFile(path)
}
