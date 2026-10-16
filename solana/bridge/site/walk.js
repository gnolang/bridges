// bridge.gno.world — the transfer walkthrough (M7): a transfer's five steps each way, from the backend's facts
// (/api/<chain>/transfer/<ref>: walk, height, proof, vouched, meter), each with what happened — the real numbers
// and times — and why that step is safe. Each step has its little picture: the proof's tree, the signers' lamps,
// the meter and the time a claim still waits. The Bridge page ties the steps to its machine.
import { el } from './common.js';
import { s, merkle, lamp, gauge, seal, block } from './draw.js';

const units = (n) => {
  const v = BigInt(n || 0), w = (v / 1000000n).toLocaleString('en-US'), f = v % 1000000n;
  return f ? w + '.' + String(f).padStart(6, '0').replace(/0+$/, '') : w;
};
const hms = (t) => (t ? new Date(t * 1000).toISOString().slice(11, 19) + ' UTC' : '');
const num = (h) => Number(h || 0).toLocaleString('en-US');
const short = (a) => { a = String(a || ''); return a.length > 13 ? a.slice(0, 6) + '…' + a.slice(-5) : a; };

// STEPS: the walkthrough's words. what(tr, step) says what happened (or what waits); why says why it is safe;
// whatEth and whyEth are Ethereum's where its words differ.
export const STEPS = {
  out: [
    { key: 'lock', title: 'Locked in the realm',
      what: (tr, c) => `${units(tr.amount)} ${tr.token} went into the ${tr.token} lock on gno.land${tr.height ? ' in block ' + num(tr.height) : ''}.`,
      why: 'The realm takes coins only from your own signed call, and nothing leaves the lock but a credited claim, paid under the meters.' },
    { key: 'final', title: 'The block is final',
      what: (tr) => `${tr.height ? 'Block ' + num(tr.height) : 'The block'} was signed by validators holding more than 2/3 of gno.land's voting power.`,
      why: 'gno.land does not reorganize: once more than 2/3 have signed a block it cannot be undone, so the lock stands.' },
    { key: 'header', title: 'Its header reaches the far chain',
      what: (tr, c, st) => st.done ? `The updater carried the header of block ${num(st.height || tr.height + 1)} to ${c}; its light client checked the signatures and now trusts it.` : `Waiting for a header at or past block ${num(tr.height + 1)} to reach ${c}: ${c}'s light client trusts block ${num(tr.trusted)} now. The updater brings one as soon as a transfer waits.`,
      // Ethereum's (eth/bridge/PLAN.md §5.5, agent/due.go): a header costs millions of gas, so it is bought for waiting
      // sends only once their fees pay for it and their deliveries, or at the oldest's six hours within what earlier
      // fees left over (E3-FC5) — and nothing invites the reader to carry one (the site offers no self-delivery)
      whatEth: (tr, c, st) => st.done ? `The updater carried the header of block ${num(st.height || tr.height + 1)} to ${c}; its light client checked the signatures and now trusts it.` : `Waiting for a header at or past block ${num(tr.height + 1)} to reach ${c}: ${c}'s light client trusts block ${num(tr.trusted)} now. A header costs millions of gas on ${c}, so the updater buys one once the fees of the sends waiting pay for it and their deliveries, or after six hours from what earlier fees left over — longer while that is not enough.`,
      why: 'Anyone may carry headers and none is trusted: a forged header would need the keys of validators holding 2/3 of the power.',
      whyEth: 'No header is taken on trust, whoever carries it: a forged header would need the keys of validators holding 2/3 of the power.' },
    { key: 'proof', title: 'The relayer proves the transfer',
      what: (tr, c, st) => {
        const p = tr.proof;
        if (!st.done) return 'Waiting for the relayer to stage the proof that your send is in the block.';
        if (!p) return 'The relayer proved your send is among the block’s results.';
        const under = p.height ? `the header of block ${num(p.height)}` : 'a header the light client holds';
        return p.total > 1 ? `A merkle proof: your send's result is leaf ${p.index + 1} of ${p.total} under ${under}'s results hash, ${p.aunts} sibling hash${p.aunts === 1 ? '' : 'es'} up to the root.` : `Your send's result is the block's only one, so it is itself the results hash in ${under}.`;
      },
      why: 'The relayer cannot invent a transfer: the proof checks out only for a real result in a block the light client trusts.' },
    { key: 'mint', title: 'Minted to you',
      what: (tr, c, st) => st.done ? `${units(tr.amount)} ${tr.token} minted to ${short(tr.to)} on ${c}, through the pausable gate.` : `Waiting for the delivery: the proof, through the gate, into the ${tr.token} mint on ${c}.`,
      why: 'The gate bounds what one transfer and one day can mint, and a single guardian’s flag pauses it.' },
  ],
  in: [
    { key: 'burn', title: 'Burned on the far chain',
      what: (tr, c) => `${tr.amount ? units(tr.amount) + ' ' + tr.token : 'The tokens'} burned on ${c}${tr.to ? ' toward ' + short(tr.to) : ''}${tr.nonce >= 0 ? ' (burn #' + tr.nonce + ')' : ''}.`,
      why: 'A burn is final: the tokens are gone from the far chain, and its mailbox records the message for gno.land.',
      whyEth: 'The tokens are gone from Ethereum and its mailbox recorded the message for gno.land; once Ethereum finalizes the block — about 13 to 19 minutes — the burn cannot be undone.' },
    { key: 'vouch', title: '2 of 3 signers vouch',
      what: (tr, c, st) => {
        const v = (tr.vouched || []).length, f = tr.finality;
        if (!st.done && f && !f.final) { // Ethereum's: the signers sign nothing before the burn's block is final
          const left = Math.max(1, f.block - f.finalized);
          return `Waiting for ${c} to finalize block ${num(f.block)}: ${num(left)} block${left === 1 ? '' : 's'} to go, about ${Math.ceil(left * 12 / 60)} minute${left * 12 > 60 ? 's' : ''}. The signers sign only a final burn.`;
        }
        if (st.done && !v) return 'Two of the three signers vouched for the burn: gno.land credited it.';
        return `${v} of ${tr.signers || 3} signers ${v === 1 ? 'has' : 'have'} signed the burn${st.done ? '' : '; ' + (tr.threshold || 2) + ' are needed'}.`;
      },
      why: 'Each signer reads the burn through two providers and refuses while the supply would pass the lock: one bad machine credits nothing.',
      whyEth: 'Each signer reads the burn through two providers, signs only once its block is final, and refuses while the supply would pass the lock: one bad machine credits nothing.' },
    { key: 'deliver', title: 'The relayer delivers it',
      what: (tr, c, st) => st.done ? 'The relayer took the burn and the signatures to hlmailbox on gno.land.' : 'Waiting for the relayer to carry the vouched burn to gno.land.',
      why: 'hlmailbox checks 2 of 3 signatures and marks the id done: a burn is credited once, never twice.' },
    { key: 'credit', title: 'A claim is credited',
      what: (tr, c, st) => st.done ? `A claim of ${units(tr.amount)} ${tr.token} for ${short(tr.to)}${tr.height ? ', credited in block ' + num(tr.height) : ''}.` : 'Waiting for the credit.',
      why: 'An IOU in your queue, counted as owed, so the lock still equals the supply plus what is owed.' },
    { key: 'pay', title: 'Paid under the meter',
      what: (tr, c, st) => st.done ? `Paid in full: ${units(tr.paid || tr.amount)} ${tr.token} (the fee was the first installment).` : tr.paid ? `Paid ${units(tr.paid)} of ${units(tr.amount)} so far, in installments.` : 'Waiting its turn under the meters: the fee first, then the rest.',
      why: 'Payouts are metered — a tenth of the lock a day for everyone, a quarter of that for one recipient — so a forged claim cannot drain the lock before governance voids it.' },
  ],
};

