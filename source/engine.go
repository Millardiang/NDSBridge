package main

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// Engine owns all state. Everything runs on one goroutine (Run), so unlike
// NSBackend there are no unsynchronised counters shared between threads.
type Engine struct {
	cfg   *Config
	store *Store
	csv   *csvLog
	deliv *Delivery
	ipc   *ipcHealth
	start time.Time

	strikes chan *StrikeRec
	storms  chan string

	date  string // local yyyy-mm-dd the counters belong to
	align int

	daily, monthly, yearly Counter
	day                    DayStats

	recent   []recentStrike // last 60 s, for rate and close rate
	burst    int
	minute   GraphRow
	pending  []StrikeRow
	pendingS map[string]*StormRec
	rawStorm []string
	active   int

	lastTS            int64
	lastDir, lastDist string
	lastType          string

	tick int

	statusMu sync.Mutex
	status   map[string]any
}

type recentStrike struct {
	t    time.Time
	dist int
}

func NewEngine(cfg *Config, st *Store, dl *Delivery, h *ipcHealth) *Engine {
	return &Engine{
		cfg: cfg, store: st, csv: newCSVLog(cfg), deliv: dl, ipc: h, start: time.Now(),
		strikes:  make(chan *StrikeRec, 100000),
		storms:   make(chan string, 10000),
		pendingS: map[string]*StormRec{},
		lastDir:  "0", lastDist: "0", lastType: "-",
	}
}

func hhmm(t time.Time) string { return t.Format("15:04") }

// Load restores counters and today's stats, and catches up on any day,
// month or year change that happened while the program was not running
// (NSBackend only reset at midnight if it happened to be running then).
func (e *Engine) Load() error {
	now := time.Now()
	today := now.Format("2006-01-02")
	c, err := e.store.Counters()
	if err != nil {
		return err
	}
	e.daily, e.monthly, e.yearly = c["daily"], c["monthly"], c["yearly"]

	prev := e.store.Meta("date")
	if prev == "" { // database from NSBackend: newest stats row is the day the counters belong to
		e.store.db.QueryRow("SELECT MAX(date) FROM stats").Scan(&prev)
	}
	if prev != "" && prev != today {
		logf("counters were last saved on %s; resetting for %s", prev, today)
		e.resetFor(prev, today)
	}
	e.date = today
	e.store.SetMeta("date", today)
	if err := e.store.EnsureStats(today, hhmm(now)); err != nil {
		return err
	}
	if d, err := e.store.Stats(today); err == nil && d != nil {
		e.day = *d
	}
	e.day.Date = today
	logf("loaded counters: today %d strikes, month %d, year %d", e.daily.Strikes, e.monthly.Strikes, e.yearly.Strikes)
	return nil
}

func (e *Engine) resetFor(prevDate, today string) {
	names := []string{"daily"}
	e.daily = Counter{}
	if len(prevDate) < 7 || prevDate[:7] != today[:7] {
		names = append(names, "monthly")
		e.monthly = Counter{}
	}
	if len(prevDate) < 4 || prevDate[:4] != today[:4] {
		names = append(names, "yearly")
		e.yearly = Counter{}
	}
	if err := e.store.ResetCounters(names...); err != nil {
		errf("reset counters: %v", err)
	}
	now := time.Now()
	e.day = DayStats{Date: today, MaxRateTime: hhmm(now), MaxBurstTime: hhmm(now), MaxStormsTime: hhmm(now)}
	logf("new day %s: reset %v counters", today, names)
}

