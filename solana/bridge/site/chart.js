// bridge.gno.world — the value locked through the bridge over time: each token's lock on gno.land since the
// launch, a step line drawn in SVG from /api/<chain>/history (no library: the page loads this origin's scripts
// alone). A pointer over it, or a touch, reads the level at that time; the line ends at the newest reading.
import { api, el } from './common.js';

const NS = 'http://www.w3.org/2000/svg';
const UNIT = 1e6; // both tokens carry six decimals
const HOUR = 3600, DAY = 86400;
const compact = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 });
const full = new Intl.NumberFormat('en-US', { maximumFractionDigits: 6 });

function svgEl(tag, attrs = {}) {
  const e = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  return e;
}
function label(cls, x, y, anchor, text) {
  const t = svgEl('text', { class: cls, x, y, 'text-anchor': anchor });
  t.textContent = text;
  return t;
}
const tokens = (v) => v / UNIT;
const stamp = (t) => new Date(t * 1000).toISOString().replace('T', ' ').slice(0, 16) + ' UTC';

// niceStep is a round step that splits range into about n parts
function niceStep(range, n) {
  const raw = range / n, p = 10 ** Math.floor(Math.log10(raw)), m = raw / p;
  return (m <= 1 ? 1 : m <= 2 ? 2 : m <= 2.5 ? 2.5 : m <= 5 ? 5 : 10) * p;
}
// EDGE is how near either end of the plot a time label may sit (pixels): one nearer is dropped, half outside.
const EDGE = 16;
// timeStep is the axis's step from t0 to t1 over a width: the finest whose labels drawn — those EDGE pixels or more
// from either end — number a label a seventy-five pixels at the most (a phone's chart keeps three or four). The
// labels drawn are counted, not the span's steps: a span just over a day took half-day steps on a phone, and one of
// its two half-day marks fell at an end whenever the hour put it there, leaving one label.
function timeStep(t0, t1, width) {
  const most = Math.max(2, Math.floor(width / 75)), pad = ((t1 - t0) * EDGE) / width;
  const drawn = (s) => Math.floor((t1 - pad) / s) - Math.ceil((t0 + pad) / s) + 1;
  for (const s of [HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR, DAY, 2 * DAY, 7 * DAY, 14 * DAY]) if (drawn(s) <= most) return s;
  return 30 * DAY;
}
function timeLabel(t, step) {
  const d = new Date(t * 1000);
  if (step < DAY && (d.getUTCHours() || d.getUTCMinutes())) return d.toISOString().slice(11, 16);
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' });
}

