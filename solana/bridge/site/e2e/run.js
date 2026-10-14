#!/usr/bin/env node
// bridge.gno.world — the browser suite (bridge/SITE.md): the real backend in -mock mode serving the real pages,
// the wallets mocked inside the page (Adena; a Solana wallet as window.solana, and once as a Wallet Standard
// wallet), both flows end to end with the exact calls the wallets receive checked, the status, help and How it
// works pages, at desktop and phone width, a screenshot of each page in e2e/out. M7: the machine on Home drawn
// from the mock's figures, an event playing as it arrives, the walkthrough through every step both ways with the
// machine playing each, the replay of the last real transfer, reduced motion (nothing moves, the same figures),
// the labels inside their parts. The mock's world moves only when the suite steps it (POST /api/mock/step).
// Runs on the devbox (node 22, playwright from /root/src/node_modules): `make -C bridge site-qa`. Exit 0 on a pass.
'use strict';
const { chromium } = require('playwright');
const { spawn, spawnSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const BRIDGE = path.resolve(__dirname, '../..');
const SITE = path.resolve(__dirname, '..');
const OUT = path.join(__dirname, 'out');
const BIN = path.join(OUT, 'hlsite');
const WIDTHS = [{ name: 'desktop', width: 1280, height: 800 }, { name: 'phone', width: 390, height: 844, isMobile: true }];

const failures = [];
let passed = 0;
function check(cond, what) { if (cond) passed++; else failures.push(what); return cond; }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function waitFor(fn, what, timeout = 30000, every = 300) {
  const t0 = Date.now();
  for (;;) {
    let v;
    try { v = await fn(); } catch (e) { v = null; }
    if (v) return v;
    if (Date.now() - t0 > timeout) { failures.push('timed out: ' + what); return null; }
    await sleep(every);
  }
}

// mockWallets is injected before every page: Adena, and a Solana wallet in one of two shapes.
function mockWallets(mock, standard) {
  return `(() => {
    const mock = ${JSON.stringify(mock)};
    window.__calls = [];
    window.adena = {
      AddEstablish: async () => ({ code: 0, status: 'success' }),
      GetAccount: async () => ({ code: 0, status: 'success', data: { address: mock.sender, chainId: 'dev' } }),
      DoContract: async (req) => { window.__calls.push({ adena: req }); return { code: 0, status: 'success', data: { hash: mock.gnoTx } }; },
    };
    if (${standard ? 'true' : 'false'}) {
      const wallet = { name: 'MockStandard', features: {
        'standard:connect': { connect: async () => ({ accounts: [{ address: mock.alice, publicKey: new Uint8Array(32), chains: ['solana:devnet'] }] }) },
        'solana:signAndSendTransaction': { signAndSendTransaction: async ({ transaction, chain }) => {
          window.__calls.push({ standard: transaction.length, chain });
          return [{ signature: Uint8Array.from(atob(mock.burnSigBase64), (c) => c.charCodeAt(0)) }];
        } },
      } };
      window.addEventListener('wallet-standard:app-ready', (ev) => ev.detail.register(wallet));
    } else {
      window.solana = { isPhantom: true, publicKey: null,
        connect: async () => { const pk = { toString: () => mock.alice }; window.solana.publicKey = pk; return { publicKey: pk }; },
        signAndSendTransaction: async (tx) => { const bytes = tx.serialize(); window.__calls.push({ solana: bytes.length }); return { signature: mock.burnSig }; },
      };
    }
  })();`;
}

async function startBackend() {
  fs.mkdirSync(OUT, { recursive: true });
  const build = spawnSync('go', ['build', '-o', BIN, './cmd/hlsite'], { cwd: BRIDGE, stdio: 'inherit' });
  if (build.status !== 0) throw new Error('go build failed');
  const proc = spawn(BIN, ['-mock', '-listen', '127.0.0.1:0', '-site', SITE], { stdio: ['ignore', 'pipe', 'pipe'] });
  const base = await new Promise((resolve, reject) => {
    let out = '';
    proc.stdout.on('data', (d) => { out += d; const m = /listening on (\S+)/.exec(out); if (m) resolve('http://' + m[1]); });
    proc.stderr.on('data', (d) => { if (process.env.E2E_VERBOSE) process.stderr.write(d); });
    proc.on('exit', (code) => reject(new Error('hlsite exited ' + code)));
    setTimeout(() => reject(new Error('hlsite did not start')), 15000);
  });
  return { proc, base };
}

// step moves the mock's world: out, in, down, reset
async function step(base, flow) {
  const r = await fetch(base + '/api/mock/step?flow=' + flow, { method: 'POST' });
  if (!r.ok) throw new Error('step ' + flow + ': ' + r.status);
  return r.json();
}

async function run() {
  const { proc, base } = await startBackend();
  const mock = await (await fetch(base + '/api/mock')).json();
  const browser = await chromium.launch();
  try {
    for (const vp of WIDTHS) {
      await step(base, 'reset');
      await cases(browser, base, mock, vp);
    }
    await step(base, 'reset');
    await standardWalletCase(browser, base, mock);
    await step(base, 'reset');
    await replayCase(browser, base);
    await step(base, 'reset');
    await reducedCase(browser, base);
    await step(base, 'reset');
    await darkCase(browser, base, mock);
  } finally {
    await browser.close();
    proc.kill();
  }
}

async function newPage(browser, base, mock, vp, standard = false, extra = {}) {
  const context = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, isMobile: !!vp.isMobile, hasTouch: !!vp.isMobile, ...extra });
  if (mock) await context.addInitScript(mockWallets(mock, standard));
  const page = await context.newPage();
  page.errors = [];
  page.on('pageerror', (e) => page.errors.push(String(e)));
  // a console error is a page error unless it is the browser noting a fetch this suite made fail on purpose
  page.on('console', (m) => { if (m.type() === 'error' && !m.text().startsWith('Failed to load resource')) page.errors.push(m.text()); });
  return { context, page };
}

