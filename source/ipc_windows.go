//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procOpenFileMappingW  = kernel32.NewProc("OpenFileMappingW")
	procCreateFileMapping = kernel32.NewProc("CreateFileMappingW")
	procMapViewOfFile     = kernel32.NewProc("MapViewOfFile")
	procUnmapViewOfFile   = kernel32.NewProc("UnmapViewOfFile")
	procCreateMutexW      = kernel32.NewProc("CreateMutexW")
	procGetTickCount64    = kernel32.NewProc("GetTickCount64")
	winmm                 = syscall.NewLazyDLL("winmm.dll")
	procTimeBeginPeriod   = winmm.NewProc("timeBeginPeriod")
)

const (
	fileMapRead    = 0x0004
	fileMapWrite   = 0x0002
	pageReadWrite  = 0x04
	errAlreadyExst = 183
	ipcSize        = 1024
)

// sharedMem is a mapping of a NexStorm IPC block. It is opened ONCE and kept
// for the life of the program (NSBackend re-opened it every few ms and never
// closed it, leaking handles and address space until it fell over).
type sharedMem struct {
	name   string
	handle syscall.Handle
	addr   uintptr
	size   int
}

func openSharedMem(name string) (*sharedMem, error) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	// Prefer attaching to NexStorm's block. If NexStorm is not running yet,
	// create the block ourselves (as NSList/NSBackend always did) so NexStorm
	// attaches to it when it starts.
	// Read-only access is all we need, and it still works when NexStorm runs
	// elevated and we do not.
	h, _, oerr := procOpenFileMappingW.Call(fileMapRead, 0, uintptr(unsafe.Pointer(n)))
	if h == 0 {
		h, _, err = procCreateFileMapping.Call(uintptr(syscall.InvalidHandle), 0, pageReadWrite, 0, ipcSize, uintptr(unsafe.Pointer(n)))
		if h == 0 {
			return nil, fmt.Errorf("cannot open NexStorm shared memory %s (open: %v, create: %v). "+
				"If NexStorm runs as administrator, run NSD Bridge as administrator too", name, oerr, err)
		}
	}
	addr, _, err := procMapViewOfFile.Call(h, fileMapRead, 0, 0, 0)
	if addr == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, fmt.Errorf("MapViewOfFile(%s): %v", name, err)
	}
	// NexStorm creates a 100 byte block; a view is at least one 4 KB page, so
	// reading 1024 bytes is always safe.
	size := ipcSize
	return &sharedMem{name: name, handle: syscall.Handle(h), addr: addr, size: size}, nil
}

// Read copies the NUL-terminated string currently in the block.
func (m *sharedMem) Read(buf []byte) string {
	n := len(buf)
	if n > m.size {
		n = m.size
	}
	src := unsafe.Slice((*byte)(unsafe.Pointer(m.addr)), n)
	copy(buf[:n], src)
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return string(buf[:i])
		}
	}
	return string(buf[:n])
}

func (m *sharedMem) Close() {
	if m.addr != 0 {
		procUnmapViewOfFile.Call(m.addr)
		m.addr = 0
	}
	if m.handle != 0 {
		syscall.CloseHandle(m.handle)
		m.handle = 0
	}
}

// writeSharedMem is used by the simulator.
func writeSharedMem(name, msg string) error {
	n, _ := syscall.UTF16PtrFromString(name)
	h, _, err := procCreateFileMapping.Call(uintptr(syscall.InvalidHandle), 0, pageReadWrite, 0, ipcSize, uintptr(unsafe.Pointer(n)))
	if h == 0 {
		return err
	}
	// keep handle open for process lifetime so the block persists
	addr, _, err := procMapViewOfFile.Call(h, fileMapWrite, 0, 0, 0)
	if addr == 0 {
		return err
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(addr)), ipcSize)
	b := append([]byte(msg), 0)
	copy(dst, b)
	procUnmapViewOfFile.Call(addr)
	simHandles[name] = syscall.Handle(h)
	return nil
}

var simHandles = map[string]syscall.Handle{}

func highResTimers() {
	procTimeBeginPeriod.Call(1)
}

// singleInstance returns false if another copy is already running with the
// same lock name.
func singleInstance(lock string) (bool, error) {
	n, _ := syscall.UTF16PtrFromString(`Local\` + lock)
	h, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(n)))
	if h == 0 {
		return false, err
	}
	if err == syscall.Errno(errAlreadyExst) {
		return false, nil
	}
	return true, nil // handle deliberately kept open until exit
}
