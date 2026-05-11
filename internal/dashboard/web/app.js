'use strict';

const state = {
  projects: [],
  activeSlug: null,
  detail: null,
  expandedWorker: null,
};

const $ = (id) => document.getElementById(id);

// ───────────────────────────── boot ─────────────────────────────

async function boot() {
  await loadProjects();
  bindUI();
  openSSE();
}

async function loadProjects() {
  try {
    const res = await fetch('/api/projects');
    state.projects = await res.json() || [];
    renderSidebar();
    if (!state.activeSlug && state.projects.length) {
      activate(state.projects[0].slug);
    } else if (state.activeSlug) {
      await loadDetail(state.activeSlug);
    }
  } catch (e) {
    console.error('loadProjects', e);
  }
}

async function loadDetail(slug) {
  try {
    const res = await fetch('/api/projects/' + encodeURIComponent(slug));
    state.detail = await res.json();
    renderDetail();
  } catch (e) {
    console.error('loadDetail', e);
  }
}

// ───────────────────────────── render ─────────────────────────────

function renderSidebar() {
  const nav = $('projects');
  $('projects-count').textContent = state.projects.length;
  nav.innerHTML = '';
  for (const p of state.projects) {
    const row = el('div', 'project-row');
    if (p.slug === state.activeSlug) row.classList.add('active');
    row.appendChild(el('div', 'name', p.name || p.slug));

    const meta = el('div', 'meta');
    const idleSpan = el('span', null, `${p.idle || 0} idle`);
    const runSpan = el('span', p.running ? 'live' : null, `${p.running || 0} running`);
    const errSpan = el('span', p.error ? 'err' : null, `${p.error || 0} errors`);
    meta.append(idleSpan, document.createTextNode(' · '), runSpan, document.createTextNode(' · '), errSpan);
    row.appendChild(meta);

    row.addEventListener('click', () => activate(p.slug));
    nav.appendChild(row);
  }
}

function renderDetail() {
  if (!state.detail || !state.detail.project) {
    $('project-view').hidden = true;
    $('empty-state').hidden = false;
    return;
  }
  $('empty-state').hidden = true;
  $('project-view').hidden = false;

  const { project, workers, inbox_pending } = state.detail;
  $('project-name').textContent = project.name;
  $('project-description').textContent = project.description || '';

  const runningCount = (workers || []).filter(w => w.status === 'running').length;
  const rollup = $('workers-rollup');
  rollup.innerHTML = '';
  rollup.append(
    document.createTextNode(`${(workers || []).length} `),
    document.createTextNode('· '),
  );
  const rspan = el('span', runningCount ? 'live' : null, `${runningCount} running`);
  rollup.appendChild(rspan);

  const list = $('workers');
  list.innerHTML = '';
  for (const w of (workers || [])) list.appendChild(workerRow(w));

  if (inbox_pending && inbox_pending > 0) {
    $('inbox-section').hidden = false;
    $('inbox-rollup').textContent = `${inbox_pending} pending`;
    renderInbox();
  } else {
    $('inbox-section').hidden = true;
  }
}

function workerRow(w) {
  const wrap = document.createElement('div');
  const row = el('div', 'worker-row');
  row.dataset.worker = w.name;

  const status = el('div', 'status');
  status.appendChild(pill(w));
  row.appendChild(status);

  row.appendChild(el('div', 'worker-name', w.name));
  row.appendChild(el('div', 'repo', w.repo_path));
  row.appendChild(el('div', 'last-run', relativeTime(w.last_run_at)));
  row.appendChild(el('div', 'chev', '→'));

  const expand = el('div', 'worker-expanded');
  const inner = document.createElement('div');
  const body = el('div', 'body');
  const meta = el('div', 'meta-line');
  meta.innerHTML = `<span><b>Model:</b> ${escape(w.model || 'default')}</span>` +
                   `<span><b>Locked:</b> ${w.locked ? 'yes' : 'no'}</span>` +
                   `<span><b>Last run:</b> ${escape(w.last_run_at || '—')}</span>`;
  body.appendChild(meta);
  if (w.last_error) {
    const err = el('div', null, w.last_error);
    err.style.color = 'var(--error)';
    err.style.marginBottom = '8px';
    body.appendChild(err);
  }
  if (w.preview_text) {
    body.appendChild(el('div', 'preview', w.preview_text));
  } else {
    body.appendChild(el('div', null, '(no cached result yet)'));
  }
  const tailBtn = el('button', 'link-btn tail-btn', null);
  tailBtn.innerHTML = '▶ Live tail <span class="chev">→</span>';
  tailBtn.style.marginTop = '12px';
  tailBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    openLiveTail(w.name);
  });
  body.appendChild(tailBtn);
  inner.appendChild(body);
  expand.appendChild(inner);

  row.addEventListener('click', () => {
    const isOpen = expand.classList.toggle('open');
    row.classList.toggle('expanded', isOpen);
    state.expandedWorker = isOpen ? w.name : null;
  });
  if (state.expandedWorker === w.name) {
    expand.classList.add('open');
    row.classList.add('expanded');
  }

  wrap.appendChild(row);
  wrap.appendChild(expand);
  return wrap;
}

