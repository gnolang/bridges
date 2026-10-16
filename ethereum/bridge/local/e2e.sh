#!/bin/sh
# The Ethereum bridge end to end on one machine, from nothing (eth/bridge/PLAN.md §5–§6; the Solana bridge's
# bridge/local/e2e.sh carried over): anvil pinned to mainnet's fork (osaka) with EIP-170's code size limit and
# EIP-7825's transaction gas cap enforced, `hleth setup` deploying the sixteen contracts (eleven codes, five proxies:
# M2-NOTES.md §1 G) and the setup step (Hyperlane 12.2.0's and OpenZeppelin's from the release's own compile, ours from
# the contracts tree built at this run's clock factor; every transaction through ethx's sender, the creations too) and
# `hleth audit` passing BEFORE any realm goes up (§5.8: the realms compile the deployment's addresses); a gnodev chain
# the realms are then uploaded to with this run's launch values swapped into STAGED copies (the governor = gnodev's
# test1, every contract address hlmailbox carries — the sixteen once the realms list them all — the routers, the
# signers' keys, the Ethereum seats, the test domain 31337, the realms' clock factor) and the governor's Launch;
# three signers, the updater, the relayer (both halves: gno.land → Ethereum, and the inbound job), the monitor with
# both watch keys, every scan starting at the config's eth.deployBlock. The amounts are the realms' own: each route's
# fee and minimum read from its json once it is up, never compiled here. Then:
#   - the GNOT minimum and two and a half BUBBLE minimums go gno.land → Ethereum and mint on anvil, through the gate
#     and the light client;
#   - the BUBBLE minimum comes back Ethereum → gno.land: signed by the signers, credited by the relayer's Process,
#     paid by its Claim with the fee first;
#   - half BUBBLE's fee is burned (dust, E5-GS5): signed by every signer, left by the relayer (pending, alarmed once,
#     never Processed), in flight in the monitor's identity and counted apart, waiting for nobody (F17a's R1);
#   - each header the updater bought for the sends is booked by their own fees, never a six-hour draw (a decision's
#     header books nothing: fold F15a);
#   - five decisions through r/hlgov, each delivered to HlAdmin and executed by the relayer after its delay (7 days at
#     this run's factor), all in one window: the protocol fee (kind 7); an upgrade of the gate to an implementation
#     built in this run WITH a call (kind 8, E4-RL4, E5-CF4: r/hlgov → HlAdmin → the ProxyAdmin's upgradeAndCall on
#     the 2,000,000 gas HlAdmin forwards, HlAdmin's after-checks inside the execute; the new code's initializer runs
#     once, through the proxy, its mark read back; the same execute left too little gas is refused CallStarved(),
#     simulated at the block before the relayer's; the proxy then answers the new code, its EIP-1967 slots the
#     decision's and the ProxyAdmin's, and the monitor's and hlwatch's slot watch holds them for the rest of the run —
#     seen at work: the implementation slot moved by hand (anvil) is alarmed proxy:slots by the monitor and keeps
#     hlwatch from green, until it is put back — every later step runs through the new gate); token 0's ISM set to the
#     mailbox's default (kind 3); guardian seat 4 replaced (kind 5); a second router of BUBBLE's, for another domain
#     (kind 6: none before it) — each read back on Ethereum;
#   - a kind 12 is recorded and cancelled by r/hlgov's cancel, executed at recording — told apart from an execution by
#     HlAdmin's logs (DecisionCancelled, no DecisionExecuted) and the gate's limits, still the launch's;
#   - a flag (hlwatch's stroke: hlmailbox, the gate, HlAdmin — both Ethereum strokes asserted) pauses deliveries: the
#     gate refuses the very deliveries the relayer makes later of a BUBBLE send and a GNOT send in flight (token 1's
#     ISM the gate; token 0's the mailbox's default since the kind 3, the gate) — Mailbox.process with the relayer's
#     own proof, simulated at a block under the flag: the gate's Paused(); at the block before the relayer's delivery:
#     taken — the sends wait it out, nothing minted, and deliver after the unpauses; the monitor mirrors the pause as
#     sends on the gno routes — and the governor's unpauses (gno.land at once; the gate's and HlAdmin's through
#     r/hlgov, kind 2, 24 hours at the factor) reopen them;
#   - two sends in two blocks, the updater held, are re-emitted by the relayer in one transaction of Reemit MsgCalls
#     (gnoland-1's run_submitters, set here as there through GovDAO, refuses a stranger's MsgRun with the allowlist's
#     own words — the same MsgRun goes before the param is set) and delivered under that block's header (E3-AS1);
#   - the identity lines hold on the monitor and on every signer, `hlwatch status` is green, and `hleth audit` of the
#     governed bridge PASSes with the member's own gno source (-gno-rpc: here a second spelling of the machines' node)
#     beside the machines' config's (every read compared) and the member's launch values and expected locks, each value
#     a decision moved held to the decision that moved it (E3-CF9) — and from the operator's config alone (its gno
#     source, its routes' locks) it is UNVERIFIED, never PASS (E5-CF3 item 3);
#   - the relayers' gno key holds the planned float (seven days of 50 credits, E5-RT5: runway:relayer-gno never raised),
#     and no alarm stands at the run's end but the dust step's.
# Not exercised here (each more than a small addition): a kind 9 (a successor HlAdmin is a deployment of its own with
# its own setup, r/hlgov's recipient swap and every machine following the line: cmd/hleth's
# TestAnvilAuditAfterGovernedChanges and TestAnvilDecisionCommandsFollowTheSuccessor hold it on anvil), the executor's
# starved resend (anvil's estimate is a search for the least gas that passes, so no execute here starves: the agent's
# TestExecutorResendsAStarvedExecuteWithMoreGas and forge's HlAdminKind8Bindings hold it), a staged update (one validator signs every header), the
# backup relayer and its takeover, sender replacements (anvil mines at once), the six-hour draw (the run's prices make
# every send's own fee cover its header), and a set of more than one validator. Every pipe whose left side can fail is
# checked by its own status (dash has no pipefail).
# Everything it starts, it stops; it fails loudly at the first thing that is not so.
#
#   eth/bridge/local/e2e.sh          (on the devbox: go, Foundry with anvil and forge, gnodev/gnokey v1.2.0, python3)
#
# Inputs, by environment: CONTRACTS a Foundry tree of the contracts (src, lib, script, foundry.toml; default this
# tree's eth/bridge/contracts), ED25519 a directory holding Ed25519.sol and Sha512.sol to stage over src/lib (default
# none: the tree's own), HLTGZ the @hyperlane-xyz/core 12.2.0 npm tarball, SOLC solc 0.8.33, EX gno's examples (GovDAO's
# realms), FACTOR the contracts' clock factor (4032: 7 days in 150 s, 24 hours in 21 s), BL the state directory
# (wiped each run). anvil enforces EIP-170 (every contract fits since E-M2b's split: M2-NOTES.md §5).
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
EB=$(cd "$HERE/.." && pwd)
REPO=$(cd "$EB/../.." && pwd)
BL=${BL:-$HOME/.cache/eth-bridge-e2e}
CONTRACTS=${CONTRACTS:-$EB/contracts}
ED25519=${ED25519:-}
HLTGZ=${HLTGZ:-/root/src/eth-m3-hl/hyperlane-xyz-core-12.2.0.tgz}
SOLC=${SOLC:-/root/.local/share/svm/0.8.33/solc-0.8.33}
EX=${EX:-/root/src/gno-v1.2.0/examples}
GNOBIN=${GNOBIN:-/root/.cache/gno-toolchains/v1.2.0}
FACTOR=${FACTOR:-4032}
REALM_FACTOR=${REALM_FACTOR:-1008}
export PATH="$GNOBIN:/root/.foundry/bin:/usr/local/go/bin:$PATH"
ADDR=g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr
NS="gno.land/r/$ADDR/eth"
TEST1=g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5
MNEMONIC="source bonus chronic canvas draft south burst lottery vacant surface solve popular case indicate oppose farm nothing bullet exhibit title speed wink action roast"
ANVIL_PORT=${ANVIL_PORT:-8745}
ETHRPC=http://127.0.0.1:$ANVIL_PORT
GNORPC=127.0.0.1:${GNO_PORT:-26757}
ETH_DOMAIN=31337
GNO_DOMAIN=1198419796
OTHER_DOMAIN=10 # a domain neither gno.land's nor Ethereum's: BUBBLE's second router's (kind 6, E5-CF4)
MARK=4242       # what the kind 8's call writes, read back through the gate's proxy (E5-CF4)
ONE=1000000
# the amounts follow the realms' own fees and minimums, read once the realms are up (never compiled here): GSEND is
# GNOT's minimum, SEND two and a half BUBBLE minimums, BACK (burned back from Ethereum) BUBBLE's minimum, the claim's
# first installment the fee
WATCH_FUND=200000000 # the gno watch key's: its flags' gas
DELAY=$((604800 / FACTOR))  # HlAdmin's 7 days at this run's factor
UNPAUSE=$((86400 / FACTOR)) # its 24 hours
DEPLOYER_KEY=ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 # anvil's first account
T0=$(date +%s)

