package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var version = "1.0.0"

const usage = `NSD Bridge %s - NexStorm/Boltek data logger (replaces NSBackend + NSList)

Usage:
  nsdbridge run                start logging (keeps itself running, restarts after any crash)
  nsdbridge check              test settings, database, NexStorm link, FTP and nxutil
  nsdbridge status             show what the running copy is doing
  nsdbridge export [options]   turn NexStorm .nex archives into CSV using nxutil.exe
      -from YYYY-MM-DD  -to YYYY-MM-DD   (default: yesterday)
      -days N            the last N days up to yesterday
      -single NAME.csv   also combine all days into one file
      -noise             include noise records
  nsdbridge install            start automatically (hidden) when you log on to Windows
  nsdbridge install-admin      same, but run elevated (use if NexStorm runs as administrator)
  nsdbridge uninstall          remove the automatic start
  nsdbridge start | stop       start or stop the background copy now
  nsdbridge simulate [-rate N] feed fake strikes into the shared memory (testing without a Boltek)
  nsdbridge version

Settings are in nsdbridge.ini next to the program (created on first run).
`

func main() {
	cmd := "run"
	args := os.Args[1:]
	if len(args) > 0 {
		cmd, args = strings.ToLower(strings.TrimLeft(args[0], "-/")), args[1:]
	}
	base := exeDir()
	if v := os.Getenv("NSDBRIDGE_HOME"); v != "" {
		base = v
	}
	switch cmd {
	case "run", "start-foreground":
		os.Exit(supervise(base))
	case "worker":
		os.Exit(worker(base))
	case "check":
		os.Exit(check(base))
	case "status":
		os.Exit(status(base))
	case "export":
		os.Exit(export(base, args))
	case "simulate":
		os.Exit(simulate(base, args))
	case "install", "install-admin":
		os.Exit(install(base, cmd == "install-admin"))
	case "uninstall":
		exitErr(uninstallTask(), "automatic start removed")
	case "start":
		exitErr(startTask(), "started (see the status page or logs folder)")
	case "stop":
		exitErr(stopRunning(base), "stopped")
	case "version":
		fmt.Println("NSD Bridge", version)
	default:
		fmt.Printf(usage, version)
		if cmd != "help" && cmd != "h" {
			os.Exit(2)
		}
	}
}

func exitErr(err error, ok string) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(ok)
}

func setupLogging(cfg *Config, name string, echo bool) {
	var e io.Writer
	if echo && hasConsole() {
		e = os.Stderr
	}
	l, err := newRotatingLog(filepath.Join(cfg.Path(cfg.LogDir), name), cfg.LogMaxMB, e)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot open log:", err)
		return
	}
	logSink = l
	logger.SetOutput(l)
	verbose = cfg.LogVerbose
}

// supervise runs the real work in a child process and restarts it if it
// ever exits unexpectedly, with a growing delay so a persistent fault does
// not spin.
func supervise(base string) int {
	cfg, notes := LoadConfig(base)
	setupLogging(cfg, "supervisor.log", true)
	ok, err := singleInstance("NSDBridge-" + lockID(base))
	if err != nil || !ok {
		warnf("another copy of NSD Bridge is already running from %s - exiting", base)
		return 1
	}
	logf("NSD Bridge %s starting in %s", version, base)
	for _, n := range notes {
		logf("config: %s", n)
	}
	exe, _ := os.Executable()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	delay := time.Second
	for {
		cmd := exec.Command(exe, "worker")
		cmd.Env = append(os.Environ(), "NSDBRIDGE_HOME="+base)
		cmd.Dir = base
		stdin, _ := cmd.StdinPipe() // the worker exits if this pipe closes (supervisor gone)
		cmd.Stdout, cmd.Stderr = nil, nil
		if hasConsole() {
			cmd.Stderr = os.Stderr
			cmd.Env = append(cmd.Env, "NSDBRIDGE_ECHO=1")
		}
		hideWindow(cmd)
		started := time.Now()
		if err := cmd.Start(); err != nil {
			errf("cannot start worker: %v", err)
		} else {
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case s := <-sig:
				logf("stopping (%v)", s)
				stdin.Close() // ask the worker to save and exit
				select {
				case <-done:
				case <-time.After(15 * time.Second):
					cmd.Process.Kill()
				}
				return 0
			case err := <-done:
				if err == nil {
					logf("worker exited normally")
					return 0
				}
				errf("worker stopped unexpectedly (%v) after %s", err, time.Since(started).Round(time.Second))
			}
		}
		if time.Since(started) > 10*time.Minute {
			delay = time.Second
		}
		logf("restarting worker in %s", delay)
		select {
		case <-sig:
			return 0
		case <-time.After(delay):
		}
		if delay < time.Minute {
			delay *= 2
		}
	}
}