function pill(w) {
  let cls = 'idle';
  let label = 'IDLE';
  if (w.status === 'running') { cls = 'running'; label = 'RUNNING'; }
  else if (w.status === 'error') { cls = 'error'; label = 'ERROR'; }
  else if (w.locked) { cls = 'locked'; label = 'LOCKED'; }
  const p = el('span', `pill ${cls}`);
  p.appendChild(el('span', 'dot'));
  p.appendChild(document.createTextNode(label));
  return p;
}

async function renderInbox() {
  if (!state.activeSlug) return;
  try {
    const res = await fetch(`/api/projects/${encodeURIComponent(state.activeSlug)}/inbox`);
    const events = await res.json() || [];
    const list = $('inbox');
    list.innerHTML = '';
    for (const ev of events.slice().reverse()) {
      const row = el('div', 'inbox-row');
      row.appendChild(el('div', `type-${ev.type || 'completed'}`, (ev.type || 'completed').toUpperCase()));
      row.appendChild(el('div', 'worker-name', ev.worker));
      row.appendChild(el('div', null, `${(ev.duration_ms || 0)}ms`));
      row.appendChild(el('div', 'cost', `$${(ev.cost_usd || 0).toFixed(4)}`));
      row.appendChild(el('div', 'time', relativeTime(ev.timestamp)));
      list.appendChild(row);
    }
  } catch (e) { console.error('renderInbox', e); }
}

// ───────────────────────────── live updates ─────────────────────────────

function openSSE() {
  const sse = new EventSource('/events');
  sse.onopen = () => updateSSE('ok', 'connected');
  sse.onerror = () => updateSSE('bad', 'reconnecting…');
  sse.onmessage = (e) => {
    let ev;
    try { ev = JSON.parse(e.data); } catch { return; }
    handleEvent(ev);
  };
}

function updateSSE(cls, label) {
  const dot = $('sse-dot');
  dot.className = 'status-dot ' + (cls === 'ok' ? 'ok' : cls === 'bad' ? 'bad' : '');
  $('sse-label').textContent = label;
}

function handleEvent(ev) {
  if (!ev || !ev.kind) return;
  if (ev.kind === 'project_added' || ev.kind === 'project_removed') {
    loadProjects();
    return;
  }
  if (ev.project !== state.activeSlug) {
    // Refresh the affected project's sidebar counts.
    loadProjects();
    return;
  }
  if (ev.kind === 'worker_changed') {
    loadDetail(state.activeSlug).then(() => flashWorker(ev.worker));
    loadProjects();
  } else if (ev.kind === 'inbox_appended') {
    loadDetail(state.activeSlug);
    loadProjects();
  } else if (ev.kind === 'contract_changed') {
    if (!$('overlay').hidden && $('panel-label').textContent === 'CONTRACT.MD') {
      openContract();
    }
  }
}

function flashWorker(name) {
  setTimeout(() => {
    const row = document.querySelector(`.worker-row[data-worker="${cssEscape(name)}"]`);
    if (!row) return;
    row.classList.remove('flash');
    void row.offsetWidth;
    row.classList.add('flash');
  }, 50);
}

// ───────────────────────────── slide-over ─────────────────────────────

function bindUI() {
  $('close-panel').addEventListener('click', closePanel);
  $('overlay').querySelector('.backdrop').addEventListener('click', closePanel);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closePanel();
  });
  $('open-contract').addEventListener('click', openContract);
  $('open-inbox').addEventListener('click', openInboxPanel);
}

async function openContract() {
  if (!state.activeSlug) return;
  $('panel-label').textContent = 'CONTRACT.MD';
  $('panel-body').innerHTML = '<div class="md">(loading…)</div>';
  showPanel();
  try {
    const res = await fetch(`/api/projects/${encodeURIComponent(state.activeSlug)}/contract`);
    const data = await res.json();
    $('panel-body').innerHTML = '<div class="md"></div>';
    $('panel-body').querySelector('.md').appendChild(renderMarkdown(data.content || '(empty)'));
  } catch (e) {
    $('panel-body').textContent = 'Failed to load contract.';
  }
}