say() { printf '\n== %s (%ss)\n' "$*" "$(($(date +%s) - T0))"; }
die() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
PIDS=""
cleanup() { for p in $PIDS; do kill "$p" 2>/dev/null || true; kill -CONT "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; }
trap cleanup EXIT
trap 'exit 1' INT TERM
wait_for() { n=$1; shift; while ! "$@" >/dev/null 2>&1; do n=$((n - 1)); [ "$n" -gt 0 ] || return 1; sleep 1; done; }

rm -rf "$BL"; mkdir -p "$BL/bin" "$BL/keys" "$BL/signers" "$BL/agents" "$BL/run" "$BL/realms" "$BL/hl"

say build
(cd "$EB" && nice -n 10 go build -o "$BL/bin/" ./cmd/hlethbridge ./cmd/hleth ./cmd/hlwatch)
(cd "$REPO/bridge" && nice -n 10 go build -o "$BL/bin/" ./cmd/hlkey)

say "contracts: ours (the client, the verifier, the gate, HlAdmin) at clock factor $FACTOR, Hyperlane 12.2.0 from the release's own compile"
CT="$BL/contracts"; mkdir -p "$CT"
for d in src lib script foundry.toml remappings.txt; do [ -e "$CONTRACTS/$d" ] && cp -R "$CONTRACTS/$d" "$CT/"; done
if [ -n "$ED25519" ]; then
  cp "$ED25519/Ed25519.sol" "$ED25519/Sha512.sol" "$CT/src/lib/"
  [ ! -f "$ED25519/Ed25519Verifier.sol" ] || cp "$ED25519/Ed25519Verifier.sol" "$CT/src/"
fi
mkdir -p "$CT/src/clock/factorE2E" "$CT/src/e2e"
printf '// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL\npragma solidity 0.8.33;\n\n// The local run'"'"'s clock factor (eth/bridge/local/e2e.sh): 7 days in %s s.\nuint64 constant CLOCK_FACTOR = %s;\n' "$DELAY" "$FACTOR" >"$CT/src/clock/factorE2E/ClockFactor.sol"
python3 - "$CT/foundry.toml" <<'EOF'
import re, sys
p = sys.argv[1]; s = open(p).read()
m = re.search(r'\[profile\.default\](.*?)(?=\n\[)', s, re.S)
block = m.group(1)
block = re.sub(r'"bridge-clock/=src/clock/factor1/"', '"bridge-clock/=src/clock/factorE2E/"', block)
assert 'factorE2E' in block, "no bridge-clock remapping in the default profile"
s += "\n[profile.e2e]" + block
open(p, "w").write(s)
EOF
# the gate's next implementation, built beside the contracts (E4-RL4): the gate's own code, storage layout and clock
# factor, a view added and an initializer of version 2 (E5-CF4) — code new to the deployment, which a kind 8 below
# installs behind the gate's proxy and initializes in the same execute, upgradeAndCall (every other contract's build is
# byte for byte the same with it in the tree)
cat >"$CT/src/e2e/PausableGateE2E.sol" <<'EOF'
// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {PausableGate} from "../PausableGate.sol";

/// The local run's new gate implementation (eth/bridge/local/e2e.sh): the gate as deployed, a view added, so the run
/// reads through the proxy that a kind 8 installed it, and the new version's initializer, which the kind 8's call runs
/// once through the proxy (its implementation's own initializers are closed by the gate's constructor).
contract PausableGateE2E is PausableGate {
    /// The slot the initializer writes: a namespace of its own, none of the gate's.
    bytes32 private constant MARK = keccak256("gno.bridge.e2e.PausableGateE2E.mark");

    function upgradedInThisRun() external pure returns (uint256) {
        return 2;
    }

    /// The kind 8's call (ProxyAdmin.upgradeAndCall through HlAdmin, on the 2,000,000 gas HlAdmin forwards).
    function initializeE2E(uint256 mark) external reinitializer(2) {
        bytes32 slot = MARK;
        assembly ("memory-safe") {
            sstore(slot, mark)
        }
    }

    function upgradeMark() external view returns (uint256 v) {
        bytes32 slot = MARK;
        assembly ("memory-safe") {
            v := sload(slot)
        }
    }
}
EOF
(cd "$CT" && FOUNDRY_PROFILE=e2e nice -n 10 forge build --force --ast --skip test --skip script >"$BL/forge.log" 2>&1) || { tail -30 "$BL/forge.log"; die "forge build"; }
[ -f "$CT/out/PausableGateE2E.sol/PausableGateE2E.json" ] || die "the gate's next implementation was not built"
grep -q "CLOCK_FACTOR = $FACTOR" "$CT/src/clock/factorE2E/ClockFactor.sol" || die "the clock factor was not staged"
sh "$HERE/hyperlane-artifacts.sh" "$HLTGZ" "$SOLC" "$BL/hl" >"$BL/hl.log" 2>&1 || { tail -20 "$BL/hl.log"; die "Hyperlane's artifacts"; }

say "keys"
key() { "$BL/bin/hleth" keygen -o "$BL/keys/$1.key" -seed "eth bridge e2e $1" | sed -n 's/.*\(0x[0-9a-fA-F]\{40\}\).*/\1/p' | head -1; }
for k in relayer updater watch5 watch6 g1 g2 g3 g4 g5 alice treasurer remote; do
  a=$(key "$k"); [ -n "$a" ] || die "keygen $k"; eval "E_$k=$a"
done
echo "$DEPLOYER_KEY" >"$BL/keys/deployer.key"; chmod 600 "$BL/keys/deployer.key"
for i in 0 1 2; do "$BL/bin/hlkey" new "$BL/signers/s$i.json" >>"$BL/signers/public.txt"; done
SIGNERS=$(paste -sd, - <"$BL/signers/public.txt")
GK="$BL/gnokey"; mkdir -p "$GK"
printf '%s\n\n\n' "$MNEMONIC" | gnokey add -recover -insecure-password-stdin -home "$GK" -quiet test1 >/dev/null 2>&1
printf '\n\n' | gnokey add -insecure-password-stdin -home "$GK" -quiet relayer >/dev/null 2>&1
printf '\n\n' | gnokey add -insecure-password-stdin -home "$GK" -quiet watch >/dev/null 2>&1
RELAYER_ADDR=$(gnokey list -home "$GK" 2>/dev/null | sed -n 's/^.* relayer (local) - addr: \(g1[a-z0-9]*\).*/\1/p')
[ -n "$RELAYER_ADDR" ] || die "no relayer gno key"
WATCH_ADDR=$(gnokey list -home "$GK" 2>/dev/null | sed -n 's/^.* watch (local) - addr: \(g1[a-z0-9]*\).*/\1/p')
[ -n "$WATCH_ADDR" ] || die "no watch gno key"

say "anvil (osaka; finalized two blocks behind the head; a block a second)"
nice -n 10 anvil --port "$ANVIL_PORT" --hardfork osaka --chain-id 31337 --block-time 1 --slots-in-an-epoch 1 --enable-tx-gas-limit --silent >"$BL/anvil.log" 2>&1 &
PIDS="$PIDS $!"
wait_for 30 cast block-number --rpc-url "$ETHRPC" || die "anvil did not start"
for k in relayer updater watch5 watch6 g1 g2 g3 g4 g5 alice; do
  eval "a=\$E_$k"
  cast rpc --rpc-url "$ETHRPC" anvil_setBalance "$a" 0x56BC75E2D63100000 >/dev/null # 100 ETH
done

say "gno chain (GovDAO and the packages at genesis; the bridge realms go up after the Ethereum audit)"
ST="$BL/gno"; mkdir -p "$ST"
stage() { mkdir -p "$2"; for f in "$1"/*.gno; do case "$f" in *_test.gno|*_filetest.gno) ;; *) cp "$f" "$2/" ;; esac; done; cp "$1/gnomod.toml" "$2/"; }
for d in "$REPO"/realm/p/*/v0; do p=$(basename "$(dirname "$d")"); stage "$d" "$ST/p/$ADDR/$p/v0"; done
stage "$REPO/realm/bubble" "$BL/realms/bubble"
for r in hlmailbox hlverifier hlfees hlgnot hlbubble hlgov; do stage "$EB/../realm/$r" "$BL/realms/$r"; done
cat >"$BL/run/closure.py" <<'EOF'
import os, re, sys
ex, roots = sys.argv[1], sys.argv[2:]
imp = re.compile(r'"(gno\.land/[pr]/[^"]+)"')
seen, todo = [], list(roots)
while todo:
    p = todo.pop(0)
    if p in seen: continue
    d = os.path.join(ex, p)
    if not os.path.isdir(d): continue
    seen.append(p)
    for f in os.listdir(d):
        if not f.endswith('.gno') or f.endswith('_test.gno') or f.endswith('_filetest.gno'): continue
        src = open(os.path.join(d, f)).read()
        head = src.split('\nfunc ', 1)[0]
        for m in imp.findall(head):
            if m not in seen: todo.append(m)
print('\n'.join(seen))
EOF
# (r/sys/params too: GovDAO sets the vm param run_submitters through it after its init, below)
for p in $(python3 "$BL/run/closure.py" "$EX" gno.land/r/gov/dao gno.land/r/gov/dao/impl/v0 gno.land/r/gov/dao/memberstore/v0 gno.land/r/gov/dao/init/v0 gno.land/p/nt/avl/v0 gno.land/p/nt/ufmt/v0 gno.land/p/nt/bptree/v0 gno.land/p/nt/cford32/v0 gno.land/p/nt/seqid/v0 gno.land/r/sys/params); do
  rel=$(echo "$p" | sed 's|^gno.land/||'); [ -d "$ST/$rel" ] || stage "$EX/$p" "$ST/$rel"
done
swap() { sed -i.bak "s|$2|$3|" "$1" && rm -f "$1.bak"; grep -qF -- "$3" "$1" || die "swap into $1 failed ($2)"; }
swap "$ST/p/$ADDR/decisions/v0/decisions.gno" 'Factor = int64(1)$' "Factor = int64($REALM_FACTOR)"
(cd "$ST" && exec nice -n 10 gnodev local -chain-id dev -no-watch -no-web -empty-blocks -empty-blocks-interval 1 -node-rpc-listener "$GNORPC" -extra-root "$ST" >"$BL/gnodev.log" 2>&1) &
PIDS="$PIDS $!"
wait_for 120 curl -sf "http://$GNORPC/status" || die "gnodev did not start; see $BL/gnodev.log"
! grep -q "type check failed" "$BL/gnodev.log" || die "a package failed to load; see $BL/gnodev.log"
gnotx() { what=$1; shift
  out=$(echo | gnokey "$@" -broadcast -chainid dev -remote "$GNORPC" -insecure-password-stdin -home "$GK" test1 2>&1) || die "$what: $out"
  echo "$out" | grep -q '^OK!' || die "$what: $out"; }
call() { pkg=$1; fn=$2; shift 2; gnotx "$pkg.$fn" maketx call -pkgpath "$NS/$pkg" -func "$fn" $(for a in "$@"; do printf -- '-args %s ' "$a"; done) -gas-fee 1000000ugnot -gas-wanted 100000000; }
callpath() { pkg=$1; fn=$2; shift 2; gnotx "$pkg.$fn" maketx call -pkgpath "$pkg" -func "$fn" $(for a in "$@"; do printf -- '-args %s ' "$a"; done) -gas-fee 1000000ugnot -gas-wanted 100000000; }
callsend() { send=$1; pkg=$2; fn=$3; shift 3; gnotx "$pkg.$fn" maketx call -pkgpath "$NS/$pkg" -func "$fn" -send "$send" $(for a in "$@"; do printf -- '-args %s ' "$a"; done) -gas-fee 1000000ugnot -gas-wanted 100000000; }
run() { out=$(echo | gnokey maketx run -gas-fee 1000000ugnot -gas-wanted 200000000 -broadcast -chainid dev -remote "$GNORPC" -insecure-password-stdin -home "$GK" test1 "$1" 2>&1) || die "$1: $out"
  echo "$out" | grep -q '^OK!' || die "$1: $out"; }
addpkg() { gnotx "addpkg $2" maketx addpkg -pkgpath "$2" -pkgdir "$BL/realms/$1" -gas-fee 1000000ugnot -gas-wanted 400000000; }
qeval() { gnokey query vm/qeval -data "$1" -remote "$GNORPC" 2>/dev/null | sed -n 's/^data: (\([-0-9]*\) .*/\1/p'; }
qrender() { gnokey query vm/qrender -data "$NS/$1" -remote "$GNORPC" 2>/dev/null | sed -n 's/^data: //p'; }
# the relayers' gno key holds the float the plan wants (§5.5; agent/floats.go: 50 credits a day at creditGnoUgnot, the
# storage deposit a credit locks and its gas, ≈1.8 GNOT — seven days' runway is 630 GNOT) and the run's own spending
# with room (≈2 GNOT a run): runway:relayer-gno does not stand in the run (E5-RT5; the integration lane I3)
CREDIT_UGNOT=$(sed -n 's/^const creditGnoUgnot = \([0-9_]*\)$/\1/p' "$EB/agent/floats.go" | tr -d _)
case "$CREDIT_UGNOT" in ''|*[!0-9]*) die "agent/floats.go's creditGnoUgnot did not read: '$CREDIT_UGNOT'" ;; esac
RELAYER_FUND=$((7 * 50 * CREDIT_UGNOT + 70000000))
for fund in "$RELAYER_ADDR:$RELAYER_FUND" "$WATCH_ADDR:$WATCH_FUND"; do
  to=${fund%%:*}
  out=$(echo | gnokey maketx send -to "$to" -send "${fund##*:}ugnot" -gas-fee 1000000ugnot -gas-wanted 2000000 -broadcast -chainid dev -remote "$GNORPC" -insecure-password-stdin -home "$GK" test1 2>&1) || die "funding $to: $out"
done
echo "the relayers' gno key holds $((RELAYER_FUND / 1000000)) GNOT: seven days of the planned 50 credits at $CREDIT_UGNOT ugnot, and the run's own"


say "hleth setup: the Ethereum side from the deployer key (§5.8's eleven steps), then a second run that sends nothing"
# the lock the launch notice expects of each token (a million, base units ×10^6): the gate's launch day cap compiles
# from it — the smaller of 100,000 and a tenth of it, here 100,000 (E3-CF4) — and the audit holds the cap to it
EXPECTED_LOCK=1000000000000
setup() { nice -n 10 "$BL/bin/hleth" setup -rpc "$ETHRPC" -deployer "$BL/keys/deployer.key" -contracts "$CT/out" -hyperlane "$BL/hl" -out "$BL/deployment.json" \
  -local-domain $ETH_DOMAIN -gno-domain $GNO_DOMAIN -gno-rpc "http://$GNORPC" -mailbox-pkgpath "$NS/hlmailbox" -hlgov "$NS/hlgov" \
  -gnot-route "$NS/hlgnot" -bubble-route "$NS/hlbubble" -seats "$E_g1,$E_g2,$E_g3,$E_g4" -watch "$E_watch5,$E_watch6" -beneficiary "$E_treasurer" \
  -expected-lock-gnot $EXPECTED_LOCK -expected-lock-bubble $EXPECTED_LOCK -start-sequence 0 -poll 100ms -config-out "$BL/bridge.eth.json"; }
setup >"$BL/setup.log" 2>&1 || { tail -30 "$BL/setup.log"; die "hleth setup"; }
grep -q 'hleth setup: DONE' "$BL/setup.log" || die "hleth setup did not reach DONE: $(tail -5 "$BL/setup.log")"
setup >"$BL/setup2.log" 2>&1 || { tail -30 "$BL/setup2.log"; die "hleth setup (second run)"; }
grep -q 'hleth setup: DONE' "$BL/setup2.log" || die "the second setup run did not reach DONE"
grep -q 'hleth setup: DONE (sent nothing' "$BL/setup2.log" || die "the second setup run sent transactions: $(grep 'hleth setup: DONE' "$BL/setup2.log")"
grep 'hleth setup: DONE' "$BL/setup.log"
echo "the second run repeated nothing"
dep() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$BL/deployment.json" "$1"; }
MAILBOX=$(dep mailbox); TOKEN0=$(dep token0); TOKEN1=$(dep token1); CLIENT=$(dep client); GATE=$(dep gate); HLADMIN=$(dep hlAdmin)
PROXYADMIN=$(dep proxyAdmin); MTH=$(dep merkleTreeHook); PFEE=$(dep protocolFee); FROM=$(dep deployBlock)
echo "the deployment: mailbox $MAILBOX, GNOT $TOKEN0, BUBBLE $TOKEN1, client $CLIENT, gate $GATE, HlAdmin $HLADMIN, HlAdminExec $(dep hlAdminExec), the verifier $(dep ed25519Verifier) (its first block $(dep firstBlock); finishSetup's, eth.deployBlock, $FROM)"
# eth.deployBlock is finishSetup's block (E2-RT6): every machine's scan starts there; -config-out wrote the machines'
# config's deployment part with it
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); c=json.load(open(sys.argv[2])); f=[s for s in d["steps"] if s["name"]=="finishSetup"][0]; assert d["deployBlock"]==f["block"]>d["firstBlock"]>0, (d["deployBlock"], f, d["firstBlock"]); assert c["eth"]["deployBlock"]==d["deployBlock"] and c["eth"]["admin"]==d["hlAdmin"] and c["routes"][1]["token"]==d["token1"], c' "$BL/deployment.json" "$BL/bridge.eth.json" || die "eth.deployBlock is not finishSetup's block, or the machines' config part does not name it"
echo "eth.deployBlock $FROM is finishSetup's block; hleth wrote the machines' config part ($BL/bridge.eth.json)"
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); f=["proxyAdmin","mailboxImpl","mailbox","merkleTreeHook","protocolFee","ed25519Verifier","clientImpl","client","token0Impl","token0","token1Impl","token1","gateImpl","gate","hlAdmin","hlAdminExec"]; m=[k for k in f if not d.get(k)]; assert not m, m; assert len(set(d[k].lower() for k in f)) == 16' "$BL/deployment.json" || die "the deployment record does not name sixteen distinct contracts"
echo "sixteen deployments recorded (eleven codes, five proxies)"
for c in clientImpl ed25519Verifier hlAdmin hlAdminExec gateImpl; do
  n=$(( $(cast code --rpc-url "$ETHRPC" "$(dep "$c")" | wc -c) / 2 - 1 ))
  [ "$n" -le 24576 ] || die "$c's runtime code is $n bytes, past EIP-170's 24,576"
  printf '%s %s bytes; ' "$c" "$n"
