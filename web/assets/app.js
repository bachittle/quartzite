/* Quartzite frontend — vanilla, no build step, no dependencies.
 *
 * The frontend is deliberately thin. Anything that renders document content
 * happens inside a sandboxed viewer iframe, not here, so a malformed or hostile
 * document can only break its own pane.
 */

const $ = (sel, root = document) => root.querySelector(sel);

/* ---------- design languages ---------- */

// All dark by design. The shell is a canvas; expressiveness lives in viewers.
const THEMES = ['void', 'terminal', 'mission'];

// Tokens forwarded to sandboxed viewers. A viewer has no same-origin access and
// cannot read the shell's stylesheet, so the shell resolves the current values
// and hands them over. Any future viewer inherits theming without knowing which
// themes exist.
const FORWARDED_TOKENS = [
  '--bg', '--bg-raised', '--bg-inset', '--line', '--line-soft',
  '--fg', '--fg-dim', '--fg-faint', '--accent', '--accent-deep', '--warn',
  '--font-body', '--font-display', '--font-mono',
  '--fs-base', '--lh-read', '--radius', '--radius-sm', '--border',
  '--weight-heading', '--tracking-h', '--read-width',
];

function currentTheme() {
  return document.documentElement.getAttribute('data-theme') || 'void';
}

function themeTokens() {
  const cs = getComputedStyle(document.documentElement);
  const tokens = {};
  for (const name of FORWARDED_TOKENS) tokens[name] = cs.getPropertyValue(name).trim();
  return tokens;
}

function applyTheme(name, { persist = true } = {}) {
  if (!THEMES.includes(name)) name = 'void';
  document.documentElement.setAttribute('data-theme', name);
  if (persist) {
    try { localStorage.setItem('quartzite:theme', name); } catch (e) { /* private mode */ }
  }
  const picker = $('#theme');
  if (picker) picker.value = name;
  broadcastTheme();
}

// Push the palette into every embedded viewer currently on screen.
function broadcastTheme() {
  const msg = { type: 'quartzite:theme', name: currentTheme(), tokens: themeTokens() };
  document.querySelectorAll('iframe').forEach((f) => {
    try { f.contentWindow?.postMessage(msg, '*'); } catch (e) { /* not ready yet */ }
  });
}
const api = async (path, opts) => {
  const res = await fetch(path, opts);
  if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || res.statusText);
  return res.json();
};

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const fmtBytes = (n) => {
  if (!n) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB'];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), u.length - 1);
  return `${(n / 1024 ** i).toFixed(i ? 1 : 0)} ${u[i]}`;
};

