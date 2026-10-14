package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhowden/tag"
)

var videoExt = map[string]bool{".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".m4v": true, ".webm": true, ".ts": true, ".m2ts": true, ".wmv": true, ".mpg": true, ".mpeg": true}
var audioExt = map[string]bool{".mp3": true, ".flac": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true, ".wav": true, ".wma": true}
var imageNames = []string{"poster", "folder", "cover", "front", "movie"}
var backdropNames = []string{"fanart", "backdrop", "background", "landscape"}

type Library struct {
	app      *App
	mu       sync.Mutex
	running  bool
	Status   string
	Progress int
	Total    int
	LastRun  time.Time
	wake     chan bool
	ffprobe  string
	ffmpeg   string
}

func NewLibrary(a *App) *Library {
	l := &Library{app: a, wake: make(chan bool, 1), Status: "idle"}
	l.ffprobe, l.ffmpeg = findTool("ffprobe"), findTool("ffmpeg")
	return l
}

// findTool looks next to the server executable first (the installer bundles ffmpeg), then in PATH.
func findTool(name string) string {
	exe := name
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if self, err := os.Executable(); err == nil {
		for _, p := range []string{filepath.Join(filepath.Dir(self), exe), filepath.Join(filepath.Dir(self), "ffmpeg", exe)} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if p, err := exec.LookPath(exe); err == nil {
		return p
	}
	return ""
}

// Watch scans at start and then every few hours (or when Scan() is called).
func (l *Library) Watch() {
	time.Sleep(3 * time.Second)
	for {
		l.ScanAll()
		hours := 6
		if h, err := strconv.Atoi(l.app.DB.Setting("scan_hours", "6")); err == nil && h > 0 {
			hours = h
		}
		select {
		case <-l.wake:
		case <-time.After(time.Duration(hours) * time.Hour):
		}
	}
}

func (l *Library) Scan() {
	select {
	case l.wake <- true:
	default:
	}
}

func (l *Library) State() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	last := ""
	if !l.LastRun.IsZero() {
		last = l.LastRun.Format(time.RFC3339)
	}
	return map[string]any{"running": l.running, "status": l.Status, "progress": l.Progress, "total": l.Total, "last_run": last,
		"ffmpeg": l.ffmpeg != "" && l.ffprobe != ""}
}

func (l *Library) set(status string, progress, total int) {
	l.mu.Lock()
	l.Status, l.Progress, l.Total = status, progress, total
	l.mu.Unlock()
}

type libRow struct {
	ID         int64
	Name, Kind string
	Path       string
}

func (l *Library) ScanAll() {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return
	}
	l.running = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.running, l.LastRun, l.Status = false, time.Now(), "idle"
		l.mu.Unlock()
	}()
	rows, err := l.app.DB.Query(`SELECT id,name,kind,path FROM libraries ORDER BY id`)
	if err != nil {
		return
	}
	var libs []libRow
	for rows.Next() {
		var r libRow
		_ = rows.Scan(&r.ID, &r.Name, &r.Kind, &r.Path)
		libs = append(libs, r)
	}
	rows.Close()
	for _, lib := range libs {
		l.scanLibrary(lib)
	}
}

func (l *Library) scanLibrary(lib libRow) {
	start := time.Now()
	scanID := start.UnixNano()
	var files []string
	_ = filepath.WalkDir(lib.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "$recycle.bin" || name == "system volume information" || name == "extras" || name == "featurettes" || name == "samples" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(name)
		if (lib.Kind == "music" && audioExt[ext]) || (lib.Kind != "music" && videoExt[ext] && !strings.Contains(name, "sample")) {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	log.Printf("scan %q (%s): %d files", lib.Name, lib.Kind, len(files))
	for i, f := range files {
		l.set(fmt.Sprintf("%s: %s", lib.Name, filepath.Base(f)), i+1, len(files))
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("scan error %s: %v", f, r)
				}
			}()
			switch lib.Kind {
			case "movies":
				l.addMovie(lib, f, scanID)
			case "series":
				l.addEpisode(lib, f, scanID)
			case "music":
				l.addTrack(lib, f, scanID)
			}
		}()
	}
	// remove what is no longer on disk (children first, parents that lost all children after)
	db := l.app.DB
	_, _ = db.Exec2(`DELETE FROM items WHERE library_id=? AND kind IN ('movie','episode','track') AND seen<>?`, lib.ID, scanID)
	_, _ = db.Exec2(`DELETE FROM items WHERE library_id=? AND kind IN ('season','album') AND id NOT IN (SELECT parent_id FROM items WHERE parent_id IS NOT NULL)`, lib.ID)
	_, _ = db.Exec2(`DELETE FROM items WHERE library_id=? AND kind='series' AND id NOT IN (SELECT parent_id FROM items WHERE parent_id IS NOT NULL)`, lib.ID)
	_, _ = db.Exec2(`UPDATE libraries SET last_scan=datetime('now') WHERE id=?`, lib.ID)
	log.Printf("scan %q done in %s", lib.Name, time.Since(start).Round(time.Second))
}

