#!/usr/bin/env node
// bridge.gno.world — the browser suite (bridge/SITE.md): the real backend in -mock mode serving the real pages,
// the wallets mocked inside the page (Adena; a Solana wallet as window.solana, and once as a Wallet Standard
// wallet), both flows end to end with the exact calls the wallets receive checked, the status, help and How it
// works pages, at desktop and phone width, a screenshot of each page in e2e/out. M7: the machine on Home drawn
// from the mock's figures, an event playing as it arrives, the walkthrough through every step both ways with the
// machine playing each, the replay of the last real transfer, reduced motion (nothing moves, the same figures),
// the labels inside their parts. The mock's world moves only when the suite steps it (POST /api/mock/step).
// E-M4: the Ethereum deployment beside Solana's (?chain=ethereum) — the picker, gno.land → Ethereum through Adena
// with ER10's refusals before Adena is asked, Ethereum → gno.land through a fake EIP-1193 wallet found by EIP-6963
// (and once as window.ethereum), the tracking both ways, Status, Help, How it works and the machine in Ethereum's
// words, no text spilling its box — and keccak-256 and EIP-55 checked in node against Foundry's answers. The value
// locked over time (chart.js) and each token's address on the other chain, on both deployments' pages.
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

// mockWallets is injected before every page: Adena, a Solana wallet in one of two shapes, and — when eth names one
// — an Ethereum wallet: announced by EIP-6963 ('eip6963') or as window.ethereum ('legacy'), on mainnet at first so
// the page must switch it to the bridge's chain.
function mockWallets(mock, standard, eth) {
  return `(() => {
    const mock = ${JSON.stringify(mock)};
    window.__calls = [];
    window.adena = {
      AddEstablish: async () => ({ code: 0, status: 'success' }),
      GetAccount: async () => ({ code: 0, status: 'success', data: { address: mock.sender, chainId: 'dev' } }),
      DoContract: async (req) => { window.__calls.push({ adena: req }); return { code: 0, status: 'success', data: { hash: req.messages.some((m) => m.value.pkg_path.includes('/eth/')) ? mock.ethGnoTx : mock.gnoTx } }; },
    };
    const ethMode = ${JSON.stringify(eth || '')};
    if (ethMode) {
      const provider = { isMetaMask: ethMode === 'legacy', chain: '0x1',
        request: async ({ method, params }) => {
          window.__calls.push({ eth: method, params: params || [] });
          switch (method) {
            case 'eth_requestAccounts': return [mock.ethSender.toLowerCase()];
            case 'eth_chainId': return provider.chain;
            case 'wallet_switchEthereumChain': provider.chain = params[0].chainId; return null;
            case 'eth_sendTransaction': return mock.ethBurnTx;
          }
          throw Object.assign(new Error('unsupported ' + method), { code: 4200 });
        } };
      if (ethMode === 'legacy') window.ethereum = provider;
      else window.addEventListener('eip6963:requestProvider', () => window.dispatchEvent(new CustomEvent('eip6963:announceProvider',
        { detail: Object.freeze({ info: { uuid: 'mock-eth-1', name: 'MockEth', icon: 'data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg"/>', rdns: 'test.mocketh' }, provider }) })));
    }
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

// step moves the mock's world: out, in, down, reset — Solana's, or with chain 'ethereum' the Ethereum deployment's
async function step(base, flow, chain) {
  const r = await fetch(base + '/api/mock/step?flow=' + flow + (chain ? '&chain=' + chain : ''), { method: 'POST' });
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
    await step(base, 'reset');
    await lockedCase(browser, base);
    await axisCase(browser, base);
    console.log(`the Solana pages: ${passed} checks passed, ${failures.length} failed`);
    await ethHashes();
    ethWords();
    for (const vp of WIDTHS) {
      await step(base, 'reset');
      await ethCases(browser, base, mock, vp);
    }
    await step(base, 'reset');
    await ethLegacyCase(browser, base, mock);
    await step(base, 'reset');
    await lockedCase(browser, base, ETH_LOCKED);
  } finally {
    await browser.close();
    proc.kill();
  }
}

// lockedCase: the value locked over time (chart.js, history.go) on Home and Status at both widths — both tokens
// drawn from the history, each line ending at the machine's lock and its figure above it, a pointer reading a
// level, the burn in flight paid out dropping BUBBLE's line one step when the page reads again, every label
// inside its chart, nothing wider than the page. c is the deployment: Solana's by default, ETH_LOCKED for the
// Ethereum deployment's own locks (its pages carry ?chain=ethereum).
const SOL_LOCKED = { q: '', chain: '', at: '', gnot: 1000000, bubble: 2500200, paid: 2500000, steps: 5 };
const ETH_LOCKED = { q: '?chain=ethereum', chain: 'ethereum', at: ' on Ethereum', gnot: 500000, bubble: 1202500, paid: 1200000, steps: 6 };
async function lockedCase(browser, base, c = SOL_LOCKED) {
  const chart = (sym) => `#locked svg.lchart[data-token="${sym}"]`;
  const n = (v) => v.toLocaleString('en-US');
  for (const vp0 of WIDTHS) {
    const vp = { ...vp0, name: vp0.name + c.at };
    const { context, page } = await newPage(browser, base, null, vp);
    await page.goto(base + '/' + c.q);
    if (!await waitFor(() => page.$(chart('BUBBLE')), `the charts on Home at ${vp.name}`)) { await context.close(); continue; }
    check(await page.$eval(chart('GNOT'), (s) => s.getAttribute('data-last')) === String(c.gnot * 1e6), `GNOT's line ends at the lock at ${vp.name}`);
    check(await page.$eval(chart('BUBBLE'), (s) => s.getAttribute('data-last')) === String(c.bubble * 1e6), `BUBBLE's line ends at the lock at ${vp.name}`);
    check(await page.$eval('#locked .lchart-wrap[data-token="GNOT"] .lchart-val', (e) => e.textContent) === n(c.gnot) + ' GNOT', `GNOT's figure above its chart at ${vp.name}`);
    check(await page.$eval(chart('GNOT') + ' .lchart-line', (p) => /^M[\d.]+,[\d.]+(H[\d.]+V[\d.]+)*H[\d.]+$/.test(p.getAttribute('d'))), `GNOT's line is a step line at ${vp.name}`);
    check((await page.$$(chart('GNOT') + ' text.xlab')).length >= 2 && (await page.$$(chart('GNOT') + ' text.ylab')).length >= 2, `both axes are labelled at ${vp.name}`);
    const box = await (await page.$(chart('BUBBLE'))).boundingBox();
    await page.hover(chart('BUBBLE'), { position: { x: box.width * 0.6, y: box.height * 0.6 } }); // scrolls it into view first
    await waitFor(() => page.$eval(chart('BUBBLE') + ' .cross', (g) => !g.classList.contains('hidden')), `a pointer over the chart reads a level at ${vp.name}`, 5000, 50);
    check(/^[\d,.]+ BUBBLE · \d{4}-\d\d-\d\d \d\d:\d\d UTC$/.test(await page.$eval(chart('BUBBLE') + ' text.tip', (t) => t.textContent)), `the level read says amount and time at ${vp.name}: ` + await page.$eval(chart('BUBBLE') + ' text.tip', (t) => t.textContent));
    await svgFits(page, 'locked-hover', vp);
    const n0 = Number(await page.$eval(chart('BUBBLE'), (s) => s.getAttribute('data-points')));
    for (let i = 0; i < c.steps; i++) await step(base, 'in', c.chain);
    await readNow(page);
    await waitFor(() => page.$eval(chart('BUBBLE'), (s, want) => s.getAttribute('data-last') === want, String(c.paid * 1e6)), `BUBBLE's line drops at the payout at ${vp.name}`);
    check(Number(await page.$eval(chart('BUBBLE'), (s) => s.getAttribute('data-points'))) === n0 + 1, `the payout is one step more at ${vp.name}`);
    check(await page.$eval('#locked .lchart-wrap[data-token="BUBBLE"] .lchart-val', (e) => e.textContent) === n(c.paid) + ' BUBBLE', `BUBBLE's figure follows at ${vp.name}`);
    await page.mouse.move(1, 1);
    await fits(page, 'home-locked' + (c.chain ? '-eth' : ''), vp0);
    await page.goto(base + '/status' + c.q);
    await waitFor(() => page.$(chart('GNOT')), `the chart on Status at ${vp.name}`);
    await fits(page, 'status-locked' + (c.chain ? '-eth' : ''), vp0);
    check(page.errors.length === 0, `no page error with the charts at ${vp.name}: ${page.errors.join(' | ')}`);
    await context.close();
    await step(base, 'reset');
  }
}

