// bridge.gno.world — How it works (M7): a transfer part by part, each scene drawn with the machine's own pieces
// (draw.js) and one or two sentences. A scene plays once when it comes into view — a packet along its path — and
// again when tapped; with reduced motion its path is lit instead and nothing moves.
import { nav, pick, kindOf } from './common.js';
import { s, g, text, seal, server, vault, coin, coinStack, envelope, block, person, keyIcon, flag, eye, lamp, gauge, gate, merkle, balance, along, polyline } from './draw.js';

nav('/how');

const reduce = typeof matchMedia === 'function' ? matchMedia('(prefers-reduced-motion: reduce)') : { matches: false };
const W = 300, H = 116;

// FAR is the far chain as the drawings name it: Solana's unless the page shows Ethereum (E-M4)
const FAR = { name: 'Solana', every: '≈0.4 s a slot', marks: 20, markW: 6, pitch: 9.4, record: 'outbox' };
const ETHEREUM_FAR = { name: 'Ethereum', every: '≈12 s a block', marks: 10, markW: 12, pitch: 18.8, record: 'mailbox' };

// ETHEREUM is each scene's text where Ethereum differs, by scene: its pace, its finality, its contracts.
const ETHEREUM = {
  0: 'gno.land makes a block about every 3.4 seconds, Ethereum one every 12. They share nothing but what the bridge carries between them.',
  1: 'You send GNOT or BUBBLE to the bridge\u2019s realm on gno.land. It sits in the lock; nothing on Ethereum exists yet.',
  3: 'The bridge\u2019s updater carries gno.land\u2019s headers to Ethereum. An update costs millions of gas there, so it buys one only when transfers wait — once their fees pay for it, or after six hours from what earlier fees left over. Ethereum\u2019s light client accepts a header only with those signatures — a forged one would need the keys of 2/3 of the power.',
  5: 'The delivery passes Ethereum\u2019s pausable gate — at most 25,000 a transfer and a day\u2019s cap, while no guardian\u2019s flag stands — and the token mints the same amount, one to one.',
  6: 'To come home, burn the token on Ethereum. Its mailbox records the burn for gno.land; once Ethereum finalizes the block — about 13 to 19 minutes — the burn cannot be undone.',
  7: 'Until a proof of Ethereum\u2019s own consensus arrives, three machines confirm each burn and 2 of the 3 must agree. Each reads Ethereum through two providers, signs only a final burn, and stops if the supply would pass the lock.',
  9: 'lock = supply on Ethereum + owed + voided − lost. Two watchers check it every ten seconds; it tips only while a transfer is on its way. If the right side ever rose above the lock, payouts would stop.',
  10: 'Four guardians hold pause keys: one flag pauses, two hold. Every decision waits 7 days, and a flag delays it — it never cancels one. On Ethereum no key can act; only a decision proven from gno.land.',
};