async function fits(page, name, vp) {
  const w = await page.evaluate(() => [document.documentElement.scrollWidth, window.innerWidth]);
  check(w[0] <= w[1], `${name} at ${vp.name}: horizontal overflow ${w[0]} > ${w[1]}`);
  // text that overflows its own box (a long address in a list) widens nothing the page reports, so count it too
  const spill = await page.evaluate(() => [...document.querySelectorAll('main *')].filter((e) => !(e instanceof SVGElement) && !e.closest('.scroll') && e.clientWidth > 0 && e.scrollWidth > e.clientWidth + 1).map((e) => e.tagName.toLowerCase() + '#' + e.id));
  check(spill.length === 0, `${name} at ${vp.name}: text spills its box in ${spill.join(', ')}`);
  await svgFits(page, name, vp);
  await page.screenshot({ path: path.join(OUT, `${name}-${vp.name}.png`), fullPage: true });
}

// svgFits: every label of a drawing stays inside the drawing, and a part's labels inside the part's plate (on
// screen, where the packets' and the streams' transforms are counted; a packet's own letters are not labels)
async function svgFits(page, name, vp) {
  const bad = await page.evaluate(() => {
    const out = [];
    const inside = (b, p, tol = 1.5) => b.left >= p.left - tol && b.right <= p.right + tol && b.top >= p.top - tol && b.bottom <= p.bottom + tol;
    for (const svg of document.querySelectorAll('svg.machine-svg, svg.scene-svg')) {
      const box = svg.getBoundingClientRect();
      for (const t of svg.querySelectorAll('text')) {
        if (!t.textContent.trim() || t.closest('.hidden, .pkt, .gblock, .coin')) continue;
        const b = t.getBoundingClientRect();
        if (!inside(b, box)) out.push('outside the picture: ' + t.textContent);
        const node = t.closest('[data-node]');
        const plate = node && node.querySelector(':scope > rect.plate');
        if (plate && !inside(b, plate.getBoundingClientRect())) out.push(node.dataset.node + ': ' + t.textContent);
      }
    }
    return out;
  });
  check(bad.length === 0, `${name} at ${vp.name}: a label spills its part: ${bad.join(' | ')}`);
}