done
echo "(EIP-170 enforced by anvil: every one fits)"

say "hleth audit: §5.8's list before any realm goes up (every masked immutable against its expected value)"
# (the member's own reading of every launch value: the realms' paths, the published seats and watch keys, the
# treasurer's Ethereum address, the expected locks — and gno.land itself, the trust root's only independent source)
# (the audit's launch values are the launch notice's: the seats and watch keys, the beneficiary — E4-TL1, E4-TL6)
member() { "$BL/bin/hleth" audit -rpc "$ETHRPC" -deployment "$BL/deployment.json" -contracts "$CT/out" -hyperlane "$BL/hl" -factor "$FACTOR" \
  -mailbox-pkgpath "$NS/hlmailbox" -hlgov "$NS/hlgov" -gnot-route "$NS/hlgnot" -bubble-route "$NS/hlbubble" \
  -seats "$E_g1,$E_g2,$E_g3,$E_g4" -watch "$E_watch5,$E_watch6" -beneficiary "$E_treasurer" "$@"; }
audit() { member -gno-rpc "http://$GNORPC" -expected-lock-gnot $EXPECTED_LOCK -expected-lock-bubble $EXPECTED_LOCK "$@"; }
# (a holder's audit reads at the finalized block: the setup's last transactions are final two blocks later)
audit_pass() { audit >"$BL/audit0.log" 2>&1 && grep -q 'hleth audit: PASS' "$BL/audit0.log"; }
wait_for 30 audit_pass || { grep -E '^FAIL|^hleth audit' "$BL/audit0.log" | head -20; die "hleth audit before the realms"; }
grep -c '  rpc> ' "$BL/audit0.log" | xargs printf 'the audit printed %s raw RPC calls\n'
grep -E '^hleth audit' "$BL/audit0.log"

say "the realms staged with this run's launch values, then up (addpkg), then the governor's Launch"
for f in hlmailbox/mailbox.gno hlgnot/hlgnot.gno hlbubble/hlbubble.gno hlfees/hlfees.gno hlgov/hlgov.gno; do
  swap "$BL/realms/$f" '^const launchGovernor = address("[a-z0-9]*")' "const launchGovernor = address(\"$TEST1\")"
done
swap "$BL/realms/bubble/mint.gno" '^const premineTo = address("[a-z0-9]*")' "const premineTo = address(\"$TEST1\")" # the premine to test1, who sends
swap "$BL/realms/hlmailbox/mailbox.gno" 'launchSigners     = "[a-f0-9,]*"' "launchSigners     = \"$SIGNERS\""
# hlmailbox's seats: test1 is guardian 1 (hlwatch's stroke below), the monitor's own watch key holds seat 5
swap "$BL/realms/hlmailbox/flags.gno" 'launchSeat1 = address("[a-z0-9]*")' "launchSeat1 = address(\"$TEST1\")"
swap "$BL/realms/hlmailbox/flags.gno" 'launchSeat5 = address("[a-z0-9]*")' "launchSeat5 = address(\"$WATCH_ADDR\")"
lower() { echo "$1" | tr 'A-F' 'a-f'; }
# every contract-address placeholder hlmailbox carries (launchEth<Name>: the record's field <name>, its first letter
# lowered — Mailbox → mailbox, HlAdminExec → hlAdminExec, Ed25519Verifier → ed25519Verifier), whatever the realm lists:
# each swapped for the deployed address, none left, none the record lacks
D="$BL/realms/hlmailbox/ethdeploy.gno"
NAMES=$(sed -n 's/^[[:space:]]*launchEth\([A-Za-z0-9]*\)[[:space:]]*= *"0x[0-9a-fA-F]\{40\}".*/\1/p' "$D")
[ -n "$NAMES" ] || die "no launchEth placeholder in $D"
n=0
for name in $NAMES; do
  field=$(python3 -c 'import sys; s=sys.argv[1]; print(s[:1].lower() + s[1:])' "$name")
  addr=$(dep "$field" 2>/dev/null) || die "hlmailbox carries launchEth$name, and the deployment records no $field"
  [ -n "$addr" ] || die "hlmailbox carries launchEth$name, and the deployment records no $field"
  swap "$D" "launchEth$name *= \"0x[0-9a-fA-F]*\"" "launchEth$name = \"$(lower "$addr")\""
  n=$((n + 1))
done
for name in $NAMES; do
  field=$(python3 -c 'import sys; s=sys.argv[1]; print(s[:1].lower() + s[1:])' "$name")
  grep -q "launchEth$name *= \"$(lower "$(dep "$field")")\"" "$D" || die "launchEth$name was not swapped"
done
echo "hlmailbox's contract list: $n placeholders swapped for the deployment's addresses ($(echo $NAMES | tr ' ' ','))"
[ "$n" -eq 16 ] || echo "NOTE hlmailbox lists $n of the sixteen deployments (fold F1 adds the Ed25519Verifier and HlAdminExec)"
swap "$BL/realms/hlgnot/hlgnot.gno" '^const launchRouter = "0x[0-9a-fA-F]*"' "const launchRouter = \"$(lower "$TOKEN0")\""
swap "$BL/realms/hlbubble/hlbubble.gno" '^const launchRouter = "0x[0-9a-fA-F]*"' "const launchRouter = \"$(lower "$TOKEN1")\""
i=1; for k in g1 g2 g3 g4 watch5 watch6; do
  eval "a=\$E_$k"; swap "$BL/realms/hlgov/hlgov.gno" "launchEthSeat$i = \"0x[0-9a-fA-F]*\"" "launchEthSeat$i = \"$(lower "$a")\""; i=$((i + 1))
done
# the meters' cap floor raised so a claim pays in a second: the meters start empty at Launch and refill at the day's
# cap a day (§5.4), which at the launch floor of 10,000 a day would hold a 400 BUBBLE fee (fold F6's) for an hour
swap "$BL/realms/hlbubble/hlbubble.gno" 'launchCapFloor = 10_000 \* one' "launchCapFloor = 100_000_000 * one"
swap "$BL/realms/hlgnot/hlgnot.gno" 'launchCapFloor = 10_000 \* gnot' "launchCapFloor = 100_000_000 * gnot"
grep -q 'const testEthereumDomain = uint32(31337)' "$BL/realms/hlmailbox/mailbox.gno" || die "the realms' test domain is not 31337"
# the swaps above leave no patterned placeholder of the dev tree behind in any staged realm
! grep -nE '"0x(0[1-9]|12|25|70|80|90)\1{19}"' "$BL/realms/hlmailbox/ethdeploy.gno" || die "a patterned contract placeholder stands in hlmailbox"
addpkg bubble "gno.land/r/$ADDR/bubble"
for r in hlmailbox hlverifier hlfees hlgnot hlbubble hlgov; do addpkg "$r" "$NS/$r"; done
cat >"$BL/run/init_govdao.gno" <<EOF
package main

import govinit "gno.land/r/gov/dao/init/v0"

func main(cur realm) {
	govinit.InitWithUsers(cross(cur), "$TEST1")
}
EOF
run "$BL/run/init_govdao.gno"
# gnoland-1 lets only GovDAO's T1 members send MsgRun (its vm param run_submitters, E3-AS1); gnodev v1.2.0 builds its
# genesis from gnoland.DefaultGenState() with run_submitters empty (contribs/gnodev/pkg/dev/node.go, devGenState: anyone
# may MsgRun) and its -genesis file gives balances and transactions only, so the param is set here, right after
# GovDAO's init and before anything else runs: GovDAO (test1, its one member here, as the governor) votes
# run_submitters = [test1] through r/sys/params — the governor's MsgRuns still go, a stranger's is refused as on chain.
# The stranger's MsgRun (the relayer's key) goes first, before the param is set: the control, so the refusal below is
# the allowlist's and nothing else's (a signature, an account) — and is held to the allowlist's own words (E4-TL5)
printf 'package main\n\nfunc main() {\n\tprintln("a stranger")\n}\n' >"$BL/run/stranger.gno"
stranger_run() { echo | gnokey maketx run -gas-fee 1000000ugnot -gas-wanted 20000000 -broadcast -chainid dev -remote "$GNORPC" -insecure-password-stdin -home "$GK" relayer "$BL/run/stranger.gno" 2>&1; }
out=$(stranger_run) || die "the control: a stranger's MsgRun before run_submitters is set was refused: $out"
echo "$out" | grep -q '^OK!' || die "the control: a stranger's MsgRun before run_submitters is set did not go: $out"
echo "the control: before run_submitters is set, a stranger's MsgRun (the relayer's key) goes"
cat >"$BL/run/run_submitters.gno" <<EOF
package main

import (
	"gno.land/r/gov/dao"
	"gno.land/r/sys/params"
)

