// End-to-end run of Photo Drop in headless Chromium. Usage: node photo-drop.e2e.js http://host:port outdir
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const BASE = process.argv[2] || 'http://localhost:8080';
const OUT = process.argv[3] || '/tmp/shots';
fs.mkdirSync(OUT, { recursive: true });
const errors = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function watch(page, label) {
  page.on('console', (m) => { if (m.type() === 'error' || m.type() === 'warning') errors.push(`${label} console ${m.type()}: ${m.text()}`); });
  page.on('pageerror', (e) => errors.push(`${label} pageerror: ${e.message}`));
  page.on('requestfailed', (r) => { if (!r.url().includes('/events')) errors.push(`${label} requestfailed: ${r.url()} ${r.failure()?.errorText}`); });
}

(async () => {
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH, args: ['--no-sandbox', '--disable-dev-shm-usage', '--disable-renderer-backgrounding', '--disable-background-timer-throttling', '--disable-backgrounding-occluded-windows'], protocolTimeout: 60000 });

  // Host starts a session.
  const host = await browser.newPage();
  watch(host, 'host');
  await host.setViewport({ width: 1280, height: 900 });
  await host.goto(BASE + '/', { waitUntil: 'networkidle0' });
  await host.screenshot({ path: `${OUT}/1-start.png` });
  await host.type('input[name=title]', "Sam's 40th");
  await host.click('input[name=requireNames]');
  await host.click('input[name=approveParticipants]');
  await Promise.all([host.waitForNavigation(), host.click('#start button[type=submit]')]);
  await host.waitForSelector('#code:not(:empty)');
  const code = await host.$eval('#code', (e) => e.textContent);
  const displayHref = await host.$eval('#openDisplay', (e) => e.href);

  // Display opens on the "TV".
  const tv = await browser.newPage();
  watch(tv, 'display');
  await tv.setViewport({ width: 1600, height: 900 });
  await tv.goto(displayHref, { waitUntil: 'load' });
  await sleep(500);
  await tv.screenshot({ path: `${OUT}/2-display-empty.png` });

  // A guest on a phone.
  const ctx = await browser.createBrowserContext();
  const phone = await ctx.newPage();
  watch(phone, 'guest');
  await phone.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await phone.goto(`${BASE}/j/${code}`, { waitUntil: 'load' });
  await phone.waitForSelector('#vForm:not([hidden])');
  await phone.click('#vForm button');                      // no name -> refused
  await sleep(300);
  await phone.type('input[name=name]', 'Alex Smith');
  await phone.click('#vForm button');
  await phone.waitForSelector('#vWaiting:not([hidden])');
  await phone.screenshot({ path: `${OUT}/3-guest-waiting.png` });

  // Host lets them in.
  await host.bringToFront();
  await host.waitForSelector('#lobby li button.good');
  await host.screenshot({ path: `${OUT}/4-host-lobby.png` });
  await host.click('#lobby li button.good');
  await phone.bringToFront();
  await phone.waitForSelector('#vMain:not([hidden])');

  // Guest sends three photos with a caption on the first.
  const files = fs.readdirSync('/tmp/testpics').map((f) => `/tmp/testpics/${f}`).sort();
  const input = await phone.$('#pick');
  await input.uploadFile(...files.slice(0, 3));
  await phone.type('.pick input[type=text]', 'Cake time!');
  await phone.screenshot({ path: `${OUT}/5-guest-picked.png` });
  await phone.click('#send');
  await phone.waitForFunction(() => document.querySelectorAll('#mine figure').length === 3, { timeout: 20000 });
  await phone.screenshot({ path: `${OUT}/6-guest-sent.png`, fullPage: true });

  // Host approves two, rejects one.
  await host.bringToFront();
  await host.waitForFunction(() => document.querySelectorAll('#pending .pcard').length === 3);
  await sleep(500);
  await host.screenshot({ path: `${OUT}/7-host-pending.png`, fullPage: true });
  for (let i = 0; i < 2; i++) {
    await host.click('#pending .pcard button.good');
    await host.waitForFunction((n) => document.querySelectorAll('#pending .pcard').length === n, {}, 2 - i);
  }
  await host.click('#pending .pcard button.bad');
  await sleep(1600);
  await tv.bringToFront();
  await tv.screenshot({ path: `${OUT}/8-display-two.png` });

  // Host adds more photos directly to fill the desk.
  await host.bringToFront();
  const hp = await host.$('#hostPick');
  await hp.uploadFile(...files.slice(3));
  await host.waitForFunction((n) => document.querySelectorAll('#desk .thumb').length === n, { timeout: 30000 }, 2 + files.length - 3);
  await tv.bringToFront();
  await sleep(300);
  await tv.screenshot({ path: `${OUT}/9a-display-midflight.png` });
  await sleep(1800);
  await tv.screenshot({ path: `${OUT}/9-display-full.png` });
  await host.bringToFront();
  await host.screenshot({ path: `${OUT}/10-host-desk.png`, fullPage: true });
  await phone.bringToFront();
  const guestState = await phone.$$eval('#mine figcaption', (els) => els.map((e) => e.textContent));
  await phone.screenshot({ path: `${OUT}/11-guest-status.png`, fullPage: true });

  // Lightbox.
  await tv.bringToFront();
  await tv.mouse.move(800, 450);
  await tv.evaluate(() => [...document.querySelectorAll('#desk .print')].sort((x, y) => y.style.zIndex - x.style.zIndex)[0].click());
  await sleep(600);
  await tv.screenshot({ path: `${OUT}/12-lightbox.png` });

  // Kick guest; end session.
  await host.bringToFront();
  await host.click('#people li button.bad');
  await phone.waitForSelector('#vOut:not([hidden])');
  const kicked = await phone.$eval('#outTitle', (e) => e.textContent);
  await host.click('#end'); await host.click('#end');
  await tv.bringToFront();
  await tv.waitForSelector('#message:not([hidden])', { timeout: 10000 });
  await tv.screenshot({ path: `${OUT}/13-display-ended.png` });

  console.log(JSON.stringify({ code, guestState, kicked, errors }, null, 1));
  await browser.close();
})().catch((e) => { console.error('FAILED', e); console.log(JSON.stringify({ errors }, null, 1)); process.exit(1); });
