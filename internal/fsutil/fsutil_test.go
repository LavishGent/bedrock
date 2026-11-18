package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadRegular(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok")
	if err := os.WriteFile(ok, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadRegular(ok, 10); err != nil || string(b) != "hello" {
		t.Fatalf("regular file: %q %v", b, err)
	}
	if _, err := ReadRegular(ok, 3); err == nil {
		t.Fatal("oversized file accepted")
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip("mkfifo unsupported:", err)
	}
	done := make(chan error, 1)
	go func() { _, err := ReadRegular(fifo, 10); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("fifo: err = %v, want ErrNotRegular", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadRegular blocked on a FIFO without a writer")
	}
	if _, err := ReadRegular(dir, 10); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("directory: err = %v, want ErrNotRegular", err)
	}
}