func main(cur realm) {
	pid := dao.MustCreateProposal(cross(cur), params.ProposeSetRunSubmitters(cross(cur), []string{"$TEST1"}))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	if !dao.ExecuteProposal(cross(cur), pid) {
		panic("the run_submitters proposal did not execute")
	}
}
EOF
run "$BL/run/run_submitters.gno"
RS=$(gnokey query params/vm:p:run_submitters -remote "$GNORPC" 2>/dev/null | sed -n 's/^data: //p')
[ "$RS" = "[\"$TEST1\"]" ] || die "run_submitters is $RS, not the governor alone"
out=$(stranger_run) && die "a stranger's MsgRun (the relayer's key) was taken: $out"
echo "$out" | grep -qF "$RELAYER_ADDR is not authorized to send MsgRun; see the vm run_submitters param" || die "a stranger's MsgRun failed otherwise than by the run_submitters allowlist: $out"
echo "run_submitters = $RS: a stranger's MsgRun is refused (the relayer re-emits by MsgCalls), the governor's goes"
govern() { printf 'package main\n\nimport "%s/%s"\n\nfunc main(cur realm) {\n\t%s.Govern(cross(cur), %s)\n}\n' "$NS" "$2" "$2" "$3" >"$BL/run/$1.gno"; run "$BL/run/$1.gno"; }
govern launch hlmailbox 'hlmailbox.NewLaunchRequest(cross(cur))'
qrender "hlmailbox:json" | grep -q '"live":true' || die "not live after Launch: $(qrender hlmailbox:json | cut -c1-300)"
echo "live by the governor's Launch; the signers: $SIGNERS"
# the routes' own fees and minimums, as the realms render them (base units)
rj() { qrender "$1:json" | python3 -c 'import json,sys; print(json.load(sys.stdin)[sys.argv[1]])' "$2"; }
GFEE=$(rj hlgnot fee); GMIN=$(rj hlgnot minimum); FEE=$(rj hlbubble fee); BMIN=$(rj hlbubble minimum)
for v in "$GFEE" "$GMIN" "$FEE" "$BMIN"; do case "$v" in ''|*[!0-9]*) die "a route's fee or minimum did not read: GNOT $GFEE/$GMIN, BUBBLE $FEE/$BMIN" ;; esac; done
[ "$BMIN" -gt "$FEE" ] || die "BUBBLE's minimum $BMIN is not above its fee $FEE: a claim of the minimum would pay nothing past the fee"
GSEND=$GMIN               # GNOT gno.land -> Ethereum: the minimum, the fee on top
SEND=$((BMIN * 5 / 2))    # BUBBLE gno.land -> Ethereum: two and a half minimums, the fee on top
BACK=$BMIN                # BUBBLE Ethereum -> gno.land: the minimum (a claim; the fee its first installment)
echo "the routes read: GNOT fee $GFEE, minimum $GMIN; BUBBLE fee $FEE, minimum $BMIN (base units) — sending $GSEND GNOT and $SEND BUBBLE out, $BACK BUBBLE back"

say "the machines: three signers, the updater, the relayer (both halves), the monitor"
cat >"$BL/run/sink.py" <<'EOF'
import http.server, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('content-length', 0)); b = self.rfile.read(n)
        open(sys.argv[2], 'ab').write(b + b'\n'); self.send_response(204); self.end_headers()
    def log_message(self, *a): pass
http.server.HTTPServer(('127.0.0.1', int(sys.argv[1])), H).serve_forever()
EOF
: >"$BL/alarms.log"
python3 "$BL/run/sink.py" 9330 "$BL/alarms.log" &
PIDS="$PIDS $!"
# the sink listens before any machine starts: an alarm raised at a machine's start (prices:stale is) posted to a sink
# not yet up is refused and, standing, not posted again for its cadence (a day), so alarms.log would miss it
sink_up() { curl -s -o /dev/null "http://127.0.0.1:9330/"; }
wait_for 10 sink_up || die "the alarm sink does not listen on 127.0.0.1:9330"
python3 - "$BL" "$(command -v gnokey)" "$RELAYER_ADDR" "$WATCH_ADDR" "$ETHRPC" "http://$GNORPC" "$NS" "$E_relayer" "$E_updater" <<'EOF'
import datetime, json, sys
bl, gnokey, relayer, watch, ethrpc, gnorpc, ns, erelayer, eupdater = sys.argv[1:10]
# the deployment's part of the config is hleth setup's (-config-out: the contracts, the domains, the routes' tokens and
# eth.deployBlock, finishSetup's block); each machine adds its node, its providers and its keys
cfg = json.load(open(bl + '/bridge.eth.json'))
pub = [l.strip() for l in open(bl + '/signers/public.txt')]
cfg["gno"].update({"rpc": gnorpc, "sources": [gnorpc, gnorpc], "chainId": "dev", "fees": ns + "/hlfees", "dao": "gno.land/r/gov/dao",
          "gnokey": gnokey, "home": bl + "/gnokey", "key": "relayer", "address": relayer, "watchKey": "watch", "watchAddress": watch, "gasPrice": 0.001})
cfg["eth"].update({"providers": [{"name": "anvil-a", "url": ethrpc}, {"name": "anvil-b", "url": ethrpc}],
          "relayerKey": bl + "/keys/relayer.key", "updaterKey": bl + "/keys/updater.key",
          "watchKey": bl + "/keys/watch5.key", "relayer": erelayer, "updater": eupdater, "maxBaseFeeGwei": 50,
          # the GNOT/ETH and BUBBLE/ETH rates the updater and the relayer refuse to start without, of the plan's order
          # (§5.5: GNOT $0.0707 at ETH $2,506 is 0.000028 ETH, BUBBLE at the routes' ratio of 20 BUBBLE a GNOT): anvil
          # prices gas at ≈1 gwei (the 1-gwei tip it suggests), above the ≈0.7 gwei that 20 GNOT covers at the plan's
          # rate, so the run's rates are ≈3.5 times it — a send's own fee, 20 GNOT or 400 BUBBLE, is 0.002 ETH, which
          # covers its header and its delivery (≈0.0008 ETH at ≈1 gwei for this one-validator chain) and the run never
          # waits for a send's six hours; at the plan's own rate the same send would wait for its six hours (budget.go)
          "prices": {"GNOT": 0.0001, "BUBBLE": 0.000005},
          # the date they were set (E3-GS11): the run's own, so prices:stale (undated, or older than a week) stays quiet
          "pricesAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d")})
assert cfg["gno"]["mailbox"] == ns + "/hlmailbox" and cfg["gno"]["gov"] == ns + "/hlgov", cfg["gno"]
# the lock the launch notice expects (a million of each): the cap-ratio rule stays quiet under it, as it is meant to
for r in cfg["routes"]:
    r["expectedLock"] = 10**12
cfg.update({"signers": [{"url": "http://127.0.0.1:%d" % (9310 + i), "key": k} for i, k in enumerate(pub)],
  "allowHTTP": True,
  "webhook": "http://127.0.0.1:9330/alarm"})
json.dump(cfg, open(bl + '/bridge.json', 'w'), indent=2)
EOF
for i in 0 1 2; do
  nice -n 10 "$BL/bin/hlethbridge" signer -every 1s -config "$BL/bridge.json" -key "$BL/signers/s$i.json" -store "$BL/agents/s$i" \
    -listen "127.0.0.1:931$i" -status-listen "127.0.0.1:934$i" >"$BL/agents/s$i.log" 2>&1 &
  PIDS="$PIDS $!"
done
nice -n 10 "$BL/bin/hlethbridge" updater -every 1s -min-gap 3s -batch-wait 5s -config "$BL/bridge.json" -state "$BL/agents/updater.json" -sender-state "$BL/agents/updater-tx.json" -listen 127.0.0.1:9303 >"$BL/agents/updater.log" 2>&1 &
UPDATER_PID=$!
PIDS="$PIDS $!"
nice -n 10 "$BL/bin/hlethbridge" relayer -every 1s -role primary -config "$BL/bridge.json" -state "$BL/agents/relayer.json" -sender-state "$BL/agents/relayer-tx.json" -listen 127.0.0.1:9301 -gno-gas 60000000 >"$BL/agents/relayer.log" 2>&1 &
RELAYER_PID=$!
PIDS="$PIDS $!"
nice -n 10 "$BL/bin/hlethbridge" monitor -every 2s -config "$BL/bridge.json" -state "$BL/agents/monitor.json" -listen 127.0.0.1:9302 >"$BL/agents/monitor.log" 2>&1 &
PIDS="$PIDS $!"
status() { curl -sf "http://127.0.0.1:$1/v1/status"; }
hlwatch() { "$BL/bin/hlwatch" status -config "$BL/bridge.json" -node "http://$GNORPC" -state "$BL/agents/hlwatch.json"; }
for i in 0 1 2; do signing() { status "934$i" | grep -q '"signing":true'; }; wait_for 90 signing || die "signer $i is not signing: $(status "934$i"); see $BL/agents/s$i.log"; done
# every machine's fresh scans start at the config's eth.deployBlock (no -from-block given)
for i in 0 1 2; do status "934$i" | python3 -c 'import json,sys; j=json.load(sys.stdin); assert j["from"] == int(sys.argv[1]), (j["from"], sys.argv[1])' "$FROM" || die "signer $i scans from $(status "934$i" | python3 -c 'import json,sys; print(json.load(sys.stdin)["from"])'), not the config's deployBlock $FROM"; done
echo "signer 0: $(status 9340 | python3 -c 'import json,sys; j=json.load(sys.stdin); print("signing —", j["cut"])')"
# the launch line (fold F5's launchNonce, fold F7's scans): no message below it is a burn
LINE=$(cast call --rpc-url "$ETHRPC" "$(dep hlAdmin)" 'launchNonce()(uint32)' | cut -d' ' -f1)
echo "HlAdmin's launch line (launchNonce): $LINE — the signers, the monitors, the inbound relayer and hlwatch count no message below it"
curl -sf "http://127.0.0.1:9310/v1/status" >/dev/null 2>&1 && die "a signer serves its status beside its public endpoint"
"$BL/bin/hlethbridge" providers "$BL/bridge.json" >/dev/null 2>&1 && die "the provider rule took one provider twice"
echo "the provider rule refuses one provider twice; the public endpoint serves no status"

