"""Trinket Watcher: deploys whatever lands on master, and keeps every version to go back to.

Every INTERVAL seconds it fetches the repo. When master has moved it works out which gadgets the
new commits touched, runs `tools/trinket check`, each changed gadget's own test (deploy.test in its
gadget.json, run on the build box), then `tools/trinket deploy <id> --existing-only` with the commit
as the version (images are tagged <image>:<commit>). The registry is redeployed when a manifest,
services.json or the registry itself changed. Stacks that don't exist yet are left for a first
deploy by hand, and the watcher never deploys itself (that would stop it mid-run).

The page lists runs with their output, and per gadget its versions with a switch to go back
(`tools/trinket use`). POSTs need the header X-Gadget-Action: 1, as card actions send it.

Env: REPO_URL, BRANCH (master), INTERVAL (300), KEEP (10 versions per image), SITE_LOCAL_JSON
(registry/site.local.json, which isn't in git), PORTAINER_URL, PORTAINER_API_KEY, BUILDBOX_HOST,
BUILDBOX_PORT (2223), SELF (trinket-watcher), START (commit to start after; default: the current
head, so nothing that's already there is deployed again).
"""
import json, os, re, subprocess, threading, time, traceback
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DATA = os.environ.get('DATA', '/data')
REPO = os.path.join(DATA, 'repo')
STATE = os.path.join(DATA, 'state.json')
REPO_URL = os.environ.get('REPO_URL', 'https://github.com/kuraara/shiny-trinkets.git')
BRANCH = os.environ.get('BRANCH', 'master')
INTERVAL = int(os.environ.get('INTERVAL', '300'))
KEEP = int(os.environ.get('KEEP', '10'))
SELF = os.environ.get('SELF', 'trinket-watcher')
WEB = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'web')

lock = threading.Lock()          # one job at a time (a run, a switch)
wake = threading.Event()
state = {'last': None, 'paused': False, 'runs': [], 'history': {}, 'checked': None, 'error': None, 'busy': None}


def now():
    return datetime.now(timezone.utc).isoformat(timespec='seconds')


def save():
    tmp = STATE + '.tmp'
    with open(tmp, 'w') as f:
        json.dump(state, f)
    os.replace(tmp, STATE)


def sh(*cmd, cwd=REPO, env=None, input=None, timeout=3600):
    r = subprocess.run(cmd, cwd=cwd, env={**os.environ, **(env or {})}, input=input, capture_output=True,
                       text=True, timeout=timeout)
    return r.returncode, (r.stdout + r.stderr).strip()


def git(*a):
    code, out = sh('git', *a)
    if code:
        raise RuntimeError(f'git {" ".join(a)}: {out[-500:]}')
    return out


def trinket(*a, env=None):
    return sh('python3', 'tools/trinket', *a, env=env)


def prepare_repo():
    if not os.path.isdir(os.path.join(REPO, '.git')):
        code, out = sh('git', 'clone', '-q', '--branch', BRANCH, REPO_URL, REPO, cwd=DATA)
        if code:
            raise RuntimeError(out)
    if os.environ.get('SITE_LOCAL_JSON'):
        with open(os.path.join(REPO, 'registry', 'site.local.json'), 'w') as f:
            f.write(os.environ['SITE_LOCAL_JSON'])


def gadget(gid):
    p = os.path.join(REPO, 'gadgets', gid, 'gadget.json')
    return json.load(open(p)) if os.path.isfile(p) else None


def affected(files):
    """Gadget ids whose folders changed, and whether the registry needs redeploying."""
    ids, registry = set(), False
    for f in files:
        parts = f.split('/')
        if parts[0] == 'gadgets' and len(parts) > 2:
            ids.add(parts[1])
            if parts[-1] == 'gadget.json' or (len(parts) > 3 and parts[2] == 'web' and parts[-1] == 'icon.svg'):
                registry = True
        elif parts[0] == 'registry' or f == 'tools/trinket':
            registry = True
    return sorted(ids), registry


def remember(gid, rev, commit, subject, result):
    h = state['history'].setdefault(gid, [])
    h.insert(0, {'rev': rev, 'commit': commit, 'subject': subject, 'at': now(), 'result': result})
    del h[50:]


