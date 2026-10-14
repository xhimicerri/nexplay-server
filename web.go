package main

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var webFS embed.FS

// Admin panel: static files from web/ and a small JSON API under /web/api/ (admin only, cookie session).
func (a *App) webRoutes(mux *http.ServeMux) {
	static, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(static))
	mux.Handle("/web/", http.StripPrefix("/web/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})))
	api := map[string]http.HandlerFunc{
		"state": a.wState, "setup": a.wSetup, "login": a.wLogin, "logout": a.wLogout,
		"libraries": a.needAdmin(a.wLibraries), "scan": a.needAdmin(a.wScan), "users": a.needAdmin(a.wUsers),
		"devices": a.needAdmin(a.wDevices), "settings": a.needAdmin(a.wSettings), "browse": a.needAdmin(a.wBrowse),
		"items": a.needAdmin(a.wItems),
	}
	mux.HandleFunc("/web/api/", func(w http.ResponseWriter, r *http.Request) {
		if h, ok := api[strings.TrimPrefix(r.URL.Path, "/web/api/")]; ok {
			h(w, r)
			return
		}
		fail(w, &apiErr{404, "NOT_FOUND", "Unknown endpoint.", nil})
	})
}

func (a *App) needAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.authUser(r)
		if u == nil || u.Role != "admin" {
			fail(w, &apiErr{401, "NOT_ADMIN", "Please sign in as an administrator.", nil})
			return
		}
		h(w, r)
	}
}

func (a *App) setCookie(w http.ResponseWriter, tok string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "nxs", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

func (a *App) wState(w http.ResponseWriter, r *http.Request) {
	u := a.authUser(r)
	st := map[string]any{"configured": a.DB.Configured(), "name": a.serverName(), "version": Version, "port": a.Port,
		"addresses": LocalAddresses(), "os": runtime.GOOS, "scan": a.Lib.State()}
	if u != nil && u.Role == "admin" {
		st["user"] = userJSON(u)
		counts := map[string]int{}
		rows, err := a.DB.Query(`SELECT kind, COUNT(*) FROM items GROUP BY kind`)
		if err == nil {
			for rows.Next() {
				var k string
				var n int
				_ = rows.Scan(&k, &n)
				counts[k] = n
			}
			rows.Close()
		}
		st["counts"] = counts
	}
	ok(w, st)
}

func (a *App) wSetup(w http.ResponseWriter, r *http.Request) {
	if a.DB.Configured() {
		fail(w, &apiErr{409, "ALREADY_SET_UP", "This server is already set up.", nil})
		return
	}
	in := readBody(r)
	name, user, pw := strings.TrimSpace(str(in["name"])), strings.TrimSpace(str(in["username"])), str(in["password"])
	if user == "" || len(pw) < 6 {
		fail(w, &apiErr{422, "INVALID_INPUT", "Choose a username and a password of at least 6 characters.", nil})
		return
	}
	if sn := strings.TrimSpace(str(in["server_name"])); sn != "" {
		a.DB.SetSetting("server_name", sn)
	}
	if name == "" {
		name = user
	}
	u, err := a.createUser(name, user, strings.TrimSpace(str(in["email"])), pw, "admin")
	if err != nil {
		fail(w, errServer)
		return
	}
	tok, _ := a.deviceLogin(u, &Device{Type: "web", ID: "web" + randHex(8), Name: "Admin panel"}, clientIP(r))
	a.setCookie(w, tok, 30*86400)
	ok(w, userJSON(u))
}

func (a *App) wLogin(w http.ResponseWriter, r *http.Request) {
	in := readBody(r)
	u, hash := a.findUserForLogin(strings.TrimSpace(str(in["username"])))
	if u == nil || !checkPassword(hash, str(in["password"])) {
		time.Sleep(400 * time.Millisecond)
		fail(w, &apiErr{401, "INVALID_CREDENTIALS", "Incorrect username or password.", nil})
		return
	}
	if u.Role != "admin" {
		fail(w, &apiErr{403, "NOT_ADMIN", "Only administrators can open the server panel.", nil})
		return
	}
	tok, _ := a.deviceLogin(u, &Device{Type: "web", ID: "web" + randHex(8), Name: "Admin panel"}, clientIP(r))
	a.setCookie(w, tok, 30*86400)
	ok(w, userJSON(u))
}

func (a *App) wLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("nxs"); err == nil {
		_, _ = a.DB.Exec2(`DELETE FROM sessions WHERE token=?`, c.Value)
	}
	a.setCookie(w, "", -1)
	ok(w, true)
}

