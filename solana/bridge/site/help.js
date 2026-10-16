// bridge.gno.world — the Help page: the launch figures in the text are replaced by the live ones, the tokens'
// addresses on the other chain are given, and the realm pages are linked. On Ethereum (E-M4) the words that differ —
// its gas, its delays, its reference, how a wallet adds the token — are its own.
import { api, chains, el, fmt, nav, pick, realmLink, kindOf, chainHref, tokenAddress } from './common.js';

nav('/help');

// ETHEREUM are the Help page's sentences that differ on Ethereum, by element id: the launch figures stand until the
// status is read (the Ethereum routes' fee is 20 GNOT or 400 BUBBLE each way, the minimum twice it: eth PLAN.md §5.5).
const ETHEREUM = {
  fees: '20 GNOT or 400 BUBBLE at launch',
  min: '40 GNOT or 800 BUBBLE',
  fargas: 'the Ethereum burn pays its gas in ETH, with the mailbox\u2019s protocol fee when one is set (the wallet shows both before you sign)',
  delayout: null, // built below: it names the chain
  delayinhead: 'Ethereum → gno.land:',
  delayin: 'the burn is credited as a claim once Ethereum has finalized its block — about 13 to 19 minutes — and two of the three signer machines confirm it; the signers sign nothing before.',
  farref: 'your Ethereum transaction hash',
  addhead: 'Adding GNOT or BUBBLE to an Ethereum wallet',
  addhow: 'A wallet may not show the token until you add it: import it by its contract address on Ethereum, below. The same address tells the bridge\u2019s token from another of the same name.',
};

(async () => {
  const c = await chains;
  const chain = await pick();
  if (kindOf(chain) === 'ethereum') {
    for (const [id, text] of Object.entries(ETHEREUM)) if (text) document.getElementById(id).textContent = text;
    const out = document.getElementById('delayout');
    // a header costs millions of gas on Ethereum: bought for waiting sends once their fees pay for it, or at the
    // oldest's six hours from what earlier fees left over (eth PLAN.md §5.5, agent/due.go; E3-FC5); nothing here
    // offers to deliver a send oneself (the lead's decision: no self-delivery now)
    out.replaceChildren(el('b', {}, 'gno.land → ' + chain.name + ':'), ' minutes to hours. Your send waits for a gno.land header past it to reach ' + chain.name + ' and for the relayer\u2019s proof. A header costs millions of gas on ' + chain.name + ', so the updater buys one as soon as the fees of the sends waiting pay for it and their deliveries; otherwise the oldest send waits six hours for one, paid from what earlier fees left over — longer while that is not enough. A delivery waits out a gas spike (the relayer resumes when the base fee falls) and is never stranded; after a validator change on gno.land every send waits seven days (the quarantine).');
    document.getElementById('bridgelink').setAttribute('href', chainHref('/bridge', c, chain));
  }
  const realms = chain.gno || c.gno; // the chain's own deployment on gno.land, else the shared one
  const links = [...chain.tokens.map((t) => [t.symbol + ' route', t.realm]), ['mailbox', realms.mailbox], ['fees', realms.fees], ['decisions to ' + chain.name, realms.gov]].filter((x) => x[1]);
  document.getElementById('tokenaddrs').replaceChildren(...chain.tokens.filter((t) => t.mint).flatMap((t) => [el('dt', {}, `${t.symbol} on ${chain.name}`), el('dd', {}, tokenAddress(chain, t))]));
  document.getElementById('realms').replaceChildren(...links.map(([n, r]) => el('li', {}, n + ': ', el('a', { href: realmLink(c, r), target: '_blank', rel: 'noopener' }, r))));
  try {
    const st = await api('/api/' + chain.id + '/status');
    const t = st.tokens;
    document.getElementById('fees').textContent = t.map((x) => fmt(x.fee) + ' ' + x.symbol).join(' or ');
    document.getElementById('min').textContent = t.map((x) => fmt(x.minimum) + ' ' + x.symbol).join(' or ');
    document.getElementById('max').textContent = t.map((x) => fmt(x.maximum) + ' ' + x.symbol).join(' or ');
  } catch (e) { /* the launch figures stand */ }
})();
