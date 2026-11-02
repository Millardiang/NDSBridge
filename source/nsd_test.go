package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseStrike(t *testing.T) {
	r, err := ParseStrike("1234,2026,10,28,31501,1805,42,42,10,20,30,40,0,1,0\x00junk")
	if err == nil {
		t.Fatalf("expected junk after NUL to be rejected by caller, got %+v", r)
	}
	r, err = ParseStrike("1234,2026,10,28,31501,1805,42,42,10,20,30,40,0,1,0")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindStrike || r.NSDType() != "cc+" || r.NSListType() != " +IC" || r.Distance() != 42 || r.BearingT() != 1805 {
		t.Fatalf("bad decode %+v %s %s", r, r.NSDType(), r.NSListType())
	}
	nt, ok := r.NexStormTime(time.UTC)
	if !ok || nt.Format("15:04:05") != "08:45:01" {
		t.Fatalf("time %v", nt)
	}
	hb, _ := ParseStrike("1235,2026,10,28,31502,-9,-9,15,-9,-9,-9,-9,-9,-9,-9")
	if hb.Kind != KindHeartbeat || hb.F[7] != 15 {
		t.Fatalf("heartbeat %+v", hb)
	}
	nz, _ := ParseStrike("1236,2026,10,28,31502,-1,-1,-1,-1,-1,-1,-1,-1,-1,-1")
	if nz.Kind != KindNoise {
		t.Fatalf("noise %+v", nz)
	}
	for _, bad := range []string{"", "1,2,3", "1,2026,10,28,31502,1x,0,0,0,0,0,0,0,0,0", "1,2026,10,28,31502,10,0,0,0,0,0,0,0,0"} {
		if _, err := ParseStrike(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if alignedBearing(3555, 10) != 5 || alignedBearing(100, 0) != 10 || alignedBearing(3599, 0) != 359 {
		t.Error("alignedBearing")
	}
}

func TestParseStorm(t *testing.T) {
	msg := "77,0,2026,10,28,30000,1,1,31000,2250,150,0,0,12,20,1,2,40,10,10,10,10"
	s, err := ParseStorm(msg, time.UTC, 10, true)
	if err != nil || s == nil {
		t.Fatal(err)
	}
	if s.ID != "1-1" || s.DirT != 2350 || s.Dist != 150 || s.Cat != 1 || s.Trend != 2 || s.Total != 40 || s.CurRate != 12 || s.MaxRate != 20 {
		t.Fatalf("%+v", s)
	}
	if time.Unix(s.Detect, 0).UTC().Format("15:04:05") != "08:20:00" {
		t.Fatal("detect time")
	}
	none, err := ParseStorm("78,0,2026,10,28,0,?,?,0,0,0,0,0,0,0,0,0,0,0,0,0,0", time.UTC, 0, false)
	if none != nil || err != nil {
		t.Fatal("expected no storm")
	}
}

// The report generated from the user's own NSD.sqlite must match the report
// NSBackend wrote from it (day rows are identical; only the live header row
// for the current day may differ).
func TestReportMatchesNSBackend(t *testing.T) {
	src := "/home/claude/src/nsd/NSD/Backend/data/NSD.sqlite"
	want, err := os.ReadFile("/home/claude/src/nsd/NSD/Backend/reports/NSDReport102026.txt")
	if err != nil {
		t.Skip("sample data not present")
	}
	dir := t.TempDir()
	b, _ := os.ReadFile(src)
	db := filepath.Join(dir, "NSD.sqlite")
	os.WriteFile(db, b, 0o644)
	st, err := OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	days, err := st.MonthStats("2026-10")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := st.Counters()
	got := fmtReport(time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local), days, c["monthly"], c["yearly"])
	gl := strings.Split(string(got), "\r\n")
	wl := strings.Split(string(want), "\r\n")
	// compare every row for the days in the sample report
	n := 0
	for _, w := range wl {
		if !strings.HasPrefix(w, "  ") || !strings.Contains(w, "|") {
			continue
		}
		found := false
		for _, g := range gl {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Errorf("row missing/different:\nwant %q", w)
		}
		n++
	}
	if n < 20 {
		t.Fatalf("only %d rows compared", n)
	}
	for _, i := range []int{0, 1, 2, 3, 4, 5} {
		if gl[i] != wl[i] {
			t.Errorf("header line %d: got %q want %q", i, gl[i], wl[i])
		}
	}
	t.Logf("compared %d day rows", n)
}

func TestFormats(t *testing.T) {
	s := string(fmtStrikes([]StrikeRow{{TS: 1793174710, Dist: 653, Dir: 10, Type: "cc-", Corr: 0}, {TS: 1793173022, Dist: 590, Dir: 126, Type: "cc+"}}))
	want, _ := os.ReadFile("/home/claude/src/nsd/NSD/Backend/data/NSDStrikes.txt")
	if len(want) > 0 && s != string(want) {
		t.Fatalf("NSDStrikes: got %q want %q", s, want)
	}
	if string(fmtStrikes(nil)) != "\r\n" {
		t.Fatal("empty file must be CRLF like NSBackend")
	}
	rt := string(fmtRealtime(&Realtime{Now: 1793175378, LastStrike: 1793174710, LastDir: "10", LastDist: "653", LastType: "cc-",
		LastHour: 2, Day: 38, Month: 18560, Year: 169820, MaxRate: 3, MaxRateTime: "02:10", MaxBurst: 2, MaxBurstTime: "04:02",
		CGP: 4, CGN: 7, CCP: 18, CCN: 9, Uptime: "5 days 13 hours 36 min", Measure: "km", Persistence: 60, Crop: 5000}))
	wantRT, _ := os.ReadFile("/home/claude/src/nsd/NSD/Backend/data/NSDRealtime.txt")
	if len(wantRT) > 0 && rt != string(wantRT) {
		t.Fatalf("NSDRealtime: got %q want %q", rt, wantRT)
	}
	if fmtUptime(5*24*time.Hour+13*time.Hour+36*time.Minute+20*time.Second) != "5 days 13 hours 36 min" {
		t.Fatal("uptime")
	}
}

func TestCSV(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig(dir)
	cfg.CSVNoise = true
	c := newCSVLog(cfg)
	r, _ := ParseStrike("1234,2026,10,28,31501,1805,42,42,10,20,30,40,0,1,1")
	r.Received = time.Date(2026, 10, 28, 8, 45, 1, 250e6, time.Local)
	c.Add(r, 10)
	nz, _ := ParseStrike("1235,2026,10,28,31502,-1,-1,-1,-1,-1,-1,-1,-1,-1,-1")
	nz.Received = r.Received.Add(time.Second)
	c.Add(nz, 10)
	if ch := c.Flush(); len(ch) != 2 {
		t.Fatalf("changed %v", ch)
	}
	ns, _ := os.ReadFile(filepath.Join(dir, "csv", "L20261028.csv"))
	if string(ns) != "2026,10,28,08,45,01,\" -IC\",180.5,42\r\n2026,10,28,08,45,02,\"noise\",0.0,0\r\n" {
		t.Fatalf("nslist csv %q", ns)
	}
	ex, _ := os.ReadFile(filepath.Join(dir, "csv", "strikes_2026-10-28.csv"))
	lines := strings.Split(strings.TrimSpace(string(ex)), "\r\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "utc_time,") || !strings.Contains(lines[1], ",strike,IC,-,cc-,180.5,190,42,40,10,1234,") {
		t.Fatalf("extended csv %q", ex)
	}
	if got := len(strings.Split(lines[1], ",")); got != len(strings.Split(strings.TrimSpace(extHeader), ",")) {
		t.Fatalf("column count %d", got)
	}
}

func writeTestArchive(t *testing.T, path string) {
	hdr := make([]byte, 532)
	copy(hdr, "NexStorm historical archive")
	type rec struct {
		s          uint32
		b          float64
		d, c       int32
		typ, pol   int16
		x, y, k, l int32
	}
	recs := []rec{{3600, 123.4, 55, 50, 0, 1, 1, 2, 3, 4}, {3605, -1, 0, 0, 0, 0, 0, 0, 0, 0}, {3661, 10, 300, 290, 1, 0, -2, 3, -4, 5}, {86399, 359.9, 12, 11, 0, 0, 3, 4, 5, 6}}
	buf := append([]byte{}, hdr...)
	for _, r := range recs {
		b := make([]byte, 64)
		binary.LittleEndian.PutUint64(b[0:], math.Float64bits(r.b))
		binary.LittleEndian.PutUint32(b[0x10:], r.s)
		binary.LittleEndian.PutUint32(b[0x14:], uint32(r.d))
		binary.LittleEndian.PutUint32(b[0x18:], uint32(r.c))
		binary.LittleEndian.PutUint16(b[0x1c:], uint16(r.typ))
		binary.LittleEndian.PutUint16(b[0x1e:], uint16(r.pol))
		for i, v := range []int32{r.x, r.y, r.k, r.l} {
			binary.LittleEndian.PutUint32(b[0x28+4*i:], uint32(v))
		}
		buf = append(buf, b...)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Export through the real nxutil.exe (run with Wine on Linux) and through the
// built-in reader must give identical CSVs.
func TestExportNXUtilVsNative(t *testing.T) {
	nx := "/home/claude/src/nxutil.exe"
	if _, err := os.Stat(nx); err != nil {
		t.Skip("nxutil.exe not present")
	}
	if _, err := exec.LookPath("wine"); err != nil {
		t.Skip("wine not installed")
	}
	dir := t.TempDir()
	cfg := defaultConfig(dir)
	cfg.ArchiveDir = filepath.Join(dir, "arch")
	os.MkdirAll(cfg.ArchiveDir, 0o755)
	writeTestArchive(t, filepath.Join(cfg.ArchiveDir, "20261027.nex"))
	day := time.Date(2026, 10, 27, 0, 0, 0, 0, time.Local)
	for _, noise := range []bool{false, true} {
		p1, n1, err := exportDay(cfg, nx, day, filepath.Join(dir, "a"), noise)
		if err != nil {
			t.Fatal(err)
		}
		p2, n2, err := exportDay(cfg, "", day, filepath.Join(dir, "b"), noise)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := os.ReadFile(p1)
		b, _ := os.ReadFile(p2)
		want := 3
		if noise {
			want = 4
		}
		if n1 != want || n2 != want || string(a) != string(b) {
			t.Fatalf("noise=%v nxutil %d rows, native %d rows\n%s\n---\n%s", noise, n1, n2, a, b)
		}
		if noise {
			t.Logf("CSV:\n%s", a)
		}
	}
}

func TestRollover(t *testing.T) {
	src := "/home/claude/src/nsd/NSD/Backend/data/NSD.sqlite"
	b, err := os.ReadFile(src)
	if err != nil {
		t.Skip("sample data not present")
	}
	dir := t.TempDir()
	cfg := defaultConfig(dir)
	db := filepath.Join(dir, "data", "NSD.sqlite")
	os.MkdirAll(filepath.Dir(db), 0o755)
	os.WriteFile(db, b, 0o644)
	st, err := OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e := NewEngine(cfg, st, NewDelivery(cfg), &ipcHealth{})
	if err := e.Load(); err != nil {
		t.Fatal(err)
	}
	// pretend we are on 31 Oct with some strikes, then cross into 1 Nov
	e.date = "2026-10-31"
	st.EnsureStats("2026-10-31", "00:00")
	e.daily = Counter{100, 5, 1}
	e.monthly.Strikes += 100
	e.yearly.Strikes += 100
	octTotal := 0
	days, _ := st.MonthStats("2026-10")
	for _, d := range days {
		if d.Date != "2026-10-31" {
			octTotal += d.Strikes
		}
	}
	yearBefore := e.yearly.Strikes
	e.rollover(time.Date(2026, 11, 1, 0, 0, 1, 0, time.Local))
	if e.date != "2026-11-01" || e.daily.Strikes != 0 || e.monthly.Strikes != 0 || e.yearly.Strikes != yearBefore {
		t.Fatalf("after rollover date=%s daily=%v monthly=%v yearly=%v", e.date, e.daily, e.monthly, e.yearly)
	}
	rep, err := os.ReadFile(filepath.Join(dir, "reports", "NSDReport102026.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rep), "  31  |     100") || !strings.Contains(string(rep), " Tot  | "+padL(itoa(octTotal+100), 7)) {
		t.Fatalf("october report wrong:\n%s", rep)
	}
	if _, err := os.Stat(filepath.Join(dir, "reports", "NSDReport112026.txt")); err != nil {
		t.Fatal("november report missing")
	}
	cur, _ := os.ReadFile(filepath.Join(dir, "reports", "NSDReport.txt"))
	if string(cur) != string(rep) {
		t.Fatal("NSDReport.txt should still be October just after midnight")
	}
	// restart on 2 Jan next year: daily, monthly and yearly must all reset
	st.SetMeta("date", "2026-12-30")
	e2 := NewEngine(cfg, st, NewDelivery(cfg), &ipcHealth{})
	e2.Load()
	if e2.yearly.Strikes != 0 && time.Now().Year() != 2026 {
		t.Fatalf("yearly not reset: %v", e2.yearly)
	}
}

func TestRealtimeJSON(t *testing.T) {
	b := fmtRealtimeJSON(&Realtime{Now: 1793175378, LastStrike: 1793174710, LastDir: "10", LastDist: "653", LastType: "cc-",
		LastHour: 2, Day: 38, Month: 18560, Year: 169820, MaxRate: 3, MaxRateTime: "02:10", MaxBurst: 2, MaxBurstTime: "04:02",
		CGP: 4, CGN: 7, CCP: 18, CCN: 9, Uptime: "5 days 13 hours 36 min", Measure: "km", Persistence: 60, Crop: 5000}, nil, nil)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["last_strike_distance"].(float64) != 653 || m["last_strike_direction"].(float64) != 10 || m["strikes_year"].(float64) != 169820 ||
		m["time_utc"] != "2026-10-28T08:16:18Z" || m["last_strike_type"] != "cc-" || len(m) != 28 || m["last_strike_lat"] != nil {
		t.Fatalf("%s", b)
	}
	t.Logf("%s", b)
}

func TestDestination(t *testing.T) {
	// 100 km due north of the equator/Greenwich: 0.8993 deg north
	if la, lo := destination(0, 0, 0, 100); la != 0.8993 || lo != 0 {
		t.Fatalf("%v %v", la, lo)
	}
	// London -> 343.6 km at bearing 148.1 lands near Paris (48.8566, 2.3522)
	la, lo := destination(51.5074, -0.1278, 148.1, 343.6)
	if math.Abs(la-48.8566) > 0.05 || math.Abs(lo-2.3522) > 0.05 {
		t.Fatalf("London->Paris gave %v %v", la, lo)
	}
	// crossing the date line wraps
	if _, lo := destination(0, 179.9, 90, 50); lo > -179 || lo < -180 {
		t.Fatalf("wrap %v", lo)
	}
	if trueBearing(3555, 10) != 5.5 || trueBearing(1805, 0) != 180.5 {
		t.Fatal("trueBearing")
	}
	b := fmtRealtimeJSON(&Realtime{LastStrike: 1, LastType: "cg-", LastDir: "180", LastDist: "10", LastPos: &[2]float64{51.4175, -0.1278}}, nil, nil)
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["last_strike_lat"] != 51.4175 || m["last_strike_lon"] != -0.1278 {
		t.Fatalf("%s", b)
	}
}

func TestRealtimeJSONStrikes(t *testing.T) {
	cfg := defaultConfig(t.TempDir())
	cfg.StationLat, cfg.StationLon = "51.4545", "-2.5879"
	cfg.validate()
	b1 := 216.4
	la, lo := 49.3726, -4.9347
	rows := []StrikeRow{
		{TS: 1793612215, Dir: 216, Dist: 285, Type: "cc+", Bearing: &b1, Lat: &la, Lon: &lo},
		{TS: 1793612100, Dir: 90, Dist: 100, Type: "cg-"}, // old NSBackend row: no bearing/lat/lon
	}
	b := fmtRealtimeJSON(&Realtime{Now: 1793612216, Persistence: 60}, rows, cfg)
	var m struct {
		Window  int            `json:"strikes_window_min"`
		Count   int            `json:"strikes_count"`
		Station map[string]any `json:"station"`
		Strikes []JSONStrike   `json:"strikes"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Window != 60 || m.Count != 2 || len(m.Strikes) != 2 || m.Station["lat"] != 51.4545 {
		t.Fatalf("%s", b)
	}
	if *m.Strikes[0].Lat != 49.3726 || *m.Strikes[0].Bearing != 216.4 || m.Strikes[0].TimeUTC != "2026-11-02T09:36:55Z" {
		t.Fatalf("%+v", m.Strikes[0])
	}
	if m.Strikes[1].Lat == nil || *m.Strikes[1].Bearing != 90 || math.Abs(*m.Strikes[1].Lat-51.4545) > 0.01 {
		t.Fatalf("fallback %+v", m.Strikes[1])
	}
	// empty list is [] not missing
	b = fmtRealtimeJSON(&Realtime{Persistence: 60}, nil, cfg)
	if !strings.Contains(string(b), `"strikes": []`) {
		t.Fatalf("%s", b)
	}
	// switched off
	cfg.RealtimeJSONStrikes = false
	if strings.Contains(string(fmtRealtimeJSON(&Realtime{}, rows, cfg)), "strikes_window_min") {
		t.Fatal("list should be omitted")
	}
}

func TestStoreMigratesStrikeColumns(t *testing.T) {
	b, err := os.ReadFile("/home/claude/src/nsd/NSD/Backend/data/NSD.sqlite")
	if err != nil {
		t.Skip("sample data not present")
	}
	db := filepath.Join(t.TempDir(), "NSD.sqlite")
	os.WriteFile(db, b, 0o644)
	st, err := OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	be, la, lo := 12.3, 50.0, -1.0
	if err := st.InsertStrikes([]StrikeRow{{TS: 4000000000, Dir: 12, Dist: 10, Type: "cg+", Bearing: &be, Lat: &la, Lon: &lo}}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.RecentStrikes(0, 10)
	if err != nil || len(rows) == 0 || rows[0].Lat == nil || *rows[0].Lat != 50 {
		t.Fatalf("%v %+v", err, rows)
	}
	st.Close()
	if st, err = OpenStore(db); err != nil { // reopening must not try to add the columns again
		t.Fatal(err)
	}
	st.Close()
}

// After a restart the last strike and the per-minute rate must come back.
func TestRestartRestoresRecent(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig(dir)
	cfg.StationLat, cfg.StationLon = "51.4545", "-2.5879"
	cfg.validate()
	st, err := OpenStore(filepath.Join(dir, "data", "NSD.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(cfg, st, NewDelivery(cfg), &ipcHealth{})
	if err := e.Load(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 5; i++ {
		r, _ := ParseStrike(fmt.Sprintf("%d,2026,11,2,36000,1925,265,0,0,0,0,0,0,0,1", 100+i))
		r.Received = now.Add(time.Duration(i-5) * time.Second)
		e.handleStrike(r)
	}
	e.everySecond(now) // saves strikes and (tick 1) nothing else
	e.persist(now, false)
	st.Close()

	st2, _ := OpenStore(filepath.Join(dir, "data", "NSD.sqlite"))
	defer st2.Close()
	e2 := NewEngine(cfg, st2, NewDelivery(cfg), &ipcHealth{})
	if err := e2.Load(); err != nil {
		t.Fatal(err)
	}
	if len(e2.recent) != 5 || e2.lastType != "cg-" || e2.lastDist != "265" || e2.lastDir != "192" || e2.lastPos == nil || e2.daily.Strikes != 5 {
		t.Fatalf("recent=%d last=%s %s %s pos=%v daily=%v", len(e2.recent), e2.lastType, e2.lastDir, e2.lastDist, e2.lastPos, e2.daily)
	}
	if math.Abs(e2.lastPos[0]-e.lastPos[0]) > 1e-9 {
		t.Fatalf("position changed %v %v", e.lastPos, e2.lastPos)
	}
}