func (e *Engine) rollover(now time.Time) {
	today := now.Format("2006-01-02")
	if today == e.date {
		return
	}
	prev := e.date
	e.persist(now, false) // final numbers for the day that just ended
	e.resetFor(prev, today)
	e.date = today
	if err := e.store.EnsureStats(today, hhmm(now)); err != nil {
		errf("stats row for %s: %v", today, err)
	}
	e.store.SetMeta("date", today)
	e.writeReports(now)
	go func() {
		defer recoverLog("backup", nil)
		if err := e.store.Backup(filepath.Join(filepath.Dir(e.store.path), "backup"), 14); err != nil {
			warnf("daily database backup failed: %v", err)
		}
	}()
	if e.cfg.AutoExport {
		if pt, err := time.ParseInLocation("2006-01-02", prev, time.Local); err == nil {
			go func() {
				defer recoverLog("auto export", nil)
				time.Sleep(10 * time.Minute) // let NexStorm finish and close yesterday's archive
				if _, err := runExport(e.cfg, pt, pt, "", false); err != nil {
					warnf("automatic export of %s failed: %v", prev, err)
				}
			}()
		}
	}
}

func (e *Engine) handleStrike(r *StrikeRec) {
	switch r.Kind {
	case KindHeartbeat:
		e.align = r.F[7]
		return
	case KindNoise:
		e.daily.Noises++
		e.monthly.Noises++
		e.yearly.Noises++
		e.csv.Add(r, e.align)
		return
	case KindOther:
		debugf("unrecognised record %q", r.Raw)
		return
	}
	ts := r.Received.Unix()
	dir := alignedBearing(r.BearingT(), e.align)
	typ := r.NSDType()
	e.daily.Strikes++
	e.monthly.Strikes++
	e.yearly.Strikes++
	e.burst++
	e.minute.Total++
	switch typ {
	case "cg+":
		e.day.CGP++
		e.minute.CGP++
	case "cg-":
		e.day.CGN++
		e.minute.CGN++
	case "cc+":
		e.day.CCP++
		e.minute.CCP++
	case "cc-":
		e.day.CCN++
		e.minute.CCN++
	}
	e.recent = append(e.recent, recentStrike{r.Received, r.Distance()})
	e.lastTS, e.lastDir, e.lastDist, e.lastType = ts, itoa(dir), itoa(r.Distance()), typ
	e.pending = append(e.pending, StrikeRow{TS: ts, Dir: dir, Dist: r.Distance(), Corr: r.Corr(), Type: typ})
	e.csv.Add(r, e.align)
}

func (e *Engine) handleStorm(raw string) {
	s, err := ParseStorm(raw, time.Local, e.align, e.cfg.StormFix)
	if err != nil {
		debugf("ignored storm message %q: %v", raw, err)
		return
	}
	if s == nil {
		return
	}
	e.pendingS[s.ID] = s
	e.rawStorm = append(e.rawStorm, joinc("ACTIVE", i64(time.Now().Unix()), s.ID, itoa(s.DirT), itoa(s.Dist))+"|")
	if len(e.rawStorm) > 5000 {
		e.rawStorm = e.rawStorm[len(e.rawStorm)-5000:]
	}
}

// Run is the main loop.
func (e *Engine) Run(ctx context.Context) {
	sec := time.NewTicker(time.Second)
	defer sec.Stop()
	for {
		select {
		case <-ctx.Done():
			e.drain()
			e.persist(time.Now(), false)
			e.flushStrikes()
			e.csv.Flush()
			logf("engine stopped, state saved")
			return
		case r := <-e.strikes:
			e.handleStrike(r)
		case s := <-e.storms:
			e.handleStorm(s)
		case now := <-sec.C:
			e.safe("1s", func() { e.everySecond(now) })
		}
	}
}

func (e *Engine) drain() {
	for {
		select {
		case r := <-e.strikes:
			e.handleStrike(r)
		case s := <-e.storms:
			e.handleStorm(s)
		default:
			return
		}
	}
}

// safe runs a periodic job so that one failure (disk full, locked file,
// database busy) is logged and retried next time instead of killing the loop.
func (e *Engine) safe(what string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			errf("%s job failed: %v", what, r)
		}
	}()
	fn()
}

