// bridge.gno.world — the Help page: the launch figures in the text are replaced by the live ones, and the
// realm pages are linked.
import { api, chains, el, fmt, nav, pick, realmLink } from './common.js';

nav('/help');
(async () => {
  const c = await chains;
  const chain = await pick();
  const links = [...chain.tokens.map((t) => [t.symbol + ' route', t.realm]), ['mailbox', c.gno.mailbox], ['fees', c.gno.fees], ['decisions to ' + chain.name, c.gno.gov]].filter((x) => x[1]);
  document.getElementById('realms').replaceChildren(...links.map(([n, r]) => el('li', {}, n + ': ', el('a', { href: realmLink(c, r), target: '_blank', rel: 'noopener' }, r))));
  try {
    const st = await api('/api/' + chain.id + '/status');
    const t = st.tokens;
    document.getElementById('fees').textContent = t.map((x) => fmt(x.fee) + ' ' + x.symbol).join(' or ');
    document.getElementById('min').textContent = t.map((x) => fmt(x.minimum) + ' ' + x.symbol).join(' or ');
    document.getElementById('max').textContent = t.map((x) => fmt(x.maximum) + ' ' + x.symbol).join(' or ');
  } catch (e) { /* the launch figures stand */ }
})();
