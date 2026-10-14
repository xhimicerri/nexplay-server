package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// TV sign-in with a 6-digit code (same flow as newfilma.com): the TV shows a code + QR,
// a signed-in phone (or the /tv web page) approves it, the TV polls and receives its own session.

const tvCodeTTL = 600

var nonDigit = regexp.MustCompile(`\D`)

func normCode(c string) string {
	c = nonDigit.ReplaceAllString(c, "")
	if len(c) > 6 {
		c = c[:6]
	}
	return c
}

func newCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}

func (a *App) hTvCode(w http.ResponseWriter, r *http.Request) {
	in := readBody(r)
	in["device_type"] = "tv"
	d := deviceFromInput(in)
	if d == nil {
		fail(w, &apiErr{422, "INVALID_DEVICE", "Missing device information.", nil})
		return
	}
	now := time.Now().Unix()
	_, _ = a.DB.Exec2(`DELETE FROM tv_links WHERE expires_at < ? OR (device_id=? AND token IS NULL)`, now-3600, d.ID)
	var code string
	for i := 0; i < 20; i++ {
		code = newCode()
		var n int
		_ = a.DB.QueryRow(`SELECT COUNT(*) FROM tv_links WHERE code=?`, code).Scan(&n)
		if n == 0 {
			break
		}
	}
	secret := randHex(32)
	if _, err := a.DB.Exec2(`INSERT INTO tv_links(code,secret,device_id,device_name,ip,expires_at) VALUES(?,?,?,?,?,?)`, code, secret, d.ID, d.Name, clientIP(r), now+tvCodeTTL); err != nil {
		fail(w, errServer)
		return
	}
	ok(w, map[string]any{"code": code, "secret": secret, "expires_in": tvCodeTTL, "url": baseURL(r) + "/tv?code=" + code})
}

func (a *App) hTvPoll(w http.ResponseWriter, r *http.Request) {
	in := readBody(r)
	code, secret := normCode(str(in["code"])), strings.ToLower(str(in["secret"]))
	var sec string
	var uid *int64
	var tok *string
	var exp int64
	err := a.DB.QueryRow(`SELECT secret,user_id,token,expires_at FROM tv_links WHERE code=?`, code).Scan(&sec, &uid, &tok, &exp)
	if err != nil || sec != secret {
		ok(w, map[string]any{"status": "expired"})
		return
	}
	if tok != nil && *tok != "" && uid != nil {
		_, _ = a.DB.Exec2(`DELETE FROM tv_links WHERE code=?`, code)
		u := a.userByID(*uid)
		if u == nil {
			ok(w, map[string]any{"status": "expired"})
			return
		}
		ok(w, map[string]any{"status": "approved", "token": *tok, "user": userJSON(u)})
		return
	}
	if time.Now().Unix() > exp {
		ok(w, map[string]any{"status": "expired"})
		return
	}
	ok(w, map[string]any{"status": "pending"})
}

func (a *App) hTvLink(w http.ResponseWriter, r *http.Request, u *User) {
	code := normCode(str(readBody(r)["code"]))
	if len(code) != 6 {
		fail(w, &apiErr{422, "INVALID_CODE", "Enter the 6-digit code shown on your TV.", nil})
		return
	}
	now := time.Now().Unix()
	var fails int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM link_fails WHERE user_id=? AND at>?`, u.ID, now-600).Scan(&fails)
	if fails >= 10 {
		fail(w, &apiErr{429, "TOO_MANY_ATTEMPTS", "Too many wrong codes. Please wait 10 minutes and try again.", nil})
		return
	}
	var devID, devName string
	var exp int64
	var tok *string
	var owner *int64
	err := a.DB.QueryRow(`SELECT device_id,device_name,expires_at,token,user_id FROM tv_links WHERE code=?`, code).Scan(&devID, &devName, &exp, &tok, &owner)
	if err != nil || now > exp {
		_, _ = a.DB.Exec2(`INSERT INTO link_fails(user_id,at) VALUES(?,?)`, u.ID, now)
		fail(w, &apiErr{404, "INVALID_CODE", "This code is wrong or has expired. Check the code on your TV and try again.", nil})
		return
	}
	if tok != nil && *tok != "" {
		if owner != nil && *owner == u.ID {
			writeJSON(w, 200, map[string]any{"ok": true, "message": "Your TV is now signed in.", "data": map[string]any{"device_name": devName}})
			return
		}
		fail(w, &apiErr{409, "INVALID_CODE", "This code has already been used.", nil})
		return
	}
	t, e := a.deviceLogin(u, &Device{Type: "tv", ID: devID, Name: devName}, clientIP(r))
	if e != nil {
		fail(w, e)
		return
	}
	_, _ = a.DB.Exec2(`UPDATE tv_links SET user_id=?, token=? WHERE code=?`, u.ID, t, code)
	writeJSON(w, 200, map[string]any{"ok": true, "message": "Your TV is now signed in.", "data": map[string]any{"device_name": devName}})
}

// /tv — web page to approve a TV code with username + password (the QR on the TV opens it).
func (a *App) hTvPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(strings.ReplaceAll(tvPageHTML, "{{NAME}}", htmlEscape(a.serverName()))))
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

const tvPageHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sign in on your TV</title><link rel="stylesheet" href="/web/app.css"></head><body class="center">
<div class="card narrow"><div class="logo">NEXPLAY <span>▶</span></div><h1>Sign in on your TV</h1>
<p class="muted">{{NAME}} · enter the 6-digit code shown on your TV.</p>
<form id="f"><label for="code">TV code</label><input id="code" class="code" inputmode="numeric" maxlength="6" placeholder="123456" required>
<label for="u">Username or email</label><input id="u" autocomplete="username" required>
<label for="p">Password</label><input id="p" type="password" autocomplete="current-password" required>
<button class="btn wide" id="go">Sign in TV</button></form><div id="msg" class="msg"></div></div>
<script>
const c=document.getElementById('code'),m=document.getElementById('msg');c.value=(new URLSearchParams(location.search).get('code')||'').replace(/\D/g,'').slice(0,6);
c.oninput=()=>c.value=c.value.replace(/\D/g,'').slice(0,6);
async function api(p,b,t){const h={'Content-Type':'application/json'};if(t)h.Authorization='Bearer '+t;const r=await fetch('/API_v2/'+p,{method:'POST',headers:h,body:JSON.stringify(b)});const j=await r.json().catch(()=>({}));if(!r.ok||!j.ok)throw new Error(j.message||'Something went wrong.');return j}
document.getElementById('f').onsubmit=async e=>{e.preventDefault();m.className='msg';try{const s=await api('app-login.php',{username:document.getElementById('u').value.trim(),password:document.getElementById('p').value,device_type:'web',device_id:'web'+Math.random().toString(36).slice(2,14),device_name:'Web browser'});
try{const r=await api('tv-link.php',{code:c.value},s.data.token);m.textContent='✔ '+(r.data.device_name||'Your TV')+' is now signed in.';m.className='msg ok'}finally{api('logout.php',{},s.data.token).catch(()=>{})}}catch(err){m.textContent=err.message;m.className='msg err'}}
</script></body></html>`
