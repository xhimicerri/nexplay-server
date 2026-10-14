// Screenshots of the admin panel: node shots.js <base> <outdir> [fresh]
const puppeteer = require('puppeteer-core');
const [base, out, fresh] = [process.argv[2] || 'http://127.0.0.1:8096', process.argv[3] || '.', process.argv[4]];
(async () => {
  const b = await puppeteer.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: 'new', args: ['--no-sandbox'] });
  const p = await b.newPage(); await p.setViewport({ width: 1366, height: 860 });
  const wait = ms => new Promise(r => setTimeout(r, ms));
  await p.goto(base + '/web/', { waitUntil: 'networkidle0' }); await wait(500);
  if (fresh) {
    await p.screenshot({ path: out + '/01-setup.png' });
    await p.type('#nm', 'Gimi'); await p.type('#un', 'admin'); await p.type('#pw', 'secret123');
    await p.click('button.btn.wide'); await wait(1200); await p.screenshot({ path: out + '/02-add-media.png' });
    await p.click('[data-k="movies"]'); await wait(800); await p.screenshot({ path: out + '/03-folder-picker.png' });
    await p.evaluate(() => document.querySelector('.modal').remove());
    await p.click('#next'); await wait(600); await p.screenshot({ path: out + '/04-ready.png' });
  } else {
    await p.type('#u', 'admin'); await p.type('#p', 'secret123'); await p.click('button.btn.wide'); await wait(1500);
  }
  for (const t of ['overview', 'libraries', 'users', 'devices', 'settings']) {
    await p.evaluate(t => { const x = document.querySelector('[data-t="' + t + '"]'); x && x.click(); }, t); await wait(1500);
    await p.screenshot({ path: `${out}/${fresh ? 'f' : ''}tab-${t}.png`, fullPage: true });
  }
  await p.setViewport({ width: 390, height: 844 }); await p.evaluate(() => document.querySelector('[data-t="overview"]').click()); await wait(1200);
  await p.screenshot({ path: out + '/phone-overview.png', fullPage: true });
  await b.close();
})().catch(e => { console.error(e); process.exit(1); });
