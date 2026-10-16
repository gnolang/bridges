#!/usr/bin/env python3
"""The mutation check of eth/bridge/M2a-NOTES.md §4: each mutant plants one plausible verifier bug (several are SCL's
divergences from Go) in a scratch copy of Ed25519.sol or Sha512.sol, and the gates must fail on every one; the pristine
copy runs first and must pass everything, or a "caught" would mean nothing. N1-N5 are audit round 2's planted decoding
bugs (E2-ED1, eth/bridge/audits/r2/ed25519.md), three of them universal forgeries, which passed every gate before
test/Ed25519Decode.t.sol (the direct decode vectors and the forgery probes); the filter "decode:" runs those five.
The seven "E3-ED" ones are audit round 3's (its Ed25519 report; the auditor's mut/arts S4, S3, D7, VB1, C1, C3b, H1):
each was caught by one witness at most — S4 and S3 by Wycheproof tc15, C1 by tc59, C3b by one edge case, D7 by the
direct decode test, VB1 by its gas tripwire alone, H1 by nothing — and is caught now by a generated family (edges.vec's
special, negR and laxR; probes.vec's .asB) or the dirty-memory tests; the filter "E3-ED" runs those seven.

Usage, on the devbox: python3 -I mutants.py <scratch project> <contracts dir>. The scratch project is a copy of
contracts/ (src, test, foundry.toml, lib/forge-std) whose test/vectors/ed25519/diff holds the differential set as
real files (a symlink out of the project is refused by forge's fs_permissions); the contracts dir is the pristine
tree it copies from. About a minute a mutant."""
import os, re, shutil, subprocess, sys
exp, pristine = sys.argv[1], sys.argv[2]
ED = "src/lib/Ed25519.sol"
SHA = "src/lib/Sha512.sol"
NOROOT = "            if (check != P - u) return (false, 0, 0);\n"
# B's affine coordinates (what D7 and VB1 read a refused key as)
BX = "0x216936d3cd6e53fec0a4e231fdd6dc5c692cc7609525a7b2c9562d608f25d51a"
BY = "0x6666666666666666666666666666666666666666666666666666666666666658"
M = [
 ("S = L accepted", ED, "|| sv >= L) return false;", "|| sv > L) return false;"),
 ("S reduced mod L instead of refused (any S, top bits ignored)", ED, "if (uint256(s) & 0xe0 != 0 || sv >= L) return false;", "sv = sv % L;"),
 ("S = 0 refused (SCL)", ED, "|| sv >= L) return false;", "|| sv >= L || sv == 0) return false;"),
 ("key y >= p refused (strict decoding)", ED, "if (y >= P) y -= P; // y < 2^255 < 2p", "if (y >= P) return (false, 0, 0);"),
 ("key sign bit on a zero x refused", ED, "if (v >> 255 == 1 && x != 0) x = P - x;", "if (v >> 255 == 1) { if (x == 0) return (false, 0, 0); x = P - x; }"),
 ("key sign bit ignored", ED, "if (v >> 255 == 1 && x != 0) x = P - x;", ""),
 ("A re-encoded canonically before hashing", ED, "Sha512.hash(abi.encodePacked(r, publicKey, message))", "Sha512.hash(abi.encodePacked(r, bytes32(_le(bytes32(ay | ((ax & 1) << 255)))), message))"),
 ("[L-k]A instead of -[k]A (SCL)", ED, "_doubleScalarMul(sv, k, ax, ay)", "_doubleScalarMul(sv, L - k, P - ax, ay)"),
 ("non-canonical R accepted (y mod p, sign of zero x ignored)", ED, [("return _le(r) == (y | ((x & 1) << 255));", "return _laxR(_le(r), x, y);"),
   ("    /// The 32 bytes of `b` read as a little-endian integer", "    function _laxR(uint256 rr, uint256 x, uint256 y) private pure returns (bool) {\n        return (rr & ((1 << 255) - 1)) % P == y && ((rr >> 255) == (x & 1) || x == 0);\n    }\n\n    /// The 32 bytes of `b` read as a little-endian integer")], None),
 ("2ZZ left unreduced in the addition (overflow past 2^256)", ED, "zz := addmod(zz, zz, p) // 2ZZ, reduced", "zz := add(zz, zz)"),
 ("top window (bits 252-253) skipped", ED, "for { let sh := 254 } sh {} {", "for { let sh := 252 } sh {} {"),
 ("SHA-512 padding one byte short", SHA, "add(len, 144)", "add(len, 143)"),
 ("challenge k = low 253 bits of the digest, not mod L", ED, "uint256 k = addmod(mulmod(_le(h1), TWO256_MOD_L, L), _le(h0), L);", "uint256 k = _le(h0) & ((1 << 253) - 1);"),
 ("N1 decode: a key with no root decoded as the identity", ED, NOROOT, "            if (check != P - u) return (true, 0, 1);\n"),
 ("N2 decode: a key with no root, the -u check dropped (x sqrt(-1) taken)", ED, NOROOT, ""),
 ("N3 decode: a key with y >= p (y - p >= 2) decoded as the identity", ED, "if (y >= P) y -= P; // y < 2^255 < 2p", "if (y >= P) { y -= P; if (y > 1) return (true, 0, 1); }"),
 ("N4 decode: a key with no root decoded as (0, y)", ED, NOROOT, "            if (check != P - u) return (true, 0, y);\n"),
 ("N5 decode: a key with no root decoded as the point of order 2", ED, NOROOT, "            if (check != P - u) return (true, 0, P - 1);\n"),
 ("E3-ED1 S4: R = the identity's encoding with S = 0 accepted under any key", ED, "|| sv >= L) return false;", "|| sv >= L) return false;\n        if (sv == 0 && _le(r) == 1) return true;"),
 ("E3-ED1 S3: S = 0 accepted exactly when R is the identity's encoding", ED, "|| sv >= L) return false;", "|| sv >= L) return false;\n        if (sv == 0) return _le(r) == 1;"),
 ("E3-ED2 D7: a key with no root decoded as B", ED, NOROOT, "            if (check != P - u) return (true, " + BX + ", " + BY + ");\n"),
 ("E3-ED2 VB1: verify takes a key with no root as B", ED, "if (!onCurve) return false;", "if (!onCurve) { ax = " + BX + "; ay = " + BY + "; }"),
 ("E3-ED3 C1: R accepted with either sign of a nonzero x", ED, "return _le(r) == (y | ((x & 1) << 255));", "return _le(r) == (y | ((x & 1) << 255)) || _le(r) == (y | ((((P - x) % P) & 1) << 255));"),
 ("E3-ED3 C3b: R's y reduced mod p (y + p accepted)", ED, [("return _le(r) == (y | ((x & 1) << 255));", "return _laxY(_le(r), x, y);"),
   ("    /// The 32 bytes of `b` read as a little-endian integer", "    function _laxY(uint256 rr, uint256 x, uint256 y) private pure returns (bool) {\n        return ((rr & ((1 << 255) - 1)) % P) == y && (rr >> 255) == (x & 1);\n    }\n\n    /// The 32 bytes of `b` read as a little-endian integer")], None),
 ("E3-ED4 H1: SHA-512's padded buffer not zeroed", SHA, "calldatacopy(buf, calldatasize(), size) // zeros", ""),
]
env = dict(os.environ, PATH="/root/.foundry/bin:" + os.environ["PATH"])
def run(args):
    p = subprocess.run(["forge", "test"] + args, cwd=exp, env=env, capture_output=True, text=True)
    out = p.stdout + p.stderr
    m = re.findall(r"(\d+) tests passed, (\d+) failed", out)
    if not m:
        return None, out[-2000:]
    passed = sum(int(a) for a, b in m); failed = sum(int(b) for a, b in m)
    fails = sorted(set(re.findall(r"\[FAIL[^\]]*\] (\w+)", out)))
    return (passed, failed, fails), ""
