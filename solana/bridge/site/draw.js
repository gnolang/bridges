// bridge.gno.world — the drawing primitives the machine, the walkthrough and How it works share: SVG nouns, not
// text in boxes (a machine is a box with a vent and a lamp, a lock is a vault, a block has a header strip, a proof
// is a tree of rectangles, a signature is a seal with a four-point star). Colour is the role's, from the sheet's
// tokens: teal a cryptographic check, orange the trusted signers, violet a brake or a limit, grey a carrier.

export const NS = 'http://www.w3.org/2000/svg';

export function s(tag, attrs = {}, ...kids) {
  const e = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) if (v !== null && v !== undefined && v !== false) e.setAttribute(k, v);
  for (const k of kids.flat()) if (k !== null && k !== undefined && k !== false) e.append(k.nodeType ? k : document.createTextNode(String(k)));
  return e;
}

export const g = (attrs, ...kids) => s('g', attrs, ...kids);

export function text(x, y, str, cls = 'lbl', anchor = 'middle') {
  return s('text', { x, y, class: cls, 'text-anchor': anchor }, str);
}

// fit shrinks a text in place until it is no wider than w (never below 8 px), then shortens it with an ellipsis:
// a label never spills its node. The text must be in the document.
export function fit(t, w) {
  if (!t.isConnected || !t.getComputedTextLength) return t;
  let size = parseFloat(getComputedStyle(t).fontSize) || 11;
  while (t.getComputedTextLength() > w && size > 8) { size -= 0.5; t.style.fontSize = size + 'px'; }
  let str = t.textContent;
  while (t.getComputedTextLength() > w && str.length > 2) { str = str.slice(0, -2) + '…'; t.textContent = str; }
  return t;
}

// a four-point star (long rays, short diagonals): the seal's mark
export function star4(cx, cy, r) {
  const k = r * 0.28;
  return s('path', { class: 'star', d: `M${cx},${cy - r} L${cx + k},${cy - k} L${cx + r},${cy} L${cx + k},${cy + k} L${cx},${cy + r} L${cx - k},${cy + k} L${cx - r},${cy} L${cx - k},${cy - k} Z` });
}

// a seal: a round stamp with a four-point star — a signature, a check passed
export function seal(cx, cy, r, cls = 'check') {
  return g({ class: 'seal ' + cls }, s('circle', { cx, cy, r }), star4(cx, cy, r * 0.62));
}

// a machine seen from the front-right: its front, top and side, a vent and a slot for its lamp
export function server(x, y, w, h, cls = 'carrier') {
  const d = Math.max(4, Math.min(9, h * 0.22));
  return g({ class: 'srv ' + cls },
    s('path', { class: 'top', d: `M${x},${y + d}l${d},${-d}h${w - d}l${-d},${d}z` }),
    s('path', { class: 'side', d: `M${x + w - d},${y + d}l${d},${-d}v${h - d}l${-d},${d}z` }),
    s('rect', { class: 'front', x, y: y + d, width: w - d, height: h - d, rx: 2 }),
    s('path', { class: 'vent', d: `M${x + 5},${y + d + (h - d) * 0.32}h${w - d - 10}M${x + 5},${y + d + (h - d) * 0.5}h${w - d - 10}` }));
}

// a lock: a vault with its dial and handle
export function vault(cx, cy, w, h) {
  const x = cx - w / 2, y = cy - h / 2, r = Math.min(w, h) * 0.22;
  return g({ class: 'vault' },
    s('rect', { class: 'body', x, y, width: w, height: h, rx: 3 }),
    s('rect', { class: 'door', x: x + 3, y: y + 3, width: w - 6, height: h - 6, rx: 2 }),
    s('circle', { class: 'dial', cx: cx - w * 0.1, cy, r }),
    s('path', { class: 'spokes', d: `M${cx - w * 0.1 - r},${cy}h${2 * r}M${cx - w * 0.1},${cy - r}v${2 * r}` }),
    s('rect', { class: 'handle', x: cx + w * 0.22, y: cy - h * 0.18, width: 3, height: h * 0.36, rx: 1.5 }));
}

// a coin, with its token's initial
export function coin(cx, cy, r, sym = '') {
  return g({ class: 'coin' }, s('circle', { cx, cy, r }), sym ? s('text', { x: cx, y: cy + r * 0.36, 'text-anchor': 'middle', class: 'coin-t', 'font-size': r }, sym[0]) : null);
}

// a mint: a stack of coins, the newest on top
export function coinStack(cx, cy, w, n = 4) {
  const rx = w / 2, ry = w * 0.16, out = g({ class: 'stack' });
  for (let i = n - 1; i >= 0; i--) {
    const y = cy + i * ry * 1.1;
    out.append(s('path', { class: 'rim', d: `M${cx - rx},${y}v${ry * 0.9}a${rx},${ry} 0 0 0 ${2 * rx},0v${-ry * 0.9}` }), s('ellipse', { class: 'face', cx, cy: y, rx, ry }));
  }
  return out;
}