// drawLockChart is one token's chart at a width: the grid with its amounts, the time axis, the step line and the
// area under it, an open mark where a point's time is an estimate, the end at the newest reading, the crosshair.
export function drawLockChart(tok, hist, width) {
  const H = width < 480 ? 170 : 210;
  const m = { l: 44, r: 12, t: 26, b: 22 };
  const w = width - m.l - m.r, h = H - m.t - m.b;
  const pts = tok.points.map((p) => ({ t: p[0], v: p[1], est: p[2] === 1 }));
  const t0 = Math.min(hist.from > 0 ? hist.from : pts[0].t, pts[0].t);
  const t1 = Math.max(hist.now, pts[pts.length - 1].t, t0 + 60);
  const vmax = Math.max(0, ...pts.map((p) => p.v));
  const ystep = vmax > 0 ? niceStep(tokens(vmax) * 1.1, 3) : 1;
  const ytop = ystep * Math.max(1, Math.ceil((tokens(vmax) * 1.1) / ystep));
  const x = (t) => m.l + ((t - t0) / (t1 - t0)) * w;
  const y = (v) => m.t + h - (tokens(v) / ytop) * h;
  const svg = svgEl('svg', {
    class: 'lchart', viewBox: `0 0 ${width} ${H}`, width, height: H, role: 'img',
    'aria-label': `${tok.symbol} locked on gno.land from ${stamp(t0)} to ${stamp(t1)}`,
    'data-token': tok.symbol, 'data-last': String(tok.lock), 'data-points': String(pts.length),
  });
  for (let v = 0; v <= ytop + ystep / 1e6; v += ystep) {
    const yy = y(v * UNIT);
    svg.append(svgEl('line', { class: 'grid', x1: m.l, x2: m.l + w, y1: yy, y2: yy }), label('ylab', m.l - 6, yy + 3.5, 'end', compact.format(v)));
  }
  const step = timeStep(t0, t1, w);
  for (let t = Math.ceil(t0 / step) * step; t <= t1; t += step) {
    const xx = x(t);
    if (xx < m.l + EDGE || xx > m.l + w - EDGE) continue; // a label half outside the plot is dropped
    svg.append(svgEl('line', { class: 'tick', x1: xx, x2: xx, y1: m.t + h, y2: m.t + h + 4 }), label('xlab', xx, H - 6, 'middle', timeLabel(t, step)));
  }
  let d = `M${x(pts[0].t).toFixed(1)},${y(pts[0].v).toFixed(1)}`;
  for (let i = 1; i < pts.length; i++) d += `H${x(pts[i].t).toFixed(1)}V${y(pts[i].v).toFixed(1)}`;
  d += `H${x(t1).toFixed(1)}`;
  svg.append(svgEl('path', { class: 'lchart-area', d: `${d}V${y(0).toFixed(1)}H${x(pts[0].t).toFixed(1)}Z` }));
  svg.append(svgEl('path', { class: 'lchart-line', d }));
  for (const p of pts) if (p.est) svg.append(svgEl('circle', { class: 'est', cx: x(p.t), cy: y(p.v), r: 3 }));
  svg.append(svgEl('circle', { class: 'end', cx: x(t1), cy: y(tok.lock), r: 3.5 }));

  // the crosshair: the level at the time under the pointer, in a box kept inside the plot
  const cross = svgEl('g', { class: 'cross hidden' });
  const vline = svgEl('line', { class: 'vline', y1: m.t, y2: m.t + h });
  const dot = svgEl('circle', { class: 'dot', r: 3.5 });
  const box = svgEl('rect', { class: 'tipbox', rx: 3, height: 18, y: 4 });
  const tip = label('tip', 0, 17, 'start', '');
  cross.append(vline, dot, box, tip);
  svg.append(cross);
  const levelAt = (t) => { let v = pts[0].v; for (const p of pts) { if (p.t <= t) v = p.v; else break; } return v; };
  const show = (ev) => {
    const r = svg.getBoundingClientRect();
    const px = ((ev.clientX - r.left) * width) / r.width;
    if (px < m.l || px > m.l + w) { cross.classList.add('hidden'); return; }
    const t = t0 + ((px - m.l) / w) * (t1 - t0), v = levelAt(t);
    vline.setAttribute('x1', px); vline.setAttribute('x2', px);
    dot.setAttribute('cx', px); dot.setAttribute('cy', y(v));
    tip.textContent = `${full.format(tokens(v))} ${tok.symbol} · ${stamp(t)}`;
    cross.classList.remove('hidden');
    const tw = tip.getComputedTextLength() + 10;
    const bx = Math.min(Math.max(px - tw / 2, m.l), m.l + w - tw);
    box.setAttribute('x', bx); box.setAttribute('width', tw);
    tip.setAttribute('x', bx + 5);
  };
  svg.addEventListener('pointermove', show);
  svg.addEventListener('pointerdown', show);
  svg.addEventListener('pointerleave', () => cross.classList.add('hidden'));
  return svg;
}

// mountLocked keeps a chain's charts in box: read now, every minute and when the tab comes back, redrawn at a new
// width. A token never locked gets a line, not an empty chart.
export function mountLocked(box, chain) {
  let hist = null;
  const draw = () => {
    if (!hist) return;
    const width = Math.max(260, Math.floor(box.clientWidth || 600));
    const parts = [];
    let est = false;
    for (const tok of hist.tokens) {
      const head = el('div', { class: 'lchart-head' },
        el('span', { class: 'lchart-val' }, `${full.format(tokens(tok.lock))} ${tok.symbol}`),
        el('span', { class: 'mute' }, 'locked on gno.land'));
      if (!tok.points.length || tok.points.every((p) => p[1] === 0)) {
        parts.push(el('div', { class: 'lchart-wrap', 'data-token': tok.symbol }, head, el('p', { class: 'line' }, `no ${tok.symbol} has been sent to ${chain.name} yet`)));
        continue;
      }
      est = est || tok.points.some((p) => p[2] === 1);
      parts.push(el('div', { class: 'lchart-wrap', 'data-token': tok.symbol }, head, drawLockChart(tok, hist, width)));
    }
    const notes = [];
    if (hist.from > 0) notes.push(`since the launch, ${stamp(hist.from)}`);
    if (hist.rebuilt && hist.sampled) notes.push(`before ${stamp(hist.sampled)} rebuilt from the chain's records`);
    if (est) notes.push('an open circle is a time the chain does not record, so it is estimated');
    parts.push(el('p', { class: 'line' }, notes.join(' · ')));
    box.replaceChildren(...parts);
  };
  const read = async () => {
    try {
      hist = await api('/api/' + chain.id + '/history');
      draw();
    } catch (e) {
      if (!hist) box.replaceChildren(el('p', { class: 'line' }, 'the history is not read yet: ' + e.message));
    }
  };
  read();
  setInterval(read, 60000);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) read(); });
  let resized;
  window.addEventListener('resize', () => { clearTimeout(resized); resized = setTimeout(draw, 150); });
}
