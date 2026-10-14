package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Item struct {
	ID, LibraryID                       int64
	Kind                                string
	ParentID                            sql.NullInt64
	Title, Year, Overview, Genres, Rate string
	Runtime                             int
	Duration                            float64
	Season, Episode, Track              int
	Artist, Album, Path                 string
	Size                                int64
	VideoCodec, AudioCodec, Container   string
	Poster, Backdrop, Subtitles         string
	AddedAt                             string
}

const itemCols = `id,library_id,kind,parent_id,title,year,overview,genres,rating,runtime,duration,season,episode,track,artist,album,path,size,video_codec,audio_codec,container,poster,backdrop,subtitles,added_at`

func scanItem(sc interface{ Scan(...any) error }) (*Item, error) {
	it := &Item{}
	err := sc.Scan(&it.ID, &it.LibraryID, &it.Kind, &it.ParentID, &it.Title, &it.Year, &it.Overview, &it.Genres, &it.Rate, &it.Runtime, &it.Duration,
		&it.Season, &it.Episode, &it.Track, &it.Artist, &it.Album, &it.Path, &it.Size, &it.VideoCodec, &it.AudioCodec, &it.Container,
		&it.Poster, &it.Backdrop, &it.Subtitles, &it.AddedAt)
	return it, err
}

func (a *App) item(id int64) *Item {
	it, err := scanItem(a.DB.QueryRow(`SELECT `+itemCols+` FROM items WHERE id=?`, id))
	if err != nil {
		return nil
	}
	return it
}

func (a *App) items(q string, args ...any) []*Item {
	rows, err := a.DB.Query(`SELECT `+itemCols+` FROM items `+q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		if it, err := scanItem(rows); err == nil {
			out = append(out, it)
		}
	}
	return out
}

// imgURL gives a signed image link (images are long-lived, so cache-friendly 30-day links).
func (a *App) imgURL(r *http.Request, it *Item, which string) string {
	p := it.Poster
	if which == "backdrop" {
		p = it.Backdrop
	}
	if p == "" {
		return ""
	}
	day := time.Now().Truncate(24 * time.Hour).Add(30 * 24 * time.Hour)
	path := fmt.Sprintf("/img/%d/%s", it.ID, which)
	return fmt.Sprintf("%s%s?e=%d&s=%s", baseURL(r), path, day.Unix(), a.sign(path, day.Unix()))
}

func (a *App) card(r *http.Request, it *Item) map[string]any {
	c := map[string]any{"id": it.ID, "type": it.Kind, "title": it.Title, "year": it.Year, "genres": it.Genres,
		"rating": it.Rate, "plot": it.Overview, "poster": a.imgURL(r, it, "poster"), "backdrop": a.imgURL(r, it, "backdrop"),
		"is_free": true, "is_premium": false, "access_level": "free"}
	if c["backdrop"] == "" {
		c["backdrop"] = c["poster"]
	}
	switch it.Kind {
	case "movie":
		c["runtime"] = runtimeStr(it)
		c["quality"] = qualityLabel(it)
	case "series":
		var seasons, eps int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM items WHERE kind='season' AND parent_id=?`, it.ID).Scan(&seasons)
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM items e JOIN items s ON e.parent_id=s.id WHERE e.kind='episode' AND s.parent_id=?`, it.ID).Scan(&eps)
		c["total_seasons"], c["total_episodes"] = seasons, eps
	case "episode":
		c["season_number"], c["episode_number"] = it.Season, it.Episode
		c["label"] = fmt.Sprintf("S%d E%d", it.Season, it.Episode)
		c["runtime"] = runtimeStr(it)
	case "album":
		c["artist"] = it.Artist
	case "track":
		c["artist"], c["album"], c["track"] = it.Artist, it.Album, it.Track
		c["duration"] = int(it.Duration)
	}
	return c
}

func runtimeStr(it *Item) string {
	if it.Runtime > 0 {
		return strconv.Itoa(it.Runtime)
	}
	if it.Duration > 0 {
		return strconv.Itoa(int(it.Duration/60 + 0.5))
	}
	return ""
}

func qualityLabel(it *Item) string {
	return "HD"
}

func (a *App) cards(r *http.Request, list []*Item) []map[string]any {
	out := []map[string]any{}
	for _, it := range list {
		out = append(out, a.card(r, it))
	}
	return out
}

