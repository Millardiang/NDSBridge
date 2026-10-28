package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Config holds every setting. Keys in nsdbridge.ini use the same names as the
// struct tags below. Legacy NSBackend config.ini keys (ftpactive, username,
// password, server, serverpath, nslmap, nscrop, measure, persistence,
// stormfix, ftprename) are understood too.
type Config struct {
	BaseDir string // folder of the exe; all relative paths resolve from here

	DataDir    string `ini:"data_dir"`
	ReportsDir string `ini:"reports_dir"`
	CSVDir     string `ini:"csv_dir"`
	LogDir     string `ini:"log_dir"`
	DBFile     string `ini:"db_file"`

	// NSD (NSDisplay) outputs
	NSDFiles     bool   `ini:"nsd_files"`
	NSCrop       int    `ini:"nscrop"`
	Measure      string `ini:"measure"`
	Persistence  int    `ini:"persistence"`
	StormFix     bool   `ini:"stormfix"`
	WriteEvery   int    `ini:"write_interval_sec"`
	RealtimeJSON bool   `ini:"realtime_json"`

	// CSV outputs
	CSVExtended   bool `ini:"csv_extended"`
	CSVNSList     bool `ini:"csv_nslist"`
	CSVNoise      bool `ini:"csv_include_noise"`
	CSVNSListMile bool `ini:"csv_nslist_miles"`

	// IPC
	StrikeMem       string `ini:"ipc_strike_name"`
	StormMem        string `ini:"ipc_storm_name"`
	PollMs          int    `ini:"ipc_poll_ms"`
	HeartbeatWarnMn int    `ini:"heartbeat_warn_min"`

	// Delivery
	FTPActive  bool   `ini:"ftpactive"`
	FTPRename  bool   `ini:"ftprename"`
	FTPUser    string `ini:"username"`
	FTPPass    string `ini:"password"`
	FTPServer  string `ini:"server"`
	FTPPath    string `ini:"serverpath"`
	MirrorDir  string `ini:"mirror_dir"`
	NSLMapURL  string `ini:"nslmap"`
	HTTPListen string `ini:"http_listen"`

	// Archive export (nxutil)
	NXUtil       string `ini:"nxutil"`
	ArchiveDir   string `ini:"nexstorm_archive_dir"`
	ExportDir    string `ini:"export_dir"`
	AutoExport   bool   `ini:"auto_export"`
	ExportNoise  bool   `ini:"export_include_noise"`
	ExportTimout int    `ini:"export_timeout_sec"`

	LogMaxMB   int  `ini:"log_max_mb"`
	LogVerbose bool `ini:"log_verbose"`

	source []string // files the config came from
}

func defaultConfig(base string) *Config {
	return &Config{
		BaseDir:         base,
		DataDir:         "data",
		ReportsDir:      "reports",
		CSVDir:          "csv",
		LogDir:          "logs",
		DBFile:          `data\NSD.sqlite`,
		NSDFiles:        true,
		NSCrop:          2000,
		Measure:         "km",
		Persistence:     60,
		WriteEvery:      10,
		RealtimeJSON:    true,
		CSVExtended:     true,
		CSVNSList:       true,
		StrikeMem:       "NXFGIPC_SHMEM_0822931589443_238731_GATE0",
		StormMem:        "NXFGIPC_SHMEM_5216659004766_091723_GATE0",
		PollMs:          1,
		HeartbeatWarnMn: 5,
		FTPRename:       true,
		HTTPListen:      "127.0.0.1:8090",
		NXUtil:          "nxutil.exe",
		ExportDir:       "exports",
		ExportTimout:    300,
		LogMaxMB:        5,
	}
}

