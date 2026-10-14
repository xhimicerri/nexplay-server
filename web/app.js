// NEXPLAY Server admin panel (no framework: small, fast, works offline on the local network).
const $app = document.getElementById('app');
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const LOGO = '<div class="logo">NEXPLAY <span>▶</span></div>';
let state = {}, tab = 'overview', poll = null;

async function api(path, opts = {}) {
  const r = await fetch('/web/api/' + path, { method: opts.method || (opts.body ? 'POST' : 'GET'), headers: { 'Content-Type': 'application/json' },
    body: opts.body ? JSON.stringify(opts.body) : undefined, credentials: 'same-origin' });
  const j = await r.json().catch(() => ({}));
  if (!r.ok || !j.ok) throw new Error(j.message || 'Something went wrong.');
  return j.data;
}
function msg(el, text, ok) { el.textContent = text; el.className = 'msg ' + (ok ? 'ok' : 'err'); }
function form(id) { const o = {}; document.querySelectorAll('#' + id + ' [name]').forEach(i => o[i.name] = i.type === 'checkbox' ? (i.checked ? '1' : '0') : i.value); return o; }

async function boot() {
  try { state = await api('state'); } catch (e) { $app.innerHTML = `<div class="center"><div class="card narrow">${LOGO}<p class="msg err" style="display:block">${esc(e.message)}</p></div></div>`; return; }
  if (!state.configured) return setupStep1();
  if (!state.user) return login();
  dashboard();
}

// ---------- first run ----------
function setupStep1() {
  $app.innerHTML = `<div class="center"><div class="card narrow">${LOGO}
    <div class="steps"><i class="on"></i><i></i><i></i></div>
    <h1>Welcome to NEXPLAY Server</h1><p class="muted">Let's set up your server. First, create the administrator account.</p>
    <form id="s1">
      <label for="sn">Server name</label><input id="sn" name="server_name" value="${esc(state.name === 'NEXPLAY Server' ? 'My NEXPLAY' : state.name)}" required>
      <label for="nm">Your name</label><input id="nm" name="name" autocomplete="name">
      <label for="un">Username</label><input id="un" name="username" autocomplete="username" required>
      <label for="pw">Password</label><input id="pw" name="password" type="password" autocomplete="new-password" minlength="6" required>
      <button class="btn wide">Continue</button><div id="m" class="msg"></div></form></div></div>`;
  document.getElementById('s1').onsubmit = async e => {
    e.preventDefault();
    try { await api('setup', { body: form('s1') }); state = await api('state'); setupStep2(); }
    catch (err) { msg(document.getElementById('m'), err.message); }
  };
}

function setupStep2() {
  $app.innerHTML = `<div class="center"><div class="card narrow" style="max-width:620px">${LOGO}
    <div class="steps"><i class="on"></i><i class="on"></i><i></i></div>
    <h1>Add your media</h1><p class="muted">Choose the folders on this computer with your movies, series and music. You can add more later.</p>
    <div id="libs"></div>
    <div class="row" style="margin-top:14px"><button class="btn ghost" data-k="movies">+ Movies</button><button class="btn ghost" data-k="series">+ Series</button><button class="btn ghost" data-k="music">+ Music</button></div>
    <button class="btn wide" id="next">Continue</button></div></div>`;
  document.querySelectorAll('[data-k]').forEach(b => b.onclick = () => pickFolder(b.dataset.k, renderLibs));
  document.getElementById('next').onclick = setupStep3;
  renderLibs();
}
async function renderLibs() {
  const el = document.getElementById('libs'); if (!el) return;
  const libs = await api('libraries').catch(() => []);
  el.innerHTML = libs.length ? libs.map(l => `<div class="row" style="justify-content:space-between;padding:10px 0;border-bottom:1px solid var(--line)"><div><span class="pill">${esc(l.kind)}</span> <b>${esc(l.name)}</b><div class="muted" style="font-size:13px">${esc(l.path)}</div></div></div>`).join('')
    : '<p class="muted" style="margin:18px 0 0">No folders yet.</p>';
}

function setupStep3() {
  const addr = (state.addresses || []).map(a => `<span class="addr">${esc(a)}:${state.port}</span>`).join(' ') || `<span class="addr">this computer's IP:${state.port}</span>`;
  $app.innerHTML = `<div class="center"><div class="card narrow" style="max-width:620px">${LOGO}
    <div class="steps"><i class="on"></i><i class="on"></i><i class="on"></i></div>
    <h1>Your server is ready</h1>
    <p>Open the <b>NEXPLAY</b> app on your phone or TV and connect to:</p><p class="row">${addr}</p>
    <p class="muted">The phone and the TV must be on the same Wi-Fi as this computer. The library is being scanned now – new titles appear in a few minutes.</p>
    <button class="btn wide" id="go">Open dashboard</button></div></div>`;
  document.getElementById('go').onclick = boot;
}

