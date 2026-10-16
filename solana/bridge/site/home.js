// bridge.gno.world — the Home page: the machine (machine.js) fed by /api/<chain>/activity every ten seconds, the
// caption strip that says what is happening, the recent events (a click plays one), and the replay of the last real
// transfer once nothing has happened for a while; the value locked over time below it (chart.js). The story below is
// static, told in Ethereum's words on its pages (E-M4).
import { api, nav, pick, el, short, kindOf, words } from './common.js';
import { createMachine } from './machine.js';
import { mountLocked } from './chart.js';
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

// ETHEREUM is the story told of the Ethereum deployment (eth/bridge/PLAN.md §1 in plain words), by paragraph id:
// what changes from Solana's is the chain's finality, its tokens, its admin contract and what the code's review
// reads first.
const ETHEREUM = {
  lede: 'The bridge as it runs: gno.land\u2019s blocks along the top, Ethereum\u2019s blocks along the bottom, and every real transfer moving through the parts that check it. Read live from both chains.',
  'story-what': 'Send GNOT or BUBBLE from gno.land to Ethereum and back. On gno.land your coins go into a lock a realm holds — this bridge\u2019s own, apart from the Solana bridge\u2019s; on Ethereum you get the same amount of an ERC-20 token that stands for them, one to one. Burn that token on Ethereum and the lock pays you back on gno.land. Ethereum is the second chain, built the way the first was.',
  'story-check1': 'Every transfer to Ethereum carries a proof of a gno.land block. Ethereum\u2019s light client of gno.land accepts it only after checking that more than two thirds of gno.land\u2019s validators signed the block it came from; nobody\u2019s machine stands in that path.',
  'story-check2': 'Burns back are confirmed by three machines until a zero-knowledge proof of Ethereum\u2019s own consensus replaces them: two of the three must agree on a burn, and they sign it only once Ethereum has finalized its block, about 13 to 19 minutes after it was made. They can pay a claim that never happened — slowly, under a cap, until governance voids it — and they can stop; they can never mint. Two watchers compare both sides every ten seconds: the lock on gno.land must equal the supply on Ethereum plus what is owed. If it ever does not, payouts stop.',
  'story-stop': 'Four guardians hold pause keys and nothing else: one flag pauses, two flags hold. A hold lasts until the two lift it, or until governance replaces them — on the hold\u2019s 21st day at the earliest. GovDAO decides after a 7-day wait; until Jae yields to GovDAO, his key decides, with the same 7-day waits, and GovDAO\u2019s proposals are refused. A flag can delay a decision; it can never cancel one. There is no admin key on Ethereum: HlAdmin, the contract that carries out decisions, can never be changed and acts on nothing but a decision proven from a gno.land block, and the contracts it holds change only by such a decision, after its 7 days.',
  'story-limits1': 'Caps bound the loss. A day\u2019s payouts on gno.land are at most a tenth of the lock (never below a floor), one recipient gets a quarter of that, and a transfer is at most 25,000 per message. If the three machines stop, nobody leaves Ethereum until governance swaps them: 7 days plus the swap. GovDAO\u2019s two-thirds vote can replace gno.land\u2019s validators in one block, and nothing here stops it; the week of delays buys holders time to leave, about half the lock in seven days. Until the yield to GovDAO one key decides, so the lock stays small and no outside audit has read the code yet — the check of gno.land\u2019s signatures, written for Ethereum, comes first. A transfer can be late — a header must pass it, and a delivery waits out a spike in Ethereum\u2019s gas price — but it is not lost: a send waits in the lock, a burn waits as a claim.',
  'story-limits2': 'Two endings no rule here prevents. If more than two thirds of gno.land\u2019s validator keys were stolen, Ethereum could not tell a forged block from a real one: the guardians\u2019 hold and the 7-day wait give governance time to cancel each forged decision, the caps bound what leaves meanwhile, and the cure is a new validator set and a new deployment. If Ethereum\u2019s check of gno.land ever stops working — gno.land forks, or its validators change in a way the check cannot follow — sends to Ethereum stop for good while burns back keep working, so every holder can still come home, and governance deploys the bridge again.',
};

function sentence(e, chain, w) {
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
    case 'enact': return w && w.admin === 'HlAdmin' ? 'a decision reached HlAdmin on ' + chain : `a decision reached ${chain}'s admin program`;
  }
  return e.kind;
}

(async () => {
  const chain = await pick();
  if (kindOf(chain) === 'ethereum') for (const [id, text] of Object.entries(ETHEREUM)) document.getElementById(id).textContent = text;
  mountLocked($('locked'), chain);
  const cap = $('caption');
  const m = createMachine($('machine'), { chain: chain.name, kind: kindOf(chain), onCaption: (t) => { cap.textContent = t; }, onMode: () => offer() });
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
        el('span', { class: 'what' }, sentence(e, chain.name, words(chain)), e.link ? el('a', { href: e.link, target: '_blank', rel: 'noopener', onclick: (ev) => ev.stopPropagation() }, ' ↗') : ''),
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