// axisCase: the locked chart's time axis drawn in the page (chart.js's drawLockChart) over fixed spans at a phone's
// and a desktop's chart width — whatever the hour, between two labels and the width's budget (one label a seventy-five
// pixels): a span just over a day once kept one label on a phone when a half-day mark fell near an end, where a label
// is dropped (the suite's own mock span, a day and its minutes, failed so at 23:42 UTC)
async function axisCase(browser, base) {
  const { context, page } = await newPage(browser, base, null, WIDTHS[0]);
  await page.goto(base + '/status');
  const day = 86400, at = (iso) => Date.parse(iso) / 1000;
  const spans = [
    ['a day and ten minutes, a half-day mark at the start', at('2026-10-14T23:42:00Z') - day - 600, at('2026-10-14T23:42:00Z')],
    ['a day and ten minutes, a half-day mark at the end', at('2026-10-15T00:16:00Z') - day - 600, at('2026-10-15T00:16:00Z')],
    ['a day and an hour', at('2026-10-15T12:40:00Z') - day - 3600, at('2026-10-15T12:40:00Z')],
    ['eleven hours', at('2026-10-15T05:20:00Z') - 11 * 3600, at('2026-10-15T05:20:00Z')],
    ['three days', at('2026-10-15T00:30:00Z') - 3 * day, at('2026-10-15T00:30:00Z')],
    ['thirty days', at('2026-10-15T23:50:00Z') - 30 * day, at('2026-10-15T23:50:00Z')],
  ];
  for (const width of [358, 900]) {
    for (const [what, from, now] of spans) {
      const n = await page.evaluate(async ([from, now, width]) => {
        const { drawLockChart } = await import('/chart.js');
        const svg = drawLockChart({ symbol: 'GNOT', lock: 5e12, points: [[from, 0], [from + (now - from) / 2, 5e12]] }, { from, now }, width);
        return svg.querySelectorAll('text.xlab').length;
      }, [from, now, width]);
      const most = Math.max(2, Math.floor((width - 56) / 75));
      check(n >= 2 && n <= most, `the time axis over ${what} at ${width} px: ${n} labels (2 to ${most})`);
    }
  }
  await context.close();
}

