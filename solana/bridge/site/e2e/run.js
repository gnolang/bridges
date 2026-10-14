#!/usr/bin/env node
// bridge.gno.world — the browser suite (bridge/SITE.md): the real backend in -mock mode serving the real pages,
// the wallets mocked inside the page (Adena; a Solana wallet as window.solana, and once as a Wallet Standard
// wallet), both flows end to end with the exact calls the wallets receive checked, the status and help pages,
// at desktop and phone width, a screenshot of each page in e2e/out. Runs on the devbox (node 22, playwright
// from /root/src/node_modules): `make -C bridge site-qa`. Exit 0 on a pass.
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
async function waitFor(fn, what, timeout = 30000) {
  const t0 = Date.now();
  for (;;) {
    let v;
    try { v = await fn(); } catch (e) { v = null; }
    if (v) return v;
    if (Date.now() - t0 > timeout) { failures.push('timed out: ' + what); return null; }
    await sleep(300);
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

async function run() {
  const { proc, base } = await startBackend();
  const mock = await (await fetch(base + '/api/mock')).json();
  const browser = await chromium.launch();
  try {
    for (const vp of WIDTHS) {
      await cases(browser, base, mock, vp);
    }
    await standardWalletCase(browser, base, mock);
  } finally {
    await browser.close();
    proc.kill();
  }
}

async function newPage(browser, base, mock, vp, standard = false) {
  const context = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, isMobile: !!vp.isMobile, hasTouch: !!vp.isMobile });
  await context.addInitScript(mockWallets(mock, standard));
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
  const spill = await page.evaluate(() => [...document.querySelectorAll('main *')].filter((e) => !e.closest('.scroll') && e.clientWidth > 0 && e.scrollWidth > e.clientWidth + 1).map((e) => e.tagName.toLowerCase() + '#' + e.id));
  check(spill.length === 0, `${name} at ${vp.name}: text spills its box in ${spill.join(', ')}`);
  await page.screenshot({ path: path.join(OUT, `${name}-${vp.name}.png`), fullPage: true });
}

async function cases(browser, base, mock, vp) {
  const { context, page } = await newPage(browser, base, mock, vp);
  // the home page: the story, no jargon word for the model, the policy on the response
  const resp = await page.goto(base + '/');
  check((resp.headers()['content-security-policy'] || '').includes("connect-src 'self'"), 'CSP on the home page');
  const body = (await page.textContent('body')).toLowerCase();
  check(body.includes('four guardians') && body.includes('govdao') && body.includes('caps bound the loss'), 'the story is on the home page');
  check(!body.includes('claude'), 'the home page never names the model');
  check(await page.getAttribute('header nav a[href="/"]', 'aria-current') === 'page', 'the nav marks Home');
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
  check((await page.textContent('#identity')).includes('lock 1000000000000 = supply 1000000000000'), 'the identity line with its numbers');
  check(await page.$$eval('#tokens, #caps', (ts) => ts.every((t) => t.parentElement.classList.contains('scroll'))), 'the status tables scroll in their own box');
  await fits(page, 'status', vp);

  // the help page: the figures from the status, the realm links
  await page.goto(base + '/help');
  await waitFor(() => page.$$('#realms a').then((r) => r.length >= 4), 'the realm links');
  check((await page.textContent('#fees')).includes('5 GNOT'), 'the fee from the status: ' + (await page.textContent('#fees')));
  check((await page.textContent('body')).includes('paid in installments'), 'installments explained');
  check((await page.$$eval('#realms a', (as) => as.every((a) => a.href.startsWith('https://gno.land/gno.land/r/')))), 'realm links go to gnoweb');
  await fits(page, 'help', vp);

  // the bridge page, gno.land → Solana, GNOT: the exact envelope
  await page.goto(base + '/bridge');
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('fee 5 GNOT')), 'the GNOT fee line');
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
  await waitFor(() => page.textContent('#track').then((t) => t.includes('dispatched on gno.land')), 'the send is dispatched');
  check((await page.textContent('#track')).includes('message id ' + mock.outId), 'the tracker shows the message id');
  await waitFor(() => page.$$eval('#track li.done', (l) => l.length === 2), 'the send is delivered on Solana (the mock delivers on the third look)', 40000);
  check((await page.textContent('#track')).includes('delivered'), 'the state says delivered');

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

  // Solana → gno.land: the burn built here, signed and sent by the wallet, tracked to the payout
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
  await waitFor(() => page.textContent('#track').then((t) => t.includes('burned on Solana') && t.includes('credited on gno.land')), 'the burn is credited');
  await waitFor(() => page.$$eval('#track li.done', (l) => l.length === 3), 'the claim is paid (the mock pays on the fourth look)', 40000);
  check((await page.textContent('#track')).includes('paid in full'), 'paid in full');
  check((await page.textContent('#track')).includes('height 1203'), 'the credit names its height');

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
  await waitFor(() => page.textContent('#track').then((t) => t.includes('burned on Solana')), 'the signature the wallet answered in bytes tracks');
  check((await page.textContent('#result')).includes(mock.burnSig.slice(0, 8)), 'the signature is encoded in base58');
  check(page.errors.length === 0, 'no page errors with the standard wallet: ' + page.errors.join(' | '));
  await context.close();
}

run().then(() => {
  if (failures.length) {
    console.log(`FAIL: ${failures.length} failed, ${passed} passed\n - ` + failures.join('\n - '));
    process.exit(1);
  }
  console.log(`PASS: ${passed} checks at ${WIDTHS.map((w) => w.name).join(' and ')} width; screenshots in ${OUT}`);
}).catch((e) => { console.log('FAIL: ' + (e.stack || e)); process.exit(1); });
