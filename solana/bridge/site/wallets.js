// bridge.gno.world — the wallet adapters, written here rather than fetched: Adena for gno.land (window.adena),
// for Solana the Wallet Standard (Phantom, Backpack and the rest register themselves; a transaction is bytes)
// with the window.solana provider as the fallback, and for Ethereum an EIP-1193 provider found by EIP-6963 (each
// wallet announces itself) with window.ethereum as the fallback. Nothing here talks to a chain: the wallet signs
// and sends through its own RPC, and the page reads results from this site alone.

const B58 = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';

export function b58decode(s) {
  let n = 0n;
  for (const c of s) { const i = B58.indexOf(c); if (i < 0) return null; n = n * 58n + BigInt(i); }
  const out = [];
  while (n > 0n) { out.unshift(Number(n % 256n)); n /= 256n; }
  for (const c of s) { if (c === '1') out.unshift(0); else break; }
  return Uint8Array.from(out);
}

export function b58encode(bytes) {
  let n = 0n;
  for (const b of bytes) n = n * 256n + BigInt(b);
  let s = '';
  while (n > 0n) { s = B58[Number(n % 58n)] + s; n /= 58n; }
  for (const b of bytes) { if (b === 0) s = '1' + s; else break; }
  return s;
}

export function b64decode(s) {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function isSolanaAddress(s) { const b = b58decode(String(s || '').trim()); return !!b && b.length === 32; }
export function isGnoAddress(s) { return /^g1[02-9ac-hj-np-z]{38}$/.test(String(s || '').trim()); }

// ---------------------------------------------------------------- Adena --

export const adena = {
  found() { return !!window.adena; },
  // connect establishes the site with the extension and returns the account {address, chainId, …}.
  async connect() {
    const a = window.adena;
    if (!a) throw new Error('Adena not found: install the Adena wallet extension, then reload');
    const est = await a.AddEstablish({ name: 'bridge.gno.world' });
    if (est && est.code && est.code !== 0 && est.code !== 4001) throw new Error(est.message || 'Adena refused the connection');
    const acc = await a.GetAccount();
    if (!acc || acc.code !== 0 || !acc.data || !acc.data.address) throw new Error((acc && acc.message) || 'Adena: no account');
    return acc.data;
  },
  // send signs and broadcasts the messages; it returns the transaction hash the node answered with.
  async send(messages, gasFee, gasWanted) {
    const r = await window.adena.DoContract({ messages, gasFee, gasWanted, memo: '' });
    if (!r || r.code !== 0) throw new Error((r && r.message) || 'Adena refused the transaction');
    return (r.data && (r.data.hash || r.data.txHash || r.data.tx_hash)) || '';
  },
};

// --------------------------------------------------------------- Solana --

const standard = [];
function addStandard(...ws) {
  for (const w of ws) if (w && w.features && !standard.includes(w)) standard.push(w);
}
try {
  window.addEventListener('wallet-standard:register-wallet', (ev) => { try { ev.detail({ register: addStandard }); } catch (e) { /* not a wallet */ } });
  window.dispatchEvent(new CustomEvent('wallet-standard:app-ready', { detail: { register: addStandard } }));
} catch (e) { /* no window */ }

function standardWallet() {
  return standard.find((w) => w.features['solana:signAndSendTransaction'] && w.features['standard:connect']) || null;
}

function legacyProvider() {
  return (window.phantom && window.phantom.solana) || window.solana || window.backpack || null;
}

export const solanaWallet = {
  found() { return !!(standardWallet() || legacyProvider()); },
  name() { const w = standardWallet(); if (w) return w.name; const p = legacyProvider(); return p ? (p.isPhantom ? 'Phantom' : p.isBackpack ? 'Backpack' : 'Solana wallet') : ''; },
  // connect returns the wallet's address (base58).
  async connect() {
    const w = standardWallet();
    if (w) {
      const r = await w.features['standard:connect'].connect();
      const acc = r && r.accounts && r.accounts[0];
      if (!acc) throw new Error(w.name + ': no account');
      this._account = acc;
      return acc.address;
    }
    const p = legacyProvider();
    if (!p) throw new Error('No Solana wallet found: install Phantom or Backpack, then reload');
    const r = await p.connect();
    const pk = (r && r.publicKey) || p.publicKey;
    if (!pk) throw new Error('the wallet gave no account');
    return pk.toString();
  },
  // signAndSend has the wallet sign the serialized transaction as its fee payer and send it through its own
  // RPC; it returns the signature (base58).
  async signAndSend(bytes, cluster) {
    const w = standardWallet();
    if (w) {
      const chain = 'solana:' + (cluster || 'mainnet');
      const acc = this._account || (await this.connect(), this._account);
      const out = await w.features['solana:signAndSendTransaction'].signAndSendTransaction({ transaction: bytes, account: acc, chain });
      const sig = out && out[0] && out[0].signature;
      if (!sig) throw new Error(w.name + ' returned no signature');
      return typeof sig === 'string' ? sig : b58encode(sig);
    }
    const p = legacyProvider();
    if (!p) throw new Error('No Solana wallet found');
    // the provider serializes what it is given: these bytes, as built, the payer's slot empty
    const tx = { serialize: () => bytes, signatures: [] };
    const r = await p.signAndSendTransaction(tx);
    const sig = (r && r.signature) || r;
    if (!sig) throw new Error('the wallet returned no signature');
    return typeof sig === 'string' ? sig : b58encode(sig);
  },
};

// ------------------------------------------------------------- Ethereum --

// EIP-6963: the page asks (eip6963:requestProvider) and every installed wallet answers with its provider and its
// name (eip6963:announceProvider), so two wallets never fight over window.ethereum; that global is the fallback.
const announced = [];
try {
  window.addEventListener('eip6963:announceProvider', (ev) => {
    const d = ev.detail;
    if (d && d.provider && d.info && d.info.uuid && !announced.some((x) => x.info.uuid === d.info.uuid)) announced.push(d);
  });
  window.dispatchEvent(new Event('eip6963:requestProvider'));
} catch (e) { /* no window */ }

function walletError(e, who) {
  if (e && e.code === 4001) return new Error(who + ': you declined the request');
  return new Error((e && e.message) || who + ' refused the request');
}

export const ethWallet = {
  _pick: 0,
  // wallets are the announced wallets' names, in the order they answered
  wallets() { return announced.map((d) => d.info.name); },
  choose(i) { this._pick = i; },
  _entry() { return announced[this._pick] || announced[0] || null; },
  provider() { const d = this._entry(); return (d && d.provider) || window.ethereum || null; },
  found() { return !!this.provider(); },
  name() {
    const d = this._entry();
    if (d) return d.info.name;
    const p = window.ethereum;
    return p ? (p.isMetaMask ? 'MetaMask' : 'Ethereum wallet') : '';
  },
  // connect asks the wallet for its account (eth_requestAccounts) and returns the address.
  async connect() {
    const p = this.provider();
    if (!p) throw new Error('No Ethereum wallet found: install one (MetaMask, Rabby, …), then reload');
    let accs;
    try { accs = await p.request({ method: 'eth_requestAccounts' }); } catch (e) { throw walletError(e, this.name()); }
    if (!accs || !accs[0]) throw new Error(this.name() + ' gave no account');
    return String(accs[0]);
  },
  // onChain has the wallet on the chain (an EIP-155 id in hex): it asks the wallet to switch when it is on another,
  // and checks that it did.
  async onChain(chainId) {
    const p = this.provider();
    const want = String(chainId).toLowerCase();
    const cur = String(await p.request({ method: 'eth_chainId' })).toLowerCase();
    if (cur === want) return;
    try { await p.request({ method: 'wallet_switchEthereumChain', params: [{ chainId: want }] }); } catch (e) {
      if (e && e.code === 4902) throw new Error(this.name() + ' does not know chain ' + parseInt(want, 16) + ': add the network to the wallet, then try again');
      throw walletError(e, this.name());
    }
    const now = String(await p.request({ method: 'eth_chainId' })).toLowerCase();
    if (now !== want) throw new Error(this.name() + ' is on chain ' + parseInt(now, 16) + ', not ' + parseInt(want, 16) + ': switch it, then try again');
  },
  // send has the wallet sign and send the call the site built (eth_sendTransaction); it returns the hash.
  async send(call) {
    const p = this.provider();
    let hash;
    try { hash = await p.request({ method: 'eth_sendTransaction', params: [{ from: call.from, to: call.to, data: call.data, value: call.value }] }); } catch (e) { throw walletError(e, this.name()); }
    if (!/^0x[0-9a-fA-F]{64}$/.test(String(hash || ''))) throw new Error(this.name() + ' returned no transaction hash');
    return String(hash).toLowerCase();
  },
};