const machineText = (page, node) => page.$eval(`.machine-svg [data-node="${node}"] text.val`, (t) => t.textContent);
const playing = (page) => page.$eval('.machine-svg', (s) => s.getAttribute('data-playing') || '');
// readNow has the page read the activity at once (it does when its tab comes back into view)
const readNow = (page) => page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));

async function cases(browser, base, mock, vp) {
  const { context, page } = await newPage(browser, base, mock, vp);
  // the home page: the story, no jargon word for the model, the policy on the response
  const resp = await page.goto(base + '/');
  check((resp.headers()['content-security-policy'] || '').includes("connect-src 'self'"), 'CSP on the home page');
  const body = (await page.textContent('body')).toLowerCase();
  check(body.includes('four guardians') && body.includes('govdao') && body.includes('caps bound the loss'), 'the story is on the home page');
  check(!body.includes('claude'), 'the home page never names the model');
  check(await page.getAttribute('header nav a[href="/"]', 'aria-current') === 'page', 'the nav marks Home');

  // the machine, drawn from the mock's figures: the heads, the locks and the mints, the lamps, the validators
  await waitFor(() => page.$eval('.machine-svg', (s) => Number(s.getAttribute('data-head')) >= 1300), 'the machine shows gno.land’s head');
  check(await page.$eval('.machine-svg', (s) => s.getAttribute('data-trusted')) === '1288', 'the light client trusts block 1,288');
  check(await page.$eval('.machine-svg', (s) => s.getAttribute('data-layout')) === (vp.width >= 700 ? 'wide' : 'narrow'), 'the layout fits the width');
  check(await machineText(page, 'lockGNOT') === '1,000,000' && await machineText(page, 'lockBUBBLE') === '2,500,200', 'the locks: ' + await machineText(page, 'lockGNOT') + ' ' + await machineText(page, 'lockBUBBLE'));
  check(await machineText(page, 'mintGNOT') === '999,990' && await machineText(page, 'mintBUBBLE') === '2,499,700', 'the supplies on Solana');
  check((await page.$$('.machine-svg [data-node="signers"] .lamp[data-state="up"]')).length === 3, 'three signers up, none vouching yet');
  check((await page.$$('.machine-svg [data-node="validators"] .seal')).length === 6, 'six validators');
  check(await page.$eval('.machine-svg [data-node="balGNOT"] .balance', (b) => b.getAttribute('data-gap') === '10000000' && /rotate\(-/.test(b.querySelector('.beam').getAttribute('transform'))), 'the GNOT balance tips while 10 GNOT are in flight');
  check(await page.$eval('.machine-svg [data-node="balBUBBLE"] .balance', (b) => b.getAttribute('data-identity') === 'holds'), 'the BUBBLE balance holds');
  check((await page.$$('#events li')).length >= 5 && (await page.textContent('#events')).includes('10 GNOT locked on gno.land · block 1,290'), 'the recent events');
  check(await page.$eval('#replay', (b) => b.classList.contains('hidden')), 'no replay offered right after an event');
  // the page opened seconds after the newest event (the header of block 1,288), so it plays it, then says what stands
  await waitFor(async () => (await playing(page)) === '' && (await page.textContent('#caption')).startsWith('gno.land is at block'), 'the caption says what stands once the newest event has played', 15000, 100);
  // an event arrives and plays: the header past the send travels to the light client
  await step(base, 'out');
  await readNow(page);
  await waitFor(async () => (await playing(page)) === 'header' && (await page.$$('.pkt[data-kind="header"]')).length === 1, 'the header plays as it arrives', 10000, 50);
  check((await page.textContent('#caption')).includes('A header for gno.land block 1,291 reached Solana'), 'the caption says the header arrived: ' + await page.textContent('#caption'));
  await waitFor(() => page.$eval('.machine-svg', (s) => s.getAttribute('data-trusted') === '1291'), 'the light client trusts block 1,291');
  await waitFor(async () => (await playing(page)) === '', 'the header lands', 10000, 100);
  // an event row plays again on a click
  await page.click('#events li[data-kind="send"]');
  await waitFor(async () => (await playing(page)) === 'send', 'a recent event plays again on a click', 5000, 50);
  await fits(page, 'home', vp);

  // the status page: both tokens, the identity, no flag, the governor, two pending decisions
  await page.goto(base + '/status');
  await waitFor(() => page.$$('#tokens tbody tr').then((r) => r.length === 2), 'two token rows');
  const idents = await page.$$eval('#tokens tbody tr td:last-child', (tds) => tds.map((t) => t.textContent));
  check(idents.join() === 'holds,holds', 'both identities hold: ' + idents.join());
  const syms = await page.$$eval('#tokens tbody tr td:first-child a', (as) => as.map((a) => a.textContent));
  check(syms.join() === 'GNOT,BUBBLE', 'the tokens: ' + syms.join());
  check((await page.textContent('#flags')).includes('no flag, no hold'), 'no flag stands');
  check((await page.textContent('#client')).includes('governor: ' + mock.sender.slice(0, 0) + 'g1'), 'the governor line');
  check((await page.$$('#pending li')).length === 2, 'two pending decisions (gno.land and Solana)');
  check((await page.textContent('#caps')).includes('25,000'), 'the maximum per transfer');
  check((await page.textContent('#identity')).includes('holds; 10000000 in flight — GNOT: lock 1000000000000 = supply 999990000000'), 'the identity line with its numbers, the send in flight');
  check(await page.$$eval('#tokens, #caps', (ts) => ts.every((t) => t.parentElement.classList.contains('scroll'))), 'the status tables scroll in their own box');
  await fits(page, 'status', vp);

  // the help page: the figures from the status, the realm links
  await page.goto(base + '/help');
  await waitFor(() => page.$$('#realms a').then((r) => r.length >= 4), 'the realm links');
  check((await page.textContent('#fees')).includes('5 GNOT'), 'the fee from the status: ' + (await page.textContent('#fees')));
  check((await page.textContent('body')).includes('paid in installments'), 'installments explained');
  check((await page.$$eval('#realms a', (as) => as.every((a) => a.href.startsWith('https://gno.land/gno.land/r/')))), 'realm links go to gnoweb');
  await fits(page, 'help', vp);

  // How it works: the scenes, each played as it comes into view and again on a tap
  await page.goto(base + '/how');
  await waitFor(() => page.$$('figure.scene svg').then((r) => r.length === 11), 'eleven scenes');
  check(await page.getAttribute('header nav a[href="/how"]', 'aria-current') === 'page', 'the nav marks How it works');
  await waitFor(() => page.$eval('figure.scene[data-scene="0"]', (f) => Number(f.dataset.played) >= 1), 'the first scene plays in view');
  const scene = await page.$('figure.scene[data-scene="4"]');
  await scene.scrollIntoViewIfNeeded();
  // it plays once on coming into view; when that packet has landed, a tap plays it again
  await waitFor(async () => Number(await scene.getAttribute('data-played')) >= 1 && (await scene.$$('.pkt')).length === 0, 'the scene played in view and landed', 10000, 100);
  const before = Number(await scene.getAttribute('data-played'));
  await scene.click();
  check(Number(await scene.getAttribute('data-played')) === before + 1 && (await scene.$$('.pkt')).length === 1, 'a tapped scene plays again, a packet moving');
  check((await page.textContent('main')).includes('merkle proof'), 'the proof explained');
  await fits(page, 'how', vp);

  // the bridge page, gno.land → Solana, GNOT: the exact envelope
  await page.goto(base + '/bridge');
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('fee 5 GNOT')), 'the GNOT fee line');
  check(await page.$eval('.machine-svg', (s) => s.classList.contains('compact')), 'the Bridge page carries the compact machine');
  check((await page.textContent('#fees')).includes('at most 25,000 per transfer'), 'the maximum on the fee line');
  await page.fill('#amount', '10');
  await page.fill('#recipient', mock.alice);
  await page.click('#send');
  await waitFor(() => page.evaluate(() => window.__calls.length), 'Adena was asked');
  const calls = await page.evaluate(() => window.__calls);
  const req = calls[0] && calls[0].adena;
  check(req && req.messages.length === 1 && req.gasWanted === 60000000 && req.gasFee === 60000, 'one message with the gas: ' + JSON.stringify(req && { n: req.messages.length, gasFee: req.gasFee, gasWanted: req.gasWanted }));
  const m0 = req && req.messages[0].value;
  check(m0 && m0.send === '15000000ugnot' && m0.func === 'TransferRemote' && m0.pkg_path.endsWith('/hlgnot') && m0.caller === mock.sender, 'GNOT sends amount + fee in ugnot with the call: ' + JSON.stringify(m0));
  check(m0 && m0.args.join() === ['1399811150', mock.alice, '10000000'].join(), 'the TransferRemote arguments: ' + JSON.stringify(m0 && m0.args));
  check((await page.textContent('#result')).includes('tracking'), 'the result line tracks');
  // the walkthrough: five steps; the header is in already (the home page's step), then the proof, then the mint
  await waitFor(() => page.$$eval('#track ol.walk li', (l) => l.length === 5), 'the send’s five steps');
  check((await page.textContent('#track')).includes('message id ' + mock.outId), 'the tracker shows the message id');
  const doneOut = () => page.$$eval('#track ol.walk li.done', (l) => l.map((x) => x.dataset.key).join(' '));
  await waitFor(async () => (await doneOut()) === 'lock final header', 'locked, final, its header in');
  check(await page.$eval('#track li[data-key="proof"]', (li) => li.classList.contains('now')), 'the proof is the step under way');
  check((await page.textContent('#track li[data-key="header"]')).includes('block 1,291') && (await page.textContent('#track li[data-key="header"]')).includes('forged header'), 'the header step says what and why');
  for (const [want, kind] of [['lock final header proof', 'mint'], ['lock final header proof mint', 'mint']]) {
    await step(base, 'out');
    await page.click('#trackgo');
    await waitFor(async () => (await doneOut()) === want, 'the walkthrough reaches ' + want.split(' ').pop(), 15000);
    await waitFor(async () => (await playing(page)) === kind, 'the machine plays the ' + want.split(' ').pop() + ' step', 8000, 50);
  }
  check((await page.textContent('#track li[data-key="proof"]')).includes('leaf 2 of 3') && (await page.$$('#track li[data-key="proof"] .merkle .node.on')).length === 3, 'the proof step draws its tree: leaf 2 of 3, two levels up');
  check((await page.textContent('#track')).includes('delivered'), 'the state says delivered');
  await page.click('#track li[data-key="header"]');
  await waitFor(async () => (await playing(page)) === 'header', 'a chosen step plays on the machine', 5000, 50);

  // BUBBLE: approve amount + fee to the route, then TransferRemote without coins
  await page.selectOption('#token', 'BUBBLE');
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('fee 100 BUBBLE')), 'the BUBBLE fee line');
  await page.fill('#amount', '200');
  await page.click('#send');
  await waitFor(() => page.evaluate(() => window.__calls.length >= 2), 'Adena was asked again');
  const req2 = (await page.evaluate(() => window.__calls))[1].adena;
  const [a, b] = req2.messages.map((m) => m.value);
  check(a.func === 'Approve' && a.pkg_path.endsWith('/bubble') && a.args[0] === mock.bubbleRealm && a.args[1] === '300000000' && a.send === '', 'BUBBLE approves amount + fee to the route: ' + JSON.stringify(a));
  check(b.func === 'TransferRemote' && b.pkg_path.endsWith('/hlbubble') && b.send === '' && b.args[2] === '200000000', 'then TransferRemote with no coins: ' + JSON.stringify(b));

  // the refusals before any wallet is asked
  await page.selectOption('#token', 'GNOT');
  await page.fill('#amount', '5');
  await page.click('#send');
  await waitFor(() => page.textContent('#result').then((t) => t.includes('at least 10 GNOT')), 'the minimum is enforced');
  await page.fill('#amount', '10');
  await page.fill('#recipient', 'not-an-address');
  await page.click('#send');
  await waitFor(() => page.textContent('#result').then((t) => t.includes('Solana address')), 'a bad recipient is refused');
  check((await page.evaluate(() => window.__calls.length)) === 2, 'no wallet call for a refused form');

  // Solana → gno.land: the burn built here, signed and sent by the wallet, walked through to the payout
  await page.click('#in');
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('taken from the claim first')), 'the inbound fee line');
  await page.selectOption('#token', 'BUBBLE');
  await page.fill('#amount', '200');
  await page.fill('#recipient', mock.bubbleRealm);
  await page.click('#send');
  await waitFor(() => page.textContent('#result').then((t) => t.includes('bridge realm')), 'a realm address is refused by the backend');
  await page.fill('#amount', '50');
  await page.fill('#recipient', mock.recipient);
  await page.click('#send');
  await waitFor(() => page.textContent('#result').then((t) => t.includes('more than the fee')), 'a burn under the fee is refused');
  await page.fill('#amount', '200');
  await page.click('#send');
  await waitFor(() => page.evaluate(() => window.__calls.some((c) => c.solana)), 'the Solana wallet signed');
  const sol = (await page.evaluate(() => window.__calls)).find((c) => c.solana);
  check(sol.solana > 200, 'the wallet received the serialized transaction (' + sol.solana + ' bytes)');
  check((await page.textContent('#connect')).includes('Phantom'), 'the wallet is named once connected');
  const doneIn = () => page.$$eval('#track ol.walk li.done', (l) => l.map((x) => x.dataset.key).join(' '));
  const lit = () => page.$$eval('#track li[data-key="vouch"] .lamp[data-state="vouching"]', (l) => l.length);
  await waitFor(async () => (await page.textContent('#track')).includes('Burned on Solana') && (await doneIn()) === 'burn', 'the burn is the first step done');
  check((await lit()) === 0, 'no signer has vouched yet');
  // the signers light one by one; at two the step is done; then the delivery and the credit; then the pay
  await step(base, 'in');
  await page.click('#trackgo');
  await waitFor(async () => (await lit()) === 1 && (await doneIn()) === 'burn', 'one signer vouches', 15000);
  await step(base, 'in');
  await page.click('#trackgo');
  await waitFor(async () => (await lit()) === 2 && (await doneIn()) === 'burn vouch', 'two of three vouch: the step is done', 15000);
  await waitFor(async () => (await playing(page)) === 'burn', 'the machine plays the signers vouching', 8000, 50);
  await step(base, 'in');
  await page.click('#trackgo');
  await waitFor(async () => (await doneIn()) === 'burn vouch deliver credit', 'delivered and credited', 15000);
  await waitFor(() => page.textContent('#track .eta').then((t) => /about \d/.test(t)), 'the pay step counts the meters’ time left');
  check((await page.$$('#track li[data-key="pay"] .gauge')).length === 1, 'the pay step draws the meter');
  check((await page.textContent('#track li[data-key="credit"]')).includes('credited in block 1,'), 'the credit names its block');
  await step(base, 'in');
  await page.click('#trackgo');
  await waitFor(() => page.textContent('#track li[data-key="pay"]').then((t) => t.includes('Paid 100 of 200')), 'paid in installments, half so far', 15000);
  await step(base, 'in');
  await page.click('#trackgo');
  await waitFor(async () => (await doneIn()) === 'burn vouch deliver credit pay', 'the claim is paid', 15000);
  check((await page.textContent('#track')).includes('Paid in full'), 'paid in full');

  // the track box by hand: a gno.land transaction hash resolves to the send
  await page.fill('#trackref', mock.gnoTx);
  await page.click('#trackgo');
  await waitFor(() => page.textContent('#track').then((t) => t.includes('message id ' + mock.outId)), 'a gno.land hash resolves to its message id');
  await page.fill('#trackref', 'nothing-here');
  await page.click('#trackgo');
  await waitFor(() => page.textContent('#track').then((t) => t.includes('not seen yet')), 'an unknown reference says so');
  await fits(page, 'bridge', vp);
  check(page.errors.length === 0, `no page errors at ${vp.name}: ${page.errors.join(' | ')}`);
  await context.close();
}