const fmtDuration = (sec) => {
  if (sec < 60) return `${Math.floor(sec)}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ${Math.floor(sec % 60)}s`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ${Math.floor((sec % 3600) / 60)}m`;
  return `${Math.floor(sec / 86400)}d ${Math.floor((sec % 86400) / 3600)}h`;
};

const relTime = (iso) => {
  const d = (Date.now() - new Date(iso)) / 1000;
  if (d < 60) return 'just now';
  if (d < 3600) return `${Math.floor(d / 60)}m ago`;
  if (d < 86400) return `${Math.floor(d / 3600)}h ago`;
  return `${Math.floor(d / 86400)}d ago`;
};

const main = $('#main');
const latencyHistory = [];

/* ---------- routing ---------- */

function parseRoute() {
  const hash = location.hash.slice(1) || '/';
  const [path, query] = hash.split('?');
  return { path, params: new URLSearchParams(query || '') };
}

async function render() {
  const { path, params } = parseRoute();
  document.querySelectorAll('.nav-item').forEach((a) => a.classList.remove('active'));

  try {
    if (path === '/' || path === '') {
      markNav('home');
      await renderHome();
    } else if (path === '/system') {
      markNav('system');
      await renderSystem();
    } else if (path === '/docs') {
      const src = params.get('source');
      markNav(src === 'store' ? 'store' : src === 'vault' ? 'vault' : 'docs');
      await renderDocs(src);
    } else if (path === '/archive') {
      markNav('archive');
      await renderArchive();
    } else if (path === '/viewers') {
      markNav('viewers');
      await renderViewers();
    } else if (path === '/daily') {
      markNav('daily');
      await renderDaily(params);
    } else if (path.startsWith('/doc/')) {
      await renderDoc(path.slice(5));
    } else {
      main.innerHTML = `<div class="empty-state">Nothing here.</div>`;
    }
  } catch (err) {
    main.innerHTML = `<div class="empty-state">Error: ${esc(err.message)}</div>`;
  }
}

function markNav(route) {
  const el = document.querySelector(`.nav-item[data-route="${route}"]`);
  if (el) el.classList.add('active');
}

/* ---------- home ----------
 *
 * Home is about documents, not about the process serving them. Heap size and
 * GC counts are debugging facts; they belong on System, where looking at them
 * is the point. Nothing here rebuilds on a timer — the DOM is built once and
 * values are patched in place, because wholesale innerHTML replacement is what
 * made this screen flicker.
 */

async function renderHome() {
  main.innerHTML = `
    <div class="page-head">
      <h1>Quartzite</h1>
    </div>

    <div class="counts">
      <a class="count" href="#/docs">
        <span class="count-value" data-field="docs">–</span>
        <span class="count-label">documents</span>
      </a>
      <a class="count" href="#/docs?source=store">
        <span class="count-value" data-field="storeDocs">–</span>
        <span class="count-label">in the store</span>
      </a>
      <a class="count" href="#/docs?source=vault">
        <span class="count-value" data-field="vaultDocs">–</span>
        <span class="count-label">in the vault</span>
      </a>
      <a class="count" href="#/system">
        <span class="count-value" data-field="links">–</span>
        <span class="count-label">links · <span data-field="broken">–</span> unresolved</span>
      </a>
    </div>

    <div class="page-head section">
      <h1>Recently modified</h1>
    </div>
    <div class="doclist" id="recent"></div>
  `;
  await refreshHome();
}

// Patch values without touching structure. Only the recent list is replaced,
// and only when its contents actually changed.
let recentFingerprint = '';

async function refreshHome() {
  if (!$('[data-field="docs"]')) return;
  const s = await api('/api/stats');

  setField('docs', s.docs.toLocaleString());
  setField('storeDocs', s.storeDocs.toLocaleString());
  setField('vaultDocs', s.vaultDocs.toLocaleString());
  $('[data-field="vaultDocs"]').closest('.count').hidden = s.vaultDocs === 0;
  setField('links', s.links.toLocaleString());
  setField('broken', s.broken.toLocaleString());

  const recent = await api('/api/docs?limit=300');
  const rows = recent.docs
    .slice()
    .sort((a, b) => new Date(b.modified) - new Date(a.modified))
    .slice(0, 10);
  const fp = rows.map((d) => d.id + d.modified).join('|');
  if (fp !== recentFingerprint) {
    recentFingerprint = fp;
    const el = $('#recent');
    if (el) el.innerHTML = rows.map(docRow).join('');
  }
}

function setField(name, value) {
  const el = document.querySelector(`[data-field="${name}"]`);
  if (el && el.textContent !== String(value)) el.textContent = value;
}

/* ---------- system ----------
 *
 * Everything that used to crowd the home page. Here the instruments are the
 * content, so this is the one screen that may look like an instrument.
 */

async function renderSystem() {
  main.innerHTML = `
    <div class="page-head">
      <h1>System</h1>
      <p>Quartzite observing itself. Nothing here is about your documents.</p>
    </div>

    <div class="grid">
      <div class="card">
        <h3>Backend</h3>
        <div class="stat"><span class="stat-value" style="font-size:20px" data-field="backend">–</span></div>
        <div class="stat-sub" data-field="goVersion">–</div>
        <dl style="margin-top:10px">
          <div class="kv"><dt>dependencies</dt><dd class="good" data-field="deps">–</dd></div>
          <div class="kv"><dt>platform</dt><dd data-field="platform">–</dd></div>
          <div class="kv"><dt>cores</dt><dd data-field="numCpu">–</dd></div>
          <div class="kv"><dt>uptime</dt><dd data-field="uptime">–</dd></div>
        </dl>
      </div>

      <div class="card">
        <h3>Response time</h3>
        <div class="stat"><span class="stat-value" data-field="p50">–</span><span class="stat-unit">ms p50</span></div>
        <div class="stat-sub"><span data-field="requests">–</span> requests served</div>
        <div id="spark"></div>
        <dl style="margin-top:8px">
          <div class="kv"><dt>p95</dt><dd data-field="p95">–</dd></div>
          <div class="kv"><dt>p99</dt><dd data-field="p99">–</dd></div>
          <div class="kv"><dt>max</dt><dd data-field="max">–</dd></div>
        </dl>
      </div>

      <div class="card">
        <h3>Index</h3>
        <div class="stat"><span class="stat-value" data-field="docs">–</span><span class="stat-unit">documents</span></div>
        <div class="stat-sub" data-field="indexTiming">–</div>
        <dl style="margin-top:10px">
          <div class="kv"><dt>store (read-write)</dt><dd class="good" data-field="storeDocs">–</dd></div>
          <div class="kv"><dt>vault (read-only)</dt><dd data-field="vaultDocs">–</dd></div>
          <div class="kv"><dt>content</dt><dd data-field="bytes">–</dd></div>
        </dl>
      </div>

      <div class="card">
        <h3>Link graph</h3>
        <div class="stat"><span class="stat-value" data-field="links">–</span><span class="stat-unit">links</span></div>
        <div class="stat-sub"><span data-field="broken">–</span> unresolved</div>
        <dl style="margin-top:10px">
          <div class="kv"><dt>resolved</dt><dd class="good" data-field="resolved">–</dd></div>
          <div class="kv"><dt>broken</dt><dd data-field="brokenB">–</dd></div>
          <div class="kv"><dt>connected clients</dt><dd data-field="listeners">–</dd></div>
        </dl>
      </div>

      <div class="card">
        <h3>Memory</h3>
        <div class="stat"><span class="stat-value" data-field="heapMb">–</span><span class="stat-unit">MB heap</span></div>
        <div class="stat-sub"><span data-field="sysMb">–</span> MB reserved from OS</div>
        <dl style="margin-top:10px">
          <div class="kv"><dt>goroutines</dt><dd data-field="goroutines">–</dd></div>
          <div class="kv"><dt>GC cycles</dt><dd data-field="gcCount">–</dd></div>
        </dl>
      </div>

      <div class="card">
        <h3>Documents by kind</h3>
        <div class="bars" id="byKind"></div>
        <h3 style="margin-top:14px">By source</h3>
        <div class="bars" id="bySource"></div>
      </div>

      <div class="card">
        <h3>Formats</h3>
        <div class="bars" id="byFormat"></div>
      </div>

      <div class="card">
        <h3>Slowest routes (mean ms)</h3>
        <div class="bars" id="byRoute"></div>
      </div>
    </div>
  `;
  await refreshSystem();
}

async function refreshSystem() {
  if (!$('[data-field="goVersion"]')) return;
  const m = await api('/api/metrics');
  const p = m.process, ix = m.index;

  if (p.p50 || latencyHistory.length) latencyHistory.push(p.p50 || 0);
  if (latencyHistory.length > 60) latencyHistory.shift();

  setField('backend', p.backend);
  setField('goVersion', p.goVersion);
  setField('deps', p.deps);
  setField('platform', `${p.os}/${p.arch}`);
  setField('numCpu', p.numCpu);
  setField('uptime', fmtDuration(p.uptimeSec));

  setField('p50', p.p50 ?? 0);
  setField('requests', p.requests.toLocaleString());
  setField('p95', `${p.p95 ?? 0} ms`);
  setField('p99', `${p.p99 ?? 0} ms`);
  setField('max', `${p.max ?? 0} ms`);

  setField('docs', ix.docs);
  setField('indexTiming', `rebuilt in ${ix.indexTimeMs} ms · ${relTime(ix.indexedAt)}`);
  setField('storeDocs', ix.storeDocs);
  setField('vaultDocs', ix.vaultDocs);
  setField('bytes', fmtBytes(ix.bytes));

  setField('links', ix.links);
  setField('broken', ix.broken);
  setField('brokenB', ix.broken);
  setField('resolved', ix.links - ix.broken);
  setField('listeners', m.listeners);

  setField('heapMb', p.heapMb);
  setField('sysMb', p.sysMb);
  setField('goroutines', p.goroutines);
  setField('gcCount', p.gcCount);

  setHTML('spark', sparkline(latencyHistory));
  setHTML('byKind', Object.entries(ix.byKind || {}).sort((a, b) => b[1] - a[1])
    .map(([k, v]) => bar(k, v, ix.docs)).join('') || '<div class="panel empty">none</div>');
  setHTML('bySource', bar('store', ix.storeDocs, ix.docs, 'store') + bar('vault', ix.vaultDocs, ix.docs, 'vault'));
  setHTML('byFormat', Object.entries(ix.byFormat || {}).sort((a, b) => b[1] - a[1]).slice(0, 8)
    .map(([k, v]) => bar(k, v, ix.docs)).join('') || '<div class="panel empty">none</div>');

  const routes = Object.entries(p.routes || {}).sort((a, b) => b[1] - a[1]).slice(0, 8);
  setHTML('byRoute', routes.length
    ? routes.map(([k, v]) => bar(k.replace(/^(GET|POST|PUT|PATCH|DELETE) /, ''), v, routes[0][1], '', v.toFixed(1))).join('')
    : '<div class="panel empty">no traffic yet</div>');
}

function setHTML(id, html) {
  const el = document.getElementById(id);
  if (el && el.innerHTML !== html) el.innerHTML = html;
}

function kv(label, value, cls = '') {
  return `<div class="kv"><dt>${esc(label)}</dt><dd class="${cls}">${esc(value)}</dd></div>`;
}

function bar(label, value, total, cls = '', display) {
  const pct = total > 0 ? Math.max(2, (value / total) * 100) : 0;
  return `<div class="bar-row">
    <span class="bar-label" title="${esc(label)}">${esc(label)}</span>
    <span class="bar-track"><span class="bar-fill ${cls}" style="width:${pct.toFixed(1)}%"></span></span>
    <span class="bar-num">${esc(display ?? value)}</span>
  </div>`;
}

function sparkline(values) {
  if (values.length < 2) return `<svg class="spark" viewBox="0 0 100 30" preserveAspectRatio="none"></svg>`;
  // Floor the scale at 1ms. Auto-scaling to the data stretches sub-millisecond
  // latencies to full height, so the chart reads as a solid block — technically
  // correct, and a lie about how fast this actually is.
  const max = Math.max(...values, 1);
  const pts = values.map((v, i) => [(i / (values.length - 1)) * 100, 30 - (v / max) * 26]);
  const line = pts.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`).join(' ');
  const fill = `${line} L100,30 L0,30 Z`;
  return `<svg class="spark" viewBox="0 0 100 30" preserveAspectRatio="none">
    <path class="fill" d="${fill}"/><path d="${line}"/></svg>`;
}

