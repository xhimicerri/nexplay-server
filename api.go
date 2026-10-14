package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type apiErr struct {
	Code    int
	Err     string
	Message string
	Extra   map[string]any
}

var errServer = &apiErr{500, "SERVER_ERROR", "Something went wrong on the server.", nil}
var errAuth = &apiErr{401, "INVALID_SESSION", "You are signed out on this device. Please sign in again.", nil}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, data any) { writeJSON(w, 200, map[string]any{"ok": true, "data": data}) }

func fail(w http.ResponseWriter, e *apiErr) {
	m := map[string]any{"ok": false, "error": e.Err, "message": e.Message}
	for k, v := range e.Extra {
		m[k] = v
	}
	writeJSON(w, e.Code, m)
}

func readBody(r *http.Request) map[string]any {
	m := map[string]any{}
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = json.Unmarshal(b, &m)
	return m
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func qint(r *http.Request, k string, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get(k)); err == nil {
		return n
	}
	return def
}

// baseURL is how this client reaches the server (used to build media links).
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (a *App) routes(mux *http.ServeMux) {
	api := map[string]func(http.ResponseWriter, *http.Request){
		"server-info": a.hServerInfo, "app-login": a.hLogin, "app-register": a.hRegister, "forgot-password": a.hForgot,
		"profile": a.needUser(a.hProfile), "home": a.needUser(a.hHome),
		"latest-movies": a.needUser(a.hListKind("movie")), "movies": a.needUser(a.hListKind("movie")),
		"latest-series": a.needUser(a.hListKind("series")), "series": a.needUser(a.hListKind("series")),
		"search": a.needUser(a.hSearch), "details": a.needUser(a.hDetails), "play": a.needUser(a.hPlay),
		"music": a.needUser(a.hMusic), "watch-progress": a.needUser(a.hProgress), "sync": a.needUser(a.hSync),
		"devices": a.needUser(a.hDevices), "logout": a.hLogout, "device-session": a.needUser(a.hDeviceSession),
		"tv-code": a.hTvCode, "tv-poll": a.hTvPoll, "tv-link": a.needUser(a.hTvLink), "delete-account": a.needUser(a.hDeleteAccount),
	}
	mux.HandleFunc("/API_v2/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/API_v2/"), ".php")
		if h, found := api[name]; found {
			defer func() {
				if rec := recover(); rec != nil {
					fail(w, errServer)
				}
			}()
			h(w, r)
			return
		}
		fail(w, &apiErr{404, "NOT_FOUND", "Unknown endpoint.", nil})
	})
	mux.HandleFunc("/stream/", a.hStream)
	mux.HandleFunc("/img/", a.hImage)
	mux.HandleFunc("/tv", a.hTvPage)
	a.webRoutes(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/web/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
}

type userHandler func(http.ResponseWriter, *http.Request, *User)

func (a *App) needUser(h userHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.authUser(r)
		if u == nil {
			fail(w, errAuth)
			return
		}
		h(w, r, u)
	}
}

func (a *App) serverName() string { return a.DB.Setting("server_name", "NEXPLAY Server") }

func (a *App) hServerInfo(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"server": "nexplay", "api": 2, "name": a.serverName(), "version": Version,
		"web": baseURL(r), "support": a.DB.Setting("support_email", ""), "register": a.DB.SettingBool("allow_register", false),
		"personal": true, "music": true})
}

func userJSON(u *User) map[string]any {
	return map[string]any{"id": u.ID, "name": u.Name, "username": u.Username, "email": u.Email, "role": u.Role}
}

func (a *App) hLogin(w http.ResponseWriter, r *http.Request) {
	in := readBody(r)
	ident := strings.TrimSpace(str(in["username"]))
	if ident == "" {
		ident = strings.TrimSpace(str(in["email"]))
	}
	u, hash := a.findUserForLogin(ident)
	if u == nil || !checkPassword(hash, str(in["password"])) {
		time.Sleep(400 * time.Millisecond)
		fail(w, &apiErr{401, "INVALID_CREDENTIALS", "Incorrect username or password.", nil})
		return
	}
	d := deviceFromInput(in)
	if d == nil {
		d = &Device{Type: "phone", ID: "legacy" + randHex(6), Name: "App"}
	}
	tok, e := a.deviceLogin(u, d, clientIP(r))
	if e != nil {
		fail(w, e)
		return
	}
	ok(w, map[string]any{"token": tok, "user": userJSON(u)})
}

func (a *App) hRegister(w http.ResponseWriter, r *http.Request) {
	if !a.DB.SettingBool("allow_register", false) {
		fail(w, &apiErr{403, "REGISTRATION_CLOSED", "Accounts are created by the server administrator.", nil})
		return
	}
	in := readBody(r)
	name, email, pw := strings.TrimSpace(str(in["name"])), strings.TrimSpace(str(in["email"])), str(in["password"])
	if name == "" || !strings.Contains(email, "@") || len(pw) < 6 {
		fail(w, &apiErr{422, "INVALID_INPUT", "Enter your name, a valid email and a password of at least 6 characters.", nil})
		return
	}
	u, err := a.createUser(name, email, email, pw, "user")
	if err != nil {
		fail(w, &apiErr{409, "EMAIL_TAKEN", "An account with this email already exists.", nil})
		return
	}
	d := deviceFromInput(in)
	if d == nil {
		d = &Device{Type: "phone", ID: "legacy" + randHex(6), Name: "App"}
	}
	tok, e := a.deviceLogin(u, d, clientIP(r))
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "data": map[string]any{"token": tok, "user": userJSON(u)}})
}

