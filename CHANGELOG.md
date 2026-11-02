# Changelog

All notable changes to NSD Bridge are listed here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions use [Semantic Versioning](https://semver.org/).

## [1.3.1] - 2026-11-02

### Fixed
- The JSON strike list could show only the strikes since the last restart instead of the whole day, while `strikes_today` showed the full count. This happened because versions before 1.3.0 removed strikes from the database after about an hour. At start-up, any of today's strikes missing from the database are now restored from `csv\strikes_YYYY-MM-DD.csv`. Strikes already stored are not added twice, and the restored strikes keep their exact bearing and coordinates.

## [1.3.0] - 2026-11-02

### Changed
- The `strikes` list in `NSDRealtime.json` now covers the whole current day (since local midnight) instead of the last 60 minutes. Its count matches `strikes_today`, and it survives restarts.
- In the JSON, `strikes_window_min` is replaced by `strikes_window` (`"today"` or `"N min"`), `strikes_since` and `strikes_since_utc`.
- The strike list is written one strike per line, which keeps a full day's file compact.
- The database keeps today's strikes, not just the last hour.

### Added
- `realtime_json_window` setting: `today` (default) or a number of minutes.
- `realtime_json_max_strikes` setting: the most strikes to list, keeping the newest (default `0`, no limit).

### Unchanged
- `NSDStrikes.txt`, `NSDStrikes20sec.txt` and `strikes_last_hour` still use the `persistence` window, as NSDisplay expects.

## [1.2.1] - 2026-11-02

### Fixed
- After a restart, these values in `NSDRealtime.json` / `.txt` went back to 0 or `null` until the next strike: the strike rate, the close-strike rate, and the last-strike time, direction, distance, type and coordinates. They are now restored from the database at start-up. NSBackend had the same behaviour.
- A forced stop (power cut, crash, Task Manager) could lose up to 60 seconds of counts. Strikes are now saved to the database every second and counters every 10 seconds (`write_interval_sec`).

## [1.2.0] - 2026-11-02

### Added
- A `strikes` list in `NSDRealtime.json` with every strike from the last `persistence` minutes (at most `nscrop`, newest first). This is the same set of strikes as `NSDStrikes.txt`. Each entry has the time, whole-degree direction, exact bearing, distance, type and calculated `lat`/`lon`.
- `station`, `strikes_window_min` and `strikes_count` in `NSDRealtime.json`.
- `realtime_json_strikes` setting (default `true`) to switch the list on or off.
- The database now stores each strike's exact bearing and position (new `bearing`, `lat` and `lon` columns in the `strikes` table), so coordinates are kept across restarts. The columns are added automatically to existing databases.

### Notes
- Strikes recorded by NSBackend, before the upgrade, have no stored position. If a station position is set, their coordinates are worked out from the whole-degree direction instead.

## [1.1.0] - 2026-11-02

### Added
- `last_strike_lat` and `last_strike_lon` in `NSDRealtime.json`: the calculated position of the last strike. It is worked out from the station position, the bearing (at full 0.1° resolution, with the TRAC alignment applied) and the distance. Both values are `null` until a station position is set.
- `station_lat` and `station_lon` settings for the detector's position, in decimal degrees.
- `distance_unit` setting (`km` or `mi`) for the units NexStorm reports distances in.

### Unchanged
- `NSDRealtime.txt` and all other NSD files keep their exact NSBackend format.

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

[1.3.1]: #131---2026-11-02
[1.3.0]: #130---2026-11-02
[1.2.1]: #121---2026-11-02
[1.2.0]: #120---2026-11-02
[1.1.0]: #110---2026-11-02
[1.0.1]: #101---2026-10-28
[1.0.0]: #100---2026-10-28
