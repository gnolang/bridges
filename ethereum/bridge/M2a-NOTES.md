# E-M2a — `Ed25519.sol`: Go's `crypto/ed25519.Verify` on Ethereum

Lane E-M2a, 2026-10-14, branch `eth-m2a` (PLAN.md §2, §5.9, §6.6; BUILD.md). Every number here was measured on the
devbox: forge 1.8.5, solc 0.8.33, EVM `cancun`, optimizer on at 10,000 runs, the legacy pipeline (`via_ir = false`):
lane E-M2b's settings, which are Hyperlane 12.2.0's.

## 1. Interface, and what lane E-M2b needs (read first)

**The library**, `src/lib/Ed25519.sol`, with the placeholder's signature unchanged:

```solidity
library Ed25519 {
    function verify(bytes32 publicKey, bytes memory message, bytes32 r, bytes32 s) internal view returns (bool);
}
```

- `publicKey`, `r`, `s` are the 32-byte encodings exactly as tm2 carries them: `publicKey` is the validator's
  `PubKeyEd25519` (the 32 bytes after `0A 20` in the validator leaf), `r` is `sig[0:32]`, `s` is `sig[32:64]`. The
  caller reverses no bytes and never decodes, re-encodes or canonicalizes the key: those bytes are what is hashed.
  The key's 32 bytes are the library's only key input; it derives every value it computes with from them.
- It returns Go's verdict (§3) and never reverts on any input it can carry, out of gas aside; called externally through
  `Ed25519Verifier`, a well-formed call with no value and enough gas (a malformed or truncated calldata, or value,
  reverts — audit round 2, E2-ED2).
- **The caller refuses a signature that is not 64 bytes** before calling: tm2's `PubKeyEd25519.VerifyBytes` does,
  and the interface cannot carry another length.
- solc warns 2018 ("can be restricted to pure"): the body is pure, the signature keeps the `view` it was given.

**Proposal: GnoLightClient calls it through `Ed25519Verifier`, not inline.** Inline, the library adds **6,648 bytes**
of runtime code to its caller (Ed25519.sol with Sha512.sol: a one-function caller, 7,126 bytes, minus the same
function without the library, 478). E-M2b's `GnoLightClient` is 20,279-20,456 bytes with the placeholder (its devbox
build of 2026-10-14; 4,120 bytes free by E-M2b's own count), so inline it would be about 27,000, over the 24,576 of
EIP-170. Hence:

- `src/Ed25519Verifier.sol` (this branch) holds `interface IEd25519Verifier { function verify(bytes32 publicKey,
  bytes calldata message, bytes32 r, bytes32 s) external view returns (bool); }` and `contract Ed25519Verifier`,
  which only forwards to the library: no storage, no owner, no constructor arguments, never behind a proxy;
  **7,007 bytes** of runtime code.
- In GnoLightClient: `IEd25519Verifier public immutable ED25519;` set by the implementation's constructor, and the
  call `Ed25519.verify(pubkey, signBytes, r, s)` (GnoLightClient.sol:304 on E-M2b's branch today) becomes
  `ED25519.verify(pubkey, signBytes, r, s)`. A caller through it carries 306 bytes more than an external function
  without it.
- PLAN §5.8: a step before step 4 deploys `Ed25519Verifier` (once per chain), and the client implementation's
  constructor takes its address. `hleth audit` compares the verifier's runtime code with this tree's build (it has no
  immutables to mask) and the implementation's `ED25519` with the verifier's address. Replacing the verifier is an
  upgrade of the client (kind 8); it adds no authority.
- Cost: one call costs about 5,600 gas more than inline (§5), plus 2,500 for the first call of a transaction (a
  cold account). Each call starts with fresh memory, so the library's ~9 KB of scratch memory never stacks on the
  caller's: inline, a caller with a large memory footprint pays quadratic memory expansion on it (a test holding
  about 1 MB in memory paid 31,000-455,000 gas more per verification than a fresh frame does).
- To keep it inline instead, GnoLightClient must stay under 17,928 bytes without it.

**Compiler:** the legacy pipeline only. With `via_ir = true` at 10,000 runs, `Ed25519Verifier` inlines to 49,405
bytes (over EIP-170), and a contract with any assembly not marked memory-safe fails "stack too deep" in SHA-512's
rounds. Switching to via-IR means re-running every gate below on that build.