function openLiveTail(workerName) {
  if (!state.activeSlug) return;
  closeTailStream(); // close any prior stream
  $('panel-label').innerHTML = `LIVE TAIL · ${escape(workerName)} <span class="tail-live">● LIVE</span>`;
  const body = $('panel-body');
  body.innerHTML = '';
  const pane = el('div', 'tail-pane');
  body.appendChild(pane);
  showPanel();

  const url = `/api/projects/${encodeURIComponent(state.activeSlug)}/workers/${encodeURIComponent(workerName)}/log/stream`;
  const sse = new EventSource(url);
  state.tailStream = sse;
  state.tailPane = pane;

  sse.onmessage = (e) => {
    let ev;
    try { ev = JSON.parse(e.data); } catch { return; }
    appendTailEvent(pane, ev);
  };
  sse.onerror = () => {
    const indicator = document.querySelector('.tail-live');
    if (indicator) { indicator.textContent = '○ DISCONNECTED'; indicator.classList.add('disconnected'); }
  };
}

function appendTailEvent(pane, ev) {
  const block = el('div', 'tail-event tail-' + ev.kind);
  if (ev.kind === 'assistant') {
    block.appendChild(el('span', 'tail-prefix', 'assistant'));
    block.appendChild(el('span', 'tail-text', ev.text || ''));
  } else if (ev.kind === 'tool_use') {
    block.appendChild(el('span', 'tail-prefix', 'tool'));
    const t = el('span', 'tail-text');
    t.appendChild(el('span', 'tail-tool', ev.tool || ''));
    if (ev.input) t.appendChild(document.createTextNode(' ' + ev.input));
    block.appendChild(t);
  } else if (ev.kind === 'tool_result') {
    block.appendChild(el('span', 'tail-prefix', 'result'));
    block.appendChild(el('span', 'tail-text tail-muted', ev.output || ''));
  } else if (ev.kind === 'result') {
    block.appendChild(el('span', 'tail-prefix', 'done'));
    block.appendChild(el('span', 'tail-text', ev.text || ''));
  } else if (ev.kind === 'error') {
    block.appendChild(el('span', 'tail-prefix', 'error'));
    block.appendChild(el('span', 'tail-text tail-err', ev.text || ''));
  } else if (ev.kind === 'system') {
    block.appendChild(el('span', 'tail-prefix', 'system'));
    block.appendChild(el('span', 'tail-text tail-muted', ev.text || ''));
  }
  // Auto-scroll only if user is already near the bottom.
  const atBottom = pane.scrollHeight - pane.scrollTop - pane.clientHeight < 80;
  pane.appendChild(block);
  if (atBottom) pane.scrollTop = pane.scrollHeight;
}

function closeTailStream() {
  if (state.tailStream) {
    try { state.tailStream.close(); } catch {}
    state.tailStream = null;
    state.tailPane = null;
  }
}

async function openInboxPanel() {
  if (!state.activeSlug) return;
  $('panel-label').textContent = 'INBOX';
  showPanel();
  try {
    const res = await fetch(`/api/projects/${encodeURIComponent(state.activeSlug)}/inbox`);
    const events = await res.json() || [];
    const body = $('panel-body');
    body.innerHTML = '';
    if (!events.length) {
      body.appendChild(el('div', null, '(empty)'));
      return;
    }
    for (const ev of events.slice().reverse()) {
      const block = el('div', null);
      block.style.marginBottom = '24px';
      block.style.paddingBottom = '16px';
      block.style.borderBottom = '1px solid var(--hairline)';
      const top = el('div', null);
      top.innerHTML = `<span class="type-${ev.type || 'completed'}">${(ev.type || 'completed').toUpperCase()}</span>` +
                      ` &nbsp; <span style="color:var(--fg-muted)">${escape(ev.worker || '')}</span>` +
                      ` &nbsp; <span style="color:var(--fg-dim)">${escape(ev.timestamp || '')}</span>`;
      block.appendChild(top);
      if (ev.error) {
        const er = el('div', null, ev.error);
        er.style.color = 'var(--error)';
        er.style.marginTop = '8px';
        block.appendChild(er);
      }
      if (ev.result) {
        const pre = el('pre', null, ev.result);
        pre.style.marginTop = '8px';
        pre.style.background = 'var(--surface-1)';
        pre.style.border = '1px solid var(--hairline)';
        pre.style.padding = '12px 16px';
        pre.style.whiteSpace = 'pre-wrap';
        pre.style.fontSize = '11px';
        block.appendChild(pre);
      }
      body.appendChild(block);
    }
  } catch (e) {
    $('panel-body').textContent = 'Failed to load inbox.';
  }
}