func (a *App) wLibraries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		in := readBody(r)
		kind, path, name := str(in["kind"]), filepath.Clean(strings.TrimSpace(str(in["path"]))), strings.TrimSpace(str(in["name"]))
		if kind != "movies" && kind != "series" && kind != "music" {
			fail(w, &apiErr{422, "INVALID_KIND", "Choose Movies, Series or Music.", nil})
			return
		}
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			fail(w, &apiErr{422, "INVALID_PATH", "This folder does not exist or cannot be read.", nil})
			return
		}
		if name == "" {
			name = map[string]string{"movies": "Movies", "series": "Series", "music": "Music"}[kind]
		}
		_, _ = a.DB.Exec2(`INSERT INTO libraries(name,kind,path) VALUES(?,?,?)`, name, kind, path)
		a.Lib.Scan()
	case http.MethodDelete:
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		_, _ = a.DB.Exec2(`DELETE FROM libraries WHERE id=?`, id)
	}
	rows, err := a.DB.Query(`SELECT l.id,l.name,l.kind,l.path,COALESCE(l.last_scan,''),(SELECT COUNT(*) FROM items i WHERE i.library_id=l.id AND i.kind IN ('movie','series','album')) FROM libraries l ORDER BY l.id`)
	if err != nil {
		fail(w, errServer)
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var n, k, p, ls string
		var c int
		_ = rows.Scan(&id, &n, &k, &p, &ls, &c)
		list = append(list, map[string]any{"id": id, "name": n, "kind": k, "path": p, "last_scan": ls, "count": c})
	}
	ok(w, list)
}

func (a *App) wScan(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.Lib.Scan()
	}
	ok(w, a.Lib.State())
}

func (a *App) wUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		in := readBody(r)
		if id, _ := strconv.ParseInt(str(in["id"]), 10, 64); id > 0 { // reset password / role
			if pw := str(in["password"]); pw != "" {
				if len(pw) < 6 {
					fail(w, &apiErr{422, "INVALID_INPUT", "The password needs at least 6 characters.", nil})
					return
				}
				_, _ = a.DB.Exec2(`UPDATE users SET password_hash=? WHERE id=?`, hashPassword(pw), id)
				_, _ = a.DB.Exec2(`DELETE FROM sessions WHERE user_id=? AND device_type<>'web'`, id)
			}
			if role := str(in["role"]); role == "admin" || role == "user" {
				_, _ = a.DB.Exec2(`UPDATE users SET role=? WHERE id=?`, role, id)
			}
			if d := str(in["disabled"]); d != "" {
				_, _ = a.DB.Exec2(`UPDATE users SET disabled=? WHERE id=?`, d == "1", id)
			}
		} else {
			name, user, pw := strings.TrimSpace(str(in["name"])), strings.TrimSpace(str(in["username"])), str(in["password"])
			if user == "" || len(pw) < 6 {
				fail(w, &apiErr{422, "INVALID_INPUT", "Choose a username and a password of at least 6 characters.", nil})
				return
			}
			if name == "" {
				name = user
			}
			role := "user"
			if str(in["role"]) == "admin" {
				role = "admin"
			}
			if _, err := a.createUser(name, user, strings.TrimSpace(str(in["email"])), pw, role); err != nil {
				fail(w, &apiErr{409, "USERNAME_TAKEN", "This username is already used.", nil})
				return
			}
		}
	case http.MethodDelete:
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		var role string
		_ = a.DB.QueryRow(`SELECT role FROM users WHERE id=?`, id).Scan(&role)
		var admins int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&admins)
		if role == "admin" && admins <= 1 {
			fail(w, &apiErr{403, "LAST_ADMIN", "The only administrator cannot be deleted.", nil})
			return
		}
		_, _ = a.DB.Exec2(`DELETE FROM users WHERE id=?`, id)
	}
	rows, err := a.DB.Query(`SELECT u.id,u.name,u.username,u.email,u.role,u.disabled,u.created_at,COALESCE(u.last_login,''),(SELECT COUNT(*) FROM sessions s WHERE s.user_id=u.id AND s.device_type<>'web') FROM users u ORDER BY u.id`)
	if err != nil {
		fail(w, errServer)
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var n, un, e, role, c, ll string
		var dis, devs int
		_ = rows.Scan(&id, &n, &un, &e, &role, &dis, &c, &ll, &devs)
		list = append(list, map[string]any{"id": id, "name": n, "username": un, "email": e, "role": role, "disabled": dis == 1, "created_at": c, "last_login": ll, "devices": devs})
	}
	ok(w, list)
}