// ---------- login ----------
function login() {
  $app.innerHTML = `<div class="center"><div class="card narrow">${LOGO}<h1>${esc(state.name)}</h1><p class="muted">Sign in with an administrator account.</p>
    <form id="lf"><label for="u">Username</label><input id="u" name="username" autocomplete="username" required>
    <label for="p">Password</label><input id="p" name="password" type="password" autocomplete="current-password" required>
    <button class="btn wide">Sign in</button><div id="m" class="msg"></div></form></div></div>`;
  document.getElementById('lf').onsubmit = async e => {
    e.preventDefault();
    try { await api('login', { body: form('lf') }); boot(); } catch (err) { msg(document.getElementById('m'), err.message); }
  };
}

// ---------- dashboard ----------
const TABS = [['overview', 'Overview'], ['libraries', 'Libraries'], ['users', 'Users'], ['devices', 'Devices'], ['settings', 'Settings']];
function dashboard() {
  $app.innerHTML = `<div class="layout"><aside class="side">${LOGO}<div class="muted" style="font-size:13px;margin-top:4px">${esc(state.name)} · v${esc(state.version)}</div>
    <nav>${TABS.map(([k, t]) => `<button data-t="${k}" class="${tab === k ? 'on' : ''}">${t}</button>`).join('')}<button id="lo">Sign out</button></nav></aside>
    <main class="main" id="main"></main></div>`;
  document.querySelectorAll('[data-t]').forEach(b => b.onclick = () => { tab = b.dataset.t; dashboard(); });
  document.getElementById('lo').onclick = async () => { await api('logout', { body: {} }).catch(() => {}); boot(); };
  clearInterval(poll);
  ({ overview, libraries, users, devices, settings })[tab]();
}
const main = () => document.getElementById('main');

async function overview() {
  state = await api('state');
  const c = state.counts || {}, s = state.scan || {};
  const addr = (state.addresses || []).map(a => `<span class="addr">${esc(a)}:${state.port}</span>`).join(' ');
  main().innerHTML = `<h1 style="margin-top:0">Overview</h1>
    <div class="grid"><div class="stat"><b>${c.movie || 0}</b><span>Movies</span></div><div class="stat"><b>${c.series || 0}</b><span>Series</span></div>
    <div class="stat"><b>${c.episode || 0}</b><span>Episodes</span></div><div class="stat"><b>${c.album || 0}</b><span>Albums</span></div><div class="stat"><b>${c.track || 0}</b><span>Songs</span></div></div>
    <div class="card" style="margin-top:16px"><h2>Connect your apps</h2><p>In the NEXPLAY app choose <b>Connect to your server</b> and type:</p><p class="row">${addr || '<span class="muted">No network found</span>'}</p></div>
    <div class="card" style="margin-top:16px"><div class="row" style="justify-content:space-between"><h2 style="margin:0">Library scan</h2><button class="btn sm" id="sc" ${s.running ? 'disabled' : ''}>Scan now</button></div>
      <p class="muted" id="ss">${s.running ? esc(s.status) : 'Idle' + (s.last_run ? ' · last scan ' + new Date(s.last_run).toLocaleString() : '')}</p>
      ${s.running && s.total ? `<div class="bar"><i style="width:${Math.round(100 * s.progress / s.total)}%"></i></div>` : ''}
      ${s.ffmpeg ? '' : '<p class="msg err" style="display:block">ffmpeg was not found: durations and pictures from videos are disabled. Reinstall NEXPLAY Server.</p>'}</div>
    <h2 style="margin-top:26px">Recently added</h2><div class="posters" id="rec"></div>`;
  document.getElementById('sc').onclick = async () => { await api('scan', { body: {} }); setTimeout(overview, 800); };
  const items = await api('items?kind=movie').catch(() => []);
  const series = await api('items?kind=series').catch(() => []);
  document.getElementById('rec').innerHTML = items.concat(series).slice(0, 18).map(i => `<figure><img loading="lazy" alt="" src="${esc(i.poster)}"><figcaption>${esc(i.title)}${i.year ? ' (' + esc(i.year) + ')' : ''}</figcaption></figure>`).join('') || '<p class="muted">Nothing yet – add a library.</p>';
  if (s.running) poll = setInterval(async () => { if (tab !== 'overview') return clearInterval(poll); const st = await api('state').catch(() => null); if (st && !st.scan.running) { clearInterval(poll); overview(); } else if (st) { const el = document.getElementById('ss'); if (el) el.textContent = st.scan.status + ` (${st.scan.progress}/${st.scan.total})`; } }, 2000);
}

