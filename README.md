# NEXPLAY Server

[Download for Windows](https://nexplaytv.com/#download) · [Website](https://nexplaytv.com)

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

## License
GPL-3.0 — see [LICENSE](LICENSE). The Windows installer bundles FFmpeg (GPL), see `third_party/ffmpeg/`.

## Code signing policy
Free code signing for the Windows installer is requested from [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org).
- Only builds produced by the GitHub Actions workflow in this repository from this source code are signed.
- Committers and reviewers: [@xhimicerri](https://github.com/xhimicerri). Approvers: [@xhimicerri](https://github.com/xhimicerri).
- Privacy: this program does not transfer any information to other networked systems unless specifically requested by the user (optional TMDB metadata lookups when a TMDB API key is set).
