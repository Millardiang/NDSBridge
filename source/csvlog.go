package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const extHeader = "utc_time,local_time,unix_time,nexstorm_time,kind,type,polarity,nsd_type,bearing_deg,bearing_aligned_deg,distance,corr,trac_alignment,count,f7,f8,f9,f10,f12\r\n"

// csvLog appends strike rows to daily CSV files. Rows are queued in memory
// and written once a second; if a file is locked (e.g. open in Excel) the
// rows stay queued and are written as soon as the file is free, so nothing
// is lost and the program never stops because of it.
type csvLog struct {
	cfg     *Config
	dir     string
	pending map[string]*pendingFile
	order   []string
	dropped int
}

type pendingFile struct {
	header string
	lines  []string
	since  time.Time
}

const maxQueued = 200000

func newCSVLog(cfg *Config) *csvLog {
	return &csvLog{cfg: cfg, dir: cfg.Path(cfg.CSVDir), pending: map[string]*pendingFile{}}
}

func (c *csvLog) queue(path, header, line string) {
	p, ok := c.pending[path]
	if !ok {
		p = &pendingFile{header: header, since: time.Now()}
		c.pending[path] = p
	}
	if c.queued() >= maxQueued {
		c.dropped++
		warnEvery("csv-overflow", time.Minute, "CSV queue full (%d rows waiting); dropping rows - check the csv folder is writable", maxQueued)
		return
	}
	p.lines = append(p.lines, line)
}

func (c *csvLog) queued() int {
	n := 0
	for _, p := range c.pending {
		n += len(p.lines)
	}
	return n
}

func f1(v float64) string { return fmt.Sprintf("%.1f", v) }

// Add records one strike (or noise) record.
func (c *csvLog) Add(r *StrikeRec, align int) {
	if r.Kind != KindStrike && !(r.Kind == KindNoise && (c.cfg.CSVNoise)) {
		return
	}
	recv := r.Received
	nxTime, nxOK := r.NexStormTime(time.Local)

	if c.cfg.CSVExtended {
		kind, typ, pol, nsd := "strike", "CG", "+", r.NSDType()
		if r.IsIC() {
			typ = "IC"
		}
		if r.Negative() {
			pol = "-"
		}
		bearing, aligned := f1(float64(r.BearingT())/10), itoa(alignedBearing(r.BearingT(), align))
		dist := itoa(r.Distance())
		if r.Kind == KindNoise {
			kind, typ, pol, nsd, bearing, aligned, dist = "noise", "", "", "", "", "", ""
		}
		nx := ""
		if nxOK {
			nx = nxTime.Format("2006-01-02 15:04:05")
		}
		line := strings.Join([]string{
			recv.UTC().Format("2006-01-02 15:04:05.000"),
			recv.Format("2006-01-02 15:04:05.000"),
			fmt.Sprintf("%.3f", float64(recv.UnixMilli())/1000),
			nx, kind, typ, pol, nsd, bearing, aligned, dist,
			itoa(r.Corr()), itoa(align), itoa(r.Count()),
			itoa(r.F[7]), itoa(r.F[8]), itoa(r.F[9]), itoa(r.F[10]), itoa(r.F[12]),
		}, ",") + "\r\n"
		path := filepath.Join(c.dir, "strikes_"+recv.Format("2006-01-02")+".csv")
		c.queue(path, extHeader, line)
	}

	if c.cfg.CSVNSList {
		// Exactly SonotaCo NSList's format:
		// %04d,%02d,%02d,%02d,%02d,%02d,"%s",%.1f,%.0f   (bearing, distance)
		y, mo, d, sec := r.F[1], r.F[2], r.F[3], r.F[4]
		if !nxOK {
			t := recv
			y, mo, d = t.Year(), int(t.Month()), t.Day()
			sec = t.Hour()*3600 + t.Minute()*60 + t.Second()
		}
		bearing, dist := float64(r.BearingT())*0.1, float64(r.Distance())
		if r.Kind == KindNoise {
			bearing, dist = 0, 0
		}
		suffix := ""
		if c.cfg.CSVNSListMile {
			dist *= 0.6213712
			suffix = "M"
		}
		line := fmt.Sprintf("%04d,%02d,%02d,%02d,%02d,%02d,\"%s\",%.1f,%.0f\r\n",
			y, mo, d, sec/3600, (sec/60)%60, sec%60, r.NSListType(), bearing, dist)
		path := filepath.Join(c.dir, fmt.Sprintf("L%04d%02d%02d%s.csv", y, mo, d, suffix))
		c.queue(path, "", line)
	}
}

// Flush writes whatever is queued. Returns the files that changed.
func (c *csvLog) Flush() []string {
	var changed []string
	paths := make([]string, 0, len(c.pending))
	for p := range c.pending {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		p := c.pending[path]
		if len(p.lines) == 0 {
			delete(c.pending, path)
			continue
		}
		if err := appendFile(path, p.header, strings.Join(p.lines, "")); err != nil {
			warnEvery("csv:"+path, 5*time.Minute, "cannot write %s (%v) - %d rows kept and will be retried; is it open in Excel?", path, err, len(p.lines))
			continue
		}
		clearWarn("csv:"+path, "%s writable again; queued rows written", path)
		changed = append(changed, path)
		delete(c.pending, path)
	}
	return changed
}