/* ---------- document list ---------- */

function docRow(d) {
  return `<a class="doc-row" href="#/doc/${esc(d.id)}">
    <span class="doc-main">
      <div class="doc-title">${esc(d.title)}</div>
      ${d.excerpt ? `<div class="doc-excerpt">${esc(d.excerpt)}</div>` : ''}
    </span>
    <span class="doc-tags">
      ${d.kind !== 'file' ? `<span class="tag ${esc(d.kind)}">${esc(d.kind)}</span>` : ''}
      <span class="tag">${esc(d.format)}</span>
      <span class="tag ${esc(d.source)}">${esc(d.source)}</span>
    </span>
  </a>`;
}

async function renderDocs(source) {
  const qs = source ? `?source=${encodeURIComponent(source)}&limit=500` : '?limit=500';
  const data = await api('/api/docs' + qs);
  const label = source === 'store' ? 'Store' : source === 'vault' ? 'Vault' : 'All documents';
  const note = source === 'vault'
    ? 'Indexed read-only from the Obsidian vault. Quartzite never writes here.'
    : source === 'store'
      ? 'Quartzite\'s read-write corpus. Everything agents create lands here.'
      : 'Everything indexed, merged into one graph.';

  main.innerHTML = `
    <div class="page-head">
      <h1>${label}</h1>
      <p>${note} Showing ${data.count} of ${data.total}.</p>
    </div>
    <div class="doclist">${data.docs.map(docRow).join('') || '<div class="empty-state">No documents.</div>'}</div>
  `;
}

