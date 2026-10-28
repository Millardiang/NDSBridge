# Changelog

All notable changes to NSD Bridge are listed here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions use [Semantic Versioning](https://semver.org/).

## [1.0.1] - 2026-10-28

### Added
- `data\NSDRealtime.json`: the same values as `NSDRealtime.txt`, with field names, JSON numbers, and UTC times (`time_utc`, `last_strike_time_utc`). It is written whenever `NSDRealtime.txt` is, and is also served over HTTP, mirrored and uploaded by FTP.
- `realtime_json` setting (default `true`) to switch the JSON file on or off.

### Changed
- The README is now in Markdown (`README.md`), with a settings reference.
- Added this changelog.

## [1.0.0] - 2026-10-28

First release. Replaces NSBackend 2.12 and NSList 1.4.

### Added
- Single self-contained program for Windows 10/11 (64-bit and 32-bit). It needs no .NET and no extra DLLs.
- Reads NexStorm's strike and TRAC storm shared memory directly. The memory is opened once and read only when a message is complete, and malformed messages are rejected.
- NSD outputs byte-for-byte compatible with NSBackend: `NSDRealtime.txt`, `NSDStrikes.txt`, `NSDStrikes20sec.txt`, `NSDStorms.txt`, `NSDOldStorms.txt`, `NSDGraph.txt`, `NSDRaw.txt`, and the monthly reports.
- Uses the existing `NSD.sqlite` database and schema, so year-to-date counters carry over.
- NSList-compatible daily CSV (`LYYYYMMDD.csv`, optionally in miles).
- Fuller daily CSV (`strikes_YYYY-MM-DD.csv`) with a header row, UTC and local times, and every field NexStorm sends.
- `export` command: turns NexStorm `.nex` archives into CSV for a single day or a date range, with an optional combined file. The archive is copied first and repaired automatically if needed.
- Built-in `.nex` reader, used when nxutil is missing or fails.
- Optional automatic export of yesterday's archive after midnight.
- Status page and read-only file server (`http://localhost:8090/`), plus the `status` command.
- Optional mirror folder for copying outputs to a share or mapped drive.
- `check` command to test settings, folders, database, NexStorm link, FTP and nxutil.
- `install` / `install-admin` / `uninstall` / `start` / `stop` commands. These set up a logon scheduled task with no 3-day run limit and restart on failure.
- `simulate` command for testing without a Boltek detector.
- Rotating log files. Each problem is logged once, with a note when it recovers.
- Nightly database backup (last 14 kept).

### Fixed (compared with NSBackend / NSList / nxutil)
- Handle and memory leak from reopening shared memory every few milliseconds.
- Unsupported Async CTP library and unsafe cross-thread window updates.
- Strike counters updated from several threads without locking.
- Database corruption risk from `Synchronous=Off`; the database is now checked at start-up and replaced if it is damaged.
- Half-written output files: all files are now written to a temporary file and then renamed into place.
- FTP and `nslmap` calls stalling file updates: they now run separately, with timeouts and retries.
- Uptime failing on PCs with damaged performance counters or non-English Windows.
- Daily, monthly and yearly counters not resetting if the program was off at midnight.
- "Maxstorms" never resetting each day.
- The stale strike in memory being counted again on every restart.
- Settings being read from the current folder instead of the program folder.
- nxutil 1.1 repeating the last record of every export.
- Days with no strikes being reported as failed exports by nxutil.
- Monthly totals in the report for a finished month: they are now worked out from that month's daily rows.

[1.0.1]: #101---2026-10-28
[1.0.0]: #100---2026-10-28