def run(old, new):
    rev = new[:7]
    subject = git('log', '-1', '--format=%s', new)
    commits = git('log', '--format=%h %s', f'{old}..{new}').splitlines() if old else [f'{rev} {subject}']
    files = git('diff', '--name-only', old, new).splitlines() if old else []
    ids, registry = affected(files)
    rec = {'at': now(), 'from': old[:7] if old else None, 'to': rev, 'commits': commits, 'gadgets': {}, 'log': [], 'result': 'ok'}
    state['runs'].insert(0, rec)
    del state['runs'][30:]
    log = lambda line: (rec['log'].append(line), save())
    git('checkout', '-q', '-f', new)
    prepare_repo()
    code, out = trinket('check')
    log('$ trinket check\n' + out)
    if code:
        rec['result'] = 'check failed: nothing deployed'
        return
    for gid in ids:
        m = gadget(gid)
        if not m:
            rec['gadgets'][gid] = 'removed or not a gadget: skipped'
            continue
        if not m.get('deploy', {}).get('stacks'):
            rec['gadgets'][gid] = 'nothing to deploy'
            continue
        if gid == SELF:
            rec['gadgets'][gid] = 'changed: deploy it by hand (it would stop itself)'
            continue
        test = m.get('deploy', {}).get('test')
        if test:
            code, out = sh('sh', '-c', f'tar -C gadgets/{gid} -cf - . | ssh -F /root/.ssh/config buildbox '
                           f'"rm -rf /work/watcher/{gid} && mkdir -p /work/watcher/{gid} && cd /work/watcher/{gid} && tar -xf - && {test}"')
            log(f'$ test {gid}: {test}\n{out}')
            if code:
                rec['gadgets'][gid] = 'test failed: not deployed'
                remember(gid, rev, new, subject, 'test failed')
                rec['result'] = 'some failed'
                continue
        code, out = trinket('deploy', gid, '--existing-only', env={'TRINKET_REV': rev})
        log(f'$ trinket deploy {gid}\n{out}')
        if code:
            rec['gadgets'][gid] = 'deploy failed'
            rec['result'] = 'some failed'
            remember(gid, rev, new, subject, 'deploy failed')
            continue
        skipped = "doesn't exist yet" in out
        rec['gadgets'][gid] = 'new: deploy it by hand first' if skipped else f'deployed {rev}'
        if not skipped:
            remember(gid, rev, new, subject, 'deployed')
            code, out = trinket('prune', gid, '--keep', str(KEEP))
            if out:
                log(f'$ trinket prune {gid}\n{out}')
    if registry:
        code, out = trinket('deploy', 'registry', env={'TRINKET_REV': rev})
        log(f'$ trinket deploy registry\n{out}')
        rec['gadgets']['registry'] = 'deploy failed' if code else f'deployed {rev}'
        if code:
            rec['result'] = 'some failed'
        else:
            remember('registry', rev, new, subject, 'deployed')
            trinket('prune', 'registry', '--keep', str(KEEP))
    if not ids and not registry:
        rec['result'] = 'nothing to deploy'


def check_once():
    with lock:
        state['busy'] = 'checking'
        try:
            prepare_repo()
            git('fetch', '-q', 'origin', BRANCH)
            head = git('rev-parse', f'origin/{BRANCH}')
            state['checked'], state['error'], state['head'] = now(), None, head[:7]
            if state['last'] is None:
                start = os.environ.get('START')
                state['last'] = git('rev-parse', start) if start else head
            if head != state['last'] and not state['paused']:
                state['busy'] = f'deploying {head[:7]}'
                save()
                try:
                    run(state['last'], head)
                except Exception as e:
                    if state['runs'] and state['runs'][0]['to'] == head[:7]:
                        state['runs'][0]['result'] = f'error: {e}'
                    raise
                finally:
                    state['last'] = head      # a broken commit isn't retried; the next push is
        except Exception as e:
            state['error'] = f'{e}'
            traceback.print_exc()
        finally:
            state['busy'] = None
            save()


def loop():
    while True:
        check_once()
        wake.wait(INTERVAL)
        wake.clear()


def switch(gid, rev):
    with lock:
        state['busy'] = f'switching {gid} to {rev}'
        save()
        try:
            git('checkout', '-q', '-f', state['last'])
            prepare_repo()
            code, out = trinket('use', gid, rev)
            subject = next((h['subject'] for h in state['history'].get(gid, []) if h['rev'] == rev), '')
            remember(gid, rev, None, subject, 'switched to' if not code else 'switch failed')
            return code == 0, out
        finally:
            state['busy'] = None
            save()