// each scene draws itself into its svg and answers the path its packet takes and what the packet is
const SCENES = [
  {
    title: 'Two chains',
    text: 'gno.land makes a block about every 3.4 seconds, Solana a slot every 0.4. They share nothing but what the bridge carries between them.',
    draw(svg) {
      svg.append(s('rect', { class: 'band gnoband', x: 0, y: 4, width: W, height: 40, rx: 6 }), s('rect', { class: 'band solband', x: 0, y: 72, width: W, height: 40, rx: 6 }),
        text(8, 21, 'gno.land', 'chain', 'start'), text(8, 34, '≈3.4 s a block', 'sub', 'start'), text(8, 89, FAR.name, 'chain', 'start'), text(8, 102, FAR.every, 'sub', 'start'));
      for (let i = 0; i < 6; i++) svg.append(block(108 + i * 31, 12, 26, 24));
      for (let i = 0; i < FAR.marks; i++) svg.append(s('rect', { class: 'slot', x: 108 + i * FAR.pitch, y: 85, width: FAR.markW, height: 14, rx: 1 }));
      return { path: [[121 + 5 * 31, 36], [121 + 5 * 31, 60], [170, 60], [170, 84]], carry: () => g({}, envelope(0, 0, 14, 10)) };
    },
  },
  {
    title: 'You lock',
    text: 'You send GNOT or BUBBLE to the bridge’s realm on gno.land. It sits in the lock; nothing on Solana exists yet.',
    draw(svg) {
      svg.append(block(126, 6, 48, 30), text(186, 25, 'your send, in a block', 'sub', 'start'), vault(150, 84, 58, 40), text(186, 88, 'the lock', 'lbl', 'start'));
      return { path: [[150, 30], [150, 80]], carry: () => coin(0, 0, 8, 'G') };
    },
  },
  {
    title: 'The validators sign the block',
    text: 'A gno.land block is final once validators holding more than 2/3 of the voting power have signed it. It cannot be undone.',
    draw(svg) {
      svg.append(block(18, 30, 70, 50));
      const xs = [150, 175, 200, 225, 250, 275];
      xs.forEach((x, i) => svg.append(seal(x, 40, 10, 'check'), i === 5 ? s('circle', { class: 'unsigned', cx: x, cy: 40, r: 10 }) : null));
      svg.append(s('path', { class: 'bracket', d: 'M146,62v6h112v-6' }), text(202, 84, '> 2/3 of the power signed', 'sub'), text(53, 98, 'block', 'lbl'));
      return { path: [[200, 40], [120, 52], [70, 52]], carry: () => seal(0, 0, 7, 'check') };
    },
  },
  {
    title: 'Its header crosses',
    text: 'Anyone may carry gno.land’s headers to Solana; the bridge’s updater does it. Solana’s light client accepts a header only with those signatures — a forged one would need the keys of 2/3 of the power.',
    draw(svg) {
      svg.append(block(10, 8, 46, 32), text(33, 54, 'header', 'sub'), server(112, 34, 76, 34, 'carrier'), text(150, 84, 'updater', 'lbl'),
        s('rect', { class: 'plate check', x: 222, y: 56, width: 74, height: 54, rx: 6 }), seal(259, 76, 12, 'check'), text(259, 104, 'light client', 'lbl'));
      return { path: [[33, 40], [33, 52], [150, 52], [150, 60], [259, 60], [259, 68]], carry: () => g({}, block(-9, -7, 18, 14, 'blk hdrpkt'), seal(8, -7, 4.5, 'check')) };
    },
  },
  {
    title: 'The relayer proves your transfer',
    text: 'Each header commits to the block’s results. The relayer shows that your transfer is one of them: a merkle proof from your result up to that hash. It cannot invent a transfer — the proof fails for anything that is not in the block.',
    draw(svg) {
      svg.append(merkle(30, 22, 240, 86, 8, 5), block(124, 0, 52, 18), text(182, 13, 'header · results hash', 'sub', 'start'), text(30, 113, 'the block’s results', 'sub', 'start'), text(270, 113, 'yours', 'sub', 'end'));
      // the proof's path, from the transfer's leaf (index 5 of 8) to the root
      const lvl = (l, i, count) => [30 + (i + 0.5) * 240 / count, 22 + 86 - (86 / 4) * (l + 0.5)];
      return { path: [lvl(0, 5, 8), lvl(1, 2, 4), lvl(2, 1, 2), lvl(3, 0, 1), [150, 9]], carry: () => s('circle', { class: 'proofdot', r: 4.5 }) };
    },
  },
  {
    title: 'The gate, then the mint',
    text: 'The delivery passes Solana’s pausable gate — at most 25,000 a transfer and a day’s cap, while no guardian’s flag stands — and the mint makes the same amount, one to one.',
    draw(svg) {
      const gt = gate(70, 44, 160, 14);
      svg.append(gt, text(240, 55, 'gate', 'lbl', 'start'), coinStack(150, 80, 40, 3), text(196, 98, 'the mint', 'lbl', 'start'));
      return { path: [[150, 6], [150, 50], [150, 78]], carry: () => envelope(0, 0, 18, 12) };
    },
  },
  {
    title: 'Back: you burn',
    text: 'To come home, burn the token on Solana. The burn is final; Solana’s mailbox records it for gno.land.',
    draw(svg) {
      svg.append(coinStack(60, 46, 40, 3), text(60, 98, 'the mint', 'lbl'), envelope(236, 58, 46, 32), text(236, 98, FAR.record, 'lbl'));
      return { path: [[80, 52], [150, 30], [216, 58]], carry: () => g({}, coin(0, 0, 8, 'G'), s('path', { class: 'burnmark', d: 'M-7,7L7,-7' })) };
    },
  },
  {
    title: 'Three signers vouch',
    text: 'Until Solana’s own proofs arrive, three machines confirm each burn and 2 of the 3 must agree. Each reads Solana through two providers and stops if the supply would pass the lock.',
    draw(svg) {
      svg.append(envelope(34, 58, 40, 28), text(34, 98, 'the burn', 'lbl'));
      const lamps = [];
      for (let i = 0; i < 3; i++) {
        const y = 4 + i * 31;
        const lp = lamp(170, y + 13, 4.5);
        svg.append(server(160, y, 88, 24, 'trust'), lp);
        lp.set(i < 2 ? 'vouching' : 'up');
        lamps.push(lp);
      }
      svg.append(text(204, 112, '2 of 3 vouch', 'lbl'));
      return { path: [[54, 58], [120, 58], [158, 24]], carry: () => seal(0, 0, 6, 'trust') };
    },
  },
  {
    title: 'A claim, paid under the meter',
    text: 'gno.land credits a claim and pays it under the meters: a tenth of the lock a day for everyone, a quarter of that for one recipient. A claim that never happened cannot drain the lock before governance voids it.',
    draw(svg) {
      const gg = gauge(140, 10, 22, 80, 'brake', 0.25);
      gg.set(0.6);
      svg.append(s('rect', { class: 'card', x: 20, y: 34, width: 44, height: 30, rx: 3 }), text(42, 82, 'claim', 'lbl'), gg, text(151, 108, 'meter', 'lbl'), person(250, 52, 34), text(250, 92, 'you', 'lbl'));
      return { path: [[64, 49], [140, 49], [162, 49], [232, 49]], carry: () => coin(0, 0, 8, 'G') };
    },
  },
  {
    title: 'The balance stays level',
    text: 'lock = supply on Solana + owed + voided − lost. Two watchers check it every ten seconds; it tips only while a transfer is on its way. If the right side ever rose above the lock, payouts would stop.',
    draw(svg) {
      const bal = balance(150, 30, 160);
      svg.append(bal, text(74, 92, 'lock', 'lbl'), text(226, 92, 'supply + owed', 'lbl'));
      this.bal = bal;
      return { tick: (f) => bal.set(f < 0.5 ? -0.5 * Math.sin(f * Math.PI * 2) : 0) };
    },
  },
  {
    title: 'Who can stop it',
    text: 'Four guardians hold pause keys: one flag pauses, two hold. Every decision waits 7 days, and a flag delays it — it never cancels one. On Solana no key can act; only a decision proven from gno.land.',
    draw(svg) {
      for (let i = 0; i < 4; i++) svg.append(person(30 + i * 40, 66, 26), flag(42 + i * 40, 30, 22, i === 1));
      svg.append(text(90, 104, 'guardians', 'lbl'), person(226, 66, 28), keyIcon(244, 74, 16), text(232, 104, 'governor', 'lbl'),
        s('circle', { class: 'clock', cx: 280, cy: 34, r: 14 }), s('path', { class: 'hand', d: 'M280,34v-9M280,34l6,4' }), text(280, 60, '7 days', 'sub'));
      return { path: [[54, 20], [54, 20]], carry: null };
    },
  },
];

