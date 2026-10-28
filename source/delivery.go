package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Delivery copies changed output files to the mirror folder and/or uploads
// them by FTP. It runs on its own goroutine so a slow network share or FTP
// server can never hold up strike capture (NSBackend did FTP inline).
type Delivery struct {
	cfg     *Config
	mu      sync.Mutex
	dirty   map[string]bool // NSD files: mirror + FTP
	mirror  map[string]bool // CSV files: mirror only
	lastOK  time.Time
	lastErr string
}

func NewDelivery(cfg *Config) *Delivery {
	return &Delivery{cfg: cfg, dirty: map[string]bool{}, mirror: map[string]bool{}}
}

func (d *Delivery) enabled() bool { return d.cfg.FTPActive || d.cfg.MirrorDir != "" }

func (d *Delivery) Publish(paths ...string) {
	if !d.enabled() {
		return
	}
	d.mu.Lock()
	for _, p := range paths {
		d.dirty[p] = true
	}
	d.mu.Unlock()
}

func (d *Delivery) Mirror(paths ...string) {
	if d.cfg.MirrorDir == "" {
		return
	}
	d.mu.Lock()
	for _, p := range paths {
		d.mirror[p] = true
	}
	d.mu.Unlock()
}

func (d *Delivery) Run(ctx context.Context) {
	if !d.enabled() {
		return
	}
	t := time.NewTicker(time.Duration(d.cfg.WriteEvery) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			d.once()
			return
		case <-t.C:
			func() {
				defer recoverLog("delivery", nil)
				d.once()
			}()
		}
	}
}

func (d *Delivery) take() (nsd, csv []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for p := range d.dirty {
		nsd = append(nsd, p)
	}
	for p := range d.mirror {
		csv = append(csv, p)
	}
	d.dirty, d.mirror = map[string]bool{}, map[string]bool{}
	sort.Strings(nsd)
	sort.Strings(csv)
	return
}

func (d *Delivery) requeue(nsd []string) {
	d.mu.Lock()
	for _, p := range nsd {
		d.dirty[p] = true
	}
	d.mu.Unlock()
}

// relTarget keeps the data/reports/csv sub-folder in the mirror.
func (d *Delivery) relTarget(p string) string {
	rel, err := filepath.Rel(d.cfg.BaseDir, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Base(p)
	}
	return rel
}

func (d *Delivery) once() {
	nsd, csv := d.take()
	if len(nsd)+len(csv) == 0 {
		return
	}
	if d.cfg.MirrorDir != "" {
		var failed []string
		for _, p := range append(append([]string{}, nsd...), csv...) {
			dst := filepath.Join(d.cfg.Path(d.cfg.MirrorDir), d.relTarget(p))
			if err := copyFile(p, dst); err != nil {
				warnEvery("mirror", 10*time.Minute, "copying to mirror folder %s: %v (will retry)", d.cfg.MirrorDir, err)
				failed = append(failed, p)
			}
		}
		if len(failed) == 0 {
			clearWarn("mirror", "mirror folder reachable again")
		} else {
			d.mu.Lock()
			for _, p := range failed {
				if strings.HasSuffix(strings.ToLower(p), ".csv") {
					d.mirror[p] = true
				} else {
					d.dirty[p] = true
				}
			}
			d.mu.Unlock()
			if !d.cfg.FTPActive {
				return
			}
		}
	}
	if d.cfg.FTPActive && len(nsd) > 0 {
		if err := d.ftpUpload(nsd); err != nil {
			d.lastErr = err.Error()
			warnEvery("ftp", 10*time.Minute, "FTP upload to %s failed: %v (will retry)", d.cfg.FTPServer, err)
			d.requeue(nsd)
			return
		}
		d.lastOK = time.Now()
		clearWarn("ftp", "FTP uploads working again")
	}
}

func (d *Delivery) ftpUpload(paths []string) error {
	f, err := ftpDial(d.cfg.FTPServer, d.cfg.FTPUser, d.cfg.FTPPass)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Cwd(d.cfg.FTPPath); err != nil {
		return err
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		name := filepath.Base(p)
		if d.cfg.FTPRename {
			tmp := strings.TrimSuffix(name, filepath.Ext(name)) + ".tmp"
			if err := f.Stor(tmp, bytes.NewReader(b)); err != nil {
				return err
			}
			if err := f.Rename(tmp, name); err != nil {
				return err
			}
		} else if err := f.Stor(name, bytes.NewReader(b)); err != nil {
			return err
		}
	}
	return nil
}
