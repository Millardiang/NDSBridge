package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store wraps NSD.sqlite. The schema is the one NSBackend created, so an
// existing database (with this year's counters and stats) carries straight
// over. One extra table, "meta", is added.
type Store struct {
	db   *sql.DB
	path string
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS strikes (timestamp INT, dir INT, dist INT, corr INT, strtype VARCHAR(6))`,
	`CREATE INDEX IF NOT EXISTS strikedata ON strikes(timestamp ASC, strtype ASC)`,
	`CREATE TABLE IF NOT EXISTS counters (name VARCHAR(10), strikes INT, noises INT, storms INT)`,
	`CREATE INDEX IF NOT EXISTS counterdata ON counters(name ASC)`,
	`CREATE TABLE IF NOT EXISTS stats (date VARCHAR(12), strikes INT, noises INT, storms INT, cgp INT, cgn INT, ccp INT, ccn INT, maxrate INT, maxratetime VARCHAR(12), maxburst INT, maxbursttime VARCHAR(12), maxstorms INT, maxstormstime VARCHAR(12))`,
	`CREATE INDEX IF NOT EXISTS statsdata ON stats(date ASC)`,
	`CREATE TABLE IF NOT EXISTS storms (timestamp INT, id VARCHAR(8), active VARCHAR(6), detect INT, last INT, dir INT, dist INT, detectdir INT, detectdist INT, cat INT, trend INT, currate INT, maxrate INT, cgp INT, cgn INT, ccp INT, ccn INT, total INT)`,
	`CREATE INDEX IF NOT EXISTS stormsdata ON storms(timestamp ASC, active ASC)`,
	`CREATE INDEX IF NOT EXISTS stormsid ON storms(id)`,
	`CREATE TABLE IF NOT EXISTS graphs (timestamp INT, cgp INT, cgn INT, ccp INT, ccn INT, total INT)`,
	`CREATE INDEX IF NOT EXISTS graphsdata ON graphs(timestamp ASC)`,
	`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)`,
}

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	s, err := openStore(path)
	if err == nil {
		return s, nil
	}
	// The database is damaged (e.g. by a power cut under NSBackend, which ran
	// with Synchronous=Off). Keep the damaged file for inspection and start a
	// fresh one rather than refusing to run.
	bad := fmt.Sprintf("%s.corrupt-%s", path, time.Now().Format("20060102-150405"))
	errf("database %s unusable (%v); moved to %s and starting a new one", path, err, bad)
	os.Rename(path, bad)
	os.Remove(path + "-journal")
	return openStore(path)
}

func openStore(path string) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_busy_timeout=10000&_journal_mode=DELETE&_synchronous=NORMAL"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var res string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&res); err != nil {
		db.Close()
		return nil, err
	}
	if res != "ok" {
		db.Close()
		return nil, fmt.Errorf("integrity check: %s", res)
	}
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %v", q, err)
		}
	}
	s := &Store{db: db, path: path}
	for _, n := range []string{"daily", "monthly", "yearly"} {
		var c int
		db.QueryRow("SELECT COUNT(*) FROM counters WHERE name = ?", n).Scan(&c)
		if c == 0 {
			if _, err := db.Exec("INSERT INTO counters (name, strikes, noises, storms) VALUES (?,0,0,0)", n); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *Store) Close() { s.db.Close() }

func (s *Store) Meta(key string) string {
	var v string
	s.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	return v
}

func (s *Store) SetMeta(key, val string) error {
	_, err := s.db.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, val)
	return err
}

type Counter struct{ Strikes, Noises, Storms int }

func (s *Store) Counters() (map[string]Counter, error) {
	rows, err := s.db.Query("SELECT name, strikes, noises, storms FROM counters")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]Counter{}
	for rows.Next() {
		var name string
		var a, b, c sql.NullInt64
		if err := rows.Scan(&name, &a, &b, &c); err != nil {
			return nil, err
		}
		m[name] = Counter{int(a.Int64), int(b.Int64), int(c.Int64)}
	}
	return m, rows.Err()
}

type DayStats struct {
	Date                    string
	Strikes, Noises, Storms int
	CGP, CGN, CCP, CCN      int
	MaxRate                 int
	MaxRateTime             string
	MaxBurst                int
	MaxBurstTime            string
	MaxStorms               int
	MaxStormsTime           string
}

func nstr(v sql.NullString) string { return v.String }

func (s *Store) scanStats(rows *sql.Rows) (*DayStats, error) {
	var d DayStats
	var date, mrt, mbt, mst sql.NullString
	var a [10]sql.NullInt64
	if err := rows.Scan(&date, &a[0], &a[1], &a[2], &a[3], &a[4], &a[5], &a[6], &a[7], &mrt, &a[8], &mbt, &a[9], &mst); err != nil {
		return nil, err
	}
	d.Date = nstr(date)
	d.Strikes, d.Noises, d.Storms = int(a[0].Int64), int(a[1].Int64), int(a[2].Int64)
	d.CGP, d.CGN, d.CCP, d.CCN = int(a[3].Int64), int(a[4].Int64), int(a[5].Int64), int(a[6].Int64)
	d.MaxRate, d.MaxRateTime = int(a[7].Int64), nstr(mrt)
	d.MaxBurst, d.MaxBurstTime = int(a[8].Int64), nstr(mbt)
	d.MaxStorms, d.MaxStormsTime = int(a[9].Int64), nstr(mst)
	return &d, nil
}

const statsCols = "date, strikes, noises, storms, cgp, cgn, ccp, ccn, maxrate, maxratetime, maxburst, maxbursttime, maxstorms, maxstormstime"

func (s *Store) Stats(date string) (*DayStats, error) {
	rows, err := s.db.Query("SELECT "+statsCols+" FROM stats WHERE date = ? LIMIT 1", date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if rows.Next() {
		return s.scanStats(rows)
	}
	return nil, rows.Err()
}

func (s *Store) MonthStats(prefix string) ([]*DayStats, error) {
	rows, err := s.db.Query("SELECT "+statsCols+" FROM stats WHERE date LIKE ? ORDER BY date", prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DayStats
	seen := map[string]bool{}
	for rows.Next() {
		d, err := s.scanStats(rows)
		if err != nil {
			return nil, err
		}
		if seen[d.Date] { // NSBackend could insert the same day twice
			continue
		}
		seen[d.Date] = true
		out = append(out, d)
	}
	return out, rows.Err()
}

// SumStats totals the daily rows whose date starts with prefix.
func (s *Store) SumStats(prefix string) (Counter, error) {
	days, err := s.MonthStats(prefix)
	var c Counter
	for _, d := range days {
		c.Strikes += d.Strikes
		c.Noises += d.Noises
		c.Storms += d.Storms
	}
	return c, err
}

func (s *Store) EnsureStats(date, hhmm string) error {
	var n int
	s.db.QueryRow("SELECT COUNT(date) FROM stats WHERE date = ?", date).Scan(&n)
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO stats (`+statsCols+`) VALUES (?,0,0,0,0,0,0,0,0,?,0,?,0,?)`, date, hhmm, hhmm, hhmm)
	return err
}