async function libraries() {
  const libs = await api('libraries');
  main().innerHTML = `<h1 style="margin-top:0">Libraries</h1>
    <div class="row"><button class="btn" data-k="movies">+ Movies folder</button><button class="btn ghost" data-k="series">+ Series folder</button><button class="btn ghost" data-k="music">+ Music folder</button></div>
    <div class="card tablebox" style="margin-top:16px"><table><tr><th>Name</th><th>Type</th><th>Folder</th><th>Titles</th><th>Last scan</th><th></th></tr>
    ${libs.map(l => `<tr><td><b>${esc(l.name)}</b></td><td><span class="pill">${esc(l.kind)}</span></td><td class="muted">${esc(l.path)}</td><td>${l.count}</td><td class="muted">${esc(l.last_scan || '—')}</td><td><button class="btn danger sm" data-del="${l.id}">Remove</button></td></tr>`).join('') || '<tr><td colspan="6" class="muted">No libraries yet.</td></tr>'}
    </table></div>
    <div class="card" style="margin-top:16px"><h2>How to name your files</h2><p class="muted">Movies: <span class="addr">Movies/Inception (2010)/Inception (2010).mkv</span><br>Series: <span class="addr">Series/Dark/Season 1/Dark S01E01.mkv</span><br>Music: any folders – artist, album and cover come from the song tags.<br>Subtitles: put <span class="addr">Movie.en.srt</span> or <span class="addr">Movie.sq.srt</span> next to the video. Pictures: <span class="addr">poster.jpg</span> and <span class="addr">fanart.jpg</span> in the movie folder.</p></div>`;
  document.querySelectorAll('[data-k]').forEach(b => b.onclick = () => pickFolder(b.dataset.k, libraries));
  document.querySelectorAll('[data-del]').forEach(b => b.onclick = async () => { if (confirm('Remove this library? Your files are not deleted.')) { await api('libraries?id=' + b.dataset.del, { method: 'DELETE' }); libraries(); } });
}

function pickFolder(kind, done) {
  const m = document.createElement('div'); m.className = 'modal';
  m.innerHTML = `<div class="card"><h2>Choose the ${esc(kind)} folder</h2><p class="muted" id="cur">This computer</p><div class="dirs" id="dl"></div>
    <label for="pp">Or type a path</label><input id="pp" placeholder="D:\\Movies or \\\\NAS\\Media">
    <div class="row" style="margin-top:14px;justify-content:flex-end"><button class="btn ghost" id="cx">Cancel</button><button class="btn" id="ad">Add this folder</button></div><div id="mm" class="msg"></div></div>`;
  document.body.appendChild(m);
  const pp = m.querySelector('#pp');
  async function open(p) {
    try {
      const d = await api('browse?path=' + encodeURIComponent(p || ''));
      pp.value = d.path || ''; m.querySelector('#cur').textContent = d.path || 'This computer';
      m.querySelector('#dl').innerHTML = (d.parent !== '' || d.path ? `<button data-p="${esc(d.parent)}">⬆ Up</button>` : '') + d.dirs.map(x => `<button data-p="${esc(x.path)}">📁 ${esc(x.name)}</button>`).join('');
      m.querySelectorAll('[data-p]').forEach(b => b.onclick = () => open(b.dataset.p));
    } catch (e) { msg(m.querySelector('#mm'), e.message); }
  }
  m.querySelector('#cx').onclick = () => m.remove();
  m.querySelector('#ad').onclick = async () => {
    try { await api('libraries', { body: { kind, path: pp.value } }); m.remove(); done(); } catch (e) { msg(m.querySelector('#mm'), e.message); }
  };
  open('');
}

