//go:build !windows

package main

// Non-Windows stand-in for the NexStorm shared memory, used only for testing:
// the "block" is a small file in $TMPDIR/nxipc/<name>.

import (
	"os"
	"path/filepath"
	"syscall"
)

type sharedMem struct {
	path string
}

func ipcPath(name string) string {
	d := filepath.Join(os.TempDir(), "nxipc")
	os.MkdirAll(d, 0o755)
	return filepath.Join(d, name)
}

func openSharedMem(name string) (*sharedMem, error) {
	return &sharedMem{path: ipcPath(name)}, nil
}

func (m *sharedMem) Read(buf []byte) string {
	f, err := os.Open(m.path)
	if err != nil {
		return ""
	}
	defer f.Close()
	n, _ := f.Read(buf)
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return string(buf[:i])
		}
	}
	return string(buf[:n])
}

func (m *sharedMem) Close() {}

func writeSharedMem(name, msg string) error {
	p := ipcPath(name)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(msg), 0), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func highResTimers() {}

func singleInstance(lock string) (bool, error) {
	p := filepath.Join(os.TempDir(), lock+".lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false, nil
	}
	return true, nil
}