say "gno.land -> Ethereum: $GSEND GNOT (+$GFEE fee) and $SEND BUBBLE (+$FEE fee) in base units, minted on anvil through the gate and the light client"
callsend "$((GSEND + GFEE))ugnot" hlgnot TransferRemote $ETH_DOMAIN "$(lower "$E_alice")" $GSEND
ROUTE_ADDR=$("$BL/bin/hlkey" pkg "$NS/hlbubble" | cut -d' ' -f1)
callpath "gno.land/r/$ADDR/bubble" Approve "$ROUTE_ADDR" $((SEND + FEE))
call hlbubble TransferRemote $ETH_DOMAIN "$(lower "$E_alice")" $SEND
B0=$(qeval "gno.land/r/$ADDR/bubble.BalanceOf(\"$TEST1\")")
case "$B0" in ''|*[!0-9]*) die "test1's BUBBLE balance on gno.land did not read: '$B0'" ;; esac
bal() { "$BL/bin/hleth" balance -rpc "$ETHRPC" -token "$1" -owner "$E_alice" | tail -1; }
is() { [ "$("$@")" = "$WANT" ]; }
WANT=$GSEND; wait_for 240 is bal "$TOKEN0" || die "alice has $(bal "$TOKEN0") GNOT on Ethereum, want $WANT; see $BL/agents/relayer.log and updater.log"
WANT=$SEND; wait_for 240 is bal "$TOKEN1" || die "alice has $(bal "$TOKEN1") BUBBLE on Ethereum, want $WANT; see $BL/agents/relayer.log"
echo "alice holds $(bal "$TOKEN0") GNOT and $(bal "$TOKEN1") BUBBLE on Ethereum"
# the fee-funded budget (§5.5; fold F7): each header the sends needed was bought by their own fees, at the rates above
grep -q "booked (bought by its sends' fees)" "$BL/agents/updater.log" || die "no header was bought by its sends' own fees; see $BL/agents/updater.log"
echo "the updater: $(grep -c "booked (bought by its sends' fees)" "$BL/agents/updater.log") header(s) bought by their sends' fees — $(grep "booked (bought by its sends' fees)" "$BL/agents/updater.log" | tail -1 | sed 's/.*; the budget/the budget/')"
# every booked header (E3-GS10): bought by its sends' fees — never a six-hour draw on the budget (the run's prices make
# every send's own fee cover its header); a decision's header books nothing (fold F15a), so no other booking is
# right; checked again at the end
booked_ok() { ! grep 'updater: header [0-9]* booked (' "$BL/agents/updater.log" | grep -v "booked (bought by its sends' fees)" | grep -q .; }
booked_ok || die "a header booked for another reason: $(grep 'booked (' "$BL/agents/updater.log" | grep -v "bought by its sends' fees" | head -3)"

say "Ethereum -> gno.land: $BACK BUBBLE burned, signed, credited by Process, paid by Claim with the fee first"
out=$("$BL/bin/hleth" send -rpc "$ETHRPC" -key "$BL/keys/alice.key" -token "$TOKEN1" -to "$TEST1" -amount $BACK -gno-domain $GNO_DOMAIN 2>&1) || die "hleth send: $out"
echo "$out" | tail -2
claims() { qrender "hlbubble:json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["claims"])'; }
WANT=1; wait_for 240 is claims || die "$(claims) claims credited on gno, want 1; see $BL/agents/s0.log and relayer.log"
gbal() { qeval "gno.land/r/$ADDR/bubble.BalanceOf(\"$TEST1\")"; }
WANT=$((B0 + BACK - FEE)); wait_for 180 is gbal || die "test1 holds $(gbal) BUBBLE, want $WANT (the claim paid with the fee first); see $BL/agents/relayer.log"
echo "test1 holds $(gbal) BUBBLE on gno.land: the $BACK credited and paid, the $FEE fee first"
status 9301 | python3 -c 'import json,sys; j=json.load(sys.stdin); l=j["last"]; c=[v for k,v in l.items() if k=="claim:BUBBLE"]; assert c and "paid" in c[0], l; print("the relayer:", c[0][:120])' || die "the relayer's status does not show the claim paid: $(status 9301)"

say "a stranger's dispatch toward gno.land, naming the BUBBLE route as recipient with a token message, is never a burn (ER5)"
# Mailbox.dispatch is open to anyone: alice dispatches a message that looks like a burn of 1,000 BUBBLE to test1, with
# no token transfer beside it. No signer signs it, the relayer never delivers it, no claim is credited.
cat >"$BL/run/g1to32.py" <<'EOF2'
import sys
CH = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
s = sys.argv[1]; data = [CH.index(c) for c in s[s.rindex("1") + 1:]][:-6]
acc = bits = 0; out = []
for v in data:
    acc = (acc << 5) | v; bits += 5
    while bits >= 8:
        bits -= 8; out.append((acc >> bits) & 0xff)
assert len(out) == 20, len(out)
print("0x" + "00" * 12 + bytes(out).hex())
EOF2
NONCE=$(cast call --rpc-url "$ETHRPC" "$MAILBOX" 'nonce()(uint32)')
RCPT=$(python3 "$BL/run/g1to32.py" "$ROUTE_ADDR"); TO32=$(python3 "$BL/run/g1to32.py" "$TEST1")
BODY="${TO32}$(printf '%064x' $((1000 * ONE)))"
cast send --rpc-url "$ETHRPC" --private-key "$(cat "$BL/keys/alice.key")" "$MAILBOX" 'dispatch(uint32,bytes32,bytes)' $GNO_DOMAIN "$RCPT" "$BODY" >/dev/null || die "the stranger's dispatch"
sleep 8
for i in 0 1 2; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:931$i/v1/eth/$NONCE")
  [ "$code" = 404 ] || die "signer $i serves a signature for the stranger's dispatch (nonce $NONCE): $code"
done
[ "$(claims)" = 1 ] || die "a claim was credited for the stranger's dispatch: $(claims) claims"
echo "the stranger's dispatch (nonce $NONCE) is signed by nobody and credited nowhere"

say "a burn below BUBBLE's fee is dust the relayer leaves to whoever pays for it (E5-GS5): signed by every signer, never Processed, alarmed once; the monitor counts it in flight, waiting for nobody"
# p/hlroute would sweep it with no fee while its Process locks the relayer's gno key's storage deposit: the relayer keeps
# it pending, judged against the fee each tick, and the monitor counts it apart (the integration lane I3, F17a's R1)
DUST=$((FEE / 2))
DN=$(cast call --rpc-url "$ETHRPC" "$MAILBOX" 'nonce()(uint32)')
D0=$(rj hlmailbox delivered)
out=$("$BL/bin/hleth" send -rpc "$ETHRPC" -key "$BL/keys/alice.key" -token "$TOKEN1" -to "$TEST1" -amount $DUST -gno-domain $GNO_DOMAIN 2>&1) || die "hleth send (the dust burn): $out"
for i in 0 1 2; do
  dsigned() { [ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:931$i/v1/eth/$DN")" = 200 ]; }
  wait_for 120 dsigned || die "signer $i did not sign the dust burn (nonce $DN): the signers sign every burn of something, anyone may bring it"
done
left() { status 9301 | python3 -c 'import json,sys; t=json.load(sys.stdin)["last"].get("inbound",""); assert "(1 of them below their route" in t, t'; }
wait_for 60 left || die "the relayer did not leave the dust burn: $(status 9301 | python3 -c 'import json,sys; print(json.load(sys.stdin)["last"].get("inbound"))')"
dusted() { grep '"key":"inbound:dust:BUBBLE"' "$BL/alarms.log" | grep -q '"level":"alarm"'; }
wait_for 30 dusted || die "the relayer's inbound:dust:BUBBLE alarm was not posted"
mondust() { status 9302 | python3 -c '
import json,sys; j=json.load(sys.stdin); d=int(sys.argv[1])
assert (j.get("dust") or {}).get("BUBBLE") == 1 and "ethereum→gno BUBBLE" not in (j.get("waiting") or {}), (j.get("dust"), j.get("waiting"))
assert j["identity"]["BUBBLE"]["inflightIn"] >= d and j["identity"]["BUBBLE"]["ok"], j["identity"]["BUBBLE"]' "$DUST"; }
wait_for 60 mondust || die "the monitor does not count the dust burn in flight and apart: $(status 9302 | python3 -c 'import json,sys; j=json.load(sys.stdin); print(j.get("dust"), j.get("waiting"), j["identity"]["BUBBLE"]["text"])')"
sleep 5 # a few more relayer ticks: still left
[ "$(rj hlmailbox delivered)" = "$D0" ] || die "the dust burn was Processed: hlmailbox delivered $(rj hlmailbox delivered), was $D0"
n=$(grep -c '"key":"inbound:dust:BUBBLE"' "$BL/alarms.log")
[ "$n" = 1 ] || die "the dust alarm was posted $n times, want once"
echo "the dust burn of $DUST BUBBLE (nonce $DN, under the fee $FEE): signed by all three, left by the relayer (alarmed once, never Processed: hlmailbox delivered $D0), in flight and counted apart on the monitor"

say "the identity holds on the monitor and the signers"
identity_ok() { status 9302 | python3 -c '
import json,sys; j=json.load(sys.stdin)
for s in ("GNOT","BUBBLE"):
    i=j["identity"][s]; assert i["ok"] and i["lock"] == i["supply"] + i["owed"] + i["voided"] - i["lost"] + i["inflightOut"] + i["inflightIn"], i
r=j["record"]; assert r["mismatch"] is None and not r["gap"] and r["count"] >= 2, r
print("monitor:", j["identity"]["GNOT"]["text"]); print("monitor:", j["identity"]["BUBBLE"]["text"]); print("monitor: record", r["count"], "headers,", r["checked"], "checked")'; }
wait_for 60 identity_ok || die "the monitor's identity lines are not right: $(status 9302)"
identity_ok
# (a signer signs on no verdict older than two of its rounds — two seconds at -every 1s — so between rounds on a loaded
# machine its "signing" reads false for a moment: each signer is waited for)
for i in 0 1 2; do
  held() { status "934$i" | python3 -c 'import json,sys; j=json.load(sys.stdin); assert j["signing"], j; assert all(v["verdict"]=="holds" for v in j["identity"].values()), j["identity"]'; }
  wait_for 30 held || die "signer $i: $(status "934$i")"
done
! grep -q '"level":"flag"' "$BL/alarms.log" || die "a flag was raised: $(grep '"level":"flag"' "$BL/alarms.log")"
echo "no flag raised; the three signers sign on held identities"

say "five decisions through r/hlgov, delivered to HlAdmin and executed after their $DELAY s: the protocol fee (kind 7), the gate upgraded to an implementation built in this run with its initializer's call (kind 8), token 0's ISM (kind 3), guardian seat 4 (kind 5), a second router of BUBBLE's, for domain $OTHER_DOMAIN (kind 6)"
# the gate's next implementation (built with the contracts): deployed by a stranger's key, its code hash read back from
# the chain; the gate's proxy does not answer its new view before the upgrade, and the implementation's own
# initializer is closed (E4-RL4, E5-CF4)
NEWGATE=$(cast send --rpc-url "$ETHRPC" --private-key "$(cat "$BL/keys/alice.key")" --create "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["bytecode"]["object"])' "$CT/out/PausableGateE2E.sol/PausableGateE2E.json")" --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["contractAddress"])') || die "the gate's next implementation was not deployed"
case "$NEWGATE" in 0x????????????????????????????????????????) ;; *) die "the gate's next implementation's address: '$NEWGATE'" ;; esac
NEWGATE=$(cast to-check-sum-address "$NEWGATE") || die "the checksum of $NEWGATE" # as the audit and hlwatch print it
NEWCODEHASH=$(cast keccak "$(cast code --rpc-url "$ETHRPC" "$NEWGATE")") || die "the code hash of $NEWGATE"
[ "$(cast call --rpc-url "$ETHRPC" "$NEWGATE" 'upgradedInThisRun()(uint256)')" = 2 ] || die "the gate's next implementation does not answer its view"
! cast call --rpc-url "$ETHRPC" "$GATE" 'upgradedInThisRun()(uint256)' >/dev/null 2>&1 || die "the gate's proxy answers the new view before any upgrade"
! cast call --rpc-url "$ETHRPC" "$NEWGATE" 'initializeE2E(uint256)' 1 >/dev/null 2>&1 || die "the gate's next implementation takes its own initializer"
# the kind 8's call (E5-CF4): the new version's initializer, run once through the proxy by ProxyAdmin.upgradeAndCall
CALL=$(cast calldata 'initializeE2E(uint256)' $MARK) || die "the kind 8's call"
echo "the gate's next implementation: $NEWGATE (code hash $NEWCODEHASH), deployed by a stranger; the gate's proxy does not answer its new view, the implementation refuses its own initializer; the call: $CALL"
# a second router of BUBBLE's (kind 6, E5-CF4): for a domain the token has none for yet, so its read-back moves from
# zero — an execution, not a re-enrolment that writes what stands
[ "$(cast call --rpc-url "$ETHRPC" "$TOKEN1" 'routers(uint32)(bytes32)' $OTHER_DOMAIN)" = 0x0000000000000000000000000000000000000000000000000000000000000000 ] || die "token 1 has a router for domain $OTHER_DOMAIN before decision 5"
govern fee hlgov "hlgov.NewSetProtocolFeeRequest(cross(cur), 1000, \"$(lower "$E_treasurer")\")"
govern upgrade hlgov "hlgov.NewUpgradeRequest(cross(cur), \"$(lower "$GATE")\", \"$(lower "$NEWGATE")\", \"$NEWCODEHASH\", \"$CALL\")"
govern ism hlgov 'hlgov.NewSetTokenISMRequest(cross(cur), 0, "0x0000000000000000000000000000000000000000")'
govern seat4 hlgov "hlgov.NewSetGuardiansRequest(cross(cur), \"4:$(lower "$E_g5")\")"
govern router hlgov "hlgov.NewEnrollRouterRequest(cross(cur), 1, $OTHER_DOMAIN, \"$(lower "$E_remote")\")"
govlast() { qrender "hlgov:json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["last"])'; }
[ "$(govlast)" = 5 ] || die "r/hlgov did not dispatch decisions 1–5: $(qrender hlgov:json | cut -c1-300)"
recorded() { status 9302 | python3 -c 'import json,sys; j=json.load(sys.stdin); d=j["decisions"]; assert d and d["adminLast"] >= '"$1"' and d["govLast"] >= '"$1"', d'; }
wait_for 240 recorded 5 || die "HlAdmin did not record decisions 1–5: $(status 9302 | python3 -c 'import json,sys; print(json.load(sys.stdin)["decisions"])'); see $BL/agents/relayer.log"
TREC=$(date +%s)
W=$(hlwatch)
echo "$W" | grep -q '  - #1 kind 7 (set protocol fee 1000 wei' || die "hlwatch does not show decision 1 pending on HlAdmin: $W"
echo "$W" | grep -q '  - #2 kind 8 (' || die "hlwatch does not show the upgrade, decision 2, pending on HlAdmin: $W"
echo "$W" | grep -E '  - #[1-5] kind [0-9]+ \(' | cut -c1-200
# the members' commands (the Solana hlrecover's): the pending decisions read, and an execute before its maturity refused
DS=$("$BL/bin/hleth" decision status -rpc "$ETHRPC" -deployment "$BL/deployment.json" 2>&1)
echo "$DS" | grep -q '  - #1 kind 7:' || die "hleth decision status does not show decision 1: $DS"
echo "$DS" | grep -q '  - #2 kind 8: upgrade' || die "hleth decision status does not show the upgrade, decision 2: $DS"
echo "$DS" | grep -E '  - #[12] kind [78]:' | cut -c1-220
rc=0; out=$("$BL/bin/hleth" decision execute -rpc "$ETHRPC" -deployment "$BL/deployment.json" -key "$BL/keys/alice.key" -number 1 2>&1) || rc=$?
[ "$rc" -ne 0 ] || die "a member's execute before the maturity was taken: $out"
echo "hleth decision execute before the maturity: refused — $(echo "$out" | tail -1 | cut -c1-200)"
fee() { cast call --rpc-url "$ETHRPC" "$PFEE" 'protocolFee()(uint256)' | cut -d' ' -f1; }
[ "$(fee)" = 0 ] || die "the protocol fee moved before the decision matured: $(fee)"
WANT=1000; wait_for $((DELAY + 120)) is fee || die "the protocol fee is $(fee) after $((DELAY + 120)) s, want 1000 (the relayer's execute); see $BL/agents/relayer.log"
echo "HlAdmin executed decision 1 $(($(date +%s) - TREC)) s after it was recorded (the delay $DELAY s): the protocol fee is $(fee) wei"
nlogs() { cast logs --json --rpc-url "$ETHRPC" --from-block "$FROM" --address "$HLADMIN" "$(cast keccak "$1")" "$(printf '0x%064x' "$2")" 2>/dev/null | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))'; }
executed() { [ "$(nlogs 'DecisionExecuted(uint64,uint8)' "$1")" = 1 ]; }
for n in 2 3 4 5; do
  wait_for 120 executed "$n" || die "HlAdmin did not execute decision $n: $(status 9301 | python3 -c 'import json,sys; l=json.load(sys.stdin)["last"]; print({k: v for k, v in l.items() if "exec" in k or "admin" in k})'); see $BL/agents/relayer.log"
done
# the kind 8 end to end (E4-RL4, E5-CF4): HlAdmin's execute ran ProxyAdmin.upgradeAndCall — the call on the 2,000,000
# gas HlAdmin forwards — and its after-checks (the implementation the decision's, the admin the ProxyAdmin, the owner
# HlAdmin, every binding finishSetup held — any off reverts the execute, the decision pending), so DecisionExecuted(2) is
# all of them holding; the gate's proxy now runs the new code, initialized once by the call (its mark read back, its
# initializer closed after it), and its two EIP-1967 slots are the decision's implementation and the ProxyAdmin
[ "$(cast call --rpc-url "$ETHRPC" "$GATE" 'upgradedInThisRun()(uint256)')" = 2 ] || die "the gate's proxy does not run the implementation decision 2 installed"
[ "$(cast call --rpc-url "$ETHRPC" "$GATE" 'upgradeMark()(uint256)')" = "$MARK" ] || die "the kind 8's call did not run: the gate's mark is $(cast call --rpc-url "$ETHRPC" "$GATE" 'upgradeMark()(uint256)'), not $MARK"
out=$(cast call --rpc-url "$ETHRPC" "$GATE" 'initializeE2E(uint256)' 1 2>&1) && die "the gate's new initializer runs again after the kind 8's call: $out"
echo "$out" | grep -q "already initialized" || die "the gate's new initializer refused otherwise than as run: $out"
# CallStarved's path on the real wiring (E3-CT4, E5-CF4): the same execute, simulated at the block before the relayer's
# with too little gas for the call's floor, is refused CallStarved(); with enough, taken
XLOG=$(cast logs --json --rpc-url "$ETHRPC" --from-block "$FROM" --address "$HLADMIN" "$(cast keccak 'DecisionExecuted(uint64,uint8)')" "$(printf '0x%064x' 2)" | python3 -c 'import json,sys; l=json.load(sys.stdin); assert len(l) == 1, l; b=l[0]["blockNumber"]; print(int(b, 16) if isinstance(b, str) else int(b))') || die "decision 2's DecisionExecuted log"
STARVED=$(cast sig 'CallStarved()')
out=$(cast call --rpc-url "$ETHRPC" --block "$((XLOG - 1))" --gas-limit 1500000 "$HLADMIN" 'execute(uint64)' 2 2>&1) && die "decision 2's execute with 1,500,000 gas, at block $((XLOG - 1)), was taken: $out"
echo "$out" | grep -qiE "${STARVED#0x}|CallStarved" || die "decision 2's execute with 1,500,000 gas failed otherwise than CallStarved() ($STARVED): $out"
out=$(cast call --rpc-url "$ETHRPC" --block "$((XLOG - 1))" --gas-limit 5000000 "$HLADMIN" 'execute(uint64)' 2 2>&1) || die "decision 2's execute with 5,000,000 gas, at block $((XLOG - 1)), was refused: $out"
echo "decision 2 (kind 8) ran its call: the gate's mark is $MARK, its initializer closed after it; at block $((XLOG - 1)), before the relayer's execute, HlAdmin refuses the execute left 1,500,000 gas CallStarved() ($STARVED) and takes it with 5,000,000"
slot() { cast storage --rpc-url "$ETHRPC" "$GATE" "$1" | tr 'A-F' 'a-f'; }
word() { printf '0x%024d%s' 0 "$(lower "${1#0x}")"; }
IMPL_SLOT=0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc
ADMIN_SLOT=0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103
[ "$(slot $IMPL_SLOT)" = "$(word "$NEWGATE")" ] || die "the gate's implementation slot holds $(slot $IMPL_SLOT), not $NEWGATE"
[ "$(slot $ADMIN_SLOT)" = "$(word "$PROXYADMIN")" ] || die "the gate's admin slot holds $(slot $ADMIN_SLOT), not the ProxyAdmin $PROXYADMIN"
echo "decision 2 (kind 8) executed: the gate's proxy runs $NEWGATE (its new view answers 2), its EIP-1967 slots the decision's implementation and the ProxyAdmin"
# the slot watch (§6.5, E3-RT8): the monitor read decision 2 as an executed kind 8 of the gate's and holds the gate's
# two slots to it every poll from then on (its state lists the upgrade with the parameters it holds them to; a slot off
# them is proxy:slots:<gate>, checked never raised at the end, and hlwatch's green needs it read and held)
slotwatch() { python3 - "$BL/agents/monitor.json" "$GATE" "$NEWGATE" <<'EOF'
import json, sys
st, gate, impl = json.load(open(sys.argv[1])), sys.argv[2].lower(), sys.argv[3].lower()
ups = [u for u in (st["decisions"].get("upgrades") or []) if u["number"] == 2]
assert ups and ups[0].get("params"), st["decisions"].get("upgrades")
p = ups[0]["params"].lower()
assert p[24:64] == gate[2:] and p[88:128] == impl[2:], p
print("the monitor's slot watch holds the gate to decision #2 (kind 8, executed in block %d): its implementation slot to %s, its admin slot to the ProxyAdmin" % (ups[0]["block"], sys.argv[3]))
EOF
}
wait_for 60 slotwatch || die "the monitor's slot watch does not hold the gate to decision 2: $(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["decisions"].get("upgrades"))' "$BL/agents/monitor.json")"
slotwatch
# the other three, read back on Ethereum
[ "$(cast call --rpc-url "$ETHRPC" "$TOKEN0" 'interchainSecurityModule()(address)')" = 0x0000000000000000000000000000000000000000 ] || die "token 0's ISM is not zero after decision 3"
[ "$(lower "$(cast call --rpc-url "$ETHRPC" "$MAILBOX" 'recipientIsm(address)(address)' "$TOKEN0")")" = "$(lower "$GATE")" ] || die "token 0's ISM, the mailbox's default, is not the gate"
cast call --rpc-url "$ETHRPC" "$GATE" 'keys()(address[6])' | tr 'A-F' 'a-f' | grep -q "$(lower "$E_g5")" || die "the gate's seat 4 is not $E_g5 after decision 4: $(cast call --rpc-url "$ETHRPC" "$GATE" 'keys()(address[6])')"
[ "$(cast call --rpc-url "$ETHRPC" "$TOKEN1" 'routers(uint32)(bytes32)' $OTHER_DOMAIN)" = "$(word "$E_remote")" ] || die "token 1's router for domain $OTHER_DOMAIN is $(cast call --rpc-url "$ETHRPC" "$TOKEN1" 'routers(uint32)(bytes32)' $OTHER_DOMAIN) after decision 5, not $E_remote"
doms() { cast call --rpc-url "$ETHRPC" "$TOKEN1" 'domains()(uint32[])' | sed 's/ \[[0-9.e+]*\]//g'; } # cast's [1.198e9] notes dropped
[ "$(doms)" = "[$GNO_DOMAIN, $OTHER_DOMAIN]" ] || die "token 1's domains after decision 5: $(doms)"
[ "$(cast call --rpc-url "$ETHRPC" "$TOKEN1" 'routers(uint32)(bytes32)' $GNO_DOMAIN)" = "$RCPT" ] || die "token 1's router for gno.land is not its route after decision 5"
echo "decisions 3–5 executed: token 0's ISM the mailbox's default (the gate), the gate's seat 4 $E_g5, token 1's router for domain $OTHER_DOMAIN $E_remote (none before), its router for gno.land still its route"
# the slot watch at work (§6.5, E3-RT8, E5-CF4): the gate's implementation slot moved by hand to the launch's
# implementation (anvil writes it, as code that rewrote its own slot would): the monitor reads the slot itself and alarms
# proxy:slots:<gate>, hlwatch is not green over it; put back, the alarm clears
A0=$(wc -l <"$BL/alarms.log")
GATEIMPL=$(dep gateImpl)
cast rpc --rpc-url "$ETHRPC" anvil_setStorageAt "$GATE" $IMPL_SLOT "$(word "$GATEIMPL")" >/dev/null || die "anvil_setStorageAt on the gate's implementation slot"
[ "$(slot $IMPL_SLOT)" = "$(word "$GATEIMPL")" ] || die "the gate's implementation slot did not move: $(slot $IMPL_SLOT)"
slotalarm() { tail -n +$((A0 + 1)) "$BL/alarms.log" | grep "\"key\":\"proxy:slots:$GATE\"" | grep "\"level\":\"$1\"" | grep -q '"source":"monitor"'; }
wait_for 60 slotalarm alarm || die "the monitor did not alarm the gate's moved implementation slot: $(tail -n +$((A0 + 1)) "$BL/alarms.log" | head -5)"
tail -n +$((A0 + 1)) "$BL/alarms.log" | grep "\"key\":\"proxy:slots:$GATE\"" | head -1 | python3 -c 'import json,sys; j=json.loads(sys.stdin.read()); print("the monitor:", j["key"], "—", j["text"][:230])'
notgreen() { hlwatch | grep -q "^hlwatch: NOT GREEN.*alarm proxy:slots:$GATE"; }
wait_for 60 notgreen || die "hlwatch is not held from green by the moved slot: $(hlwatch | tail -3)"
echo "hlwatch: NOT GREEN — alarm proxy:slots:$GATE (its own reading of the slot)"
cast rpc --rpc-url "$ETHRPC" anvil_setStorageAt "$GATE" $IMPL_SLOT "$(word "$NEWGATE")" >/dev/null || die "anvil_setStorageAt putting the gate's implementation slot back"
wait_for 60 slotalarm clear || die "the monitor did not clear proxy:slots:$GATE once the slot was put back"
A1=$(wc -l <"$BL/alarms.log"); M1=$(wc -l <"$BL/agents/monitor.log")
echo "the slot put back: the monitor cleared proxy:slots:$GATE"

say "a cancel through r/hlgov: a kind 12 recorded, then cancelled — executed at recording"
govern limits hlgov "hlgov.NewSetMintLimitsRequest(cross(cur), 1, 30000000000, 120000000000)"
wait_for 240 recorded 6 || die "HlAdmin did not record decision 6 (kind 12)"
W=$(hlwatch); echo "$W" | grep -q '  - #6 kind 12 (' || die "the kind 12 is not pending on HlAdmin: $W"
govern cancel hlgov 'hlgov.NewCancelRequest(cross(cur), "6")'
wait_for 240 recorded 7 || die "HlAdmin did not record the cancel (decision 7)"
gone() { W=$(hlwatch); ! echo "$W" | grep -q '  - #6 kind 12 ('; }
wait_for 60 gone || die "decision 6 still pending after the cancel: $(hlwatch)"
# cancelled, not executed (E3-GS10): HlAdmin logged DecisionCancelled(6) and no DecisionExecuted(6) — the cancel itself,
# decision 7 (kind 1), executed at its recording — and the gate's limits for token 1 are still the launch's
[ "$(nlogs 'DecisionCancelled(uint64)' 6)" = 1 ] || die "HlAdmin logged no DecisionCancelled for decision 6 ($(nlogs 'DecisionCancelled(uint64)' 6))"
[ "$(nlogs 'DecisionExecuted(uint64,uint8)' 6)" = 0 ] || die "decision 6 (kind 12) was executed, not cancelled"
[ "$(nlogs 'DecisionExecuted(uint64,uint8)' 7)" = 1 ] || die "the cancel, decision 7, did not execute at its recording"
LIM1=$(cast call --rpc-url "$ETHRPC" "$GATE" 'limits(uint8)(uint64,uint64,uint64,uint64)' 1 | head -2 | cut -d' ' -f1 | paste -sd, -)
[ "$LIM1" = "25000000000,100000000000" ] || die "the gate's limits for token 1 are $LIM1 after the cancel, not the launch's 25000000000,100000000000"
echo "decision 6 (kind 12) cancelled by decision 7 at its recording: DecisionCancelled(6), no DecisionExecuted(6); token 1's limits still $LIM1"

say "a flag: hlwatch's stroke (hlmailbox, the gate, HlAdmin — each Ethereum transaction confirmed final) pauses deliveries, a BUBBLE send and a GNOT send in flight wait it out; the monitor mirrors it"
# two sends in flight when the pause lands (E3-GS10), one of each token (E5-CF4: token 1's ISM is the gate, token 0's
# the mailbox's default since decision 3 — the gate too): dispatched while the relayer is held (SIGSTOP), and proven by
# the updater's header before the gate is flagged — so they can only wait out the pause, nothing minted, and be
# delivered from that header after the unpauses. (One dispatched under the pause waits unproven, the unpauses' headers
# rise past its block, and only a re-emit serves it: the relayer's batch re-emit, one transaction of Reemit MsgCalls
# since fold-r3 F13a — its old MsgRun never type-checked on gno v1.2.0, F13b's notes — is exercised in its own step
# below.)
BB=$(bal "$TOKEN1"); GB0=$(bal "$TOKEN0")
N0=$(rj hlmailbox count)
NG=$((N0 + 1))
kill -STOP "$RELAYER_PID"
callpath "gno.land/r/$ADDR/bubble" Approve "$ROUTE_ADDR" $((SEND + FEE))
call hlbubble TransferRemote $ETH_DOMAIN "$(lower "$E_alice")" $SEND
callsend "$((GSEND + GFEE))ugnot" hlgnot TransferRemote $ETH_DOMAIN "$(lower "$E_alice")" $GSEND
[ "$(rj hlmailbox count)" = $((N0 + 2)) ] || { kill -CONT "$RELAYER_PID"; die "the two sends are not messages $N0 and $NG: the count is $(rj hlmailbox count)"; }
SH=$(qrender "hlmailbox:msg/$NG" | python3 -c 'import json,sys; print(json.load(sys.stdin)["height"])')
trusted() { cast call --rpc-url "$ETHRPC" "$CLIENT" 'trustedHeight()(int64)' | cut -d' ' -f1; }
proven() { [ "$(trusted)" -gt "$SH" ]; }
wait_for 120 proven || { kill -CONT "$RELAYER_PID"; die "the updater bought no header above the sends' block $SH (the client trusts $(trusted))"; }
echo "the sends (message $N0 BUBBLE, $NG GNOT; gno block $SH the later) are proven by the updater's header: the client trusts $(trusted)"
# the stroke's own status, and both Ethereum strokes final (a pipe to tail would hide either: E4-TL5)
out=$("$BL/bin/hlwatch" flag -config "$BL/bridge.json" -node "http://$GNORPC" -home "$GK" -key test1 -eth-key "$BL/keys/watch6.key" -route BUBBLE -fn credits 2>&1) || { kill -CONT "$RELAYER_PID"; die "hlwatch flag: $out"; }
echo "$out" | tail -6
for obj in "the gate" HlAdmin; do
  echo "$out" | grep -q "^setFlag on $obj is final" || { kill -CONT "$RELAYER_PID"; die "hlwatch's stroke did not land final on $obj: $out"; }
done
mirrored() { status 9302 | python3 -c 'import json,sys; j=json.load(sys.stdin); assert j["sendsSet"] and j["gate"]["anyFlag"] and j["admin"]["anyFlag"], (j["gate"], j["admin"]); print("the monitor: the gate and HlAdmin flagged, sends mirrored on the gno routes")'; }
wait_for 60 mirrored || { kill -CONT "$RELAYER_PID"; die "the monitor did not mirror the pause: $(status 9302)"; }
# a block under the flag (both flags final and read): the deliveries the relayer makes after the unpauses are simulated
# here, below, and the gate must refuse them
PAUSE_BLOCK=$(cast block-number --rpc-url "$ETHRPC")
kill -CONT "$RELAYER_PID"
mirrored
held() { status 9301 | python3 -c 'import json,sys; w=json.load(sys.stdin)["last"].get("deliveries",""); assert "paused" in w and "2 route messages wait" in w, w; print("the relayer:", w[:160])'; }
wait_for 60 held || die "the relayer does not hold the two sends in flight for the pause: $(status 9301 | python3 -c 'import json,sys; print(json.load(sys.stdin)["last"].get("deliveries"))')"
held
sleep 10
[ "$(bal "$TOKEN1")" = "$BB" ] || die "alice's BUBBLE moved during the pause: $(bal "$TOKEN1"), was $BB"
[ "$(bal "$TOKEN0")" = "$GB0" ] || die "alice's GNOT moved during the pause: $(bal "$TOKEN0"), was $GB0"
echo "the sends in flight wait out the pause: nothing minted (alice holds $BB BUBBLE, $GB0 GNOT)"
W=$(hlwatch)
echo "$W" | grep -q 'credits by seat 1' || die "hlwatch does not show the gno credits flag: $W"
echo "$W" | grep -q '^- the gate .*watch slot 6 [^;]* FLAG since block' || die "hlwatch does not show the gate's flag: $W"
echo "$W" | grep -q '^- HlAdmin .*watch slot 6 [^;]* FLAG since block' || die "hlwatch does not show HlAdmin's flag: $W"

say "the unpauses: gno.land's at once (Govern), the gate's and HlAdmin's through r/hlgov (kind 2, $UNPAUSE s after delivery)"
govern unpausegno hlmailbox "hlmailbox.NewUnpauseRequest(cross(cur), \"$NS/hlbubble\", \"credits\", 0)"
govern unpausegate hlgov 'hlgov.NewUnpauseRequest(cross(cur), 0)'
govern unpauseadmin hlgov 'hlgov.NewUnpauseRequest(cross(cur), 1)'
wait_for 240 recorded 9 || die "HlAdmin did not record the two unpauses (decisions 8 and 9)"
cleared() { status 9302 | python3 -c 'import json,sys; j=json.load(sys.stdin); assert not j["gate"]["anyFlag"] and not j["admin"]["anyFlag"] and not j["sendsSet"], (j["gate"], j["admin"], j["sendsSet"])'; }
wait_for $((UNPAUSE + 180)) cleared || die "the flags did not clear: $(status 9302 | python3 -c 'import json,sys; j=json.load(sys.stdin); print(j["gate"], j["admin"], j["sendsSet"])')"
W=$(hlwatch)
! echo "$W" | grep -q 'credits by seat 1' || die "the gno credits flag stands after the unpause: $W"
echo "every flag cleared; the monitor cleared its sends mirror"
WANT=$((BB + SEND)); wait_for 240 is bal "$TOKEN1" || die "the BUBBLE send in flight during the pause did not deliver after the unpauses: alice has $(bal "$TOKEN1") BUBBLE, want $WANT"
WANT=$((GB0 + GSEND)); wait_for 240 is bal "$TOKEN0" || die "the GNOT send in flight during the pause did not deliver after the unpauses: alice has $(bal "$TOKEN0") GNOT, want $WANT"
echo "the sends in flight during the pause delivered after the unpauses: alice holds $(bal "$TOKEN1") BUBBLE, $(bal "$TOKEN0") GNOT"
# the gate's refusal itself, for both tokens (E3-GS10, E4-TL5, E5-CF4): the relayer's own delivery of each send — its
# Mailbox.process with the proof under the header that proved it before the flag — simulated at the block under the
# flag is refused by the gate (Paused()), and at the block before the relayer's delivery it is taken: nothing but the
# flag stood between
PAUSED=$(cast sig 'Paused()')
refused() {
  MID=$(qrender "hlmailbox:msg/$1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])') || die "message $1's id"
  DLV=$(cast logs --json --rpc-url "$ETHRPC" --from-block "$FROM" --address "$MAILBOX" "$(cast keccak 'ProcessId(bytes32)')" "0x$MID" | python3 -c 'import json,sys; l=json.load(sys.stdin); assert len(l) == 1, l; b=l[0]["blockNumber"]; print(l[0]["transactionHash"], int(b, 16) if isinstance(b, str) else int(b))') || die "the delivery of message $1 (id $MID) is not one ProcessId log"
  DTX=${DLV% *}; DBLK=${DLV#* }
  [ "$(lower "$(cast tx --rpc-url "$ETHRPC" "$DTX" to)")" = "$(lower "$MAILBOX")" ] || die "the delivery $DTX is no call of the mailbox"
  INPUT=$(cast tx --rpc-url "$ETHRPC" "$DTX" input) || die "the delivery $DTX's input"
  out=$(cast call --rpc-url "$ETHRPC" --block "$PAUSE_BLOCK" "$MAILBOX" "$INPUT" 2>&1) && die "the relayer's delivery of message $1 ($2), simulated at block $PAUSE_BLOCK under the flag, was taken: $out"
  echo "$out" | grep -qiE "${PAUSED#0x}|Paused\(\)" || die "the relayer's delivery of message $1 ($2) under the flag failed otherwise than by the gate's Paused() ($PAUSED): $out"
  out=$(cast call --rpc-url "$ETHRPC" --block "$((DBLK - 1))" "$MAILBOX" "$INPUT" 2>&1) || die "the relayer's delivery of message $1 ($2), simulated at block $((DBLK - 1)) after the unpauses, was refused: $out"
  echo "the gate refuses the relayer's own delivery of message $1 ($2) under the flag (block $PAUSE_BLOCK: Paused() $PAUSED) and takes it at block $((DBLK - 1)), before the relayer's ($DTX, block $DBLK)"
}
refused "$N0" "BUBBLE: token 1, its ISM the gate"
refused "$NG" "GNOT: token 0, its ISM the mailbox's default, the gate"

say "a send after the unpause delivers"
callpath "gno.land/r/$ADDR/bubble" Approve "$ROUTE_ADDR" $((SEND + FEE))
call hlbubble TransferRemote $ETH_DOMAIN "$(lower "$E_alice")" $SEND
WANT=$((BB + 2 * SEND)); wait_for 240 is bal "$TOKEN1" || die "alice has $(bal "$TOKEN1") BUBBLE on Ethereum, want $WANT"
echo "alice holds $(bal "$TOKEN1") BUBBLE on Ethereum"

say "a batch re-emit executes on chain (E3-AS1): two sends in two blocks, the updater held, re-emitted by the relayer in one transaction of Reemit MsgCalls, then delivered under that block's header"
# the relayer re-emits split waiting messages in one transaction so one header serves them (§5.7): with the updater
# held (SIGSTOP) no header is bought for either send's own block, so the relayer's batch is the only way they meet —
# and gnoland-1 (here too: run_submitters) refuses a MsgRun from its key, so it must be MsgCalls
GB=$(bal "$TOKEN0")
N1=$(rj hlmailbox count)
N2=$((N1 + 1))
kill -STOP "$UPDATER_PID"
callsend "$((GSEND + GFEE))ugnot" hlgnot TransferRemote $ETH_DOMAIN "$(lower "$E_alice")" $GSEND
callsend "$((GSEND + GFEE))ugnot" hlgnot TransferRemote $ETH_DOMAIN "$(lower "$E_alice")" $GSEND
reemitted() { grep -q "re-emitted \[$N1 $N2\] at gno height" "$BL/agents/relayer.log"; }
wait_for 90 reemitted || { kill -CONT "$UPDATER_PID"; die "the relayer did not re-emit messages $N1 and $N2 in one transaction: $(status 9301 | python3 -c 'import json,sys; l=json.load(sys.stdin)["last"]; print(l.get("waiting"), "|", l.get("reemit:batch"))')"; }
RH=$(sed -n "s/.*re-emitted \[$N1 $N2\] at gno height \([0-9]*\) .*/\1/p" "$BL/agents/relayer.log" | tail -1)
curl -sf "http://$GNORPC/block_results?height=$RH" >"$BL/run/reemit_results.json" || { kill -CONT "$UPDATER_PID"; die "block_results at $RH"; }
python3 - "$BL/run/reemit_results.json" "$NS/hlmailbox" "$N1" "$N2" <<'EOF' || { kill -CONT "$UPDATER_PID"; die "block $RH is not one transaction carrying both re-emits: $(cut -c1-600 "$BL/run/reemit_results.json")"; }
import json, sys
res, mailbox, want = json.load(open(sys.argv[1]))["result"], sys.argv[2], {sys.argv[3], sys.argv[4]}
def dispatches(tx):
    out = []
    for e in tx.get("ResponseBase", {}).get("Events") or []:
        if e.get("type") == "Dispatch" and e.get("pkg_path") == mailbox:
            out.append({a["key"]: a["value"] for a in e.get("attrs") or []}.get("nonce"))
    return out
carrying = [d for d in (dispatches(tx) for tx in res["results"]["deliver_tx"]) if d]
assert len(carrying) == 1 and set(carrying[0]) == want, carrying
print("block %s: one transaction re-emits messages %s (its one result carries both Dispatch events)" % (res.get("height"), sorted(carrying[0])))
EOF
kill -CONT "$UPDATER_PID"
WANT=$((GB + 2 * GSEND)); wait_for 240 is bal "$TOKEN0" || die "the re-emitted sends did not deliver: alice has $(bal "$TOKEN0") GNOT on Ethereum, want $WANT"
echo "both re-emitted sends delivered: alice holds $(bal "$TOKEN0") GNOT on Ethereum"

say "hlwatch: green"
wait_for 60 identity_ok || die "the monitor's identity lines at the end: $(status 9302)"
green() { hlwatch | grep -q '^hlwatch: GREEN'; }
wait_for 60 green || { hlwatch; die "hlwatch is not green"; }
W=$(hlwatch)
echo "$W" | grep -E '^- (BUBBLE|GNOT):|  - holds|the client:|the header record|^- HlAdmin|hlwatch: GREEN' | cut -c1-200
! grep '"level":"flag"' "$BL/alarms.log" | grep -q '"key":"identity\|"key":"header\|"key":"decision\|"key":"sources\|"key":"diverted\|"key":"credit' || die "a monitor flagged a finding: $(grep '"level":"flag"' "$BL/alarms.log")"
# the slot watch held the upgraded gate the rest of the run: no proxy:slots since the slot was put back (the one alarm
# before it, the hand's, seen and cleared above), and hlwatch (green above) holds it too
slots_since() { tail -n +$((A1 + 1)) "$BL/alarms.log" | grep 'proxy:slots'; tail -n +$((M1 + 1)) "$BL/agents/monitor.log" | grep 'proxy:slots'; }
! slots_since | grep -q . || die "the slot watch alarmed after the slot was put back: $(slots_since | head -3)"
python3 -c 'import json,sys; u=[x for x in json.load(open(sys.argv[1]))["decisions"].get("upgrades") or [] if x["number"] == 2 and x.get("params")]; assert u, "no upgrade"' "$BL/agents/hlwatch.json" || die "hlwatch's slot watch does not hold the gate to decision 2: $(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["decisions"].get("upgrades"))' "$BL/agents/hlwatch.json")"
echo "the slot watch held the gate to decision 2 (kind 8) on the monitor and on hlwatch: no proxy:slots alarm but the hand's, cleared"
# eth.pricesAt dates the run's prices (E3-GS11): neither machine that values fees by them raised prices:stale
stale() { grep -h 'alarm prices:stale' "$BL/agents/updater.log" "$BL/agents/relayer.log"; grep '"key":"prices:stale"' "$BL/alarms.log"; }
! stale | grep -q . || die "prices:stale was raised though the config dates its prices: $(stale | head -1)"
echo "the alarms the run raised: $(wc -l <"$BL/alarms.log" | tr -d ' ') lines in $BL/alarms.log (no prices:stale: eth.pricesAt is the run's date)"
# no alarm remains that the run did not mean to leave (the integration lane I3): at its end only inbound:dust:BUBBLE
# (the dust step's, standing a day by design) may stand — deliveries:paused (the flag step) and proxy:slots:<the gate>
# (the slot moved by hand) ended cleared like every other — and runway:relayer-gno was never raised (the relayers' gno
# key holds the planned float, E5-RT5); an alarm the run did not raise on purpose and cleared is printed, never fatal (a
# moment's cut or a parked attempt on a loaded box)
python3 - "$BL/alarms.log" "proxy:slots:$GATE" <<'EOF' || die "an alarm stands at the run's end, or runway:relayer-gno was raised; see $BL/alarms.log"
import json, sys
purpose, last = {"deliveries:paused", sys.argv[2], "inbound:dust:BUBBLE"}, {}
for line in open(sys.argv[1]):
    if line.strip():
        j = json.loads(line)
        last[j["key"]] = j["level"]
standing = sorted(k for k, v in last.items() if v != "clear" and k != "inbound:dust:BUBBLE")
others = sorted(k for k in last if k not in purpose)
print("alarms.log keys: " + ", ".join("%s (last %s)" % (k, last[k]) for k in sorted(last)))
if others:
    print("raised and cleared, not on purpose: " + ", ".join(others))
assert not standing and "runway:relayer-gno" not in last, "standing at the end: %s; runway:relayer-gno raised: %s" % (standing, "runway:relayer-gno" in last)
EOF
booked_ok || die "a header booked for another reason: $(grep 'booked (' "$BL/agents/updater.log" | grep -v "bought by its sends' fees" | head -3)"
echo "the updater booked $(grep -c 'updater: header [0-9]* booked (' "$BL/agents/updater.log") header(s), each by its sends' fees: no six-hour draw (a decision's header books nothing)"

say "hleth audit of the governed bridge (E3-CF9): each value a decision moved held to the decision that moved it — PASS"
# the members' audit at the yield reads the bridge as it stands: kinds 7, 8, 3, 5 and 6 executed, a kind 12 cancelled,
# two unpauses. The machines' config is the operator's file (E5-CF3 item 3): from it alone — its gno source, its routes'
# expected locks — the verdict is UNVERIFIED, naming both, never PASS. The member's audit gives its own gno source
# (-gno-rpc: here a second spelling of the same node, beside the config's, so every gno read is made twice and
# compared) and the launch notice's expected locks (E4-TL1): PASS
audit_operators() { member -config "$BL/bridge.json" >"$BL/audit1-config.log" 2>&1; grep -q '^hleth audit: UNVERIFIED' "$BL/audit1-config.log"; }
wait_for 60 audit_operators || { grep -E '^FAIL|^UNVERIFIED|^hleth audit' "$BL/audit1-config.log" | head -20; die "hleth audit from the operator's config alone is not UNVERIFIED; see $BL/audit1-config.log"; }
for want in "UNVERIFIED gno.land is read only from the operator's config" "UNVERIFIED token 0's launch limits" "UNVERIFIED token 1's launch limits"; do
  grep -q "^$want" "$BL/audit1-config.log" || die "the audit from the operator's config alone does not say \"$want\": $(grep -E '^UNVERIFIED|^hleth audit' "$BL/audit1-config.log" | head -8)"
done
grep -q "the operator's config (-config's route of token 0's address $TOKEN0, its realm $NS/hlgnot) expects $EXPECTED_LOCK — the operator's word, never taken" "$BL/audit1-config.log" || die "the operator's lock is not said to be noted only: $(grep "token 0's launch limits" "$BL/audit1-config.log")"
echo "from the operator's config alone: $(grep '^hleth audit' "$BL/audit1-config.log" | cut -c1-120) — gno.land and both locks the operator's"
audit_end() { member -config "$BL/bridge.json" -gno-rpc "http://$GNORPC/" -expected-lock-gnot $EXPECTED_LOCK -expected-lock-bubble $EXPECTED_LOCK >"$BL/audit1.log" 2>&1 && grep -q 'hleth audit: PASS' "$BL/audit1.log"; }
wait_for 60 audit_end || { grep -E '^FAIL|^UNVERIFIED|^WARN|^hleth audit' "$BL/audit1.log" | head -20; die "hleth audit of the governed bridge; see $BL/audit1.log"; }
grep -q '^gno.land is read from 2 source(s): .*(-gno-rpc: yours), .*(the config.s: the operator.s)' "$BL/audit1.log" || die "the audit did not read gno.land from the member's source beside the config's: $(grep '^gno.land is read' "$BL/audit1.log")"
grep -q "compiled from its expected lock $EXPECTED_LOCK (-expected-lock-gnot)" "$BL/audit1.log" || die "the audit did not take token 0's expected lock from -expected-lock-gnot: $(grep "token 0's launch limits" "$BL/audit1.log")"
grep -q 'ok   the protocol fee is 1000 wei (must be 1000: 0 at launch; decision #1 (kind 7' "$BL/audit1.log" || die "the audit does not hold the fee to decision 1: $(grep 'the protocol fee is' "$BL/audit1.log")"
grep -q "ok   gate's upgrade 2, to $NEWGATE, is decision #2 (kind 8" "$BL/audit1.log" || die "the audit does not hold the gate's upgrade to decision 2: $(grep "gate's upgrade\|gate was upgraded" "$BL/audit1.log")"
grep -q "ok   gate's implementation slot holds $NEWGATE (must be $NEWGATE, the implementation decision #2 (kind 8" "$BL/audit1.log" || die "the audit does not hold the gate's implementation slot to decision 2: $(grep "gate's implementation slot" "$BL/audit1.log")"
grep -q "ok   token 0's ISM is 0x0000000000000000000000000000000000000000 (must be 0x0000000000000000000000000000000000000000, what decision #3 (kind 3" "$BL/audit1.log" || die "the audit does not hold token 0's ISM to decision 3: $(grep "token 0's ISM" "$BL/audit1.log")"
grep -q "ok   seat 4 is $E_g5, r/hlgov's Ethereum seat as it last set it" "$BL/audit1.log" || die "the audit does not hold seat 4 to decision 4: $(grep 'seat 4' "$BL/audit1.log")"
grep -q "ok   token 1's router for domain $OTHER_DOMAIN is $(word "$E_remote") (must be $(word "$E_remote"); decision #5 (kind 6" "$BL/audit1.log" || die "the audit does not hold token 1's second router to decision 5: $(grep "token 1's router" "$BL/audit1.log")"
grep -q "ok   token 1's enrolled domains are \[$GNO_DOMAIN $OTHER_DOMAIN\]" "$BL/audit1.log" || die "the audit does not hold token 1's domains to decision 5: $(grep "token 1's enrolled domains" "$BL/audit1.log")"
grep -q 'the line recorded 9 decision(s): 8 executed, 0 spent, 1 cancelled, 0 pending' "$BL/audit1.log" || die "the audit's reading of HlAdmin's decisions: $(grep 'the line recorded' "$BL/audit1.log")"
grep -q 'ok   the line.s last number 9 is r/hlgov.s counter 9' "$BL/audit1.log" || die "the audit's counter: $(grep "r/hlgov's counter" "$BL/audit1.log")"
grep -E '^gno.land is read|the line recorded|ok   the protocol fee is|ok   gate.s upgrade 2|ok   gate.s implementation slot|ok   seat [46] is|ok   the trust root.s hash|^hleth audit' "$BL/audit1.log" | cut -c1-200
say "PASS in $(($(date +%s) - T0))s (state and logs in $BL)"