async function newPage(browser, base, mock, vp, standard = false, extra = {}, eth = '') {
  const context = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, isMobile: !!vp.isMobile, hasTouch: !!vp.isMobile, ...extra });
  if (mock) await context.addInitScript(mockWallets(mock, standard, eth));
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
    for (const svg of document.querySelectorAll('svg.machine-svg, svg.scene-svg, svg.lchart')) {
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
  // each token's address on Solana, as the chains name it: the Status, Help and Bridge pages show these
  const solChain = (await (await fetch(base + '/api/chains')).json()).chains[0];
  const mintOf = (sym) => solChain.tokens.find((t) => t.symbol === sym).mint;
  check(!!mintOf('GNOT') && !!mintOf('BUBBLE'), 'the chains name each token\'s address on Solana');
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
  check((await page.$$eval('#addrs dd .addr code', (cs) => cs.map((c) => c.textContent))).join() === [mintOf('GNOT'), mintOf('BUBBLE')].join(), `both tokens' addresses on the Status page at ${vp.name}`);
  check((await page.$$eval('#addrs dt', (ds) => ds.map((d) => d.textContent))).join() === 'GNOT on Solana,BUBBLE on Solana', 'each address named');
  await fits(page, 'status', vp);

  // the help page: the figures from the status, the realm links
  await page.goto(base + '/help');
  await waitFor(() => page.$$('#realms a').then((r) => r.length >= 4), 'the realm links');
  check((await page.textContent('#fees')).includes('5 GNOT'), 'the fee from the status: ' + (await page.textContent('#fees')));
  check((await page.textContent('body')).includes('paid in installments'), 'installments explained');
  check((await page.$$eval('#realms a', (as) => as.every((a) => a.href.startsWith('https://gno.land/gno.land/r/')))), 'realm links go to gnoweb');
  check((await page.$$eval('#tokenaddrs dd .addr code', (cs) => cs.map((c) => c.textContent))).join() === [mintOf('GNOT'), mintOf('BUBBLE')].join(), `both tokens' addresses on Help at ${vp.name}`);
  check((await page.textContent('body')).includes('Only the token at this address is backed by the lock'), 'Help says which token is the real one');
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
  // the chosen token's address on Solana under the form, whole, with its explorer link; it follows the token picked,
  // and its button copies it
  check(await page.$eval('#tokenaddr .addr code', (c) => c.textContent) === mintOf('GNOT'), `the GNOT address on the Bridge page at ${vp.name}`);
  check(await page.$eval('#tokenaddr .addr a', (a) => a.href) === `https://explorer.solana.com/address/${mintOf('GNOT')}?cluster=devnet`, `the explorer link at ${vp.name}`);
  await page.selectOption('#token', 'BUBBLE');
  check(await page.$eval('#tokenaddr .addr code', (c) => c.textContent) === mintOf('BUBBLE') && (await page.textContent('#tokenaddr')).startsWith('BUBBLE on Solana: '), `the address follows the token at ${vp.name}`);
  await page.selectOption('#token', 'GNOT');
  if (vp.name === 'desktop') {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: base });
    await page.click('#tokenaddr .addr button.copy');
    await waitFor(() => page.textContent('#tokenaddr .addr button.copy').then((t) => t === 'copied'), 'the copy button says copied', 5000, 50);
    check(await page.evaluate(() => navigator.clipboard.readText()) === mintOf('GNOT'), 'the copy button copies the address');
  }
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

// ------------------------------------------------------------------------------------------------ Ethereum --

// ethHashes: the page's keccak-256 and EIP-55 (eth.js), run in node against Foundry's answers (cast keccak, cast
// to-check-sum-address, 2026-10-14): the empty input, a topic, three blocks of input, and both padding edges.
async function ethHashes() {
  const eth = await import('data:text/javascript;base64,' + fs.readFileSync(path.join(SITE, 'eth.js')).toString('base64'));
  const k = (b) => eth.hex(eth.keccak256(typeof b === 'string' ? new TextEncoder().encode(b) : b));
  check(k('') === 'c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470', 'keccak-256 of nothing');
  check(k('Dispatch(address,uint32,bytes32,bytes)') === '769f711d20c679153d382254f59892613b58a97cc876b249134ac25c80f9c814', 'keccak-256: the Dispatch topic');
  check(k(Uint8Array.from({ length: 300 }, (_, i) => i & 255)) === 'a679e749a6af300c36e7ff2255d220864eab27b382f9cfdc5aa4d13563ba36ff', 'keccak-256 over three blocks');
  check(k('a'.repeat(135)) === '34367dc248bbd832f4e3e69dfaac2f92638bd0bbd18f2912ba4ef454919cf446' && k('a'.repeat(136)) === 'a6c4d403279fe3e0af03729caada8374b5ca54d8065329a3ebcaeb4b60aa386e', 'keccak-256 at both padding edges');
  for (const a of ['0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed', '0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359', '0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB', '0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb']) {
    check(eth.checksum(a.toLowerCase()) === a, 'EIP-55: ' + a);
  }
  // the recipient rule (§5.2): only the EIP-55 form is taken; one case is refused with no checksummed form in the
  // answer to take (E3-CF6); an address with no letter is its own checksum
  const cs = '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed', bare = { name: 'Ethereum', contracts: {} };
  for (const typed of [cs.toLowerCase(), '0x' + cs.slice(2).toUpperCase()]) {
    const r = eth.recipient(typed, bare);
    check(!r.ok && !('confirm' in r) && !JSON.stringify(r).includes(cs), 'one case is refused, no checksummed form offered: ' + JSON.stringify(r));
  }
  check(eth.recipient(cs, bare).ok && eth.recipient(cs, bare).addr === cs, 'the checksummed form is taken');
  check(eth.recipient('0x1234567890123456789012345678901234567890', bare).ok, 'an address with no letter is its own checksum');
}

