package main

import (
	"sync"
	"time"
)

// ipcHealth is shared with the status page.
type ipcHealth struct {
	mu            sync.Mutex
	StrikeOpen    bool
	StormOpen     bool
	LastHeartbeat time.Time
	LastStrikeMsg time.Time
	LastStormMsg  time.Time
	Rejected      int
	LastRejected  string
	Align         int
	MessagesSeen  int
	StormMessages int
}

func (h *ipcHealth) snapshot() ipcHealth {
	h.mu.Lock()
	defer h.mu.Unlock()
	return ipcHealth{
		StrikeOpen: h.StrikeOpen, StormOpen: h.StormOpen, LastHeartbeat: h.LastHeartbeat,
		LastStrikeMsg: h.LastStrikeMsg, LastStormMsg: h.LastStormMsg, Rejected: h.Rejected,
		LastRejected: h.LastRejected, Align: h.Align, MessagesSeen: h.MessagesSeen, StormMessages: h.StormMessages,
	}
}

// readStable reads the block until two consecutive reads agree, so a message
// NexStorm is in the middle of writing is never used.
func readStable(m *sharedMem, buf []byte) string {
	a := m.Read(buf)
	for i := 0; i < 5; i++ {
		b := m.Read(buf)
		if a == b {
			return a
		}
		a = b
	}
	return a
}

// strikeReader polls the strike block and sends every new record. It never
// returns; failures are logged and retried.
func strikeReader(name string, poll time.Duration, out chan<- *StrikeRec, h *ipcHealth, stop <-chan struct{}) {
	defer recoverLog("strike reader", func() { go strikeReader(name, poll, out, h, stop) })
	buf := make([]byte, ipcBufSize)
	for {
		m, err := openSharedMem(name)
		if err != nil {
			warnEvery("ipc-open-strike", 10*time.Minute, "%v (retrying)", err)
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		clearWarn("ipc-open-strike", "strike shared memory opened")
		h.mu.Lock()
		h.StrikeOpen = true
		h.mu.Unlock()
		logf("listening for NexStorm strikes on %s", name)

		last := readStable(m, buf)
		lastCount := -1
		if r, err := ParseStrike(last); err == nil {
			lastCount = r.Count()
			// A record already sitting in memory at start-up is normally old
			// (NSBackend counted it again on every restart). Only use it if
			// NexStorm stamped it within the last few seconds.
			if t, ok := r.NexStormTime(time.Local); ok && r.Kind != KindHeartbeat && time.Since(t).Abs() < 3*time.Second {
				r.Received = time.Now()
				out <- r
			}
		}
		for {
			select {
			case <-stop:
				m.Close()
				return
			default:
			}
			s := m.Read(buf)
			if s == last {
				time.Sleep(poll)
				continue
			}
			s = readStable(m, buf)
			last = s
			r, err := ParseStrike(s)
			if err != nil {
				h.mu.Lock()
				h.Rejected++
				h.LastRejected = s
				h.mu.Unlock()
				debugf("ignored malformed strike message %q: %v", s, err)
				continue
			}
			if r.Count() == lastCount {
				continue
			}
			lastCount = r.Count()
			r.Received = time.Now()
			h.mu.Lock()
			h.MessagesSeen++
			h.LastStrikeMsg = r.Received
			if r.Kind == KindHeartbeat {
				h.LastHeartbeat = r.Received
				h.Align = r.F[7]
			}
			h.mu.Unlock()
			out <- r
		}
	}
}

// stormReader does the same for the TRAC storm block.
func stormReader(name string, poll time.Duration, out chan<- string, h *ipcHealth, stop <-chan struct{}) {
	defer recoverLog("storm reader", func() { go stormReader(name, poll, out, h, stop) })
	buf := make([]byte, ipcBufSize)
	for {
		m, err := openSharedMem(name)
		if err != nil {
			warnEvery("ipc-open-storm", 10*time.Minute, "%v (retrying)", err)
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		clearWarn("ipc-open-storm", "storm shared memory opened")
		h.mu.Lock()
		h.StormOpen = true
		h.mu.Unlock()
		last := readStable(m, buf) // whatever is there at start-up is old
		for {
			select {
			case <-stop:
				m.Close()
				return
			default:
			}
			s := m.Read(buf)
			if s == last {
				time.Sleep(poll)
				continue
			}
			s = readStable(m, buf)
			last = s
			h.mu.Lock()
			h.StormMessages++
			h.LastStormMsg = time.Now()
			h.mu.Unlock()
			out <- s
		}
	}
}

const ipcBufSize = 1024

// recoverLog turns a panic in a goroutine into a log entry and a restart
// instead of a crash.
func recoverLog(what string, restart func()) {
	if r := recover(); r != nil {
		errf("%s crashed: %v - restarting it", what, r)
		time.Sleep(time.Second)
		if restart != nil {
			restart()
		}
	}
}