func (a *App) wDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		_, _ = a.DB.Exec2(`DELETE FROM sessions WHERE id=?`, id)
	}
	rows, err := a.DB.Query(`SELECT s.id,u.name,s.device_type,s.device_name,s.app,s.ip,s.created_at,s.last_seen FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.device_type<>'web' ORDER BY s.last_seen DESC`)
	if err != nil {
		fail(w, errServer)
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var user, t, n, app, ip, c, ls string
		_ = rows.Scan(&id, &user, &t, &n, &app, &ip, &c, &ls)
		list = append(list, map[string]any{"id": id, "user": user, "type": t, "name": n, "app": app, "ip": ip, "signed_in": c, "last_seen": ls})
	}
	ok(w, list)
}

var settingKeys = []string{"server_name", "support_email", "allow_register", "device_limit", "tmdb_key", "metadata_language", "scan_hours"}

func (a *App) wSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		in := readBody(r)
		for _, k := range settingKeys {
			if v, ok := in[k]; ok {
				a.DB.SetSetting(k, strings.TrimSpace(str(v)))
			}
		}
	}
	out := map[string]any{}
	for _, k := range settingKeys {
		out[k] = a.DB.Setting(k, "")
	}
	if out["server_name"] == "" {
		out["server_name"] = a.serverName()
	}
	ok(w, out)
}

// wBrowse lists folders so the admin can pick library paths (drives on Windows when path is empty).
func (a *App) wBrowse(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	dirs := []map[string]string{}
	if p == "" {
		if runtime.GOOS == "windows" {
			for c := 'C'; c <= 'Z'; c++ {
				d := string(c) + `:\`
				if _, err := os.Stat(d); err == nil {
					dirs = append(dirs, map[string]string{"name": d, "path": d})
				}
			}
		} else {
			p = "/"
		}
	}
	if p != "" {
		entries, err := os.ReadDir(p)
		if err != nil {
			fail(w, &apiErr{422, "INVALID_PATH", "This folder cannot be opened.", nil})
			return
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(e.Name(), "$") {
				dirs = append(dirs, map[string]string{"name": e.Name(), "path": filepath.Join(p, e.Name())})
			}
		}
		sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i]["name"]) < strings.ToLower(dirs[j]["name"]) })
	}
	parent := ""
	if p != "" {
		parent = filepath.Dir(p)
		if parent == p {
			parent = ""
		}
	}
	ok(w, map[string]any{"path": p, "parent": parent, "dirs": dirs})
}

// wItems: latest items for the dashboard.
func (a *App) wItems(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "movie"
	}
	ok(w, a.cards(r, a.items(`WHERE kind=? ORDER BY id DESC LIMIT 60`, kind)))
}