// ethWords: no page keeps a stale Ethereum figure or promise (round 3: E3-CF11's "about thirteen minutes" of finality,
// 12.8 to 19 measured; E3-FC5's "as soon as a transfer waits, and every 6 hours" and the one-click checksum)
function ethWords() {
  for (const f of fs.readdirSync(SITE).filter((n) => /\.(js|html)$/.test(n))) {
    const t = fs.readFileSync(path.join(SITE, f), 'utf8');
    check(!/thirteen minutes|every 6 hours|confirmuse|checksummed form is/i.test(t), `no stale Ethereum words in ${f}`);
  }
}

// flipCase changes the case of an address's first hex letter: mixed case that is not its checksum
function flipCase(a) {
  const i = a.slice(2).search(/[a-fA-F]/) + 2;
  const c = a[i];
  return a.slice(0, i) + (c === c.toLowerCase() ? c.toUpperCase() : c.toLowerCase()) + a.slice(i + 1);
}

// ethCases: the Ethereum deployment's pages at one width — Home's machine in Ethereum's words and figures, Status,
// Help, How it works, and the Bridge page both ways with the wallets' exact requests, ER10's refusals before Adena
// is asked, and the tracking through every step.
async function ethCases(browser, base, mock, vp) {
  const { context, page } = await newPage(browser, base, mock, vp, false, {}, 'eip6963');
  const E = '?chain=ethereum';
  const playingE = (pg) => playing(pg);
  // Home: the machine draws Ethereum, its figures the mock's (the supplies at its finalized block)
  await page.goto(base + '/' + E);
  await waitFor(() => page.$eval('.machine-svg', (s) => Number(s.getAttribute('data-head')) >= 1300), 'the Ethereum machine shows gno.land’s head');
  check(await page.$eval('.machine-svg', (s) => s.getAttribute('data-trusted')) === '1050', 'Ethereum’s light client trusts block 1,050');
  check(await machineText(page, 'lockGNOT') === '500,000' && await machineText(page, 'lockBUBBLE') === '1,202,500', 'the Ethereum deployment’s own locks: ' + await machineText(page, 'lockGNOT'));
  check(await machineText(page, 'mintGNOT') === '499,880' && await machineText(page, 'mintBUBBLE') === '1,202,500', 'the supplies at Ethereum’s finalized block: ' + await machineText(page, 'mintGNOT') + ' ' + await machineText(page, 'mintBUBBLE'));
  const svgText = await page.$eval('.machine-svg', (s) => s.textContent);
  check(svgText.includes('Ethereum') && svgText.includes(vp.width >= 700 ? 'a block ≈12 s' : '12 s/block') && !svgText.includes('slot'), 'the stream is Ethereum’s blocks, every twelve seconds');
  check((await page.$eval('.machine-svg [data-node="admin"]', (n) => n.textContent)).includes('HlAdmin'), 'the admin part is HlAdmin');
  check((await page.$$('.machine-svg [data-node="signers"] .lamp[data-state="up"]')).length === 3, 'three Ethereum signers up, none vouching (the burn is not final)');
  check(await page.$eval('.machine-svg [data-node="balGNOT"] .balance', (b) => b.getAttribute('data-gap') === '120000000'), 'the GNOT balance tips while 120 GNOT are on the way to Ethereum');
  await readNow(page);
  await waitFor(async () => (await page.$$('.machine-svg [data-node="validators"] .seal')).length === 6, 'the six validators from the set the newest update carried', 15000);
  check((await page.textContent('#events')).includes('120 GNOT locked on gno.land · block 1,293') && !(await page.textContent('#events')).includes('1,000,000'), 'the recent events, no stranger’s dispatch among them');
  check((await page.textContent('#lede')).includes('Ethereum’s blocks along the bottom') && (await page.textContent('main')).includes('HlAdmin, the contract that carries out decisions'), 'the story is Ethereum’s');
  check((await page.textContent('#story-check2')).includes('about 13 to 19 minutes after it was made'), 'the story says Ethereum’s finality: 13 to 19 minutes (E3-CF11)');
  // when a header is bought on Ethereum, truly: only for transfers waiting, once their fees pay for it or after six
  // hours; and nothing on the machine invites the reader to relay or carry headers — no self-delivery (E3-FC5)
  await waitFor(async () => (await page.textContent('#caption')).startsWith('gno.land is at block'), 'the Ethereum caption says what stands', 20000, 100);
  const capE = await page.textContent('#caption');
  check(capE.includes('so the updater buys one only when transfers wait: once their fees pay for it, or after six hours from what earlier fees left over') && !capE.includes('as soon as a transfer waits'), 'the caption says when Ethereum’s updater buys a header: ' + capE);
  check(!(await page.$eval('.machine-svg', (x) => x.textContent)).toLowerCase().includes('anyone may'), 'the Ethereum machine invites no one to relay or carry headers');
  check(await page.$eval('header .chain a[aria-current]', (a) => a.textContent) === 'Ethereum' && (await page.$$('header .chain a')).length === 2, 'the header switches chains, Ethereum current');
  check(await page.getAttribute('header nav a[href="/status?chain=ethereum"]', 'href') !== null, 'the nav keeps the chain');
  await fits(page, 'home-eth', vp);

  // Status: Ethereum's figures
  await page.goto(base + '/status' + E);
  await waitFor(() => page.$$('#tokens tbody tr').then((r) => r.length === 2), 'two Ethereum token rows');
  check((await page.$$eval('#tokens tbody tr td:last-child', (t) => t.map((x) => x.textContent))).join() === 'holds,holds', 'both identities hold');
  const head = await page.textContent('#head');
  check(head.includes('Ethereum block') && head.includes('finalized block'), 'the head says Ethereum’s block and the finalized one: ' + head);
  check((await page.textContent('#identity')).includes('holds; 120000000 in flight — GNOT: lock 500000000000 = supply 499880000000') && (await page.textContent('#identity')).includes('; block '), 'the identity at Ethereum’s finalized block');
  check((await page.$$('#pending li')).length === 2 && (await page.textContent('#pending')).includes('Ethereum #1 set protocol fee: set the protocol fee to 0.0001 ETH'), 'the pending decisions: gno.land’s and HlAdmin’s');
  check((await page.textContent('#client')).includes('gno.land on Ethereum') && (await page.textContent('#client')).includes('trusted height 1050') && (await page.textContent('#client')).includes('1 recorded on Ethereum'), 'the machinery on Ethereum');
  check((await page.textContent('#flags')).includes('no flag, no hold') && (await page.textContent('#caps')).includes('Ethereum day cap'), 'no flag; the gate’s caps');
  // each token's contract on Ethereum, as on Solana's page: whole, named, Etherscan's link (no cluster)
  check((await page.$$eval('#addrs dd .addr code', (cs) => cs.map((x) => x.textContent))).join() === [mock.ethGnot, mock.ethBubble].join(), `both tokens' contracts on the Ethereum Status page at ${vp.name}`);
  check((await page.$$eval('#addrs dt', (ds) => ds.map((d) => d.textContent))).join() === 'GNOT on Ethereum,BUBBLE on Ethereum', 'each contract named on Ethereum');
  check((await page.$$eval('#addrs dd .addr a', (as) => as.map((x) => x.href))).join() === [mock.ethGnot, mock.ethBubble].map((x) => 'https://sepolia.etherscan.io/address/' + x).join(), 'Etherscan links the contracts');
  await fits(page, 'status-eth', vp);

  // Help: Ethereum's fees, delays, references and realms
  await page.goto(base + '/help' + E);
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('50 GNOT or 1,000 BUBBLE')), 'the Ethereum routes’ fees');
  const help = await page.textContent('main');
  check(help.includes('Ethereum → gno.land:') && help.includes('Ethereum has finalized its block') && help.includes('your Ethereum transaction hash') && help.includes('protocol fee'), 'the help says Ethereum’s delays, gas and reference');
  check(await page.$$eval('#realms a', (as) => as.some((a) => a.href.endsWith('/eth/hlmailbox')) && as.some((a) => a.href.endsWith('/eth/hlgov'))), 'the Ethereum deployment’s own realms are linked');
  check((await page.$$eval('#tokenaddrs dd .addr code', (cs) => cs.map((x) => x.textContent))).join() === [mock.ethGnot, mock.ethBubble].join(), `both tokens' contracts on the Ethereum Help at ${vp.name}`);
  check((await page.textContent('#addhead')) === 'Adding GNOT or BUBBLE to an Ethereum wallet' && help.includes('import it by its contract address on Ethereum') && help.includes('Only the token at this address is backed by the lock'), 'Help says how an Ethereum wallet adds the token');
  check(!(await page.textContent('main')).includes('Solana'), 'the Ethereum Help never says Solana');
  check(help.includes('gno.land → Ethereum: minutes to hours') && help.includes('the updater buys one as soon as the fees of the sends waiting pay for it and their deliveries; otherwise the oldest send waits six hours') && !help.includes('as soon as a transfer waits'), 'Help says how long a send to Ethereum waits for its header (E3-FC5)');
  check(help.includes('about 13 to 19 minutes') && !help.includes('thirteen'), 'Help says Ethereum’s finality: 13 to 19 minutes (E3-CF11)');
  await fits(page, 'help-eth', vp);
  // with the status unread the launch figures stand — the Ethereum routes' own, 20 GNOT or 400 BUBBLE each way and
  // at least twice that (eth PLAN.md §5.5), never Solana's
  await page.route('**/api/ethereum/status', (r) => r.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"down"}' }));
  const statusRead = page.waitForResponse((r) => r.url().includes('/api/ethereum/status'));
  await page.goto(base + '/help' + E);
  await statusRead;
  await waitFor(() => page.$$('#realms a').then((r) => r.length >= 4), 'the Ethereum Help with its status down');
  check((await page.textContent('#fees')) === '20 GNOT or 400 BUBBLE at launch' && (await page.textContent('#min')) === '40 GNOT or 800 BUBBLE', 'Ethereum’s launch figures stand while its status is down: ' + (await page.textContent('#fees')) + ' / ' + (await page.textContent('#min')));
  await page.unroute('**/api/ethereum/status');

  // How it works: the same scenes in Ethereum's words
  await page.goto(base + '/how' + E);
  await waitFor(() => page.$$('figure.scene svg').then((r) => r.length === 11), 'eleven scenes for Ethereum');
  check((await page.textContent('figure.scene[data-scene="0"]')).includes('Ethereum one every 12') && (await page.textContent('figure.scene[data-scene="0"] svg')).includes('≈12 s a block'), 'the scenes say Ethereum’s pace');
  check((await page.textContent('figure.scene[data-scene="6"]')).includes('mailbox') && (await page.textContent('figure.scene[data-scene="7"]')).includes('signs only a final burn'), 'the burn and the signers in Ethereum’s words');
  const s3 = await page.textContent('figure.scene[data-scene="3"]');
  check(s3.includes('so it buys one only when transfers wait — once their fees pay for it, or after six hours') && !s3.includes('Anyone may'), 'the header scene says when Ethereum’s updater buys one and invites no one: ' + s3);
  check((await page.textContent('figure.scene[data-scene="6"]')).includes('about 13 to 19 minutes'), 'the burn scene says Ethereum’s finality: 13 to 19 minutes');
  await fits(page, 'how-eth', vp);

  // the Bridge page: the picker, then gno.land → Ethereum, GNOT
  await page.goto(base + '/bridge' + E);
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('fee 50 GNOT')), 'the Ethereum GNOT fee line');
  check(await page.$$eval('#chain option', (o) => o.map((x) => x.value).join()) === 'solana,ethereum' && await page.$eval('#chain', (s) => s.value) === 'ethereum', 'the picker lists both chains, Ethereum chosen');
  check((await page.textContent('#out')) === 'gno.land → Ethereum' && (await page.getAttribute('#trackref', 'placeholder')).includes('an Ethereum transaction hash'), 'the toggles and the track box say Ethereum');
  check(await page.$eval('#tokenaddr .addr code', (x) => x.textContent) === mock.ethGnot && (await page.textContent('#tokenaddr')).startsWith('GNOT on Ethereum: '), `the GNOT contract under the Ethereum form at ${vp.name}`);
  check(await page.$eval('#tokenaddr .addr a', (x) => x.href) === 'https://sepolia.etherscan.io/address/' + mock.ethGnot, 'its Etherscan link');
  await page.selectOption('#token', 'BUBBLE');
  check(await page.$eval('#tokenaddr .addr code', (x) => x.textContent) === mock.ethBubble, 'the contract follows the token on Ethereum');
  await page.selectOption('#token', 'GNOT');
  await page.fill('#amount', '100');
  const refuse = async (to, want, what) => {
    await page.fill('#recipient', to);
    await page.evaluate(() => { document.getElementById('result').textContent = ''; }); // two refusals may say the same
    await page.click('#send');
    await waitFor(() => page.textContent('#result').then((t) => t.includes(want)), what);
  };
  await refuse(flipCase(mock.ethRecipient), 'not its checksum', 'a mixed case that is no checksum is refused');
  await refuse('0x' + '0'.repeat(40), 'zero address', 'the zero address is refused');
  await refuse('0x' + '0'.repeat(39) + '1', 'precompile', 'a precompile is refused');
  await refuse('0x' + '0'.repeat(36) + 'ffff', 'precompile', '0x…ffff is a precompile');
  await refuse(mock.ethGnot, 'GNOT token contract', 'the GNOT token (the route’s router) is refused');
  await refuse(mock.ethMailbox.toLowerCase(), 'mailbox', 'the bridge’s mailbox is refused, whatever its case');
  // all sixteen of the deployment, as the realm refuses them (E3-CF6): an implementation and the verifier too
  await refuse(mock.ethClientImpl, 'light client’s implementation', 'the light client’s implementation is refused');
  await refuse(mock.ethVerifierImpl, 'signature verifier (Ed25519Verifier)', 'the Ed25519Verifier is refused');
  await refuse('0x' + '0'.repeat(24) + mock.ethRecipient.slice(2), 'as 20 bytes', 'the 32-byte form is refused on the page');
  // an address in one case carries no checksum: refused, its checksummed form never shown nor offered — that form
  // would be a typo's checksum too (§5.2: only EIP-55 checksummed input; round 3's E3-CF6)
  await refuse(mock.ethRecipient.toLowerCase(), 'all in one case', 'an address in lower case is refused');
  check(!(await page.textContent('main')).includes(mock.ethRecipient) && !(await page.$('#confirm, #confirmuse')), 'no checksummed form is shown or offered for a lower-case address');
  await refuse('0x' + mock.ethRecipient.slice(2).toUpperCase(), 'all in one case', 'an address in upper case is refused');
  check(!(await page.textContent('main')).includes(mock.ethRecipient), 'nor for an upper-case one');
  check(!(await page.evaluate(() => window.__calls.some((c) => c.adena))), 'Adena was not asked for any refused recipient');
  await page.fill('#recipient', mock.ethRecipient);
  await page.click('#send');
  await waitFor(() => page.evaluate(() => window.__calls.some((c) => c.adena)), 'Adena was asked for the Ethereum send');
  const req = (await page.evaluate(() => window.__calls)).find((c) => c.adena).adena;
  const m0 = req.messages[0].value;
  check(req.messages.length === 1 && req.gasWanted === 60000000 && m0.send === '150000000ugnot' && m0.func === 'TransferRemote' && m0.pkg_path.endsWith('/eth/hlgnot'), 'GNOT to Ethereum: amount + fee in ugnot to the Ethereum route: ' + JSON.stringify(m0));
  check(m0.args.join() === ['11155111', mock.ethRecipient, '100000000'].join(), 'the TransferRemote arguments: Ethereum’s domain, the checksummed address: ' + JSON.stringify(m0.args));
  // the walkthrough: locked, final; then the header of 1,294; then the delivery with its proof
  const done = () => page.$$eval('#track ol.walk li.done', (l) => l.map((x) => x.dataset.key).join(' '));
  await waitFor(async () => (await page.textContent('#track')).includes('message id ' + mock.ethOutId) && (await done()) === 'lock final', 'the Ethereum send is locked and final');
  const hdr = await page.textContent('#track li[data-key="header"]');
  check(hdr.includes('so the updater buys one once the fees of the sends waiting pay for it and their deliveries, or after six hours') && !hdr.includes('Anyone may') && !hdr.includes('as soon as a transfer waits'), 'the waiting header step says when Ethereum’s updater buys one, inviting no one: ' + hdr);
  for (const want of ['lock final header', 'lock final header proof mint']) {
    await step(base, 'out', 'ethereum');
    await page.click('#trackgo');
    await waitFor(async () => (await done()) === want, 'the Ethereum walkthrough reaches ' + want.split(' ').pop(), 15000);
  }
  check((await page.textContent('#track li[data-key="header"]')).includes('block 1,294') && (await page.textContent('#track li[data-key="proof"]')).includes('leaf 2 of 3') && (await page.textContent('#track')).includes('delivered'), 'delivered on Ethereum, proven under the header of 1,294');
  await waitFor(async () => ['mint', 'header', ''].includes(await playingE(page)), 'the machine plays the step', 8000, 50);
  // BUBBLE to Ethereum: approve amount + fee to the Ethereum route, then TransferRemote without coins
  await page.selectOption('#token', 'BUBBLE');
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('fee 1,000 BUBBLE')), 'the Ethereum BUBBLE fee line');
  await page.fill('#amount', '2000');
  await page.click('#send');
  await waitFor(() => page.evaluate(() => window.__calls.filter((c) => c.adena).length >= 2), 'Adena was asked for BUBBLE');
  const req2 = (await page.evaluate(() => window.__calls)).filter((c) => c.adena)[1].adena;
  const [ap, tr] = req2.messages.map((m) => m.value);
  check(ap.func === 'Approve' && ap.args[0] === mock.ethBubbleRealm && ap.args[1] === '3000000000' && tr.pkg_path.endsWith('/eth/hlbubble') && tr.send === '' && tr.args[1] === mock.ethRecipient, 'BUBBLE to Ethereum: approve 3,000 to the Ethereum route, then TransferRemote: ' + JSON.stringify(ap));

  // Ethereum → gno.land: the burn built here, the wallet put on the chain, signed and sent, walked to the payout
  await page.click('#in');
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('taken from the claim first')), 'the Ethereum inbound fee line');
  check((await page.textContent('#connect')).includes('MockEth'), 'the wallet EIP-6963 announced is named');
  await page.fill('#amount', '500');
  await page.fill('#recipient', mock.recipient);
  await page.click('#send');
  await waitFor(() => page.textContent('#result').then((t) => t.includes('more than the fee')), 'a burn under the fee is refused');
  await page.fill('#amount', '2500');
  await page.fill('#recipient', mock.ethBubbleRealm);
  await page.click('#send');
  await waitFor(() => page.textContent('#result').then((t) => t.includes('bridge realm')), 'the Ethereum route’s address is refused by the backend');
  check(!(await page.evaluate(() => window.__calls.some((c) => c.eth === 'eth_sendTransaction'))), 'no transaction was sent for a refused burn');
  await page.fill('#recipient', mock.recipient);
  await page.click('#send');
  await waitFor(() => page.evaluate(() => window.__calls.some((c) => c.eth === 'eth_sendTransaction')), 'the Ethereum wallet sent the burn');
  const ec = (await page.evaluate(() => window.__calls)).filter((c) => c.eth);
  const sw = ec.find((c) => c.eth === 'wallet_switchEthereumChain'), sent = ec.find((c) => c.eth === 'eth_sendTransaction').params[0];
  check(ec[0].eth === 'eth_requestAccounts' && sw && sw.params[0].chainId === mock.ethChainId, 'the wallet was asked for its account, then switched to the bridge’s chain: ' + ec.map((c) => c.eth).join(' '));
  check(ec.indexOf(sw) < ec.findIndex((c) => c.eth === 'eth_sendTransaction'), 'the chain was switched before anything was sent');
  check(sent.from === mock.ethSender && sent.to === mock.ethBubble && sent.value === mock.ethFee && sent.data.startsWith('0x81b4e8b4') && sent.data.length === 2 + 8 + 3 * 64 && sent.data.includes('476e6f54') && sent.data.endsWith('9502f900'), 'eth_sendTransaction: transferRemote(gno.land, the recipient, 2,500) to the BUBBLE token with the quote’s exact value: ' + JSON.stringify(sent));
  check((await page.textContent('#result')).includes('fee of 0.0001 ETH') && (await page.textContent('#result')).includes(mock.ethBurnTx.slice(0, 8)), 'the result names the fee and the hash');
  const doneIn = () => page.$$eval('#track ol.walk li.done', (l) => l.map((x) => x.dataset.key).join(' '));
  const lit = () => page.$$eval('#track li[data-key="vouch"] .lamp[data-state="vouching"]', (l) => l.length);
  await waitFor(async () => (await page.textContent('#track')).includes('Burned on Ethereum') && (await doneIn()) === 'burn', 'the Ethereum burn is the first step done');
  check((await page.textContent('#track li[data-key="vouch"]')).includes('Waiting for Ethereum to finalize block'), 'the signers wait for Ethereum’s finality');
  check((await page.textContent('#track li[data-key="burn"]')).includes('about 13 to 19 minutes'), 'the burn step says Ethereum’s finality: 13 to 19 minutes');
  await step(base, 'in', 'ethereum');
  await page.click('#trackgo');
  await waitFor(async () => (await page.textContent('#track li[data-key="vouch"]')).includes('0 of 3 signers'), 'the burn is final: the signers may sign', 15000);
  for (const [n, want] of [[1, 'burn'], [2, 'burn vouch']]) {
    await step(base, 'in', 'ethereum');
    await page.click('#trackgo');
    await waitFor(async () => (await lit()) === n && (await doneIn()) === want, n + ' Ethereum signer(s) vouch', 15000);
  }
  await step(base, 'in', 'ethereum');
  await page.click('#trackgo');
  await waitFor(async () => (await doneIn()) === 'burn vouch deliver credit', 'the Ethereum burn is credited', 15000);
  await step(base, 'in', 'ethereum');
  await page.click('#trackgo');
  await waitFor(() => page.textContent('#track li[data-key="pay"]').then((t) => t.includes('Paid 1,250 of 2,500')), 'paid in installments, half so far', 15000);
  await step(base, 'in', 'ethereum');
  await page.click('#trackgo');
  await waitFor(async () => (await doneIn()) === 'burn vouch deliver credit pay' && (await page.textContent('#track')).includes('Paid in full'), 'the Ethereum claim is paid', 15000);
  // the track box by hand: the history's burn by its hash, a stranger's dispatch, a message id
  await page.fill('#trackref', mock.ethQBurnTx);
  await page.click('#trackgo');
  await waitFor(() => page.textContent('#track').then((t) => t.includes('message id ' + mock.ethQId) && t.includes('Paid in full')), 'the history’s burn by its Ethereum hash');
  await page.fill('#trackref', mock.ethStrangerTx);
  await page.click('#trackgo');
  await waitFor(() => page.textContent('#track').then((t) => t.includes('made no burn toward gno.land')), 'a stranger’s dispatch is no burn (ER5)');
  await page.fill('#trackref', '0x' + mock.ethInId.toUpperCase());
  await page.click('#trackgo');
  await waitFor(() => page.textContent('#track').then((t) => t.includes('message id ' + mock.ethInId)), 'a message id with 0x is read as an id');
  await page.fill('#trackref', 'nothing-here'); // as the Solana flow leaves it: a hash longer than the box scrolls inside it
  await page.click('#trackgo');
  await waitFor(() => page.textContent('#track').then((t) => t.includes('not seen yet')), 'an unknown reference says so on Ethereum');
  await fits(page, 'bridge-eth', vp);
  check(page.errors.length === 0, `no page errors on Ethereum at ${vp.name}: ${page.errors.join(' | ')}`);
  await context.close();
}