// standardWalletCase: the Wallet Standard path — the wallet registers itself, takes the transaction as bytes
// and answers a signature as bytes, which the page encodes.
async function standardWalletCase(browser, base, mock) {
  const { context, page } = await newPage(browser, base, mock, WIDTHS[0], true);
  await page.goto(base + '/bridge');
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('fee')), 'the fee line');
  await page.click('#in');
  await page.selectOption('#token', 'BUBBLE');
  await page.fill('#amount', '200');
  await page.fill('#recipient', mock.recipient);
  await page.click('#send');
  await waitFor(() => page.evaluate(() => window.__calls.some((c) => c.standard)), 'the standard wallet signed');
  const call = (await page.evaluate(() => window.__calls)).find((c) => c.standard);
  check(call.standard > 200 && call.chain === 'solana:devnet', 'bytes and the chain went to the standard wallet: ' + JSON.stringify(call));
  check((await page.textContent('#connect')).includes('MockStandard'), 'the standard wallet is named');
  await waitFor(() => page.textContent('#track').then((t) => t.includes('Burned on Solana')), 'the signature the wallet answered in bytes tracks');
  check((await page.textContent('#result')).includes(mock.burnSig.slice(0, 8)), 'the signature is encoded in base58');
  check(page.errors.length === 0, 'no page errors with the standard wallet: ' + page.errors.join(' | '));
  await context.close();
}