def versions(gid):
    git('checkout', '-q', '-f', state['last'])
    prepare_repo()
    code, out = trinket('versions', gid, '--json')
    if code:
        return {'error': out[-400:]}
    stacks = json.loads(out)
    subjects = {h['rev']: h for h in state['history'].get(gid, []) if h.get('commit')}
    for s in stacks:
        for v in s['versions']:
            h = subjects.get(v['rev'])
            if v['rev'] == 'rev0':
                v['subject'] = 'as it was when the watcher started'
            elif h:
                v['subject'], v['at'] = h['subject'], h['at']
            else:
                code, subj = sh('git', 'log', '-1', '--format=%s', v['rev'])
                v['subject'] = subj if not code else ''
    return {'stacks': stacks}


def managed():
    out = []
    for gid in sorted(os.listdir(os.path.join(REPO, 'gadgets'))):
        m = gadget(gid)
        if m and any(s.get('build') for s in m.get('deploy', {}).get('stacks', [])):
            out.append({'id': gid, 'name': m.get('name', gid), 'self': gid == SELF})
    return [{'id': 'registry', 'name': 'Gadget registry (home page)', 'self': False}] + out


class Handler(BaseHTTPRequestHandler):
    def reply(self, code, body, ctype='application/json'):
        b = body if isinstance(body, bytes) else json.dumps(body).encode()
        self.send_response(code)
        self.send_header('Content-Type', ctype)
        self.send_header('Cache-Control', 'no-store')
        self.send_header('Content-Length', str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_HEAD(self):
        self.reply(200, b'', 'text/plain')

    def do_GET(self):
        path, _, q = self.path.partition('?')
        if path in ('/', '/index.html'):
            return self.reply(200, open(os.path.join(WEB, 'index.html'), 'rb').read(), 'text/html; charset=utf-8')
        if path == '/icon.svg':
            return self.reply(200, open(os.path.join(WEB, 'icon.svg'), 'rb').read(), 'image/svg+xml')
        if path == '/healthz':
            return self.reply(200, {'ok': True})
        if path == '/api/state':
            pub = os.path.join(DATA, 'ssh', 'id_ed25519.pub')
            return self.reply(200, {**state, 'gadgets': managed() if os.path.isdir(REPO) else [],
                                    'interval': INTERVAL, 'branch': BRANCH,
                                    'pubkey': open(pub).read().strip() if os.path.isfile(pub) else None})
        if path == '/api/versions':
            gid = dict(p.split('=', 1) for p in q.split('&') if '=' in p).get('id', '')
            if not re.fullmatch(r'[a-z][a-z0-9-]{1,40}', gid):
                return self.reply(400, {'error': 'bad id'})
            if not lock.acquire(timeout=1):
                return self.reply(409, {'error': 'busy: try again in a moment'})
            try:
                return self.reply(200, versions(gid))
            finally:
                lock.release()
        self.reply(404, {'error': 'not found'})

    def do_POST(self):
        if self.headers.get('X-Gadget-Action') != '1':
            return self.reply(403, {'error': 'missing X-Gadget-Action header'})
        n = int(self.headers.get('Content-Length') or 0)
        body = json.loads(self.rfile.read(n) or b'{}') if n else {}
        path = self.path.split('?')[0]
        if path == '/api/check':
            wake.set()
            return self.reply(200, {'message': 'Checking now' if not state['busy'] else f'Busy: {state["busy"]}'})
        if path == '/api/pause':
            state['paused'] = bool(body.get('paused', not state['paused']))
            save()
            return self.reply(200, {'message': 'Paused' if state['paused'] else 'Running', 'paused': state['paused']})
        if path == '/api/use':
            gid, rev = str(body.get('id', '')), str(body.get('rev', ''))
            if not re.fullmatch(r'[a-z][a-z0-9-]{1,40}', gid) or not re.fullmatch(r'[A-Za-z0-9._-]{1,40}', rev):
                return self.reply(400, {'error': 'bad id or version'})
            if gid == SELF:
                return self.reply(400, {'error': "the watcher can't switch itself"})
            ok, out = switch(gid, rev)
            return self.reply(200 if ok else 500, {'message' if ok else 'error': out[-1500:]})
        self.reply(404, {'error': 'not found'})

    def log_message(self, fmt, *a):
        pass


if __name__ == '__main__':
    os.makedirs(DATA, exist_ok=True)
    if os.path.isfile(STATE):
        state.update(json.load(open(STATE)))
    state['busy'] = None
    threading.Thread(target=loop, daemon=True).start()
    print(f'trinket-watcher on :8080, {REPO_URL} {BRANCH} every {INTERVAL}s', flush=True)
    ThreadingHTTPServer(('', 8080), Handler).serve_forever()