func (e *Engine) everySecond(now time.Time) {
	e.tick++
	e.rollover(now)

	cut := now.Add(-60 * time.Second)
	i := 0
	for i < len(e.recent) && e.recent[i].t.Before(cut) {
		i++
	}
	e.recent = e.recent[i:]
	rate := len(e.recent)
	if rate > e.day.MaxRate {
		e.day.MaxRate, e.day.MaxRateTime = rate, hhmm(now)
	}
	if e.burst > e.day.MaxBurst {
		e.day.MaxBurst, e.day.MaxBurstTime = e.burst, hhmm(now)
	}
	e.burst = 0
	if e.active > e.day.MaxStorms {
		e.day.MaxStorms, e.day.MaxStormsTime = e.active, hhmm(now)
	}

	if changed := e.csv.Flush(); len(changed) > 0 {
		e.deliv.Mirror(changed...)
	}
	if e.tick%e.cfg.WriteEvery == 0 {
		e.safe("strikes", func() { e.flushStrikes(); e.writeStrikeFiles(now, rate) })
	}
	if e.tick%20 == 0 {
		e.safe("storms", func() { e.flushStorms(now) })
	}
	if e.tick%60 == 0 {
		e.safe("minute", func() { e.persist(now, true); e.writeGraph(now); e.pingNSLMap() })
	}
	if e.tick%1200 == 0 {
		e.safe("housekeeping", func() { e.housekeeping(now) })
	}
	e.publishStatus(now, rate)
}

func (e *Engine) flushStrikes() {
	if len(e.pending) == 0 {
		return
	}
	if err := e.store.InsertStrikes(e.pending); err != nil {
		warnEvery("db-strikes", 5*time.Minute, "saving strikes to database: %v (will retry)", err)
		if len(e.pending) > 50000 {
			e.pending = e.pending[len(e.pending)-50000:]
		}
		return
	}
	clearWarn("db-strikes", "database writes working again")
	e.pending = e.pending[:0]
}

func (e *Engine) out(name string, data []byte) {
	if !e.cfg.NSDFiles {
		return
	}
	p := filepath.Join(e.cfg.Path(e.cfg.DataDir), name)
	if err := writeFileAtomic(p, data); err != nil {
		warnEvery("write:"+name, 5*time.Minute, "writing %s: %v", p, err)
		return
	}
	clearWarn("write:"+name, "%s written again", name)
	e.deliv.Publish(p)
}

func (e *Engine) writeStrikeFiles(now time.Time, rate int) {
	nowU := now.Unix()
	rows, err := e.store.RecentStrikes(nowU-int64(e.cfg.Persistence*60), e.cfg.NSCrop)
	if err != nil {
		warnEvery("db-read", 5*time.Minute, "reading strikes: %v", err)
		return
	}
	var last20 []StrikeRow
	for _, r := range rows {
		if r.TS > nowU-20 {
			last20 = append(last20, r)
		}
	}
	closeRate := 0
	for _, r := range e.recent {
		if r.dist < 50 {
			closeRate++
		}
	}
	e.out("NSDStrikes.txt", fmtStrikes(rows))
	e.out("NSDStrikes20sec.txt", fmtStrikes(last20))
	rt := &Realtime{
		Now: nowU, Rate: rate, CloseRate: closeRate,
		LastStrike: e.lastTS, LastDir: e.lastDir, LastDist: e.lastDist, LastType: e.lastType,
		LastHour: len(rows), Day: e.daily.Strikes, Month: e.monthly.Strikes, Year: e.yearly.Strikes,
		MaxRate: e.day.MaxRate, MaxRateTime: e.day.MaxRateTime, MaxBurst: e.day.MaxBurst, MaxBurstTime: e.day.MaxBurstTime,
		CGP: e.day.CGP, CGN: e.day.CGN, CCP: e.day.CCP, CCN: e.day.CCN,
		Uptime: fmtUptime(time.Since(e.start)), Measure: e.cfg.Measure, Persistence: e.cfg.Persistence, Crop: e.cfg.NSCrop,
	}
	e.out("NSDRealtime.txt", fmtRealtime(rt))
	if e.cfg.RealtimeJSON {
		e.out("NSDRealtime.json", fmtRealtimeJSON(rt))
	}
}

