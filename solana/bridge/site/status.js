// bridge.gno.world — the Status page: per token the lock against the supply (the identity), the caps and fees,
// the flags on both sides, the client, the pending decisions, who governs. Read every ten seconds.
import { api, chains, el, fmt, whole, nav, pick, short, when, date, realmLink, kindOf, words, tokenAddress } from './common.js';
import { mountLocked } from './chart.js';

const $ = (id) => document.getElementById(id);

async function main() {
  nav('/status');
  const c = await chains;
  const chain = await pick();
  $('addrs').replaceChildren(...chain.tokens.filter((t) => t.mint).flatMap((t) => [el('dt', {}, `${t.symbol} on ${chain.name}`), el('dd', {}, tokenAddress(chain, t))]));
  mountLocked($('locked'), chain);
  const draw = async () => {
    let st;
    try { st = await api('/api/' + chain.id + '/status'); } catch (e) { $('head').textContent = 'the status could not be read: ' + e.message; $('head').className = 'line err'; return; }
    render(c, chain, st);
  };
  await draw();
  setInterval(draw, 10000);
}

function render(c, chain, st) {
  const o = st.other || {}, w = words(chain), eth = kindOf(chain) === 'ethereum';
  $('head').className = 'line';
  // Ethereum's supplies are read at its finalized block, the newest block's figures beside them (E-M4)
  const far = eth ? `${chain.name} block ${o.head || '—'} (supplies at the finalized block ${o.slot || '—'})` : `${chain.name} slot ${o.slot || '—'}`;
  $('head').textContent = `gno.land height ${st.gno.height} (${date(st.gno.time)}) · ${far} · read ${when(st.at)}` + (st.gno.live ? '' : ' · NOT LAUNCHED') + (c.preview ? ' · PREVIEW: example figures' : '');
  // the tokens
  const rows = st.tokens.map((t) => {
    const idCls = t.identity === 'BROKEN' ? 'bad' : t.identity === 'holds' ? 'ok' : 'warn';
    return el('tr', {},
      el('td', {}, el('a', { href: realmLink(c, t.realm), target: '_blank', rel: 'noopener' }, t.symbol), t.paused ? el('div', { class: 'warn' }, 'paused: ' + t.paused) : '', t.migratedTo ? el('div', { class: 'warn' }, 'migrated to ' + t.migratedTo) : ''),
      el('td', { class: 'num' }, fmt(t.lock)),
      el('td', { class: 'num' }, t.supply === null || t.supply === undefined ? '—' : fmt(t.supply)),
      el('td', { class: 'num' }, fmt(t.owed)),
      el('td', { class: 'num' }, fmt(t.voided)),
      el('td', { class: 'num' }, fmt(t.lost)),
      el('td', { class: 'num' }, fmt(t.gap)),
      el('td', { class: idCls, title: t.identityLine }, t.identity));
  });
  $('tokens').replaceChildren(el('thead', {}, el('tr', {}, el('th', {}, 'token'), el('th', { class: 'num' }, 'lock'), el('th', { class: 'num' }, 'supply on ' + chain.name), el('th', { class: 'num' }, 'owed'), el('th', { class: 'num' }, 'voided'), el('th', { class: 'num' }, 'lost'), el('th', { class: 'num' }, 'in flight'), el('th', {}, 'identity'))), el('tbody', {}, rows));
  $('identity').replaceChildren(...st.tokens.map((t) => el('div', { class: 'line mono' }, t.identityLine || (t.symbol + ': ' + chain.name + ' unread'))));
  // the caps and fees
  $('caps').replaceChildren(el('thead', {}, el('tr', {}, el('th', {}, 'token'), el('th', { class: 'num' }, 'fee'), el('th', { class: 'num' }, 'minimum'), el('th', { class: 'num' }, 'max per transfer'), el('th', { class: 'num' }, 'day cap'), el('th', { class: 'num' }, 'available now'), el('th', { class: 'num' }, chain.name + ' max'), el('th', { class: 'num' }, chain.name + ' day cap'), el('th', { class: 'num' }, 'used'), el('th', { class: 'num' }, 'open claims'))),
    el('tbody', {}, st.tokens.map((t) => el('tr', {}, el('td', {}, t.symbol, t.mismatch ? el('div', { class: 'warn' }, t.mismatch) : ''), el('td', { class: 'num' }, fmt(t.fee)), el('td', { class: 'num' }, fmt(t.minimum)), el('td', { class: 'num' }, fmt(t.maximum)), el('td', { class: 'num' }, whole(t.cap)), el('td', { class: 'num' }, whole(t.meter)),
      el('td', { class: 'num' }, t.limit ? whole(t.limit.maxPerMessage) : '—'), el('td', { class: 'num' }, t.limit ? whole(t.limit.dayCap) : '—'), el('td', { class: 'num' }, t.limit ? whole(t.limit.used) : '—'), el('td', { class: 'num' }, t.openClaims)))));
  // the flags
  const flags = [];
  for (const f of st.gno.flags) flags.push(el('li', { class: 'warn' }, `gno.land: ${f.fn} of ${short(f.route.split('/').pop(), 12)} paused by seat ${f.seat} (${short(f.addr, 6)}) ${when(f.at)}` + (f.ids && f.ids.length ? `, naming ${f.ids.length} ids` : '')));
  for (const [name, g] of [[`the deliveries (${w.gate})`, o.pausable], [`the decisions (${w.admin})`, o.admin]]) {
    if (!g) continue;
    g.flags.forEach((f, i) => { if (f) flags.push(el('li', { class: 'warn' }, `${chain.name}: ${name} flagged by seat ${i + 1}${i >= 4 ? ' (watch key)' : ''} (${short(g.keys[i], 6)}) ${when(f.time)}`)); });
    if (g.hold) flags.push(el('li', { class: 'bad' }, `${chain.name}: a HOLD stands on ${name} since ${date(g.holdSince && g.holdSince.time)}: two guardians' flags; it ends when they lift, or when governance replaces them (the hold's 21st day at the earliest)`));
  }
  if (o.client && o.client.quarantineEnd > Math.floor(Date.now() / 1000)) flags.push(el('li', { class: 'warn' }, `${chain.name}: the quarantine after a validator change stands until ${date(o.client.quarantineEnd)}: gno.land → ${chain.name} waits`));
  $('flags').replaceChildren(...(flags.length ? flags : [el('li', { class: 'ok' }, 'no flag, no hold, no quarantine: everything runs')]));
  // the client
  const cl = o.client;
  $('client').replaceChildren(
    el('dt', {}, 'gno.land on ' + chain.name), el('dd', {}, cl ? `trusted height ${cl.trustedHeight} (gno.land is at ${st.gno.height}, ${st.gno.height - cl.trustedHeight} behind); last header ${when(cl.lastAccepted)}` : 'unread'),
    el('dt', {}, 'who governs'), el('dd', {}, st.gno.governorLine),
    el('dt', {}, 'the way back'), el('dd', {}, `${st.gno.signers} signer machines, rule ${st.gno.rule}; verifier ${short(st.gno.verifier, 14)}`),
    el('dt', {}, 'guardian seats'), el('dd', {}, st.gno.seats.map((s, i) => (i + 1) + ': ' + (s ? short(s, 6) : 'empty')).join(' · ')),
    el('dt', {}, 'decisions to ' + chain.name), el('dd', {}, st.gno.gov ? `${st.gno.gov.last} sent by r/hlgov, ${o.lastSeq === undefined ? '—' : o.lastSeq} recorded on ${chain.name}` + (st.gno.gov.last !== o.lastSeq ? ' (a gap: one is on its way, or a number was taken)' : '') : '—'));
  // the pending decisions
  const pend = [];
  for (const d of st.decisions) pend.push(el('li', {}, `gno.land ${short(d.realm.split('/').pop(), 12)} #${d.id} ${d.kind}: ${d.text} — matures ${when(d.due)}`));
  for (const p of o.pending || []) pend.push(el('li', {}, `${chain.name} #${p.number} ${p.kindName}: ${p.text} — matures ${when(p.maturesAt)}` + (p.deferred ? ' (' + p.deferred + ')' : '')));
  $('pending').replaceChildren(...(pend.length ? pend : [el('li', { class: 'mute' }, 'nothing pending')]));
  const errs = (st.errors || []).map((e) => el('li', { class: 'err' }, e));
  $('errors').replaceChildren(...errs);
  $('errors').classList.toggle('hidden', !errs.length);
}

main().catch((e) => { $('head').textContent = e.message; $('head').className = 'line err'; });
