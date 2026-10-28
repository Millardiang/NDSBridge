package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---- logging -------------------------------------------------------------

type rotatingLog struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	f       *os.File
	size    int64
	echo    io.Writer
	recent  []string // last lines, for the status page
}

func newRotatingLog(path string, maxMB int, echo io.Writer) (*rotatingLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l := &rotatingLog{path: path, maxSize: int64(maxMB) << 20, echo: echo}
	return l, l.open()
}

func (l *rotatingLog) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	st, _ := f.Stat()
	l.f = f
	if st != nil {
		l.size = st.Size()
	}
	return nil
}

func (l *rotatingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.echo != nil {
		l.echo.Write(p)
	}
	line := strings.TrimRight(string(p), "\r\n")
	l.recent = append(l.recent, line)
	if len(l.recent) > 200 {
		l.recent = l.recent[len(l.recent)-200:]
	}
	if l.f == nil {
		if err := l.open(); err != nil {
			return len(p), nil // never let logging take the program down
		}
	}
	if l.size+int64(len(p)) > l.maxSize {
		l.f.Close()
		for i := 4; i >= 1; i-- {
			os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
		}
		os.Rename(l.path, l.path+".1")
		if err := l.open(); err != nil {
			l.f = nil
			return len(p), nil
		}
		l.size = 0
	}
	out := strings.ReplaceAll(line, "\n", "\r\n") + "\r\n"
	n, _ := l.f.WriteString(out)
	l.size += int64(n)
	return len(p), nil
}

func (l *rotatingLog) Recent(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > len(l.recent) {
		n = len(l.recent)
	}
	return append([]string(nil), l.recent[len(l.recent)-n:]...)
}

var (
	logger  = log.New(os.Stderr, "", log.LstdFlags)
	logSink *rotatingLog
	verbose bool
)

func logf(format string, a ...any)  { logger.Printf("INFO  "+format, a...) }
func warnf(format string, a ...any) { logger.Printf("WARN  "+format, a...) }
func errf(format string, a ...any)  { logger.Printf("ERROR "+format, a...) }
func debugf(format string, a ...any) {
	if verbose {
		logger.Printf("DEBUG "+format, a...)
	}
}

// rate-limited warnings so a persistent problem (e.g. a locked file) does not
// flood the log.
var (
	warnOnceMu sync.Mutex
	warnOnceAt = map[string]time.Time{}
)

func warnEvery(key string, every time.Duration, format string, a ...any) {
	warnOnceMu.Lock()
	last, ok := warnOnceAt[key]
	now := time.Now()
	if ok && now.Sub(last) < every {
		warnOnceMu.Unlock()
		return
	}
	warnOnceAt[key] = now
	warnOnceMu.Unlock()
	warnf(format, a...)
}

func clearWarn(key string, format string, a ...any) {
	warnOnceMu.Lock()
	_, had := warnOnceAt[key]
	delete(warnOnceAt, key)
	warnOnceMu.Unlock()
	if had && format != "" {
		logf(format, a...)
	}
}

// ---- safe file writing -----------------------------------------------------

// writeFileAtomic writes to a temp file and renames it over the target, so a
// web server or the Pi never sees a half-written file. On Windows the rename
// can fail for a moment if someone has the target open; retry, then fall back
// to a direct overwrite rather than losing the update.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	os.Chmod(tmpName, 0o644) // CreateTemp makes 0600; readers such as a web server need access
	var rerr error
	for i := 0; i < 20; i++ {
		if rerr = os.Rename(tmpName, path); rerr == nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	os.Remove(tmpName)
	if werr := os.WriteFile(path, data, 0o644); werr != nil {
		return fmt.Errorf("replace %s: %v (fallback write: %v)", path, rerr, werr)
	}
	return nil
}

// appendFile appends text, creating the file (and header) if needed.
func appendFile(path string, header string, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if header != "" {
		if st, err := f.Stat(); err == nil && st.Size() == 0 {
			if _, err := f.WriteString(header); err != nil {
				f.Close()
				return err
			}
		}
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, b)
}

func fmtUptime(d time.Duration) string {
	m := int(d.Minutes())
	return fmt.Sprintf("%d days %d hours %d min", m/1440, (m/60)%24, m%60)
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}