function figure(sc, i) {
  const svg = s('svg', { viewBox: `0 0 ${W} ${H}`, class: 'walk-svg scene-svg', role: 'img', 'aria-label': sc.title });
  const fig = document.createElement('figure');
  fig.className = 'scene';
  fig.dataset.scene = i;
  fig.dataset.played = '0';
  const h = document.createElement('h3');
  const n = document.createElement('span');
  n.className = 'n';
  n.textContent = i + 1 + '.';
  h.append(n, sc.title);
  const p = document.createElement('p');
  p.textContent = sc.text;
  fig.append(svg, h, p);
  document.getElementById('scenes').append(fig);
  const plan = sc.draw.call(sc, svg) || {};
  let lane = null;
  if (plan.path && plan.path.length > 1 && plan.carry) { lane = polyline(plan.path, 'lane'); svg.insertBefore(lane, svg.firstChild); }
  const play = () => {
    fig.dataset.played = String(Number(fig.dataset.played) + 1);
    if (reduce.matches) { // nothing moves: the path is lit for a moment, the picture says the rest
      if (lane) { lane.classList.add('lit'); setTimeout(() => lane.classList.remove('lit'), 1600); }
      return;
    }
    const pkt = plan.carry ? g({ class: 'pkt' }, plan.carry()) : null;
    if (pkt) svg.append(pkt);
    if (lane) lane.classList.add('lit');
    const t0 = performance.now(), dur = 1700;
    const step = (now) => {
      const f = Math.min(1, (now - t0) / dur), e = f < 0.5 ? 2 * f * f : 1 - Math.pow(-2 * f + 2, 2) / 2;
      if (pkt && plan.path) { const q = along(plan.path, e); pkt.setAttribute('transform', `translate(${q[0].toFixed(1)},${q[1].toFixed(1)})`); }
      if (plan.tick) plan.tick(f);
      if (f < 1) requestAnimationFrame(step);
      else { if (pkt) pkt.remove(); if (lane) lane.classList.remove('lit'); if (plan.tick) plan.tick(1); }
    };
    requestAnimationFrame(step);
  };
  fig.addEventListener('click', play);
  return { fig, play };
}

(async () => {
  let eth = false;
  try { eth = kindOf(await pick()) === 'ethereum'; } catch (e) { /* the scenes stand in Solana's words */ }
  if (eth) Object.assign(FAR, ETHEREUM_FAR);
  const figs = SCENES.map((sc, i) => figure(eth && ETHEREUM[i] ? { ...sc, text: ETHEREUM[i] } : sc, i));
  if (typeof IntersectionObserver === 'function') {
    const io = new IntersectionObserver((es) => {
      for (const e of es) {
        if (!e.isIntersecting) continue;
        const f = figs.find((x) => x.fig === e.target);
        io.unobserve(e.target);
        if (f) f.play();
      }
    }, { threshold: 0.6 });
    figs.forEach((f) => io.observe(f.fig));
  }
})();
