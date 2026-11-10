// Plan Runner page: the job list, and the selected job with its plan, commits and log (refreshed).
const $ = (id) => document.getElementById(id);
const el = (t, c, x) => { const e = document.createElement(t); if (c) e.className = c; if (x != null) e.textContent = x; return e; };
let current = null, logTotal = 0;
const when = (iso) => (iso ? new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) : '');
const took = (j) => { if (!j.started) return ''; const s = ((j.finished ? new Date(j.finished) : new Date()) - new Date(j.started)) / 1000; return s < 90 ? `${Math.round(s)} s` : `${Math.round(s / 60)} min`; };

async function getJSON(u) { const r = await fetch(u, { cache: 'no-store' }); if (!r.ok) throw new Error(r.status); return r.json(); }

async function jobs() {
  const { jobs } = await getJSON('api/jobs');
  const ul = $('jobs'); ul.replaceChildren();
  jobs.forEach((j) => {
    const b = el('button'); b.type = 'button'; b.setAttribute('aria-current', String(j.id === current));
    const m = el('span', 'm'); m.append(el('span', `badge ${j.status}`, j.status), el('span', null, when(j.created)));
    if (j.commits && j.commits.length) m.append(el('span', null, `${j.commits.length} commit${j.commits.length > 1 ? 's' : ''}`));
    b.append(el('span', 't', j.title), m);
    b.addEventListener('click', () => { current = j.id; logTotal = 0; $('log').textContent = ''; history.replaceState(null, '', '#' + j.id); refresh(); });
    const li = el('li'); li.append(b); ul.append(li);
  });
  $('nojobs').hidden = jobs.length > 0;
  if (!current && jobs.length) current = jobs[0].id;
}

async function detail() {
  if (!current) { $('detail').hidden = true; return; }
  const { job: j, plan, position } = await getJSON(`api/jobs/${current}`);
  $('detail').hidden = false;
  $('title').textContent = j.title;
  const f = $('facts'); f.replaceChildren();
  const add = (k, v) => { if (v) { f.append(el('dt', null, k), el('dd', null, v)); } };
  add('Status', j.status + (j.step ? ` (${j.step})` : '') + (j.status === 'queued' && position ? `, ${position} ahead` : ''));
  add('Repo', j.repo); add('Branch', j.branch); add('Agent', j.executor);
  add('Started', when(j.started)); add('Took', took(j)); add('Pushed', j.status === 'done' ? (j.pushed ? 'yes' : 'no') : '');
  add('Error', j.error);
  $('plan').textContent = plan;
  $('commits-box').hidden = !(j.commits && j.commits.length);
  $('commits').replaceChildren(...(j.commits || []).map((c) => el('li', null, c)));
  $('cancel').hidden = !(j.status === 'queued' || j.status === 'running');
  const { lines, total } = await getJSON(`api/jobs/${current}/log?from=${logTotal}`);
  if (lines.length) {
    const log = $('log'); const atEnd = log.scrollTop + log.clientHeight >= log.scrollHeight - 20;
    log.textContent += lines.join('\n') + '\n'; logTotal = total;
    if (atEnd) log.scrollTop = log.scrollHeight;
  }
}

async function refresh() {
  try { await jobs(); await detail(); $('status').textContent = ''; }
  catch { $('status').textContent = "Can't reach the runner right now; retrying."; }
}
$('cancel').addEventListener('click', async () => {
  $('cancel').disabled = true;
  try { await fetch(`api/jobs/${current}/cancel`, { method: 'POST' }); } finally { $('cancel').disabled = false; refresh(); }
});
getJSON('api/info').then((i) => { $('info').textContent = `Build plans sent here over MCP are carried out one at a time by ${i.executor === 'codex' ? 'Codex' : i.executor === 'claude' ? 'Claude Code' : 'a command'} in a fresh clone (up to ${i.timeLimitMinutes} min each), then pushed.`; }).catch(() => {});
if (location.hash.length > 1) current = location.hash.slice(1);
refresh(); setInterval(refresh, 4000);