async function renderArchive() {
  const data = await api('/api/docs?archived=true&limit=500');
  main.innerHTML = `
    <div class="page-head">
      <h1>Archive</h1>
      <p>Hidden from lists and search. Nothing is deleted, and links still resolve
         to these documents. Archive state lives on the server, so it is the same
         in every browser.</p>
    </div>
    <div class="doclist">${
      data.docs.map((d) => `<div class="doc-row">
        <span class="doc-main">
          <a class="doc-title" href="#/doc/${esc(d.id)}">${esc(d.title)}</a>
          ${d.excerpt ? `<div class="doc-excerpt">${esc(d.excerpt)}</div>` : ''}
        </span>
        <span class="doc-tags">
          <span class="tag">${esc(d.format)}</span>
          <span class="tag ${esc(d.source)}">${esc(d.source)}</span>
          <button class="viewer-btn" data-unarchive="${esc(d.id)}">Restore</button>
        </span>
      </div>`).join('') || '<div class="empty-state">Nothing archived.</div>'
    }</div>
  `;
  main.querySelectorAll('[data-unarchive]').forEach((b) =>
    b.addEventListener('click', async () => {
      b.disabled = true; b.textContent = 'Restoring…';
      await setArchived(b.dataset.unarchive, false);
      await renderArchive();
      await updatePills();
    }));
}

