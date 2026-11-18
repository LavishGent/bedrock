// Package fsutil reads small input files without trusting their type: a FIFO,
// device or other non-regular file is rejected instead of blocking, and the
// size is bounded.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// ErrNotRegular reports a path that is not a regular file.
var ErrNotRegular = errors.New("not a regular file")

// ReadRegular reads path if it is a regular file of at most max bytes. It
// opens with O_NONBLOCK so a FIFO without a writer cannot block, and checks
// the type of the opened file (not just the path) before reading.
func ReadRegular(path string, max int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w (%s)", path, ErrNotRegular, st.Mode().Type())
	}
	if st.Size() > max {
		return nil, fmt.Errorf("%s: %d bytes exceeds the %d-byte limit", path, st.Size(), max)
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s: exceeds the %d-byte limit", path, max)
	}
	return b, nil
}
