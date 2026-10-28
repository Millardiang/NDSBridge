NSD BRIDGE 1.0.1  -  NexStorm / Boltek data logger for modern Windows
=====================================================================

NSD Bridge replaces the 2013 NSD backend (NSBackend.exe + NSList.exe) with a
single program. It reads the live strike and storm data that NexStorm
publishes, and writes:

  * the same NSD files as before, byte-for-byte compatible
    (NSDRealtime.txt, NSDStrikes.txt, NSDStrikes20sec.txt, NSDStorms.txt,
    NSDOldStorms.txt, NSDGraph.txt, NSDRaw.txt, reports\NSDReportMMYYYY.txt)
    and the same NSD.sqlite database, so your year-to-date counters carry on
  * the same daily CSV that NSList's "Log CSV file" option produced
    (csv\LYYYYMMDD.csv)
  * a new, fuller daily CSV with a header row (csv\strikes_YYYY-MM-DD.csv)
  * CSV exports of NexStorm's archive files using nxutil.exe
    (exports\nex_YYYY-MM-DD.csv)

Windows 10 or 11 (64-bit; a 32-bit copy is in the 32bit folder). Nothing else
to install: no .NET, no extra DLLs.


WHAT WAS WRONG WITH THE OLD SOFTWARE
------------------------------------
These were found by taking NSBackend 2.12 and NSList apart:

  * Every 2-4 ms NSBackend opened NexStorm's shared memory again and never
    closed it. Handles and memory leaked until it became unstable - the main
    cause of the "flaky after a few days" behaviour.
  * It was built on the 2011 "Async CTP" preview library, which Microsoft
    never supported on .NET 4.5 and later (what Windows 10/11 have), and it
    updated its window from background threads, which is unsafe and can hang
    or crash a .NET program.
  * Strike counters were changed from several threads at once with no
    locking, so strikes could be lost or counted twice.
  * The database ran with Synchronous=Off, so a power cut could corrupt it.
  * Files were written in place, so the web page or the Pi could read a
    half-written file; a reader holding a file open could crash NSBackend.
  * FTP uploads ran inside the same job that builds the files, so a slow or
    failing server held everything up.
  * Uptime used a Windows performance counter, which fails on PCs where the
    counters are damaged or Windows is not in English.
  * If it was not running at midnight the daily counters were not reset;
    "Maxstorms" was never reset at all (see the stuck "3  22:12" in your
    October report). The strike sitting in memory at start-up was counted
    again on every restart.
  * config.ini was read from the current folder, so starting it from a
    shortcut or at logon could silently use the wrong settings.
  * nxutil.exe has to be run inside the archive folder, works on one day at
    a time, and repeats the last record of every export (a bug in nxutil
    1.1 itself).

NSD Bridge fixes all of these. It also keeps itself running: the work is
done in a child process that is restarted automatically if anything ever
goes wrong, every scheduled job recovers from errors on its own, and if a
CSV is open in Excel the rows are held and written as soon as it is closed.


INSTALLING (MOVING FROM NSBACKEND)
----------------------------------
 1. Stop NSBackend.exe and NSList.exe (and remove them from Startup).
 2. Copy nsdbridge.exe and nsdbridgew.exe into your NSD\Backend folder -
    the one with config.ini and the data folder - so the existing database
    and settings are picked up. Copy nxutil.exe there too.
    (Any folder you can write to works; avoid C:\Program Files.)
 3. Open a Command Prompt in that folder and run:
        nsdbridge check
    This creates nsdbridge.ini (copying your config.ini settings into it)
    and tests the folders, database, NexStorm link, FTP and nxutil.
 4. Try it in the foreground first:
        nsdbridge run
    With NexStorm running you will see it connect. Open
    http://localhost:8090/ in a browser for the status page. Ctrl+C stops.
 5. Make it start by itself at logon:
        nsdbridge install
        nsdbridge start
    If NexStorm runs "as administrator", use "nsdbridge install-admin" from
    an administrator Command Prompt instead (both must run at the same
    level or they cannot see each other).

It runs as a logon task rather than a Windows service on purpose: NexStorm
shares its data inside your logged-in session, which a service cannot see.

Other commands:
    nsdbridge status       what the running copy is doing
    nsdbridge stop         save and stop the background copy
    nsdbridge uninstall    stop starting at logon
    nsdbridge simulate     feed fake strikes in, for testing without a Boltek