**Merging into `bridge-eth`:**
- `foundry.toml`: both branches add one; keep E-M2b's. It already has every setting this lane's tests need (solc
  0.8.33, `cancun`, 10,000 runs, `via_ir = false`, `forge-std/=lib/forge-std/src/`, `fs_permissions` read on
  `./test/vectors`, the large `gas_limit`); this lane's file holds nothing else.
- `src/lib/Ed25519.sol`: this branch's replaces the placeholder, which E-M2b did not change.
- New files, overlapping nothing: `src/lib/Sha512.sol`, `src/Ed25519Verifier.sol`, `test/Ed25519*.t.sol`,
  `test/vectors/ed25519/` (its `.gitignore` keeps out `diff/`), `tools/ed25519vectors/` (standard library only; no
  `go.mod` change).
- The tests import `forge-std/Test.sol` (E-M2b's `lib/forge-std`, v1.16.2, is the copy these runs used) and this
  tree's sources by relative path.

## 2. Approach

**Not SCL.** The default was to wrap SCL (get-smooth/crypto-lib `d714e98`). Read against Go, its `Verify_LE`
differs in four ways, three of which a wrapper cannot repair without replacing SCL's core:
1. It takes the key as a precomputed point (`extKpub`: Weierstrass x, y, the same for [2^128]A, and the 32 bytes) and
   never ties the point to the bytes it hashes. Reproduced on the devbox (`tools/ed25519vectors/sclbench`, §6): a
   signature by A2's secret with A's bytes hashed in (SCL's own `Sign`), handed A2's point beside A's bytes, makes
   `Verify_LE` return true; `Ed25519.verify` refuses it under A's bytes and under A2's.
2. It refuses S = 0, which Go accepts (reachable with small-order keys, or by a 2^-252 chance).
3. It computes [S]B + [L - k]A. That equals Go's [S]B - [k]A only when A has no torsion component: for A = A' + T
   it is off by [L]T, which is not the identity for T of order 2, 4 or 8 (L = 5 mod 8). Verdicts on mixed-order and
   small-order keys differ from Go's.
4. It works in Weierstrass coordinates through the birational map, whose exceptional points are the identity (0, 1)
   and the point of order 2, (0, -1): `Edwards2WeierStrass` divides by 1 - y and by x, which are zero there.
A correct wrapper would decompress A (a square root), convert it, and run SCL's 128 Weierstrass doublings for
[2^128]A per key, and still need its own scalar handling for (3). SCL also costs 2.4-3.5 times this library on the
same inputs in the same EVM (§5). And the Veridise-audited commit `c559657` is not in the public history: all six
branches and the tags of get-smooth/crypto-lib (328 commits) were fetched and searched; the report
(`doc/Audits/VAR_SmoothCryptoLib_240906_V2.pdf`) names only the short hash, reviewed 2024-07-18 to 07-30, so its
diff to `d714e98` cannot be taken. The nearest public commit is `484651b` "Veridise audit start"
(2024-07-17); after it come the audit fixes (`c7552d0` VERIDISE-002, the S range; `b0bcd2c` VERIDISE-001; `59abff9`
VERIDISE-003; `605cdb5` VERIDISE-005) and then unaudited changes to the same files (`87bd617` "gas optimization",
`8efe888`/`62b9d30` sqrtMod reverts, `cc24ee4` revert handling): 380 lines added and 110 removed in the ed25519
files from `484651b` to `d714e98`. SCL is not vendored; nothing of it is in this tree.

**This library** follows Go's own code path, step for step:

| Go 1.25.9 (`crypto/internal/fips140/…`) | `Ed25519.sol` |
|---|---|
| `verifyWithDom`: `len(sig) != 64` refused | the caller's (§1); the interface carries r and s |
| `sig[63]&224 != 0` refused | `uint256(s) & 0xe0 != 0` (s's last byte is the low byte of `uint256(s)`) |
| `NewPublicKey` → `Point.SetBytes`: y = the low 255 bits, unreduced (field arithmetic is mod p) | `_decode`: y = the low 255 bits, minus p when ≥ p |
| u = y² - 1, v = d y² + 1, `SqrtRatio(u, v)`: r = u v³ (u v⁷)^((p-5)/8); v r² = u → r, = -u → r √-1, else not square | the same candidate and the same two checks (`_pow250`, Go's addition chain); anything else refused |
| `Absolute` (the even root), then `Select(-x, x, sign)` | x odd → p - x; sign bit set and x ≠ 0 → p - x |
| `kh = SHA-512(sig[:32] ‖ pub.aBytes ‖ message)`, aBytes the bytes as given | `Sha512.hash(abi.encodePacked(r, publicKey, message))` |
| `SetUniformBytes`: the 64 bytes little-endian, mod L | k = (LE(h1) 2^256 + LE(h0)) mod L |
| `SetCanonicalBytes(sig[32:])`: S < L, S = 0 allowed | `LE(s) >= L` refused |
| `VarTimeDoubleScalarBaseMult(k, -A, S)` = [k](-A) + [S]B | `_doubleScalarMul`: the same sum, by Straus's joint 2-bit windows over a table of [i]B + [j](-A), with Go's formulas (`projP1xP1.Add`, `.Double`, `fromP1xP1`) |
| `R.Bytes()`: `Invert(Z)` (the p - 2 chain), y's 32 bytes, x's low bit in the top bit | `_invert` (Go's chain), the same encoding as an integer |
| `bytes.Equal(sig[:32], R.Bytes())` | LE(r) equals y with x's low bit as bit 255 |

The group arithmetic is the whole curve group: the twisted Edwards addition law is complete for a = -1 (a square)
and d (not a square), and Go's doubling and addition formulas inherit it, so a sum of any two points, small-order
and mixed-order ones included, comes out exactly; a different window method than Go's NAF reaches the same point
because the group law is unique. Inside the window loop, sums that only feed a `mulmod` are left unreduced: each is
below 2p < 2^256 (the bound is noted at every one), and a difference x - y is written x + (p - y) for y ≤ p, or
x + (2p - y) for y < 2p and only inside an `addmod`. SHA-512 is FIPS 180-4 in assembly (`src/lib/Sha512.sol`), eight
rounds per pass with the roles renamed. The scalar multiplication handles public inputs only, so nothing needs to
run in constant time.

## 3. Divergences from Go

**None** on any input the interface carries: every gate below agrees with Go on every record. Two inputs it cannot
carry, and neither reaches it from tm2: a signature that is not 64 bytes (tm2 refuses it before Verify; the caller
must too, §1), and a key that is not 32 bytes (Go's `Verify` panics; tm2's keys are `[32]byte`). Go checks in a
different order (it hashes before it checks S); a verdict is the conjunction of the checks, so the order cannot
change it.

## 4. Gates

| # | Gate | Records | Go accepts | Disagreements |
|---|---|---|---|---|
| 1 | RFC 8032 §7.1, the five Ed25519 tests (TEST 1, 2, 3, 1024 with its 1023-byte message, SHA(abc)); each also refused after an appended byte, a changed message bit (TEST 1's message is empty), a changed R bit and a changed S bit | 5 (+19 tampered) | 5 | 0 |
| 2 | Wycheproof `testvectors_v1/ed25519_test.json`, C2SP/wycheproof `7a325932` | 151 | 88 | 0 |
| 3 | "Taming the many EdDSAs" `cases.json`, novifinancial/ed25519-speccheck `65519336` | 12 | 5 | 0 |
| 4 | the differential set (below) | 118,392 | 21,570 | 0 |
| 5 | the key binding (below) | 384 | 32 | 0 |
| 6 | SHA-512: FIPS 180-4's examples and Go's `crypto/sha512` (below) | 951 + 1 | — | 0 |
| — | named edge cases, each with the verdict the rules predict (Go's equals it) | 25 | 11 | 0 |
| — | every published set, the edges and the binding cases again through `Ed25519Verifier` | 577 | 141 | 0 |

**Gate 2.** Of the 151 cases, 12 carry a signature that is not 64 bytes (0, 32, 62, 63×4, 65×3, 66 and 96 bytes): Go
refuses them by length, and the tests check that the caller's rule (§1) does the same. Wycheproof's own `result`
agrees with Go's verdict on all 151 cases (88 valid, 63 invalid; this version has no "acceptable").

**Gate 3.** Go accepts cases 0-3 and 11 and refuses 4-10, which is Go's row in speccheck's `results.md`
(V V V V X X X X X X X V); the library agrees on all twelve.

**Gate 4: the differential set.** `tools/ed25519vectors diff` (seed `f9188d76…5c48` = sha256("eth-m2a ed25519
differential v1"); manifest sha256 `c13f8ae9…cf62`, the 60 chunks concatenated `277e1a63…96b1`), every record with
`crypto/ed25519.Verify`'s verdict:

| Family | Records | Go accepts | What |
|---|---|---|---|
| valid | 100,000 | 20,000 | 20,000 honest signatures (fresh keys; 10,000 messages of 0-300 random bytes, 10,000 of exactly 110 bytes), each followed by four copies with one random bit flipped: in R, in S, in A, in M (R again when M is empty) |
| random | 10,000 | 0 | random 64-byte signatures under random 32-byte keys; random R with canonical S under real keys; and under random keys (about half off the curve) |
| smallkey | 1,604 | 840 | all 14 encodings of the 8 points of order dividing 8 as the key (8 canonical; 6 only a lax decoder takes: y ≥ p, or x = 0 with the sign bit), with R = [S]B - [j]T and the message ground so that k = j (accepted) or not (refused) mod ord(T); with S = 0, R' itself small, written in every encoding |
| mixedkey | 1,401 | 292 | A = [a]B + T for every T of order 2, 4, 8, signed honestly: cofactorless accepts exactly when ord(T) divides k |
| smallR | 271 | 108 | R a small-order point: the identity in its 4 encodings under prime-order keys (S = k a), and every encoding of -[k]T under mixed keys |
| edgeS | 3,196 | 80 | S + L, S + 2L, S + 8L and S with bit 253, 254 or 255 set on honest signatures; under the identity key in all 4 encodings, S = 0, 1, 2, L-1, L-2, 2^252-1, 2^252, L-2^128 and random (accepted), the same + L, and L, L+1, L+2, L+2^252, 2^253-1, 2^253, 2^255-1, 2^255, 2^256-1 (refused) |
| laxkey | 420 | 0 | every key written with y = p + δ (δ = 0..18, both signs) and keys with no x, under random signatures |
| negR | 500 | 0 | honest signatures with R's sign bit flipped (-R) |
| binding | 1,000 | 250 | as gate 5, 250 times |

The generator checks a second, independent model of the rules (`model.go`: affine math/big arithmetic, Atkin's
square root, no code shared with Go or the library) against `crypto/ed25519` on every record before writing it; it
agreed on all of them. **Go versions:** the verify path is the same source in Go 1.25.9 (what gno v1.2.0's `go.mod`
names) and Go 1.27.1 (the devbox's): `fips140/ed25519/ed25519.go`, `edwards25519.go` and `scalar.go` are identical;
`scalarmult.go` differs only in a loop that skips leading zero digits (1.25.9's never advances, which only doubles
the identity), `fe.go` only in the addition chains for the same two exponents. Every vector file, the differential
set included, came out byte for byte the same when generated under Go 1.25.9 and under Go 1.27.1. tm2 v1.2.0 calls
`golang.org/x/crypto/ed25519.Verify` (x/crypto v0.53.0), which is `crypto/ed25519.Verify`, after refusing a
signature that is not 64 bytes (`tm2/pkg/crypto/ed25519/ed25519.go`).

**Gate 5: the key binding**, checked by stated outcome as well as against Go, since a Go-derived differential run
never pairs a point with foreign bytes (`test_binding_signatureByAnotherKeyRefused`): 32 times, A2's secret signs
with A's bytes hashed in (R = [r]B, S = r + H(R ‖ A ‖ M) a2: the forgery SCL accepted) and is refused under A and
under A2 (64); A2's honest signature is refused under A (32) and accepted under A2 (32, the machinery's control);
and an honest signature is refused under each of the 256 one-bit changes of its key's bytes. The library exposes no
point input, so no test can hand it A2's point beside A's bytes; the point is computed from the bytes inside it.

**Gate 6.** 951 vectors: the FIPS 180-4 examples ("abc", the two-block 112-byte message, the empty message), Go on
three random messages of every length 0-300, all-zero and all-0xff messages at every padding boundary (0, 1, 63,
64, 111, 112, 113, 127, 128, 129, 175, 176, 239-241, 255-257, 383, 384), and five long messages (512-4,096 bytes);
plus one million bytes of 'a' (FIPS 180-2 C.3).

**Never reverts:** fuzzed 256 times on fully random inputs and 256 times on the full path (real keys, S < L).

**Since audit round 2 (E2-ED1, fold F4):** the round-2 auditor planted five key-decoding bugs that every gate above
missed (three of them universal forgeries); `test/Ed25519Decode.t.sol` (478 keys against Go's `Point.SetBytes`, in
`decode.vec`) and the forgery probes (`probes.vec`, 1,104 records) now catch them, `mutants.py` carries them as
N1–N5, and all 18 mutants fail the gates (M2-NOTES.md, Fold F4).

**The gates bite** (`tools/ed25519vectors/mutants.py`): each mutant plants one plausible verifier bug in a copy of the
library; the pristine copy first passes every gate (the control), then each mutant must fail at least one:

| Mutant | Failing tests (of 15 unit and vector tests; of 17 differential) |
|---|---|
| S = L accepted | edges, the verifier-contract replay; 1 lane (edgeS) |
| S reduced mod L instead of refused (any S; the top bits ignored) | edges, Wycheproof, speccheck, replay; 3 lanes |
| S = 0 refused (SCL) | edges, speccheck, replay; 2 lanes (smallkey, edgeS) |
| a key with y ≥ p refused (strict decoding) | edges, replay; 2 lanes |
| a key's sign bit on a zero x refused | edges, speccheck, replay; 2 lanes |
| a key's sign bit ignored | 9 tests, RFC 8032 among them; 16 lanes |
| A re-encoded canonically before hashing | edges, speccheck, replay; 1 lane (smallkey) |
| [L - k]A instead of -[k]A (SCL) | edges, speccheck, replay; 2 lanes (smallkey, mixedkey) |
| a non-canonical R accepted (y mod p, the sign of a zero x ignored) | edges, Wycheproof, speccheck, replay; 2 lanes |
| 2ZZ left unreduced in the addition (a sum past 2^256) | 9 tests; 16 lanes |
| the top window (bits 252-253) skipped | edges, replay; 1 lane (edgeS: S ≥ 2^252) |
| SHA-512's padding one byte short | the SHA-512 vectors, both gas tests; 16 lanes |
| k = the digest's low 253 bits instead of the digest mod L | 9 tests; 16 lanes |

All thirteen are caught. Nine of them only by crafted cases (the named edges, the published sets, and the
smallkey, mixedkey, smallR and edgeS families; lanes 7-10 hold those chunks): random and honest signatures never
reach S = L, S = 0, S ≥ 2^252, a key with y ≥ p or a zero x under the sign bit, a non-canonical R or a torsion
component, which is why the generator crafts them. Two of SCL's divergences that a wrapper could not mend (S = 0,
[L - k]A) are among them.

## 5. Gas

One verification, measured around the internal call in a fresh call frame (so no caller memory is counted), over
the 1,600 honest signatures of the differential set's first four chunks; the gas varies by signature because a
window whose two digits are both zero skips its addition:

| Message | SHA-512 blocks | `Ed25519.verify`, min / mean / max | n | SCL `Verify_LE`, same inputs | SCL ÷ this |
|---|---|---|---|---|---|
| 0-47 bytes | 1 | 181,471 / 183,215 / 184,654 | 130 | 445,722 (0 bytes; this: 182,253) | 2.45 |
| 48-175 bytes, 110 among them (gnoland-1's precommit sign bytes) | 2 | 217,558 / 220,816 / 222,873 | 1,123 | 678,852 (110 bytes; this: 221,940) | 3.06 |
| 176-300 bytes | 3 | 256,036 / 258,450 / 260,289 | 347 | 910,950 (300 bytes; this: 257,138) | 3.54 |

Through `Ed25519Verifier` from a caller holding its address, one call costs about 5,600 more (110 bytes: 227,344
against 221,747 for the same signature inline), and 2,500 more again for the first call of a transaction. Every
figure is the same under `osaka` rules (compiled for and run under each; checked): the library uses no operation a
fork since Cancun repriced (no MODEXP: both exponentiations are addition chains of `mulmod`).

By part, for a 110-byte message: decoding A 9,735 (one exponentiation), SHA-512 of R ‖ A ‖ M 77,773 (two blocks,
the packing included; about 38,000 a block), the double-scalar multiplication 125,433, the inversion 8,758, the
five byte reversals 1,410. The double-scalar multiplication is a 15-entry table and 127 windows of two doublings
and (in 15 of 16) one addition, about 40 field operations a window; the hot loop compiles to three DUPs and a MULMOD
per product, and lazy reduction took about 16,000 off it, so little is left there without fewer operations.

**Against SCL**, on the same keys and messages in the same EVM and settings (`scl-bench`: SCL `d714e98` compiled with
solc 0.8.33, cancun, 10,000 runs; SCL's own 1,000,000 runs gives the same within 0.1%): SCL's `Verify_LE` costs
445,722 / 678,852 / 910,950 gas at 0 / 110 / 300 bytes, against 182,253 / 221,940 / 257,138 for `Ed25519.verify`
on the same signatures: 2.45, 3.06 and 3.54 times. SCL's figure leaves out what it would need to bind the key
(§2). The research's 808-810K at 110 bytes (eth_estimateGas on Base, with 21,000 and calldata) is the same SCL
measured another way; against it the factor is about 3.6.

**What it means for the client** (E-M2b measures the update itself): gnoland-1's update needs 5 signatures (more than
2/3 of 6 × 60), about 1,140,000 gas of signature checks through the verifier; EIP-7825's 16,777,216-gas
transaction cap leaves room for about 73 signatures before the client's own work (SCL's ~810K allowed about 20).
With E-M2b's update cost without signatures (its M2-NOTES: 344K at 6 validators, 553K at 20, 863K at 40), an
update by more than 2/3 of equal powers comes to about 1.5M gas at 6 validators, 3.7M at 20 and 7.0M at 40, and
stays inside one transaction up to about 95 validators (beyond, PLAN §6.3's staging).

**Bytecode:** inline 6,648 bytes; `Ed25519Verifier` 7,007 bytes runtime; a caller through it +306 bytes (§1).

## 6. Commands

On the devbox, from a copy of the worktree (`/root/src/eth-m2a`):

```sh
cd eth/bridge
go vet ./tools/ed25519vectors && gofmt -l tools/
D=/root/src/eth-m2a-deps  # rfc8032.txt, wycheproof @7a325932, ed25519-speccheck @65519336 (sha256-pinned)
go run ./tools/ed25519vectors sources -rfc8032 $D/rfc8032.txt \
  -wycheproof $D/wycheproof/testvectors_v1/ed25519_test.json -speccheck $D/ed25519-speccheck/cases.json \
  -out contracts/test/vectors/ed25519                                    # gates 1-3 (committed)
go run ./tools/ed25519vectors local -out contracts/test/vectors/ed25519  # edges, binding, gas, sha512 (committed)
go run ./tools/ed25519vectors diff -out contracts/test/vectors/ed25519/diff   # gate 4: ~4 min, 56 MB, not committed
cd contracts
forge test --no-match-contract Differential -vv    # gates 1-3, 5, 6, edges, the verifier contract, gas, fuzz
forge test --match-contract Ed25519Differential -vv   # gate 4: 16 lanes + the manifest, ~30 s; none may be skipped
```

The differential tests skip, visibly, when `diff/manifest.txt` is absent; the gate is a run in which all 17 pass.
Regenerating any committed vector file reproduces it byte for byte (under Go 1.25.9 or 1.27.1).

The mutation check (§4) and the SCL comparison (§2, §5) run in scratch Foundry projects, not in this one: the first
needs a copy of `contracts/` to plant bugs in (`python3 -I tools/ed25519vectors/mutants.py <copy> contracts`, about
a minute a mutant), the second needs SCL, which is not vendored (`tools/ed25519vectors/sclbench/Bench.t.sol` says how
to lay it out).

## 7. Files

- `contracts/src/lib/Ed25519.sol` — the library.
- `contracts/src/lib/Sha512.sol` — SHA-512.
- `contracts/src/Ed25519Verifier.sol` — the verifier as a contract, and its interface (§1).
- `contracts/test/Ed25519Vectors.t.sol` — the vector reader; gates 1-3 and the edges.
- `contracts/test/Ed25519.t.sol` — gate 5, never-reverts fuzzing, gas and bytecode, the verifier contract.
- `contracts/test/Ed25519Differential.t.sol` — gate 4.
- `contracts/test/Ed25519Sha512.t.sol` — gate 6.
- `contracts/test/vectors/ed25519/` — `rfc8032.vec`, `wycheproof.vec`, `speccheck.vec`, `edges.vec`, `binding.vec`,
  `gas.vec`, `sha512.vec`; `diff/` generated.
- `tools/ed25519vectors/` — the generator: `main.go` (commands, edges), `model.go` (the math/big model),
  `sources.go` (the pinned published sets), `diff.go` (the differential families), `records.go` (Go's verdicts, the
  model cross-check, the file format), `sha.go` (SHA-512 vectors); `mutants.py` (§4); `sclbench/` (§2, §5).
- `contracts/foundry.toml` — minimal; superseded by E-M2b's at the merge (§1).