func (e *Engine) flushStorms(now time.Time) {
	nowU := now.Unix()
	if len(e.pendingS) > 0 {
		list := make([]*StormRec, 0, len(e.pendingS))
		for _, s := range e.pendingS {
			list = append(list, s)
		}
		newIDs, err := e.store.UpsertStorms(list, nowU)
		if err != nil {
			warnEvery("db-storms", 5*time.Minute, "saving storms: %v (will retry)", err)
		} else {
			e.pendingS = map[string]*StormRec{}
			for _, id := range newIDs {
				e.daily.Storms++
				e.monthly.Storms++
				e.yearly.Storms++
				logf("new storm %s", id)
			}
		}
	}
	act, err := e.store.ActiveStorms(nowU - 300)
	if err != nil {
		warnEvery("db-read", 5*time.Minute, "reading storms: %v", err)
		return
	}
	old, err := e.store.OldStorms(nowU-300, nowU-86400)
	if err != nil {
		warnEvery("db-read", 5*time.Minute, "reading old storms: %v", err)
		return
	}
	e.active = len(act)
	e.out("NSDStorms.txt", fmtStorms(act))
	e.out("NSDOldStorms.txt", fmtStorms(old))
}

func (e *Engine) persist(now time.Time, withGraph bool) {
	e.day.Strikes, e.day.Noises, e.day.Storms = e.daily.Strikes, e.daily.Noises, e.daily.Storms
	sn := &Snapshot{Date: e.date, Daily: e.daily, Monthly: e.monthly, Yearly: e.yearly, Day: e.day}
	if withGraph {
		g := e.minute
		g.TS = now.Unix()
		sn.Graph = &g
	}
	if err := e.store.SaveSnapshot(sn); err != nil {
		warnEvery("db-snap", 5*time.Minute, "saving counters: %v (will retry)", err)
		return
	}
	if withGraph {
		e.minute = GraphRow{}
	}
}

func (e *Engine) writeGraph(now time.Time) {
	rows, err := e.store.Graph(now.Unix() - 86400)
	if err != nil {
		warnEvery("db-read", 5*time.Minute, "reading graph: %v", err)
		return
	}
	e.out("NSDGraph.txt", fmtGraph(rows))
}

// writeReports writes NSDReportMMYYYY.txt for the current month and, in the
// first hours of a new month, the month just finished. NSDReport.txt is the
// month of "now minus 6 hours", as NSBackend did.
func (e *Engine) writeReports(now time.Time) {
	main := now.Add(-6 * time.Hour)
	months := []time.Time{main}
	if main.Format("2006-01") != now.Format("2006-01") {
		months = append(months, now)
	}
	dir := e.cfg.Path(e.cfg.ReportsDir)
	for i, m := range months {
		rep, err := e.report(m)
		if err != nil {
			warnf("report %s: %v", m.Format("2006-01"), err)
			continue
		}
		names := []string{"NSDReport" + m.Format("012006") + ".txt"}
		if i == 0 {
			names = append(names, "NSDReport.txt")
		}
		for _, name := range names {
			p := filepath.Join(dir, name)
			if err := writeFileAtomic(p, rep); err != nil {
				warnEvery("write:"+name, 5*time.Minute, "writing %s: %v", p, err)
				continue
			}
			e.deliv.Publish(p)
		}
	}
}

func (e *Engine) report(month time.Time) ([]byte, error) {
	mkey := month.Format("2006-01")
	days, err := e.store.MonthStats(mkey)
	if err != nil {
		return nil, err
	}
	for _, d := range days { // today's live numbers
		if d.Date == e.date {
			*d = e.day
			d.Date = e.date
			d.Strikes, d.Noises, d.Storms = e.daily.Strikes, e.daily.Noises, e.daily.Storms
		}
	}
	mon, yr := e.monthly, e.yearly
	if mkey != e.date[:7] { // a finished month: totals from its daily rows
		mon = Counter{}
		for _, d := range days {
			mon.Strikes += d.Strikes
			mon.Noises += d.Noises
			mon.Storms += d.Storms
		}
	}
	if mkey[:4] != e.date[:4] {
		yr, err = e.store.SumStats(mkey[:4] + "-")
		if err != nil {
			return nil, err
		}
	}
	return fmtReport(month, days, mon, yr), nil
}

