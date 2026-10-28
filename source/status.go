package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Small built-in web server: a status page, /status.json, and read-only
// access to the output folders so another machine can fetch the files over
// HTTP instead of needing FTP.
func serveStatus(ctx context.Context, cfg *Config, e *Engine, shutdown func()) {
	if cfg.HTTPListen == "" {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if r.Method != http.MethodPost || !net.ParseIP(host).IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		logf("stop requested from this PC")
		fmt.Fprintln(w, "stopping")
		go func() { time.Sleep(200 * time.Millisecond); shutdown() }()
	})
	mux.HandleFunc("/status.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		st := e.Status()
		st["version"] = version
		json.NewEncoder(w).Encode(st)
	})
	dirs := map[string]string{"data": cfg.DataDir, "reports": cfg.ReportsDir, "csv": cfg.CSVDir, "exports": cfg.ExportDir}
	for name, d := range dirs {
		prefix := "/" + name + "/"
		fs := http.StripPrefix(prefix, http.FileServer(noListFS{http.Dir(cfg.Path(d))}))
		mux.Handle(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			if r.URL.Path == prefix {
				listDir(w, cfg.Path(dirs[strings.Trim(prefix, "/")]), prefix)
				return
			}
			fs.ServeHTTP(w, r)
		}))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		st := e.Status()
		keys := make([]string, 0, len(st))
		for k := range st {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var rows strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&rows, "<tr><th>%s</th><td>%s</td></tr>", html.EscapeString(strings.ReplaceAll(k, "_", " ")), html.EscapeString(fmt.Sprint(st[k])))
		}
		var logs strings.Builder
		if logSink != nil {
			for _, l := range logSink.Recent(40) {
				logs.WriteString(html.EscapeString(l) + "\n")
			}
		}
		state, cls := "Receiving from NexStorm", "ok"
		if v, _ := st["nexstorm"].(string); v != "ok" {
			state, cls = "NexStorm: "+v, "bad"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, statusPage, version, cls, html.EscapeString(state), rows.String(), logs.String())
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	for {
		ln, err := net.Listen("tcp", cfg.HTTPListen)
		if err != nil {
			warnEvery("http", 30*time.Minute, "status page cannot listen on %s: %v (retrying)", cfg.HTTPListen, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}
		logf("status page on http://%s/", strings.Replace(cfg.HTTPListen, "0.0.0.0", "localhost", 1))
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			warnf("status page stopped: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		return
	}
}

type noListFS struct{ fs http.FileSystem }

func (n noListFS) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err == nil && st.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

func listDir(w http.ResponseWriter, dir, prefix string) {
	entries, _ := os.ReadDir(dir)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>%s</title><style>body{font:14px system-ui;margin:2em}td{padding:2px 12px}</style><h2>%s</h2><table>", prefix, prefix)
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, _ := e.Info()
		size, mod := int64(0), ""
		if info != nil {
			size, mod = info.Size(), info.ModTime().Format("2006-01-02 15:04:05")
		}
		n := html.EscapeString(e.Name())
		fmt.Fprintf(w, "<tr><td><a href=\"%s%s\">%s</a></td><td align=right>%d</td><td>%s</td></tr>", prefix, n, n, size, mod)
	}
	fmt.Fprint(w, "</table><p><a href=\"/\">status</a>")
	_ = filepath.Separator
}

const statusPage = `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="10">
<title>NSD Bridge</title><style>
body{font:14px/1.4 system-ui,Segoe UI,sans-serif;margin:2em;color:#222;max-width:960px}
h1{font-size:20px;margin:0 0 .2em} .v{color:#888;font-size:12px}
.s{padding:.6em 1em;border-radius:6px;margin:1em 0;font-weight:600}
.ok{background:#e6f4ea;color:#1e6b34}.bad{background:#fdecea;color:#a1260d}
table{border-collapse:collapse} th{text-align:left;font-weight:500;color:#555;padding:2px 16px 2px 0} td{padding:2px 0}
pre{background:#f6f6f6;padding:1em;overflow:auto;font-size:12px}
a{color:#0b57d0}</style></head><body>
<h1>NSD Bridge</h1><div class="v">version %s &middot; refreshes every 10 s &middot;
<a href="/data/">data</a> &middot; <a href="/csv/">csv</a> &middot; <a href="/reports/">reports</a> &middot; <a href="/exports/">exports</a> &middot; <a href="/status.json">json</a></div>
<div class="s %s">%s</div>
<table>%s</table>
<h3>Recent log</h3><pre>%s</pre></body></html>`