// ---- names ----

var yearRe = regexp.MustCompile(`[\s._(\[-]((?:19|20)\d{2})(?:[\s._)\]-]|$)`)
var junkRe = regexp.MustCompile(`(?i)[\s._-]+(2160p|1080p|720p|480p|4k|uhd|hdr|x264|x265|h\.?264|h\.?265|hevc|bluray|blu-ray|brrip|bdrip|web-?dl|webrip|hdtv|dvdrip|xvid|aac|ac3|dts|remux|proper|repack|extended|unrated).*$`)
var epRe = regexp.MustCompile(`(?i)(?:s(\d{1,2})[\s._-]*e(\d{1,3})|(\d{1,2})x(\d{1,3})|(?:episode|ep|e)[\s._-]*(\d{1,3}))`)
var seasonDirRe = regexp.MustCompile(`(?i)^(?:season|sezoni|staffel|saison|s)[\s._-]*(\d{1,2})$`)

func cleanTitle(s string) (title, year string) {
	s = strings.TrimSuffix(s, filepath.Ext(s))
	if m := yearRe.FindStringSubmatchIndex(" " + s + " "); m != nil {
		year = (" " + s + " ")[m[2]:m[3]]
		s = (" " + s)[:m[0]]
	}
	s = junkRe.ReplaceAllString(s, "")
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = strings.TrimSpace(strings.Trim(strings.Join(strings.Fields(s), " "), "-([ "))
	return s, year
}

// ---- upsert ----

type itemData struct {
	Kind, Key, Title, Year, Overview, Genres, Rating, Path, Artist, Album, Poster, Backdrop, Subtitles string
	VideoCodec, AudioCodec, Container                                                                  string
	Parent                                                                                             int64
	Season, Episode, Track, Runtime                                                                    int
	Duration                                                                                           float64
	Size, Mtime                                                                                        int64
}