// ethLegacyCase: no wallet answers EIP-6963, so window.ethereum is the wallet: the same burn through it.
async function ethLegacyCase(browser, base, mock) {
  const { context, page } = await newPage(browser, base, mock, WIDTHS[0], false, {}, 'legacy');
  await page.goto(base + '/bridge?chain=ethereum');
  await waitFor(() => page.textContent('#fees').then((t) => t.includes('fee')), 'the fee line');
  await page.click('#in');
  check((await page.textContent('#connect')).includes('MetaMask'), 'window.ethereum is the wallet: MetaMask');
  await page.selectOption('#token', 'BUBBLE');
  await page.fill('#amount', '2500');
  await page.fill('#recipient', mock.recipient);
  await page.click('#send');
  await waitFor(() => page.evaluate(() => window.__calls.some((c) => c.eth === 'eth_sendTransaction')), 'window.ethereum sent the burn');
  const sent = (await page.evaluate(() => window.__calls)).find((c) => c.eth === 'eth_sendTransaction').params[0];
  check(sent.to === mock.ethBubble && sent.value === mock.ethFee, 'the call went to window.ethereum: ' + JSON.stringify(sent));
  await waitFor(() => page.textContent('#track').then((t) => t.includes('Burned on Ethereum')), 'the hash window.ethereum answered tracks');
  check(page.errors.length === 0, 'no page errors with window.ethereum: ' + page.errors.join(' | '));
  await context.close();
}

run().then(() => {
  if (failures.length) {
    console.log(`FAIL: ${failures.length} failed, ${passed} passed\n - ` + failures.join('\n - '));
    process.exit(1);
  }
  console.log(`PASS: ${passed} checks at ${WIDTHS.map((w) => w.name).join(' and ')} width; screenshots in ${OUT}`);
}).catch((e) => { console.log('FAIL: ' + (e.stack || e)); process.exit(1); });
