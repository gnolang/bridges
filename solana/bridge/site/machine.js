// bridge.gno.world — the machine (M7): the bridge drawn as it runs, fed by /api/<chain>/activity. gno.land's
// blocks stream along the top, Solana's slots along the bottom; between them the parts — the locks, the claims
// and their meter, the guardians and the governor on gno.land; the light client, the pausable gate, the mints and
// the admin program on Solana; the mailboxes, the updater, the relayer and the three signers in the middle — and
// every real event plays as a packet travelling the path it took. The page polls; this module draws.
//
// The paths are straight lanes: a send goes down its token's lane (lock → hlmailbox → relayer → Solana's
// mailbox → the gate → the mint), a burn comes up the lane on the right (mint → outbox → the signers vouch →
// relayer → hlmailbox → a claim → the meter → paid), headers go down the left (gno.land → updater → light client).
// prefers-reduced-motion: nothing moves — the path a packet takes is lit instead, and the caption says the same.

import { s, g, text, fit, seal, server, vault, coin, coinStack, envelope, block, person, keyIcon, flag, eye, lamp, gauge, gate, merkle, balance, along, pathLen, polyline, compact } from './draw.js';

const BLOCK_S = 3.43; // gno.land's block time (PLAN.md: ≈3.4 s)
const SLOT_S = 0.4; // Solana's slot
const reducedQuery = typeof matchMedia === 'function' ? matchMedia('(prefers-reduced-motion: reduce)') : { matches: false, addEventListener() {} };

// ------------------------------------------------------------------ layouts --
// A layout places the parts: each node a box {x, y, w, h}; the lanes are x positions; the bands the chains'. The
// left column is the headers' (gno.land's validators sign a block, the updater carries its header, the light
// client checks it); a send goes down its token's lane, a burn comes up the lane on the right.

function wide(compact) {
  const L = { kind: 'wide', W: 840, lanes: { GNOT: 300, BUBBLE: 384, up: 506, dec: 230, hdr: 104 } };
  if (!compact) {
    Object.assign(L, {
      H: 562,
      gno: { y: 0, h: 58, x0: 92, x1: 834, bw: 30, bh: 24, by: 9, pitch: 38 },
      sol: { y: 412, h: 58, x0: 92, x1: 834, sw: 6, sh: 14, sy: 423, pitch: 9 },
      n: {
        validators: { x: 6, y: 64, w: 196, h: 86 }, fees: { x: 210, y: 64, w: 46, h: 86 },
        lockGNOT: { x: 260, y: 64, w: 80, h: 86 }, lockBUBBLE: { x: 344, y: 64, w: 80, h: 86 }, meter: { x: 430, y: 64, w: 42, h: 86 }, claims: { x: 478, y: 64, w: 56, h: 86 },
        governor: { x: 548, y: 64, w: 70, h: 86 }, guardians: { x: 622, y: 64, w: 214, h: 86 },
        mbGno: { x: 210, y: 158, w: 350, h: 24 },
        updater: { x: 44, y: 190, w: 120, h: 72 }, relayer: { x: 210, y: 190, w: 350, h: 72 }, signers: { x: 572, y: 190, w: 264, h: 72 },
        mbSol: { x: 210, y: 270, w: 350, h: 24 },
        client: { x: 6, y: 302, w: 196, h: 102 }, gate: { x: 250, y: 306, w: 184, h: 12 },
        mintGNOT: { x: 260, y: 322, w: 80, h: 70 }, mintBUBBLE: { x: 344, y: 322, w: 80, h: 70 }, room: { x: 260, y: 396, w: 164, h: 6 },
        admin: { x: 572, y: 302, w: 264, h: 102 },
        balGNOT: { x: 6, y: 478, w: 410, h: 80 }, balBUBBLE: { x: 426, y: 478, w: 410, h: 80 },
      },
    });
  } else {
    Object.assign(L, {
      H: 352,
      gno: { y: 0, h: 46, x0: 92, x1: 834, bw: 26, bh: 20, by: 7, pitch: 34 },
      sol: { y: 304, h: 48, x0: 92, x1: 834, sw: 6, sh: 12, sy: 314, pitch: 9 },
      n: {
        validators: { x: 6, y: 52, w: 196, h: 70 },
        lockGNOT: { x: 260, y: 52, w: 80, h: 70 }, lockBUBBLE: { x: 344, y: 52, w: 80, h: 70 }, meter: { x: 430, y: 52, w: 42, h: 70 }, claims: { x: 478, y: 52, w: 56, h: 70 },
        mbGno: { x: 210, y: 128, w: 350, h: 20 },
        updater: { x: 44, y: 154, w: 120, h: 60 }, relayer: { x: 210, y: 154, w: 350, h: 60 }, signers: { x: 572, y: 154, w: 264, h: 60 },
        mbSol: { x: 210, y: 220, w: 350, h: 20 },
        client: { x: 6, y: 246, w: 196, h: 54 }, gate: { x: 250, y: 248, w: 184, h: 10 },
        mintGNOT: { x: 260, y: 262, w: 80, h: 38 }, mintBUBBLE: { x: 344, y: 262, w: 80, h: 38 },
      },
    });
  }
  return L;
}

function narrow(compact) {
  const L = { kind: 'narrow', W: 360, lanes: { GNOT: 96, BUBBLE: 165, up: 264, dec: 54, hdr: 22 } };
  if (!compact) {
    Object.assign(L, {
      H: 566,
      gno: { y: 0, h: 50, x0: 72, x1: 356, bw: 24, bh: 20, by: 8, pitch: 30 },
      sol: { y: 426, h: 50, x0: 72, x1: 356, sw: 5, sh: 12, sy: 436, pitch: 8 },
      n: {
        validators: { x: 4, y: 56, w: 116, h: 62 }, governor: { x: 124, y: 56, w: 62, h: 62 }, guardians: { x: 190, y: 56, w: 166, h: 62 },
        lockGNOT: { x: 62, y: 124, w: 68, h: 72 }, lockBUBBLE: { x: 132, y: 124, w: 66, h: 72 }, meter: { x: 202, y: 124, w: 34, h: 72 }, claims: { x: 238, y: 124, w: 52, h: 72 }, fees: { x: 294, y: 124, w: 62, h: 72 },
        mbGno: { x: 44, y: 202, w: 248, h: 20 },
        updater: { x: 4, y: 228, w: 36, h: 64 }, relayer: { x: 44, y: 228, w: 248, h: 64 }, signers: { x: 296, y: 228, w: 60, h: 64 },
        mbSol: { x: 44, y: 298, w: 248, h: 20 },
        client: { x: 4, y: 324, w: 54, h: 96 }, gate: { x: 64, y: 326, w: 134, h: 10 },
        mintGNOT: { x: 62, y: 340, w: 68, h: 66 }, mintBUBBLE: { x: 132, y: 340, w: 66, h: 66 }, room: { x: 66, y: 410, w: 128, h: 6 },
        admin: { x: 206, y: 324, w: 150, h: 96 },
        balGNOT: { x: 4, y: 482, w: 174, h: 80 }, balBUBBLE: { x: 182, y: 482, w: 174, h: 80 },
      },
    });
  } else {
    Object.assign(L, {
      H: 392,
      gno: { y: 0, h: 44, x0: 72, x1: 356, bw: 22, bh: 18, by: 7, pitch: 28 },
      sol: { y: 344, h: 48, x0: 72, x1: 356, sw: 5, sh: 12, sy: 354, pitch: 8 },
      n: {
        validators: { x: 4, y: 50, w: 54, h: 66 },
        lockGNOT: { x: 62, y: 50, w: 68, h: 66 }, lockBUBBLE: { x: 132, y: 50, w: 66, h: 66 }, meter: { x: 202, y: 50, w: 34, h: 66 }, claims: { x: 238, y: 50, w: 52, h: 66 },
        mbGno: { x: 44, y: 122, w: 248, h: 18 },
        updater: { x: 4, y: 146, w: 36, h: 58 }, relayer: { x: 44, y: 146, w: 248, h: 58 }, signers: { x: 296, y: 146, w: 60, h: 58 },
        mbSol: { x: 44, y: 210, w: 248, h: 18 },
        client: { x: 4, y: 234, w: 54, h: 104 }, gate: { x: 64, y: 236, w: 134, h: 10 },
        mintGNOT: { x: 62, y: 250, w: 68, h: 54 }, mintBUBBLE: { x: 132, y: 250, w: 66, h: 54 },
      },
    });
  }
  return L;
}

