package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// nxutil -extract writes the selected fields in this fixed order whatever
// order they are given in (worked out from nxutil 1.1 itself):
//
//	S seconds since midnight, B bearing (2 dp), D distance, C corrected
//	distance, T type (0 CG / 1 IC), P polarity (0 + / 1 -), X, Y raw
//	coordinates, K, L corrected coordinates
var nxFields = []string{"seconds", "bearing_deg", "distance", "distance_corrected", "type", "polarity", "x", "y", "x_corrected", "y_corrected"}

const exportHeader = "utc_time,local_time,date,time,seconds,bearing_deg,distance,distance_corrected,type,polarity,x,y,x_corrected,y_corrected\r\n"

func archiveCandidates(cfg *Config) []string {
	if cfg.ArchiveDir != "" {
		return []string{cfg.Path(cfg.ArchiveDir)}
	}
	var c []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "ProgramW6432"} {
		pf := os.Getenv(env)
		if pf == "" {
			continue
		}
		for _, p := range []string{`Astrogenic\NexStormLite\NXArchive`, `Astrogenic\NexStorm\NXArchive`,
			`Astrogenic\NexStormLite\archive`, `Astrogenic\NexStorm\archive`, `Astrogenic\NexStormLite`, `Astrogenic\NexStorm`} {
			c = append(c, filepath.Join(pf, p))
		}
	}
	c = append(c, `C:\NexStorm\NXArchive`, cfg.BaseDir)
	seen := map[string]bool{}
	out := c[:0]
	for _, d := range c {
		k := strings.ToLower(d)
		if !seen[k] {
			seen[k] = true
			out = append(out, d)
		}
	}
	return out
}

func findArchive(cfg *Config, day time.Time) (string, []string) {
	name := day.Format("20060102") + ".nex"
	var looked []string
	for _, dir := range archiveCandidates(cfg) {
		p := filepath.Join(dir, name)
		looked = append(looked, dir)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, looked
		}
	}
	return "", looked
}

func findNXUtil(cfg *Config) (string, error) {
	cands := []string{cfg.Path(cfg.NXUtil), filepath.Join(cfg.BaseDir, "nxutil.exe")}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	if p, err := exec.LookPath("nxutil.exe"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("nxutil.exe not found (looked for %s) - set nxutil= in %s", strings.Join(cands, ", "), iniName)
}

func runNX(cfg *Config, nx, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ExportTimout)*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, nx, args...)
	} else {
		cmd = exec.CommandContext(ctx, "wine", append([]string{nx}, args...)...) // for testing on Linux
	}
	cmd.Dir = dir
	hideWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), fmt.Errorf("nxutil timed out after %ds", cfg.ExportTimout)
	}
	return out.String(), err
}

// nxRow is one archive record in nxutil's field order.
type nxRow [10]string

// readNexNative reads a .nex archive directly. Layout (as nxutil 1.1 reads
// it): 500 byte header starting "NexStorm historical archive", a 32 byte
// state block, then 64 byte records:
//
//	0x00 float64 bearing (negative = noise)   0x10 uint32 seconds since midnight
//	0x14 int32 distance   0x18 int32 corrected distance
//	0x1c int16 type       0x1e int16 polarity
//	0x28,0x2c int32 x,y   0x30,0x34 int32 corrected x,y
func readNexNative(path string, noise bool) ([]nxRow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 532 || !bytes.HasPrefix(b, []byte("NexStorm historical archive")) {
		return nil, fmt.Errorf("%s is not a NexStorm archive", filepath.Base(path))
	}
	var out []nxRow
	for off := 532; off+64 <= len(b); off += 64 {
		r := b[off : off+64]
		bearing := math.Float64frombits(binary.LittleEndian.Uint64(r[0:]))
		if math.IsNaN(bearing) || (bearing < 0 && !noise) {
			continue
		}
		secs := binary.LittleEndian.Uint32(r[0x10:])
		if secs > 86400 {
			continue
		}
		i32 := func(o int) string { return strconv.Itoa(int(int32(binary.LittleEndian.Uint32(r[o:])))) }
		i16 := func(o int) string { return strconv.Itoa(int(int16(binary.LittleEndian.Uint16(r[o:])))) }
		out = append(out, nxRow{strconv.Itoa(int(secs)), strconv.FormatFloat(bearing, 'f', 2, 64),
			i32(0x14), i32(0x18), i16(0x1c), i16(0x1e), i32(0x28), i32(0x2c), i32(0x30), i32(0x34)})
	}
	return out, nil
}