// wait estimates the seconds a claim waits for its remainder: the bridge meter refills at its cap a day, and the
// recipient's at a quarter of it from empty at the claim's credit (an estimate: the chain does not render a
// recipient's meter).
export function wait(meter, creditAt, now = Date.now() / 1000) {
  if (!meter || !meter.cap) return 0;
  const rate = meter.cap / 86400, qrate = rate / 4;
  const level = Math.min(meter.cap, meter.level + rate * Math.max(0, now - meter.at));
  const tBridge = Math.max(0, (meter.remaining - level) / rate);
  const qlevel = Math.min(meter.recipient, qrate * Math.max(0, now - (creditAt || meter.at)));
  const tRecipient = Math.max(0, (meter.remaining - qlevel) / qrate);
  return Math.ceil(Math.max(tBridge, tRecipient));
}

export function duration(secs) {
  if (secs <= 0) return 'now';
  const d = Math.floor(secs / 86400), h = Math.floor(secs % 86400 / 3600), mi = Math.floor(secs % 3600 / 60), sec = Math.floor(secs % 60);
  if (d) return `${d} d ${h} h`;
  if (h) return `${h} h ${mi} min`;
  if (mi) return `${mi} min ${sec} s`;
  return `${sec} s`;
}

// picture is a step's small drawing.
function picture(dir, key, tr, st, now) {
  const svg = s('svg', { viewBox: '0 0 64 40', width: 64, height: 40, class: 'walk-svg', 'aria-hidden': 'true' });
  if (dir === 'out' && key === 'proof') svg.append(merkle(4, 2, 56, 36, (tr.proof && tr.proof.total) || 4, (tr.proof && tr.proof.index) || 1));
  else if (dir === 'out' && (key === 'header' || key === 'final')) svg.append(block(8, 8, 34, 24), ...[0, 1, 2].map((i) => seal(48 + (i % 2) * 8, 10 + i * 9, 4, 'check')));
  else if (dir === 'in' && key === 'vouch') {
    const v = new Set(tr.vouched || []);
    if (st.done && !v.size) { v.add(1); v.add(2); } // credited: two at least vouched; which two the chain does not say
    for (let i = 0; i < 3; i++) {
      const lp = lamp(12 + i * 20, 20, 6);
      lp.set(v.has(i + 1) ? 'vouching' : 'up');
      svg.append(lp);
    }
  } else if (dir === 'in' && key === 'pay' && tr.meter) {
    const gg = gauge(24, 2, 16, 36, 'brake', 0.25);
    const lvl = Math.min(tr.meter.cap, tr.meter.level + tr.meter.cap / 86400 * Math.max(0, now - tr.meter.at));
    gg.set(tr.meter.cap ? lvl / tr.meter.cap : 0);
    svg.append(gg);
  } else return null;
  return svg;
}

