// bridge.gno.world — the Bridge page: a chain, a direction, a token, an amount, a recipient; the wallet signs;
// the transfer is tracked here until it lands, step by step (walk.js), each step shown on the machine below
// (machine.js, compact), which runs live between transfers; under the form, the chosen token's address on the other
// chain. On Ethereum (E-M4) the way out names an Ethereum recipient (its EIP-55 checksum and ER10's refusals checked
// before Adena is asked: an address in one case is refused, never completed) and the way back is the call the site
// builds, sent by an EIP-1193 wallet on the right chain.
import { api, chains, el, fmt, whole, parseAmount, nav, pick, short, setLine, kindOf, words, tokenAddress } from './common.js';
import { adena, solanaWallet, ethWallet, isSolanaAddress, isGnoAddress, b64decode } from './wallets.js';
import { recipient as ethRecipient } from './eth.js';
import { createMachine } from './machine.js';
import { render as renderWalk, tickEta } from './walk.js';

const $ = (id) => document.getElementById(id);
const state = { c: null, chain: null, kind: 'solana', dir: 'out', token: null, status: null, gnoAddr: '', solAddr: '', ethAddr: '', poll: null, machine: null, tr: null, shown: '' };

// farWallet is the chain's own wallet: Solana's, or Ethereum's (E-M4)
const farWallet = () => (state.kind === 'ethereum' ? ethWallet : solanaWallet);

async function main() {
  nav('/bridge');
  state.c = await chains;
  state.chain = await pick();
  state.kind = kindOf(state.chain);
  state.machine = createMachine($('machine'), { compact: true, chain: state.chain.name, kind: state.kind, onCaption: (t) => { $('caption').textContent = t; } });
  const live = async () => { try { state.machine.update(await api('/api/' + state.chain.id + '/activity')); } catch (e) { $('caption').textContent = 'the bridge could not be read: ' + e.message; } };
  live();
  setInterval(live, 10000);
  setInterval(() => { if (state.tr) tickEta($('track'), state.tr); }, 1000);
  const sel = $('chain');
  for (const ch of state.c.chains) sel.append(el('option', { value: ch.id }, ch.name));
  sel.value = state.chain.id;
  sel.onchange = () => { location.search = '?chain=' + sel.value; };
  for (const t of state.chain.tokens) $('token').append(el('option', { value: t.symbol }, t.symbol));
  state.token = state.chain.tokens[0].symbol;
  $('token').onchange = () => { state.token = $('token').value; showAddress(); fees(); };
  showAddress();
  $('out').onclick = () => setDir('out');
  $('in').onclick = () => setDir('in');
  $('send').onclick = () => send().catch((e) => setLine('result', e.message, 'err'));
  $('connect').onclick = () => connect().catch((e) => setLine('result', e.message, 'err'));
  $('trackgo').onclick = () => { const ref = $('trackref').value.trim(); if (ref) track(ref); };
  $('trackref').placeholder = 'a message id, a gno.land transaction hash or ' + (state.kind === 'ethereum' ? 'an ' : 'a ') + state.chain.name + ' ' + words(state.chain).ref;
  if (state.kind === 'ethereum' && ethWallet.wallets().length > 1) { // more than one wallet answered: the reader picks
    const pickw = el('select', { id: 'ethwallet', 'aria-label': 'Ethereum wallet' }, ...ethWallet.wallets().map((n, i) => el('option', { value: i }, n)));
    pickw.onchange = () => { ethWallet.choose(Number(pickw.value)); state.ethAddr = ''; setDir(state.dir); };
    $('connect').before(pickw);
  }
  $('trackref').onkeydown = (ev) => { if (ev.key === 'Enter') $('trackgo').click(); };
  const ref = new URLSearchParams(location.search).get('track');
  if (ref) { $('trackref').value = ref; track(ref); }
  setDir('out');
  refreshStatus();
  setInterval(refreshStatus, 30000);
  if (state.c.preview) { // nothing to sign before the programs exist: the wallets are never asked
    for (const id of ['connect', 'send']) { $(id).disabled = true; $(id).title = 'opens when the bridge is deployed'; }
  }
}