// replayCase: nothing has happened for ten minutes (the page's clock says so), so the replay is offered; it plays
// the last completed transfer each way — the history's 25 GNOT out, 500 BUBBLE back — with their real steps,
// labelled as a replay, and gives the picture back to the live view after.
async function replayCase(browser, base) {
  const { context, page } = await newPage(browser, base, null, WIDTHS[0]);
  await page.clock.setFixedTime(Date.now() + 10 * 60 * 1000);
  await page.goto(base + '/');
  const btn = await waitFor(async () => { const b = await page.$('#replay:not(.hidden)'); return b; }, 'the replay is offered when nothing happened for a while', 15000);
  if (!btn) { await context.close(); return; }
  await btn.click();
  const seen = [], captions = new Set();
  await waitFor(async () => {
    const k = await playing(page);
    if (k && seen[seen.length - 1] !== k) seen.push(k);
    captions.add(await page.textContent('#caption'));
    return seen.length >= 1 && k === '' && (await page.$eval('.banner', (b) => b.classList.contains('hidden')));
  }, 'the replay runs to its end', 40000, 40);
  check(seen.join(' ') === 'send header mint burn credit payout', 'the replay plays the round trip in order: ' + seen.join(' '));
  const caps = [...captions].join(' | ');
  check(caps.includes('Replay · ') && caps.includes('25 GNOT locked on gno.land in block 600') && caps.includes('500 BUBBLE burned on Solana (burn #8)') && caps.includes('header of block 601'), 'the replay narrates the real transfer: ' + caps.slice(0, 600));
  check(page.errors.length === 0, 'no page errors in the replay: ' + page.errors.join(' | '));
  await context.close();
}