func (e *Engine) housekeeping(now time.Time) {
	nowU := now.Unix()
	if err := e.store.Housekeeping(nowU-int64(e.cfg.Persistence*60)-600, nowU-86400-600, nowU-90*86400, nowU-300); err != nil {
		warnf("database housekeeping: %v", err)
	}
	if e.cfg.NSDFiles {
		e.out("NSDRaw.txt", nsdFile(e.rawStorm))
		e.rawStorm = nil
	}
	e.writeReports(now)
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func (e *Engine) pingNSLMap() {
	if e.cfg.NSLMapURL == "" {
		return
	}
	url := e.cfg.NSLMapURL
	go func() {
		defer recoverLog("nslmap", nil)
		resp, err := httpClient.Get(url)
		if err != nil {
			warnEvery("nslmap", 30*time.Minute, "calling nslmap %s: %v", url, err)
			return
		}
		resp.Body.Close()
		clearWarn("nslmap", "nslmap reachable again")
	}()
}

func (e *Engine) publishStatus(now time.Time, rate int) {
	h := e.ipc.snapshot()
	st := map[string]any{
		"time":               now.Format(time.RFC3339),
		"uptime":             fmtUptime(time.Since(e.start)),
		"strikes_today":      e.daily.Strikes,
		"strikes_month":      e.monthly.Strikes,
		"strikes_year":       e.yearly.Strikes,
		"noises_today":       e.daily.Noises,
		"storms_today":       e.daily.Storms,
		"active_storms":      e.active,
		"rate_per_min":       rate,
		"max_rate":           e.day.MaxRate,
		"last_strike":        e.lastTS,
		"last_strike_dir":    e.lastDir,
		"last_strike_dist":   e.lastDist,
		"last_strike_type":   e.lastType,
		"trac_alignment":     e.align,
		"strike_memory_open": h.StrikeOpen,
		"storm_memory_open":  h.StormOpen,
		"ipc_messages":       h.MessagesSeen,
		"ipc_rejected":       h.Rejected,
		"csv_rows_waiting":   e.csv.queued(),
		"csv_rows_dropped":   e.csv.dropped,
	}
	hb := "never"
	if !h.LastHeartbeat.IsZero() {
		hb = h.LastHeartbeat.Format(time.RFC3339)
		age := now.Sub(h.LastHeartbeat)
		if age > time.Duration(e.cfg.HeartbeatWarnMn)*time.Minute {
			warnEvery("heartbeat", 30*time.Minute, "no heartbeat from NexStorm for %s - is NexStorm running and its IPC output enabled?", age.Round(time.Second))
			st["nexstorm"] = "no heartbeat for " + age.Round(time.Second).String()
		} else {
			clearWarn("heartbeat", "NexStorm heartbeat back")
			st["nexstorm"] = "ok"
		}
	} else {
		st["nexstorm"] = "waiting for first heartbeat"
		if time.Since(e.start) > time.Duration(e.cfg.HeartbeatWarnMn)*time.Minute {
			warnEvery("heartbeat", 30*time.Minute, "nothing received from NexStorm since start-up - is NexStorm running (same user, same elevation) with IPC enabled?")
		}
	}
	st["last_heartbeat"] = hb
	e.statusMu.Lock()
	e.status = st
	e.statusMu.Unlock()
}

func (e *Engine) Status() map[string]any {
	e.statusMu.Lock()
	defer e.statusMu.Unlock()
	out := map[string]any{}
	for k, v := range e.status {
		out[k] = v
	}
	return out
}
