package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID        int64
	Name      string
	Username  string
	Email     string
	Role      string
	Disabled  bool
	CreatedAt string
	LastLogin sql.NullString
	Session   *Session
}

type Session struct {
	ID         int64
	DeviceType string
	DeviceID   string
	DeviceName string
	Token      string
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashPassword(pw string) string {
	h, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h)
}

func checkPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

var bearerRe = regexp.MustCompile(`(?i)^Bearer\s+([a-f0-9]{64})$`)

func bearer(r *http.Request) string {
	if m := bearerRe.FindStringSubmatch(strings.TrimSpace(r.Header.Get("Authorization"))); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

func (a *App) userByID(id int64) *User {
	u := &User{}
	var dis int
	err := a.DB.QueryRow(`SELECT id,name,username,email,role,disabled,created_at,last_login FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.Username, &u.Email, &u.Role, &dis, &u.CreatedAt, &u.LastLogin)
	if err != nil {
		return nil
	}
	u.Disabled = dis == 1
	return u
}

// authUser returns the signed-in user for the request's Bearer token (or the admin panel cookie).
func (a *App) authUser(r *http.Request) *User {
	tok := bearer(r)
	if tok == "" {
		if c, err := r.Cookie("nxs"); err == nil && len(c.Value) == 64 {
			tok = c.Value
		}
	}
	if tok == "" {
		return nil
	}
	s := &Session{Token: tok}
	var uid int64
	var lastSeen string
	err := a.DB.QueryRow(`SELECT id,user_id,device_type,device_id,device_name,last_seen FROM sessions WHERE token=?`, tok).
		Scan(&s.ID, &uid, &s.DeviceType, &s.DeviceID, &s.DeviceName, &lastSeen)
	if err != nil {
		return nil
	}
	u := a.userByID(uid)
	if u == nil || u.Disabled {
		return nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", lastSeen); err != nil || time.Since(t) > 5*time.Minute {
		_, _ = a.DB.Exec2(`UPDATE sessions SET last_seen=datetime('now'), ip=? WHERE id=?`, clientIP(r), s.ID)
	}
	u.Session = s
	return u
}

var deviceTypes = map[string]string{"phone": "phone", "tablet": "tablet", "tv": "TV", "web": "web browser"}
var idRe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

type Device struct{ Type, ID, Name, App string }

func deviceFromInput(in map[string]any) *Device {
	t := strings.ToLower(strings.TrimSpace(str(in["device_type"])))
	if _, ok := deviceTypes[t]; !ok {
		return nil
	}
	id := idRe.ReplaceAllString(str(in["device_id"]), "")
	if len(id) < 8 {
		return nil
	}
	if len(id) > 64 {
		id = id[:64]
	}
	name := strings.Join(strings.Fields(str(in["device_name"])), " ")
	if name == "" {
		name = strings.Title(t)
	}
	if len([]rune(name)) > 100 {
		name = string([]rune(name)[:100])
	}
	app := strings.ToLower(idRe.ReplaceAllString(str(in["app"]), ""))
	if len(app) > 20 {
		app = app[:20]
	}
	return &Device{Type: t, ID: id, Name: name, App: app}
}

// deviceLogin creates a session for the device, enforcing the per-type device limit when it is enabled.
func (a *App) deviceLogin(u *User, d *Device, ip string) (string, *apiErr) {
	limit := a.DB.SettingBool("device_limit", false)
	if limit && u.Role != "admin" && d.Type != "web" {
		var name string
		err := a.DB.QueryRow(`SELECT device_name FROM sessions WHERE user_id=? AND device_type=? AND device_id<>? ORDER BY last_seen DESC LIMIT 1`, u.ID, d.Type, d.ID).Scan(&name)
		if err == nil {
			label := deviceTypes[d.Type]
			return "", &apiErr{409, "DEVICE_IN_USE", fmt.Sprintf("This account is already signed in on another %s (%s). Sign out on that %s, or remove it from Profile → Account → Device Management, then try again.", label, name, label),
				map[string]any{"device_type": d.Type, "device_name": name}}
		}
	}
	tok := randHex(32)
	if _, err := a.DB.Exec2(`DELETE FROM sessions WHERE user_id=? AND device_id=? AND app=?`, u.ID, d.ID, d.App); err != nil {
		return "", errServer
	}
	if _, err := a.DB.Exec2(`INSERT INTO sessions(user_id,device_type,device_id,device_name,app,token,ip) VALUES(?,?,?,?,?,?,?)`, u.ID, d.Type, d.ID, d.Name, d.App, tok, ip); err != nil {
		return "", errServer
	}
	_, _ = a.DB.Exec2(`UPDATE users SET last_login=datetime('now') WHERE id=?`, u.ID)
	return tok, nil
}

func (a *App) findUserForLogin(identity string) (*User, string) {
	var id int64
	var hash string
	err := a.DB.QueryRow(`SELECT id,password_hash FROM users WHERE (username=? OR (email<>'' AND email=?)) AND disabled=0`, identity, identity).Scan(&id, &hash)
	if err != nil {
		return nil, ""
	}
	return a.userByID(id), hash
}

// ---- signed media URLs: players (ExoPlayer, Coil) cannot send the Bearer header ----

func (a *App) streamKey() []byte {
	k := a.DB.Setting("stream_key", "")
	if k == "" {
		k = randHex(32)
		a.DB.SetSetting("stream_key", k)
	}
	return []byte(k)
}

func (a *App) sign(path string, exp int64) string {
	m := hmac.New(sha256.New, a.streamKey())
	m.Write([]byte(path + "|" + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// signedURL returns base+path?e=..&s=.. valid for ttl.
func (a *App) signedURL(base, path string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	return fmt.Sprintf("%s%s?e=%d&s=%s", base, path, exp, a.sign(path, exp))
}

func (a *App) checkSigned(r *http.Request) bool {
	exp, err := strconv.ParseInt(r.URL.Query().Get("e"), 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(a.sign(r.URL.Path, exp)), []byte(r.URL.Query().Get("s")))
}
