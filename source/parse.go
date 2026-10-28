package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// NexStorm FlashGate IPC strike record: 15 comma separated integers
//
//	 0 count       increments for every new record (used to spot new data)
//	 1 year        NexStorm PC local date
//	 2 month
//	 3 day
//	 4 seconds     since local midnight
//	 5 bearing     tenths of a degree; -1 = noise, -9 = heartbeat
//	 6 distance
//	 7 alignment   (TRAC antenna alignment in heartbeat records)
//	 8-12          other values; 11 is stored by NSBackend as "corr"
//	13 type        0 = cloud-to-ground, 1 = intracloud; -1 noise, -9 heartbeat
//	14 polarity    0 = positive, 1 = negative
//
// This layout is taken from how SonotaCo NSList and NSBackend decode it.

type RecKind int

const (
	KindStrike RecKind = iota
	KindNoise
	KindHeartbeat
	KindOther
)

func (k RecKind) String() string {
	return [...]string{"strike", "noise", "heartbeat", "other"}[k]
}

type StrikeRec struct {
	Raw      string
	F        [15]int
	Kind     RecKind
	Received time.Time
}

func (r *StrikeRec) Count() int     { return r.F[0] }
func (r *StrikeRec) BearingT() int  { return r.F[5] } // tenths of a degree
func (r *StrikeRec) Distance() int  { return r.F[6] }
func (r *StrikeRec) Corr() int      { return r.F[11] }
func (r *StrikeRec) IsIC() bool     { return r.F[13] == 1 }
func (r *StrikeRec) Negative() bool { return r.F[14] == 1 }

// NSDType is the cg+/cg-/cc+/cc- code NSBackend uses.
func (r *StrikeRec) NSDType() string {
	t := "cg"
	if r.IsIC() {
		t = "cc"
	}
	if r.Negative() {
		return t + "-"
	}
	return t + "+"
}

// NSListType is the label SonotaCo NSList wrote into its CSV (with its leading space).
func (r *StrikeRec) NSListType() string {
	if r.Kind == KindNoise {
		return "noise"
	}
	s := " +"
	if r.Negative() {
		s = " -"
	}
	if r.IsIC() {
		return s + "IC"
	}
	return s + "CG"
}

// NexStormTime is the timestamp NexStorm put in the record (its PC local time).
func (r *StrikeRec) NexStormTime(loc *time.Location) (time.Time, bool) {
	y, m, d, s := r.F[1], r.F[2], r.F[3], r.F[4]
	if y < 2000 || y > 2200 || m < 1 || m > 12 || d < 1 || d > 31 || s < 0 || s > 86400 {
		return time.Time{}, false
	}
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, loc).Add(time.Duration(s) * time.Second), true
}

func atoiStrict(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty field")
	}
	return strconv.Atoi(s)
}

// ParseStrike decodes one shared-memory strike message. Anything malformed
// (e.g. a half-written buffer) is rejected rather than guessed at.
func ParseStrike(raw string) (*StrikeRec, error) {
	raw = strings.TrimRight(raw, "\r\n\x00 ")
	parts := strings.Split(raw, ",")
	if len(parts) != 15 {
		return nil, fmt.Errorf("expected 15 fields, got %d", len(parts))
	}
	r := &StrikeRec{Raw: raw}
	for i, p := range parts {
		n, err := atoiStrict(p)
		if err != nil {
			return nil, fmt.Errorf("field %d: %v", i, err)
		}
		r.F[i] = n
	}
	switch {
	case r.F[5] == -9 || r.F[13] == -9:
		r.Kind = KindHeartbeat
	case r.F[5] == -1 || r.F[13] == -1:
		r.Kind = KindNoise
	case r.F[5] >= 0 && (r.F[13] == 0 || r.F[13] == 1):
		r.Kind = KindStrike
	default:
		r.Kind = KindOther
	}
	return r, nil
}

// StormRec is one TRAC storm message from the second shared memory block.
// Field positions follow NSBackend:
//
//	0 count, 2 year, 3 month, 4 day, 5 detect secs (since midnight),
//	6+7 storm id, 8 last-activity secs, 9 bearing (tenths), 10 distance,
//	13 current rate, 14 max rate, 15 category, 16 trend, 17 total,
//	18 CG+, 19 CG-, 20 CC+, 21 CC-
type StormRec struct {
	Raw     string
	F       []string
	ID      string
	Detect  int64 // unix UTC
	Last    int64 // unix UTC
	DirT    int   // tenths of a degree (after optional stormfix)
	Dist    int
	Cat     int
	Trend   int
	CurRate int
	MaxRate int
	CGP     int
	CGN     int
	CCP     int
	CCN     int
	Total   int
}

// ParseStorm returns (nil, nil) for messages that are valid but carry no storm.
func ParseStorm(raw string, loc *time.Location, align int, stormfix bool) (*StormRec, error) {
	raw = strings.TrimRight(raw, "\r\n\x00 ")
	f := strings.Split(raw, ",")
	if len(f) < 22 {
		if len(f) > 8 {
			return nil, nil // e.g. "no storms" / heartbeat style message
		}
		return nil, fmt.Errorf("expected at least 22 fields, got %d", len(f))
	}
	if f[6] == "?" || f[5] == "0" || f[7] == "-9" {
		return nil, nil
	}
	n := make([]int, len(f))
	for _, i := range []int{2, 3, 4, 5, 8, 9, 10, 13, 14, 15, 16, 17, 18, 19, 20, 21} {
		v, err := atoiStrict(f[i])
		if err != nil {
			return nil, fmt.Errorf("field %d: %v", i, err)
		}
		n[i] = v
	}
	if n[2] < 2000 || n[3] < 1 || n[3] > 12 || n[4] < 1 || n[4] > 31 {
		return nil, fmt.Errorf("bad date %d-%d-%d", n[2], n[3], n[4])
	}
	day := time.Date(n[2], time.Month(n[3]), n[4], 0, 0, 0, 0, loc)
	s := &StormRec{
		Raw:     raw,
		F:       f,
		ID:      strings.TrimSpace(f[6]) + "-" + strings.TrimSpace(f[7]),
		Detect:  day.Add(time.Duration(n[5]) * time.Second).Unix(),
		Last:    day.Add(time.Duration(n[8]) * time.Second).Unix(),
		DirT:    n[9],
		Dist:    n[10],
		CurRate: n[13],
		MaxRate: n[14],
		Cat:     n[15],
		Trend:   n[16],
		Total:   n[17],
		CGP:     n[18],
		CGN:     n[19],
		CCP:     n[20],
		CCN:     n[21],
	}
	if stormfix {
		s.DirT = ((s.DirT+align*10)%3600 + 3600) % 3600
	}
	return s, nil
}

// alignedBearing reproduces NSBackend: whole degrees + TRAC alignment, wrapped to 0-359.
func alignedBearing(bearingTenths, align int) int {
	d := bearingTenths/10 + align
	d %= 360
	if d < 0 {
		d += 360
	}
	return d
}