// ------------------------------------------------------------------ helpers --

const n0 = (v) => Number(v || 0);
function whole(units) { return (BigInt(units || 0) / 1000000n).toLocaleString('en-US'); }
function shortAddr(a, k = 5) { a = String(a || ''); return a.length > 2 * k + 1 ? a.slice(0, k) + '…' + a.slice(-k) : a; }
function hm(unix) { return unix ? new Date(unix * 1000).toISOString().slice(11, 19) + ' UTC' : ''; }
const cx = (b) => b.x + b.w / 2;
const cy = (b) => b.y + b.h / 2;

// plate: a node's ground, with its label lines under the icon
function plate(b, cls) { return s('rect', { class: 'plate ' + (cls || ''), x: b.x, y: b.y, width: b.w, height: b.h, rx: 6 }); }

// ------------------------------------------------------------------ the machine --

export function createMachine(host, opts = {}) {
  const m = {
    compact: !!opts.compact, chain: opts.chain || 'Solana', onCaption: opts.onCaption || (() => {}), onMode: opts.onMode || (() => {}),
    data: null, st: null, seen: new Set(), first: true, queue: [], cur: null, mode: 'live', L: null, r: {}, svg: null,
    H: 0, S: 0, lastFrame: 0, raf: 0, reduced: reducedQuery.matches, visible: true, pulses: [],
  };

  function layoutFor() {
    const w = host.clientWidth || 840;
    return w >= 700 ? wide(m.compact) : narrow(m.compact);
  }

  // build draws the static machine for the host's width and keeps the parts the data moves.
  function build() {
    const L = (m.L = layoutFor());
    const r = (m.r = {});
    const nar = L.kind === 'narrow';
    const svgEl = s('svg', { viewBox: `0 0 ${L.W} ${L.H}`, class: 'machine-svg ' + L.kind + (m.compact ? ' compact' : ''), role: 'img', 'aria-label': 'the bridge between gno.land and ' + m.chain + ', live', 'data-layout': L.kind });
    svgEl.style.maxWidth = (nar ? 520 : L.W) + 'px';
    const defs = s('defs', {},
      s('clipPath', { id: 'clip-gno-' + uid }, s('rect', { x: L.gno.x0, y: L.gno.y, width: L.gno.x1 - L.gno.x0, height: L.gno.h })),
      s('clipPath', { id: 'clip-sol-' + uid }, s('rect', { x: L.sol.x0, y: L.sol.y, width: L.sol.x1 - L.sol.x0, height: L.sol.h })),
      s('pattern', { id: 'slots-' + uid, x: 0, y: L.sol.y, width: L.sol.pitch, height: L.sol.h, patternUnits: 'userSpaceOnUse' }, s('rect', { class: 'slot', x: 1, y: L.sol.sy - L.sol.y, width: L.sol.sw, height: L.sol.sh, rx: 1 })));
    svgEl.append(defs);
    // the layers, bottom to top: the chains, the carriers and the mailboxes, the lanes, the parts, the packets
    const mid = g({ class: 'mid' }), lanes = g({ class: 'lanes' }), nodes = g({ class: 'nodes' }), packets = g({ class: 'packets' }), over = g({ class: 'over' });
    r.packets = packets; r.lanes = lanes;
    svgEl.append(bands(L, r), lanes, mid, nodes, packets, over); // the lanes run behind the parts, like pipes
    const N = L.n;
    const node = (id, cls, title, ...kids) => g({ class: 'node ' + cls, 'data-node': id }, title ? s('title', {}, title) : null, ...kids);
    // a node's two lines under its picture: a label and a muted line
    const lines = (b, lbl, sub) => [text(cx(b), b.y + b.h - (sub === null ? 8 : 18), lbl, 'lbl'), sub === null ? null : text(cx(b), b.y + b.h - 6, sub, 'sub')];

    // ---- gno.land's validators: every block is final once more than 2/3 of their power signs it
    if (N.validators) {
      const b = N.validators, tiny = b.w < 80;
      r.vals = g({ class: 'vals' });
      nodes.append(node('validators', 'validators', 'gno.land’s validators sign every block; a block is final once validators holding more than 2/3 of the voting power have signed it — what the light client checks', plate(b), r.vals,
        tiny ? text(cx(b), b.y + b.h - 18, 'validators', 'tiny') : text(cx(b), b.y + b.h - 18, 'gno.land validators', 'lbl'),
        tiny ? text(cx(b), b.y + b.h - 6, '> 2/3 sign', 'tiny') : text(cx(b), b.y + b.h - 6, nar ? '> 2/3 of the power signs' : 'sign each block: > 2/3 of the power', 'sub')));
    }
    // ---- gno.land: who decides, who can pause
    if (N.governor) {
      const b = N.governor, [l, sb] = lines(b, 'governor', '');
      r.govL = l; r.govS = sb;
      nodes.append(node('governor', 'gov', 'who decides: the governor’s key until it yields to GovDAO, GovDAO after; every decision waits 7 days and a guardian’s flag delays it', plate(b, 'brake'),
        person(cx(b) - 6, b.y + 26, 26, 'person'), keyIcon(cx(b) + 12, b.y + 33, 14), l, sb));
    }
    if (N.guardians) {
      const b = N.guardians;
      const gg = node('guardians', 'guardians', 'four guardians and two watch keys: one flag pauses, two flags hold; a flag delays a decision, it never cancels one', plate(b, 'brake'));
      r.flags = [];
      const step = (b.w - 12) / 6, top = b.y + 7;
      for (let i = 0; i < 6; i++) {
        const x = b.x + 6 + step * (i + 0.5);
        const who = i < 4 ? person(x - 3, top + 20, 15, 'person small') : eye(x - 3, top + 16, 12);
        const grp = g({ class: 'seat', 'data-seat': i + 1 }, who, g({ class: 'flagslot' }));
        gg.append(grp);
        r.flags.push({ grp, x: x + 5, y: top });
      }
      gg.append(...lines(b, 'guardians', nar ? '1 pauses · 2 hold' : '1 flag pauses · 2 hold'));
      nodes.append(gg);
    }
    if (N.fees) {
      const b = N.fees, [l, sb] = lines(b, 'fees', '');
      r.feesS = sb;
      nodes.append(node('fees', 'fees', 'the fee realm: a flat fee on each send, and the first installment of each claim', plate(b), coinStack(cx(b), b.y + 14, Math.min(24, b.w - 16), 3), l, sb));
    }
    // ---- the routes: a lock per token, the claims and their meter
    for (const sym of ['GNOT', 'BUBBLE']) {
      const b = N['lock' + sym];
      if (!b) continue;
      const vh = Math.min(30, b.h - 42);
      const [l, sb] = lines(b, '—', sym + ' lock');
      l.setAttribute('class', 'val');
      r['lockV' + sym] = l;
      nodes.append(node('lock' + sym, 'lock', `the ${sym} lock on gno.land: what is sent waits here; only a credited claim, paid under the meters, leaves it`, plate(b), vault(cx(b), b.y + 8 + vh / 2, Math.min(42, b.w - 22), vh), l, sb));
    }
    if (N.meter) {
      const b = N.meter, gh = b.h - 36;
      const [l, sb] = lines(b, 'meter', '');
      r.meterS = sb;
      r.meterG = gauge(cx(b) - 7, b.y + 7, 14, gh, 'brake', 0.25);
      nodes.append(r.meterNode = node('meter', 'meter', '', plate(b), r.meterG, l, sb));
      r.meterTitle = r.meterNode.querySelector('title') || r.meterNode.insertBefore(s('title', {}, ''), r.meterNode.firstChild);
    }
    if (N.claims) {
      const b = N.claims, cards = g({ class: 'cards' });
      for (let i = 0; i < 3; i++) cards.append(s('rect', { class: 'card', x: cx(b) - 12 + i * 3, y: b.y + 9 + i * 5, width: 20, height: 14, rx: 2 }));
      const [l, sb] = lines(b, 'claims', '');
      r.cards = cards; r.claimsS = sb;
      nodes.append(node('claims', 'claims', 'claims: a burn is credited as an IOU in the recipient’s queue, then paid in turns under the meters', plate(b), cards, l, sb));
    }
    // ---- the balances: the lock against what stands for it on the far side
    for (const sym of ['GNOT', 'BUBBLE']) {
      const b = N['bal' + sym];
      if (!b) continue;
      // the scale on the left, a number under each pan; on a wide layout the identity's words and numbers beside it
      const roomy = b.w > 300, sw = roomy ? 118 : Math.min(112, b.w - 40), sx = roomy ? b.x + 14 + sw / 2 : cx(b);
      const bal = balance(sx, b.y + 20, sw);
      r['bal' + sym] = bal;
      const tl = (x, y, v, cls, anchor, room) => { const t = text(x, y, v, cls, anchor); t.dataset.room = room; return t; };
      const pan = sw / 2 + 8;
      // narrow: a number under each pan; roomy: the pans' words, the numbers in the identity beside the scale
      const parts = [plate(b), bal,
        roomy ? null : (r['balL' + sym] = tl(sx - sw / 2 + 4, b.y + 54, '—', 'val', 'middle', pan)), tl(sx - sw / 2 + 4, b.y + (roomy ? 56 : 65), 'lock', 'sub', 'middle', pan),
        roomy ? null : (r['balR' + sym] = tl(sx + sw / 2 - 4, b.y + 54, '—', 'val', 'middle', pan)), tl(sx + sw / 2 - 4, b.y + (roomy ? 56 : 65), roomy ? 'supply + owed' : '+ owed', 'sub', 'middle', pan)];
      if (roomy) {
        const fx = b.x + sw + 62, room = b.x + b.w - fx - 8;
        parts.push(tl(fx, b.y + 20, 'lock = supply + owed + voided − lost', 'lbl', 'start', room),
          r['balF' + sym] = tl(fx, b.y + 38, '', 'val num', 'start', room),
          r['balS' + sym] = tl(fx, b.y + 56, sym, 'lbl', 'start', room),
          r['balW' + sym] = tl(fx, b.y + 70, '', 'sub', 'start', room));
      } else {
        parts.push(r['balS' + sym] = tl(cx(b), b.y + b.h - 4, sym, 'lbl', 'middle', b.w - 8));
      }
      nodes.append(node('bal' + sym, 'bal', `the ${sym} balance: lock = supply on ${m.chain} + owed + voided − lost; it tips while a transfer is in flight and levels when it lands`, ...parts));
    }
    // ---- the mailboxes and the carriers (under the lanes)
    const bar = (b, id, title, left, right) => node(id, 'bar', title, s('rect', { class: 'barbg', x: b.x, y: b.y, width: b.w, height: b.h, rx: b.h / 2 }),
      s('path', { class: 'split', d: `M${(L.lanes.BUBBLE + L.lanes.up) / 2},${b.y + 3}v${b.h - 6}` }),
      text(b.x + 12, b.y + b.h / 2 + 3.5, left, 'barl', 'start'), text(b.x + b.w - 10, b.y + b.h / 2 + 3.5, right, 'barl', 'end'));
    mid.append(bar(N.mbGno, 'mbGno', 'hlmailbox on gno.land: sends messages out; checks 2 of 3 signers on a burn coming in, and marks each id done once', nar ? 'hlmailbox ⇣' : 'hlmailbox · sends out ⇣', nar ? '⇡ 2 of 3' : '⇡ checks 2 of 3'));
    mid.append(bar(N.mbSol, 'mbSol', `the mailbox on ${m.chain}: processes a proven message once; records each burn toward gno.land`, nar ? 'mailbox ⇣' : m.chain + ' mailbox ⇣', '⇡ burns'));
    {
      const b = N.updater, hh = Math.min(34, b.h - (nar ? 18 : 30));
      mid.append(node('updater', 'updater', 'the updater carries gno.land’s headers to the light client — anyone may; a forged header fails the check', server(b.x + (nar ? 2 : 6), b.y + 4, b.w - (nar ? 4 : 12), hh, 'carrier'),
        nar ? text(cx(b), b.y + b.h - 4, 'updater', 'tiny') : null, nar ? null : text(cx(b), b.y + b.h - 16, 'updater', 'lbl'), nar ? null : text(cx(b), b.y + b.h - 4, 'carries headers', 'sub')));
    }
    {
      const b = N.relayer, hh = Math.min(38, b.h - 24), split = (L.lanes.BUBBLE + L.lanes.up) / 2;
      const rel = node('relayer', 'relayer', 'the relayer carries messages both ways: down, a send with its proof; up, a burn with the signers’ signatures. It cannot invent either', server(b.x + 2, b.y + 4, b.w - 4, hh, 'carrier'));
      const d = Math.max(4, Math.min(9, hh * 0.22)), fy = b.y + 4 + d;
      rel.append(s('rect', { class: 'door', x: b.x + 12, y: fy + 4, width: split - b.x - 22, height: hh - d - 8, rx: 2 }), s('rect', { class: 'door', x: split + 6, y: fy + 4, width: b.x + b.w - split - 18 - d, height: hh - d - 8, rx: 2 }));
      if (!nar) rel.append(text(b.x + 18, fy + (hh - d) / 2 + 3.5, 'proves sends ⇣', 'barl', 'start'), text(split + 12, fy + (hh - d) / 2 + 3.5, '⇡ vouched burns', 'barl', 'start'));
      else rel.append(text(b.x + 22, fy + (hh - d) / 2 + 4, 'proofs ⇣', 'barl', 'start'), text(split + 10, fy + (hh - d) / 2 + 4, '⇡ burns', 'barl', 'start'));
      rel.append(text(nar ? cx(b) : b.x + 6, b.y + b.h - 5, nar ? 'relayer' : 'relayer · anyone may relay', 'lbl', nar ? 'middle' : 'start'));
      mid.append(rel);
    }
    {
      const b = N.signers;
      const sg = node('signers', 'signers', 'three signer machines, each reading ' + m.chain + ' through two providers: a burn is credited on gno.land only when 2 of the 3 vouch for it, and each stops signing if the supply would pass the lock');
      r.lamps = [];
      // the label on top, so a path arriving from below crosses nothing; each machine's word under its lamp
      if (!nar) sg.append(r.sigL = text(b.x + 6, b.y + 10, 'signers · 2 of 3 must vouch for a burn', 'lbl', 'start'));
      for (let i = 0; i < 3; i++) {
        const w = nar ? b.w - 4 : 70, hh = nar ? 14 : Math.min(28, b.h - 40);
        const x = nar ? b.x + 2 : b.x + 6 + i * 86, y = nar ? b.y + 2 + i * 16 : b.y + 16;
        const lp = lamp(x + (nar ? 8 : 13), y + hh * (nar ? 0.6 : 0.62), nar ? 3 : 4.5);
        const word = nar ? null : text(x + w / 2 - 4, y + hh + 12, '', 'tiny');
        r.lamps.push({ lamp: lp, word });
        sg.append(server(x, y, w, hh, 'trust'), lp, word);
      }
      if (nar) sg.append(r.sigL = text(cx(b), b.y + b.h - 4, '', 'tiny', 'middle'));
      mid.append(sg);
    }
    // ---- Solana: the light client, the gate and the mints, the admin program
    {
      const b = N.client;
      const cg = node('client', 'client', 'the light client on ' + m.chain + ': it accepts a gno.land header only when validators holding more than 2/3 of the voting power signed it, and checks every proof against such a header', plate(b, 'check'));
      if (!nar) {
        cg.append(r.clientSeal = seal(b.x + 20, b.y + 19, 11, 'check'), text(b.x + 38, b.y + 16, 'light client', 'lbl', 'start'), r.clientT = text(b.x + 38, b.y + 30, '', 'val', 'start'));
        if (!m.compact) {
          r.powers = g({ class: 'powers' });
          cg.append(r.powers, r.clientS = text(b.x + 10, b.y + b.h - 7, 'a header needs > 2/3 of the power', 'sub', 'start'));
        }
      } else {
        cg.append(r.clientSeal = seal(cx(b), b.y + 16, 10, 'check'), text(cx(b), b.y + 40, 'light', 'lbl'), text(cx(b), b.y + 52, 'client', 'lbl'), text(cx(b), b.y + 66, 'trusts', 'tiny'), r.clientT = text(cx(b), b.y + 77, '', 'tiny'));
      }
      nodes.append(cg);
    }
    {
      const b = N.gate;
      r.gate = gate(b.x, b.y, b.w, b.h);
      nodes.append(node('gate', 'gatenode', 'the pausable gate: a delivery passes only under the per-transfer maximum and the day cap, and while no guardian’s flag stands', r.gate,
        nar || m.compact ? null : text(b.x + b.w + 8, b.y + b.h - 1, 'pausable gate', 'barl', 'start')));
      if (N.client) mid.append(polyline([[N.client.x + N.client.w, N.client.y + (nar ? 16 : 22)], [b.x - 4, b.y + b.h / 2]], 'link check'));
    }
    for (const sym of ['GNOT', 'BUBBLE']) {
      const b = N['mint' + sym];
      if (!b) continue;
      const [l, sb] = lines(b, '—', m.compact ? null : nar ? sym : sym + ' on ' + m.chain);
      l.setAttribute('class', 'val');
      r['mintV' + sym] = l;
      nodes.append(node('mint' + sym, 'mint', `the ${sym} mint on ${m.chain}: it mints only what a proven delivery carries, and burns what goes back`, plate(b), coinStack(cx(b), b.y + 8, Math.min(28, b.w - 30), m.compact ? 2 : 3), l, sb));
    }
    if (N.room) {
      const b = N.room;
      r.room = s('rect', { class: 'roomfill', x: b.x, y: b.y, width: 0, height: b.h, rx: b.h / 2 });
      nodes.append(r.roomNode = node('room', 'room', '', s('rect', { class: 'roomtrack', x: b.x, y: b.y, width: b.w, height: b.h, rx: b.h / 2 }), r.room,
        nar ? null : text(b.x + b.w + 8, b.y + b.h, 'day room', 'tiny', 'start')));
      r.roomTitle = r.roomNode.querySelector('title') || r.roomNode.insertBefore(s('title', {}, ''), r.roomNode.firstChild);
    }
    if (N.admin) {
      const b = N.admin;
      nodes.append(node('admin', 'admin', 'the admin program holds every authority on ' + m.chain + '; it has no key and acts only on a decision proven from a gno.land block, after its delay', plate(b, 'brake'),
        vault(nar ? cx(b) : b.x + 34, b.y + (nar ? 24 : 30), nar ? 34 : 40, nar ? 26 : 30),
        nar ? text(cx(b), b.y + 52, 'admin program', 'lbl') : text(b.x + 64, b.y + 22, 'admin program', 'lbl', 'start'),
        nar ? text(cx(b), b.y + 64, 'no key', 'sub') : text(b.x + 64, b.y + 36, 'no key: acts only on decisions', 'sub', 'start'),
        nar ? null : text(b.x + 64, b.y + 48, 'proven from a gno.land block', 'sub', 'start'),
        r.adminS = text(nar ? cx(b) : b.x + 10, b.y + b.h - 8, '', 'sub', nar ? 'middle' : 'start')));
    }
    // ---- the lanes: the paths the packets take, drawn faint
    r.paths = paths(L);
    for (const pts of Object.values(r.paths)) lanes.append(polyline(pts, 'lane'));
    // ---- the replay's banner
    // over the Solana stream's right end, where it covers no part
    r.banner = g({ class: 'banner hidden' }, s('rect', { x: L.W - 198, y: L.sol.y + 3, width: 192, height: 16, rx: 8 }), r.bannerT = text(L.W - 102, L.sol.y + 15, 'REPLAY', 'bannert'));
    over.append(r.banner);
    host.replaceChildren(svgEl);
    m.svg = svgEl;
    // every label fits its part: its room is the part's width less its offset
    for (const t of svgEl.querySelectorAll('text')) {
      if (t.dataset.room) { fit(t, Number(t.dataset.room)); continue; }
      const nodeEl = t.closest('[data-node]');
      const key = nodeEl && nodeEl.getAttribute('data-node');
      const box = key && N[key];
      if (!box || key === 'gate' || key === 'room') continue;
      const anchor = t.getAttribute('text-anchor'), x = n0(t.getAttribute('x'));
      const room = anchor === 'start' ? box.x + box.w - x - 3 : anchor === 'end' ? x - box.x - 3 : 2 * Math.min(x - box.x, box.x + box.w - x) - 4;
      t.dataset.room = Math.max(8, room);
      fit(t, Number(t.dataset.room));
    }
    if (m.data) apply(m.data);
    frame(performance.now(), true);
  }

  const uid = Math.random().toString(36).slice(2, 8);

  // bands are the two chains: gno.land's blocks and Solana's slots, each streaming past its label
  function bands(L, r) {
    const out = g({ class: 'bands' });
    const G = L.gno, S = L.sol;
    out.append(s('rect', { class: 'band gnoband', x: 0, y: G.y, width: L.W, height: G.h, rx: 6 }), s('rect', { class: 'band solband', x: 0, y: S.y, width: L.W, height: S.h, rx: 6 }));
    const nar = L.kind === 'narrow';
    out.append(text(6, G.y + 17, 'gno.land', 'chain', 'start'), text(6, G.y + 30, nar ? '3.4 s/block' : 'a block ≈3.4 s', 'sub', 'start'), r.headT = text(6, G.y + 43, '', 'val', 'start'));
    out.append(text(6, S.y + 17, m.chain, 'chain', 'start'), text(6, S.y + 30, nar ? '0.4 s/slot' : 'a slot ≈0.4 s', 'sub', 'start'), r.slotT = text(6, S.y + 43, '', 'tiny', 'start'));
    const gs = g({ 'clip-path': `url(#clip-gno-${uid})` });
    r.blocks = [];
    const count = Math.ceil((G.x1 - G.x0) / G.pitch) + 2;
    for (let i = 0; i < count; i++) {
      const b = g({ class: 'gblock' }, block(0, G.by, G.bw, G.bh, 'blk'), g({ class: 'mark' }));
      r.blocks.push(b);
      gs.append(b);
    }
    r.trust = g({ class: 'trustpin' }, s('path', { class: 'pin', d: `M0,${G.by + G.bh + 2}v${G.h - G.by - G.bh - 6}` }), seal(0, G.by + G.bh + 7, 4, 'check'));
    gs.append(r.trust);
    out.append(gs, r.trustT = text(G.x0 + 4, G.y + G.h - 4, '', 'tiny check-t', 'start'));
    r.slots = s('rect', { class: 'slots', x: S.x0 - S.pitch, y: S.y, width: S.x1 - S.x0 + 2 * S.pitch, height: S.h, fill: `url(#slots-${uid})` });
    out.append(g({ 'clip-path': `url(#clip-sol-${uid})` }, r.slots));
    return out;
  }

  // the paths a packet takes, through the layout's lanes
  function paths(L) {
    const N = L.n, P = {};
    const gb = L.gno.y + L.gno.by + L.gno.bh;
    for (const sym of ['GNOT', 'BUBBLE']) {
      const x = L.lanes[sym], lock = N['lock' + sym], mint = N['mint' + sym];
      if (!lock || !mint) continue;
      P['send' + sym] = [[x, gb], [x, lock.y + 18]];
      P['mint' + sym] = [[x, lock.y + 24], [x, N.mbGno.y + N.mbGno.h / 2], [x, cy(N.relayer)], [x, N.mbSol.y + N.mbSol.h / 2], [x, N.gate.y + N.gate.h / 2], [x, mint.y + 14]];
      P['burn' + sym] = [[x + mint.w * 0.3, mint.y + 10], [x + mint.w * 0.3, N.gate.y + N.gate.h + 4], [L.lanes.up, N.gate.y + N.gate.h + 4], [L.lanes.up, N.mbSol.y + N.mbSol.h / 2]];
      P['pay' + sym] = [[cx(N.claims), N.claims.y + 30], [cx(N.meter), N.claims.y + 30], [x, lock.y + 24], [x, gb]];
    }
    const up = L.lanes.up, gap = N.mbSol.y - (N.signers.y + N.signers.h);
    // the signers read the burn from the outbox: up the lane, across under their machines
    P.vouch = [[up, N.mbSol.y + N.mbSol.h / 2], [up, N.mbSol.y - gap / 2], [cx(N.signers), N.mbSol.y - gap / 2], [cx(N.signers), N.signers.y + N.signers.h - 2]];
    P.credit = [[N.signers.x - 2, cy(N.relayer)], [up, cy(N.relayer)], [up, N.mbGno.y + N.mbGno.h / 2], [up, N.claims.y + N.claims.h - 4]];
    // a header: from its block, through the validators who signed it, the updater, to the light client
    const hx = L.lanes.hdr, sx = Math.max(hx, L.gno.x0 + 12);
    P.header = [[sx, gb], ...(sx !== hx ? [[sx, gb + 3], [hx, gb + 3]] : []), [hx, cy(N.validators)], [hx, N.updater.y + 6], [hx, N.updater.y + N.updater.h - 6], [hx, N.client.y + 8]];
    if (N.governor && N.admin) {
      // a decision: the governor's (GovDAO's) call, out through hlmailbox, down the relayer like a send, into the admin program
      const d = L.lanes.dec, gy = N.mbGno.y + N.mbGno.h / 2, sy = N.mbSol.y + N.mbSol.h / 2;
      const gx = Math.min(cx(N.governor), N.mbGno.x + N.mbGno.w - 8);
      P.decision = [[cx(N.governor), N.governor.y + N.governor.h - 2], [cx(N.governor), N.governor.y + N.governor.h + 4], [gx, N.governor.y + N.governor.h + 4], [gx, gy], [d, gy]];
      P.enact = [[d, gy], [d, cy(N.relayer)], [d, sy], [Math.min(cx(N.admin), N.mbSol.x + N.mbSol.w + 8), sy], [Math.min(cx(N.admin), N.mbSol.x + N.mbSol.w + 8), N.admin.y + 10]];
    }
    for (const k of Object.keys(P)) if (!P[k]) delete P[k];
    return P;
  }

  // ------------------------------------------------------------------ values --

  // apply puts a reading's figures on the parts.
  function apply(act) {
    const r = m.r, st = act.status, nar = m.L.kind === 'narrow';
    m.st = st;
    if (st) {
      const toks = Object.fromEntries((st.tokens || []).map((t) => [t.symbol, t]));
      for (const sym of ['GNOT', 'BUBBLE']) {
        const t = toks[sym];
        if (!t) continue;
        if (r['lockV' + sym]) setText(r['lockV' + sym], whole(t.lock));
        if (r['mintV' + sym]) setText(r['mintV' + sym], t.supply === null || t.supply === undefined ? '—' : whole(t.supply));
        if (r['bal' + sym]) {
          const right = BigInt(t.supply || 0) + BigInt(t.owed || 0) + BigInt(t.voided || 0) - BigInt(t.lost || 0);
          const gap = BigInt(t.lock || 0) - right;
          // the beam: level while the identity holds with nothing in flight; down on the lock's side while a
          // transfer is in flight (sent and not minted, burned and not credited); the other way when BROKEN
          const tilt = t.identity === 'BROKEN' ? 0.9 : gap > 0n ? -Math.min(0.8, 0.3 + Number(gap * 1000n / (BigInt(t.lock || 0) + 1n)) / 1000) : 0;
          r['bal' + sym].set(tilt);
          r['bal' + sym].setAttribute('data-identity', t.identity || 'unread');
          r['bal' + sym].setAttribute('data-gap', String(gap));
          if (r['balL' + sym]) setText(r['balL' + sym], whole(t.lock));
          if (r['balR' + sym]) setText(r['balR' + sym], t.supply === null || t.supply === undefined ? '—' : whole(right));
          const verdict = t.identity === 'BROKEN' ? 'BROKEN' : gap > 0n ? whole(gap) + ' in flight' : t.identity === 'holds' ? 'holds' : 'unread';
          setText(r['balS' + sym], sym + ' · ' + verdict);
          if (r['balF' + sym]) {
            setText(r['balF' + sym], `${whole(t.lock)} = ${t.supply === null || t.supply === undefined ? '—' : whole(t.supply)} + ${whole(t.owed)} + ${whole(t.voided)} − ${whole(t.lost)}`);
            setText(r['balW' + sym], t.identity === 'BROKEN' ? 'above the lock: payouts stop until governance voids it' : gap > 0n ? 'tipped while a transfer is on its way between the chains' : 'level: every token on ' + m.chain + ' is backed in the lock');
          }
          const tt = r['bal' + sym].closest('[data-node]').querySelector('title');
          if (tt && t.identityLine) tt.textContent = t.identityLine;
        }
      }
      const open = (st.tokens || []).reduce((a, t) => a + n0(t.openClaims), 0);
      if (r.claimsS) setText(r.claimsS, open + ' open');
      if (r.cards) r.cards.setAttribute('data-open', Math.min(3, open));
      if (r.feesS) setText(r.feesS, (st.tokens || []).map((t) => whole(t.fee)).join(' · '));
      const g0 = st.gno || {};
      if (r.govS) setText(r.govS, g0.daoGoverns ? 'decides, 7-day waits' : 'until the yield');
      if (r.govL) setText(r.govL, g0.daoGoverns ? 'GovDAO' : 'governor');
      const o = st.other || {};
      if (r.flags) {
        const up = new Set();
        for (const f of g0.flags || []) up.add(f.seat);
        for (const obj of [o.pausable, o.admin]) (obj && obj.flags || []).forEach((f, i) => { if (f) up.add(i + 1); });
        r.flags.forEach((f, i) => {
          const isUp = up.has(i + 1);
          if (f.grp.getAttribute('data-flag') !== (isUp ? 'up' : 'down')) f.grp.querySelector('.flagslot').replaceChildren(flag(f.x, f.y, 15, isUp));
          f.grp.setAttribute('data-flag', isUp ? 'up' : 'down');
          f.grp.setAttribute('data-empty', (g0.seats || [])[i] ? 'no' : 'yes');
        });
      }
      const flagged = o.pausable && ((o.pausable.flags || []).some((f) => f) || o.pausable.hold);
      const quarantine = o.client && o.client.quarantineEnd > Date.now() / 1000;
      if (r.gate) r.gate.set(flagged || quarantine ? 'closed' : 'open');
      const pend = (o.pending || []).length;
      if (r.adminS) setText(r.adminS, pend ? pend + ' decision' + (pend > 1 ? 's' : '') + (nar ? ' waiting' : ' waiting out their delay') : o.admin && o.admin.hold ? 'held by two guardians' : 'nothing pending');
      if (o.client) {
        setText(r.clientT, (nar ? '#' : 'trusts #') + n0(o.client.trustedHeight).toLocaleString('en-US'));
        if (r.powers && o.client.powers && o.client.powers.length) drawPowers(o.client.powers);
        if (r.vals && o.client.powers && o.client.powers.length && r.vals.getAttribute('data-n') !== String(o.client.powers.length)) drawValidators(o.client.powers);
      }
    }
    // the signers' lamps, each with its word (never colour alone)
    const lamps = act.signers || [];
    lamps.forEach((l, i) => {
      const lp = r.lamps && r.lamps[i];
      if (!lp) return;
      const was = lp.lamp.getAttribute('data-state');
      lp.lamp.set(l.state);
      if (lp.word) setText(lp.word, l.state);
      if (was !== l.state && l.state === 'vouching') pulse(lp.lamp);
    });
    if (r.lamps && !lamps.length) r.lamps.forEach((lp) => { lp.lamp.set('unknown'); if (lp.word) setText(lp.word, ''); });
    if (nar && r.sigL) {
      const v = lamps.filter((l) => l.state === 'vouching').length, d = lamps.filter((l) => l.state === 'down').length;
      setText(r.sigL, !lamps.length ? 'signers' : v ? v + ' vouch' + (d ? ' · ' + d + ' down' : '') : d ? d + ' down' : 'all up');
    }
  }

  // drawValidators draws gno.land's validators as the light client knows them: a seal each, sized by its power
  function drawValidators(powers) {
    const b = m.L.n.validators, total = powers.reduce((a, p) => a + p, 0) || 1, max = Math.max(...powers);
    const n = powers.length, room = b.w - 12, cell = room / n, y = b.y + (b.h - 30) / 2 + 4;
    const out = powers.map((p, i) => seal(b.x + 6 + cell * (i + 0.5), y, Math.max(2.5, Math.min(cell / 2 - 2, 11) * Math.sqrt(p / max)), 'check'));
    m.r.vals.replaceChildren(...out);
    m.r.vals.setAttribute('data-n', n);
    m.r.vals.setAttribute('data-power', total);
  }

  // drawPowers draws the light client's rule: the validators' power as one bar, a seal per validator, and the 2/3
  // mark a header's signatures must pass
  function drawPowers(powers) {
    const b = m.L.n.client, total = powers.reduce((a, p) => a + p, 0) || 1;
    const x0 = b.x + 10, w = b.w - 20, y = b.y + 48, h = 16;
    const out = [];
    let x = x0;
    powers.forEach((p) => {
      const pw = w * p / total;
      out.push(s('rect', { class: 'vpower', x: x + 0.5, y, width: Math.max(1, pw - 1), height: h, rx: 2 }));
      if (pw >= 10) out.push(seal(x + pw / 2, y + h / 2, Math.min(5.5, pw / 2 - 2), 'check'));
      x += pw;
    });
    out.push(s('path', { class: 'third', d: `M${x0 + w * 2 / 3},${y - 4}v${h + 8}` }), text(x0 + w * 2 / 3 - 3, y - 6, '2/3', 'tiny', 'end'), text(x0 + w, y - 6, powers.length + ' validators', 'tiny', 'end'));
    m.r.powers.replaceChildren(...out);
    m.r.powers.setAttribute('data-validators', powers.length);
  }
  // setText changes a label and fits it to its room again
  function setText(t, v) {
    if (!t || t.dataset.v === v) return;
    t.dataset.v = v;
    t.textContent = v;
    if (t.dataset.room) { t.style.fontSize = ''; fit(t, Number(t.dataset.room)); }
  }

  function pulse(el) {
    if (m.reduced || !el) return;
    el.classList.remove('pulse');
    void el.getBBox; // restart the animation
    el.classList.add('pulse');
    setTimeout(() => el.classList.remove('pulse'), 900);
  }

  function nodeEl(id) { return m.svg && m.svg.querySelector(`[data-node="${id}"]`); }

  // ------------------------------------------------------------------ the streams --

  function headFigures() {
    const st = m.st;
    if (!st || !st.gno) return null;
    const o = st.other || {};
    return { h: n0(st.gno.height), ht: n0(st.gno.time) * 1000, slot: n0(o.slot), st: n0(o.at || st.at) * 1000, trusted: n0(o.client && o.client.trustedHeight) };
  }

  function frame(now, force) {
    m.raf = 0;
    const L = m.L, r = m.r;
    if (!L) return;
    const dt = Math.min(0.25, Math.max(0, (now - (m.lastFrame || now)) / 1000));
    m.lastFrame = now;
    const f = headFigures();
    if (f) {
      // gno.land's head: the reading's, run forward at the block time, never back, at most four blocks past it
      const target = f.h + Math.min(4, Math.max(0, (Date.now() - f.ht) / 1000 / BLOCK_S));
      if (!m.H || target - m.H > 12 || m.reduced) m.H = target;
      else if (m.H < target) m.H = Math.min(target, m.H + dt / BLOCK_S * 1.6);
      const S = f.slot + Math.min(60, Math.max(0, (Date.now() - f.st) / 1000 / SLOT_S));
      if (!m.S || S - m.S > 80 || m.reduced) m.S = S;
      else if (m.S < S) m.S = Math.min(S, m.S + dt / SLOT_S * 1.6);
      drawStreams(f);
    }
    if (m.cur) stepAnim(now);
    if (!m.reduced && m.visible) schedule();
  }

  function schedule() { if (!m.raf) m.raf = requestAnimationFrame(frame); }

  function drawStreams(f) {
    const L = m.L, r = m.r, G = L.gno, S = L.sol;
    const H = m.reduced ? Math.floor(m.H) : m.H;
    const top = Math.floor(H), frac = H - top;
    const marks = sendHeights();
    r.blocks.forEach((b, i) => {
      const h = top - i;
      const x = G.x1 - G.bw - (frac + i) * G.pitch;
      b.setAttribute('transform', `translate(${x.toFixed(1)},0)`);
      if (b.getAttribute('data-h') !== String(h)) {
        b.setAttribute('data-h', h);
        const mk = b.querySelector('.mark');
        mk.replaceChildren(...(marks.has(h) ? [coin(G.bw / 2, G.by + G.bh / 2 + 2, 6, marks.get(h))] : []));
        b.classList.toggle('head', i === 0);
      }
    });
    setText(r.headT, '#' + top.toLocaleString('en-US'));
    // Solana's view of gno.land: a pin on the block the light client trusts, or the lag at the window's edge
    const lag = top - f.trusted;
    const px = G.x1 - G.bw / 2 - (frac + lag) * G.pitch;
    if (f.trusted && px >= G.x0 + 4) {
      r.trust.setAttribute('transform', `translate(${px.toFixed(1)},0)`);
      r.trust.classList.remove('hidden');
      setText(r.trustT, '');
    } else {
      r.trust.classList.add('hidden');
      setText(r.trustT, f.trusted ? `◂ ${m.chain} trusts #${f.trusted.toLocaleString('en-US')} · ${Math.max(0, lag).toLocaleString('en-US')} blocks behind` : '');
    }
    m.svg.setAttribute('data-head', top);
    m.svg.setAttribute('data-trusted', f.trusted);
    const sTop = Math.floor(m.reduced ? m.S : m.S), sFrac = m.reduced ? 0 : m.S - sTop;
    r.slots.setAttribute('x', (S.x0 - S.pitch - sFrac * S.pitch).toFixed(1));
    setText(r.slotT, '#' + sTop.toLocaleString('en-US'));
    // the meters refill between readings (the bridge meter at a day cap a day; the gate's bucket drains)
    gauges();
  }

  // the sends of the reading by block height, so a block carrying one shows its coin
  function sendHeights() {
    const out = new Map();
    for (const e of (m.data && m.data.events) || []) if (e.kind === 'send' && e.height) out.set(e.height, e.token);
    return out;
  }

  function gauges() {
    const st = m.st, r = m.r;
    if (!st) return;
    const now = Date.now() / 1000;
    const toks = st.tokens || [];
    if (r.meterG && toks.length) {
      // the bridge meter of the token with the most room relative to its cap, refilled since the reading
      let best = 0, label = '', title = [];
      for (const t of toks) {
        const cap = n0(t.cap), lvl = Math.min(cap, n0(t.meter) + cap / 86400 * Math.max(0, now - n0(st.at)));
        const f = cap ? lvl / cap : 0;
        title.push(`${t.symbol}: ${whole(BigInt(Math.floor(lvl)))} of ${whole(t.cap)} a day, a quarter of it per recipient`);
        if (f >= best) { best = f; label = Math.round(f * 100) + '%'; }
      }
      r.meterG.set(best);
      setText(r.meterS, label);
      setText(r.meterTitle, 'the payout meter — ' + title.join('; '));
    }
    if (r.room && toks.length) {
      const b = m.L.n.room;
      const t = toks.find((x) => x.limit) || toks[0];
      if (t && t.limit) {
        const cap = n0(t.limit.dayCap), used = Math.max(0, n0(t.limit.used) - cap / 86400 * Math.max(0, now - n0(st.at)));
        const f = cap ? Math.max(0, 1 - used / cap) : 0;
        r.room.setAttribute('width', (b.w * f).toFixed(1));
        r.room.parentNode.setAttribute('data-level', f.toFixed(3));
        setText(r.roomTitle, `the gate’s day bucket: room for ${whole(BigInt(Math.floor(cap - used)))} of ${whole(t.limit.dayCap)} today, at most ${whole(t.limit.maxPerMessage)} a transfer`);
      }
    }
  }

  // ------------------------------------------------------------------ animations --
  // An animation is a list of steps; a step carries a shape along a path (or, without motion, lights the path),
  // sets the caption when it starts, and runs its effect when it ends.

  const DUR = 1400;

  function carryShape(kind, ev) {
    switch (kind) {
      case 'coin': return coin(0, 0, 7, ev && ev.token);
      case 'header': return g({}, block(-9, -7, 18, 14, 'blk hdrpkt'), seal(8, -7, 4.5, 'check'));
      case 'proof': return g({}, envelope(0, 0, 16, 11), merkle(10, -20, 22, 16, (ev && ev.proof && ev.proof.total) || 2, (ev && ev.proof && ev.proof.index) || 0));
      case 'vouched': return g({}, envelope(0, 0, 16, 11), seal(-9, -8, 4, 'trust'), seal(0, -10, 4, 'trust'));
      case 'card': return g({ class: 'cardpkt' }, s('rect', { x: -9, y: -6, width: 18, height: 12, rx: 2 }), s('path', { d: 'M-4,0l3,3l5,-6' }));
      case 'doc': return g({ class: 'doc' }, s('rect', { x: -6, y: -8, width: 12, height: 16, rx: 1.5 }), s('path', { d: 'M-3,-3h6M-3,0h6M-3,3h4' }));
      case 'seal': return seal(0, 0, 5, 'trust');
    }
    return envelope(0, 0, 14, 10);
  }

  function enqueue(anim) {
    m.queue.push(anim);
    if (!m.cur) next();
  }

  function next() {
    m.cur = m.queue.shift() || null;
    if (!m.cur) {
      if (m.mode === 'replay') { m.mode = 'live'; m.r.banner.classList.add('hidden'); m.onMode('live'); }
      m.svg && m.svg.setAttribute('data-playing', '');
      idleCaption();
      return;
    }
    m.cur.i = -1;
    m.svg.setAttribute('data-playing', m.cur.kind);
    if (m.cur.replay && m.mode !== 'replay') { m.mode = 'replay'; m.r.banner.classList.remove('hidden'); m.onMode('replay'); }
    if (m.cur.replay) setText(m.r.bannerT, m.cur.replay);
    nextStep(performance.now());
    schedule();
  }

  function nextStep(now) {
    const a = m.cur;
    if (a.step && a.step.end) a.step.end();
    clearLit();
    if (a.shape) { a.shape.remove(); a.shape = null; }
    a.i++;
    if (a.i >= a.steps.length) {
      a.done = true;
      if (m.reduced) setTimeout(next, 0); else next();
      return;
    }
    const st = (a.step = a.steps[a.i]);
    st.t0 = now;
    if (st.caption) m.onCaption(st.caption, a.kind);
    if (st.start) st.start();
    if (st.pts && st.pts.length > 1) {
      const lane = polyline(st.pts, 'lane lit');
      m.r.lanes.append(lane);
      a.lit = lane;
      if (!m.reduced) {
        a.shape = g({ class: 'pkt', 'data-kind': a.kind }, g({ transform: 'scale(1.4)' }, carryShape(st.carry, a.ev)));
        const p = st.pts[0];
        a.shape.setAttribute('transform', `translate(${p[0]},${p[1]})`);
        m.r.packets.append(a.shape);
      }
    }
    if (st.pulse) for (const id of st.pulse) pulse(nodeEl(id));
    if (m.reduced) {
      // no motion: the lit path stands for the step's time, then the next
      clearTimeout(a.timer);
      a.timer = setTimeout(() => { if (m.cur === a) nextStep(performance.now()); }, st.dur || DUR);
    }
  }

  function clearLit() {
    const a = m.cur;
    if (a && a.lit) { a.lit.remove(); a.lit = null; }
  }

  function stepAnim(now) {
    const a = m.cur, st = a && a.step;
    if (!st || m.reduced) return;
    const dur = st.dur || DUR;
    const f = Math.min(1, (now - st.t0) / dur);
    if (a.shape && st.pts) {
      const e = f < 0.5 ? 2 * f * f : 1 - Math.pow(-2 * f + 2, 2) / 2;
      const p = along(st.pts, e);
      a.shape.setAttribute('transform', `translate(${p[0].toFixed(1)},${p[1].toFixed(1)})`);
    }
    if (f >= 1) nextStep(now);
  }

  // ---- what each event looks like

  function fmtAmt(e) { return e.amount ? whole(e.amount) + ' ' + (e.token || '') : ''; }
  function blockNo(h) { return h ? 'block ' + Number(h).toLocaleString('en-US') : 'its block'; }

  // stepsFor are an event's steps: the path it took and the sentence that says what happened and why it is safe.
  function stepsFor(e, prefix = '', opts = {}) {
    const P = m.r.paths, sym = e.token === 'BUBBLE' ? 'BUBBLE' : 'GNOT';
    const T = (s) => prefix + s;
    switch (e.kind) {
      case 'send':
        return [{ pts: P['send' + sym], carry: 'coin', pulse: ['lock' + sym], caption: T(`${fmtAmt(e)} locked on gno.land in ${blockNo(e.height)}: it waits in the lock until a header past that block reaches ${m.chain}.`) }];
      case 'header':
        return [{ pts: P.header, carry: 'header', dur: 1800, pulse: ['client'], caption: T(`A header for gno.land ${blockNo(e.height)} reached ${m.chain}: the light client checked that validators holding more than 2/3 of the voting power signed it.`) }];
      case 'mint': {
        const pr = e.proof, T2 = (s) => (opts.proofPrefix !== undefined ? opts.proofPrefix : prefix) + s;
        const hdr = pr && pr.height ? `the header of ${blockNo(pr.height)}` : 'a header the light client holds';
        const proof = !pr ? `The relayer proves the send is among its block's results, under a header the light client trusts.`
          : pr.total > 1 ? `The relayer proves the send is among its block's ${pr.total} results: a merkle proof up ${pr.aunts} level${pr.aunts === 1 ? '' : 's'} to the hash ${hdr} carries.`
          : `The relayer proves the send: it is its block's only result, so its hash is the very one ${hdr} carries.`;
        return [
          { pts: P['mint' + sym].slice(0, 3), carry: 'proof', dur: 1500, pulse: ['relayer'], caption: T2(proof) },
          { pts: P['mint' + sym].slice(2), carry: 'proof', dur: 1500, pulse: ['client', 'gate', 'mint' + sym], caption: T(`${m.chain} checked the proof and let it through the gate: ${fmtAmt(e)} minted to ${shortAddr(e.to)}.`) },
        ];
      }
      case 'burn': {
        const steps = [{ pts: P['burn' + sym], carry: 'coin', pulse: ['mbSol'], caption: T(`${fmtAmt(e)} burned on ${m.chain}${e.nonce !== undefined ? ' (burn #' + e.nonce + ')' : ''}: the burn is final, and the three signers read it next.`) }];
        if (P.vouch) steps.push({ pts: P.vouch, carry: 'seal', dur: 1300, pulse: ['signers'], caption: T(vouchSentence(e)) });
        return steps;
      }
      case 'credit':
        return [{ pts: P.credit, carry: 'vouched', dur: 1600, pulse: ['mbGno', 'claims'], caption: T(`The relayer delivered the vouched burn: hlmailbox checked 2 of 3 signatures and marked it done, and a claim of ${fmtAmt(e)} was credited in ${blockNo(e.height)}.`) }];
      case 'payout':
        return [{ pts: P['pay' + sym], carry: 'coin', dur: 1600, pulse: ['meter', 'lock' + sym], caption: T(`Paid under the meter: ${fmtAmt(e)} left the lock for ${shortAddr(e.to)}${e.full ? '' : ' (an installment)'} — a tenth of the lock a day at most for everyone, a quarter of that per recipient.`) }];
      case 'decision':
        return P.decision ? [{ pts: P.decision, carry: 'doc', caption: T(`A decision was sent to ${m.chain}${e.what ? ' (' + e.what + ')' : ''}: the admin program will act on it only once the light client proves it came from gno.land.`) }] : [];
      case 'enact':
        return P.enact ? [{ pts: P.enact, carry: 'doc', dur: 1700, pulse: ['admin'], caption: T(`A decision reached the admin program, proven from a gno.land block; a 7-day kind waits out its week before it acts.`) }] : [];
    }
    return [];
  }

  function vouchSentence(e) {
    const n = (e.signers || []).length;
    const who = n ? `${n} of 3 signers vouch` : 'The signers check it';
    return `${who}${e.nonce !== undefined ? ' for burn #' + e.nonce : ''}: each reads it on ${m.chain} through two providers and signs only while the supply stays within the lock; 2 are needed.`;
  }

  function play(e, opts = {}) {
    const steps = stepsFor(e, opts.prefix || '', opts);
    if (!steps.length) return;
    enqueue({ kind: e.kind, ev: e, steps, replay: opts.replay });
  }

  // ---- the replay: the last real transfer each way, its real steps, time-compressed and labelled

  // stop drops what plays and what waits: a replay or a chosen step goes first
  function stop() {
    m.queue = [];
    if (m.cur) { if (m.cur.shape) m.cur.shape.remove(); clearLit(); clearTimeout(m.cur.timer); m.cur = null; }
  }

  function replay(last) {
    if (!last || (!last.out && !last.in)) return false;
    stop();
    const at = (w, k) => { const x = (w || []).find((s) => s.key === k); return x && x.time ? hm(x.time) + ' · ' : ''; };
    const o = last.out, i = last.in;
    const label = 'REPLAY · ' + hm((o && o.at) || (i && i.at)).replace(' UTC', '') + ' UTC';
    if (o) {
      const w = o.walk;
      const base = { token: o.token, amount: o.amount, to: o.to, height: o.height, id: o.id };
      play({ ...base, kind: 'send' }, { replay: label, prefix: 'Replay · ' + at(w, 'lock') });
      const hs = (w || []).find((s) => s.key === 'header');
      play({ kind: 'header', height: (hs && hs.height) || o.height + 1 }, { replay: label, prefix: 'Replay · ' + at(w, 'header') });
      if ((w || []).find((s) => s.key === 'mint' && s.done)) play({ ...base, kind: 'mint', proof: o.proof }, { replay: label, prefix: 'Replay · ' + at(w, 'mint'), proofPrefix: 'Replay · ' + at(w, 'proof') });
    }
    if (i) {
      const w = i.walk;
      const base = { token: i.token, amount: i.amount, to: i.to, height: i.height, id: i.id, nonce: i.nonce >= 0 ? i.nonce : undefined, signers: i.vouched && i.vouched.length ? i.vouched : [1, 2] };
      play({ ...base, kind: 'burn' }, { replay: label, prefix: 'Replay · ' + at(w, 'burn') });
      if ((w || []).find((s) => s.key === 'credit' && s.done)) play({ ...base, kind: 'credit' }, { replay: label, prefix: 'Replay · ' + at(w, 'credit') });
      if ((w || []).find((s) => s.key === 'pay' && s.done)) play({ ...base, kind: 'payout', full: true, amount: i.paid || i.amount }, { replay: label, prefix: 'Replay · ' + at(w, 'pay') });
    }
    return true;
  }

  // ---- the walkthrough's steps on the compact machine (the Bridge page)

  function walkStep(dir, key, tr) {
    const P = m.r.paths;
    const base = { token: tr.token, amount: tr.amount, to: tr.to, height: tr.height, nonce: tr.nonce >= 0 ? tr.nonce : undefined, signers: tr.vouched || [], proof: tr.proof };
    const one = (kind, steps) => enqueue({ kind, ev: base, steps });
    stop();
    if (m.mode === 'replay') { m.mode = 'live'; m.r.banner.classList.add('hidden'); m.onMode('live'); }
    if (dir === 'out') {
      if (key === 'lock' || key === 'final') return play({ ...base, kind: 'send' });
      if (key === 'header') { const hs = (tr.walk || []).find((s) => s.key === 'header'); return play({ kind: 'header', height: (hs && hs.height) || tr.height + 1 }); }
      if (key === 'proof') return one('mint', [stepsFor({ ...base, kind: 'mint' })[0]]);
      if (key === 'mint') return one('mint', [stepsFor({ ...base, kind: 'mint' })[1]]);
    } else {
      if (key === 'burn') return one('burn', [stepsFor({ ...base, kind: 'burn' })[0]]);
      if (key === 'vouch' && P.vouch) return one('burn', [{ pts: P.vouch, carry: 'seal', dur: 1300, pulse: ['signers'], caption: vouchSentence(base) }]);
      if (key === 'deliver' || key === 'credit') return play({ ...base, kind: 'credit' });
      if (key === 'pay') return play({ ...base, kind: 'payout', full: true });
    }
  }

  // ---- the caption while nothing plays

  function idleCaption() {
    const f = headFigures();
    if (!f) return;
    const lag = f.h - f.trusted;
    m.onCaption(`gno.land is at block ${f.h.toLocaleString('en-US')}; ${m.chain}'s light client trusts block ${f.trusted.toLocaleString('en-US')}, ${lag.toLocaleString('en-US')} behind — the updater brings a header every 10 minutes, or as soon as a transfer waits.`, 'idle');
  }

  // ------------------------------------------------------------------ the API --

  function update(act) {
    m.data = act;
    if (!m.svg) build();
    apply(act);
    const evs = act.events || [];
    const fresh = evs.filter((e) => !m.seen.has(e.key));
    for (const e of fresh) m.seen.add(e.key);
    if (m.first) {
      m.first = false;
      // the page opened just after something happened: play the newest
      const newest = evs[0];
      if (newest && Date.now() / 1000 - newest.time < 90) play(newest);
    } else {
      fresh.sort((a, b) => a.time - b.time).forEach((e) => play(e));
    }
    if (!m.cur) idleCaption();
    frame(performance.now(), true);
  }

  function lastEventAge() {
    const e = m.data && (m.data.events || [])[0];
    return e ? Date.now() / 1000 - e.time : Infinity;
  }

  const onReduce = () => {
    m.reduced = reducedQuery.matches;
    if (m.svg) m.svg.classList.toggle('still', m.reduced);
    schedule();
  };
  reducedQuery.addEventListener && reducedQuery.addEventListener('change', onReduce);
  let lastKind = '';
  const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(() => {
    const k = (host.clientWidth || 840) >= 700 ? 'wide' : 'narrow';
    if (k !== lastKind) { lastKind = k; build(); }
  }) : null;
  if (ro) ro.observe(host);
  if (typeof IntersectionObserver === 'function') {
    new IntersectionObserver((es) => { m.visible = es.some((x) => x.isIntersecting); if (m.visible) schedule(); }).observe(host);
  }
  document.addEventListener('visibilitychange', () => { if (!document.hidden) schedule(); });

  build();
  lastKind = m.L.kind;
  if (m.svg) m.svg.classList.toggle('still', m.reduced);

  return {
    update, replay, play, walkStep, lastEventAge,
    get mode() { return m.mode; },
    get playing() { return m.cur ? m.cur.kind : ''; },
    get layout() { return m.L && m.L.kind; },
  };
}
