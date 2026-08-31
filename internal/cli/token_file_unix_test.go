//go:build !windows

package cli

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadTokenFileRejectsFIFOWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "token-fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := readTokenFile(fifo)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("readTokenFile accepted a FIFO")
		}
	case <-time.After(time.Second):
		t.Fatal("readTokenFile blocked while opening a FIFO")
	}
}