// Snapshot is everything the engine persists once a minute.
type Snapshot struct {
	Date                   string
	Daily, Monthly, Yearly Counter
	Day                    DayStats
	Graph                  *GraphRow
}

type GraphRow struct {
	TS                        int64
	CGP, CGN, CCP, CCN, Total int
}

func (s *Store) SaveSnapshot(sn *Snapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for name, c := range map[string]Counter{"daily": sn.Daily, "monthly": sn.Monthly, "yearly": sn.Yearly} {
		if _, err := tx.Exec("UPDATE counters SET strikes = ?, noises = ?, storms = ? WHERE name = ?", c.Strikes, c.Noises, c.Storms, name); err != nil {
			return err
		}
	}
	d := sn.Day
	if _, err := tx.Exec(`UPDATE stats SET strikes=?, noises=?, storms=?, maxrate=?, maxratetime=?, maxburst=?, maxbursttime=?, maxstorms=?, maxstormstime=?, ccp=?, ccn=?, cgp=?, cgn=? WHERE date=?`,
		d.Strikes, d.Noises, d.Storms, d.MaxRate, d.MaxRateTime, d.MaxBurst, d.MaxBurstTime, d.MaxStorms, d.MaxStormsTime, d.CCP, d.CCN, d.CGP, d.CGN, sn.Date); err != nil {
		return err
	}
	if g := sn.Graph; g != nil {
		if _, err := tx.Exec("INSERT INTO graphs (timestamp, cgp, cgn, ccp, ccn, total) VALUES (?,?,?,?,?,?)", g.TS, g.CGP, g.CGN, g.CCP, g.CCN, g.Total); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) Graph(since int64) ([]GraphRow, error) {
	rows, err := s.db.Query("SELECT timestamp, total, cgp, cgn, ccp, ccn FROM graphs WHERE timestamp > ? ORDER BY timestamp", since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GraphRow
	for rows.Next() {
		var g GraphRow
		var v [6]sql.NullInt64
		if err := rows.Scan(&v[0], &v[1], &v[2], &v[3], &v[4], &v[5]); err != nil {
			return nil, err
		}
		g.TS, g.Total, g.CGP, g.CGN, g.CCP, g.CCN = v[0].Int64, int(v[1].Int64), int(v[2].Int64), int(v[3].Int64), int(v[4].Int64), int(v[5].Int64)
		out = append(out, g)
	}
	return out, rows.Err()
}

type StrikeRow struct {
	TS        int64
	Dir, Dist int
	Corr      int
	Type      string
}

func (s *Store) InsertStrikes(batch []StrikeRow) error {
	if len(batch) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.Prepare("INSERT INTO strikes (timestamp, dir, dist, corr, strtype) VALUES (?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer st.Close()
	for _, r := range batch {
		if _, err := st.Exec(r.TS, r.Dir, r.Dist, r.Corr, r.Type); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RecentStrikes(since int64, limit int) ([]StrikeRow, error) {
	rows, err := s.db.Query("SELECT timestamp, dir, dist, corr, strtype FROM strikes WHERE timestamp > ? ORDER BY timestamp DESC LIMIT ?", since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StrikeRow
	for rows.Next() {
		var r StrikeRow
		var v [4]sql.NullInt64
		var t sql.NullString
		if err := rows.Scan(&v[0], &v[1], &v[2], &v[3], &t); err != nil {
			return nil, err
		}
		r.TS, r.Dir, r.Dist, r.Corr, r.Type = v[0].Int64, int(v[1].Int64), int(v[2].Int64), int(v[3].Int64), t.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertStorm stores the latest state of a storm; returns true if it is new.
func (s *Store) UpsertStorms(list []*StormRec, now int64) (newIDs []string, err error) {
	if len(list) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, r := range list {
		var n int
		if err := tx.QueryRow("SELECT COUNT(id) FROM storms WHERE id = ?", r.ID).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			_, err = tx.Exec(`INSERT INTO storms (timestamp,id,active,detect,last,dir,dist,detectdir,detectdist,cat,trend,currate,maxrate,cgp,cgn,ccp,ccn,total)
				VALUES (?,?,'yes',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				now, r.ID, r.Detect, r.Last, r.DirT, r.Dist, r.DirT, r.Dist, r.Cat, r.Trend, r.CurRate, r.MaxRate, r.CGP, r.CGN, r.CCP, r.CCN, r.Total)
			newIDs = append(newIDs, r.ID)
		} else {
			_, err = tx.Exec(`UPDATE storms SET timestamp=?, active='yes', detect=?, last=?, dir=?, dist=?, cat=?, trend=?, currate=?, maxrate=?, cgp=?, cgn=?, ccp=?, ccn=?, total=? WHERE id=?`,
				now, r.Detect, r.Last, r.DirT, r.Dist, r.Cat, r.Trend, r.CurRate, r.MaxRate, r.CGP, r.CGN, r.CCP, r.CCN, r.Total, r.ID)
		}
		if err != nil {
			return nil, err
		}
	}
	return newIDs, tx.Commit()
}

type StormRow struct {
	ID                               string
	Detect, Last                     int64
	Dir, Dist, DetectDir, DetectDist int
	Cat, Trend, CurRate, MaxRate     int
	CGP, CGN, CCP, CCN, Total        int
}

func (s *Store) storms(where string, args ...any) ([]StormRow, error) {
	rows, err := s.db.Query(`SELECT id, detect, last, dir, dist, detectdir, detectdist, cat, trend, currate, maxrate, cgp, cgn, ccp, ccn, total FROM storms WHERE `+where+` ORDER BY detect`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StormRow
	for rows.Next() {
		var id sql.NullString
		var v [15]sql.NullInt64
		p := []any{&id}
		for i := range v {
			p = append(p, &v[i])
		}
		if err := rows.Scan(p...); err != nil {
			return nil, err
		}
		out = append(out, StormRow{
			ID: id.String, Detect: v[0].Int64, Last: v[1].Int64,
			Dir: int(v[2].Int64), Dist: int(v[3].Int64), DetectDir: int(v[4].Int64), DetectDist: int(v[5].Int64),
			Cat: int(v[6].Int64), Trend: int(v[7].Int64), CurRate: int(v[8].Int64), MaxRate: int(v[9].Int64),
			CGP: int(v[10].Int64), CGN: int(v[11].Int64), CCP: int(v[12].Int64), CCN: int(v[13].Int64), Total: int(v[14].Int64),
		})
	}
	return out, rows.Err()
}

func (s *Store) ActiveStorms(lastAfter int64) ([]StormRow, error) {
	return s.storms("last > ?", lastAfter)
}

func (s *Store) OldStorms(lastBefore, tsAfter int64) ([]StormRow, error) {
	return s.storms("last < ? AND timestamp > ?", lastBefore, tsAfter)
}

func (s *Store) Housekeeping(strikesBefore, graphsBefore, stormsBefore, inactiveBefore int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{"DELETE FROM strikes WHERE timestamp < ?", strikesBefore},
		{"DELETE FROM graphs WHERE timestamp < ?", graphsBefore},
		{"DELETE FROM storms WHERE timestamp < ?", stormsBefore},
		{"UPDATE storms SET active = 'no' WHERE active = 'yes' AND last < ?", inactiveBefore},
	} {
		if _, err := tx.Exec(q.sql, q.arg); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResetCounters zeroes the named counters (daily/monthly/yearly).
func (s *Store) ResetCounters(names ...string) error {
	for _, n := range names {
		if _, err := s.db.Exec("UPDATE counters SET strikes = 0, noises = 0, storms = 0 WHERE name = ?", n); err != nil {
			return err
		}
	}
	return nil
}

// Backup writes a consistent copy of the database and keeps the newest `keep`.
func (s *Store) Backup(dir string, keep int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := filepath.Join(dir, "NSD-"+time.Now().Format("20060102")+".sqlite")
	os.Remove(name)
	if _, err := s.db.Exec("VACUUM INTO ?", name); err != nil {
		return err
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "NSD-*.sqlite"))
	sort.Strings(matches)
	for len(matches) > keep {
		os.Remove(matches[0])
		matches = matches[1:]
	}
	return nil
}

func itoa(n int) string        { return strconv.Itoa(n) }
func i64(n int64) string       { return strconv.FormatInt(n, 10) }
func joinc(a ...string) string { return strings.Join(a, ",") }