// extractWithNX runs nxutil on the copy in dir and returns its rows.
func extractWithNX(cfg *Config, nx, dir, id string, noise bool) ([]nxRow, error) {
	args := []string{"-extract", "-i", id, "-f", "ALL", "-o", "out.txt"}
	if !noise {
		args = append(args, "-validate")
	}
	outFile := filepath.Join(dir, "out.txt")
	log, err := runNX(cfg, nx, dir, args...)
	if bad(log, outFile, err) {
		if strings.Contains(log, "check your parameters") && err == nil {
			// nxutil says this when an archive simply has no records
			if rows, nerr := readNexNative(filepath.Join(dir, id+".nex"), noise); nerr == nil && len(rows) == 0 {
				return nil, nil
			}
		}
		logf("export %s: nxutil reported a problem, running -repair on a copy (%s)", id, oneLine(log))
		rlog, _ := runNX(cfg, nx, dir, "-repair", id)
		debugf("repair output: %s", oneLine(rlog))
		os.Remove(outFile)
		log, err = runNX(cfg, nx, dir, args...)
		if bad(log, outFile, err) {
			return nil, fmt.Errorf("nxutil could not extract %s: %v %s", id, err, oneLine(log))
		}
	}
	raw, err := os.ReadFile(outFile)
	if err != nil {
		return nil, err
	}
	var rows []nxRow
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(strings.TrimRight(line, ","), ",")
		if len(f) != len(nxFields) {
			debugf("export %s: skipped line %q", id, line)
			continue
		}
		var r nxRow
		for i := range f {
			r[i] = strings.TrimSpace(f[i])
		}
		rows = append(rows, r)
	}
	// nxutil 1.1 reads with a while(!feof) loop and so repeats the last
	// record of every archive; drop that copy.
	if n := len(rows); n >= 2 && rows[n-1] == rows[n-2] {
		rows = rows[:n-1]
	}
	return rows, nil
}

// exportDay turns one NexStorm archive into a CSV. The archive is copied to a
// temporary folder first so nxutil never touches the file NexStorm is
// writing, and so a repair (if needed) is done on the copy. If nxutil is
// missing or fails, the built-in reader is used instead.
func exportDay(cfg *Config, nx string, day time.Time, outDir string, noise bool) (string, int, error) {
	src, looked := findArchive(cfg, day)
	if src == "" {
		return "", 0, fmt.Errorf("no archive %s.nex (looked in %s)", day.Format("20060102"), strings.Join(looked, "; "))
	}
	tmp, err := os.MkdirTemp("", "nsdexport")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(tmp)
	id := day.Format("20060102")
	copyPath := filepath.Join(tmp, id+".nex")
	if err := copyPlain(src, copyPath); err != nil {
		return "", 0, fmt.Errorf("copy archive: %v", err)
	}
	var rows []nxRow
	method := "nxutil"
	if nx != "" {
		rows, err = extractWithNX(cfg, nx, tmp, id, noise)
	} else {
		err = fmt.Errorf("nxutil.exe not available")
	}
	if err != nil {
		if nx != "" {
			warnf("export %s: %v - using built-in reader instead", id, err)
		}
		method = "built-in reader"
		if rows, err = readNexNative(copyPath, noise); err != nil {
			return "", 0, err
		}
	}
	var b strings.Builder
	b.WriteString(exportHeader)
	for _, f := range rows {
		secs, err := strconv.Atoi(f[0])
		if err != nil || secs < 0 || secs > 86400 {
			continue
		}
		lt := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local).Add(time.Duration(secs) * time.Second)
		typ := map[string]string{"0": "CG", "1": "IC"}[f[4]]
		pol := map[string]string{"0": "+", "1": "-"}[f[5]]
		if typ == "" || strings.HasPrefix(f[1], "-") {
			typ, pol = "noise", ""
		}
		cols := []string{lt.UTC().Format("2006-01-02 15:04:05"), lt.Format("2006-01-02 15:04:05"),
			lt.Format("2006-01-02"), lt.Format("15:04:05"), f[0], f[1], f[2], f[3], typ, pol, f[6], f[7], f[8], f[9]}
		b.WriteString(strings.Join(cols, ",") + "\r\n")
	}
	dst := filepath.Join(outDir, "nex_"+day.Format("2006-01-02")+".csv")
	if err := writeFileAtomic(dst, []byte(b.String())); err != nil {
		return "", 0, err
	}
	debugf("export %s via %s", id, method)
	return dst, len(rows), nil
}

func bad(log, outFile string, err error) bool {
	l := strings.ToLower(log)
	if err != nil || strings.Contains(l, "failed") || strings.Contains(l, "could not") {
		return true
	}
	_, serr := os.Stat(outFile)
	return serr != nil
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func copyPlain(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// runExport exports every day from..to inclusive. If single is set, all days
// go into that one CSV as well.
func runExport(cfg *Config, from, to time.Time, single string, noise bool) ([]string, error) {
	nx, err := findNXUtil(cfg)
	if err != nil {
		warnf("%v - using the built-in archive reader", err)
		nx = ""
	}
	noise = noise || cfg.ExportNoise
	outDir := cfg.Path(cfg.ExportDir)
	var files []string
	var failures []string
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		p, n, err := exportDay(cfg, nx, d, outDir, noise)
		if err != nil {
			warnf("export %s: %v", d.Format("2006-01-02"), err)
			failures = append(failures, d.Format("2006-01-02"))
			continue
		}
		logf("exported %s: %d records -> %s", d.Format("2006-01-02"), n, p)
		files = append(files, p)
	}
	if single != "" && len(files) > 0 {
		var b strings.Builder
		b.WriteString(exportHeader)
		for _, f := range files {
			data, _ := os.ReadFile(f)
			body := strings.TrimPrefix(string(data), exportHeader)
			b.WriteString(body)
		}
		if !filepath.IsAbs(single) {
			single = filepath.Join(outDir, single)
		}
		if err := writeFileAtomic(single, []byte(b.String())); err != nil {
			return files, err
		}
		logf("combined CSV written to %s", single)
		files = append(files, single)
	}
	if len(failures) > 0 {
		return files, fmt.Errorf("%d day(s) not exported: %s", len(failures), strings.Join(failures, ", "))
	}
	return files, nil
}
