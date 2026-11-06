// jot desktop: a window over the Markdown memory store.
// Plain JS, no build step. Talks to Go through window.go.main.{App,Window}.
'use strict';

const $ = (s, el = document) => el.querySelector(s);
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const ICON = {
  all: '<svg class="ico" viewBox="0 0 24 24"><path d="M6 3h9l4 4v14H6z"/><path d="M15 3v4h4"/></svg>',
  pin: '<svg class="ico" viewBox="0 0 24 24"><path d="M12 16v5M9 3h6l-1 6 3 3v2H7v-2l3-3z"/></svg>',
  rev: '<svg class="ico" viewBox="0 0 24 24"><path d="M4 7h10M4 12h7M4 17h10"/><circle cx="18" cy="12" r="3"/></svg>',
  search: '<svg class="ico" viewBox="0 0 24 24"><circle cx="11" cy="11" r="6"/><path d="M20 20l-4-4"/></svg>',
  gear: '<svg class="ico" viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M12 3v3M12 18v3M3 12h3M18 12h3M5.6 5.6l2.1 2.1M16.3 16.3l2.1 2.1M5.6 18.4l2.1-2.1M16.3 7.7l2.1-2.1"/></svg>',
  tools: '<svg class="ico" viewBox="0 0 24 24"><path d="M9 7H5v10h4M15 7h4v10h-4M8 12h8"/></svg>',
  disk: '<svg class="ico" viewBox="0 0 24 24"><ellipse cx="12" cy="6" rx="7" ry="3"/><path d="M5 6v12c0 1.7 3.1 3 7 3s7-1.3 7-3V6M5 12c0 1.7 3.1 3 7 3s7-1.3 7-3"/></svg>',
  back: '<svg class="ico" viewBox="0 0 24 24"><path d="M15 6l-6 6 6 6"/></svg>',
  check: '<svg class="ico" viewBox="0 0 24 24" style="width:22px;height:22px"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>',
};
const KINDS = ['note', 'fact', 'preference', 'decision'];

// ---------- backend

const api = new Proxy({}, {
  get: (_, name) => async (...args) => {
    const g = window.go && window.go.main;
    const fn = g && ((g.App && g.App[name]) || (g.Window && g.Window[name]));
    if (!fn) throw new Error('jot backend is not available');
    return fn(...args);
  },
});

function waitForBackend(ms = 4000) {
  return new Promise(resolve => {
    const t0 = Date.now();
    (function poll() {
      if (window.go && window.go.main && window.go.main.App) return resolve(true);
      if (Date.now() - t0 > ms) return resolve(false);
      setTimeout(poll, 25);
    })();
  });
}

// ---------- state

const S = {
  state: null, notes: [], tools: [], dupes: [],
  view: 'library',          // library | review | settings | onboarding
  filter: { type: 'all' },  // all | pinned | store:<key> | source:<name>
  query: '', hits: null,
  sort: 'updated',
  sel: null,                // "store\u001fid" (no NUL: HTML attributes can't carry it)
  editing: null,            // {mode:'new'|'edit', ...}
  history: [],
  reviewIndex: 0,
  settingsTab: 'general',
  obStep: 1, obPick: new Set(), obCur: null,
};
const keyOf = n => n.store + '\u001f' + n.id;
const isMac = () => (S.state && S.state.os === 'darwin');
const modKey = () => isMac() ? '⌘' : 'Ctrl';

// ---------- utils

function toast(msg, opts = {}) {
  const el = $('#toast');
  el.className = 'toast' + (opts.err ? ' err' : '');
  el.innerHTML = `<span>${esc(msg)}</span>` + (opts.action ? `<a>${esc(opts.action)}</a>` : '');
  if (opts.action) $('a', el).onclick = () => { el.classList.remove('show'); opts.onAction(); };
  requestAnimationFrame(() => el.classList.add('show'));
  clearTimeout(toast.t);
  toast.t = setTimeout(() => el.classList.remove('show'), opts.err ? 6000 : 3200);
}
const fail = e => toast(String((e && e.message) || e).replace(/^Error: /, ''), { err: true });

