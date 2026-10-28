package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// All formats below reproduce NSBackend 2.12 byte-for-byte so NSDisplay (and
// anything else reading these files) keeps working: lines joined with "\n",
// file ended with "\r\n".

func nsdFile(lines []string) []byte {
	return []byte(strings.Join(lines, "\n") + "\r\n")
}

// NSDStrikes.txt / NSDStrikes20sec.txt: timestamp,distance,direction,type,corr
func fmtStrikes(rows []StrikeRow) []byte {
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("%d,%d,%d,%s,%d", r.TS, r.Dist, r.Dir, r.Type, r.Corr))
	}
	return nsdFile(lines)
}

// NSDStorms.txt / NSDOldStorms.txt (26 fields, EWN/NSLog compatible)
func fmtStorms(rows []StormRow) []byte {
	lines := make([]string, 0, len(rows))
	for _, s := range rows {
		lines = append(lines, fmt.Sprintf("%s,-,%d,%d,%d,-,-,%d,%d,%d,-,-,%d,%d,-,-,-,-,%d,-,%d,%d,%d,%d,%d,%d",
			s.ID, s.Detect, s.DetectDir, s.DetectDist, s.Last, s.Dir, s.Dist, s.CurRate, s.MaxRate,
			s.Cat, s.Trend, s.Total, s.CGP, s.CGN, s.CCP, s.CCN))
	}
	return nsdFile(lines)
}

// NSDGraph.txt: timestamp,total,cg+,cg-,cc+,cc-  (one line per minute, 24 h)
func fmtGraph(rows []GraphRow) []byte {
	lines := make([]string, 0, len(rows))
	for _, g := range rows {
		lines = append(lines, fmt.Sprintf("%d,%d,%d,%d,%d,%d", g.TS, g.Total, g.CGP, g.CGN, g.CCP, g.CCN))
	}
	return nsdFile(lines)
}

type Realtime struct {
	Now                        int64
	Rate, CloseRate            int
	LastStrike                 int64
	LastDir, LastDist          string
	LastType                   string
	LastHour, Day, Month, Year int
	MaxRate                    int
	MaxRateTime                string
	MaxBurst                   int
	MaxBurstTime               string
	CGP, CGN, CCP, CCN         int
	Uptime, Measure            string
	Persistence, Crop          int
}

// NSDRealtime.txt (23 fields)
func fmtRealtime(r *Realtime) []byte {
	line := fmt.Sprintf("%d,%d,%d,%d,%s,%s,%s,%d,%d,%d,%d,%d,%s,%d,%s,%d,%d,%d,%d,%s,%s,%d,%d",
		r.Now, r.Rate, r.CloseRate, r.LastStrike, r.LastDir, r.LastDist, r.LastType,
		r.LastHour, r.Day, r.Month, r.Year, r.MaxRate, r.MaxRateTime, r.MaxBurst, r.MaxBurstTime,
		r.CGP, r.CGN, r.CCP, r.CCN, r.Uptime, r.Measure, r.Persistence, r.Crop)
	return nsdFile([]string{line})
}

func padL(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}

const repRule = "--------------------------------------------------------------------------------------------------------"