// reducedCase: prefers-reduced-motion — the same figures, nothing moves: the streams stand, an event lights its
// path with no packet, the scenes of How it works light theirs.
async function reducedCase(browser, base) {
  const vp = WIDTHS[1];
  const { context, page } = await newPage(browser, base, null, vp, false, { reducedMotion: 'reduce' });
  await page.goto(base + '/');
  await waitFor(() => page.$eval('.machine-svg', (s) => Number(s.getAttribute('data-head')) >= 1300), 'the still machine shows the head');
  check(await page.$eval('.machine-svg', (s) => s.classList.contains('still')), 'the machine is still under reduced motion');
  check(await machineText(page, 'lockGNOT') === '1,000,000' && (await page.$$('.machine-svg [data-node="signers"] .lamp')).length === 3, 'the still machine carries the same figures');
  const where = () => page.$eval('.machine-svg .gblock', (b) => b.getAttribute('transform'));
  await waitFor(async () => (await playing(page)) === '', 'the newest event has played', 15000, 100);
  const w0 = await where();
  await sleep(1500);
  check(await where() === w0, 'the blocks do not stream');
  await step(base, 'out');
  await readNow(page);
  await waitFor(async () => (await playing(page)) === 'header', 'the event still plays, as a lit path', 10000, 50);
  check((await page.$$('.pkt')).length === 0 && (await page.$$('.lane.lit')).length === 1, 'no packet moves; the path is lit');
  check((await page.textContent('#caption')).includes('A header for gno.land block 1,291'), 'the caption says the same');
  await fits(page, 'home-still', vp);
  await page.goto(base + '/how');
  await waitFor(() => page.$eval('figure.scene[data-scene="0"]', (f) => Number(f.dataset.played) >= 1), 'a scene plays in view');
  await page.click('figure.scene[data-scene="1"]');
  check((await page.$$('.pkt')).length === 0 && (await page.$$('figure.scene .lane.lit')).length >= 1, 'How it works lights its paths, nothing moves');
  check(page.errors.length === 0, 'no page errors under reduced motion: ' + page.errors.join(' | '));
  await context.close();
}