function ago(iso) {
  const d = new Date(iso), s = (Date.now() - d) / 1000;
  if (isNaN(d)) return '';
  if (s < 60) return 'now';
  if (s < 3600) return Math.floor(s / 60) + 'm';
  if (s < 86400) return Math.floor(s / 3600) + 'h';
  if (s < 86400 * 7) return Math.floor(s / 86400) + 'd';
  const opt = { month: 'short', day: 'numeric' };
  if (d.getFullYear() !== new Date().getFullYear()) opt.year = 'numeric';
  return d.toLocaleDateString('en-US', opt);
}
function stamp(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return '';
  const p = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
function rest(body) {
  const i = body.indexOf('\n');
  return i < 0 ? '' : body.slice(i + 1).trim();
}
function snippet(n) {
  const r = rest(n.body).replace(/^\s*([-*+]|\d+\.)\s+/gm, '').replace(/[#>*_`]/g, '').replace(/\s+/g, ' ').trim();
  return r || n.kind;
}
const parseTags = s => s.split(',').map(t => t.trim().toLowerCase()).filter(Boolean);
const shownTags = n => n.tags.filter(t => t !== 'pinned');
const sep = () => (S.state && S.state.os === 'windows') ? '\\' : '/';

// Small, safe Markdown: escape first, then add structure.
function md(src) {
  const lines = src.replace(/\r\n/g, '\n').split('\n');
  const out = [];
  let i = 0;
  const inline = t => esc(t)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[^*])\*([^*\s][^*]*)\*/g, '$1<em>$2</em>')
    .replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');
  while (i < lines.length) {
    const l = lines[i];
    if (/^```/.test(l)) {
      const buf = []; i++;
      while (i < lines.length && !/^```/.test(lines[i])) buf.push(lines[i++]);
      i++; out.push(`<pre><code>${esc(buf.join('\n'))}</code></pre>`); continue;
    }
    if (/^#{1,4}\s/.test(l)) { const n = Math.min(4, l.match(/^#+/)[0].length + 1); out.push(`<h${n}>${inline(l.replace(/^#+\s*/, ''))}</h${n}>`); i++; continue; }
    if (/^\s*([-*+]|\d+\.)\s+/.test(l)) {
      const ordered = /^\s*\d+\./.test(l), items = [];
      while (i < lines.length && /^\s*([-*+]|\d+\.)\s+/.test(lines[i])) items.push(lines[i++].replace(/^\s*([-*+]|\d+\.)\s+/, ''));
      out.push(`<${ordered ? 'ol' : 'ul'}>${items.map(t => `<li>${inline(t)}</li>`).join('')}</${ordered ? 'ol' : 'ul'}>`); continue;
    }
    if (/^>\s?/.test(l)) {
      const buf = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) buf.push(lines[i++].replace(/^>\s?/, ''));
      out.push(`<blockquote>${inline(buf.join(' '))}</blockquote>`); continue;
    }
    if (/^\s*(---|\*\*\*)\s*$/.test(l)) { out.push('<hr>'); i++; continue; }
    if (!l.trim()) { i++; continue; }
    const buf = [];
    while (i < lines.length && lines[i].trim() && !/^(```|#{1,4}\s|\s*([-*+]|\d+\.)\s+|>)/.test(lines[i])) buf.push(lines[i++]);
    out.push(`<p>${inline(buf.join(' '))}</p>`);
  }
  return out.join('');
}

// Word diff (LCS) for the duplicate review.
function wordDiff(a, b) {
  const A = a.split(/(\s+)/), B = b.split(/(\s+)/);
  if (A.length * B.length > 400000) return { a: esc(a), b: esc(b) };
  const m = A.length, n = B.length, L = Array.from({ length: m + 1 }, () => new Uint16Array(n + 1));
  for (let i = m - 1; i >= 0; i--) for (let j = n - 1; j >= 0; j--) L[i][j] = A[i] === B[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
  let i = 0, j = 0, x = '', y = '';
  while (i < m && j < n) {
    if (A[i] === B[j]) { x += esc(A[i]); y += esc(B[j]); i++; j++; }
    else if (L[i + 1][j] >= L[i][j + 1]) { x += /^\s+$/.test(A[i]) ? esc(A[i]) : `<del>${esc(A[i])}</del>`; i++; }
    else { y += /^\s+$/.test(B[j]) ? esc(B[j]) : `<ins>${esc(B[j])}</ins>`; j++; }
  }
  for (; i < m; i++) x += /^\s+$/.test(A[i]) ? esc(A[i]) : `<del>${esc(A[i])}</del>`;
  for (; j < n; j++) y += /^\s+$/.test(B[j]) ? esc(B[j]) : `<ins>${esc(B[j])}</ins>`;
  return { a: x.replace(/<\/del>(\s*)<del>/g, '$1'), b: y.replace(/<\/ins>(\s*)<ins>/g, '$1') };
}

// ---------- data

async function refresh(opts = {}) {
  const [state, notes, dupes] = await Promise.all([api.State(), api.Notes(), api.Duplicates()]);
  S.state = state; S.notes = notes || []; S.dupes = dupes || [];
  if (opts.tools) S.tools = (await api.Tools()) || [];
  if (S.query) S.hits = await api.Search(S.query);
  if (S.reviewIndex >= S.dupes.length) S.reviewIndex = Math.max(0, S.dupes.length - 1);
}

function visible() {
  let list = S.notes;
  const f = S.filter;
  if (f.type === 'pinned') list = list.filter(n => n.pinned);
  else if (f.type === 'store') list = list.filter(n => n.store === f.key);
  else if (f.type === 'source') list = list.filter(n => (n.source || 'unknown') === f.key);
  if (S.hits) {
    const rank = new Map(S.hits.map((h, i) => [keyOf(h), i]));
    list = list.filter(n => rank.has(keyOf(n))).sort((a, b) => rank.get(keyOf(a)) - rank.get(keyOf(b)));
  } else if (S.sort === 'created') list = [...list].sort((a, b) => new Date(b.created) - new Date(a.created));
  else if (S.sort === 'title') list = [...list].sort((a, b) => a.title.localeCompare(b.title));
  else list = [...list].sort((a, b) => (b.pinned - a.pinned) || (new Date(b.updated) - new Date(a.updated)));
  return list;
}
const selected = () => S.notes.find(n => keyOf(n) === S.sel) || null;

function filterTitle() {
  const f = S.filter;
  if (S.query) return 'Results';
  if (f.type === 'pinned') return 'Pinned';
  if (f.type === 'store') { const s = S.state.stores.find(s => s.key === f.key); return s ? s.label : 'Notes'; }
  if (f.type === 'source') return f.key;
  return 'All notes';
}

// ---------- render

function render() {
  const app = $('#app');
  if (S.view === 'onboarding') { app.innerHTML = renderOnboarding(); bindOnboarding(); return; }
  if (S.view === 'settings') { app.innerHTML = renderSettings(); bindSettings(); return; }
  app.innerHTML = `${topbar()}<div class="main">${sidebar()}${S.view === 'review' ? renderReview() : renderList() + renderDoc()}</div>`;
  bindShell();
  if (S.view === 'review') bindReview(); else { bindList(); bindDoc(); }
}

function topbar() {
  return `<div class="tb">${isMac() ? '<span class="mac-pad"></span>' : ''}<span class="mark">jot<b>.</b></span><span class="sp"></span>
<label class="srch">${ICON.search}<input id="q" placeholder="Search notes" value="${esc(S.query)}" spellcheck="false" autocomplete="off"><span class="key">${modKey()} K</span></label>
<button class="btn" id="new">New note</button>
<button class="iconbtn" id="gear" title="Settings">${ICON.gear}</button></div>`;
}

function sidebar() {
  const ni = (label, count, attrs, on, cls = '', icon = '', alert = false) =>
    `<div class="ni ${cls}${on ? ' on' : ''}" tabindex="0" ${attrs}>${icon}<span class="lbl">${esc(label)}</span><span class="c${alert ? ' alert' : ''}">${count}</span></div>`;
  const f = S.filter, lib = S.view === 'library';
  const sources = {};
  S.notes.forEach(n => { const k = n.source || 'unknown'; sources[k] = (sources[k] || 0) + 1; });
  const st = S.state, global = st.stores[0];
  let h = `<div class="side"><div class="sh">Library</div>`;
  h += ni('All notes', S.notes.length, 'data-f="all"', lib && f.type === 'all', '', ICON.all);
  h += ni('Pinned', S.notes.filter(n => n.pinned).length, 'data-f="pinned"', lib && f.type === 'pinned', '', ICON.pin);
  h += ni('Needs review', S.dupes.length || '', 'data-v="review"', S.view === 'review', '', ICON.rev, S.dupes.length > 0);
  h += `<div class="sh">Scope</div>`;
  st.stores.forEach(s => { h += ni(s.label, s.count, `data-f="store" data-k="${esc(s.key)}"`, lib && f.type === 'store' && f.key === s.key, 'mono'); });
  h += `<div class="ni" tabindex="0" id="addproj" style="color:var(--ink3)"><span class="lbl">+ Add project</span></div>`;
  const names = Object.keys(sources).sort((a, b) => sources[b] - sources[a]);
  if (names.length) {
    h += `<div class="sh">Written by</div>`;
    names.forEach(k => { h += ni(k, sources[k], `data-f="source" data-k="${esc(k)}"`, lib && f.type === 'source' && f.key === k, 'mono'); });
  }
  const tools = st.connected;
  h += `<div class="foot"><div class="txt"><div title="${esc(global.dir)}">${esc(tildeish(global.dir))}${global.git ? ' · git' : ''}</div>
<div>${tools ? `<span class="g">●</span> ${tools} tool${tools > 1 ? 's' : ''} connected` : `<span class="r">●</span> no tools connected`}</div></div></div></div>`;
  return h;
}
function tildeish(p) {
  return p.replace(/^([A-Za-z]:\\Users\\[^\\]+|\/Users\/[^/]+|\/home\/[^/]+)/, '~');
}

function renderList() {
  const list = visible();
  let rows = '';
  const hl = s => {
    if (!S.query) return esc(s);
    const terms = S.query.toLowerCase().split(/\s+/).filter(t => t.length > 1).map(t => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
    if (!terms.length) return esc(s);
    return esc(s).replace(new RegExp(`(${terms.join('|')})`, 'gi'), '<mark>$1</mark>');
  };
  list.forEach(n => {
    rows += `<div class="row${keyOf(n) === S.sel ? ' on' : ''}" tabindex="0" data-key="${esc(keyOf(n))}">
<div class="rt">${n.pinned ? '<span class="dot"></span>' : ''}<span>${hl(n.title || 'Untitled')}</span></div>
<div class="rs">${hl(snippet(n))}</div>
<div class="rm">${esc(n.scope)} · ${esc(n.source || 'unknown')} · ${esc(ago(n.updated))}</div></div>`;
  });
  if (!list.length) rows = emptyList();
  const sort = S.query ? '<span class="sortw">Best match</span>' :
    `<span class="sortw"><select id="sort">${[['updated', 'Last edited'], ['created', 'Created'], ['title', 'Title']].map(([v, l]) => `<option value="${v}"${S.sort === v ? ' selected' : ''}>${l}</option>`).join('')}</select>▾</span>`;
  return `<div class="list"><div class="lh"><h2 class="serif">${esc(filterTitle())}</h2>${sort}</div><div class="rows" id="rows">${rows}</div></div>`;
}
function emptyList() {
  if (S.query) return `<div class="empty"><h3>Nothing matches</h3>No note mentions “${esc(S.query)}”.</div>`;
  if (!S.notes.length) return `<div class="empty"><h3>No notes yet</h3>Connected AI tools add notes here as you work.<br>You can also write one yourself.<br><button class="btn k" id="new2">Write a note</button></div>`;
  if (S.filter.type === 'pinned') return `<div class="empty"><h3>Nothing pinned</h3>Pin a note to keep it at the top.</div>`;
  return `<div class="empty"><h3>Empty</h3>No notes here yet.</div>`;
}

function renderDoc() {
  if (S.editing) return renderEditor();
  const n = selected();
  if (!n) return `<div class="doc"><div class="dh"></div><div class="empty" style="margin-top:80px">${S.notes.length ? 'Select a note to read it.' : ''}</div></div>`;
  const body = rest(n.body);
  const fm = [['kind', n.kind], ['scope', n.scope], ['tags', shownTags(n).join(', ') || '—'], ['source', n.source || '—'], ['created', stamp(n.created)], ['updated', stamp(n.updated)]]
    .map(([k, v]) => `<span class="k">${k}</span><span>${esc(v)}</span>`).join('');
  let hist = '';
  if (S.history.length) {
    hist = `<table>${S.history.map(c => {
      const verb = c.subject.split(' ')[0];
      const label = { remember: 'created', update: 'edited', merge: 'merged', forget: 'deleted' }[verb] || c.subject;
      return `<tr><td class="h">${esc(c.hash)}</td><td>${esc(label)}</td><td class="d">${esc(ago(c.date))}${/^\d+[mhd]$/.test(ago(c.date)) ? ' ago' : ''}</td></tr>`;
    }).join('')}</table>`;
  } else if (!S.state.gitFound) hist = `<div class="nogit">Install Git to keep a history of every change.</div>`;
  return `<div class="doc fade"><div class="dh"><span class="path" title="${esc(n.file)}">${esc(n.scope)} / ${esc(n.id)}.md</span><span class="sp"></span>
<button class="btn q sm" id="pin">${n.pinned ? 'Unpin' : 'Pin'}</button><button class="btn q sm" id="edit">Edit</button><button class="btn q sm" id="reveal">Show file</button><button class="btn q sm danger" id="del">Delete</button></div>
<div class="dscroll"><div class="dc selectable"><h1 class="serif">${esc(n.title || 'Untitled')}</h1><div class="fm">${fm}</div>
<div class="pr">${md(body)}</div>${hist ? `<div class="hs"><div class="t">History</div>${hist}</div>` : ''}</div></div></div>`;
}

function renderEditor() {
  const e = S.editing;
  const stores = S.state.stores.map(s => `<option value="${esc(s.key)}"${s.key === e.store ? ' selected' : ''}>${esc(s.label)}</option>`).join('');
  const kinds = [...new Set([...KINDS, e.kind])].map(k => `<option${k === e.kind ? ' selected' : ''}>${esc(k)}</option>`).join('');
  return `<div class="doc fade"><div class="dh"><span class="path">${e.mode === 'new' ? 'New note' : `Editing ${esc(e.scope)} / ${esc(e.id)}.md`}</span><span class="sp"></span></div>
<div class="dscroll"><div class="ed">
<div class="meta">${e.mode === 'new' ? `<label>scope <select class="field" id="e-store">${stores}</select></label>` : ''}
<label>kind <select class="field" id="e-kind">${kinds}</select></label>
<label style="flex:1;min-width:220px">tags <input class="field" id="e-tags" style="flex:1" placeholder="comma, separated" value="${esc(e.tags.join(', '))}"></label></div>
<textarea id="e-body" spellcheck="true" placeholder="The first line becomes the title.">${esc(e.body)}</textarea>
<div class="acts"><button class="btn k" id="e-save">${e.mode === 'new' ? 'Save note' : 'Save'}</button><button class="btn q" id="e-cancel">Cancel</button>
<span class="hint"><span class="key">${modKey()} S</span> save <span class="key">Esc</span> cancel · Markdown works</span></div></div></div></div>`;
}

function renderReview() {
  if (!S.dupes.length) return `<div class="rv"><div class="empty" style="margin-top:100px"><h3>Nothing to review</h3>No two notes say the same thing right now.</div></div>`;
  const i = S.reviewIndex, p = S.dupes[i], d = wordDiff(p.drop.body, p.keep.body);
  const who = p.keep.source === p.drop.source ? `${p.keep.source || 'Someone'} saved two notes`
    : `${cap(p.drop.source || 'unknown')} and ${p.keep.source || 'unknown'} each saved a note`;
  const days = Math.round(Math.abs(new Date(p.keep.created) - new Date(p.drop.created)) / 864e5);
  const when = days === 0 ? 'on the same day' : days === 1 ? 'a day apart' : `${days} days apart`;
  const next = S.dupes.map((q, j) => j === i ? '' : `<div class="ur" data-i="${j}"><span class="l">${esc(q.drop.title)}</span><span class="m">vs</span><span class="l">${esc(q.keep.title)}</span><span class="s">${esc(q.drop.source || '?')} · ${esc(q.keep.source || '?')}</span></div>`).join('');
  return `<div class="rv fade"><div class="mh"><div class="crumb">Needs review · ${i + 1} of ${S.dupes.length} · ${Math.round(p.score * 100)}% alike</div>
<h1 class="serif">These two notes say the same thing</h1><p>${esc(who)}, ${when}.</p></div>
<div class="cols"><div class="nt"><div class="h"><span>${esc(p.drop.scope)} / ${esc(p.drop.id)}.md</span><span>${esc(p.drop.source || '?')} · ${esc(ago(p.drop.created))}</span></div><div class="b selectable">${d.a}</div></div>
<div class="nt"><div class="h"><span>${esc(p.keep.scope)} / ${esc(p.keep.id)}.md</span><span>${esc(p.keep.source || '?')} · ${esc(ago(p.keep.created))}</span></div><div class="b selectable">${d.b}</div></div></div>
<div class="mg"><div class="h"><span>Merged → ${esc(p.keep.scope)} / ${esc(p.keep.id)}.md</span><span class="muted">editable</span></div>
<textarea id="m-body" spellcheck="true">${esc(p.mergedBody)}</textarea>
<div class="tags">tags <input id="m-tags" value="${esc(p.mergedTags.join(', '))}"></div></div>
<div class="acts"><span class="n">Both originals stay in git history.</span><span style="flex:1"></span>
<button class="btn q" id="r-skip"${S.dupes.length < 2 ? ' disabled' : ''}>Skip</button><button class="btn" id="r-both">Keep both</button><button class="btn k" id="r-merge">Merge</button></div>
${next ? `<div class="up"><div class="t">Up next</div>${next}</div>` : ''}</div>`;
}
const cap = s => s.charAt(0).toUpperCase() + s.slice(1);

// ---------- onboarding

function renderOnboarding() {
  const st = S.state;
  const head = `<div class="tb">${isMac() ? '<span class="mac-pad"></span>' : ''}<span class="mark">jot<b>.</b></span><span class="sp"></span><span class="muted" style="font-size:12px">Setup</span></div>`;
  let body = '';
  if (S.obStep === 1) {
    const g = st.stores[0];
    body = `<div class="step">Step 1 of 3 · Welcome</div>
<h1 class="serif">One memory for every AI tool you use.</h1>
<p class="lead">Claude, Cursor, Codex and Antigravity forget everything between sessions. jot gives them one shared notebook. When one tool learns something about you or your project, the others know it too.</p>
<div class="facts">
<div class="fact"><span class="k">Where notes live</span><span class="v"><span class="mono">${esc(g.dir)}</span><br>Plain Markdown files you can open, edit or delete.</span></div>
<div class="fact"><span class="k">History</span><span class="v">${st.gitFound ? '<span class="ok">Git found.</span> Every change is a commit, so nothing is ever lost.' : 'Git isn’t installed. jot works without it, but install Git to keep a history of every change.'}</span></div>
<div class="fact"><span class="k">Privacy</span><span class="v">Everything stays on this computer. jot has no account, no cloud and no tracking.</span></div></div>
<div class="act"><span class="note">Version ${esc(st.version)}</span><span style="flex:1"></span><button class="btn k" id="ob-next">Get started</button></div>`;
  } else if (S.obStep === 2) {
    const usable = S.tools.filter(t => t.state === 'absent' || t.state === 'different');
    const found = S.tools.filter(t => t.state !== 'missing').length;
    if (!S.obCur) S.obCur = (usable[0] || S.tools.find(t => t.state !== 'missing') || {}).id;
    const rows = S.tools.map(t => {
      const can = t.state === 'absent' || t.state === 'different';
      const on = t.state === 'connected' || S.obPick.has(t.id);
      const lbl = { connected: ['ok', 'connected'], absent: ['go', on ? 'will connect' : 'skip'], different: ['go', on ? 'will update' : 'skip'], missing: ['', 'not installed'], error: ['err', 'config unreadable'] }[t.state] || ['', t.state];
      return `<tr class="${t.state !== 'missing' ? 'click' : ''}${S.obCur === t.id ? ' cur' : ''}" data-id="${t.id}" style="${t.state === 'missing' ? 'opacity:.5' : ''}">
<td class="cbc"><span class="cbx${on ? ' on' : ''}${can ? '' : ' dis'}" data-cb="${t.id}"></span></td><td class="nm">${esc(t.name)}</td>
<td class="pth">${t.state === 'missing' ? '' : esc(t.config)}</td><td class="st ${lbl[0]}">${lbl[1]}</td></tr>`;
    }).join('');
    const cur = S.tools.find(t => t.id === S.obCur);
    const diff = cur && cur.state !== 'missing' && cur.snippet ? `<div class="diff"><div class="h"><span>${esc(cur.config)}</span><span>${cur.state === 'connected' ? 'already there' : '+' + cur.lines + ' lines'}</span></div><pre>${esc(cur.snippet).split('\n').map(l => `<span class="add">${cur.state === 'connected' ? '  ' : '+ '}${l}</span>`).join('\n')}</pre></div>` : '';
    const n = S.obPick.size;
    const noCLI = !st.cli;
    body = `<div class="step">Step 2 of 3 · Connect</div>
<h1 class="serif">Which tools should share your notes?</h1>
<p class="lead">${found ? `jot found ${found === 1 ? 'one AI tool' : numWord(found) + ' AI tools'} on this computer. It adds one entry to each tool’s MCP config and changes nothing else. The original file is backed up, and you can undo this in Settings.` : 'jot didn’t find any supported AI tools yet. Install Claude Code, Cursor, Codex, Antigravity or Claude Desktop, then connect them from Settings.'}</p>
${noCLI ? `<p class="lead" style="color:var(--red);margin-top:-14px">The jot command-line tool is missing from the app folder, so tools can’t be connected. Reinstall jot to fix this.</p>` : ''}
<table class="tt">${rows}</table>${diff}
<div class="act"><span class="note">Notes are Markdown files in <span class="mono">${esc(tildeish(st.stores[0].dir))}</span></span><span style="flex:1"></span>
<button class="btn q" id="ob-back">Back</button><button class="btn${n ? '' : ' k'}" id="ob-skip">${n ? 'Skip' : 'Continue'}</button>${n ? `<button class="btn k" id="ob-connect"${noCLI ? ' disabled' : ''}>Connect ${n === 1 ? '1 tool' : n + ' tools'}</button>` : ''}</div>`;
  } else {
    const connected = S.tools.filter(t => t.state === 'connected');
    body = `<div class="big-ok">${ICON.check}</div><div class="step">Step 3 of 3 · Ready</div>
<h1 class="serif">jot is ready.</h1>
<p class="lead">${connected.length ? `Restart ${listWords(connected.map(t => t.name))} so ${connected.length > 1 ? 'they pick' : 'it picks'} up jot. After that, ask any of them to “remember” something and it shows up here.` : 'Connect a tool from Settings whenever you’re ready. You can also write notes yourself.'}</p>
<div class="facts">
<div class="fact"><span class="k">Command line</span><span class="v">${st.onPath ? '<span class="ok">Ready.</span> Run <span class="mono">jot</span> in any new terminal.' : st.cli ? `<span>jot isn’t on your PATH yet.</span> <button class="btn sm" id="ob-path" style="margin-left:8px">Add to PATH</button>` : 'Not installed with this copy of the app.'}</span></div>
<div class="fact"><span class="k">Projects</span><span class="v">Notes are global by default. Add a project folder to keep notes that only matter there. <button class="btn sm" id="ob-proj" style="margin-left:8px">Add project</button></span></div></div>
<div class="act"><span style="flex:1"></span><button class="btn k" id="ob-done">Open jot</button></div>`;
  }
  return `${head}<div class="ob"><div class="in fade">${body}</div></div>`;
}
const numWord = n => ['zero', 'one', 'two', 'three', 'four', 'five', 'six'][n] || String(n);
const listWords = a => a.length < 2 ? a.join('') : a.slice(0, -1).join(', ') + ' and ' + a[a.length - 1];

function bindOnboarding() {
  const on = (id, fn) => { const el = $('#' + id); if (el) el.onclick = fn; };
  on('ob-next', async () => {
    try { S.tools = await api.Tools(); } catch (e) { fail(e); }
    S.obPick = new Set(S.tools.filter(t => t.state === 'absent' || t.state === 'different').map(t => t.id));
    if (!S.state.cli) S.obPick.clear();
    S.obStep = 2; render();
  });
  on('ob-back', () => { S.obStep = 1; render(); });
  on('ob-skip', () => { S.obStep = 3; render(); });
  on('ob-connect', async (ev) => {
    ev.target.disabled = true; ev.target.textContent = 'Connecting…';
    try { S.tools = await api.Connect([...S.obPick]); S.obStep = 3; await refresh(); render(); }
    catch (e) { try { S.tools = await api.Tools(); } catch (_) { } fail(e); render(); }
  });
  on('ob-path', async () => { try { S.state = await api.AddToPath(); render(); toast('Added. Open a new terminal to use jot.'); } catch (e) { fail(e); } });
  on('ob-proj', async () => { try { const d = await api.ChooseProject(); if (d) { await refresh(); toast('Added ' + d.split(/[\\/]/).pop()); } } catch (e) { fail(e); } });
  on('ob-done', async () => {
    try { await api.FinishOnboarding(); await refresh(); } catch (e) { fail(e); }
    S.view = 'library'; S.filter = { type: 'all' };
    const v = visible(); S.sel = v[0] ? keyOf(v[0]) : null; await loadHistory(); render();
  });
  document.querySelectorAll('.tt tr.click').forEach(tr => tr.onclick = ev => {
    const id = tr.dataset.id, t = S.tools.find(t => t.id === id);
    if (ev.target.dataset.cb && (t.state === 'absent' || t.state === 'different') && S.state.cli) {
      S.obPick.has(id) ? S.obPick.delete(id) : S.obPick.add(id);
    }
    S.obCur = id; render();
  });
}

// ---------- settings

function renderSettings() {
  const st = S.state;
  const tab = (id, icon, label) => `<button class="${S.settingsTab === id ? 'on' : ''}" data-tab="${id}">${icon}${label}</button>`;
  let body = '';
  if (S.settingsTab === 'general') {
    body = `<div class="form">
<div class="lbl">Version</div><div class="val"><span class="pill">jot ${esc(st.version)}</span></div>
<div class="lbl">Command line</div><div class="val">${st.cli ? `<div class="line"><span class="pill" title="${esc(st.cli)}">${esc(st.cli)}</span>${st.onPath ? '<span class="ok" style="font-size:12.5px">● on PATH</span>' : '<button class="btn sm" id="s-path">Add to PATH</button>'}</div>
<div class="hint">${st.onPath ? 'Run <span class="mono">jot</span> in any new terminal. Try <span class="mono">jot ls</span> or <span class="mono">jot search</span>.' : 'Adding it lets you run <span class="mono">jot</span> from any terminal.'}</div>` : '<span class="muted">Not found next to the app. Reinstall jot to get the command-line tool.</span>'}</div>
<hr>
<div class="lbl">Search</div><div class="val">Keyword search with relevance ranking, tag matches and a boost for recent notes. Built in, works offline.</div>
<div class="lbl">Duplicates</div><div class="val">${S.dupes.length ? `${S.dupes.length} pair${S.dupes.length > 1 ? 's' : ''} to review. <button class="btn sm" id="s-review" style="margin-left:6px">Review</button>` : 'None right now.'}<div class="hint">jot flags notes that share most of their words. Agents get the same warning before they save a near copy.</div></div>
<hr>
<div class="lbl">Setup</div><div class="val"><button class="btn" id="s-onboard">Run setup again</button></div>
</div>`;
  } else if (S.settingsTab === 'tools') {
    const rows = S.tools.map(t => {
      const lbl = { connected: ['ok', 'connected'], absent: ['', 'not connected'], different: ['go', 'needs update'], missing: ['', 'not installed'], error: ['err', 'config unreadable'] }[t.state] || ['', t.state];
      const btn = t.state === 'connected' ? `<button class="btn sm" data-dis="${t.id}">Disconnect</button>`
        : (t.state === 'absent' || t.state === 'different') ? `<button class="btn sm k" data-con="${t.id}"${st.cli ? '' : ' disabled'}>${t.state === 'different' ? 'Update' : 'Connect'}</button>` : '';
      return `<tr class="${t.state !== 'missing' ? 'click' : ''}${S.obCur === t.id ? ' cur' : ''}" data-id="${t.id}" style="${t.state === 'missing' ? 'opacity:.5' : ''}"><td class="nm" style="padding-left:4px">${esc(t.name)}</td><td class="pth">${t.state === 'missing' ? '' : esc(t.config)}</td><td class="st ${lbl[0]}">${lbl[1]}</td><td style="width:120px;text-align:right;padding-right:4px">${btn}</td></tr>`;
    }).join('');
    const cur = S.tools.find(t => t.id === S.obCur && t.state !== 'missing');
    const diff = cur && cur.snippet ? `<div class="diff"><div class="h"><span>${esc(cur.config)}</span><span>${cur.state === 'connected' ? 'current entry' : '+' + cur.lines + ' lines'}</span></div><pre>${esc(cur.snippet).split('\n').map(l => `<span class="add">${cur.state === 'connected' ? '  ' : '+ '}${l}</span>`).join('\n')}</pre></div>` : '';
    body = `<div class="form" style="display:block"><p class="muted" style="margin-bottom:16px;font-size:13px;line-height:1.55">jot adds one <span class="mono">jot</span> entry to each tool’s MCP config. The first time it edits a file, it saves the original as <span class="mono">.jot-backup</span>. Restart a tool after connecting it.</p><table class="tt">${rows}</table>${diff}</div>`;
  } else {
    const rows = st.stores.map((s, i) => `<div class="sr"><span class="n">${esc(s.label)}</span><span class="p" title="${esc(s.dir)}">${esc(s.dir)}</span><span class="c">${s.count} note${s.count === 1 ? '' : 's'}${s.git ? ' · git' : ''}</span>
<button class="btn sm q" data-rev="${esc(s.dir)}">Show</button>${i ? `<button class="btn sm q" data-rm="${esc(s.key)}">Remove</button>` : ''}</div>`).join('');
    body = `<div class="form">
<div class="lbl">Stores</div><div class="val"><div class="storelist">${rows}</div><div class="line" style="margin-top:12px"><button class="btn sm" id="s-proj">Add project folder</button></div>
<div class="hint">The global store holds notes every tool sees. A project store lives in that project’s <span class="mono">.jot</span> folder, so you can commit it with the code. Removing a project only hides it here. Its files stay on disk.</div></div>
<hr>
<div class="lbl">History</div><div class="val">${st.gitFound ? 'Every save, edit, merge and delete is a git commit in the store folder.' : 'Git isn’t installed, so changes aren’t versioned. Install Git and jot starts keeping history automatically.'}</div>
<div class="lbl">App settings</div><div class="val"><span class="pill" title="${esc(st.prefsFile)}">${esc(st.prefsFile)}</span></div>
</div>`;
  }
  return `<div class="tb"><button class="iconbtn" id="s-back" title="Back">${ICON.back}</button><span class="mark">jot<b>.</b></span><span class="sp"></span><span class="muted" style="font-size:12px">Settings</span></div>
<div class="st-wrap"><div class="seg">${tab('general', ICON.gear, 'General')}${tab('tools', ICON.tools, 'Tools')}${tab('storage', ICON.disk, 'Storage')}</div><div class="st-body fade">${body}</div></div>`;
}

function bindSettings() {
  $('#s-back').onclick = () => { S.view = 'library'; render(); };
  document.querySelectorAll('[data-tab]').forEach(b => b.onclick = async () => {
    S.settingsTab = b.dataset.tab;
    if (S.settingsTab === 'tools') { try { S.tools = await api.Tools(); } catch (e) { fail(e); } S.obCur = (S.tools.find(t => t.state !== 'missing') || {}).id; }
    render();
  });
  const on = (id, fn) => { const el = $('#' + id); if (el) el.onclick = fn; };
  on('s-path', async () => { try { S.state = await api.AddToPath(); render(); toast('Added. Open a new terminal to use jot.'); } catch (e) { fail(e); } });
  on('s-review', () => { S.view = 'review'; S.reviewIndex = 0; render(); });
  on('s-onboard', async () => { S.obStep = 1; S.view = 'onboarding'; render(); });
  on('s-proj', async () => { try { const d = await api.ChooseProject(); if (d) { await refresh(); render(); toast('Added ' + d.split(/[\\/]/).pop()); } } catch (e) { fail(e); } });
  document.querySelectorAll('[data-rev]').forEach(b => b.onclick = () => api.Reveal(b.dataset.rev).catch(fail));
  document.querySelectorAll('[data-rm]').forEach(b => b.onclick = async () => {
    try { await api.RemoveProject(b.dataset.rm); if (S.filter.key === b.dataset.rm) S.filter = { type: 'all' }; await refresh(); render(); } catch (e) { fail(e); }
  });
  document.querySelectorAll('[data-con]').forEach(b => b.onclick = async ev => {
    ev.stopPropagation(); b.disabled = true;
    try { S.tools = await api.Connect([b.dataset.con]); await refresh(); render(); toast('Connected. Restart ' + S.tools.find(t => t.id === b.dataset.con).name + ' to load jot.'); }
    catch (e) { fail(e); }
  });
  document.querySelectorAll('[data-dis]').forEach(b => b.onclick = async ev => {
    ev.stopPropagation(); b.disabled = true;
    try { S.tools = await api.Disconnect(b.dataset.dis); await refresh(); render(); toast('Disconnected.'); } catch (e) { fail(e); }
  });
  document.querySelectorAll('.tt tr.click').forEach(tr => tr.onclick = () => { S.obCur = tr.dataset.id; render(); });
}

// ---------- library bindings

function bindShell() {
  const q = $('#q');
  q.oninput = () => {
    clearTimeout(bindShell.t);
    bindShell.t = setTimeout(async () => {
      S.query = q.value.trim();
      S.hits = S.query ? await api.Search(S.query).catch(() => []) : null;
      if (S.view !== 'library') S.view = 'library';
      const list = visible();
      if (!list.find(n => keyOf(n) === S.sel)) S.sel = list[0] ? keyOf(list[0]) : null;
      await loadHistory();
      const pos = q.selectionStart; render(); const nq = $('#q'); nq.focus(); nq.setSelectionRange(pos, pos);
    }, 120);
  };
  q.onkeydown = ev => {
    if (ev.key === 'Escape') { q.value = ''; q.oninput(); q.blur(); }
    if (ev.key === 'ArrowDown' || ev.key === 'Enter') { ev.preventDefault(); const r = $('.row.on') || $('.row'); if (r) r.focus(); }
  };
  $('#new').onclick = startNew;
  $('#gear').onclick = async () => { S.view = 'settings'; render(); };
  document.querySelectorAll('.side .ni[data-f]').forEach(el => el.onclick = async () => {
    S.view = 'library'; S.editing = null;
    S.filter = { type: el.dataset.f, key: el.dataset.k };
    const list = visible(); if (!list.find(n => keyOf(n) === S.sel)) S.sel = list[0] ? keyOf(list[0]) : null;
    await loadHistory(); render();
  });
  const rv = $('.side .ni[data-v="review"]');
  if (rv) rv.onclick = () => { S.view = 'review'; S.editing = null; render(); };
  $('#addproj').onclick = async () => {
    try { const d = await api.ChooseProject(); if (d) { await refresh(); S.filter = { type: 'store', key: d }; S.view = 'library'; render(); toast('Added ' + d.split(/[\\/]/).pop()); } } catch (e) { fail(e); }
  };
  document.querySelectorAll('.side .ni').forEach(el => el.onkeydown = ev => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); el.click(); } });
}

function bindList() {
  const s = $('#sort'); if (s) s.onchange = () => { S.sort = s.value; render(); };
  const n2 = $('#new2'); if (n2) n2.onclick = startNew;
  document.querySelectorAll('.row').forEach(r => {
    r.onclick = () => select(r.dataset.key);
    r.onkeydown = ev => {
      if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
        ev.preventDefault();
        const sib = ev.key === 'ArrowDown' ? r.nextElementSibling : r.previousElementSibling;
        if (sib && sib.classList.contains('row')) select(sib.dataset.key, true);
      }
      if (ev.key === 'Enter') { ev.preventDefault(); startEdit(); }
    };
  });
}

async function select(key, focus) {
  if (S.editing && !(await leaveEditor())) return;
  S.sel = key; await loadHistory(); render();
  const el = document.querySelector(`.row[data-key="${CSS.escape(key)}"]`);
  if (el) { if (focus) el.focus(); el.scrollIntoView({ block: 'nearest' }); }
}

async function loadHistory() {
  const n = selected();
  S.history = [];
  if (n) { try { S.history = (await api.History(n.store, n.id)) || []; } catch (_) { } }
}

function bindDoc() {
  if (S.editing) return bindEditor();
  const n = selected(); if (!n) return;
  $('#edit').onclick = startEdit;
  $('#reveal').onclick = () => api.Reveal(n.file).catch(fail);
  $('#pin').onclick = async () => {
    try { await api.SetPinned(n.store, n.id, !n.pinned); await refresh(); await loadHistory(); render(); } catch (e) { fail(e); }
  };
  $('#del').onclick = async () => {
    let ok;
    try { ok = await api.Confirm('Delete this note?', `“${n.title}” will be removed from ${n.scope}. ${S.state.gitFound ? 'Git history keeps a copy.' : ''}`); } catch (_) { ok = window.confirm('Delete this note?'); }
    if (!ok) return;
    try {
      const list = visible(), i = list.findIndex(x => keyOf(x) === S.sel);
      await api.Delete(n.store, n.id); await refresh();
      const after = visible(); S.sel = after[Math.min(i, after.length - 1)] ? keyOf(after[Math.min(i, after.length - 1)]) : null;
      await loadHistory(); render(); toast('Note deleted.');
    } catch (e) { fail(e); }
  };
}

function startNew() {
  S.view = 'library';
  const store = S.filter.type === 'store' ? S.filter.key : 'global';
  S.editing = { mode: 'new', store, kind: 'note', tags: [], body: '' };
  render(); $('#e-body').focus();
}
function startEdit() {
  const n = selected(); if (!n) return;
  S.editing = { mode: 'edit', store: n.store, id: n.id, scope: n.scope, kind: n.kind, tags: shownTags(n), pinned: n.pinned, body: n.body, orig: n.body };
  render(); const t = $('#e-body'); t.focus(); t.setSelectionRange(0, 0); t.scrollTop = 0;
}
function editorDirty() {
  const e = S.editing; if (!e) return false;
  const b = $('#e-body'); return b && b.value !== (e.mode === 'new' ? '' : e.orig);
}
async function leaveEditor() {
  if (editorDirty()) {
    let ok;
    try { ok = await api.Confirm('Discard changes?', 'Your edits to this note haven’t been saved.'); } catch (_) { ok = window.confirm('Discard changes?'); }
    if (!ok) return false;
  }
  S.editing = null; return true;
}
function bindEditor() {
  const e = S.editing, body = $('#e-body');
  const fit = () => { body.style.height = 'auto'; body.style.height = Math.max(360, body.scrollHeight + 2) + 'px'; };
  body.oninput = fit; fit();
  $('#e-cancel').onclick = async () => { if (await leaveEditor()) render(); };
  $('#e-save').onclick = async () => {
    const kind = $('#e-kind').value, tags = parseTags($('#e-tags').value), text = body.value;
    try {
      let n;
      if (e.mode === 'new') { const store = $('#e-store').value; n = await api.Create(store, text, kind, tags); }
      else { n = await api.Save(e.store, e.id, text, kind, e.pinned ? [...tags, 'pinned'] : tags); }
      S.editing = null; S.query = ''; S.hits = null;
      await refresh(); S.sel = keyOf(n);
      if (e.mode === 'new' && S.filter.type !== 'all' && !(S.filter.type === 'store' && S.filter.key === n.store)) S.filter = { type: 'all' };
      await loadHistory(); render(); toast(e.mode === 'new' ? 'Note saved.' : 'Saved.');
    } catch (err) { fail(err); }
  };
}

// ---------- review bindings

function bindReview() {
  if (!S.dupes.length) return;
  const p = S.dupes[S.reviewIndex];
  const ta = $('#m-body');
  const fit = () => { ta.style.height = 'auto'; ta.style.height = Math.max(130, ta.scrollHeight + 2) + 'px'; };
  ta.oninput = fit; fit();
  $('#r-skip').onclick = () => { S.reviewIndex = (S.reviewIndex + 1) % S.dupes.length; render(); };
  $('#r-both').onclick = async () => {
    try { await api.KeepBoth(p.keep.id, p.drop.id); await refresh(); render(); toast('Kept both. jot won’t suggest this pair again.'); } catch (e) { fail(e); }
  };
  $('#r-merge').onclick = async () => {
    try {
      const n = await api.Merge(p.keep.store, p.keep.id, p.drop.id, ta.value, parseTags($('#m-tags').value));
      await refresh(); S.sel = keyOf(n);
      if (!S.dupes.length) { S.view = 'library'; await loadHistory(); }
      render(); toast('Merged.' + (S.dupes.length ? ` ${S.dupes.length} left to review.` : ''));
    } catch (e) { fail(e); }
  };
  document.querySelectorAll('.ur').forEach(r => r.onclick = () => { S.reviewIndex = +r.dataset.i; render(); });
}

// ---------- keyboard

document.addEventListener('keydown', async ev => {
  const mod = ev.ctrlKey || ev.metaKey;
  if (S.view === 'onboarding') return;
  if (mod && ev.key.toLowerCase() === 'k') { ev.preventDefault(); if (S.view === 'settings') { S.view = 'library'; render(); } const q = $('#q'); if (q) { q.focus(); q.select(); } }
  else if (mod && ev.key.toLowerCase() === 'n') { ev.preventDefault(); if (!S.editing || await leaveEditor()) startNew(); }
  else if (mod && ev.key.toLowerCase() === 's' && S.editing) { ev.preventDefault(); $('#e-save').click(); }
  else if (mod && ev.key === ',') { ev.preventDefault(); S.view = 'settings'; render(); }
  else if (ev.key === 'Escape' && S.editing) { ev.preventDefault(); if (await leaveEditor()) render(); }
  else if (ev.key === 'Escape' && S.view === 'settings') { S.view = 'library'; render(); }
});

// Re-read the store when the window comes back, since agents write to it
// while jot sits in the background.
window.addEventListener('focus', async () => {
  if (S.view === 'onboarding' || S.editing || !S.state) return;
  try {
    const before = S.notes.map(n => n.id + n.updated).join() + S.dupes.length;
    await refresh();
    if (S.notes.map(n => n.id + n.updated).join() + S.dupes.length !== before) {
      if (S.sel && !selected()) S.sel = null;
      await loadHistory(); render();
    }
  } catch (_) { }
});

// ---------- boot

(async function boot() {
  const ok = await waitForBackend();
  if (!ok) { $('#app').innerHTML = '<div class="empty" style="margin-top:120px"><h3>jot couldn’t start</h3>The app backend didn’t load. Reinstall jot.</div>'; return; }
  try {
    await refresh();
    if (!S.state.onboarded) { S.view = 'onboarding'; }
    else { const v = visible(); S.sel = v[0] ? keyOf(v[0]) : null; await loadHistory(); }
    render();
  } catch (e) {
    $('#app').innerHTML = `<div class="empty" style="margin-top:120px"><h3>jot couldn’t open your notes</h3>${esc(e.message || e)}</div>`;
  }
})();