// a message: an envelope
export function envelope(cx, cy, w, h) {
  const x = cx - w / 2, y = cy - h / 2;
  return g({ class: 'env' }, s('rect', { x, y, width: w, height: h, rx: 1.5 }), s('path', { d: `M${x},${y}L${cx},${cy + h * 0.12}L${x + w},${y}` }));
}

// a block: a body with its header strip on top
export function block(x, y, w, h, cls = 'blk') {
  return g({ class: cls }, s('rect', { class: 'body', x, y, width: w, height: h, rx: 2 }), s('rect', { class: 'head', x, y, width: w, height: Math.max(4, h * 0.24), rx: 2 }));
}

// a person; a person holding a key (the governor)
export function person(cx, cy, size, cls = 'person') {
  const r = size * 0.22;
  return g({ class: cls }, s('circle', { cx, cy: cy - size * 0.28, r }), s('path', { d: `M${cx - size * 0.38},${cy + size * 0.42}q0,${-size * 0.52} ${size * 0.38},${-size * 0.52}q${size * 0.38},0 ${size * 0.38},${size * 0.52}z` }));
}

export function keyIcon(cx, cy, size) {
  const r = size * 0.22;
  return g({ class: 'key' }, s('circle', { cx: cx - size * 0.3, cy, r }), s('path', { d: `M${cx - size * 0.3 + r},${cy}h${size * 0.62}v${size * 0.2}M${cx + size * 0.18},${cy}v${size * 0.16}` }));
}

// a guardian's flag on its pole: up (a flag stands) or down
export function flag(x, y, h, up) {
  return g({ class: 'flag' + (up ? ' up' : '') }, s('path', { class: 'pole', d: `M${x},${y}v${h}` }), s('path', { class: 'cloth', d: up ? `M${x},${y}h${h * 0.5}l${-h * 0.12},${h * 0.16}l${h * 0.12},${h * 0.16}h${-h * 0.5}z` : `M${x},${y + h * 0.66}h${h * 0.36}v${h * 0.22}h${-h * 0.36}z` }));
}

// a watch key: an eye
export function eye(cx, cy, w) {
  return g({ class: 'eye' }, s('path', { d: `M${cx - w / 2},${cy}q${w / 2},${-w * 0.5} ${w},0q${-w / 2},${w * 0.5} ${-w},0z` }), s('circle', { cx, cy, r: w * 0.16 }));
}

// a lamp: a machine's state — vouching (lit), up, down — read with its word, never by colour alone
export function lamp(cx, cy, r) {
  const el = g({ class: 'lamp', 'data-state': 'unknown' }, s('circle', { class: 'glow', cx, cy, r: r * 1.9 }), s('circle', { class: 'bulb', cx, cy, r }));
  el.set = (state) => el.setAttribute('data-state', state);
  return el;
}

// a gauge: a tank that fills from the bottom — a meter's level against its ceiling — with an optional mark (a
// recipient's quarter). set(frac) moves the level.
export function gauge(x, y, w, h, cls = 'brake', mark) {
  const fill = s('rect', { class: 'fill', x: x + 2, width: w - 4, y: y + h - 2, height: 0, rx: 1.5 });
  const el = g({ class: 'gauge ' + cls }, s('rect', { class: 'track', x, y, width: w, height: h, rx: 3 }), fill,
    mark ? s('path', { class: 'mark', d: `M${x - 3},${y + h * (1 - mark)}h${w + 6}` }) : null);
  el.set = (f) => {
    f = Math.max(0, Math.min(1, Number.isFinite(f) ? f : 0));
    const hh = (h - 4) * f;
    fill.setAttribute('y', y + h - 2 - hh);
    fill.setAttribute('height', hh);
    el.setAttribute('data-level', f.toFixed(4));
  };
  el.set(0);
  return el;
}

// the pausable gate: two posts and bars across — lifted while deliveries pass, down (violet) while a flag stands
export function gate(x, y, w, h) {
  const bars = g({ class: 'bars' });
  const rows = h >= 14 ? 3 : 2, bh = Math.max(1.5, (h - 4) / rows - 1);
  for (let i = 0; i < rows; i++) bars.append(s('rect', { x: x + 6, y: y + 2 + i * (h - 4) / rows, width: w - 12, height: bh, rx: 1 }));
  const el = g({ class: 'gate', 'data-state': 'open' }, s('rect', { class: 'post', x, y: y - 3, width: 5, height: h + 6, rx: 1 }), s('rect', { class: 'post', x: x + w - 5, y: y - 3, width: 5, height: h + 6, rx: 1 }), bars);
  el.set = (state) => el.setAttribute('data-state', state);
  return el;
}