func (a *App) hHome(w http.ResponseWriter, r *http.Request, u *User) {
	limit := clamp(qint(r, "limit", 18), 4, 40)
	movies := a.cards(r, a.items(`WHERE kind='movie' ORDER BY id DESC LIMIT ?`, limit))
	series := a.cards(r, a.items(`WHERE kind='series' ORDER BY id DESC LIMIT ?`, limit))
	var featured any
	if len(movies) > 0 {
		featured = movies[0]
	} else if len(series) > 0 {
		featured = series[0]
	}
	ok(w, map[string]any{"featured": featured, "latest_movies": movies, "latest_series": series,
		"latest_albums": a.cards(r, a.items(`WHERE kind='album' ORDER BY id DESC LIMIT ?`, limit)), "latest_episodes": []any{}})
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (a *App) hListKind(kind string) userHandler {
	return func(w http.ResponseWriter, r *http.Request, u *User) {
		limit := clamp(qint(r, "limit", 50), 1, 200)
		page := clamp(qint(r, "page", 1), 1, 100000)
		ok(w, a.cards(r, a.items(`WHERE kind=? ORDER BY id DESC LIMIT ? OFFSET ?`, kind, limit, (page-1)*limit)))
	}
}

func (a *App) hSearch(w http.ResponseWriter, r *http.Request, u *User) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		fail(w, &apiErr{400, "MISSING_QUERY", "Type something to search.", nil})
		return
	}
	limit := clamp(qint(r, "limit", 30), 5, 60)
	like := "%" + q + "%"
	ok(w, map[string]any{
		"movies": a.cards(r, a.items(`WHERE kind='movie' AND (title LIKE ? OR genres LIKE ? OR overview LIKE ?) ORDER BY title LIMIT ?`, like, like, like, limit)),
		"series": a.cards(r, a.items(`WHERE kind='series' AND (title LIKE ? OR genres LIKE ? OR overview LIKE ?) ORDER BY title LIMIT ?`, like, like, like, limit)),
		"albums": a.cards(r, a.items(`WHERE kind='album' AND (title LIKE ? OR artist LIKE ?) ORDER BY title LIMIT ?`, like, like, limit)),
		"tracks": a.cards(r, a.items(`WHERE kind='track' AND (title LIKE ? OR artist LIKE ? OR album LIKE ?) ORDER BY artist, album, track LIMIT ?`, like, like, like, limit)),
	})
}

func (a *App) hDetails(w http.ResponseWriter, r *http.Request, u *User) {
	id := int64(qint(r, "id", 0))
	it := a.item(id)
	if it == nil || (it.Kind != "movie" && it.Kind != "series" && it.Kind != "album") {
		fail(w, &apiErr{404, "NOT_FOUND", "This title is no longer in the library.", nil})
		return
	}
	item := a.card(r, it)
	item["cast"] = []string{}
	item["trailer_url"], item["youtube_trailer_id"], item["country"] = "", "", ""
	genre := strings.TrimSpace(strings.Split(it.Genres, ",")[0])
	sugQ := `WHERE kind=? AND id<>? ORDER BY RANDOM() LIMIT 12`
	sugArgs := []any{it.Kind, it.ID}
	if genre != "" {
		sugQ = `WHERE kind=? AND id<>? AND genres LIKE ? ORDER BY RANDOM() LIMIT 12`
		sugArgs = []any{it.Kind, it.ID, "%" + genre + "%"}
	}
	data := map[string]any{"item": item, "suggestions": a.cards(r, a.items(sugQ, sugArgs...))}
	switch it.Kind {
	case "series":
		seasons := []map[string]any{}
		for _, s := range a.items(`WHERE kind='season' AND parent_id=? ORDER BY season`, it.ID) {
			eps := []map[string]any{}
			for _, e := range a.items(`WHERE kind='episode' AND parent_id=? ORDER BY episode, title`, s.ID) {
				c := a.card(r, e)
				c["series_id"], c["series_title"] = it.ID, it.Title
				eps = append(eps, c)
			}
			seasons = append(seasons, map[string]any{"id": s.ID, "season_number": s.Season, "title": s.Title, "poster": item["poster"], "episodes": eps})
		}
		data["seasons"] = seasons
	case "album":
		data["tracks"] = a.cards(r, a.items(`WHERE kind='track' AND parent_id=? ORDER BY track, title`, it.ID))
	}
	writeJSON(w, 200, map[string]any{"ok": true, "type": it.Kind, "data": data})
}

