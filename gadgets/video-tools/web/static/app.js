// Video tools — small progressive enhancements. Every form also works without this script.
(() => {
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const human = (n) => {
    const u = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i ? n.toFixed(1) : n) + ' ' + u[i];
  };
  const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;

  // enlarge contact sheets
  document.addEventListener('click', (e) => {
    const b = e.target.closest('[data-lightbox]');
    if (!b) return;
    const d = $('#lightbox');
    $('img', d).src = b.dataset.lightbox;
    d.showModal();
  });

  // copy buttons
  $$('[data-copy]').forEach((pre) => {
    const btn = document.createElement('button');
    btn.type = 'button'; btn.className = 'btn btn-quiet copybtn'; btn.textContent = 'Copy';
    btn.onclick = async () => {
      try { await navigator.clipboard.writeText(pre.textContent.trim()); btn.textContent = 'Copied'; }
      catch { btn.textContent = 'Select the text to copy it'; }
      setTimeout(() => (btn.textContent = 'Copy'), 2000);
    };
    pre.after(btn);
  });

  // uploads with progress (panel: one clip; join: an ordered list of clips)
  $$('form[data-upload]').forEach((form) => {
    const isJoin = form.hasAttribute('data-join');
    const input = $('[data-file]', form);
    const drop = $('[data-drop]', form);
    const existing = $('[data-existing]', form);
    const picked = $('[data-picked]', form);
    const err = $('[data-error]', form);
    const bar = $('[data-progress]', form);
    const barText = $('[data-progress-text]', form);
    const submit = $('button[type=submit]', form);
    const items = []; // join: {file} or {id, name, size}
    let single = null; // panel: the chosen File

    const showError = (msg) => { err.textContent = msg; err.hidden = !msg; };

    const renderOrder = () => {
      const ol = $('[data-order]', form);
      if (!ol) return;
      ol.replaceChildren(...items.map((it, i) => {
        const li = document.createElement('li');
        const row = document.createElement('div'); row.className = 'it';
        const nm = document.createElement('span'); nm.className = 'nm';
        nm.textContent = it.file ? `${it.file.name} (${human(it.file.size)}, to upload)` : `${it.name} (${it.size})`;
        const ctl = document.createElement('span'); ctl.className = 'ctl';
        const mk = (label, aria, fn, disabled) => {
          const b = document.createElement('button'); b.type = 'button'; b.textContent = label;
          b.setAttribute('aria-label', aria); b.disabled = disabled; b.onclick = fn; return b;
        };
        ctl.append(
          mk('Up', `Move ${nm.textContent} up`, () => { [items[i - 1], items[i]] = [items[i], items[i - 1]]; renderOrder(); }, i === 0),
          mk('Down', `Move ${nm.textContent} down`, () => { [items[i + 1], items[i]] = [items[i], items[i + 1]]; renderOrder(); }, i === items.length - 1),
          mk('Remove', `Remove ${nm.textContent}`, () => { items.splice(i, 1); renderOrder(); }, false),
        );
        row.append(nm, ctl); li.append(row); return li;
      }));
    };

    const take = (files) => {
      showError('');
      if (isJoin) { for (const f of files) items.push({ file: f }); renderOrder(); }
      else if (files.length) {
        single = files[0];
        if (existing) existing.value = '';
        picked.textContent = `${single.name} (${human(single.size)})`;
      }
    };

    input.addEventListener('change', () => { take([...input.files]); if (isJoin) input.value = ''; });
    ['dragenter', 'dragover'].forEach((t) => drop.addEventListener(t, (e) => { e.preventDefault(); drop.classList.add('over'); }));
    ['dragleave', 'drop'].forEach((t) => drop.addEventListener(t, () => drop.classList.remove('over')));
    drop.addEventListener('drop', (e) => { e.preventDefault(); take([...e.dataTransfer.files]); });

    if (existing && !isJoin) existing.addEventListener('change', () => {
      if (existing.value) { single = null; input.value = ''; picked.textContent = ''; }
    });
    const add = $('[data-add-existing]', form);
    if (add) add.onclick = () => {
      const o = existing.selectedOptions[0];
      if (!o || !o.value) return;
      items.push({ id: o.value, name: o.dataset.name, size: o.dataset.size });
      existing.value = ''; renderOrder();
    };

    form.addEventListener('submit', (e) => {
      e.preventDefault();
      showError('');
      const fd = new FormData();
      for (const el of form.elements) {
        if (!el.name || el.type === 'file' || el.disabled) continue;
        if ((el.type === 'radio' || el.type === 'checkbox') && !el.checked) continue;
        if (isJoin && el.hasAttribute('data-existing')) continue;
        fd.append(el.name, el.value);
      }
      if (isJoin) {
        if (items.length < 2) return showError('Add at least two clips to join.');
        let n = 0;
        const order = items.map((it) => (it.file ? `new:${n++}` : `id:${it.id}`));
        fd.set('order', order.join(','));
        for (const it of items) if (it.file) fd.append('files', it.file, it.file.name);
      } else {
        if (!single && !(existing && existing.value)) return showError('Choose a clip first.');
        const mode = form.elements.frames && form.elements.frames.value;
        if (mode === 'times' && !form.elements.times.value.trim()) return showError('Enter the times you want, for example 1.0, 2.5, 7.2.');
        if (single) fd.append('file', single, single.name);
      }
      const total = (isJoin ? items : [{ file: single }]).reduce((s, it) => s + (it.file ? it.file.size : 0), 0);
      const xhr = new XMLHttpRequest();
      xhr.open('POST', form.action);
      xhr.setRequestHeader('Accept', 'application/json');
      if (total) {
        bar.hidden = false;
        xhr.upload.onprogress = (ev) => {
          if (!ev.lengthComputable) return;
          bar.value = Math.round((ev.loaded / ev.total) * 100);
          barText.textContent = `Uploading ${bar.value}% (${human(ev.loaded)} of ${human(ev.total)})`;
        };
      }
      xhr.onload = () => {
        let body = {};
        try { body = JSON.parse(xhr.responseText); } catch { /* not JSON */ }
        if (xhr.status < 300 && body.redirect) { barText.textContent = 'Starting…'; location.href = body.redirect; return; }
        submit.disabled = false; bar.hidden = true; barText.textContent = '';
        showError(body.error || `Upload failed (${xhr.status}).`);
      };
      xhr.onerror = () => { submit.disabled = false; bar.hidden = true; barText.textContent = ''; showError('The connection dropped during the upload. Try again.'); };
      submit.disabled = true;
      barText.textContent = total ? 'Uploading…' : 'Starting…';
      xhr.send(fd);
    });
  });

  // panel review: live subtitle preview + speaker suggestions
  const review = $('[data-review]');
  if (review) {
    const lines = $$('[data-line]', review);
    const list = $('#speakers');
    const update = () => {
      $$('[data-subs]', review).forEach((s) => s.replaceChildren());
      const names = new Set();
      for (const fs of lines) {
        const sp = $('[data-speaker]', fs).value.trim();
        const tx = $('[data-text]', fs).value.trim();
        const fr = $('[data-frame]', fs).value;
        if (sp) names.add(sp);
        const box = $(`[data-subs="${fr}"]`, review);
        if (!box || !tx) continue;
        const p = document.createElement('p');
        if (sp) { const b = document.createElement('b'); b.textContent = sp + ': '; p.append(b); }
        p.append(`“${tx.replace(/^["“”]+|["“”]+$/g, '').replace(/'/g, '’')}”`);
        box.append(p);
      }
      list.replaceChildren(...[...names].map((n) => Object.assign(document.createElement('option'), { value: n })));
    };
    review.addEventListener('input', update);
    review.addEventListener('change', update);
    update();
    document.body.addEventListener('htmx:afterSwap', (e) => {
      if (e.detail.target.id === 'result') e.detail.target.scrollIntoView({ behavior: reduced ? 'auto' : 'smooth', block: 'start' });
    });
  }
})();

// review preview: panel numbers and times are hidden unless ticked (remembered)
for (const box of document.querySelectorAll('[data-panel-numbers]')) {
  const strip = box.closest('.strip');
  const apply = () => { for (const c of strip.querySelectorAll('figcaption')) c.hidden = !box.checked; };
  try { box.checked = localStorage.getItem('vt-panel-numbers') === '1'; } catch { /* private mode */ }
  apply();
  box.addEventListener('change', () => { try { localStorage.setItem('vt-panel-numbers', box.checked ? '1' : '0'); } catch { /* ignore */ } apply(); });
}