func (a *App) createUser(name, username, email, pw, role string) (*User, error) {
	res, err := a.DB.Exec2(`INSERT INTO users(name,username,email,password_hash,role) VALUES(?,?,?,?,?)`, name, username, email, hashPassword(pw), role)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return a.userByID(id), nil
}

func (a *App) hForgot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "message": "Ask the administrator of " + a.serverName() + " to reset your password."})
}

func (a *App) hProfile(w http.ResponseWriter, r *http.Request, u *User) {
	ok(w, map[string]any{"id": u.ID, "name": u.Name, "username": u.Username, "email": u.Email, "role": u.Role,
		"status": "active", "is_active": true, "joined_at": u.CreatedAt, "last_login": nullStr(u.LastLogin), "expires_at": nil,
		"plan": map[string]any{"id": 0, "name": a.serverName(), "description": "Personal media server"}})
}

func nullStr(s sql.NullString) any {
	if s.Valid {
		return s.String
	}
	return nil
}

// ---- devices / sessions ----

func (a *App) hDevices(w http.ResponseWriter, r *http.Request, u *User) {
	if r.Method == http.MethodPost {
		in := readBody(r)
		target := str(in["remove"])
		if id, err := strconv.ParseInt(target, 10, 64); err == nil {
			_, _ = a.DB.Exec2(`DELETE FROM sessions WHERE id=? AND user_id=?`, id, u.ID)
		}
	}
	rows, err := a.DB.Query(`SELECT id,device_type,device_name,created_at,last_seen,token FROM sessions WHERE user_id=? AND device_type<>'web' ORDER BY last_seen DESC`, u.ID)
	if err != nil {
		fail(w, errServer)
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var t, n, c, l, tok string
		_ = rows.Scan(&id, &t, &n, &c, &l, &tok)
		list = append(list, map[string]any{"id": id, "type": t, "name": n, "signed_in": c, "last_seen": l, "current": u.Session != nil && tok == u.Session.Token})
	}
	ok(w, map[string]any{"devices": list, "website": false, "limits": map[string]int{"phone": 1, "tablet": 1, "tv": 1}})
}

func (a *App) hLogout(w http.ResponseWriter, r *http.Request) {
	if t := bearer(r); t != "" {
		_, _ = a.DB.Exec2(`DELETE FROM sessions WHERE token=?`, t)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "Signed out."})
}

func (a *App) hDeviceSession(w http.ResponseWriter, r *http.Request, u *User) {
	d := deviceFromInput(readBody(r))
	if d == nil {
		fail(w, &apiErr{422, "INVALID_DEVICE", "Missing device information.", nil})
		return
	}
	if u.Session.DeviceID != d.ID && u.Session.DeviceType == d.Type {
		_, _ = a.DB.Exec2(`UPDATE sessions SET device_id=?, app=? WHERE id=?`, d.ID, d.App, u.Session.ID)
	}
	ok(w, map[string]any{"token": u.Session.Token, "upgraded": false})
}

func (a *App) hDeleteAccount(w http.ResponseWriter, r *http.Request, u *User) {
	in := readBody(r)
	var hash string
	_ = a.DB.QueryRow(`SELECT password_hash FROM users WHERE id=?`, u.ID).Scan(&hash)
	if !checkPassword(hash, str(in["password"])) {
		time.Sleep(300 * time.Millisecond)
		fail(w, &apiErr{403, "WRONG_PASSWORD", "The password is not correct.", nil})
		return
	}
	if u.Role == "admin" {
		var n int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&n)
		if n <= 1 {
			fail(w, &apiErr{403, "LAST_ADMIN", "The only administrator cannot be deleted. Create another administrator first.", nil})
			return
		}
	}
	_, _ = a.DB.Exec2(`DELETE FROM users WHERE id=?`, u.ID)
	writeJSON(w, 200, map[string]any{"ok": true, "message": "Your account has been deleted."})
}

func (a *App) hSync(w http.ResponseWriter, r *http.Request, u *User) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) hProgress(w http.ResponseWriter, r *http.Request, u *User) {
	in := readBody(r)
	id, _ := strconv.ParseInt(str(in["id"]), 10, 64)
	pos, _ := strconv.ParseFloat(str(in["position"]), 64)
	dur, _ := strconv.ParseFloat(str(in["duration"]), 64)
	if id > 0 {
		_, _ = a.DB.Exec2(`INSERT INTO progress(user_id,item_id,position,duration) VALUES(?,?,?,?) ON CONFLICT(user_id,item_id) DO UPDATE SET position=excluded.position, duration=excluded.duration, updated_at=datetime('now')`, u.ID, id, pos, dur)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
