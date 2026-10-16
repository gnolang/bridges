// bridge.gno.world — Ethereum's address rules for the page (E-M4), written here rather than fetched: keccak-256
// (the EIP-55 checksum's hash), the checksum, and what a recipient on Ethereum may be (ER10). The realm refuses the
// same addresses; the page says why before Adena is opened.

const MASK = (1n << 64n) - 1n;
const RC = [0x0000000000000001n, 0x0000000000008082n, 0x800000000000808an, 0x8000000080008000n, 0x000000000000808bn, 0x0000000080000001n,
  0x8000000080008081n, 0x8000000000008009n, 0x000000000000008an, 0x0000000000000088n, 0x0000000080008009n, 0x000000008000000an,
  0x000000008000808bn, 0x800000000000008bn, 0x8000000000008089n, 0x8000000000008003n, 0x8000000000008002n, 0x8000000000000080n,
  0x000000000000800an, 0x800000008000000an, 0x8000000080008081n, 0x8000000000008080n, 0x0000000080000001n, 0x8000000080008008n];
// the rotation of lane (x, y), at index x + 5y
const ROT = [0, 1, 62, 28, 27, 36, 44, 6, 55, 20, 3, 10, 43, 25, 39, 41, 45, 15, 21, 8, 18, 2, 61, 56, 14];

function rotl(v, n) { return n ? ((v << BigInt(n)) | (v >> BigInt(64 - n))) & MASK : v; }

// keccakF is the Keccak-f[1600] permutation over 25 lanes, lane (x, y) at index x + 5y.
function keccakF(A) {
  const C = new Array(5), B = new Array(25);
  for (let round = 0; round < 24; round++) {
    for (let x = 0; x < 5; x++) C[x] = A[x] ^ A[x + 5] ^ A[x + 10] ^ A[x + 15] ^ A[x + 20];
    for (let x = 0; x < 5; x++) {
      const d = C[(x + 4) % 5] ^ rotl(C[(x + 1) % 5], 1);
      for (let y = 0; y < 25; y += 5) A[x + y] ^= d;
    }
    for (let x = 0; x < 5; x++) for (let y = 0; y < 5; y++) B[y + 5 * ((2 * x + 3 * y) % 5)] = rotl(A[x + 5 * y], ROT[x + 5 * y]);
    for (let x = 0; x < 5; x++) for (let y = 0; y < 5; y++) A[x + 5 * y] = B[x + 5 * y] ^ (~B[(x + 1) % 5 + 5 * y] & MASK & B[(x + 2) % 5 + 5 * y]);
    A[0] ^= RC[round];
  }
}

// keccak256 is Ethereum's hash (Keccak's padding, not SHA-3's): 32 bytes.
export function keccak256(bytes) {
  const rate = 136, A = new Array(25).fill(0n);
  const padded = new Uint8Array(Math.floor(bytes.length / rate) * rate + rate);
  padded.set(bytes);
  padded[bytes.length] ^= 0x01;
  padded[padded.length - 1] ^= 0x80;
  for (let off = 0; off < padded.length; off += rate) {
    for (let i = 0; i < rate / 8; i++) {
      let lane = 0n;
      for (let b = 7; b >= 0; b--) lane = (lane << 8n) | BigInt(padded[off + i * 8 + b]);
      A[i] ^= lane;
    }
    keccakF(A);
  }
  const out = new Uint8Array(32);
  for (let i = 0; i < 4; i++) { let lane = A[i]; for (let b = 0; b < 8; b++) { out[i * 8 + b] = Number(lane & 0xffn); lane >>= 8n; } }
  return out;
}

export function hex(bytes) { return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join(''); }

// checksum is an address in its EIP-55 case: a letter is upper case where the matching nibble of keccak-256 of the
// lower-case hex is 8 or more.
export function checksum(addr) {
  const lower = String(addr).replace(/^0x/i, '').toLowerCase();
  const h = keccak256(new TextEncoder().encode(lower));
  let out = '0x';
  for (let i = 0; i < 40; i++) {
    const c = lower[i], nib = (h[i >> 1] >> (i % 2 ? 0 : 4)) & 15;
    out += c >= 'a' && c <= 'f' && nib >= 8 ? c.toUpperCase() : c;
  }
  return out;
}

export function isEthAddress(s) { return /^0x[0-9a-fA-F]{40}$/.test(String(s || '').trim()); }
export function isTxHash(s) { return /^0x[0-9a-fA-F]{64}$/.test(String(s || '').trim()); }

// CONTRACT_NAMES are the deployment's sixteen contracts as /api/chains names them (a token's by its symbol), in words
const CONTRACT_NAMES = { mailbox: 'mailbox', gate: 'pausable gate', admin: 'admin contract (HlAdmin)', client: 'light client', proxyAdmin: 'ProxyAdmin',
  merkleTreeHook: 'MerkleTreeHook', protocolFee: 'protocol-fee hook', mailboxImpl: 'mailbox’s implementation', token0Impl: 'GNOT token’s implementation',
  token1Impl: 'BUBBLE token’s implementation', clientImpl: 'light client’s implementation', gateImpl: 'pausable gate’s implementation',
  ed25519Verifier: 'signature verifier (Ed25519Verifier)', hlAdminExec: 'admin contract’s executor (HlAdminExec)' };

// recipient reads an Ethereum recipient as the page accepts one (ER10, eth/bridge/PLAN.md §5.2: "the site and the
// tools accept only EIP-55 checksummed input"): 0x and 40 hex digits in its EIP-55 case, exactly. An address typed in
// one case carries no checksum, so a typo in it would pass unseen: it is refused, and the page never shows its
// checksummed form — that form is a typo's checksum too, so offering it would hand the typo a valid checksum (round
// 3's E3-CF6). Never the zero address, a precompile's (0x…0001 to 0x…ffff), or any contract of the bridge's Ethereum
// deployment. It answers {ok, addr} or {why}.
export function recipient(text, chain) {
  const s = String(text || '').trim();
  if (/^(0x)?[0-9a-fA-F]{64}$/.test(s)) return { why: 'write the address as 20 bytes: 0x and 40 hex digits, as your wallet shows it' };
  if (!isEthAddress(s)) return { why: 'the recipient must be an Ethereum address: 0x and 40 hex digits' };
  const cs = checksum(s), body = s.slice(2);
  const n = BigInt('0x' + body);
  if (n === 0n) return { why: 'that is the zero address: the token refuses to mint to it, and the transfer would never land' };
  if (n <= 0xffffn) return { why: `${cs} is a precompile's address (0x…0001 to 0x…ffff): nobody can ever move what is minted there` };
  for (const [name, a] of Object.entries((chain && chain.contracts) || {})) {
    if (String(a).toLowerCase() === s.toLowerCase()) {
      const what = CONTRACT_NAMES[name] || name + ' token contract';
      return { why: `${cs} is the bridge's ${what} on ${chain.name}: what is minted to it is stuck there; name a wallet` };
    }
  }
  const mixed = /[a-f]/.test(body) && /[A-F]/.test(body);
  if (mixed && s !== cs) return { why: 'the address’s mixed case is not its checksum (a typo?): copy it again as your wallet shows it' };
  if (s !== cs) return { why: 'this address is all in one case, so it carries no checksum and a typo in it would go unseen: copy it again from your wallet, in its checksummed (mixed) case' };
  return { ok: true, addr: cs };
}
