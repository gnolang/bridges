// bridge.gno.world — the Home page: the machine (machine.js) fed by /api/<chain>/activity every ten seconds, the
// caption strip that says what is happening, the recent events (a click plays one), and the replay of the last real
// transfer once nothing has happened for a while. The story below is static.
import { api, nav, pick, el, short } from './common.js';
import { createMachine } from './machine.js';
import { s, coin, block, seal, coinStack, envelope } from './draw.js';

nav('/');

const $ = (id) => document.getElementById(id);
const IDLE = 60; // seconds without an event before the replay is offered

function units(n) { return (BigInt(n || 0) / 1000000n).toLocaleString('en-US') + (BigInt(n || 0) % 1000000n ? '.' + String(BigInt(n) % 1000000n).padStart(6, '0').replace(/0+$/, '') : ''); }
function ago(t) {
  const d = Math.max(0, Math.floor(Date.now() / 1000 - t));
  if (d < 60) return d + ' s ago';
  if (d < 3600) return Math.floor(d / 60) + ' min ago';
  if (d < 86400) return Math.floor(d / 3600) + ' h ago';
  return Math.floor(d / 86400) + ' d ago';
}
function hms(t) { return new Date(t * 1000).toISOString().slice(11, 16) + ' UTC'; }

// icon is an event's small picture, the machine's own shapes
function icon(kind, token) {
  const svg = s('svg', { viewBox: '0 0 20 16', width: 20, height: 16, class: 'walk-svg', 'aria-hidden': 'true' });
  switch (kind) {
    case 'send': case 'payout': svg.append(coin(10, 8, 6.5, token)); break;
    case 'burn': svg.append(coin(10, 8, 6.5, token), s('path', { class: 'burnmark', d: 'M4,13L16,3' })); break;
    case 'mint': svg.append(coinStack(10, 4, 12, 2)); break;
    case 'header': svg.append(block(2, 3, 12, 10), seal(15, 5, 4, 'check')); break;
    case 'credit': svg.append(envelope(9, 9, 13, 9), seal(16, 4, 3.5, 'trust')); break;
    default: svg.append(envelope(10, 8, 14, 10));
  }
  return svg;
}

function sentence(e, chain) {
  const amt = e.amount ? units(e.amount) + ' ' + (e.token || '') : '';
  const blk = (h) => h ? ' · block ' + Number(h).toLocaleString('en-US') : '';
  switch (e.kind) {
    case 'send': return `${amt} locked on gno.land${blk(e.height)}`;
    case 'header': return `a header of block ${Number(e.height).toLocaleString('en-US')} reached ${chain}`;
    case 'mint': return `${amt} minted on ${chain} to ${short(e.to, 4)}` + (e.proof && e.proof.height ? ` · proven under block ${Number(e.proof.height).toLocaleString('en-US')}` : '');
    case 'burn': return `${amt} burned on ${chain}` + (e.nonce !== undefined ? ` · burn #${e.nonce}` : '') + (e.signers && e.signers.length ? ` · ${e.signers.length} of 3 vouched` : '');
    case 'credit': return `a claim of ${amt} credited on gno.land${blk(e.height)}`;
    case 'payout': return `${amt} paid out on gno.land` + (e.full ? '' : ' (an installment)');
    case 'decision': return `a decision sent to ${chain}` + (e.what ? ' · ' + e.what : '');
    case 'enact': return `a decision reached ${chain}'s admin program`;
  }
  return e.kind;
}

(async () => {
  const chain = await pick();
  const cap = $('caption');
  const m = createMachine($('machine'), { chain: chain.name, onCaption: (t) => { cap.textContent = t; }, onMode: () => offer() });
  let act = null;
  const offer = () => {
    const can = !!(act && act.last && (act.last.out || act.last.in));
    $('replay').classList.toggle('hidden', !can || m.mode === 'replay' || m.lastEventAge() < IDLE);
  };
  $('replay').onclick = () => { if (act && m.replay(act.last)) offer(); };
  const render = () => {
    // the newest header (one comes every ten minutes) and the newest transfers' steps, newest first
    const all = act.events || [];
    const hdr = all.find((e) => e.kind === 'header');
    const evs = [...(hdr ? [hdr] : []), ...all.filter((e) => e.kind !== 'header').slice(0, 5)].sort((x, y) => y.time - x.time);
    $('events').replaceChildren(...evs.map((e) => {
      const li = el('li', { tabindex: 0, 'data-kind': e.kind, title: 'play it on the machine' },
        el('span', { class: 'ico' }, icon(e.kind, e.token)),
        el('span', { class: 'what' }, sentence(e, chain.name), e.link ? el('a', { href: e.link, target: '_blank', rel: 'noopener', onclick: (ev) => ev.stopPropagation() }, ' ↗') : ''),
        el('span', { class: 't', title: e.exact ? '' : 'the chain records no time for this; the nearest known' }, (e.exact ? '' : '≈') + hms(e.time) + ' · ' + ago(e.time)));
      const go = () => m.play(e);
      li.addEventListener('click', go);
      li.addEventListener('keydown', (ev) => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); go(); } });
      return li;
    }));
    const notes = [];
    if (act.stale) notes.push('some figures are from an earlier reading');
    for (const e of act.errors || []) notes.push(e);
    $('stale').textContent = notes.join(' · ');
    $('stale').classList.toggle('hidden', !notes.length);
  };
  const poll = async () => {
    try { act = await api('/api/' + chain.id + '/activity'); } catch (e) { cap.textContent = 'the bridge could not be read: ' + e.message; return; }
    m.update(act);
    render();
    offer();
  };
  await poll();
  setInterval(poll, 10000);
  setInterval(offer, 5000);
  // back to the tab: read at once rather than at the next tick
  document.addEventListener('visibilitychange', () => { if (!document.hidden) poll(); });
})();