// stopRunning asks the running copy to save its state and exit (via the
// local status page); if that is not possible it is terminated.
func stopRunning(base string) error {
	cfg, _ := LoadConfig(base)
	if cfg.HTTPListen != "" {
		addr := strings.Replace(cfg.HTTPListen, "0.0.0.0", "127.0.0.1", 1)
		c := &http.Client{Timeout: 5 * time.Second}
		if resp, err := c.Post("http://"+addr+"/shutdown", "text/plain", nil); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				for i := 0; i < 30; i++ {
					time.Sleep(500 * time.Millisecond)
					if _, err := c.Get("http://" + addr + "/status.json"); err != nil {
						return nil
					}
				}
			}
		}
	}
	return killOthers()
}

func lockID(base string) string {
	h := uint32(2166136261)
	for _, c := range strings.ToLower(base) {
		h = (h ^ uint32(c)) * 16777619
	}
	return fmt.Sprintf("%08x", h)
}

func worker(base string) int {
	cfg, _ := LoadConfig(base)
	setupLogging(cfg, "nsdbridge.log", os.Getenv("NSDBRIDGE_ECHO") == "1")
	defer func() {
		if r := recover(); r != nil {
			errf("worker crashed: %v", r)
			os.Exit(3)
		}
	}()
	logf("worker started; %s", cfg.summary())
	highResTimers()

	st, err := OpenStore(cfg.Path(cfg.DBFile))
	if err != nil {
		errf("database: %v", err)
		return 4
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// stop when stdin closes (supervisor asked us to, or it has gone away)
	go func() {
		io.Copy(io.Discard, os.Stdin)
		logf("supervisor gone or asked us to stop; saving and exiting")
		cancel()
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; cancel() }()

	h := &ipcHealth{}
	dl := NewDelivery(cfg)
	eng := NewEngine(cfg, st, dl, h)
	if err := eng.Load(); err != nil {
		errf("loading state: %v", err)
		return 5
	}
	eng.writeReports(time.Now())

	stop := make(chan struct{})
	go strikeReader(cfg.StrikeMem, time.Duration(cfg.PollMs)*time.Millisecond, eng.strikes, h, stop)
	go stormReader(cfg.StormMem, 30*time.Millisecond, eng.storms, h, stop)
	go dl.Run(ctx)
	go serveStatus(ctx, cfg, eng, cancel)

	eng.Run(ctx)
	close(stop)
	return 0
}

func check(base string) int {
	cfg, notes := LoadConfig(base)
	bad := 0
	okf := func(f string, a ...any) { fmt.Printf("  OK    "+f+"\n", a...) }
	nof := func(f string, a ...any) { bad++; fmt.Printf("  FAIL  "+f+"\n", a...) }
	inf := func(f string, a ...any) { fmt.Printf("  info  "+f+"\n", a...) }
	fmt.Println("NSD Bridge", version, "check -", base)
	for _, n := range notes {
		inf("config: %s", n)
	}
	inf("%s", cfg.summary())
	for _, d := range []string{cfg.DataDir, cfg.ReportsDir, cfg.CSVDir, cfg.LogDir} {
		p := cfg.Path(d)
		if err := writeFileAtomic(filepath.Join(p, ".write-test"), []byte("x")); err != nil {
			nof("folder %s not writable: %v", p, err)
		} else {
			os.Remove(filepath.Join(p, ".write-test"))
			okf("folder %s writable", p)
		}
	}
	if st, err := OpenStore(cfg.Path(cfg.DBFile)); err != nil {
		nof("database %s: %v", cfg.Path(cfg.DBFile), err)
	} else {
		c, _ := st.Counters()
		okf("database %s (year %d strikes, month %d)", cfg.Path(cfg.DBFile), c["yearly"].Strikes, c["monthly"].Strikes)
		st.Close()
	}
	if m, err := openSharedMem(cfg.StrikeMem); err != nil {
		nof("NexStorm strike memory: %v", err)
	} else {
		buf := make([]byte, ipcBufSize)
		s := m.Read(buf)
		if r, err := ParseStrike(s); err == nil {
			okf("NexStorm strike memory readable, last record: %s (%s)", s, r.Kind)
		} else if s == "" {
			inf("NexStorm strike memory is empty - NexStorm not running yet, or no data since it started")
		} else {
			inf("NexStorm strike memory holds %q", s)
		}
		m.Close()
	}
	if cfg.FTPActive {
		if f, err := ftpDial(cfg.FTPServer, cfg.FTPUser, cfg.FTPPass); err != nil {
			nof("FTP %s: %v", cfg.FTPServer, err)
		} else if err := f.Cwd(cfg.FTPPath); err != nil {
			nof("FTP %s logged in but cannot change to %q: %v", cfg.FTPServer, cfg.FTPPath, err)
			f.Close()
		} else {
			okf("FTP %s login and folder OK", cfg.FTPServer)
			f.Close()
		}
	}
	if cfg.MirrorDir != "" {
		p := filepath.Join(cfg.Path(cfg.MirrorDir), ".write-test")
		if err := writeFileAtomic(p, []byte("x")); err != nil {
			nof("mirror folder %s: %v", cfg.MirrorDir, err)
		} else {
			os.Remove(p)
			okf("mirror folder %s writable", cfg.MirrorDir)
		}
	}
	if nx, err := findNXUtil(cfg); err != nil {
		inf("%v", err)
	} else {
		okf("nxutil found: %s", nx)
		y := time.Now().AddDate(0, 0, -1)
		if p, looked := findArchive(cfg, y); p != "" {
			okf("yesterday's archive found: %s", p)
		} else {
			inf("no archive for %s found in: %s", y.Format("2006-01-02"), strings.Join(looked, "; "))
		}
	}
	if bad > 0 {
		fmt.Printf("%d problem(s) found\n", bad)
		return 1
	}
	fmt.Println("all checks passed")
	return 0
}

func status(base string) int {
	cfg, _ := LoadConfig(base)
	if cfg.HTTPListen == "" {
		fmt.Println("status page is switched off (http_listen is empty); see the logs folder")
		return 1
	}
	addr := strings.Replace(cfg.HTTPListen, "0.0.0.0", "127.0.0.1", 1)
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + addr + "/status.json")
	if err != nil {
		fmt.Println("NSD Bridge does not appear to be running:", err)
		return 1
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	b, _ := json.MarshalIndent(m, "", "  ")
	fmt.Println(string(b))
	return 0
}

func export(base string, args []string) int {
	cfg, _ := LoadConfig(base)
	setupLogging(cfg, "export.log", true)
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	from := fs.String("from", "", "first day YYYY-MM-DD")
	to := fs.String("to", "", "last day YYYY-MM-DD")
	days := fs.Int("days", 0, "last N days up to yesterday")
	single := fs.String("single", "", "also write one combined CSV")
	noise := fs.Bool("noise", false, "include noise")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	y := time.Now().AddDate(0, 0, -1)
	yd := time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, time.Local)
	f, t := yd, yd
	parse := func(s string) (time.Time, error) {
		for _, l := range []string{"2006-01-02", "20060102", "02/01/2006"} {
			if v, err := time.ParseInLocation(l, s, time.Local); err == nil {
				return v, nil
			}
		}
		return time.Time{}, fmt.Errorf("bad date %q (use YYYY-MM-DD)", s)
	}
	var err error
	if *days > 0 {
		f = yd.AddDate(0, 0, -(*days - 1))
	}
	if *from != "" {
		if f, err = parse(*from); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if *to == "" {
			t = f
		}
	}
	if *to != "" {
		if t, err = parse(*to); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	if t.Before(f) {
		f, t = t, f
	}
	files, err := runExport(cfg, f, t, *single, *noise)
	for _, p := range files {
		fmt.Println(p)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func install(base string, admin bool) int {
	exe := filepath.Join(base, "nsdbridgew.exe")
	if _, err := os.Stat(exe); err != nil {
		exe, _ = os.Executable()
	}
	var err error
	if admin {
		err = installTaskElevated(exe)
	} else {
		err = installTask(exe)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if admin {
			fmt.Fprintln(os.Stderr, "(install-admin must itself be run from an administrator command prompt)")
		}
		return 1
	}
	fmt.Printf("NSD Bridge will start automatically at logon (%s).\nRun \"nsdbridge start\" to start it now.\n", exe)
	return 0
}

// simulate writes plausible NexStorm messages into the shared memory so the
// whole chain can be tested without a Boltek detector.
func simulate(base string, args []string) int {
	cfg, _ := LoadConfig(base)
	fs := flag.NewFlagSet("simulate", flag.ContinueOnError)
	rate := fs.Float64("rate", 20, "strikes per minute")
	dur := fs.Duration("for", 0, "stop after this long (0 = until Ctrl+C)")
	storms := fs.Bool("storms", true, "also simulate a TRAC storm")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fmt.Printf("simulating %.0f strikes/min into %s (Ctrl+C to stop)\n", *rate, cfg.StrikeMem)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	end := time.Time{}
	if *dur > 0 {
		end = time.Now().Add(*dur)
	}
	count := 1000
	stormCount := 1
	next := time.Now()
	hb := time.Now()
	stormAt := time.Now()
	stormStart := time.Now()
	for {
		select {
		case <-sig:
			return 0
		default:
		}
		now := time.Now()
		if !end.IsZero() && now.After(end) {
			return 0
		}
		secs := now.Hour()*3600 + now.Minute()*60 + now.Second()
		if now.Sub(hb) >= 10*time.Second {
			count++
			writeSharedMem(cfg.StrikeMem, fmt.Sprintf("%d,%d,%d,%d,%d,-9,-9,%d,-9,-9,-9,-9,-9,-9,-9", count, now.Year(), now.Month(), now.Day(), secs, 0))
			hb = now
			time.Sleep(5 * time.Millisecond)
		}
		if now.After(next) {
			count++
			kind := rand.Intn(10)
			var msg string
			if kind == 0 {
				msg = fmt.Sprintf("%d,%d,%d,%d,%d,-1,-1,-1,-1,-1,-1,-1,-1,-1,-1", count, now.Year(), now.Month(), now.Day(), secs)
			} else {
				b := 1800 + rand.Intn(900)
				d := 20 + rand.Intn(300)
				typ, pol := rand.Intn(2), rand.Intn(2)
				msg = fmt.Sprintf("%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d", count, now.Year(), now.Month(), now.Day(), secs,
					b, d, d, rand.Intn(500), rand.Intn(500), rand.Intn(500), d, 0, typ, pol)
			}
			writeSharedMem(cfg.StrikeMem, msg)
			fmt.Println(msg)
			gap := time.Duration(float64(time.Minute) / *rate * (0.2 + rand.Float64()*1.6))
			next = now.Add(gap)
		}
		if *storms && now.Sub(stormAt) >= 2*time.Second {
			stormCount++
			ds := stormStart.Hour()*3600 + stormStart.Minute()*60 + stormStart.Second()
			msg := fmt.Sprintf("%d,0,%d,%d,%d,%d,1,1,%d,2250,150,0,0,%d,%d,1,2,%d,%d,%d,%d,%d",
				stormCount, now.Year(), now.Month(), now.Day(), ds, secs, int(*rate), int(*rate)+5, 40, 10, 10, 10, 10)
			writeSharedMem(cfg.StormMem, msg)
			stormAt = now
		}
		time.Sleep(2 * time.Millisecond)
	}
}