function showPanel() {
  const o = $('overlay');
  o.hidden = false;
  requestAnimationFrame(() => o.classList.add('open'));
}
function closePanel() {
  const o = $('overlay');
  o.classList.remove('open');
  closeTailStream();
  setTimeout(() => { o.hidden = true; }, 280);
}

// ───────────────────────────── tiny markdown renderer ─────────────────────────────
// Handles headings, paragraphs, **bold**, *italic*, `inline`, ```fenced```, lists, links.
// Pure text/element construction — no innerHTML for user content.

function renderMarkdown(src) {
  const root = document.createDocumentFragment();
  const lines = src.split(/\r?\n/);
  let i = 0;
  while (i < lines.length) {
    const ln = lines[i];

    // Fenced code block
    if (/^```/.test(ln)) {
      const buf = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) { buf.push(lines[i]); i++; }
      i++; // skip closing fence
      const pre = document.createElement('pre');
      const code = document.createElement('code');
      code.textContent = buf.join('\n');
      pre.appendChild(code);
      root.appendChild(pre);
      continue;
    }

    // Heading
    const h = /^(#{1,3})\s+(.*)/.exec(ln);
    if (h) {
      const el = document.createElement('h' + h[1].length);
      appendInline(el, h[2]);
      root.appendChild(el);
      i++;
      continue;
    }

    // List
    if (/^[-*]\s+/.test(ln)) {
      const ul = document.createElement('ul');
      while (i < lines.length && /^[-*]\s+/.test(lines[i])) {
        const li = document.createElement('li');
        appendInline(li, lines[i].replace(/^[-*]\s+/, ''));
        ul.appendChild(li);
        i++;
      }
      root.appendChild(ul);
      continue;
    }

    // Blank line
    if (/^\s*$/.test(ln)) { i++; continue; }

    // Paragraph (collect consecutive non-empty lines)
    const buf = [ln]; i++;
    while (i < lines.length && lines[i].trim() !== '' && !/^(#{1,3} |[-*] |```)/.test(lines[i])) {
      buf.push(lines[i]); i++;
    }
    const p = document.createElement('p');
    appendInline(p, buf.join(' '));
    root.appendChild(p);
  }
  return root;
}

function appendInline(parent, text) {
  // Tokenize on `code`, **bold**, *italic*, [text](url) — order matters.
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*]+\*)|(\[[^\]]+\]\([^)]+\))/g;
  let last = 0;
  let m;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
    const tok = m[0];
    if (tok.startsWith('`')) {
      const c = document.createElement('code');
      c.textContent = tok.slice(1, -1);
      parent.appendChild(c);
    } else if (tok.startsWith('**')) {
      const s = document.createElement('strong');
      s.textContent = tok.slice(2, -2);
      parent.appendChild(s);
    } else if (tok.startsWith('*')) {
      const e = document.createElement('em');
      e.textContent = tok.slice(1, -1);
      parent.appendChild(e);
    } else if (tok.startsWith('[')) {
      const lm = /\[([^\]]+)\]\(([^)]+)\)/.exec(tok);
      const a = document.createElement('a');
      a.textContent = lm[1];
      a.href = lm[2];
      a.target = '_blank';
      a.rel = 'noopener noreferrer';
      parent.appendChild(a);
    }
    last = m.index + tok.length;
  }
  if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
}

// ───────────────────────────── utils ─────────────────────────────

function activate(slug) {
  state.activeSlug = slug;
  state.expandedWorker = null;
  renderSidebar();
  loadDetail(slug);
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined && text !== null) e.textContent = String(text);
  return e;
}

function escape(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

function cssEscape(s) {
  return String(s).replace(/["\\]/g, '\\$&');
}

function relativeTime(iso) {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (isNaN(t)) return '—';
  const diff = Math.round((Date.now() - t) / 1000);
  if (diff < 60) return diff + 's ago';
  if (diff < 3600) return Math.round(diff / 60) + 'm ago';
  if (diff < 86400) return Math.round(diff / 3600) + 'h ago';
  return Math.round(diff / 86400) + 'd ago';
}

boot();