// Archive state is server-side, so this is the only place it is written.
async function setArchived(id, archived) {
  return api(`/api/docs/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ archived }),
  });
}

async function renderViewers() {
  const viewers = await api('/api/viewers');
  main.innerHTML = `
    <div class="page-head">
      <h1>Viewers</h1>
      <p>A viewer is an ordinary Quartzite document. Adding one adds a format.</p>
    </div>
    <div class="notice" style="margin-bottom:14px">
      A document at <code>viewers/&lt;format&gt;.html</code> claims that format. It runs sandboxed
      and receives its target over <code>postMessage</code> — no backend change, no restart.
    </div>
    <div class="doclist">
      ${viewers.map((v) => `<a class="doc-row" href="#/doc/${esc(v.id)}">
        <span class="doc-main">
          <div class="doc-title">${esc(v.title)}</div>
          <div class="doc-excerpt">${esc(v.path)}</div>
        </span>
        <span class="doc-tags"><span class="tag folder">.${esc(v.format)}</span></span>
      </a>`).join('') || '<div class="empty-state">No viewers registered.</div>'}
    </div>
  `;
}

/* ---------- daily shards ----------
 *
 * Four views, one data contract. dailyData() returns the complete day→shard
 * map; every view is a pure rendering of it, so switching views costs a paint
 * and nothing else. The chosen view persists like the theme does.
 */

const DAILY_VIEWS = ['grid', 'zoom', 'strata', 'constellation'];

// Local-time YYYY-MM-DD. toISOString would shift the date across midnight UTC.
const dayStr = (d) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;

const DAILY_RE = /^daily\/(\d{4}-\d{2}-\d{2})$/;

async function dailyData() {
  const data = await api('/api/docs?source=store&limit=1000');
  const days = new Map(); // 'YYYY-MM-DD' -> doc
  for (const d of data.docs) {
    const m = DAILY_RE.exec(d.path || '');
    if (m) days.set(m[1], d);
  }
  return days;
}

async function renderDaily(params) {
  let view = params.get('view') || localStorage.getItem('quartzite:dailyView') || 'grid';
  if (!DAILY_VIEWS.includes(view)) view = 'grid';
  try { localStorage.setItem('quartzite:dailyView', view); } catch (e) { /* private mode */ }

  const days = await dailyData();
  const maxSize = Math.max(1, ...[...days.values()].map((d) => d.size || 0));
  const heat = (doc) => 35 + Math.round(55 * (doc.size || 0) / maxSize); // % of --store mixed in

  main.innerHTML = `
    <div class="page-head daily-head">
      <div>
        <h1>Daily</h1>
        <p>One shard per day, compiled from the daily note. ${days.size} so far.</p>
      </div>
      <div class="daily-switch">${DAILY_VIEWS.map((v) =>
        `<a class="${v === view ? 'on' : ''}" href="#/daily?view=${v}">${v}</a>`).join('')}
      </div>
    </div>
    <div id="dailyBody"></div>
  `;

  const body = $('#dailyBody');
  if (view === 'grid') dailyGrid(body, days, heat, params);
  else if (view === 'zoom') dailyZoom(body, days, heat, params);
  else if (view === 'strata') dailyStrata(body, days);
  else dailyConstellation(body, days, maxSize);
}

/* Month grid — the classic. Random access to a known date. */
function dailyGrid(el, days, heat, params) {
  const today = dayStr(new Date());
  const m = /^(\d{4})-(\d{2})$/.exec(params.get('m') || '') || [null, today.slice(0, 4), today.slice(5, 7)];
  const year = +m[1], month = +m[2]; // month 1-12

  const first = new Date(year, month - 1, 1);
  const start = new Date(first);
  start.setDate(1 - ((first.getDay() + 6) % 7)); // back to Monday

  let cells = '';
  const cur = new Date(start);
  for (let i = 0; i < 42; i++) {
    const key = dayStr(cur);
    const inMonth = cur.getMonth() === month - 1;
    const doc = days.get(key);
    const cls = ['cal-cell', inMonth ? '' : 'dim', key === today ? 'today' : ''].filter(Boolean).join(' ');
    cells += doc
      ? `<a class="${cls} has" href="#/doc/${esc(doc.id)}" style="--heat:${heat(doc)}%"
           title="${esc(key)} · ${fmtBytes(doc.size)}"><span>${cur.getDate()}</span></a>`
      : `<div class="${cls}"><span>${cur.getDate()}</span></div>`;
    cur.setDate(cur.getDate() + 1);
  }

  const monthName = first.toLocaleString('en', { month: 'long' });
  const nav = (y, mo) => `#/daily?view=grid&m=${y}-${String(mo).padStart(2, '0')}`;
  const prev = month === 1 ? nav(year - 1, 12) : nav(year, month - 1);
  const next = month === 12 ? nav(year + 1, 1) : nav(year, month + 1);

  el.innerHTML = `
    <div class="cal">
      <div class="cal-nav">
        <a class="btn" href="${prev}">‹</a>
        <span class="cal-title">${monthName} ${year}</span>
        <a class="btn" href="${next}">›</a>
        <a class="btn cal-today" href="#/daily?view=grid">today</a>
      </div>
      <div class="cal-grid">
        ${['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'].map((d) => `<div class="cal-dow">${d}</div>`).join('')}
        ${cells}
      </div>
    </div>
  `;
}

/* Two-level zoom — a year of mini-months. Answers "when was I active?";
 * clicking a month zooms into the grid, which answers "what's on the 14th?". */
function dailyZoom(el, days, heat, params) {
  const today = new Date();
  const year = +(params.get('y') || today.getFullYear());

  let months = '';
  for (let mo = 1; mo <= 12; mo++) {
    const first = new Date(year, mo - 1, 1);
    const start = new Date(first);
    start.setDate(1 - ((first.getDay() + 6) % 7));
    let cells = '';
    const cur = new Date(start);
    for (let i = 0; i < 42; i++) {
      const doc = cur.getMonth() === mo - 1 ? days.get(dayStr(cur)) : null;
      cells += doc
        ? `<i class="has" style="--heat:${heat(doc)}%" title="${esc(dayStr(cur))}"></i>`
        : `<i class="${cur.getMonth() === mo - 1 ? '' : 'void'}"></i>`;
      cur.setDate(cur.getDate() + 1);
    }
    const n = [...days.keys()].filter((k) => k.startsWith(`${year}-${String(mo).padStart(2, '0')}`)).length;
    months += `
      <a class="zoom-month ${n ? 'active' : ''}" href="#/daily?view=grid&m=${year}-${String(mo).padStart(2, '0')}">
        <span class="zoom-label">${first.toLocaleString('en', { month: 'short' })}${n ? ` · ${n}` : ''}</span>
        <span class="zoom-cells">${cells}</span>
      </a>`;
  }

  el.innerHTML = `
    <div class="cal-nav">
      <a class="btn" href="#/daily?view=zoom&y=${year - 1}">‹</a>
      <span class="cal-title">${year}</span>
      <a class="btn" href="#/daily?view=zoom&y=${year + 1}">›</a>
    </div>
    <div class="zoom-year">${months}</div>
  `;
}

/* Strata scroll — the core sample. Newest at the surface, shards as thick
 * glinting bands, quiet days compressed into seams. Sparse history reads as
 * geology instead of failure. */
function dailyStrata(el, days) {
  const today = new Date();
  const dates = [...days.keys()].sort();
  const floor = new Date(dates.length ? dates[0] : today);
  floor.setDate(floor.getDate() - 7); // a little bedrock under the oldest shard
  const minFloor = new Date(today); minFloor.setDate(minFloor.getDate() - 30);
  if (floor > minFloor) floor.setTime(minFloor.getTime());

  let bands = '', quiet = 0;
  const flush = () => {
    if (!quiet) return '';
    const h = Math.min(10 + quiet * 3, 64);
    const s = `<div class="strata-seam" style="height:${h}px"><span>${quiet} quiet day${quiet > 1 ? 's' : ''}</span></div>`;
    quiet = 0;
    return s;
  };
  const cur = new Date(today);
  while (cur >= floor) {
    const key = dayStr(cur);
    const doc = days.get(key);
    if (doc) {
      bands += flush() + `
        <a class="strata-band" href="#/doc/${esc(doc.id)}">
          <span class="strata-date">${key} · ${cur.toLocaleString('en', { weekday: 'short' })}</span>
          <span class="strata-meta">${fmtBytes(doc.size)} · ${relTime(doc.modified)}</span>
        </a>`;
    } else quiet++;
    cur.setDate(cur.getDate() - 1);
  }
  bands += flush();

  el.innerHTML = `
    <div class="strata">
      <div class="strata-surface">today</div>
      ${bands}
      <div class="strata-bedrock">bedrock</div>
    </div>
  `;
}

/* Constellation — days spiral out from the first shard; shards glow, sized by
 * weight. A visualization more than a navigator, and it knows it. */
function dailyConstellation(el, days, maxSize) {
  const today = new Date();
  const dates = [...days.keys()].sort();
  const start = new Date(dates.length ? dates[0] : dayStr(today));
  const span = Math.max(1, Math.round((today - start) / 86400000));

  const W = 720, H = 480, cx = W / 2, cy = H / 2;
  const maxR = Math.min(W, H) / 2 - 40;
  const GOLDEN = 2.399963;
  const pos = (i) => {
    const t = span === 0 ? 0 : i / span;
    const r = 24 + t * (maxR - 24);
    return [cx + r * Math.cos(i * GOLDEN), cy + r * Math.sin(i * GOLDEN)];
  };

  let ticks = '', nodes = '', wire = [];
  const cur = new Date(start);
  for (let i = 0; i <= span; i++) {
    const key = dayStr(cur);
    const doc = days.get(key);
    const [x, y] = pos(i);
    if (doc) {
      const r = 5 + 9 * ((doc.size || 0) / maxSize);
      wire.push([x, y]);
      nodes += `
        <a href="#/doc/${esc(doc.id)}">
          <circle cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="${r.toFixed(1)}" class="constel-node">
            <title>${esc(key)} · ${fmtBytes(doc.size)}</title>
          </circle>
          <text x="${(x + r + 5).toFixed(1)}" y="${(y + 3).toFixed(1)}" class="constel-label">${esc(key)}</text>
        </a>`;
    } else if (i % 7 === 0) {
      ticks += `<circle cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="1.2" class="constel-tick"/>`;
    }
    cur.setDate(cur.getDate() + 1);
  }
  const [tx, ty] = pos(span);
  const wirePath = wire.length > 1
    ? `<path d="M ${wire.map(([x, y]) => `${x.toFixed(1)} ${y.toFixed(1)}`).join(' L ')}" class="constel-wire"/>` : '';

  el.innerHTML = `
    <div class="constel">
      <svg viewBox="0 0 ${W} ${H}" role="img" aria-label="Daily shards as a constellation">
        ${ticks}${wirePath}${nodes}
        <circle cx="${tx.toFixed(1)}" cy="${ty.toFixed(1)}" r="10" class="constel-today"><title>today</title></circle>
      </svg>
      <p class="constel-note">time spirals outward from the first shard · weekly ticks · today is the open ring</p>
    </div>
  `;
}

/* ---------- reader ---------- */

async function renderDoc(id) {
  const data = await api(`/api/docs/${encodeURIComponent(id)}`);
  const d = data.doc;
  // remembered for the save relay: a document may rewrite its own files
  window.__currentDoc = { id: d.id, writable: !!data.writable };

  const nativeHTML = ['html', 'htm'].includes((d.format || '').toLowerCase());
  const useViewer = !!data.viewer && !nativeHTML;
  const canEmbed = nativeHTML || d.bundled || useViewer;

  let viewport;
  if (canEmbed) {
    const src = useViewer ? data.viewer.url : data.serveUrl;
    const via = useViewer
      ? `rendered by <a href="#/doc/${esc(data.viewer.id)}">${esc(data.viewer.title)}</a>`
      : `served from ${esc(d.kind)}`;
    viewport = `
      <div class="viewport ${useViewer ? 'text' : ''}">
        <div class="viewer-bar">
          <span>${via}</span>
          <span class="viewer-actions">
            <a href="${esc(data.serveUrl)}" target="_blank" rel="noopener">open raw ↗</a>
            <button class="viewer-btn" data-fullscreen title="Fullscreen — Esc to exit">⤢ Fullscreen</button>
          </span>
        </div>
        <iframe id="frame" src="${esc(src)}" sandbox="allow-scripts allow-forms allow-popups allow-pointer-lock"
                title="${esc(d.title)}"></iframe>
      </div>`;
  } else if (data.content != null) {
    viewport = `
      <div class="viewport">
        <div class="viewer-bar">
          <span>raw ${esc(d.format)}</span>
          <span class="viewer-actions">
            <button class="viewer-btn" data-fullscreen title="Fullscreen — Esc to exit">⤢ Fullscreen</button>
          </span>
        </div>
        <pre class="raw">${esc(data.content)}</pre>
      </div>`;
  } else {
    viewport = `<div class="viewport"><div class="empty-state">
      No viewer for <code>.${esc(d.format)}</code>.
      Write one at <code>viewers/${esc(d.format)}.html</code> and it appears here.
    </div></div>`;
  }

  main.innerHTML = `
    <div class="reader">
      <div>
        <div class="doc-head">
          <h1>${esc(d.title)}</h1>
          <div class="doc-meta">
            <span class="tag ${esc(d.source)}">${esc(d.source)}</span>
            <span class="tag ${d.kind !== 'file' ? esc(d.kind) : ''}">${esc(d.kind)}</span>
            <span class="tag">${esc(d.format)}</span>
            <span class="tag">${fmtBytes(d.size)}</span>
            <span class="tag">${relTime(d.modified)}</span>
            ${data.writable ? '' : '<span class="tag vault">read-only</span>'}
            ${d.archived ? '<span class="tag warn-tag">archived</span>' : ''}
            <button class="viewer-btn" data-archive-toggle
                    style="margin-left:auto">${d.archived ? 'Restore' : 'Archive'}</button>
          </div>
        </div>
        ${viewport}
        <div class="notice" style="margin-top:10px">
          <code>${esc(d.path)}</code> · id <code>${esc(d.id)}</code>
        </div>
      </div>

      <aside class="side-panel">
        <div class="panel">
          <h3>Backlinks (${data.backlinks?.length || 0})</h3>
          ${data.backlinks?.length
            ? `<ul>${data.backlinks.map((b) => `<li><a href="#/doc/${esc(b.id)}">${esc(b.title)}</a></li>`).join('')}</ul>`
            : '<div class="empty">Nothing links here yet.</div>'}
        </div>
        <div class="panel">
          <h3>Links out (${data.links?.length || 0})</h3>
          ${data.links?.length
            ? `<ul>${data.links.map((l) => l.resolved
                ? `<li><a href="#/doc/${esc(l.id)}">${esc(l.title)}</a></li>`
                : `<li class="broken">${esc(l.title)} · unresolved</li>`).join('')}</ul>`
            : '<div class="empty">No outbound links.</div>'}
        </div>
        ${d.tags?.length ? `<div class="panel"><h3>Tags</h3>
          <div class="doc-tags" style="flex-wrap:wrap">${d.tags.map((t) => `<span class="tag">${esc(t)}</span>`).join('')}</div>
        </div>` : ''}
      </aside>
    </div>
  `;

  const arBtn = document.querySelector('[data-archive-toggle]');
  if (arBtn) arBtn.addEventListener('click', async () => {
    arBtn.disabled = true;
    await setArchived(d.id, !d.archived);
    await render();
    await updatePills();
  });

  const fsBtn = document.querySelector('[data-fullscreen]');
  if (fsBtn) fsBtn.addEventListener('click', () => toggleFullscreen());

  // Hand the document to its viewer once the sandboxed frame says it is ready.
  if (useViewer) {
    const frame = $('#frame');
    const payload = {
      type: 'quartzite:doc',
      doc: d,
      content: data.content ?? '',
      links: data.links || [],
    };
    const send = () => {
      frame.contentWindow?.postMessage(payload, '*');
      broadcastTheme();
    };
    window.addEventListener('message', (ev) => {
      if (ev.source === frame.contentWindow && ev.data?.type === 'quartzite:ready') send();
    });
    frame.addEventListener('load', () => setTimeout(send, 40));
  } else if (canEmbed) {
    // Native HTML documents and bundles get the palette too, if they want it.
    const frame = $('#frame');
    frame?.addEventListener('load', () => setTimeout(broadcastTheme, 40));
  }

  // Sandboxed viewers cannot navigate the shell themselves, so they ask.
  if (!window.__navWired) {
    window.__navWired = true;
    window.addEventListener('message', (ev) => {
      if (ev.data?.type === 'quartzite:navigate' && ev.data.id) {
        location.hash = `#/doc/${ev.data.id}`;
      }
      // Self-persistence: a document may rewrite files in ITS OWN bundle —
      // never another document's. The store is agent-writable by design;
      // this only narrows that door to "the doc currently on screen".
      if (ev.data?.type === 'quartzite:save' && ev.data.reqId && ev.source) {
        const src = ev.source, reqId = ev.data.reqId;
        const cur = window.__currentDoc;
        const file = String(ev.data.file || '');
        const content = String(ev.data.content ?? '');
        const bad =
          !cur ? 'no document open' :
          !cur.writable ? 'document is not writable' :
          !file || file.startsWith('/') || file.includes('..') ? 'bad file path' :
          content.length > 256 * 1024 ? 'content too large' : null;
        if (bad) {
          src.postMessage({ type: 'quartzite:save:result', reqId, error: bad }, '*');
        } else {
          fetch(`/api/docs/${encodeURIComponent(cur.id)}`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ content, file }),
          }).then(async (r) => {
            const body = await r.json().catch(() => ({ error: r.statusText }));
            if (!r.ok) throw new Error(body.error || r.statusText);
            src.postMessage({ type: 'quartzite:save:result', reqId, ok: true }, '*');
          }).catch((e) => {
            src.postMessage({ type: 'quartzite:save:result', reqId, error: String(e.message || e) }, '*');
          });
        }
      }
    });
  }
}

