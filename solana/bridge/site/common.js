// bridge.gno.world — shared by every page: the API (this origin only), amounts, the chain picker, small DOM.

export async function api(path, opts) {
  const r = await fetch(path, opts);
  let j = {};
  try { j = await r.json(); } catch (e) { /* no body */ }
  if (!r.ok) { const e = new Error(j.error || `HTTP ${r.status}`); e.status = r.status; throw e; }
  return j;
}

const UNIT = 10n ** 6n; // both tokens carry six decimals on both sides

// fmt shows base units (a number or a string) as a decimal amount.
export function fmt(n) {
  let v = BigInt(n); const neg = v < 0n; if (neg) v = -v;
  const whole = v / UNIT, frac = v % UNIT;
  let s = whole.toLocaleString('en-US');
  if (frac) s += '.' + frac.toString().padStart(6, '0').replace(/0+$/, '');
  return (neg ? '−' : '') + s;
}

// whole shows base units as whole tokens (a meter refills by the second; its fraction says nothing).
export function whole(n) { return (BigInt(n) / UNIT).toLocaleString('en-US'); }

// parseAmount reads a decimal amount as base units (a BigInt), or null.
export function parseAmount(text) {
  const m = /^\s*(\d+)(?:\.(\d{1,6}))?\s*$/.exec(text || '');
  if (!m) return null;
  return BigInt(m[1]) * UNIT + BigInt((m[2] || '').padEnd(6, '0'));
}

export function el(tag, attrs = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') e.className = v;
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else if (v !== null && v !== undefined) e.setAttribute(k, v);
  }
  for (const k of kids.flat()) if (k !== null && k !== undefined) e.append(k.nodeType ? k : String(k));
  return e;
}

export function short(s, n = 8) { s = String(s || ''); return s.length > 2 * n + 1 ? s.slice(0, n) + '…' + s.slice(-n) : s; }

// when says a unix time relative to now: "in 6 d 23 h", "3 min ago".
export function when(unix) {
  if (!unix) return '—';
  let d = unix - Math.floor(Date.now() / 1000); const past = d < 0; d = Math.abs(d);
  const parts = [];
  const days = Math.floor(d / 86400); d -= days * 86400;
  const hours = Math.floor(d / 3600); d -= hours * 3600;
  const mins = Math.floor(d / 60);
  if (days) parts.push(days + ' d');
  if (hours && parts.length < 2) parts.push(hours + ' h');
  if (!days && mins && parts.length < 2) parts.push(mins + ' min');
  if (!parts.length) parts.push(past ? 'moments' : 'under a minute');
  return past ? parts.join(' ') + ' ago' : 'in ' + parts.join(' ');
}

export function date(unix) { return unix ? new Date(unix * 1000).toISOString().replace('T', ' ').slice(0, 16) + ' UTC' : '—'; }

export const chains = api('/api/chains');

// pick is the chain the page shows: ?chain=<id>, else the first one listed.
export async function pick() {
  const c = await chains;
  const id = new URLSearchParams(location.search).get('chain');
  return c.chains.find((x) => x.id === id) || c.chains[0];
}

// kindOf is a chain's kind: /api/chains names it for every chain but Solana's (E-M4).
export function kindOf(ch) { return (ch && ch.kind) || 'solana'; }

// WORDS are what the pages say of a chain's kind where the chains differ: Solana's are the pages' own words, and
// Ethereum's replace them on its pages (E-M4).
export const WORDS = {
  solana: { unit: 'slot', period: 0.4, every: 'a slot ≈0.4 s', everyShort: '0.4 s/slot', ref: 'signature', gate: 'pausable ISM', admin: 'admin program', adminShort: 'admin program' },
  ethereum: { unit: 'block', period: 12, every: 'a block ≈12 s', everyShort: '12 s/block', ref: 'transaction hash', gate: 'PausableGate', admin: 'HlAdmin', adminShort: 'HlAdmin' },
};
export function words(ch) { return WORDS[kindOf(ch)] || WORDS.solana; }

// chainHref is a page's address on a chain: the first chain listed is the pages' own, the others carry ?chain=.
export function chainHref(path, c, ch) { return !ch || ch.id === c.chains[0].id ? path : path + '?chain=' + encodeURIComponent(ch.id); }

// nav marks the page's link and names the chain in the header; when the config lists more than one chain, the
// header switches between them and every link keeps the chain the page shows.
export async function nav(active) {
  for (const a of document.querySelectorAll('header nav a')) {
    if (a.getAttribute('href') === active) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  }
  try {
    const c = await chains;
    const ch = await pick();
    const slot = document.querySelector('header .chain');
    if (slot && ch) slot.textContent = 'gno.land ⇄ ' + ch.name;
    if (slot && ch && c.chains.length > 1) {
      for (const a of document.querySelectorAll('header nav a')) a.setAttribute('href', chainHref(a.getAttribute('href'), c, ch));
      slot.replaceChildren('gno.land ⇄ ', ...c.chains.flatMap((x, i) => [i ? ' · ' : '', el('a', { href: chainHref(location.pathname, c, x), 'aria-current': x.id === ch.id ? 'true' : null }, x.name)]));
    }
    if ((await chains).preview && !document.getElementById('preview')) {
      document.querySelector('main').prepend(el('div', { id: 'preview', class: 'preview', role: 'note' },
        'Preview: the bridge is not deployed yet. The figures are examples, and sending opens at launch.'));
    }
  } catch (e) { /* the header stands without it */ }
}

export function realmLink(c, realm) { return c.gno.realmLink.replace('%s', realm); }

// explorerAddress is an address's page on the chain's explorer, '' when the chain names none.
export function explorerAddress(chain, addr) {
  if (!chain.explorer || !addr) return '';
  return chain.explorer.replace(/\/+$/, '') + '/address/' + addr + (chain.cluster ? '?cluster=' + encodeURIComponent(chain.cluster) : '');
}

// tokenAddress is a token's address on the other chain, whole, with a button that copies it and a link to it on the
// explorer: what a wallet asks for to add the token, and how to tell the bridge's token from another of the same name.
export function tokenAddress(chain, t) {
  const copy = el('button', { class: 'copy', type: 'button', title: 'copy the address' }, 'copy');
  copy.onclick = async () => {
    try {
      await navigator.clipboard.writeText(t.mint);
      copy.textContent = 'copied';
    } catch (e) { // no clipboard (an insecure page, a refused permission): select it for a manual copy
      const r = document.createRange();
      r.selectNodeContents(code);
      const s = getSelection(); s.removeAllRanges(); s.addRange(r);
      copy.textContent = 'selected';
    }
    setTimeout(() => { copy.textContent = 'copy'; }, 1500);
  };
  const code = el('code', { class: 'mono' }, t.mint);
  const href = explorerAddress(chain, t.mint);
  return el('span', { class: 'addr', 'data-token': t.symbol }, code, copy,
    href ? el('a', { href, target: '_blank', rel: 'noopener' }, 'explorer ↗') : '');
}

export function setLine(id, text, kind) {
  const n = document.getElementById(id);
  if (!n) return;
  n.textContent = text || '';
  n.className = 'line' + (kind ? ' ' + kind : '');
  n.classList.toggle('hidden', !text);
}