function setDir(dir) {
  state.dir = dir;
  $('out').setAttribute('aria-pressed', dir === 'out');
  $('in').setAttribute('aria-pressed', dir === 'in');
  const to = $('recipient');
  $('out').textContent = 'gno.land → ' + state.chain.name;
  $('in').textContent = state.chain.name + ' → gno.land';
  if (dir === 'out') {
    to.placeholder = state.chain.name + ' address of the recipient' + (state.kind === 'ethereum' ? ' (0x…)' : '');
    $('connect').textContent = state.gnoAddr ? 'Adena: ' + short(state.gnoAddr, 6) : 'Connect Adena';
  } else {
    to.placeholder = 'gno.land address of the recipient (g1…)';
    const w = farWallet(), addr = state.kind === 'ethereum' ? state.ethAddr : state.solAddr;
    $('connect').textContent = addr ? w.name() + ': ' + short(addr, 6) : 'Connect ' + (w.found() ? w.name() : state.kind === 'ethereum' ? 'an Ethereum wallet' : 'a Solana wallet');
  }
  setLine('result', '');
  fees();
}

// showAddress is the chosen token's address on the other chain, under the form: what a wallet asks for.
function showAddress() {
  const t = state.chain.tokens.find((x) => x.symbol === state.token);
  if (!t || !t.mint) { $('tokenaddr').replaceChildren(); return; }
  $('tokenaddr').replaceChildren(el('span', {}, `${t.symbol} on ${state.chain.name}: `), tokenAddress(state.chain, t));
}

async function refreshStatus() {
  try { state.status = await api('/api/' + state.chain.id + '/status'); } catch (e) { state.status = null; setLine('fees', 'the figures could not be read: ' + e.message, 'err'); return; }
  fees();
}

// fees is the one line under the form: the fee, the minimum, the maximum per transfer and the day cap of the
// chosen token in the chosen direction, from the status.
function fees() {
  const st = state.status;
  if (!st) return;
  const t = st.tokens.find((x) => x.symbol === state.token);
  if (!t) return;
  const sym = t.symbol;
  if (state.dir === 'out') {
    let s = `fee ${fmt(t.fee)} ${sym} on top · at least ${fmt(t.minimum)} · at most ${fmt(t.maximum)} per transfer`;
    if (t.limit) s += ` · ${state.chain.name}'s day cap ${whole(t.limit.dayCap)} (${whole(t.limit.used)} used)`;
    if (t.paused && t.paused.includes('sends')) s += ' · SENDS PAUSED on gno.land';
    if (st.other && st.other.pausable && (st.other.pausable.flags.some((f) => f) || st.other.pausable.hold)) s += ' · deliveries PAUSED on ' + state.chain.name;
    if (t.mismatch) s += ' · ' + t.mismatch;
    setLine('fees', s, t.paused || t.mismatch ? 'warn' : '');
  } else {
    let s = `fee ${fmt(t.fee)} ${sym}, taken from the claim first · no minimum (a burn under the fee is swept) · paid under the meters: day cap ${whole(t.cap)} ${sym} (${whole(t.meter)} available now), a quarter of that per recipient`;
    if (t.paused && (t.paused.includes('credits') || t.paused.includes('payouts'))) s += ' · ' + t.paused.toUpperCase() + ' PAUSED on gno.land';
    setLine('fees', s, t.paused ? 'warn' : '');
  }
}

async function connect() {
  if (state.dir === 'out') {
    const acc = await adena.connect();
    if (acc.chainId && acc.chainId !== state.c.gno.chainId) setLine('result', `Adena is on ${acc.chainId}; this bridge is on ${state.c.gno.chainId}: switch the network in Adena`, 'err');
    state.gnoAddr = acc.address;
  } else if (state.kind === 'ethereum') {
    state.ethAddr = await ethWallet.connect();
  } else {
    state.solAddr = await solanaWallet.connect();
  }
  setDir(state.dir);
}