// Monthly report NSDReportMMYYYY.txt, same layout as NSBackend.
func fmtReport(month time.Time, days []*DayStats, monthly, yearly Counter) []byte {
	var b strings.Builder
	w := func(s string) { b.WriteString(s + "\r\n") }
	w("")
	w("\tNSDisplay MONTHLY REPORT " + month.Format("2006-01"))
	w("")
	w(repRule)
	w(" Date | Strikes   Storms     Noises  | Maxrate  Time  | Maxstorms  Time  |    CG+    CG-    CC+    CC-")
	w(repRule)
	for _, d := range days {
		day := d.Date
		if len(day) >= 10 {
			day = day[8:10]
		}
		mrt := d.MaxRateTime
		if d.MaxRate == 0 || mrt == "" {
			mrt = "--:--"
		}
		mst := d.MaxStormsTime
		if d.MaxStorms == 0 || mst == "" {
			mst = "--:--"
		}
		w(fmt.Sprintf("  %s  | %s    %s    %s  | %s  %s |   %s  %s | %s %s %s %s",
			day, padL(itoa(d.Strikes), 7), padL(itoa(d.Storms), 6), padL(itoa(d.Noises), 6),
			padL(itoa(d.MaxRate), 7), mrt, padL(itoa(d.MaxStorms), 7), mst,
			padL(itoa(d.CGP), 6), padL(itoa(d.CGN), 6), padL(itoa(d.CCP), 6), padL(itoa(d.CCN), 6)))
	}
	w(repRule)
	w(fmt.Sprintf(" Tot  | %s    %s    %s", padL(itoa(monthly.Strikes), 7), padL(itoa(monthly.Storms), 6), padL(itoa(monthly.Noises), 6)))
	w("--------------------------------------")
	w(fmt.Sprintf(" %s | %s    %s    %s", month.Format("2006"), padL(itoa(yearly.Strikes), 7), padL(itoa(yearly.Storms), 6), padL(itoa(yearly.Noises), 6)))
	return []byte(b.String())
}

// NSDRealtime.json: the same values as NSDRealtime.txt, with names, proper
// numbers and ISO 8601 times added alongside the unix timestamps.
func fmtRealtimeJSON(r *Realtime) []byte {
	iso := func(ts int64) any {
		if ts <= 0 {
			return nil
		}
		return time.Unix(ts, 0).UTC().Format(time.RFC3339)
	}
	num := func(s string) any {
		var n int
		if _, err := fmt.Sscan(s, &n); err == nil {
			return n
		}
		return nil
	}
	lastType := any(r.LastType)
	if r.LastType == "" || r.LastType == "-" {
		lastType = nil
	}
	m := struct {
		Timestamp    int64  `json:"timestamp"`
		TimeUTC      any    `json:"time_utc"`
		Rate         int    `json:"rate_per_min"`
		CloseRate    int    `json:"close_rate_per_min"`
		CloseKm      int    `json:"close_distance_km"`
		LastTS       int64  `json:"last_strike_timestamp"`
		LastTime     any    `json:"last_strike_time_utc"`
		LastDir      any    `json:"last_strike_direction"`
		LastDist     any    `json:"last_strike_distance"`
		LastType     any    `json:"last_strike_type"`
		LastHour     int    `json:"strikes_last_hour"`
		Day          int    `json:"strikes_today"`
		Month        int    `json:"strikes_month"`
		Year         int    `json:"strikes_year"`
		MaxRate      int    `json:"max_rate_per_min"`
		MaxRateTime  string `json:"max_rate_time"`
		MaxBurst     int    `json:"max_burst_per_sec"`
		MaxBurstTime string `json:"max_burst_time"`
		CGP          int    `json:"today_cg_pos"`
		CGN          int    `json:"today_cg_neg"`
		CCP          int    `json:"today_cc_pos"`
		CCN          int    `json:"today_cc_neg"`
		Uptime       string `json:"uptime"`
		Measure      string `json:"measure"`
		Persistence  int    `json:"persistence_min"`
		Crop         int    `json:"max_strikes_in_file"`
	}{r.Now, iso(r.Now), r.Rate, r.CloseRate, 50, r.LastStrike, iso(r.LastStrike), num(r.LastDir), num(r.LastDist), lastType,
		r.LastHour, r.Day, r.Month, r.Year, r.MaxRate, r.MaxRateTime, r.MaxBurst, r.MaxBurstTime,
		r.CGP, r.CGN, r.CCP, r.CCN, r.Uptime, r.Measure, r.Persistence, r.Crop}
	b, _ := json.MarshalIndent(m, "", "  ")
	return append(b, '\r', '\n')
}