// render draws a transfer's walkthrough as an ordered list: done steps checked, the current one marked, the rest
// waiting; onPick(key) when a step is chosen; kind the chain's ("ethereum" says Ethereum's words where they
// differ). It returns the list and the current step's key.
export function render(tr, chain, onPick, kind) {
  const dir = tr.direction === 'in' ? 'in' : 'out';
  const walk = tr.walk || [];
  const steps = STEPS[dir];
  const now = Date.now() / 1000;
  let current = '';
  for (const st of steps) { const w = walk.find((x) => x.key === st.key); if (!w || !w.done) { current = st.key; break; } }
  const final = ['paid', 'delivered', 'failed', 'refunded', 'written off', 'voided', 'swept'].includes(tr.state);
  const ol = el('ol', { class: 'walk', 'data-dir': dir });
  for (const st of steps) {
    const w = walk.find((x) => x.key === st.key) || { key: st.key };
    const state = w.done ? 'done' : st.key === current && !final ? 'now' : 'later';
    const pic = picture(dir, st.key, tr, w, now);
    const when = w.time ? (w.exact ? '' : '≈') + hms(w.time) : '';
    const li = el('li', { class: state, 'data-key': st.key, tabindex: 0, title: 'show this step on the machine' },
      el('span', { class: 'mark', 'aria-hidden': 'true' }, state === 'done' ? '✓' : state === 'now' ? '●' : '○'),
      el('div', { class: 'body' },
        el('div', { class: 'head' }, el('b', {}, st.title.replace('the far chain', chain)), when ? el('span', { class: 't' }, when) : '', w.link ? el('a', { href: w.link, target: '_blank', rel: 'noopener', onclick: (e) => e.stopPropagation() }, '↗') : ''),
        el('div', { class: 'what' }, (kind === 'ethereum' && st.whatEth ? st.whatEth : st.what)(tr, chain, w)),
        dir === 'in' && st.key === 'pay' && tr.meter && !w.done ? el('div', { class: 'what eta', 'data-eta': '1' }, '') : '',
        el('div', { class: 'why' }, kind === 'ethereum' && st.whyEth ? st.whyEth : st.why)),
      pic ? el('span', { class: 'pic' }, pic) : '');
    const pick = () => onPick && onPick(st.key);
    li.addEventListener('click', pick);
    li.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick(); } });
    ol.append(li);
  }
  tickEta(ol, tr);
  return { list: ol, current };
}

// tickEta writes the pay step's time left, an estimate from the meters, refreshed every second by the page
export function tickEta(root, tr) {
  const n = root.querySelector('[data-eta]');
  if (!n || !tr.meter) return;
  const pay = (tr.walk || []).find((x) => x.key === 'credit');
  const secs = wait(tr.meter, pay && pay.time);
  const level = Math.min(tr.meter.cap, tr.meter.level + tr.meter.cap / 86400 * Math.max(0, Date.now() / 1000 - tr.meter.at));
  const tokens = (u) => Math.floor(u / 1e6).toLocaleString('en-US');
  n.textContent = secs > 0 ? `The meters free your remaining ${units(tr.meter.remaining)} ${tr.token} in about ${duration(secs)} (the bridge meter holds ${tokens(level)} of ${tokens(tr.meter.cap)} today).` : 'The meters have room now: the next Claim pays it.';
  n.dataset.secs = secs;
}
