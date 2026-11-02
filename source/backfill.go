package main

import (
	"encoding/csv"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// backfillToday puts any of today's strikes that are missing from the
// database back in, from today's csv\strikes_YYYY-MM-DD.csv. This covers
// strikes removed by older versions (which kept only the last hour) and
// anything lost in a crash before it reached the database. Strikes already
// stored are not added twice.
func (e *Engine) backfillToday(now time.Time) {
	path := filepath.Join(e.cfg.Path(e.cfg.CSVDir), "strikes_"+now.Format("2006-01-02")+".csv")
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, time.Local).Unix()
	have, err := e.store.StrikeKeys(midnight)
	if err != nil {
		warnf("backfill: %v", err)
		return
	}
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return
	}
	col := map[string]int{}
	for i, h := range head {
		col[h] = i
	}
	for _, need := range []string{"unix_time", "kind", "nsd_type", "bearing_deg", "bearing_aligned_deg", "distance", "corr", "trac_alignment"} {
		if _, ok := col[need]; !ok {
			warnf("backfill: %s has no %s column; skipped", path, need)
			return
		}
	}
	var add []StrikeRow
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(rec) < len(head) || rec[col["kind"]] != "strike" {
			continue
		}
		ut, err1 := strconv.ParseFloat(rec[col["unix_time"]], 64)
		bearing, err2 := strconv.ParseFloat(rec[col["bearing_deg"]], 64)
		dir, err3 := strconv.Atoi(rec[col["bearing_aligned_deg"]])
		dist, err4 := strconv.Atoi(rec[col["distance"]])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		ts := int64(ut) // stored timestamps are whole seconds, as when received
		if ts < midnight {
			continue
		}
		key := [3]int64{ts, int64(dir), int64(dist)}
		if have[key] > 0 {
			have[key]--
			continue
		}
		corr, _ := strconv.Atoi(rec[col["corr"]])
		align, _ := strconv.Atoi(rec[col["trac_alignment"]])
		tb := math.Mod(bearing+float64(align), 360)
		if tb < 0 {
			tb += 360
		}
		tb = math.Round(tb*10) / 10
		row := StrikeRow{TS: ts, Dir: dir, Dist: dist, Corr: corr, Type: rec[col["nsd_type"]], Bearing: &tb}
		if e.cfg.hasPos {
			km := float64(dist)
			if e.cfg.DistUnit == "mi" {
				km *= 1.609344
			}
			la, lo := destination(e.cfg.lat, e.cfg.lon, tb, km)
			row.Lat, row.Lon = &la, &lo
		}
		add = append(add, row)
	}
	if len(add) == 0 {
		return
	}
	if err := e.store.InsertStrikes(add); err != nil {
		warnf("backfill: %v", err)
		return
	}
	logf("restored %d of today's strikes from %s", len(add), filepath.Base(path))
}
