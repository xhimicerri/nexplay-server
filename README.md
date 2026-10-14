# NEXPLAY Server

Personal media server (movies, series, music) for the NEXPLAY apps — like Jellyfin, in one small Go binary.

- `/web/` admin panel: first-run setup, libraries (folder picker), users, devices, settings
- `/API_v2/*.php` the same API the NEXPLAY Android / Android TV apps use (login, home, details, play, TV code sign-in, devices, music)
- `/stream/`, `/img/` signed media links (HTTP range, SRT → WebVTT)
- LAN discovery on UDP 7359 ("NEXPLAY_DISCOVER")
- SQLite database in `%ProgramData%\NEXPLAY Server` (Windows) or `~/.nexplay-server`

## Build
```
go-winres make --in winres/winres.json     # icon + version info (Windows)
go build -ldflags "-s -w" -o build/nexplay-server.exe .
ISCC installer/nexplay-server.iss          # needs third_party/ffmpeg/ffmpeg.exe + ffprobe.exe (gyan.dev essentials)
```
Run in a console: `nexplay-server.exe -data C:\path\to\data -port 8096`.
Service: `nexplay-server.exe -service install|start|stop|uninstall`.

## Test
`python tests/smoke.py http://127.0.0.1:8096` (end-to-end: setup, scan, app API, TV code).