// a results proof as the light client checks it: a tree of rectangles over the block's results — the transfer's
// leaf, the path to the root (teal) and the sibling hashes the proof carries (outlined); total leaves, the leaf at
// index. A block of one result is its own root.
export function merkle(x, y, w, h, total, index) {
  total = Math.max(1, Math.min(total || 1, 16));
  index = Math.max(0, Math.min(index || 0, total - 1));
  const levels = [];
  let n = total;
  for (;;) { levels.push(n); if (n === 1) break; n = Math.ceil(n / 2); }
  const out = g({ class: 'merkle' });
  const rowH = h / levels.length;
  const onPath = levels.map((_, l) => Math.floor(index / 2 ** l));
  levels.forEach((count, l) => {
    const cy = y + h - rowH * (l + 0.5), bw = Math.min(w / count - 3, 18), bh = Math.min(rowH * 0.55, 9);
    for (let i = 0; i < count; i++) {
      const cx = x + (i + 0.5) * w / count;
      const path = i === onPath[l], aunt = l < levels.length - 1 && i === (onPath[l] ^ 1) && i < count;
      out.append(s('rect', { class: path ? 'node on' : aunt ? 'node aunt' : 'node', x: cx - bw / 2, y: cy - bh / 2, width: bw, height: bh, rx: 1.5 }));
      if (l > 0) {
        for (const c of [2 * i, 2 * i + 1]) {
          if (c >= levels[l - 1]) continue;
          const ccx = x + (c + 0.5) * w / levels[l - 1], ccy = y + h - rowH * (l - 0.5);
          out.append(s('path', { class: c === onPath[l - 1] && path ? 'edge on' : 'edge', d: `M${cx},${cy + bh / 2}L${ccx},${ccy - bh / 2}` }));
        }
      }
    }
  });
  return out;
}

// a balance: a beam on a post, a pan at each end; set(tilt) leans it (−1 … 1: the right pan heavier when positive)
export function balance(cx, y, w) {
  const beam = g({ class: 'beam' });
  const pan = (px) => g({ class: 'pan' }, s('path', { class: 'cord', d: `M${px},${y}l-9,16M${px},${y}l9,16` }), s('path', { class: 'dish', d: `M${px - 13},${y + 16}q13,10 26,0z` }));
  beam.append(s('rect', { class: 'bar', x: cx - w / 2, y: y - 2, width: w, height: 4, rx: 2 }), pan(cx - w / 2 + 4), pan(cx + w / 2 - 4));
  const el = g({ class: 'balance' }, s('path', { class: 'post', d: `M${cx},${y}v30m-14,0h28` }), s('path', { class: 'fulcrum', d: `M${cx - 6},${y + 8}L${cx},${y - 2}L${cx + 6},${y + 8}z` }), beam);
  el.set = (tilt) => { beam.setAttribute('transform', `rotate(${(Math.max(-1, Math.min(1, tilt)) * 9).toFixed(2)} ${cx} ${y})`); };
  el.set(0);
  return el;
}

// a polyline's length and the point a fraction along it (a packet's place on its path)
export function pathLen(pts) {
  let L = 0;
  for (let i = 1; i < pts.length; i++) L += Math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]);
  return L;
}

export function along(pts, f) {
  const L = pathLen(pts) * Math.max(0, Math.min(1, f));
  let acc = 0;
  for (let i = 1; i < pts.length; i++) {
    const seg = Math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]);
    if (acc + seg >= L) { const t = seg ? (L - acc) / seg : 0; return [pts[i - 1][0] + (pts[i][0] - pts[i - 1][0]) * t, pts[i - 1][1] + (pts[i][1] - pts[i - 1][1]) * t]; }
    acc += seg;
  }
  return pts[pts.length - 1];
}

export function polyline(pts, cls = 'wire') {
  return s('path', { class: cls, d: 'M' + pts.map((p) => p[0].toFixed(1) + ',' + p[1].toFixed(1)).join('L') });
}

// numbers: whole tokens with thousands, or compact (1.2K, 3.4M) where room is short
export function compact(units) {
  const v = Number(BigInt(units) / 1000n) / 1000;
  const a = Math.abs(v);
  if (a >= 1e6) return (v / 1e6).toFixed(a >= 1e7 ? 0 : 1).replace(/\.0$/, '') + 'M';
  if (a >= 1e4) return (v / 1e3).toFixed(a >= 1e5 ? 0 : 1).replace(/\.0$/, '') + 'K';
  if (a >= 100) return Math.round(v).toLocaleString('en-US');
  return String(Math.round(v * 100) / 100);
}