async function send() {
  setLine('result', '');
  const t = state.chain.tokens.find((x) => x.symbol === state.token);
  const st = state.status && state.status.tokens.find((x) => x.symbol === state.token);
  if (!st) throw new Error('the bridge’s figures are not loaded yet; try again in a moment');
  const amount = parseAmount($('amount').value);
  if (amount === null || amount <= 0n) throw new Error('an amount like 10 or 12.5 (six decimals at most)');
  const to = $('recipient').value.trim();
  $('send').disabled = true;
  try {
    if (state.dir === 'out') await sendOut(t, st, amount, to); else await sendIn(t, st, amount, to);
  } finally { $('send').disabled = false; }
}

// sendOut: TransferRemote on the route realm through Adena, with the exact envelope the realm requires —
// GNOT sends amount + fee in ugnot with the call; BUBBLE approves amount + fee to the route first.
async function sendOut(t, st, amount, to) {
  if (state.kind === 'ethereum') {
    const r = ethRecipient(to, state.chain);
    if (!r.ok) throw new Error(r.why);
    to = r.addr;
  } else if (!isSolanaAddress(to)) throw new Error('the recipient must be a ' + state.chain.name + ' address (base58)');
  if (amount < BigInt(st.minimum)) throw new Error(`at least ${fmt(st.minimum)} ${t.symbol}`);
  if (amount > BigInt(st.maximum)) throw new Error(`at most ${fmt(st.maximum)} ${t.symbol} per transfer`);
  if (!state.gnoAddr) await connect();
  const fee = BigInt(st.fee);
  const g = state.c.gno;
  const call = (pkg, func, args, send) => ({ type: '/vm.m_call', value: { caller: state.gnoAddr, send, pkg_path: pkg, func, args } });
  const args = [String(state.chain.domain), to, amount.toString()];
  const messages = t.symbol === 'GNOT'
    ? [call(t.realm, 'TransferRemote', args, (amount + fee).toString() + 'ugnot')]
    : [call(g.bubble, 'Approve', [t.realmAddress, (amount + fee).toString()], ''), call(t.realm, 'TransferRemote', args, '')];
  setLine('result', 'waiting for Adena…');
  const hash = await adena.send(messages, g.gasFee, g.gasWanted);
  if (!hash) throw new Error('Adena returned no transaction hash; track the transfer by its message id from the realm’s page');
  setLine('result', `sent: ${fmt(amount)} ${t.symbol} (+ ${fmt(fee)} fee) to ${short(to)} on ${state.chain.name}; tracking ${short(hash)}`, 'ok');
  $('trackref').value = hash;
  track(hash);
}

// sendIn: the backend builds the burn (transfer_remote on the token program, unsigned); the wallet signs it as
// the payer and sends it through its own RPC; the signature is tracked until the claim is paid.
async function sendIn(t, st, amount, to) {
  if (!isGnoAddress(to)) throw new Error('the recipient must be a gno.land address (g1…)');
  if (amount <= BigInt(st.fee)) throw new Error(`more than the fee of ${fmt(st.fee)} ${t.symbol}: a burn under it is swept, paid only by a governance decision`);
  if (state.kind === 'ethereum') return sendInEth(t, amount, to);
  if (!state.solAddr) await connect();
  setLine('result', 'building the transaction…');
  const built = await api('/api/' + state.chain.id + '/burn-tx', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token: t.symbol, sender: state.solAddr, recipient: to, amount: amount.toString() }) });
  setLine('result', 'waiting for the wallet…');
  const sig = await solanaWallet.signAndSend(b64decode(built.tx), state.chain.cluster);
  setLine('result', `burned: ${fmt(amount)} ${t.symbol} toward ${to}; tracking ${short(sig)}`, 'ok');
  $('trackref').value = sig;
  track(sig);
}

