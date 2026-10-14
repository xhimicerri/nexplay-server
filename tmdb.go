package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Optional metadata from TMDB (themoviedb.org): the admin pastes a free API key in Settings.
// Without a key the server still works with names from the files and frames from the videos.

var tmdbClient = &http.Client{Timeout: 12 * time.Second}

var tmdbGenres = map[int]string{28: "Action", 12: "Adventure", 16: "Animation", 35: "Comedy", 80: "Crime", 99: "Documentary", 18: "Drama",
	10751: "Family", 14: "Fantasy", 36: "History", 27: "Horror", 10402: "Music", 9648: "Mystery", 10749: "Romance", 878: "Sci-Fi",
	53: "Thriller", 10752: "War", 37: "Western", 10759: "Action & Adventure", 10762: "Kids", 10765: "Sci-Fi & Fantasy", 10768: "War & Politics"}

func (l *Library) tmdb(id int64, kind, title, year string) {
	key := strings.TrimSpace(l.app.DB.Setting("tmdb_key", ""))
	if key == "" || title == "" {
		return
	}
	q := url.Values{"api_key": {key}, "query": {title}, "language": {l.app.DB.Setting("metadata_language", "en-US")}}
	if year != "" {
		if kind == "movie" {
			q.Set("year", year)
		} else {
			q.Set("first_air_date_year", year)
		}
	}
	resp, err := tmdbClient.Get("https://api.themoviedb.org/3/search/" + kind + "?" + q.Encode())
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var res struct {
		Results []struct {
			Title, Name, Overview          string
			ReleaseDate                    string  `json:"release_date"`
			FirstAir                       string  `json:"first_air_date"`
			Vote                           float64 `json:"vote_average"`
			Poster                         string  `json:"poster_path"`
			Backdrop                       string  `json:"backdrop_path"`
			GenreIDs                       []int   `json:"genre_ids"`
		} `json:"results"`
	}
	if json.NewDecoder(resp.Body).Decode(&res) != nil || len(res.Results) == 0 {
		return
	}
	m := res.Results[0]
	var genres []string
	for _, g := range m.GenreIDs {
		if n, ok := tmdbGenres[g]; ok {
			genres = append(genres, n)
		}
	}
	y := year
	if d := m.ReleaseDate + m.FirstAir; len(d) >= 4 && y == "" {
		y = d[:4]
	}
	name := m.Title + m.Name
	if name == "" {
		name = title
	}
	rating := ""
	if m.Vote > 0 {
		rating = strconv.FormatFloat(m.Vote, 'f', 1, 64)
	}
	_, _ = l.app.DB.Exec2(`UPDATE items SET title=?, year=?, overview=?, genres=?, rating=? WHERE id=?`, name, y, m.Overview, strings.Join(genres, ", "), rating, id)
	var curPoster, curBackdrop string
	_ = l.app.DB.QueryRow(`SELECT poster, backdrop FROM items WHERE id=?`, id).Scan(&curPoster, &curBackdrop)
	generated := func(p string) bool { return p == "" || strings.HasPrefix(p, l.imgDir()) }
	if m.Poster != "" && generated(curPoster) {
		if p := l.download("https://image.tmdb.org/t/p/w500"+m.Poster, fmt.Sprintf("%d-poster.jpg", id)); p != "" {
			l.setArt(id, p, "")
		}
	}
	if m.Backdrop != "" && generated(curBackdrop) {
		if p := l.download("https://image.tmdb.org/t/p/w1280"+m.Backdrop, fmt.Sprintf("%d-backdrop.jpg", id)); p != "" {
			l.setArt(id, "", p)
		}
	}
}

func (l *Library) download(u, name string) string {
	resp, err := tmdbClient.Get(u)
	if err != nil || resp.StatusCode != 200 {
		return ""
	}
	defer resp.Body.Close()
	out := filepath.Join(l.imgDir(), name)
	f, err := os.Create(out)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return ""
	}
	return out
}