async function users() {
  const list = await api('users');
  main().innerHTML = `<h1 style="margin-top:0">Users</h1>
    <div class="card"><h2>Add a user</h2><form id="uf" class="grid" style="align-items:end">
      <div><label for="a1">Name</label><input id="a1" name="name"></div><div><label for="a2">Username</label><input id="a2" name="username" required></div>
      <div><label for="a3">Password</label><input id="a3" name="password" type="password" minlength="6" required></div>
      <div><label for="a4">Role</label><select id="a4" name="role"><option value="user">User</option><option value="admin">Administrator</option></select></div>
      <div><button class="btn" style="width:100%">Add user</button></div></form><div id="m" class="msg"></div></div>
    <div class="card tablebox" style="margin-top:16px"><table><tr><th>Name</th><th>Username</th><th>Role</th><th>Devices</th><th>Last sign-in</th><th></th></tr>
    ${list.map(u => `<tr><td><b>${esc(u.name)}</b></td><td>${esc(u.username)}</td><td><span class="pill">${esc(u.role)}</span></td><td>${u.devices}</td><td class="muted">${esc(u.last_login || '—')}</td>
      <td class="row"><button class="btn ghost sm" data-pw="${u.id}">New password</button><button class="btn danger sm" data-del="${u.id}">Delete</button></td></tr>`).join('')}</table></div>`;
  document.getElementById('uf').onsubmit = async e => { e.preventDefault(); try { await api('users', { body: form('uf') }); users(); } catch (err) { msg(document.getElementById('m'), err.message); } };
  document.querySelectorAll('[data-pw]').forEach(b => b.onclick = async () => { const p = prompt('New password (at least 6 characters):'); if (p) { try { await api('users', { body: { id: b.dataset.pw, password: p } }); alert('Password changed. The user is signed out on the apps.'); } catch (e) { alert(e.message); } } });
  document.querySelectorAll('[data-del]').forEach(b => b.onclick = async () => { if (confirm('Delete this user?')) { try { await api('users?id=' + b.dataset.del, { method: 'DELETE' }); users(); } catch (e) { alert(e.message); } } });
}

async function devices() {
  const list = await api('devices');
  main().innerHTML = `<h1 style="margin-top:0">Devices</h1><p class="muted">Phones, tablets and TVs signed in to this server.</p>
    <div class="card tablebox"><table><tr><th>Device</th><th>Type</th><th>User</th><th>IP</th><th>Last active</th><th></th></tr>
    ${list.map(d => `<tr><td><b>${esc(d.name)}</b></td><td><span class="pill">${esc(d.type)}</span></td><td>${esc(d.user)}</td><td class="muted">${esc(d.ip)}</td><td class="muted">${esc(d.last_seen)}</td><td><button class="btn danger sm" data-del="${d.id}">Sign out</button></td></tr>`).join('') || '<tr><td colspan="6" class="muted">No devices yet.</td></tr>'}</table></div>`;
  document.querySelectorAll('[data-del]').forEach(b => b.onclick = async () => { await api('devices?id=' + b.dataset.del, { method: 'DELETE' }); devices(); });
}

async function settings() {
  const s = await api('settings');
  main().innerHTML = `<h1 style="margin-top:0">Settings</h1><form id="sf" class="card" style="max-width:640px">
    <label for="b1">Server name (shown in the apps)</label><input id="b1" name="server_name" value="${esc(s.server_name)}">
    <label for="b2">Support email (optional)</label><input id="b2" name="support_email" value="${esc(s.support_email)}">
    <label for="b5">TMDB API key (optional – posters, descriptions and genres from themoviedb.org)</label><input id="b5" name="tmdb_key" value="${esc(s.tmdb_key)}" placeholder="free key from themoviedb.org/settings/api">
    <label for="b6">Metadata language</label><select id="b6" name="metadata_language">${['en-US', 'sq-AL', 'it-IT', 'de-DE', 'fr-FR', 'es-ES', 'tr-TR'].map(l => `<option ${s.metadata_language === l ? 'selected' : ''}>${l}</option>`).join('')}</select>
    <label for="b7">Scan libraries every (hours)</label><input id="b7" name="scan_hours" type="number" min="1" value="${esc(s.scan_hours || 6)}">
    <label style="display:flex;gap:10px;align-items:center;color:var(--text)"><input type="checkbox" name="allow_register" style="width:auto;height:auto" ${s.allow_register === '1' ? 'checked' : ''}> Let people create their own account from the app</label>
    <label style="display:flex;gap:10px;align-items:center;color:var(--text)"><input type="checkbox" name="device_limit" style="width:auto;height:auto" ${s.device_limit === '1' ? 'checked' : ''}> Limit each account to 1 phone, 1 tablet and 1 TV</label>
    <button class="btn wide">Save</button><div id="m" class="msg"></div></form>`;
  document.getElementById('sf').onsubmit = async e => { e.preventDefault(); try { await api('settings', { body: form('sf') }); state = await api('state'); msg(document.getElementById('m'), 'Saved.', true); } catch (err) { msg(document.getElementById('m'), err.message); } };
}

boot();
