// Screenshot Stitch in headless Chromium (on the build box: NODE_PATH has puppeteer-core).
//   node test/e2e.js https://<pc>:8105 /tmp/shots [screenshot.png ...]
// 1. made-up screenshots of a made-up chat (clock changing, one saved smaller, all JPEG): the result
//    must be the chat with one set of bars; 2. any real screenshots given: stitched and saved for a
//    look; 3. the page at phone and desktop size.
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const path = require('path');
const [BASE, OUT = '/tmp/shots', ...REAL] = process.argv.slice(2);
if (!BASE) { console.error('usage: node test/e2e.js https://<pc>:8105 [outdir] [screenshots…]'); process.exit(2); }
fs.mkdirSync(OUT, { recursive: true });
const problems = [];
const check = (ok, what) => { console.log(`${ok ? '✓' : '✗'} ${what}`); if (!ok) problems.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function planReady(page, n) {
  await page.waitForFunction((n) => { const p = window.__stitch.plan; return p && p.segments.length === n; }, { timeout: 60000 }, n);
  return page.evaluate(() => { const p = window.__stitch.plan; return { height: p.height, W0: p.W0, joins: p.joins }; });
}

async function saveOutput(page, file) {
  const data = await page.evaluate(async () => {
    const [f] = await window.__stitch.files();
    return new Promise((res) => { const r = new FileReader(); r.onload = () => res(r.result.split(',')[1]); r.readAsDataURL(f); });
  });
  fs.writeFileSync(file, Buffer.from(data, 'base64'));
}

(async () => {
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH,
    args: ['--no-sandbox', '--disable-dev-shm-usage', '--ignore-certificate-errors'] });
  const page = await browser.newPage();
  page.on('pageerror', (e) => problems.push(`page error: ${e.message}`));
  page.on('console', (m) => { if (m.type() === 'error') problems.push(`console: ${m.text()}`); });
  await page.setViewport({ width: 1280, height: 900 });
  await page.goto(`${BASE}/`, { waitUntil: 'load' });

  // ---- 1. made-up chat
  const truth = await page.evaluate(async () => {
    const W = 780, PAGE = 3400, HDR = 150, FTR = 120, VIEW = 1100, tops = [0, 760, 1530];
    const words = 'the quick brown fox jumps over a lazy dog while seven bright gadgets stitch long screenshots into one tidy picture every morning'.split(' ');
    let seed = 3; const rnd = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32);
    const pg = document.createElement('canvas'); pg.width = W; pg.height = PAGE;
    const c = pg.getContext('2d');
    c.fillStyle = '#16171b'; c.fillRect(0, 0, W, PAGE);
    c.font = '28px sans-serif'; c.textBaseline = 'top';
    for (let y = 30; y < PAGE - 60;) {
      const bubble = rnd() < 0.3;
      const lines = 1 + Math.floor(rnd() * 4);
      if (bubble) { c.fillStyle = '#2b2d33'; c.fillRect(260, y - 10, W - 290, lines * 40 + 16); }
      c.fillStyle = '#eceef2';
      for (let l = 0; l < lines; l++, y += 40) {
        let s = ''; while (s.length < (bubble ? 26 : 40)) s += words[Math.floor(rnd() * words.length)] + ' ';
        c.fillText(s.trim(), bubble ? 280 : 30, y);
      }
      y += 34;
    }
    const shot = (top, n, scale) => {
      const s = document.createElement('canvas'); s.width = W; s.height = HDR + VIEW + FTR;
      const x = s.getContext('2d');
      x.fillStyle = '#0d0e11'; x.fillRect(0, 0, W, HDR);
      x.fillStyle = '#fff'; x.font = '600 26px sans-serif'; x.textBaseline = 'top';
      x.fillText(`14:4${n}`, 40, 18); x.fillText('100%', W - 110, 18);
      x.font = '600 30px sans-serif'; x.fillText('Chat about gadgets', 220, 90);
      x.drawImage(pg, 0, top, W, VIEW, 0, HDR, W, VIEW);
      x.fillStyle = '#0d0e11'; x.fillRect(0, HDR + VIEW, W, FTR);
      x.fillStyle = '#2b2d33'; x.fillRect(30, HDR + VIEW + 25, W - 60, 70);
      x.fillStyle = '#9aa0aa'; x.font = '26px sans-serif'; x.fillText('Reply…', 60, HDR + VIEW + 45);
      if (scale === 1) return s;
      const t = document.createElement('canvas'); t.width = Math.round(W * scale); t.height = Math.round(s.height * scale);
      t.getContext('2d').drawImage(s, 0, 0, t.width, t.height);
      return t;
    };
    const files = [];
    for (const [i, top] of tops.entries()) {
      const s = shot(top, i, i === 2 ? 0.95 : 1);
      const b = await new Promise((r) => s.toBlob(r, 'image/jpeg', 0.85));
      files.push(new File([b], `chat-${i + 1}.jpg`, { type: 'image/jpeg' }));
    }
    await window.__stitch.addFiles(files);
    window.__truth = { pg, W, HDR, FTR, VIEW, tops, first: shot(0, 0, 1), last: shot(tops[2], 2, 1) };
    return { expected: HDR + tops[2] + VIEW + FTR, overlaps: [VIEW - tops[1], VIEW - (tops[2] - tops[1])] };
  });
  let p = await planReady(page, 3);
  console.log('made-up:', JSON.stringify({ height: p.height, joins: p.joins.map((j) => ({ ok: j.ok, hdr: j.hdr, ftr: j.ftr, overlap: j.overlap, score: j.score && +j.score.toFixed(2) })) }));
  check(p.joins.every((j) => j.ok), 'both joins found');
  check(Math.abs(p.height - truth.expected) <= 3, `height ${p.height} ≈ ${truth.expected} (one set of bars, nothing doubled or lost)`);
  const diff = await page.evaluate(async () => {
    const [f] = await window.__stitch.files();
    const img = new Image(); img.src = URL.createObjectURL(f); await img.decode();
    const T = window.__truth, H = img.naturalHeight;
    const want = document.createElement('canvas'); want.width = T.W; want.height = H;
    const w = want.getContext('2d');
    w.drawImage(T.first, 0, 0, T.W, T.HDR, 0, 0, T.W, T.HDR);
    w.drawImage(T.pg, 0, 0, T.W, H - T.HDR - T.FTR, 0, T.HDR, T.W, H - T.HDR - T.FTR);
    w.drawImage(T.last, 0, T.HDR + T.VIEW, T.W, T.FTR, 0, H - T.FTR, T.W, T.FTR);
    const got = document.createElement('canvas'); got.width = T.W; got.height = H;
    got.getContext('2d').drawImage(img, 0, 0);
    const a = got.getContext('2d').getImageData(0, 0, T.W, H).data, b = w.getImageData(0, 0, T.W, H).data;
    let worst = 0, sum = 0, rows = 0;
    for (let y = T.HDR; y < H - T.FTR; y++) { // the chat itself, row by row
      let d = 0;
      for (let x = 0; x < T.W; x += 2) { const i = (y * T.W + x) * 4; d += Math.abs(a[i] - b[i]); }
      d /= T.W / 2; sum += d; rows++; worst = Math.max(worst, d);
    }
    return { mean: sum / rows, worst };
  });
  console.log('made-up vs the real chat:', JSON.stringify(diff));
  check(diff.mean < 4 && diff.worst < 40, 'the stitched chat matches the original (JPEG noise only)');
  // bars left off
  await page.click('#keepBottom');
  const q = await planReady(page, 3);
  check(q.height === p.height - p.joins[1].ftr, 'leaving off the bottom bar takes off just that');
  await page.click('#keepBottom');
  await page.click('#showJoins');
  await sleep(300);
  await page.screenshot({ path: `${OUT}/s1-desktop-madeup.png` });

  // ---- 2. real screenshots
  if (REAL.length) {
    const real = await browser.newPage();
    real.on('pageerror', (e) => problems.push(`page error (real): ${e.message}`));
    await real.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 3 });
    await real.goto(`${BASE}/`, { waitUntil: 'load' });
    await (await real.$('#pick')).uploadFile(...REAL);
    const r = await planReady(real, REAL.length);
    console.log('real:', JSON.stringify({ height: r.height, W0: r.W0, joins: r.joins.map((j) => ({ ok: j.ok, hdr: j.hdr, ftr: j.ftr, overlap: j.overlap, score: j.score && +j.score.toFixed(2) })) }));
    check(r.joins.every((j) => j.ok), 'real screenshots: every join found');
    await saveOutput(real, path.join(OUT, 'real-stitched.png'));
    await real.screenshot({ path: `${OUT}/s2-phone-real.png`, fullPage: true });
    await real.close();
  }

  // ---- 3. phone layout, empty and with the made-up shots
  const phone = await browser.newPage();
  await phone.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await phone.goto(`${BASE}/`, { waitUntil: 'load' });
  await phone.screenshot({ path: `${OUT}/s3-phone-empty.png` });
  const overflow = await phone.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1);
  check(!overflow, 'no sideways scrolling on a phone');

  await browser.close();
  console.log(problems.length ? `\n${problems.length} problem(s):\n${problems.join('\n')}` : '\nall good');
  process.exit(problems.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