// Path resolves a possibly-relative path against BaseDir.
func (c *Config) Path(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.FromSlash(strings.ReplaceAll(p, `\`, "/"))
	if filepath.IsAbs(p) || (len(p) > 1 && p[1] == ':') {
		return p
	}
	return filepath.Join(c.BaseDir, p)
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "y":
		return true, nil
	case "0", "false", "no", "off", "n", "":
		return false, nil
	}
	return false, fmt.Errorf("not a true/false value: %q", s)
}

// loadINI reads key=value lines. Lines starting with # or ; are comments,
// [sections] are ignored (all keys share one namespace).
func loadINI(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || line[0] == '#' || line[0] == ';' || line[0] == '[' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		m[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return m, sc.Err()
}

func (c *Config) apply(m map[string]string) []string {
	var problems []string
	set := func(key string, fn func(string) error) {
		if v, ok := m[key]; ok {
			if err := fn(v); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", key, err))
			}
		}
	}
	str := func(key string, dst *string) { set(key, func(v string) error { *dst = v; return nil }) }
	num := func(key string, dst *int) {
		set(key, func(v string) error {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("not a number: %q", v)
			}
			*dst = n
			return nil
		})
	}
	boo := func(key string, dst *bool) {
		set(key, func(v string) error { b, err := parseBool(v); *dst = b; return err })
	}
	str("data_dir", &c.DataDir)
	str("reports_dir", &c.ReportsDir)
	str("csv_dir", &c.CSVDir)
	str("log_dir", &c.LogDir)
	str("db_file", &c.DBFile)
	boo("nsd_files", &c.NSDFiles)
	num("nscrop", &c.NSCrop)
	str("measure", &c.Measure)
	num("persistence", &c.Persistence)
	boo("stormfix", &c.StormFix)
	num("write_interval_sec", &c.WriteEvery)
	boo("realtime_json", &c.RealtimeJSON)
	boo("csv_extended", &c.CSVExtended)
	boo("csv_nslist", &c.CSVNSList)
	boo("csv_include_noise", &c.CSVNoise)
	boo("csv_nslist_miles", &c.CSVNSListMile)
	str("ipc_strike_name", &c.StrikeMem)
	str("ipc_storm_name", &c.StormMem)
	num("ipc_poll_ms", &c.PollMs)
	num("heartbeat_warn_min", &c.HeartbeatWarnMn)
	boo("ftpactive", &c.FTPActive)
	boo("ftprename", &c.FTPRename)
	str("username", &c.FTPUser)
	str("password", &c.FTPPass)
	str("server", &c.FTPServer)
	str("serverpath", &c.FTPPath)
	str("mirror_dir", &c.MirrorDir)
	str("nslmap", &c.NSLMapURL)
	str("http_listen", &c.HTTPListen)
	str("nxutil", &c.NXUtil)
	str("nexstorm_archive_dir", &c.ArchiveDir)
	str("export_dir", &c.ExportDir)
	boo("auto_export", &c.AutoExport)
	boo("export_include_noise", &c.ExportNoise)
	num("export_timeout_sec", &c.ExportTimout)
	num("log_max_mb", &c.LogMaxMB)
	boo("log_verbose", &c.LogVerbose)
	return problems
}

func (c *Config) validate() []string {
	var p []string
	if c.NSCrop < 1 {
		p = append(p, "nscrop must be at least 1 (using 2000)")
		c.NSCrop = 2000
	}
	if c.Persistence < 1 || c.Persistence > 1440 {
		p = append(p, "persistence must be 1-1440 minutes (using 60)")
		c.Persistence = 60
	}
	c.Measure = strings.ToLower(strings.TrimSpace(c.Measure))
	if c.Measure != "km" && c.Measure != "mi" {
		p = append(p, "measure must be km or mi (using km)")
		c.Measure = "km"
	}
	if c.PollMs < 1 || c.PollMs > 50 {
		p = append(p, "ipc_poll_ms must be 1-50 (using 1)")
		c.PollMs = 1
	}
	if c.WriteEvery < 2 || c.WriteEvery > 300 {
		p = append(p, "write_interval_sec must be 2-300 (using 10)")
		c.WriteEvery = 10
	}
	if c.FTPActive && c.FTPServer == "" {
		p = append(p, "ftpactive is on but no server is set; FTP disabled")
		c.FTPActive = false
	}
	if c.LogMaxMB < 1 {
		c.LogMaxMB = 5
	}
	if c.ExportTimout < 10 {
		c.ExportTimout = 300
	}
	return p
}

const iniName = "nsdbridge.ini"

// LoadConfig reads config.ini (legacy NSBackend) then nsdbridge.ini, later
// files overriding earlier ones. If nsdbridge.ini does not exist it is
// written with every setting so it can be edited.
func LoadConfig(base string) (*Config, []string) {
	c := defaultConfig(base)
	var notes []string
	legacy := filepath.Join(base, "config.ini")
	if m, err := loadINI(legacy); err == nil {
		notes = append(notes, c.apply(m)...)
		c.source = append(c.source, legacy)
	}
	mine := filepath.Join(base, iniName)
	if m, err := loadINI(mine); err == nil {
		notes = append(notes, c.apply(m)...)
		c.source = append(c.source, mine)
	} else if os.IsNotExist(err) {
		if werr := os.WriteFile(mine, []byte(c.render()), 0o644); werr == nil {
			notes = append(notes, "created "+mine+" with default settings")
			c.source = append(c.source, mine)
		}
	}
	notes = append(notes, c.validate()...)
	return c, notes
}

func b2s(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (c *Config) render() string {
	var sb strings.Builder
	w := func(s string, a ...any) { fmt.Fprintf(&sb, s+"\r\n", a...) }
	w("; NSD Bridge settings. Lines starting with ; are comments.")
	w("; Relative paths are relative to the folder this file is in.")
	w("; Values from an old NSBackend config.ini in the same folder are read first;")
	w("; anything set here overrides them.")
	w("")
	w("[folders]")
	w("data_dir=%s", c.DataDir)
	w("reports_dir=%s", c.ReportsDir)
	w("csv_dir=%s", c.CSVDir)
	w("log_dir=%s", c.LogDir)
	w("db_file=%s", c.DBFile)
	w("")
	w("[nsdisplay]")
	w("; Write NSDRealtime.txt, NSDStrikes.txt, NSDStorms.txt etc. exactly as NSBackend did")
	w("nsd_files=%s", b2s(c.NSDFiles))
	w("; max strikes in NSDStrikes.txt")
	w("nscrop=%d", c.NSCrop)
	w("; km or mi (label only - distances stay in km, as NSBackend did)")
	w("measure=%s", c.Measure)
	w("; minutes of strikes kept in NSDStrikes.txt")
	w("persistence=%d", c.Persistence)
	w("; rotate storm bearings by the TRAC alignment")
	w("stormfix=%s", b2s(c.StormFix))
	w("; how often the NSD text files are rewritten (seconds)")
	w("write_interval_sec=%d", c.WriteEvery)
	w("; also write NSDRealtime.json (same data as NSDRealtime.txt, with named fields)")
	w("realtime_json=%s", b2s(c.RealtimeJSON))
	w("")
	w("[csv]")
	w("; csv\\strikes_YYYY-MM-DD.csv : one row per strike, with header, UTC + local time, all fields")
	w("csv_extended=%s", b2s(c.CSVExtended))
	w("; csv\\LYYYYMMDD.csv : same layout as SonotaCo NSList's \"Log CSV file\"")
	w("csv_nslist=%s", b2s(c.CSVNSList))
	w("csv_include_noise=%s", b2s(c.CSVNoise))
	w("; NSList-style file in miles (writes LYYYYMMDDM.csv like NSList did)")
	w("csv_nslist_miles=%s", b2s(c.CSVNSListMile))
	w("")
	w("[nexstorm]")
	w("ipc_strike_name=%s", c.StrikeMem)
	w("ipc_storm_name=%s", c.StormMem)
	w("; how often shared memory is checked for a new strike (ms)")
	w("ipc_poll_ms=%d", c.PollMs)
	w("; warn in the log/status page if NexStorm sends no heartbeat for this long")
	w("heartbeat_warn_min=%d", c.HeartbeatWarnMn)
	w("")
	w("[delivery]")
	w("; copy every output file to this folder too (e.g. a mapped drive or \\\\server\\share)")
	w("mirror_dir=%s", c.MirrorDir)
	w("; built-in status page and read-only file server. 127.0.0.1 = this PC only;")
	w("; use 0.0.0.0:8090 to let other machines (e.g. a Raspberry Pi) fetch the files.")
	w("; Leave empty to switch off.")
	w("http_listen=%s", c.HTTPListen)
	w("ftpactive=%s", b2s(c.FTPActive))
	w("ftprename=%s", b2s(c.FTPRename))
	w("server=%s", c.FTPServer)
	w("username=%s", c.FTPUser)
	w("password=%s", c.FTPPass)
	w("serverpath=%s", c.FTPPath)
	w("; URL of nslmap.php to call once a minute (optional)")
	w("nslmap=%s", c.NSLMapURL)
	w("")
	w("[export]")
	w("; nxutil.exe from Astrogenic, used to turn NexStorm .nex archives into CSV")
	w("nxutil=%s", c.NXUtil)
	w("; folder holding YYYYMMDD.nex archives (blank = look in the usual NexStorm folders)")
	w("nexstorm_archive_dir=%s", c.ArchiveDir)
	w("export_dir=%s", c.ExportDir)
	w("; export yesterday's archive automatically shortly after midnight")
	w("auto_export=%s", b2s(c.AutoExport))
	w("export_include_noise=%s", b2s(c.ExportNoise))
	w("export_timeout_sec=%d", c.ExportTimout)
	w("")
	w("[log]")
	w("log_max_mb=%d", c.LogMaxMB)
	w("log_verbose=%s", b2s(c.LogVerbose))
	return sb.String()
}

func (c *Config) summary() string {
	var lines []string
	lines = append(lines, "config files: "+strings.Join(c.source, ", "))
	outs := []string{}
	if c.NSDFiles {
		outs = append(outs, "NSD files")
	}
	if c.CSVExtended {
		outs = append(outs, "extended CSV")
	}
	if c.CSVNSList {
		outs = append(outs, "NSList CSV")
	}
	if c.FTPActive {
		outs = append(outs, "FTP "+c.FTPServer)
	}
	if c.MirrorDir != "" {
		outs = append(outs, "mirror "+c.MirrorDir)
	}
	if c.HTTPListen != "" {
		outs = append(outs, "http "+c.HTTPListen)
	}
	sort.Strings(outs)
	lines = append(lines, "outputs: "+strings.Join(outs, ", "))
	return strings.Join(lines, "; ")
}