// sendInEth: the backend builds the call (the token's transferRemote toward gno.land, with the native value its
// quote asks: the mailbox's protocol fee); the wallet is put on the chain, signs and sends it through its own
// provider; the transaction hash is tracked until the claim is paid.
async function sendInEth(t, amount, to) {
  if (!state.ethAddr) await connect();
  setLine('result', 'building the transaction…');
  const built = await api('/api/' + state.chain.id + '/burn-tx', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token: t.symbol, sender: state.ethAddr, recipient: to, amount: amount.toString() }) });
  setLine('result', 'checking the wallet is on ' + state.chain.name + '…');
  await ethWallet.onChain(built.chainId);
  setLine('result', 'waiting for the wallet…');
  const hash = await ethWallet.send(built);
  const fee = BigInt(built.value || '0x0');
  setLine('result', `burned: ${fmt(amount)} ${t.symbol} toward ${to}` + (fee ? ` (with the mailbox's fee of ${eth(fee)} ETH)` : '') + `; tracking ${short(hash)}`, 'ok');
  $('trackref').value = hash;
  track(hash);
}

// eth shows wei as ETH.
function eth(wei) {
  const s = wei.toString().padStart(19, '0');
  return (s.slice(0, -18).replace(/^0+(?=\d)/, '') + '.' + s.slice(-18)).replace(/\.?0+$/, '');
}

// track polls one transfer every five seconds and walks through its steps until it lands; each step newly done
// plays on the machine, and a step chosen plays again.
function track(ref) {
  if (state.poll) clearInterval(state.poll);
  const box = $('track');
  box.classList.remove('hidden');
  state.shown = '';
  // show plays a step on the machine; a step the reader chose brings the machine into view if it is off screen
  const show = (key, chosen = true) => {
    if (!state.tr) return;
    state.machine.walkStep(state.tr.direction, key, state.tr);
    $('track').querySelectorAll('ol.walk li').forEach((li) => li.classList.toggle('shown', li.dataset.key === key));
    const r = $('machine').getBoundingClientRect();
    if (chosen && (r.top > innerHeight || r.bottom < 0)) $('machine').scrollIntoView({ block: 'nearest', behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
  };
  const draw = async () => {
    let tr;
    try { tr = await api('/api/' + state.chain.id + '/transfer/' + encodeURIComponent(ref)); } catch (e) {
      box.replaceChildren(el('div', { class: 'line' + (e.status === 404 ? '' : ' err') }, e.status === 404 ? 'not seen yet: ' + e.message : e.message));
      return;
    }
    state.tr = tr;
    const head = el('div', {}, el('b', {}, tr.direction === 'out' ? 'gno.land → ' + state.chain.name : state.chain.name + ' → gno.land'), ' · ', tr.token, tr.amount ? ' · ' + fmt(tr.amount) : '', ' · ', el('span', { class: 'state ' + (tr.state === 'failed' || tr.state === 'voided' ? 'bad' : (tr.state === 'paid' || tr.state === 'delivered') ? 'ok' : '') }, tr.state));
    const idline = el('div', { class: 'line mono' }, 'message id ', tr.id || '—', tr.note ? ' · ' + tr.note : '');
    if (!tr.walk || !tr.walk.length) { // a refused transaction: no steps to walk
      box.replaceChildren(head, idline);
    } else {
      const { list } = renderWalk(tr, state.chain.name, show, state.kind);
      box.replaceChildren(head, list, idline);
      // the newest step done plays on the machine, once
      const done = tr.walk.filter((w) => w.done).map((w) => w.key);
      const newest = done[done.length - 1] || '';
      if (newest && newest !== state.shown) { state.shown = newest; show(newest, false); }
    }
    if (['paid', 'delivered', 'failed', 'refunded', 'written off', 'voided', 'swept'].includes(tr.state)) { clearInterval(state.poll); state.poll = null; }
  };
  draw();
  state.poll = setInterval(draw, 5000);
}

main().catch((e) => setLine('result', e.message, 'err'));