/* ---------- fullscreen ----------
 *
 * A class toggle, deliberately not a re-render. Rebuilding the reader would
 * recreate the iframe, and a running artifact — a simulation, a scrolled
 * document, anything holding state — would silently restart. Same element,
 * same viewer, different geometry.
 */

function fullscreenEl() { return document.querySelector('.viewport.fullscreen'); }

function toggleFullscreen(force) {
  const vp = document.querySelector('.viewport');
  if (!vp) return;
  const want = force === undefined ? !vp.classList.contains('fullscreen') : force;

  vp.classList.toggle('fullscreen', want);
  document.body.classList.toggle('fullscreen-active', want);

  const btn = vp.querySelector('[data-fullscreen]');
  if (btn) btn.textContent = want ? '⤡ Exit' : '⤢ Fullscreen';

  // Re-send the palette: a viewer that lays out on resize gets a nudge, and
  // one that mounted while hidden gets a second chance.
  broadcastTheme();
}

document.addEventListener('keydown', (e) => {
  if (e.key !== 'Escape') return;
  if (!fullscreenEl()) return;
  e.preventDefault();
  e.stopPropagation();
  toggleFullscreen(false);
}, true); // capture: exit fullscreen before any other Escape handler runs

// Leaving the document also leaves fullscreen — otherwise the class would
// survive onto whatever renders next.
window.addEventListener('hashchange', () => {
  if (fullscreenEl()) toggleFullscreen(false);
});