// upsert inserts or updates an item by (library, key); returns its id and whether it was new or changed.
func (l *Library) upsert(lib libRow, d itemData, scanID int64) (id int64, changed bool) {
	db := l.app.DB
	var oldMtime, oldSize int64
	err := db.QueryRow(`SELECT id,mtime,size FROM items WHERE library_id=? AND key=?`, lib.ID, d.Key).Scan(&id, &oldMtime, &oldSize)
	var parent any
	if d.Parent > 0 {
		parent = d.Parent
	}
	if err == sql.ErrNoRows {
		res, err := db.Exec2(`INSERT INTO items(library_id,kind,parent_id,title,sort_title,year,overview,genres,rating,runtime,duration,season,episode,track,artist,album,path,size,mtime,video_codec,audio_codec,container,poster,backdrop,subtitles,key,seen)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			lib.ID, d.Kind, parent, d.Title, strings.ToLower(d.Title), d.Year, d.Overview, d.Genres, d.Rating, d.Runtime, d.Duration, d.Season, d.Episode, d.Track,
			d.Artist, d.Album, d.Path, d.Size, d.Mtime, d.VideoCodec, d.AudioCodec, d.Container, d.Poster, d.Backdrop, d.Subtitles, d.Key, scanID)
		if err != nil {
			log.Printf("insert %s: %v", d.Key, err)
			return 0, false
		}
		id, _ = res.LastInsertId()
		return id, true
	}
	if oldMtime == d.Mtime && oldSize == d.Size && d.Mtime != 0 {
		_, _ = db.Exec2(`UPDATE items SET seen=? WHERE id=?`, scanID, id)
		return id, false
	}
	_, _ = db.Exec2(`UPDATE items SET parent_id=?,title=?,sort_title=?,year=?,season=?,episode=?,track=?,artist=?,album=?,path=?,size=?,mtime=?,duration=CASE WHEN ?>0 THEN ? ELSE duration END,
		video_codec=?,audio_codec=?,container=?,subtitles=?,seen=? WHERE id=?`,
		parent, d.Title, strings.ToLower(d.Title), d.Year, d.Season, d.Episode, d.Track, d.Artist, d.Album, d.Path, d.Size, d.Mtime, d.Duration, d.Duration,
		d.VideoCodec, d.AudioCodec, d.Container, d.Subtitles, scanID, id)
	return id, true
}

func fileInfo(p string) (size, mtime int64) {
	if st, err := os.Stat(p); err == nil {
		return st.Size(), st.ModTime().Unix()
	}
	return 0, 0
}

// ---- movies ----

func (l *Library) addMovie(lib libRow, f string, scanID int64) {
	dir := filepath.Dir(f)
	title, year := cleanTitle(filepath.Base(f))
	if dir != lib.Path { // "Movie Name (2020)/movie.mkv" — the folder name is usually better
		if t, y := cleanTitle(filepath.Base(dir) + ".x"); t != "" && (y != "" || year == "") {
			title, year = t, y
		}
	}
	size, mtime := fileInfo(f)
	d := itemData{Kind: "movie", Key: "m:" + strings.ToLower(f), Title: title, Year: year, Path: f, Size: size, Mtime: mtime, Subtitles: sidecarSubs(f)}
	id, changed := l.upsert(lib, d, scanID)
	if id == 0 || !changed {
		return
	}
	l.probe(id, f)
	l.artwork(id, f, dir != lib.Path)
	l.tmdb(id, "movie", title, year)
}

// ---- series ----

func (l *Library) addEpisode(lib libRow, f string, scanID int64) {
	rel, _ := filepath.Rel(lib.Path, f)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	showName := parts[0]
	if len(parts) == 1 {
		showName = filepath.Base(f)
	}
	showTitle, showYear := cleanTitle(showName + ".x")
	season, episode := 1, 0
	for _, p := range parts[1:max(1, len(parts)-1)] {
		if m := seasonDirRe.FindStringSubmatch(strings.TrimSpace(p)); m != nil {
			season, _ = strconv.Atoi(m[1])
		}
	}
	base := filepath.Base(f)
	if m := epRe.FindStringSubmatch(base); m != nil {
		switch {
		case m[1] != "":
			season, _ = strconv.Atoi(m[1])
			episode, _ = strconv.Atoi(m[2])
		case m[3] != "":
			season, _ = strconv.Atoi(m[3])
			episode, _ = strconv.Atoi(m[4])
		default:
			episode, _ = strconv.Atoi(m[5])
		}
	}
	showDir := filepath.Join(lib.Path, parts[0])
	seriesID, newSeries := l.upsert(lib, itemData{Kind: "series", Key: "s:" + strings.ToLower(showDir), Title: showTitle, Year: showYear}, scanID)
	if seriesID == 0 {
		return
	}
	_, _ = l.app.DB.Exec2(`UPDATE items SET seen=? WHERE id=?`, scanID, seriesID)
	if newSeries {
		l.artworkDir(seriesID, showDir)
		l.tmdb(seriesID, "tv", showTitle, showYear)
	}
	seasonID, _ := l.upsert(lib, itemData{Kind: "season", Key: fmt.Sprintf("se:%s:%d", strings.ToLower(showDir), season), Title: fmt.Sprintf("Season %d", season), Season: season, Parent: seriesID}, scanID)
	_, _ = l.app.DB.Exec2(`UPDATE items SET seen=? WHERE id=?`, scanID, seasonID)
	epTitle := strings.TrimSpace(epRe.Split(strings.TrimSuffix(base, filepath.Ext(base)), 2)[len(epRe.Split(strings.TrimSuffix(base, filepath.Ext(base)), 2))-1])
	epTitle, _ = cleanTitle(epTitle + ".x")
	if epTitle == "" || strings.EqualFold(epTitle, showTitle) {
		epTitle = fmt.Sprintf("Episode %d", episode)
	}
	size, mtime := fileInfo(f)
	id, changed := l.upsert(lib, itemData{Kind: "episode", Key: "e:" + strings.ToLower(f), Title: epTitle, Season: season, Episode: episode, Parent: seasonID,
		Path: f, Size: size, Mtime: mtime, Subtitles: sidecarSubs(f)}, scanID)
	if id == 0 || !changed {
		return
	}
	l.probe(id, f)
	l.artwork(id, f, false)
	// a series/season without its own pictures takes them from its first episode
	for _, col := range []string{"poster", "backdrop"} {
		_, _ = l.app.DB.Exec2(`UPDATE items SET `+col+`=(SELECT `+col+` FROM items WHERE id=?) WHERE id IN (?,?) AND `+col+`=''`, id, seriesID, seasonID)
	}
}

// ---- music ----

func (l *Library) addTrack(lib libRow, f string, scanID int64) {
	title := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
	artist, album, genre, year := "Unknown Artist", filepath.Base(filepath.Dir(f)), "", ""
	track := 0
	var pic *tag.Picture
	if fh, err := os.Open(f); err == nil {
		if m, err := tag.ReadFrom(fh); err == nil {
			if m.Title() != "" {
				title = m.Title()
			}
			if m.AlbumArtist() != "" {
				artist = m.AlbumArtist()
			} else if m.Artist() != "" {
				artist = m.Artist()
			}
			if m.Album() != "" {
				album = m.Album()
			}
			genre = m.Genre()
			if m.Year() > 0 {
				year = strconv.Itoa(m.Year())
			}
			track, _ = m.Track()
			pic = m.Picture()
		}
		fh.Close()
	}
	albumKey := "a:" + strings.ToLower(artist+"|"+album)
	albumID, newAlbum := l.upsert(lib, itemData{Kind: "album", Key: albumKey, Title: album, Artist: artist, Album: album, Year: year, Genres: genre}, scanID)
	if albumID == 0 {
		return
	}
	_, _ = l.app.DB.Exec2(`UPDATE items SET seen=? WHERE id=?`, scanID, albumID)
	if newAlbum {
		if p := findImage(filepath.Dir(f), imageNames); p != "" {
			l.setArt(albumID, p, "")
		} else if pic != nil && len(pic.Data) > 0 {
			out := filepath.Join(l.imgDir(), fmt.Sprintf("%d-poster.%s", albumID, picExt(pic)))
			if os.WriteFile(out, pic.Data, 0o644) == nil {
				l.setArt(albumID, out, "")
			}
		}
	}
	size, mtime := fileInfo(f)
	id, changed := l.upsert(lib, itemData{Kind: "track", Key: "t:" + strings.ToLower(f), Title: title, Artist: artist, Album: album, Year: year, Genres: genre,
		Track: track, Parent: albumID, Path: f, Size: size, Mtime: mtime}, scanID)
	if id != 0 && changed {
		l.probe(id, f)
		_, _ = l.app.DB.Exec2(`UPDATE items SET poster=(SELECT poster FROM items WHERE id=?) WHERE id=?`, albumID, id)
	}
}

func picExt(p *tag.Picture) string {
	if strings.Contains(strings.ToLower(p.MIMEType), "png") {
		return "png"
	}
	return "jpg"
}

// ---- media info / artwork ----

func (l *Library) probe(id int64, f string) {
	if l.ffprobe == "" {
		return
	}
	cmd := exec.Command(l.ffprobe, "-v", "error", "-show_entries", "format=duration,format_name:stream=codec_type,codec_name", "-of", "json", f)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return
	}
	var p struct {
		Format struct {
			Duration string `json:"duration"`
			Name     string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			Type  string `json:"codec_type"`
			Codec string `json:"codec_name"`
		} `json:"streams"`
	}
	if json.Unmarshal(out, &p) != nil {
		return
	}
	dur, _ := strconv.ParseFloat(p.Format.Duration, 64)
	var v, a string
	for _, s := range p.Streams {
		if s.Type == "video" && v == "" && s.Codec != "mjpeg" && s.Codec != "png" {
			v = s.Codec
		}
		if s.Type == "audio" && a == "" {
			a = s.Codec
		}
	}
	_, _ = l.app.DB.Exec2(`UPDATE items SET duration=?, video_codec=?, audio_codec=?, container=? WHERE id=?`, dur, v, a, p.Format.Name, id)
}

func (l *Library) imgDir() string {
	d := filepath.Join(l.app.DataDir, "images")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func findImage(dir string, names []string) string {
	for _, n := range names {
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
			p := filepath.Join(dir, n+ext)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func (l *Library) setArt(id int64, poster, backdrop string) {
	if poster != "" {
		_, _ = l.app.DB.Exec2(`UPDATE items SET poster=? WHERE id=?`, poster, id)
	}
	if backdrop != "" {
		_, _ = l.app.DB.Exec2(`UPDATE items SET backdrop=? WHERE id=?`, backdrop, id)
	}
}

func (l *Library) artworkDir(id int64, dir string) {
	l.setArt(id, findImage(dir, imageNames), findImage(dir, backdropNames))
}

// artwork: images next to the file ("Movie-poster.jpg", folder poster.jpg) or a frame from the video.
func (l *Library) artwork(id int64, f string, ownFolder bool) {
	base := strings.TrimSuffix(f, filepath.Ext(f))
	poster := findImage(filepath.Dir(f), []string{filepath.Base(base) + "-poster", filepath.Base(base)})
	backdrop := findImage(filepath.Dir(f), []string{filepath.Base(base) + "-fanart", filepath.Base(base) + "-backdrop"})
	if ownFolder {
		if poster == "" {
			poster = findImage(filepath.Dir(f), imageNames)
		}
		if backdrop == "" {
			backdrop = findImage(filepath.Dir(f), backdropNames)
		}
	}
	if backdrop == "" && l.ffmpeg != "" {
		var dur float64
		_ = l.app.DB.QueryRow(`SELECT duration FROM items WHERE id=?`, id).Scan(&dur)
		at := 60.0
		if dur > 0 {
			at = dur * 0.25
		}
		out := filepath.Join(l.imgDir(), fmt.Sprintf("%d-backdrop.jpg", id))
		cmd := exec.Command(l.ffmpeg, "-nostdin", "-y", "-v", "error", "-ss", fmt.Sprintf("%.0f", at), "-i", f, "-frames:v", "1",
			"-vf", "scale=1280:720:force_original_aspect_ratio=increase,crop=1280:720", "-q:v", "3", out)
		hideWindow(cmd)
		if cmd.Run() == nil {
			backdrop = out
			if poster == "" {
				pout := filepath.Join(l.imgDir(), fmt.Sprintf("%d-poster.jpg", id))
				c2 := exec.Command(l.ffmpeg, "-nostdin", "-y", "-v", "error", "-i", out, "-filter_complex",
					"[0:v]split=2[a][b];[a]scale=600:900:force_original_aspect_ratio=increase,crop=600:900,boxblur=25:2,eq=brightness=-0.2[bg];[b]scale=600:-2[fg];[bg][fg]overlay=0:(H-h)/2",
					"-q:v", "3", pout)
				hideWindow(c2)
				if c2.Run() == nil {
					poster = pout
				}
			}
		}
	}
	l.setArt(id, poster, backdrop)
}

// ---- subtitles ----

var subLangRe = regexp.MustCompile(`(?i)\.(sq|alb|al|en|eng|it|ita|de|fr|es|tr|mk|sr|hr)(?:\.forced)?$`)

// sidecarSubs finds "Movie.srt", "Movie.en.srt", "Movie.sq.vtt" next to the video; JSON [{path,lang}].
func sidecarSubs(f string) string {
	base := strings.TrimSuffix(f, filepath.Ext(f))
	matches, _ := filepath.Glob(globEscape(base) + "*")
	var list []map[string]string
	for _, m := range matches {
		ext := strings.ToLower(filepath.Ext(m))
		if ext != ".srt" && ext != ".vtt" {
			continue
		}
		stem := strings.TrimSuffix(m, filepath.Ext(m))
		lang := "en"
		if mm := subLangRe.FindStringSubmatch(stem); mm != nil {
			switch strings.ToLower(mm[1]) {
			case "sq", "alb", "al":
				lang = "sq"
			case "it", "ita":
				lang = "it"
			case "en", "eng":
				lang = "en"
			default:
				lang = strings.ToLower(mm[1])
			}
		}
		list = append(list, map[string]string{"path": m, "lang": lang})
	}
	if len(list) == 0 {
		return ""
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func globEscape(s string) string {
	return strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]").Replace(s)
}

var srtTime = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

// serveSubtitle sends WebVTT (SRT is converted on the fly; the players expect VTT).
func (a *App) serveSubtitle(w http.ResponseWriter, r *http.Request, p string) {
	b, err := os.ReadFile(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !utf8Valid(b) {
		b = latin1ToUTF8(b)
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	if strings.EqualFold(filepath.Ext(p), ".vtt") {
		_, _ = w.Write(b)
		return
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = srtTime.ReplaceAllString(s, "$1.$2")
	_, _ = w.Write([]byte("WEBVTT\n\n" + s))
}

func utf8Valid(b []byte) bool { return strings.ToValidUTF8(string(b), "�") == string(b) }

func latin1ToUTF8(b []byte) []byte {
	// Windows-1250/1252 subtitles (common for Albanian files): map the bytes as Latin-1, keeping ë and ç readable.
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return []byte(string(r))
}
