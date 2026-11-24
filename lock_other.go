//go:build !unix

package main

import "os"

// The Unix build uses flock. Elsewhere the atomic rename still prevents
// partial files; there is no advisory lock, so concurrent patches can race.
type fileLock struct{ f *os.File }

func lockPath(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) Close() { l.f.Close() }
