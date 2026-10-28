# NSD Bridge 1.0.1

**NexStorm / Boltek lightning data logger for modern Windows**

NSD Bridge replaces the 2013 NSD backend (`NSBackend.exe` + `NSList.exe`) with a single program. It reads the live strike and storm data that NexStorm publishes, and writes:

- **The same NSD files as before**, byte-for-byte compatible: `NSDRealtime.txt`, `NSDStrikes.txt`, `NSDStrikes20sec.txt`, `NSDStorms.txt`, `NSDOldStorms.txt`, `NSDGraph.txt`, `NSDRaw.txt` and `reports\NSDReportMMYYYY.txt`, plus the same `NSD.sqlite` database, so your year-to-date counters carry on.
- **`NSDRealtime.json`**: the realtime data as JSON, with field names.
- **The NSList daily CSV** (`csv\LYYYYMMDD.csv`), the same file NSList's "Log CSV file" option produced.
- **A fuller daily CSV** with a header row (`csv\strikes_YYYY-MM-DD.csv`).
- **CSV exports of NexStorm's archive files** (`exports\nex_YYYY-MM-DD.csv`), made with `nxutil.exe` or a built-in reader.

**Requirements:** Windows 10 or 11. The main programs are 64-bit; a 32-bit copy is in the `32bit` folder. There is nothing else to install: no .NET and no extra DLLs.

See [CHANGELOG.md](CHANGELOG.md) for the version history.

---

## Contents