/* ---------- search ---------- */

let searchTimer, searchSel = -1;
const searchInput = $('#search');
const resultsEl = $('#results');

searchInput.addEventListener('input', () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(runSearch, 130);
});

searchInput.addEventListener('keydown', (e) => {
  const items = [...resultsEl.querySelectorAll('.result')];
  if (e.key === 'Escape') { hideResults(); searchInput.blur(); return; }
  if (!items.length) return;
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault();
    searchSel = (searchSel + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
    items.forEach((el, i) => el.classList.toggle('sel', i === searchSel));
    items[searchSel].scrollIntoView({ block: 'nearest' });
  } else if (e.key === 'Enter' && searchSel >= 0) {
    e.preventDefault();
    location.hash = items[searchSel].getAttribute('href');
    hideResults();
  }
});

document.addEventListener('click', (e) => {
  if (!e.target.closest('.search-wrap')) hideResults();
});

document.addEventListener('keydown', (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key === 'k') { e.preventDefault(); searchInput.focus(); searchInput.select(); }
});

function hideResults() { resultsEl.hidden = true; searchSel = -1; }

async function runSearch() {
  const q = searchInput.value.trim();
  if (!q) return hideResults();
  try {
    const data = await api(`/api/search?q=${encodeURIComponent(q)}&limit=12`);
    if (!data.hits.length) {
      resultsEl.innerHTML = `<div class="result"><span class="result-meta">no matches</span></div>`;
    } else {
      resultsEl.innerHTML = data.hits.map((h) => `
        <a class="result" href="#/doc/${esc(h.id)}">
          <div class="result-title">${esc(h.title)}</div>
          <div class="result-meta">${esc(h.source)} · ${esc(h.format)} · ${esc(h.why)}</div>
        </a>`).join('');
      resultsEl.querySelectorAll('.result').forEach((el) =>
        el.addEventListener('click', hideResults));
    }
    resultsEl.hidden = false;
    searchSel = -1;
  } catch { hideResults(); }
}