After the first run, change settings in nsdbridge.ini (it overrides
config.ini), then run "nsdbridge stop" and "nsdbridge start".


OUTPUT FILES
------------
data\        NSD text files and NSD.sqlite, exactly as NSBackend
             (formats are described in the old NSBackend.txt)
             NSDRealtime.json - the same values as NSDRealtime.txt with
             field names and UTC times (realtime_json=true/false)
data\backup\ a copy of the database every night, last 14 kept
reports\     NSDReportMMYYYY.txt monthly reports, plus NSDReport.txt for
             the current month. Now updated every 20 minutes, and they
             include today.
csv\         LYYYYMMDD.csv           NSList format:
                 year,month,day,hour,min,sec,"type",bearing,distance
                 type is " +CG", " -CG", " +IC", " -IC" (or "noise")
             strikes_YYYY-MM-DD.csv  one row per strike, with header:
                 utc_time, local_time, unix_time  - when it was received
                 nexstorm_time        - NexStorm's own timestamp
                 kind                 - strike (or noise, if enabled)
                 type, polarity       - CG/IC and +/-
                 nsd_type             - cg+ cg- cc+ cc- as in the NSD files
                 bearing_deg          - as reported by NexStorm (0.1 deg)
                 bearing_aligned_deg  - plus the TRAC alignment, as NSD uses
                 distance, corr       - as reported (NexStorm's units)
                 trac_alignment, count, f7..f12 - remaining raw IPC values
exports\     nex_YYYY-MM-DD.csv from NexStorm archives (below)
logs\        nsdbridge.log (rotated at 5 MB), supervisor.log, export.log

Times in the CSVs are written as "2026-10-28 14:05:31.250" so Excel reads
them as date/times.


EXPORTING NEXSTORM ARCHIVES (nxutil)
------------------------------------
    nsdbridge export                       yesterday
    nsdbridge export -from 2026-10-01 -to 2026-10-27
    nsdbridge export -days 7 -single week.csv
    nsdbridge export -from 2026-10-27 -noise

It finds YYYYMMDD.nex in the usual NexStorm folders (or set
nexstorm_archive_dir= in nsdbridge.ini), copies it to a temporary folder so
the live file is never touched, runs nxutil there, repairs the copy with
"nxutil -repair" if needed, removes nxutil's duplicated last record and
writes exports\nex_YYYY-MM-DD.csv with a header:

    utc_time,local_time,date,time,seconds,bearing_deg,distance,
    distance_corrected,type,polarity,x,y,x_corrected,y_corrected

If nxutil.exe is missing or fails, a built-in archive reader (checked to give
identical output to nxutil) is used instead. Set auto_export=true to export
yesterday's archive automatically just after midnight.


GETTING THE FILES TO ANOTHER MACHINE
------------------------------------
Three options, set in nsdbridge.ini (any combination):

  mirror_dir=\\server\share\lightning   copy every output to a share/drive
  http_listen=0.0.0.0:8090              let other machines download files:
                                        http://<pc>:8090/data/NSDRealtime.txt
                                        http://<pc>:8090/csv/  (and reports,
                                        exports). Read-only. Allow the port in
                                        Windows Firewall.
  ftpactive=true + server, username,    upload the NSD files by FTP (as
  password, serverpath                  before, with retries and timeouts)

On a Raspberry Pi, for example:
    wget -q -N http://nexstorm-pc:8090/csv/strikes_$(date +%F).csv


TROUBLESHOOTING
---------------
Status page says "waiting for first heartbeat" / "no heartbeat":
  - Is NexStorm running, and is its IPC/"FlashGate" output enabled?
  - Are both running as the same user and the same elevation (both normal,
    or both "as administrator")?
Nothing in csv\: strikes only appear once NexStorm reports them; run
  "nsdbridge simulate -for 1m" in a second window to test the chain.
Everything that goes wrong is written to logs\nsdbridge.log, once, with a
  note when it recovers.

The NSDisplay web pages (Web folder) are unchanged and still read the same
files. They depend on 2013 jQuery/Highcharts and on nordicweather.net map
and radar services, some of which may no longer exist.

Source code (Go) is in the source folder, with build instructions.