# The control first: the pristine copy must pass everything, or a "caught" below would mean nothing.
for g in (ED, SHA):
    shutil.copy(os.path.join(pristine, g), os.path.join(exp, g))
fast, _ = run(["--no-match-contract", "Differential"]); diff, _ = run(["--match-contract", "Differential"])
print(f"CONTROL (pristine): unit/vector {fast[1]} failed of {fast[0]+fast[1]}; differential {diff[1]} failed of {diff[0]+diff[1]}", flush=True)
if fast[1] or diff[1]:
    sys.exit("the control fails: the instrument is broken")
only = sys.argv[3] if len(sys.argv) > 3 else ""
for name, f, old, new in M:
    if only not in name:
        continue
    for g in (ED, SHA):
        shutil.copy(os.path.join(pristine, g), os.path.join(exp, g))
    src = open(os.path.join(exp, f)).read()
    for o, n in (old if isinstance(old, list) else [(old, new)]):
        assert src.count(o) == 1, (name, src.count(o))
        src = src.replace(o, n)
    open(os.path.join(exp, f), "w").write(src)
    fast, err = run(["--no-match-contract", "Differential"])
    diff, err2 = run(["--match-contract", "Differential"])
    if fast is None or diff is None:
        print(f"MUTANT {name}: did not compile/run\n{err}{err2}", flush=True); continue
    caught = fast[1] + diff[1] > 0
    print(f"MUTANT {'CAUGHT' if caught else 'SURVIVED'}: {name}: unit/vector suites {fast[1]} failed; differential lanes {diff[1]} of {diff[0]+diff[1]} failed; failing: {', '.join((fast[2]+diff[2])[:8])}", flush=True)
for g in (ED, SHA):
    shutil.copy(os.path.join(pristine, g), os.path.join(exp, g))
fast, _ = run(["--no-match-contract", "Differential"]); diff, _ = run(["--match-contract", "Differential"])
print(f"PRISTINE: unit/vector {fast[1]} failed of {fast[0]+fast[1]}; differential {diff[1]} failed of {diff[0]+diff[1]}", flush=True)