/* ---------- live updates ---------- */

// Connection state is plumbing. A permanent green "live" badge is chrome that
// tells the user nothing — it is only worth screen space when it is bad news.
const liveEl = $('#live');
function setLive(state, text) {
  if (!liveEl) return;
  liveEl.className = `live ${state}`;
  liveEl.hidden = (state === 'on');
  const t = $('.live-text', liveEl);
  if (t) t.textContent = text;
}

function connect() {
  const es = new EventSource('/api/events');
  es.addEventListener('hello', () => setLive('on', 'connected'));
  es.onmessage = (ev) => {
    let msg; try { msg = JSON.parse(ev.data); } catch { return; }
    updatePills();
    // Patch in place. Re-rendering the whole view on every server event is
    // what made the page flicker — the user is looking at this screen.
    const route = parseRoute().path;
    if (route === '/') refreshHome().catch(() => {});
    else if (route === '/system') refreshSystem().catch(() => {});
    else if (msg.type && msg.type.startsWith('doc:') &&
             (route === '/docs' || route === '/viewers')) render();
  };
  es.onerror = () => {
    setLive('off', 'disconnected — reconnecting');
    es.close();
    setTimeout(connect, 3000);
  };
}

async function updatePills() {
  try {
    const s = await api('/api/stats');
    $('#pill-store').textContent = s.storeDocs;
    const pv = $('#pill-vault');
    pv.textContent = s.vaultDocs;
    pv.parentElement.hidden = s.vaultDocs === 0; // same rule as the archive

    const pa = $('#pill-archive');
    if (pa) {
      pa.textContent = s.archived;
      pa.parentElement.hidden = s.archived === 0; // no empty section in the nav
    }
  } catch { /* transient */ }
}

$('#theme').addEventListener('change', (e) => applyTheme(e.target.value));

// ⌥T cycles design languages, for flipping between them while comparing.
document.addEventListener('keydown', (e) => {
  if (e.altKey && (e.key === 't' || e.key === 'T' || e.code === 'KeyT')) {
    e.preventDefault();
    applyTheme(THEMES[(THEMES.indexOf(currentTheme()) + 1) % THEMES.length]);
  }
});

$('#reindex').addEventListener('click', async () => {
  const btn = $('#reindex');
  btn.disabled = true; btn.textContent = 'Reindexing…';
  try {
    await api('/api/reindex', { method: 'POST' });
    const route = parseRoute().path;
    if (route === '/') await refreshHome();
    else if (route === '/system') await refreshSystem();
    else await render();
    await updatePills();
  }
  finally { btn.disabled = false; btn.textContent = 'Reindex'; }
});

window.addEventListener('hashchange', render);

// Only System has values that move on their own. Home changes when documents
// change, and SSE already tells us about that.
setInterval(() => {
  if (parseRoute().path === '/system') refreshSystem().catch(() => {});
}, 5000);

// The pre-paint script in <head> already set the attribute; sync the picker and
// prime any viewers without re-persisting what we just read.
applyTheme(currentTheme(), { persist: false });

render();
updatePills();
connect();