// darkCase: the dark theme draws the same parts on the dark ground; a screenshot of each new page
async function darkCase(browser, base, mock) {
  for (const vp of WIDTHS) {
    const { context, page } = await newPage(browser, base, mock, vp, false, { colorScheme: 'dark' });
    await page.goto(base + '/');
    await waitFor(() => page.$eval('.machine-svg', (s) => Number(s.getAttribute('data-head')) >= 1300), 'the dark machine shows the head');
    check(await page.evaluate(() => getComputedStyle(document.documentElement).backgroundColor) === 'rgb(18, 19, 17)', 'the dark ground');
    await page.screenshot({ path: path.join(OUT, `home-dark-${vp.name}.png`), fullPage: true });
    await page.goto(base + '/how');
    await page.screenshot({ path: path.join(OUT, `how-dark-${vp.name}.png`), fullPage: true });
    check(page.errors.length === 0, `no page errors in the dark at ${vp.name}: ` + page.errors.join(' | '));
    await context.close();
  }
}

run().then(() => {
  if (failures.length) {
    console.log(`FAIL: ${failures.length} failed, ${passed} passed\n - ` + failures.join('\n - '));
    process.exit(1);
  }
  console.log(`PASS: ${passed} checks at ${WIDTHS.map((w) => w.name).join(' and ')} width; screenshots in ${OUT}`);
}).catch((e) => { console.log('FAIL: ' + (e.stack || e)); process.exit(1); });