func (a *App) hPlay(w http.ResponseWriter, r *http.Request, u *User) {
	id := int64(qint(r, "id", 0))
	it := a.item(id)
	if it != nil && it.Kind == "series" { // first episode
		eps := a.items(`WHERE kind='episode' AND parent_id IN (SELECT id FROM items WHERE kind='season' AND parent_id=?) ORDER BY season, episode LIMIT 1`, it.ID)
		if len(eps) == 0 {
			fail(w, &apiErr{404, "EPISODE_NOT_FOUND", "This series has no episodes yet.", nil})
			return
		}
		it = eps[0]
	}
	if it == nil || it.Path == "" {
		fail(w, &apiErr{404, "VIDEO_NOT_FOUND", "This file is no longer available.", nil})
		return
	}
	video := a.signedURL(baseURL(r), fmt.Sprintf("/stream/%d", it.ID), 12*time.Hour)
	subs := map[string]string{"sq": "", "en": "", "it": ""}
	var subList []map[string]string
	_ = json.Unmarshal([]byte(it.Subtitles), &subList)
	for i, s := range subList {
		u := a.signedURL(baseURL(r), fmt.Sprintf("/stream/%d/sub/%d", it.ID, i), 12*time.Hour)
		if _, known := subs[s["lang"]]; known && subs[s["lang"]] == "" {
			subs[s["lang"]] = u
		} else if subs["en"] == "" {
			subs["en"] = u
		}
	}
	data := a.card(r, it)
	data["video"], data["video_type"], data["subtitles"] = video, mimeFor(it.Path), subs
	data["subtitle_sq"], data["subtitle_en"], data["subtitle_it"] = subs["sq"], subs["en"], subs["it"]
	var pos float64
	_ = a.DB.QueryRow(`SELECT position FROM progress WHERE user_id=? AND item_id=?`, u.ID, it.ID).Scan(&pos)
	data["resume_position"] = pos
	writeJSON(w, 200, map[string]any{"ok": true, "type": it.Kind, "data": data})
}

func mimeFor(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".avi":
		return "video/x-msvideo"
	case ".ts", ".m2ts":
		return "video/mp2t"
	}
	return "video/mp4"
}

// hMusic: ?view=artists | albums[&artist=] | album&id= | tracks&q=
func (a *App) hMusic(w http.ResponseWriter, r *http.Request, u *User) {
	q := r.URL.Query()
	switch q.Get("view") {
	case "artists":
		rows, err := a.DB.Query(`SELECT artist, COUNT(*), MIN(id) FROM items WHERE kind='album' GROUP BY artist ORDER BY artist COLLATE NOCASE`)
		if err != nil {
			fail(w, errServer)
			return
		}
		defer rows.Close()
		list := []map[string]any{}
		for rows.Next() {
			var name string
			var n int
			var albumID int64
			_ = rows.Scan(&name, &n, &albumID)
			img := ""
			if al := a.item(albumID); al != nil {
				img = a.imgURL(r, al, "poster")
			}
			list = append(list, map[string]any{"name": name, "albums": n, "poster": img})
		}
		ok(w, list)
	case "album":
		a.hDetails(w, r, u)
	default:
		if artist := q.Get("artist"); artist != "" {
			ok(w, a.cards(r, a.items(`WHERE kind='album' AND artist=? ORDER BY year, title`, artist)))
			return
		}
		ok(w, a.cards(r, a.items(`WHERE kind='album' ORDER BY id DESC LIMIT ?`, clamp(qint(r, "limit", 100), 1, 500))))
	}
}

// ---- media files (signed links) ----

func (a *App) hStream(w http.ResponseWriter, r *http.Request) {
	if !a.checkSigned(r) {
		http.Error(w, "link expired", http.StatusForbidden)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/stream/"), "/")
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	it := a.item(id)
	if it == nil {
		http.NotFound(w, r)
		return
	}
	path := it.Path
	if len(parts) == 3 && parts[1] == "sub" {
		var subs []map[string]string
		_ = json.Unmarshal([]byte(it.Subtitles), &subs)
		n, _ := strconv.Atoi(parts[2])
		if n < 0 || n >= len(subs) {
			http.NotFound(w, r)
			return
		}
		a.serveSubtitle(w, r, subs[n]["path"])
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "file not available", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", mimeFor(path))
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

func (a *App) hImage(w http.ResponseWriter, r *http.Request) {
	if !a.checkSigned(r) {
		http.Error(w, "link expired", http.StatusForbidden)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/img/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(parts[0], 10, 64)
	it := a.item(id)
	if it == nil {
		http.NotFound(w, r)
		return
	}
	p := it.Poster
	if parts[1] == "backdrop" {
		p = it.Backdrop
	}
	if p == "" {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeFile(w, r, p)
}
