// bridge.gno.world — the Bridge page: a chain, a direction, a token, an amount, a recipient; the wallet signs;
// the transfer is tracked here until it lands.
import { api, chains, el, fmt, whole, parseAmount, nav, pick, short, setLine } from './common.js';
import { adena, solanaWallet, isSolanaAddress, isGnoAddress, b64decode } from './wallets.js';

const $ = (id) => document.getElementById(id);
const state = { c: null, chain: null, dir: 'out', token: null, status: null, gnoAddr: '', solAddr: '', poll: null };

async function main() {
  nav('/bridge');
  state.c = await chains;
  state.chain = await pick();
  const sel = $('chain');
  for (const ch of state.c.chains) sel.append(el('option', { value: ch.id }, ch.name));
  sel.value = state.chain.id;
  sel.onchange = () => { location.search = '?chain=' + sel.value; };
  for (const t of state.chain.tokens) $('token').append(el('option', { value: t.symbol }, t.symbol));
  state.token = state.chain.tokens[0].symbol;
  $('token').onchange = () => { state.token = $('token').value; fees(); };
  $('out').onclick = () => setDir('out');
  $('in').onclick = () => setDir('in');
  $('send').onclick = () => send().catch((e) => setLine('result', e.message, 'err'));
  $('connect').onclick = () => connect().catch((e) => setLine('result', e.message, 'err'));
  $('trackgo').onclick = () => { const ref = $('trackref').value.trim(); if (ref) track(ref); };
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
    to.placeholder = state.chain.name + ' address of the recipient';
    $('connect').textContent = state.gnoAddr ? 'Adena: ' + short(state.gnoAddr, 6) : 'Connect Adena';
  } else {
    to.placeholder = 'gno.land address of the recipient (g1…)';
    $('connect').textContent = state.solAddr ? solanaWallet.name() + ': ' + short(state.solAddr, 6) : 'Connect ' + (solanaWallet.found() ? solanaWallet.name() : 'a Solana wallet');
  }
  setLine('result', '');
  fees();
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
  if (!isSolanaAddress(to)) throw new Error('the recipient must be a ' + state.chain.name + ' address (base58)');
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
  if (!state.solAddr) await connect();
  setLine('result', 'building the transaction…');
  const built = await api('/api/' + state.chain.id + '/burn-tx', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token: t.symbol, sender: state.solAddr, recipient: to, amount: amount.toString() }) });
  setLine('result', 'waiting for the wallet…');
  const sig = await solanaWallet.signAndSend(b64decode(built.tx), state.chain.cluster);
  setLine('result', `burned: ${fmt(amount)} ${t.symbol} toward ${to}; tracking ${short(sig)}`, 'ok');
  $('trackref').value = sig;
  track(sig);
}

// track polls one transfer every five seconds and draws its steps until it lands.
function track(ref) {
  if (state.poll) clearInterval(state.poll);
  const box = $('track');
  box.classList.remove('hidden');
  const draw = async () => {
    let tr;
    try { tr = await api('/api/' + state.chain.id + '/transfer/' + encodeURIComponent(ref)); } catch (e) {
      box.replaceChildren(el('div', { class: 'line' + (e.status === 404 ? '' : ' err') }, e.status === 404 ? 'not seen yet: ' + e.message : e.message));
      return;
    }
    const head = el('div', {}, el('b', {}, tr.direction === 'out' ? 'gno.land → ' + state.chain.name : state.chain.name + ' → gno.land'), ' · ', tr.token, tr.amount ? ' · ' + fmt(tr.amount) : '', ' · ', el('span', { class: tr.state === 'failed' || tr.state === 'voided' ? 'bad' : (tr.state === 'paid' || tr.state === 'delivered') ? 'ok' : '' }, tr.state));
    const steps = el('ul', { class: 'steps' }, tr.steps.map((s) => el('li', { class: s.done ? 'done' : '' }, el('span', { class: 'mark' }, s.done ? '✓' : '…'), el('span', {}, s.link ? el('a', { href: s.link, target: '_blank', rel: 'noopener' }, s.name) : s.name, ' ', el('span', { class: 'text' }, s.text || '')))));
    const idline = el('div', { class: 'line mono' }, 'message id ', tr.id || '—', tr.note ? ' · ' + tr.note : '');
    box.replaceChildren(head, steps, idline);
    if (['paid', 'delivered', 'failed', 'refunded', 'written off', 'voided', 'swept'].includes(tr.state)) { clearInterval(state.poll); state.poll = null; }
  };
  draw();
  state.poll = setInterval(draw, 5000);
}

main().catch((e) => setLine('result', e.message, 'err'));
