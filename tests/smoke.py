"""End-to-end smoke test of NEXPLAY Server: setup, libraries, scan, app API (login, home, details, play, music, TV code)."""
import http.cookiejar, json, sys, time, urllib.request
sys.stdout.reconfigure(encoding="utf-8")
B = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8096"
T = "C:/Users/user/nexplay-test"
jar = http.cookiejar.CookieJar(); web = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

def req(url, body=None, token=None, method=None, opener=None):
    q = urllib.request.Request(B + url, data=json.dumps(body).encode() if body is not None else None, method=method or ("POST" if body is not None else "GET"),
                               headers={"Content-Type": "application/json"})
    if token: q.add_header("Authorization", "Bearer " + token)
    try:
        r = (opener.open(q, timeout=60) if opener else urllib.request.urlopen(q, timeout=60))
        return r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")

def check(name, cond, extra=""):
    print(("PASS " if cond else "FAIL ") + name, extra); return cond

c, r = req("/web/api/setup", {"server_name": "Gimi Home", "name": "Gimi", "username": "admin", "password": "secret123"}, opener=web)
check("setup admin", c == 200, r.get("message", ""))
check("setup twice refused", req("/web/api/setup", {"username": "x", "password": "secret123"}, opener=web)[0] == 409)
for kind, path in [("movies", T + "/Movies"), ("series", T + "/Series"), ("music", T + "/Music")]:
    c, r = req("/web/api/libraries", {"kind": kind, "path": path}, opener=web); check("add library " + kind, c == 200, r.get("message", ""))
check("bad folder refused", req("/web/api/libraries", {"kind": "movies", "path": "Z:/nope"}, opener=web)[0] == 422)
for _ in range(60):
    time.sleep(2); s = req("/web/api/state", opener=web)[1]["data"]
    if not s["scan"]["running"] and s.get("counts", {}).get("track"): break
print("counts:", s.get("counts"))
check("scan found 2 movies, 1 series, 3 episodes, 1 album, 3 tracks", s.get("counts") == {"movie": 2, "series": 1, "season": 1, "episode": 3, "album": 1, "track": 3}, str(s.get("counts")))
c, r = req("/web/api/users", {"name": "Ana", "username": "ana", "password": "ana12345"}, opener=web); check("add user", c == 200)

# --- app side ---
check("home needs login", req("/API_v2/home.php")[0] == 401)
c, r = req("/API_v2/app-login.php", {"username": "ana", "password": "wrong"}); check("wrong password", c == 401)
c, r = req("/API_v2/app-login.php", {"username": "ana", "password": "ana12345", "device_type": "phone", "device_id": "phoneTEST0001", "device_name": "Pixel", "app": "store"})
tok = r["data"]["token"]; check("login", c == 200)
c, h = req("/API_v2/home.php?limit=18", token=tok); d = h["data"]
check("home", c == 200 and len(d["latest_movies"]) == 2 and len(d["latest_series"]) == 1, str([m["title"] + " (" + m["year"] + ")" for m in d["latest_movies"]]))
check("titles cleaned", sorted(m["title"] for m in d["latest_movies"]) == ["Big Buck Bunny", "Test Movie"])
m = [x for x in d["latest_movies"] if x["title"] == "Big Buck Bunny"][0]
check("poster url", m["poster"].startswith(B + "/img/"), m["poster"][:60])
r2 = urllib.request.urlopen(m["poster"], timeout=20); check("poster loads", r2.status == 200 and r2.headers.get("Content-Type", "").startswith("image/"))
c, det = req("/API_v2/details.php?type=movie&id=%d" % m["id"], token=tok); check("details movie", c == 200 and det["data"]["item"]["runtime"] in ("10", "9", "11"), det["data"]["item"].get("runtime"))
c, p = req("/API_v2/play.php?type=movie&id=%d&quality=tv" % m["id"], token=tok); v = p["data"]["video"]
rq = urllib.request.Request(v, headers={"Range": "bytes=100-199"}); rr = urllib.request.urlopen(rq, timeout=20)
check("video range", rr.status == 206 and rr.headers.get("Content-Type") == "video/mp4", rr.headers.get("Content-Range"))
try:
    urllib.request.urlopen(v.replace("&s=", "&s=00"), timeout=10); check("tampered link refused", False)
except urllib.error.HTTPError as e: check("tampered link refused", e.code == 403)
sr = d["latest_series"][0]
c, det = req("/API_v2/details.php?type=series&id=%d" % sr["id"], token=tok); eps = det["data"]["seasons"][0]["episodes"]
check("series episodes", [e["episode_number"] for e in eps] == [1, 2, 3], str([(e["label"], e["title"]) for e in eps]))
c, p = req("/API_v2/play.php?type=episode&id=%d" % eps[0]["id"], token=tok); sub = p["data"]["subtitles"]["sq"]
check("albanian subtitle", sub != "")
if sub: body = urllib.request.urlopen(sub, timeout=10).read().decode(); check("subtitle is VTT", body.startswith("WEBVTT") and "00:00:01.000" in body and "shqip" in body)
c, mu = req("/API_v2/music.php?view=artists", token=tok); check("music artists", c == 200 and mu["data"][0]["name"] == "Demo Artist", str(mu["data"]))
c, al = req("/API_v2/music.php", token=tok); alb = al["data"][0]
c, ad = req("/API_v2/music.php?view=album&id=%d" % alb["id"], token=tok)
check("album tracks", [t["title"] for t in ad["data"]["tracks"]] == ["Song Number 1", "Song Number 2", "Song Number 3"])
c, p = req("/API_v2/play.php?type=track&id=%d" % ad["data"]["tracks"][0]["id"], token=tok); check("play track mp3", p["data"].get("video_type") == "audio/mpeg")
c, sr2 = req("/API_v2/search.php?q=bunny", token=tok); check("search", c == 200 and len(sr2["data"]["movies"]) == 1)
# TV code
c, tv = req("/API_v2/tv-code.php", {"device_id": "tvTEST000001", "device_name": "Living room TV"}); tvd = tv["data"]
check("tv code", c == 200 and len(tvd["code"]) == 6 and tvd["url"].endswith("/tv?code=" + tvd["code"]))
check("tv link", req("/API_v2/tv-link.php", {"code": tvd["code"]}, token=tok)[0] == 200)
c, pl = req("/API_v2/tv-poll.php", {"code": tvd["code"], "secret": tvd["secret"]}); check("tv poll approved", pl["data"]["status"] == "approved")
c, dv = req("/API_v2/devices.php", token=tok); check("devices list", len(dv["data"]["devices"]) == 2, str([x["name"] for x in dv["data"]["devices"]]))
c, dv = req("/web/api/devices", opener=web); check("admin sees devices", len(dv["data"]) == 2)
check("logout", req("/API_v2/logout.php", {}, token=tok)[0] == 200 and req("/API_v2/home.php", token=tok)[0] == 401)
check("admin panel page", urllib.request.urlopen(B + "/web/", timeout=10).status == 200)
check("tv web page", "Sign in on your TV" in urllib.request.urlopen(B + "/tv?code=123456", timeout=10).read().decode())
