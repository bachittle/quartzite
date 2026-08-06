/* welcome.js — proof that a folder document is a real, running program.
 *
 * This file is fetched relative to the document's own URL (/d/{id}/welcome.js),
 * which is the entire reason bundles get a URL prefix instead of being inlined.
 */

/* ---------- crystalline lattice backdrop ---------- */

const canvas = document.getElementById('lattice');
const ctx = canvas.getContext('2d');

let nodes = [];
let w = 0, h = 0, dpr = 1;

function resize() {
  dpr = Math.min(window.devicePixelRatio || 1, 2);
  w = canvas.clientWidth;
  h = canvas.clientHeight;
  canvas.width = w * dpr;
  canvas.height = h * dpr;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

  // Density scales with area so a wide window is not overwhelmed.
  const count = Math.max(28, Math.min(80, Math.round((w * h) / 22000)));
  nodes = Array.from({ length: count }, () => ({
    x: Math.random() * w,
    y: Math.random() * h,
    vx: (Math.random() - 0.5) * 0.16,
    vy: (Math.random() - 0.5) * 0.16,
    r: Math.random() * 1.4 + 0.6,
  }));
}

function accent() {
  return getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#7dd3fc';
}

function hexToRGB(hex) {
  const m = hex.replace('#', '');
  const n = m.length === 3 ? m.split('').map((c) => c + c).join('') : m;
  const v = parseInt(n, 16);
  return [(v >> 16) & 255, (v >> 8) & 255, v & 255];
}

let rgb = [125, 211, 252];

function frame() {
  ctx.clearRect(0, 0, w, h);

  for (const n of nodes) {
    n.x += n.vx;
    n.y += n.vy;
    if (n.x < 0 || n.x > w) n.vx *= -1;
    if (n.y < 0 || n.y > h) n.vy *= -1;
  }

  const [r, g, b] = rgb;
  const maxDist = 132;

  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      const dx = nodes[i].x - nodes[j].x;
      const dy = nodes[i].y - nodes[j].y;
      const d = Math.hypot(dx, dy);
      if (d > maxDist) continue;
      ctx.strokeStyle = `rgba(${r},${g},${b},${(1 - d / maxDist) * 0.28})`;
      ctx.lineWidth = 0.7;
      ctx.beginPath();
      ctx.moveTo(nodes[i].x, nodes[i].y);
      ctx.lineTo(nodes[j].x, nodes[j].y);
      ctx.stroke();
    }
  }

  ctx.fillStyle = `rgba(${r},${g},${b},0.55)`;
  for (const n of nodes) {
    ctx.beginPath();
    ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
    ctx.fill();
  }

  requestAnimationFrame(frame);
}

window.addEventListener('resize', resize);
resize();
rgb = hexToRGB(accent());
requestAnimationFrame(frame);

/* ---------- tabs ---------- */

document.querySelectorAll('.tab').forEach((tab) => {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('active', t === tab));
    document.querySelectorAll('.pane').forEach((p) =>
      p.classList.toggle('active', p.dataset.pane === tab.dataset.tab));
  });
});

/* ---------- counter ---------- */

const countEl = document.getElementById('count');
const hintEl = document.getElementById('hint');
let count = 0;
let interacted = false;

function bump(delta) {
  count += delta;
  countEl.textContent = count;
  if (!interacted) {
    interacted = true;
    hintEl.textContent = 'State held in a sandboxed frame, isolated from the shell.';
  }
  countEl.animate(
    [{ transform: 'scale(1)' }, { transform: 'scale(1.13)' }, { transform: 'scale(1)' }],
    { duration: 180, easing: 'ease-out' }
  );
}

document.getElementById('plus').addEventListener('click', () => bump(1));
document.getElementById('minus').addEventListener('click', () => bump(-1));

/* ---------- optional: follow the shell's design language ----------
 *
 * A bundle is arbitrary third-party content and owes the shell nothing. But if
 * it chooses to listen, theming costs it about ten lines. */

const THEME_MAP = {
  '--bg': '--bg', '--bg-raised': '--panel', '--bg-inset': '--inset',
  '--fg': '--fg', '--fg-dim': '--dim', '--fg-faint': '--faint',
  '--line': '--line', '--accent': '--accent', '--accent-deep': '--accent2',
};

window.addEventListener('message', (ev) => {
  if (ev.data?.type !== 'quartzite:theme') return;
  const tokens = ev.data.tokens || {};
  for (const [from, to] of Object.entries(THEME_MAP)) {
    if (tokens[from]) document.documentElement.style.setProperty(to, tokens[from]);
  }
  rgb = hexToRGB(accent()); // the lattice recolours on the next frame
});

/* ---------- footer stamp ---------- */

document.getElementById('stamp').textContent =
  `rendered ${new Date().toLocaleTimeString()} · ${nodes.length} lattice nodes`;