- [What was wrong with the old software](#what-was-wrong-with-the-old-software)
- [Installing (moving from NSBackend)](#installing-moving-from-nsbackend)
- [Commands](#commands)
- [Settings](#settings)
- [Output files](#output-files)
- [Exporting NexStorm archives](#exporting-nexstorm-archives)
- [Getting the files to another machine](#getting-the-files-to-another-machine)
- [Troubleshooting](#troubleshooting)
- [Building from source](#building-from-source)

---

## What was wrong with the old software

These problems were found by taking NSBackend 2.12 and NSList apart:

| Problem | Effect |
|---|---|
| NSBackend reopened NexStorm's shared memory every 2–4 ms and never closed it | Handles and memory leaked until it became unstable. This was the main cause of it being "flaky after a few days". |
| It was built on the 2011 "Async CTP" preview library | Microsoft never supported this library on .NET 4.5 and later, which is what Windows 10/11 have. |
| It updated its window from background threads | This is unsafe and can hang or crash a .NET program. |
| Strike counters were changed from several threads with no locking | Strikes could be lost or counted twice. |
| The database ran with `Synchronous=Off` | A power cut could corrupt it. |
| Files were overwritten in place | A web server or another machine could read a half-written file, and a reader holding a file open could crash NSBackend. |
| FTP uploads ran inside the job that builds the files | A slow or failing server held everything up. |
| Uptime came from a Windows performance counter | This fails on PCs where the counters are damaged or Windows is not in English. |
| The daily reset only ran if NSBackend was running at midnight | Counters carried over into the next day. |
| "Maxstorms" was never reset | The monthly report showed the same maximum every day. |
| The strike already in memory at start-up was treated as new | It was counted again on every restart. |
| `config.ini` was read from the current folder | Starting from a shortcut or at logon could silently use the wrong settings. |
| `nxutil.exe` must run inside the archive folder and does one day per run | Exports were awkward to do. |
| nxutil 1.1 repeats the last record of every export | Every exported day had one duplicated strike. This is a bug in nxutil itself. |

NSD Bridge fixes all of these. It also keeps itself running:

- **Crash recovery:** the work runs in a child process that restarts automatically if anything goes wrong.
- **Error recovery:** every scheduled job recovers from errors on its own.
- **Locked files:** if a CSV is open in Excel, new rows are held and written as soon as the file is closed.
- **Database safety:** the database is checked at start-up and backed up every night.

---

## Installing (moving from NSBackend)

1. **Stop the old programs.** Close `NSBackend.exe` and `NSList.exe`, and remove them from Startup.
2. **Copy the files in.** Put `nsdbridge.exe` and `nsdbridgew.exe` in your `NSD\Backend` folder (the one with `config.ini` and the `data` folder), so the existing database and settings are picked up. Copy `nxutil.exe` there too.
   > Any folder you can write to will work, but avoid `C:\Program Files`.
3. **Check the setup.** Open a Command Prompt in that folder and run:
   ```
   nsdbridge check
   ```
   This creates `nsdbridge.ini` (copying your `config.ini` settings into it). It then tests the folders, the database, the NexStorm link, FTP and nxutil.
4. **Try it in the foreground first:**
   ```
   nsdbridge run
   ```
   With NexStorm running you will see it connect. Open <http://localhost:8090/> in a browser for the status page. Press Ctrl+C to stop.
5. **Make it start by itself at logon:**
   ```
   nsdbridge install
   nsdbridge start
   ```
   If NexStorm runs "as administrator", use `nsdbridge install-admin` from an administrator Command Prompt instead. Both programs must run at the same level, or they cannot see each other.

> **Why a logon task and not a Windows service?** NexStorm shares its data inside your logged-in session, which a service cannot see. The logon task has no 3-day run limit (Windows applies one by default) and is restarted if it fails.

### Upgrading

```
nsdbridge stop
```
Replace both `.exe` files, then:
```
nsdbridge start
```

---

## Commands

| Command | What it does |
|---|---|
| `nsdbridge run` | Start logging in this window. It restarts itself after any crash. |
| `nsdbridge check` | Test settings, the database, the NexStorm link, FTP and nxutil. |
| `nsdbridge status` | Show what the running copy is doing. |
| `nsdbridge export [options]` | Turn NexStorm `.nex` archives into CSV ([details](#exporting-nexstorm-archives)). |
| `nsdbridge install` | Start automatically (hidden) at Windows logon. |
| `nsdbridge install-admin` | Same as `install`, but run elevated. |
| `nsdbridge uninstall` | Remove the automatic start. |
| `nsdbridge start` / `stop` | Start, or save and stop, the background copy. |
| `nsdbridge simulate [-rate N] [-for 1m]` | Feed fake strikes in, for testing without a Boltek. |
| `nsdbridge version` | Show the version. |

`nsdbridgew.exe` is the same program without a console window. The logon task uses it.

---

## Settings

Settings are in `nsdbridge.ini` next to the program. It is created on first run, copying any values from an old NSBackend `config.ini`. After that, `nsdbridge.ini` takes priority over `config.ini`. After changing a setting, run `nsdbridge stop` then `nsdbridge start`.

| Setting | Default | Meaning |
|---|---|---|
| `data_dir`, `reports_dir`, `csv_dir`, `log_dir` | `data`, `reports`, `csv`, `logs` | Output folders, relative to the program folder |
| `nsd_files` | `true` | Write the NSD text files |
| `realtime_json` | `true` | Also write `NSDRealtime.json` |
| `nscrop` | `2000` | Maximum strikes in `NSDStrikes.txt` |
| `persistence` | `60` | Minutes of strikes kept in `NSDStrikes.txt` |
| `measure` | `km` | `km` or `mi`. This is a label only; distances stay in NexStorm's units, as before. |
| `stormfix` | `false` | Rotate storm bearings by the TRAC alignment |
| `write_interval_sec` | `10` | How often the NSD files are rewritten |
| `csv_extended` | `true` | Write `strikes_YYYY-MM-DD.csv` |
| `csv_nslist` | `true` | Write the NSList-style `LYYYYMMDD.csv` |
| `csv_include_noise` | `false` | Include noise records in the CSVs |
| `csv_nslist_miles` | `false` | Write the NSList file in miles (`LYYYYMMDDM.csv`) |
| `heartbeat_warn_min` | `5` | Warn if NexStorm sends no heartbeat for this many minutes |
| `mirror_dir` | *(empty)* | Copy every output to this folder or share |
| `http_listen` | `127.0.0.1:8090` | Address for the status page and file server. Leave empty to switch it off. |
| `ftpactive`, `server`, `username`, `password`, `serverpath`, `ftprename` | | FTP upload, as in NSBackend |
| `nslmap` | *(empty)* | URL of `nslmap.php` to call once a minute |
| `nxutil` | `nxutil.exe` | Path to nxutil |
| `nexstorm_archive_dir` | *(auto)* | Folder holding the `YYYYMMDD.nex` archives |
| `export_dir` | `exports` | Where archive exports go |
| `auto_export` | `false` | Export yesterday's archive just after midnight |
| `export_include_noise` | `false` | Include noise in exports |
| `log_max_mb`, `log_verbose` | `5`, `false` | Log rotation size and detail |

---

## Output files

### `data\`

The NSD text files and `NSD.sqlite`, exactly as NSBackend wrote them. Their formats are described in the old `NSBackend.txt`.

`data\backup\` holds a copy of the database from every night; the last 14 are kept.

#### `NSDRealtime.json`

This file holds the same values as `NSDRealtime.txt`, in the same order. The differences are:

- each value has a name;
- numbers are real JSON numbers, not text;
- two UTC times are added: `time_utc` and `last_strike_time_utc`.

Switch it off with `realtime_json=false`.

```json
{
  "timestamp": 1793180215,
  "time_utc": "2026-10-28T09:36:55Z",
  "rate_per_min": 18,
  "close_rate_per_min": 2,
  "close_distance_km": 50,
  "last_strike_timestamp": 1793180213,
  "last_strike_time_utc": "2026-10-28T09:36:53Z",
  "last_strike_direction": 183,
  "last_strike_distance": 261,
  "last_strike_type": "cc+",
  "strikes_last_hour": 64,
  "strikes_today": 102,
  "strikes_month": 18624,
  "strikes_year": 169884,
  "max_rate_per_min": 46,
  "max_rate_time": "09:18",
  "max_burst_per_sec": 3,
  "max_burst_time": "09:18",
  "today_cg_pos": 20,
  "today_cg_neg": 20,
  "today_cc_pos": 34,
  "today_cc_neg": 28,
  "uptime": "0 days 0 hours 0 min",
  "measure": "km",
  "persistence_min": 60,
  "max_strikes_in_file": 5000
}
```

If no strike has been received since start-up, `last_strike_type` is `null`.

### `reports\`

- `NSDReportMMYYYY.txt`: the monthly reports.
- `NSDReport.txt`: the current month's report.

Both are now updated every 20 minutes and include today.

### `csv\`

**`LYYYYMMDD.csv`** uses the NSList format, with no header row:

```
year,month,day,hour,min,sec,"type",bearing,distance
2026,10,28,09,05,25," +CG",233.6,155
```

`type` is one of `" +CG"`, `" -CG"`, `" +IC"`, `" -IC"` or `"noise"`. The leading space is how NSList wrote it.

**`strikes_YYYY-MM-DD.csv`** has one row per strike, with a header row:

| Column | Meaning |
|---|---|
| `utc_time`, `local_time`, `unix_time` | When the strike was received |
| `nexstorm_time` | NexStorm's own timestamp |
| `kind` | `strike` (or `noise`, if enabled) |
| `type`, `polarity` | `CG`/`IC` and `+`/`-` |
| `nsd_type` | `cg+`, `cg-`, `cc+` or `cc-`, as in the NSD files |
| `bearing_deg` | As reported by NexStorm (0.1° resolution) |
| `bearing_aligned_deg` | Bearing plus the TRAC alignment, as the NSD files use |
| `distance`, `corr` | As reported, in NexStorm's units |
| `trac_alignment`, `count`, `f7`…`f12` | The remaining raw values NexStorm sends |

Times are written as `2026-10-28 14:05:31.250`, so Excel reads them as dates and times.

### `exports\`

`nex_YYYY-MM-DD.csv` files made from NexStorm's archives; see [below](#exporting-nexstorm-archives).

### `logs\`

- `nsdbridge.log`: rotated at 5 MB.
- `supervisor.log`: from the part that keeps the logger running.
- `export.log`: from archive exports.

---

## Exporting NexStorm archives

```
nsdbridge export                                   (yesterday)
nsdbridge export -from 2026-10-01 -to 2026-10-27
nsdbridge export -days 7 -single week.csv
nsdbridge export -from 2026-10-27 -noise
```

For each day, NSD Bridge:

1. finds `YYYYMMDD.nex` in the usual NexStorm folders (or in `nexstorm_archive_dir`);
2. copies it to a temporary folder, so NexStorm's live file is never touched;
3. runs nxutil on the copy, and repairs the copy with `nxutil -repair` if needed;
4. removes nxutil's duplicated last record;
5. writes `exports\nex_YYYY-MM-DD.csv` with this header:
   ```
   utc_time,local_time,date,time,seconds,bearing_deg,distance,distance_corrected,type,polarity,x,y,x_corrected,y_corrected
   ```

If `nxutil.exe` is missing or fails, a built-in archive reader is used instead. It was checked to give the same output as nxutil.

Set `auto_export=true` to export yesterday's archive automatically just after midnight.

---

## Getting the files to another machine

There are three options in `nsdbridge.ini`, and you can use any combination of them.

| Option | Setting | Notes |
|---|---|---|
| Shared folder | `mirror_dir=\\server\share\lightning` | Copies every output to a share or mapped drive |
| HTTP | `http_listen=0.0.0.0:8090` | Other machines can download files, e.g. `http://<pc>:8090/data/NSDRealtime.json` or `http://<pc>:8090/csv/`. Access is read-only. Allow the port in Windows Firewall. |
| FTP | `ftpactive=true`, plus `server`, `username`, `password`, `serverpath` | Uploads the NSD files as before, now with retries and timeouts |

For example, on a Raspberry Pi:

```bash
wget -q -N http://nexstorm-pc:8090/csv/strikes_$(date +%F).csv
curl -s http://nexstorm-pc:8090/data/NSDRealtime.json
```

---

## Troubleshooting

**The status page says "waiting for first heartbeat" or "no heartbeat"**
- Check that NexStorm is running and its IPC ("FlashGate") output is enabled.
- Check that both programs run as the same user at the same level (both normal, or both "as administrator").

**Nothing appears in `csv\`**
- Rows only appear once NexStorm reports strikes. To test the whole chain, run `nsdbridge simulate -for 1m` in a second window.

**Anything else**
- Every problem is written once to `logs\nsdbridge.log`, with a note when it recovers.

> The NSDisplay web pages (`Web` folder) are unchanged and still read the same files. They depend on 2013 jQuery/Highcharts and on nordicweather.net map and radar services, some of which may no longer exist.

---

## Building from source

The Go source is in the `source` folder; see `source/BUILD.txt`. In short, you need Go 1.22 or later and a C compiler (mingw-w64), because SQLite is compiled in:

```
go build -ldflags "-s -w" -o nsdbridge.exe .
go build -ldflags "-s -w -H windowsgui" -o nsdbridgew.exe .
```
