#!/bin/sh
# The bridge end to end on one machine, from nothing, under the light client (bridge/PLAN.md §5–§6): a Solana test
# validator with mainnet's feature set, the six program deployments and the setup step by `hlsol setup` (our three
# programs built with the factor-8 clocks and the run's launch values, Hyperlane's mainnet builds for the mailbox and
# the tokens) run to a passing audit BEFORE the realms go up (AN10), a gnodev chain the realms are then uploaded to
# at the plan's shape, the governor (gnodev's test1, compiled into the six staged realms, M1k) launching them through
# Govern and making the first decisions — a treasurer swap queued 7 days (600 s at this run's realm clock factor of
# 1008) and landed by a stranger's Execute (the relayer's own gas key) before the run ends, a seat removal at once, a
# seat replacement queued, r/hlgov's kinds and his cancel — then a GovDAO proposal refused before the yield (pre-armed,
# "awaits the yield" on hlwatch), his Yield, Govern refused after it, GovDAO's first proposal cancelling his queued seat
# replacement, and the rest through GovDAO's proposals (a builder's Request's Proposal()) executed with
# ExecuteOrRejectProposal (F2), and the machines:
# three signers, the updater feeding real local-chain headers to the client, a primary and a backup relayer, a
# monitor with both watch keys, hlwatch read against it all. Then: 500 BUBBLE and 10 GNOT go gno.land -> Solana and
# mint there; 200 BUBBLE come back as a claim the relayer's own Claim loop pays with the fee first; the locks equal
# the supplies and the monitor's identity lines say so; a 30,000 BUBBLE send — above the pausable ISM's 25,000
# per-message maximum (AN5; the gno route's own maximum is raised to 50,000 in the staged realm so the ISM is what
# refuses) — is refused by the ISM at delivery (OverMax, named on the relayer's status) and parks after three
# refusals while the sections after it run, in flight for the identity; a GovDAO decision from r/hlgov (set
# protocol fee) is delivered to the admin program and recorded pending with its factor-8 maturity (the relayer's
# executor waits for it), then a kind-12 decision (set BUBBLE's mint limits to 50,000 a message — the change that
# would let the parked send through at its hourly retry, at the decision's maturity only) is delivered and recorded
# pending too; a cancel of the fee decision — r/hlgov's salted body (v27) — executes at recording and hlwatch shows
# its salt, and a second cancel of the same list, under its own salt, is delivered by the BACKUP relayer 60 s after
# the primary is stopped; a watch key's flag (hlwatch's one stroke) pauses deliveries, which the relayer then stages
# nothing for (G2), a Solana -> gno burn parks behind the gno credits flag after three deterministic failures,
# GovDAO's unpause and the key's clear reopen both sides and the waiting send delivers; a hold of two guardians
# (with a dip) stands on the admin program and the pausable ISM, which hlwatch shows and `hlrecover status` and
# `execute` read as the deferral of the pending kind 12 (there is no recovery path: Jae's design change of
# 2026-10-13 removed the validators' channel, the stages, the digests, the windows, the recorded settled set and
# Settle from the programs and the tools); a mocked provider that
# disagrees leaves a second monitor with no verdict and the alarm `solana:providers`, and the same provider twice
# is refused by the config; a validator-set change on the gno chain (r/gnops/valopers) makes the client quarantine
# and a message dispatched before it is re-emitted; `hlsol audit` prints a setup that matches §5.8 (the client's
# trusted hash against the node, the layout version 1, the admin program's stored cancelled range, the hold
# history, the mint limits, the pending count,
# the three programs' clock factors and origin domains agreeing); hlwatch is green from nothing. Everything it
# starts, it stops.
#
# Audit round 2 (M3f) adds: a burst of a dozen 200-BUBBLE sends ten seconds apart (to a second wallet) under
# which the monitor flags no header:gap — the record's scan stops at the snapshot's slot (G14); `hlsol setup`
# compiles the pausable ISM's launch day cap from the expected locks (F10) and the audits take `-hlgov` to
# check the admin program's compiled sender and r/hlgov's counter (G20, K15); one audit with `-so` pointing at
# a corrupted copy and the wrong realm paths, which must FAIL on the ELF hash and the two compiled bindings
# (G20); `hlsol send -config` refusing a bridge realm's address as the recipient (K21); `hlrecover execute`
# refusing the immature kind 12 with its maturity (AO1); the set-size line against the client's bound (G19).
#
#   bridge/local/e2e.sh            (needs: go, cargo + the Solana CLI 3.0.14 in $SOLBIN, gnodev, gnokey, python3, rsync)
#   E2E_ONLY=setup bridge/local/e2e.sh   the setup step and the audit only (the deployments, both setup runs, hlsol audit)
#
# The compiled launch values the run cannot know in advance — the signers' keys, the routes' routers (the token
# programs), r/hlgov's recipient and the Solana lists (the admin program, the pausable ISM, the client, the six
# Solana seats), the watch key's gno seat (test1, so the monitor can flag), BUBBLE's premine and the meter's cap
# floor — are swapped into the STAGED copies of the realms only; the programs' launch values go into a STAGED copy
# of bridge/programs (never the repo's), whose SBF build is cached across runs by `hlsol setup`'s stamp, and the
# six program ids are derived from fixed seeds (hlsol keygen: local runs only) so the cache holds. State lives in
# $BL (default ~/.config/bubblerumble-jarvis/bridge-e2e), wiped each run; the durable caches beside it.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
BL=${BL:-$HOME/.config/bubblerumble-jarvis/bridge-e2e}
PROGS=${PROGS:-$HOME/.config/bubblerumble-jarvis/bridge-e2e-programs}
SBF=${SBF:-$HOME/.config/bubblerumble-jarvis/bridge-e2e-sbf}
export CARGO_TARGET_DIR=${CARGO_TARGET_DIR:-$HOME/.config/bubblerumble-jarvis/bridge-e2e-target}
SOLBIN=${SOLBIN:-$HOME/.local/share/solana-3.0.14/solana-release/bin}
SO=${SO:-$HOME/.config/bubblerumble-jarvis/bridge-ref/so}
EX=${EX:-$HOME/src/jaekwon/gno-fixes/examples}
export PATH="$SOLBIN:$HOME/.cargo/bin:$PATH"
ADDR=g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr
TEST1=g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5
MNEMONIC="source bonus chronic canvas draft south burst lottery vacant surface solve popular case indicate oppose farm nothing bullet exhibit title speed wink action roast"
SOLRPC=http://127.0.0.1:8899
PROXY=http://127.0.0.1:8898
GNORPC=127.0.0.1:26657
SOL_DOMAIN=1399811150
GNO_DOMAIN=1198419796
NS="gno.land/r/$ADDR"
ONE=1000000
SEND=$((500 * ONE)) # BUBBLE gno.land -> Solana (the minimum is 200, the fee 100 on top)
BACK=$((200 * ONE)) # BUBBLE Solana -> gno.land (a claim; the fee is its first installment)
FEE=$((100 * ONE))
GSEND=$((10 * ONE)) # GNOT gno.land -> Solana (the minimum is 10 GNOT, the fee 5 GNOT on top)
GFEE=$((5 * ONE))
SEND2=$((300 * ONE))  # BUBBLE gno.land -> Solana under the flag, delivered after the unpause
PARK=$((10 * ONE))    # BUBBLE Solana -> gno.land that parks behind the credits flag
SEND3=$((250 * ONE))  # BUBBLE gno.land -> Solana before the validator-set change: re-emitted, undelivered (the quarantine)
BIG=$((30000 * ONE))  # BUBBLE gno.land -> Solana above the pausable ISM's 25,000 per-message maximum: refused (OverMax), parked
BURST=$((200 * ONE))  # BUBBLE gno.land -> Solana, a dozen of them ten seconds apart to bob (G14: a header every tick, no header:gap)
BURST_N=12
BURST_TOTAL=$((BURST_N * BURST))
EXPECTED_LOCK=$((1000000 * ONE)) # the lock the launch notice expects, per token (F10: a tenth of it is above the 100,000 ceiling, so the day cap stays 100,000)
RELAYER_FUND=100000000 # ugnot the relayer's gas-only key gets from test1
T0=$(date +%s)

say() { printf '\n== %s (%ss)\n' "$*" "$(($(date +%s) - T0))"; }
die() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
PIDS=""
cleanup() { for p in $PIDS; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; }
trap cleanup EXIT INT TERM
# wait_for SECONDS CMD...: retry CMD once a second until it succeeds
wait_for() { n=$1; shift; while ! "$@" >/dev/null 2>&1; do n=$((n - 1)); [ "$n" -gt 0 ] || return 1; sleep 1; done; }

rm -rf "$BL"; mkdir -p "$BL/bin" "$BL/keys/programs" "$BL/signers" "$BL/agents" "$BL/run" "$BL/realms" "$SBF" "$PROGS"

say build
(cd "$REPO/bridge" && nice -n 10 go build -o "$BL/bin/" ./cmd/...)
[ -f "$SO/token.so" ] || python3 "$HERE/dump-programs.py" "$SO"
cp "$SO/mailbox.so" "$SO/token.so" "$SBF/"
# the programs' sources, staged (the launch values are written there, never in the repo; the build cache holds)
rsync -a --delete --exclude target --exclude tests "$REPO/bridge/programs/" "$PROGS/"

say keys
for k in payer relayer updater watch watch6 alice bob treasurer g1 g2 g3 g4; do "$SOLBIN/solana-keygen" new --no-bip39-passphrase --silent -o "$BL/keys/$k.json" >/dev/null; done
pk() { "$SOLBIN/solana-keygen" pubkey "$BL/keys/$1.json"; }
for p in mailbox gnot bubble client pausable admin; do "$BL/bin/hlsol" keygen -seed "bridge e2e $p" -o "$BL/keys/programs/$p.json" >/dev/null; done
ppk() { "$SOLBIN/solana-keygen" pubkey "$BL/keys/programs/$1.json"; }
for i in 0 1 2; do "$BL/bin/hlkey" new "$BL/signers/s$i.json" >>"$BL/signers/public.txt"; done
"$BL/bin/hlkey" new "$BL/keys/valoper.json" >/dev/null   # a consensus key for the validator-set change (never signs a block)
VALOPER_PUB=$("$BL/bin/hlkey" gnopub "$BL/keys/valoper.json")
GK="$BL/gnokey"; mkdir -p "$GK"
printf '%s\n\n\n' "$MNEMONIC" | gnokey add -recover -insecure-password-stdin -home "$GK" -quiet test1 >/dev/null 2>&1
printf '\n\n' | gnokey add -insecure-password-stdin -home "$GK" -quiet relayer >/dev/null 2>&1 # the relayer's gas-only key
RELAYER_ADDR=$(gnokey list -home "$GK" 2>/dev/null | sed -n 's/^.* relayer (local) - addr: \(g1[a-z0-9]*\).*/\1/p')
[ -n "$RELAYER_ADDR" ] || die "no relayer key"
cat >"$BL/run/b58hex.py" <<'EOF'
import sys
B58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
s = sys.argv[1]; n = 0
for c in s: n = n * 58 + B58.index(c)
print(n.to_bytes(32, "big").hex())
EOF
hex32() { python3 "$BL/run/b58hex.py" "$1"; }
echo "programs: mailbox $(ppk mailbox) gnot $(ppk gnot) bubble $(ppk bubble) client $(ppk client) pausable $(ppk pausable) admin $(ppk admin)"

say "gno chain (the packages and GovDAO at genesis; the bridge realms go up after the Solana audit, AN10)"
ST="$BL/gno"; mkdir -p "$ST"
stage() { # stage SRC DST: a package's non-test files and its gnomod.toml
  mkdir -p "$2"
  for f in "$1"/*.gno; do case "$f" in *_test.gno|*_filetest.gno) ;; *) cp "$f" "$2/" ;; esac; done
  cp "$1/gnomod.toml" "$2/"
}
for p in checkpoint grc20votes governor hyperlane decisions hlroute; do stage "$REPO/realm/p/$p/v0" "$ST/p/$ADDR/$p/v0"; done
for r in bubble hlmailbox hlverifier hlfees hlgnot hlbubble hlgov hlgnot2; do stage "$REPO/realm/$r" "$BL/realms/$r"; done # hlgnot2 staged for the governor swap, not uploaded
for p in avl/v0 bptree/v0 ufmt/v0 cford32/v0 seqid/v0; do stage "$EX/gno.land/p/nt/$p" "$ST/p/nt/$p"; done # gnodev loads examples lazily
# GovDAO (the proxy, its impl, the member store, the treasury and the dev-chain init), the valoper realms (a
# validator-set change goes through them) and every package they import,
# transitively: genesis txs never pass through the lazy loader, so the realms that import r/gov/dao need it in the
# extra root, where gnodev sorts dependencies before dependents (a package gnodev loads lazily at a transaction
# reloads the node, which breaks the sender's sequence: everything a transaction touches is staged here)
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
for p in $(python3 "$BL/run/closure.py" "$EX" gno.land/r/gov/dao gno.land/r/gov/dao/impl/v0 gno.land/r/gov/dao/memberstore/v0 gno.land/r/gov/dao/init/v0 gno.land/r/gnops/valopers gno.land/r/gnops/valopers/proposal); do
  [ -d "$ST/$(echo "$p" | sed 's|^gno.land/||')" ] || stage "$EX/$p" "$ST/$(echo "$p" | sed 's|^gno.land/||')"
done
# the launch values this run decides: see the header
SIGNERS=$(paste -sd, - <"$BL/signers/public.txt")
swap() { # swap FILE FROM-REGEX TO: one sed, checked (TO is read back as a fixed string)
  sed -i.bak "s|$2|$3|" "$1" && rm -f "$1.bak"
  grep -qF -- "$3" "$1" || die "swap into $1 failed"
}
swap "$BL/realms/bubble/mint.gno" '^const premineTo = address("[a-z0-9]*")' "const premineTo = address(\"$TEST1\")"
swap "$BL/realms/hlmailbox/mailbox.gno" 'launchSigners   = "[a-f0-9,]*"' "launchSigners   = \"$SIGNERS\""
swap "$BL/realms/hlmailbox/flags.gno" 'launchSeat5 = address("[a-z0-9]*")' "launchSeat5 = address(\"$TEST1\")"
swap "$BL/realms/hlbubble/hlbubble.gno" 'launchRouter   = "[A-Za-z0-9]*"' "launchRouter   = \"$(hex32 "$(ppk bubble)")\""
swap "$BL/realms/hlbubble/hlbubble.gno" 'launchCapFloor = 10_000 \* one' "launchCapFloor = 100_000_000 * one"
# the gno route's per-message maximum is raised past the pausable ISM's 25,000 (AN5) so the ISM is what refuses the 30,000 send
swap "$BL/realms/hlbubble/hlbubble.gno" 'launchMaximum  = 25_000 \* one' "launchMaximum  = 50_000 * one"
swap "$BL/realms/hlgnot/hlgnot.gno" 'launchRouter   = "[A-Za-z0-9]*"' "launchRouter   = \"$(hex32 "$(ppk gnot)")\""
swap "$BL/realms/hlgov/hlgov.gno" 'launchRecipient   = "[a-f0-9]*"' "launchRecipient   = \"$(hex32 "$(ppk admin)")\""
swap "$BL/realms/hlgov/hlgov.gno" 'launchPausableISM = "[a-f0-9]*"' "launchPausableISM = \"$(hex32 "$(ppk pausable)")\""
swap "$BL/realms/hlgov/hlgov.gno" 'launchClient      = "[a-f0-9]*"' "launchClient      = \"$(hex32 "$(ppk client)")\""
i=1; for k in g1 g2 g3 g4 watch watch6; do
  swap "$BL/realms/hlgov/hlgov.gno" "launchSolSeat$i    = \"[a-f0-9]*\"" "launchSolSeat$i    = \"$(hex32 "$(pk $k)")\""
  i=$((i + 1))
done
# the governor (M1k): gnodev's test1 in all six realms that compile one — the five uploaded and hlgnot2 — with one
# regex each, since a realm whose constant differs from the mailbox's refuses to deploy (its init: "the compiled
# governor … is not the mailbox's …", which addpkg would fail on)
for f in hlmailbox/mailbox.gno hlgnot/hlgnot.gno hlbubble/hlbubble.gno hlgnot2/hlgnot2.gno hlfees/hlfees.gno hlgov/hlgov.gno; do
  swap "$BL/realms/$f" '^const launchGovernor = address("[a-z0-9]*")' "const launchGovernor = address(\"$TEST1\")"
done
# the realms' clock factor (§5.9: the test chain's realms carry one as the programs carry 8): 1008 here, so the 7-day
# delay is 600 s and the 14-day deadline 1200 s — a decision the governor queues at the launch lands by a stranger's
# Execute before the run ends, and the deadline, which also ends the cap-ratio quiet (AR5), outlasts the run by
# some eight minutes; the meters are per real day, never divided
REALM_FACTOR=1008; REALM_DELAY=$((7 * 86400 / REALM_FACTOR))
swap "$ST/p/$ADDR/decisions/v0/decisions.gno" 'Factor = int64(1)$' "Factor = int64($REALM_FACTOR)"
# empty blocks every second: the machines' ordered cut wants a gno block after each Solana snapshot (§5.9), as
# mainnet gives one every 3.4 s; the updater's headers come from these blocks too
(cd "$ST" && exec nice -n 10 gnodev local -chain-id dev -no-watch -no-web -empty-blocks -empty-blocks-interval 1 -extra-root "$ST" >"$BL/gnodev.log" 2>&1) &
PIDS="$PIDS $!"
wait_for 120 curl -sf "http://$GNORPC/status" || die "gnodev did not start"
! grep -q "type check failed" "$BL/gnodev.log" || die "a package failed to load; see $BL/gnodev.log"
gnotx() { # gnotx WHAT ARGS...: one gnokey maketx from test1, refused unless gnokey says OK!
  what=$1; shift
  out=$(echo | gnokey "$@" -broadcast -chainid dev -remote "$GNORPC" -insecure-password-stdin -home "$GK" test1 2>&1) || die "$what: $out"
  echo "$out" | grep -q '^OK!' || die "$what: $out"
  echo "$what: $(echo "$out" | grep 'GAS USED' | tr -s ' ')"
}
call() { # call PKG FUNC ARGS...: a MsgCall from test1 to a bridge realm
  pkg=$1; fn=$2; shift 2
  gnotx "$pkg.$fn" maketx call -pkgpath "$NS/$pkg" -func "$fn" $(for a in "$@"; do printf -- '-args %s ' "$a"; done) -gas-fee 1000000ugnot -gas-wanted 100000000
}
callpath() { # callpath PKGPATH FUNC ARGS...: a MsgCall from test1 to any realm
  pkg=$1; fn=$2; shift 2
  gnotx "$pkg.$fn" maketx call -pkgpath "$pkg" -func "$fn" $(for a in "$@"; do printf -- '-args %s ' "$a"; done) -gas-fee 1000000ugnot -gas-wanted 100000000
}
callsend() { # callsend SEND PKG FUNC ARGS...: a MsgCall from test1 carrying coins (the GNOT route's envelope)
  send=$1; pkg=$2; fn=$3; shift 3
  gnotx "$pkg.$fn (sending $send)" maketx call -pkgpath "$NS/$pkg" -func "$fn" -send "$send" $(for a in "$@"; do printf -- '-args %s ' "$a"; done) -gas-fee 1000000ugnot -gas-wanted 100000000
}
run() { # run SCRIPT: a MsgRun from test1 (a GovDAO member's proposal script); the script follows the key name
  out=$(echo | gnokey maketx run -gas-fee 1000000ugnot -gas-wanted 200000000 -broadcast -chainid dev -remote "$GNORPC" \
    -insecure-password-stdin -home "$GK" test1 "$1" 2>&1) || die "$1: $out"
  echo "$out" | grep -q '^OK!' || die "$1: $out"
  echo "$(basename "$1"): $(echo "$out" | grep 'GAS USED' | tr -s ' ')"
}
addpkg() { # addpkg NAME: upload a staged realm
  gnotx "addpkg $NS/$1" maketx addpkg -pkgpath "$NS/$1" -pkgdir "$BL/realms/$1" -gas-fee 1000000ugnot -gas-wanted 400000000
}
qeval() { gnokey query vm/qeval -data "$NS/$1" -remote "$GNORPC" 2>/dev/null | sed -n 's/^data: (\([-0-9]*\) .*/\1/p'; }
qrender() { gnokey query vm/qrender -data "$NS/$1" -remote "$GNORPC" 2>/dev/null | sed -n 's/^data: //p'; }
out=$(echo | gnokey maketx send -to "$RELAYER_ADDR" -send "${RELAYER_FUND}ugnot" -gas-fee 1000000ugnot -gas-wanted 2000000 -broadcast \
  -chainid dev -remote "$GNORPC" -insecure-password-stdin -home "$GK" test1 2>&1) || die "funding the relayer's key: $out"
echo "the relayer's gas key $RELAYER_ADDR holds $RELAYER_FUND ugnot"

say "solana validator"
nice -n 10 "$SOLBIN/solana-test-validator" --reset --quiet --ledger "$BL/ledger" --rpc-port 8899 --limit-ledger-size 50000000 \
  --url https://api.mainnet-beta.solana.com --clone-feature-set \
  --clone-upgradeable-program TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb --clone ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL \
  --bpf-program noopb9bkMVfRPU8AsbpTUg8AQkHtKwMYZiFUjNRtMmV "$SO/noop.so" >"$BL/solana.log" 2>&1 &
PIDS="$PIDS $!"
wait_for 120 "$SOLBIN/solana" -u "$SOLRPC" cluster-version || die "solana validator did not start"
for k in payer relayer updater alice; do "$SOLBIN/solana" -u "$SOLRPC" airdrop 100 "$BL/keys/$k.json" >/dev/null; done
for k in watch watch6 g1 g2 g3 g4; do "$SOLBIN/solana" -u "$SOLRPC" airdrop 1 "$BL/keys/$k.json" >/dev/null; done

say "hlsol setup: the six deployments and the setup step (the programs built at factor 8 with this run's launch values)"
setup() { nice -n 10 "$BL/bin/hlsol" setup -rpc "$SOLRPC" -deployer "$BL/keys/payer.json" -keys "$BL/keys/programs" -so "$SBF" -programs "$PROGS" -factor 8 -solana-bin "$SOLBIN" \
  -gno-rpc "http://$GNORPC" -gno-domain $GNO_DOMAIN -local-domain $SOL_DOMAIN -mailbox-pkgpath "$NS/hlmailbox" -hlgov "$NS/hlgov" \
  -gnot-router "$NS/hlgnot" -bubble-router "$NS/hlbubble" -guardians "$(pk g1),$(pk g2),$(pk g3),$(pk g4)" -watch "$(pk watch),$(pk watch6)" \
  -treasurer "$(pk treasurer)" -ata-fund 100000000 -expected-lock-gnot $EXPECTED_LOCK -expected-lock-bubble $EXPECTED_LOCK; }
setup >"$BL/setup.log" 2>&1 || { tail -30 "$BL/setup.log"; die "hlsol setup"; }
grep -q 'hlsol setup: DONE' "$BL/setup.log" || die "hlsol setup did not reach its done condition: $(tail -5 "$BL/setup.log")"
# the launch day cap compiled from the expected locks (F10): the smaller of 100,000 and a tenth of the lock
grep -q "the pausable ISM's launch day cap: 100000000000 base units for each token" "$BL/setup.log" || die "the setup did not compile the day cap from the expected locks: $(grep 'day cap' "$BL/setup.log")"
grep "launch day cap" "$BL/setup.log" | cut -c1-160
grep '^setup: ' "$BL/setup.log" | tail -4
# resumable: a second run repeats nothing
setup >"$BL/setup2.log" 2>&1 || { tail -30 "$BL/setup2.log"; die "hlsol setup (second run)"; }
grep -q 'hlsol setup: DONE' "$BL/setup2.log" || die "the second setup run did not pass"
! grep -q '^setup: .*: [1-9A-HJ-NP-Za-km-z]\{80,\}$' "$BL/setup2.log" || die "the second setup run sent a transaction: $(grep -E '^setup: .*: [1-9A-HJ-NP-Za-km-z]{80,}$' "$BL/setup2.log" | head -3)"
echo "the second run repeated nothing"
say "hlsol audit: the setup against §5.8, before any realm goes up (a holder's audit reads at finalized: the last transactions get their 32 slots)"
audit_pass() { "$BL/bin/hlsol" audit -rpc "$SOLRPC" -keys "$BL/keys/programs" -so "$SBF" -gno-rpc "http://$GNORPC" -mailbox-pkgpath "$NS/hlmailbox" -hlgov "$NS/hlgov" -deployer "$(pk payer)" >"$BL/audit0.log" 2>&1 && grep -q 'hlsol audit: PASS' "$BL/audit0.log"; }
wait_for 90 audit_pass || { grep -E '^FAIL|^hlsol audit' "$BL/audit0.log"; die "hlsol audit before the realms"; }
grep -q "ok   the client's trusted hash against /block?height=" "$BL/audit0.log" || die "the audit does not compare the trusted hash with the node's (G10)"
# the compiled bindings on the deployed ELFs (G20): the pausable ISM's mailbox and tokens, the client's mailbox
# pkgpath, the admin program's mailbox, tokens and r/hlgov's address; r/hlgov's counter is read once the realm is up
for want in "ok   pausable ISM ELF binds the mailbox $(ppk mailbox)" "ok   pausable ISM ELF binds the GNOT token $(ppk gnot)" "ok   pausable ISM ELF binds the BUBBLE token $(ppk bubble)" "ok   client ELF binds the mailbox pkgpath \"$NS/hlmailbox\"" "ok   admin program ELF binds the mailbox $(ppk mailbox)" "ok   admin program ELF binds the BUBBLE token $(ppk bubble)" "ok   admin program ELF binds r/hlgov's address" "r/hlgov ($NS/hlgov) is not on the chain yet"; do
  grep -qF -- "$want" "$BL/audit0.log" || die "the first audit lacks: $want"
done
grep -E '^(ok  |FAIL) ' "$BL/audit0.log" | head -6; echo "..."; grep 'ELF binds' "$BL/audit0.log" | head -3 | cut -c1-140; grep -E '^hlsol audit: ' "$BL/audit0.log"
if [ "${E2E_ONLY:-}" = setup ]; then say "PASS (the setup step only)"; exit 0; fi

say "the realms go up (addpkg, after the Solana side passed its audit: AN10's order)"
for r in bubble hlmailbox hlverifier hlfees hlgnot hlbubble hlgov; do addpkg "$r"; done

say "the governor launches the realms (Govern from test1's own key, M1k) and queues his first decision"
cat >"$BL/run/init_govdao.gno" <<EOF
package main

import govinit "gno.land/r/gov/dao/init/v0"

func main(cur realm) {
	govinit.InitWithUsers(cross(cur), "$TEST1")
}
EOF
propose_and_execute_dao() { # propose_and_execute_dao NAME PROPOSAL-EXPR IMPORT...: a GovDAO proposal from a dao.ProposalRequest (gnops' own), voted, executed through ExecuteOrRejectProposal (F2)
  name=$1; expr=$2; shift 2
  {
    printf 'package main\n\nimport (\n'
    for imp in "$@"; do printf '\t"%s"\n' "$imp"; done
    printf '\t"gno.land/r/gov/dao"\n)\n\nfunc main(cur realm) {\n\tpid := dao.MustCreateProposal(cross(cur), %s)\n\tdao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")\n\tif !dao.ExecuteOrRejectProposal(cross(cur), pid) {\n\t\tpanic("%s did not execute (denied)")\n\t}\n}\n' "$expr" "$name"
  } >"$BL/run/$name.gno"
  run "$BL/run/$name.gno"
}
propose_and_execute() { # propose_and_execute NAME REQUEST-EXPR IMPORT...: a bridge builder's Request as a GovDAO proposal — its Proposal() (M1k) — voted, executed through ExecuteOrRejectProposal
  name=$1; expr=$2; shift 2
  propose_and_execute_dao "$name" "$expr.Proposal()" "$@"
}
govern_script() { # govern_script NAME REALM REQUEST-EXPR: the script of the governor's own decision — the realm's Govern with a builder's Request, a MsgRun from test1 (M1k)
  printf 'package main\n\nimport "%s/%s"\n\nfunc main(cur realm) {\n\t%s.Govern(cross(cur), %s)\n}\n' "$NS" "$2" "$2" "$3" >"$BL/run/$1.gno"
}
govern() { govern_script "$@"; run "$BL/run/$1.gno"; }
refused() { # refused WHAT PATTERN RC OUTPUT: the chain must have refused the transaction (no OK!) with PATTERN in its words
  if [ "$3" -eq 0 ] && echo "$4" | grep -q '^OK!'; then die "$1 was taken: $4"; fi
  echo "$4" | grep -q -- "$2" || die "$1 was not refused with '$2': $4"
  echo "$1: refused — $(echo "$4" | grep -o -- "$2.*" | head -1 | cut -c1-220)"
}
run_refused() { # run_refused SCRIPT PATTERN: a MsgRun from test1 the chain must refuse
  rc=0; out=$(echo | gnokey maketx run -gas-fee 1000000ugnot -gas-wanted 200000000 -broadcast -chainid dev -remote "$GNORPC" -insecure-password-stdin -home "$GK" test1 "$1" 2>&1) || rc=$?
  refused "$(basename "$1")" "$2" "$rc" "$out"
}
call_refused() { # call_refused PKGPATH FUNC PATTERN ARGS...: a MsgCall from test1 the chain must refuse
  pkg=$1; fn=$2; pat=$3; shift 3
  rc=0; out=$(echo | gnokey maketx call -pkgpath "$pkg" -func "$fn" $(for a in "$@"; do printf -- '-args %s ' "$a"; done) -gas-fee 1000000ugnot -gas-wanted 100000000 -broadcast -chainid dev -remote "$GNORPC" -insecure-password-stdin -home "$GK" test1 2>&1) || rc=$?
  refused "$pkg.$fn" "$pat" "$rc" "$out"
}
decision() { # decision REALM ID: that decision of the realm's ledger, as one JSON line in the realm's own compact form
  qrender "$1:decisions.json" | python3 -c 'import json,sys; d=[x for x in json.load(sys.stdin) if x["id"]=='"$2"']; assert len(d)==1, d; print(json.dumps(d[0], separators=(",", ":")))'
}
run "$BL/run/init_govdao.gno"
govern launch hlmailbox 'hlmailbox.NewLaunchRequest(cross(cur))'
qrender "hlmailbox:json" | grep -q '"live":true' || die "not live after Launch: $(qrender hlmailbox:json)"
qrender "hlmailbox:json" | grep -q "\"registered\":true" || die "the verifier is not registered: $(qrender hlmailbox:json)"
qrender "hlmailbox:json" | grep -q "\"governor\":\"$TEST1\",\"yielded\":0" || die "the mailbox does not render test1 as the governor, unyielded: $(qrender hlmailbox:json)"
echo "live by the governor's own Launch decision; signers $(qrender hlmailbox:json | python3 -c 'import json,sys; j=json.load(sys.stdin); print(len(j["signers"]["keys"]), "of threshold", j["signers"]["threshold"])'); governor $TEST1, yielded 0"
# the governor's first queued decision (§5.9): a treasurer swap, 7 days — $REALM_DELAY s at this run's factor — landed
# later by a stranger's Execute (the relayer's own gas key, neither the governor's nor GovDAO's; checked near the end)
govern treasurer hlfees "hlfees.NewSetTreasurerRequest(cross(cur), \"$RELAYER_ADDR\")"
TGOV=$(date +%s)
decision hlfees 1 | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["kind"]=="set treasurer" and d["state"]=="pending" and d["due"]-d["recorded"]=='"$REALM_DELAY"', d; print("hlfees decision 1 (set treasurer to the relayer'"'"'s key) queued by the governor, due in", d["due"]-d["recorded"], "s —", d["note"])' || die "the governor's treasurer swap is not queued $REALM_DELAY s: $(qrender hlfees:decisions.json)"

say "the machines: three signers, the updater, a primary and a backup relayer, a monitor"
# alarms land here as one JSON line each (the webhook's consumer)
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
python3 "$BL/run/sink.py" 9130 "$BL/alarms.log" &
PIDS="$PIDS $!"
python3 - "$BL" "$(ppk mailbox)" "$(ppk gnot)" "$(ppk bubble)" "$(ppk client)" "$(ppk pausable)" "$(ppk admin)" "$(command -v gnokey)" "$RELAYER_ADDR" "$TEST1" "$(pk updater)" "$PROXY" "$EXPECTED_LOCK" <<'EOF'
import json, sys
bl, mailbox, gnot, bubble, client, pausable, admin, gnokey, relayer, test1, updater, proxy = sys.argv[1:13]
expected = int(sys.argv[13])  # the lock the launch notice expects (C341: mint:cap-ratio is quiet while the lock is below it)
pub = [l.strip() for l in open(bl + '/signers/public.txt')]
ns = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/"
cfg = {
  "gno": {"rpc": "http://127.0.0.1:26657", "sources": ["http://127.0.0.1:26657", "http://127.0.0.1:26657"], "chainId": "dev",
          "mailbox": ns + "hlmailbox", "fees": ns + "hlfees", "gov": ns + "hlgov", "dao": "gno.land/r/gov/dao",
          "gnokey": gnokey, "home": bl + "/gnokey", "key": "relayer", "address": relayer,
          "watchKey": "test1", "watchAddress": test1, "gasPrice": 0.001},
  "solana": {"rpcs": ["http://127.0.0.1:8899", "http://127.0.0.1:8899"], "mailbox": mailbox, "client": client, "pausable": pausable, "admin": admin,
             "domain": 1399811150, "payer": bl + "/keys/relayer.json", "updater": updater, "updaterKey": bl + "/keys/updater.json", "watchKey": bl + "/keys/watch.json"},
  "routes": [{"symbol": "GNOT", "gno": ns + "hlgnot", "token": gnot, "expectedLock": expected}, {"symbol": "BUBBLE", "gno": ns + "hlbubble", "token": bubble, "expectedLock": expected}],
  "signers": [{"url": "http://127.0.0.1:%d" % (9110 + i), "key": e} for i, e in enumerate(pub)],
  "allowHTTP": True,
  "webhook": "http://127.0.0.1:9130/alarm"}
json.dump(cfg, open(bl + '/bridge.json', 'w'), indent=2)
# the same provider twice without allowHTTP: refused (G12)
bad = json.loads(json.dumps(cfg)); bad["allowHTTP"] = False; bad["signers"] = []
json.dump(bad, open(bl + '/bridge-same-provider.json', 'w'), indent=2)
# a second monitor whose second provider is the disagreeing proxy, no watch keys (alarms only)
m2 = json.loads(json.dumps(cfg)); m2["solana"]["rpcs"] = ["http://127.0.0.1:8899", proxy]; m2["gno"]["watchKey"] = ""; m2["gno"]["watchAddress"] = ""; m2["solana"]["watchKey"] = ""
json.dump(m2, open(bl + '/bridge-monitor2.json', 'w'), indent=2)
EOF
for i in 0 1 2; do
  nice -n 10 "$BL/bin/hlbridge" signer -every 1s -config "$BL/bridge.json" -key "$BL/signers/s$i.json" -store "$BL/agents/s$i" -listen "127.0.0.1:911$i" -status-listen "127.0.0.1:914$i" >"$BL/agents/s$i.log" 2>&1 &
  PIDS="$PIDS $!"
done
updater() { # starts the updater on its state file, its pid in LASTPID (stopped and restarted below to order the sections)
  nice -n 10 "$BL/bin/hlbridge" updater -every 2s -interval 20s -config "$BL/bridge.json" -state "$BL/agents/updater.json" -listen 127.0.0.1:9123 >>"$BL/agents/updater.log" 2>&1 &
  LASTPID=$!; PIDS="$PIDS $LASTPID"
}
updater; UPDATER=$LASTPID
relayer() { # relayer ROLE STATE PORT LOG: starts one, its pid in LASTPID
  nice -n 10 "$BL/bin/hlbridge" relayer -every 1s -role "$1" -config "$BL/bridge.json" -state "$BL/agents/$2" -listen "127.0.0.1:$3" -gno-gas 60000000 >>"$BL/agents/$4" 2>&1 &
  LASTPID=$!; PIDS="$PIDS $LASTPID"
}
relayer primary relayer.json 9121 relayer.log; PRIMARY=$LASTPID
relayer backup relayer2.json 9124 relayer2.log
nice -n 10 "$BL/bin/hlbridge" monitor -every 2s -config "$BL/bridge.json" -state "$BL/agents/monitor.json" -listen 127.0.0.1:9122 >"$BL/agents/monitor.log" 2>&1 &
PIDS="$PIDS $!"
status() { curl -sf "http://127.0.0.1:$1/v1/status"; }
hlwatch() { "$BL/bin/hlwatch" status -config "$BL/bridge.json" -node "http://$GNORPC" -state "$BL/agents/hlwatch.json"; }
signing() { status 9140 | grep -q '"signing":true'; }
wait_for 60 signing || die "signer 0 is not signing: $(status 9140); see $BL/agents/s0.log"
echo "signer 0: $(status 9140 | python3 -c 'import json,sys; j=json.load(sys.stdin); print("signing" if j["signing"] else "halted: " + j["halted"], "—", j["cut"], "; the client may run", j["aheadAllowance"], "blocks ahead before the stop")')"
curl -sf "http://127.0.0.1:9110/v1/status" >/dev/null 2>&1 && die "the signer serves its status beside the public endpoint (G5)"
echo "the signer's public endpoint serves no status (G5)"

say "the same provider twice is refused by the config (G12)"
out=$("$BL/bin/hlbridge" providers "$BL/bridge-same-provider.json" "$BL/bridge.json" 2>&1 || true)
# (the check walks a map: either config may be read first, and the two wordings of the refusal both count)
echo "$out" | grep -q "the same provider twice\|the two providers are the same" || die "the same provider twice was not refused: $out"
echo "refused: $(echo "$out" | head -1 | cut -c1-160)"

say "gno.land -> solana: 500 BUBBLE (+100 fee), under the light client"
ROUTE_ADDR=$("$BL/bin/hlkey" pkg "$NS/hlbubble" | cut -d' ' -f1)
B0=$(qeval "bubble.BalanceOf(\"$TEST1\")")
call bubble Approve "$ROUTE_ADDR" $((SEND + FEE + BIG + FEE + SEND2 + FEE + SEND3 + FEE + BURST_N * (BURST + FEE)))
call hlbubble TransferRemote $SOL_DOMAIN "$(pk alice)" $SEND
[ "$(qeval 'hlbubble.Locked()')" = "$SEND" ] || die "locked $(qeval 'hlbubble.Locked()'), want $SEND"
FEES_ADDR=$("$BL/bin/hlkey" pkg "$NS/hlfees" | cut -d' ' -f1)
[ "$(qeval "bubble.BalanceOf(\"$FEES_ADDR\")")" = "$FEE" ] || die "the fee realm holds $(qeval "bubble.BalanceOf(\"$FEES_ADDR\")"), want $FEE"
bal() { "$BL/bin/hlsol" balance -rpc "$SOLRPC" -token "$(ppk bubble)" -owner "$(pk alice)"; }
is() { [ "$("$@" | tail -1)" = "$WANT" ]; }
WANT=$SEND; wait_for 180 is bal || die "alice has $(bal) BUBBLE on solana, want $WANT; see $BL/agents/relayer.log and updater.log"
echo "alice holds $(bal) BUBBLE on solana; the updater: $(status 9123 | python3 -c 'import json,sys; j=json.load(sys.stdin); print(j["submits"], "headers submitted, trusted height", j["trustedHeight"])')"

say "gno.land -> solana: 10 GNOT (+5 fee), the GNOT lock's envelope"
callsend "$((GSEND + GFEE))ugnot" hlgnot TransferRemote $SOL_DOMAIN "$(pk alice)" $GSEND
[ "$(qeval 'hlgnot.Locked()')" = "$GSEND" ] || die "GNOT locked $(qeval 'hlgnot.Locked()'), want $GSEND"
gbalsol() { "$BL/bin/hlsol" balance -rpc "$SOLRPC" -token "$(ppk gnot)" -owner "$(pk alice)"; }
WANT=$GSEND; wait_for 180 is gbalsol || die "alice has $(gbalsol) GNOT on solana, want $WANT; see $BL/agents"
echo "alice holds $(gbalsol) GNOT on solana"

say "solana -> gno.land: 200 BUBBLE, a claim the relayer pays (a bridge realm's own address as the recipient is refused first, K21)"
out=$("$BL/bin/hlsol" send -rpc "$SOLRPC" -payer "$BL/keys/alice.json" -token "$(ppk bubble)" -mailbox "$(ppk mailbox)" -gno-domain $GNO_DOMAIN -to "$ROUTE_ADDR" -amount $BACK -config "$BL/bridge.json" 2>&1 || true)
echo "$out" | grep -q "is the address of $NS/hlbubble, a bridge realm" || die "hlsol send did not refuse the route realm's address: $out"
echo "refused: $(echo "$out" | grep -o 'hlsol send: .*a bridge realm' | cut -c1-140)"
"$BL/bin/hlsol" send -rpc "$SOLRPC" -payer "$BL/keys/alice.json" -token "$(ppk bubble)" -mailbox "$(ppk mailbox)" \
  -gno-domain $GNO_DOMAIN -to "$TEST1" -amount $BACK -config "$BL/bridge.json" | grep '^sent'
# the relayer pays the claim in the second it is credited, so the script watches claims ever credited, not owed
claims() { qrender "hlbubble:json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["claims"])'; }
WANT=1; wait_for 120 is claims || die "$(claims) claims credited on gno, want 1 (the relayer's Process); see $BL/agents"
echo "credited: $(claims) claim; owed now $(qrender hlbubble:json | python3 -c 'import json,sys; print(json.load(sys.stdin)["owed"])')"
gbal() { qeval "bubble.BalanceOf(\"$TEST1\")"; }
WANT=$((B0 - SEND - FEE + BACK - FEE)); wait_for 120 is gbal || die "test1 has $(gbal) on gno, want $WANT (the relayer's Claim, with the fee first); see $BL/agents/relayer.log"
echo "test1 holds $(gbal) BUBBLE on gno.land; the fee realm $(qeval "bubble.BalanceOf(\"$FEES_ADDR\")")"
[ "$(qeval "bubble.BalanceOf(\"$FEES_ADDR\")")" = $((2 * FEE)) ] || die "the fee realm holds $(qeval "bubble.BalanceOf(\"$FEES_ADDR\")"), want $((2 * FEE))"
status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); c=j["claims"]["BUBBLE"]; assert c["paid"] >= 1, j; print("the relayer paid", c["paid"], "claim(s) in", c["calls"], "call(s)")' || die "the relayer's status does not show the claim paid: $(status 9121)"
! grep -q "delivered on" "$BL/agents/relayer2.log" || die "the backup relayer delivered something while the primary was up: $(grep 'delivered on' "$BL/agents/relayer2.log")"
status 9124 | grep -q '"role":"backup"' || die "the backup's status: $(status 9124)"
echo "the backup relayer runs and delivered nothing (the primary acts first)"

say "the locks cover the supplies"
LOCKED=$(qeval "hlbubble.Locked()")
[ "$LOCKED" = $((SEND - BACK)) ] || die "BUBBLE locked $LOCKED, want $((SEND - BACK))"
[ "$(qeval 'hlgnot.Locked()')" = "$GSEND" ] || die "GNOT locked $(qeval 'hlgnot.Locked()'), want $GSEND"
echo "locked on gno.land: BUBBLE $LOCKED, GNOT $(qeval 'hlgnot.Locked()') (Solana's supplies are read by the monitor and hlwatch below)"
qrender "hlbubble:json" | grep -q '"owed":0,' || die "owed is not 0: $(qrender hlbubble:json)"

say "the monitor's identity lines, the client's record, no flag"
identity_ok() { status 9122 | python3 -c '
import json,sys; j=json.load(sys.stdin)
b=j["identity"]["BUBBLE"]; g=j["identity"]["GNOT"]
assert b["ok"] and b["lock"] == b["supply"] == '"$((SEND - BACK))"' and b["owed"] == 0 and b["gap"] == 0, b
assert g["ok"] and g["lock"] == g["supply"] == '"$GSEND"' and g["gap"] == 0, g
r=j["record"]; assert r["count"] >= 2 and r["mismatch"] is None and r["checked"] == r["count"], r
c=j["client"]; assert c["trustedHeight"] > 0, c
print("monitor:", b["text"]); print("monitor:", g["text"])
print("monitor: cut", j["cut"], "; sources", j["sources"]); print("monitor: client", json.dumps(c)); print("monitor: record", r["count"], "headers rebuilt and checked, no mismatch")
print("monitor: decisions", json.dumps(j["decisions"]), "; pausable", json.dumps(j["pausable"]))'; }
wait_for 90 identity_ok || die "the monitor's lines are not right: $(status 9122)"
identity_ok
! grep -q '"level":"flag"' "$BL/alarms.log" || die "a flag was raised: $(cat "$BL/alarms.log")"
# (a signer signs on no verdict older than two of its rounds — two seconds at -every 1s — so between rounds on a
# loaded machine its "signing" flag dips for a moment: each signer is waited for, as signer 0 was)
for i in 0 1 2; do signing_i() { status "914$i" | grep -q '"signing":true'; }; wait_for 60 signing_i || die "signer $i stopped: $(status "914$i")"; done
echo "no flag raised; the three signers are signing; alarms so far: $(wc -l <"$BL/alarms.log" | tr -d ' ')"

say "a burst: $BURST_N sends of 200 BUBBLE ten seconds apart to bob — a header lands every tick, and the monitor sees no header:gap (G14)"
# (every send makes the updater submit the head at its next tick; the monitor's record scan folds up to its
# snapshot's slot and no further, so the record is never one header past the client's count at the snapshot)
bbal() { "$BL/bin/hlsol" balance -rpc "$SOLRPC" -token "$(ppk bubble)" -owner "$(pk bob)"; }
i=0; while [ "$i" -lt "$BURST_N" ]; do
  call hlbubble TransferRemote $SOL_DOMAIN "$(pk bob)" $BURST >/dev/null
  i=$((i + 1)); [ "$i" -lt "$BURST_N" ] && sleep 10
done
WANT=$BURST_TOTAL; wait_for 240 is bbal || die "bob has $(bbal) BUBBLE on solana, want $WANT; see $BL/agents/relayer.log and updater.log"
echo "bob holds $(bbal) BUBBLE on solana after $BURST_N sends; the updater: $(status 9123 | python3 -c 'import json,sys; j=json.load(sys.stdin); print(j["submits"], "headers submitted, trusted height", j["trustedHeight"])')"
sleep 6 # three polls after the last delivery
status 9122 | python3 -c '
import json,sys; j=json.load(sys.stdin)
a=j["alarms"]; bad=[k for k in a if k.startswith("header:") or k == "cut"]; assert not bad, a
r=j["record"]; assert r["mismatch"] is None and r["count"] >= 14, r
print("monitor: no header alarm;", r["count"], "headers recorded,", r["checked"], "checked; alarms:", sorted(a))' || die "the monitor alarmed on the burst: $(status 9122)"
! grep -q '"key":"header:gap"\|"key":"header:mismatch"' "$BL/alarms.log" || die "a header alarm reached the sink during the burst: $(grep 'header:' "$BL/alarms.log" | head -3)"
! grep -q '"level":"flag"' "$BL/alarms.log" || die "a flag was raised during the burst: $(grep '"level":"flag"' "$BL/alarms.log")"
echo "no header:gap, no header:mismatch, no flag through the burst"

say "a mint above the pausable ISM's per-message maximum (25,000 BUBBLE, AN5): a 30,000 send is refused at delivery (OverMax) and will park"
# (the gno route lets it through at its raised maximum; the ISM's Verify parses the token message and refuses the
# amount inside the mailbox's own InboxProcess; the relayer closes its proof stage and backs off, three refusals
# park it — checked before the flag section, which pauses deliveries; the sections in between give it the minutes)
BIG_NONCE=$(qrender "hlmailbox:json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["count"])')
call hlbubble TransferRemote $SOL_DOMAIN "$(pk alice)" $BIG
TBIG=$(date +%s)
[ "$(qeval 'hlbubble.Locked()')" = $((SEND - BACK + BURST_TOTAL + BIG)) ] || die "locked $(qeval 'hlbubble.Locked()'), want $((SEND - BACK + BURST_TOTAL + BIG))"
overmax() { status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); l=j["last"].get("deliver:'"$BIG_NONCE"'",""); assert "pausable ISM: OverMax" in l, j["last"]; print("the relayer:", l[:150])'; }
wait_for 150 overmax || die "the relayer did not report the ISM's OverMax on message $BIG_NONCE: $(status 9121); see $BL/agents/relayer.log"
overmax
WANT=$((SEND - BACK)); is bal || die "alice holds $(bal) BUBBLE: the 30,000 minted"
echo "nothing minted (alice still holds $(bal)); the send is in flight for the identity until the limits change"

say "the governor's decisions on hlmailbox (Govern from test1, M1k): a seat removal at once, a seat replacement queued; hlwatch says who governs"
# (the launch and the treasurer swap were his first two; a GovDAO proposal is refused until he yields, below)
govern seat4 hlmailbox 'hlmailbox.NewRotateSeatsRequest(cross(cur), "4:-")'
decision hlmailbox 2 | python3 -c 'import json,sys; a=json.load(sys.stdin); assert a["kind"]=="rotate seats" and a["state"]=="applied" and a["due"]==a["recorded"], a; print("hlmailbox decision 2 (rotate seats: remove seat 4) applied at once —", a["note"])' || die "the seat removal is not applied at once: $(qrender hlmailbox:decisions.json)"
govern seat2 hlmailbox "hlmailbox.NewRotateSeatsRequest(cross(cur), \"2:$ADDR\")"
decision hlmailbox 3 | python3 -c 'import json,sys; q=json.load(sys.stdin); assert q["kind"]=="rotate seats" and q["state"]=="pending" and q["due"]-q["recorded"]=='"$REALM_DELAY"', q; print("hlmailbox decision 3 (rotate seats: set seat 2) queued", q["due"]-q["recorded"], "s —", q["note"])' || die "the seat replacement is not queued $REALM_DELAY s: $(qrender hlmailbox:decisions.json)"
W=$(hlwatch)
echo "$W" | grep -q "^- governor: $TEST1 (until the yield) — it alone decides, by Govern from its own key, and gno.land/r/gov/dao's proposals are refused" || die "hlwatch does not show the governor: $W"
echo "$W" | grep -q '  - 4 (guardian): (empty)' || die "hlwatch does not show seat 4 vacated: $W"
echo "$W" | grep -q "r/hlmailbox #3 rotate seats: .*set seat 2 to $ADDR — matures in" || die "hlwatch does not show the queued seat replacement: $W"
echo "$W" | grep -q "r/hlfees #1 set treasurer: set treasurer to $RELAYER_ADDR .* — matures in" || die "hlwatch does not show the governor's queued treasurer swap: $W"
echo "$W" | grep -E '^- governor:|#3 rotate seats|#1 set treasurer|  - 4 \(guardian\)' | cut -c1-200
finisher() { status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); l=j["last"].get("proposals",""); assert "'"$1"'" in l, j["last"]; print("the relayer:", l[:180])'; }
wait_for 15 finisher "awaits the yield" || die "the relayer's finisher does not wait on the yield: $(status 9121)"
finisher "awaits the yield"

say "the governor's decision from r/hlgov to the admin program (Govern): set protocol fee, delivered and recorded pending at its factor-8 maturity"
govern fee hlgov "hlgov.NewSetProtocolFeeRequest(cross(cur), 1000, \"$(hex32 "$(pk treasurer)")\")"
qrender "hlgov:json" | grep -q '"last":1,' || die "r/hlgov did not dispatch decision 1: $(qrender hlgov:json)"
recorded() { status 9122 | python3 -c 'import json,sys; j=json.load(sys.stdin); d=j["decisions"]; assert d and d["adminLast"] == '"$1"' and d["govLast"] == '"$1"', d'; }
wait_for 180 recorded 1 || die "the admin program did not record decision 1: monitor $(status 9122 | python3 -c 'import json,sys; print(json.load(sys.stdin)["decisions"])'); see $BL/agents/relayer.log"
W=$(hlwatch)
echo "$W" | grep -q '  - #1 kind 7 (set protocol fee 1000 lamports.* matures in' || die "hlwatch does not show decision 1 pending on the admin program with its text: $W"
echo "$W" | grep '  - #1 kind 7 ('
echo "$W" | grep -q 'last decision number 1 (r/hlgov'"'"'s 1)' || die "the counters are not both 1: $W"
echo "$W" | grep -q '^- #1 kind 7: set protocol fee 1000 lamports' || die "hlwatch does not list r/hlgov's decision 1: $W"
# (the LIMITS MISMATCH line is this run's own doing: hlbubble's maximum raised past the ISM's for AN5 — AS4's alarm, checked at the end)
! echo "$W" | grep -v 'LIMITS MISMATCH' | grep -q 'FOREIGN\|MISMATCH\|COUNTERS DIFFER' || die "hlwatch sees a foreign decision or a mismatch: $W"
# the relayer's executor sees it and waits for its maturity (21 hours at factor 8), attempting nothing
waits() { status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); l=j["last"].get("solexec:'"$1"'",""); assert "waits: matures at" in l, j["last"]; print("the executor:", l)'; }
wait_for 30 waits 1 || die "the relayer's executor does not wait on decision 1: $(status 9121)"
waits 1
! grep -q 'executed on the admin program' "$BL/agents/relayer.log" || die "the executor executed a decision before its maturity: $(grep 'executed on' "$BL/agents/relayer.log")"

say "the governor's kind-12 decision from r/hlgov: set BUBBLE's mint limits (50,000 a message, 200,000 a day), delivered and recorded pending at its factor-8 maturity"
# (AN5: the change that would let the parked 30,000 send through at its hourly retry — only once the decision
# matures and the executor applies it through the pausable ISM's owner-only setter; the limits stand until then)
govern limits hlgov "hlgov.NewSetMintLimitsRequest(cross(cur), 1, 50000000000, 200000000000)"
qrender "hlgov:decision/2" | grep -q '"kind":12,' || die "r/hlgov's decision 2 is not a kind 12: $(qrender hlgov:decision/2)"
wait_for 180 recorded 2 || die "the admin program did not record decision 2 (kind 12); see $BL/agents/relayer.log"
W=$(hlwatch)
echo "$W" | grep -q "  - #2 kind 12 (set token 1's mint limits: 50000000000 a message, 200000000000 a day.* matures in" || die "hlwatch does not show the kind 12 pending with its text: $W"
echo "$W" | grep '  - #2 kind 12 (' | cut -c1-200
echo "$W" | grep -q 'BUBBLE: 25000000000 a message, 100000000000 a day' || die "hlwatch does not show BUBBLE's launch limits standing: $W"
echo "$W" | grep -q 'pending accounts 2;' || die "hlwatch does not show two pending accounts: $W"
echo "$W" | grep -E 'the mint limits|BUBBLE: 25000|pending accounts' | cut -c1-160
wait_for 30 waits 2 || die "the relayer's executor does not wait on decision 2: $(status 9121)"
waits 2

say "the governor's cancel from r/hlgov executes at recording: decision 1 is closed; the body carries the realm's salt (v27)"
cancel() { propose_and_execute "$1" 'hlgov.NewCancelRequest(cross(cur), "1")' "$NS/hlgov"; } # GovDAO's, after the yield (the backup section)
govern cancel hlgov 'hlgov.NewCancelRequest(cross(cur), "1")'
qrender "hlgov:decision/3" | grep -q '"kind":1,"body":"01000000000000000[3]' || die "r/hlgov's decision 3 is not a cancel: $(qrender hlgov:decision/3)"
SALT2=$(qrender "hlgov:decision/3" | python3 -c 'import json,sys; b=json.load(sys.stdin)["body"]; assert len(b) >= 2*(9+8+1), b; print(b[18:34])')
[ -n "$SALT2" ] && [ "$SALT2" != "0000000000000000" ] || die "the cancel's body carries no salt: $(qrender hlgov:decision/3)"
echo "r/hlgov's decision 3: a cancel of 1 with salt 0x$SALT2 in its body"
wait_for 180 recorded 3 || die "the admin program did not record the cancel (decision 3); see $BL/agents/relayer.log"
W=$(hlwatch)
! echo "$W" | grep -q '  - #1 kind 7 (' || die "decision 1 still pending after the cancel: $W"
echo "$W" | grep -q '  - #2 kind 12 (' || die "the kind 12 (decision 2) is gone after the cancel of 1: $W"
echo "$W" | grep -q 'pending decisions on the admin program:' || die "hlwatch's Solana section is missing: $W"
echo "$W" | grep -A1 'pending decisions on the admin program' | tail -1 | cut -c1-200
echo "$W" | grep -q "^- #3 kind 1: cancel 0/1 (channel/number), salt 0x$SALT2" || die "hlwatch does not show the cancel's salt: $W"
echo "$W" | grep '^- #3 kind 1'

say "a GovDAO proposal before the yield (M1k): the cancel of the governor's queued seat replacement — pre-armed, 'awaits the yield' on hlwatch, refused when executed"
cat >"$BL/run/propose_cancel.gno" <<EOF
package main

import (
	"$NS/hlmailbox"
	"gno.land/r/gov/dao"
)

func main(cur realm) {
	pid := dao.MustCreateProposal(cross(cur), hlmailbox.NewCancelRequest(cross(cur), 3).Proposal())
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
}
EOF
run "$BL/run/propose_cancel.gno"
W=$(hlwatch)
echo "$W" | grep -q 'PRE-ARMED: has the votes; awaits the yield (until the governor yields, the builder'"'"'s closure refuses gno.land/r/gov/dao' || die "hlwatch does not say the pre-armed proposal awaits the yield: $W"
echo "$W" | grep 'PRE-ARMED' | cut -c1-220
CPID=$(echo "$W" | sed -n 's/^- #\([0-9]*\) .*PRE-ARMED.*/\1/p' | head -1)
[ -n "$CPID" ] || die "no proposal id in hlwatch's line"
execute_script() { # execute_script NAME PID: a script executing GovDAO proposal PID through ExecuteOrRejectProposal
  printf 'package main\n\nimport "gno.land/r/gov/dao"\n\nfunc main(cur realm) {\n\tif !dao.ExecuteOrRejectProposal(cross(cur), %s) {\n\t\tpanic("proposal %s did not execute")\n\t}\n}\n' "$2" "$2" >"$BL/run/$1.gno"
}
execute_script execute_cancel "$CPID"
run_refused "$BL/run/execute_cancel.gno" "until the governor yields, $TEST1 alone decides, by Govern from its own key: not gno.land/r/gov/dao"
decision hlmailbox 3 | grep -q '"state":"pending"' || die "the refused execution touched the queued decision: $(decision hlmailbox 3)"
W=$(hlwatch)
echo "$W" | grep -q "^- #$CPID .*PRE-ARMED: has the votes; awaits the yield" || die "the proposal is not still pre-armed after the refusal: $W"
! grep -q 'ExecuteOrRejectProposal sent' "$BL/agents/relayer.log" "$BL/agents/relayer2.log" || die "a relayer touched GovDAO before the yield: $(grep 'ExecuteOrRejectProposal sent' "$BL/agents/relayer.log" "$BL/agents/relayer2.log")"
echo "proposal #$CPID stands pre-armed and hlmailbox decision 3 pending; no relayer touched GovDAO"

say "the yield (M1k): hlmailbox.Yield from the governor's key (a MsgCall), one way; Govern is refused after it"
callpath "$NS/hlmailbox" Yield
YH=$(qrender "hlmailbox:json" | python3 -c 'import json,sys; j=json.load(sys.stdin); assert j["governor"]=="'"$TEST1"'" and j["yielded"]>0, j; print(j["yielded"])') || die "the mailbox does not render the yield: $(qrender hlmailbox:json)"
for r in hlfees hlgnot hlbubble hlgov; do qrender "$r:json" | grep -q "\"governor\":\"$TEST1\",\"yielded\":$YH" || die "$r does not read the yield: $(qrender "$r:json" | cut -c1-200)"; done
echo "the governor yielded at height $YH; the five realms render the one yield"
call_refused "$NS/hlmailbox" Yield "the governor yielded at height $YH already"
govern_script govern_after hlfees "hlfees.NewSetTreasurerRequest(cross(cur), \"$TEST1\")"
run_refused "$BL/run/govern_after.gno" "the governor yielded at height $YH, gno.land/r/gov/dao governs since: a decision is a GovDAO proposal, never Govern"
W=$(hlwatch)
echo "$W" | grep -q "^- governor: gno.land/r/gov/dao since height $YH ($TEST1 yielded)" || die "hlwatch does not show the yield: $W"
echo "$W" | grep -q "^- #$CPID .*PRE-ARMED: has the votes, anyone may execute it" || die "hlwatch still says the pre-armed proposal awaits the yield: $W"
echo "$W" | grep -E '^- governor:|PRE-ARMED' | cut -c1-200
wait_for 15 finisher "since the yield at height $YH" || die "the relayer's finisher did not see the yield: $(status 9121)"
finisher "since the yield at height $YH"

say "GovDAO's first proposal after the yield: the cancel of the governor's queued seat replacement (hlmailbox decision 3) executes"
run "$BL/run/execute_cancel.gno"
decision hlmailbox 3 | grep -q '"state":"cancelled"' || die "the seat replacement is not cancelled: $(decision hlmailbox 3)"
decision hlmailbox 4 | python3 -c 'import json,sys; c=json.load(sys.stdin); assert c["kind"]=="cancel" and c["state"]=="applied", c; print("hlmailbox decision 4 (cancel 3) by GovDAO —", c["note"])' || die "GovDAO's cancel is not recorded: $(qrender hlmailbox:decisions.json)"
W=$(hlwatch)
! echo "$W" | grep -q '#3 rotate seats' || die "the cancelled seat replacement is still pending on hlwatch: $W"
! echo "$W" | grep -q 'PRE-ARMED' || die "hlwatch still lists the executed proposal as pre-armed: $W"
echo "the governor's queued decision is cancelled by GovDAO's first proposal; nothing is pre-armed"

say "hlwatch: a pre-armed proposal (GovDAO's, after the yield), then a queued decision"
cat >"$BL/run/propose.gno" <<EOF
package main

import (
	"$NS/hlfees"
	"gno.land/r/gov/dao"
)

func main(cur realm) {
	pid := dao.MustCreateProposal(cross(cur), hlfees.NewSetTreasurerRequest(cross(cur), "$TEST1").Proposal())
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
}
EOF
run "$BL/run/propose.gno"
W=$(hlwatch)
echo "$W" | grep -q 'PRE-ARMED' || die "hlwatch does not list the voted, unexecuted proposal as pre-armed: $W"
echo "$W" | grep 'PRE-ARMED'
PID=$(echo "$W" | sed -n 's/^- #\([0-9]*\) .*PRE-ARMED.*/\1/p' | head -1)
[ -n "$PID" ] || die "no proposal id in hlwatch's line"
cat >"$BL/run/execute.gno" <<EOF
package main

import "gno.land/r/gov/dao"

func main(cur realm) {
	if !dao.ExecuteOrRejectProposal(cross(cur), $PID) {
		panic("proposal $PID did not execute")
	}
}
EOF
run "$BL/run/execute.gno"
W=$(hlwatch)
echo "$W" | grep -q "r/hlfees #2 set treasurer: set treasurer to $TEST1 .* — matures in" || die "hlwatch does not show GovDAO's queued decision maturing: $W"
echo "$W" | grep 'matures in'
echo "$W" | grep -q '5 (watch key): '"$TEST1" || die "hlwatch does not show seat 5: $W"
! echo "$W" | grep -q 'PRE-ARMED' || die "hlwatch still lists the executed proposal as pre-armed"
sleep 3
! grep -q 'applied' "$BL/agents/relayer.log" || die "the relayer executed a decision before its delay: $(grep applied "$BL/agents/relayer.log")"
decision hlfees 2 | grep -q '"state":"pending"' || die "GovDAO's decision is not pending: $(qrender hlfees:decisions.json)"
echo "the relayer leaves the queued decision alone until it matures"

say "the backup relayer: the primary is stopped, a second cancel of the same list is delivered by the backup 60 s after it became possible"
kill "$PRIMARY" 2>/dev/null || true; wait "$PRIMARY" 2>/dev/null || true
cancel cancel2
SALT3=$(qrender "hlgov:decision/4" | python3 -c 'import json,sys; b=json.load(sys.stdin)["body"]; print(b[18:34])')
[ -n "$SALT3" ] && [ "$SALT3" != "$SALT2" ] || die "the second cancel's salt is not its own: $SALT3 vs $SALT2"
TC=$(date +%s)
wait_for 200 recorded 4 || die "the admin program did not record the second cancel (decision 4); see $BL/agents/relayer2.log"
TD=$(($(date +%s) - TC))
grep -q "delivered on solana (a decision)" "$BL/agents/relayer2.log" || die "the backup did not deliver the second cancel: $(tail -5 "$BL/agents/relayer2.log")"
[ "$TD" -ge 55 ] || die "the backup delivered after ${TD}s, inside its 60 s offset"
echo "the backup delivered decision 4 after ${TD}s (its 60 s offset, then the staging)"
relayer primary relayer.json 9121 relayer.log; PRIMARY=$LASTPID
wait_for 30 status 9121 || die "the primary did not come back"
echo "the primary is back on its state file"
W=$(hlwatch)
echo "$W" | grep -q "^- #4 kind 1: cancel 0/1 (channel/number), salt 0x$SALT3" || die "hlwatch does not show the second cancel's salt: $W"
echo "$W" | grep '^- #4 kind 1'
echo "$W" | grep -q 'last decision number 4 (r/hlgov'"'"'s 4)' || die "the counters are not both 4: $W"
! echo "$W" | grep -v 'LIMITS MISMATCH' | grep -q 'FOREIGN\|MISMATCH\|COUNTERS DIFFER' || die "hlwatch sees a foreign decision or a mismatch after the cancels: $W"
# (the relayer's done map is pruned to the nonces its walk still consults, G8, so a delivered nonce leaves it; the
# walk stops at the first message not done — the parked 30,000 send — and consults everything after it each pass)
status 9124 | python3 -c 'import json,sys; j=json.load(sys.stdin); assert j["gnoNext"] == '"$BIG_NONCE"', j["gnoNext"]; assert not j["refused"] and not j["taken"], j; print("the backup: its walk stands at the undelivered 30,000 send (gnoNext", j["gnoNext"], ") and is past every delivered message, nothing taken, no Execute refused")' || die "the backup's status: $(status 9124)"

say "the 30,000 send is parked after three OverMax refusals (§5.7: retried hourly, delivered once the kind 12 matures)"
# (three refusals a minute then two minutes apart, the primary's stop in the backup section between them)
LEFT=$((TBIG + 420 - $(date +%s))); [ "$LEFT" -gt 0 ] || LEFT=1
parked_big() { status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); p=j["parked"].get("deliver:'"$BIG_NONCE"'"); assert p, j["parked"]; assert "OverMax" in p["err"] or "0x7e4" in p["err"], p; print("parked: deliver:'"$BIG_NONCE"', retried hourly;", p["err"][:90].replace("\n", " "))'; }
wait_for "$LEFT" parked_big || die "the 30,000 send did not park: $(status 9121)"
parked_big
grep -q '"key":"parked:deliver:'"$BIG_NONCE"'"' "$BL/alarms.log" || die "no parked alarm for the 30,000 send reached the sink"
WANT=$((SEND - BACK)); is bal || die "alice holds $(bal) BUBBLE: the 30,000 minted"

say "a flag: the watch key's one stroke pauses deliveries; the relayer stages nothing (G2); a burn parks behind the gno credits flag"
# the updater is stopped first, so the send below waits for a header (nothing to stage yet) until the flag and
# the monitor's sends mirror have landed; a send after the mirror would be refused on gno (sends paused), and one
# before the flag would be delivered within seconds
kill "$UPDATER" 2>/dev/null || true; wait "$UPDATER" 2>/dev/null || true
call hlbubble TransferRemote $SOL_DOMAIN "$(pk alice)" $SEND2
"$BL/bin/hlwatch" flag -config "$BL/bridge.json" -node "http://$GNORPC" -state "$BL/agents/hlwatch.json" -home "$GK" -key test1 -sol-key "$BL/keys/watch.json" -route BUBBLE -fn credits | tail -2
mirrored() { status 9122 | python3 -c 'import json,sys; j=json.load(sys.stdin); assert j["sendsSet"] and j["pausable"]["anyFlag"], j; print("the monitor: sends mirrored on the gno routes;", "alarms:", sorted(j["alarms"]))'; }
wait_for 60 mirrored || die "the monitor did not mirror the pause: $(status 9122)"
mirrored
updater; UPDATER=$LASTPID
paused() { status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); l=j["last"].get("deliveries",""); assert "paused: a flag stands" in l, j["last"]; print("the relayer:", l)'; }
wait_for 60 paused || die "the relayer does not say deliveries are paused: $(status 9121)"
paused
"$BL/bin/hlsol" send -rpc "$SOLRPC" -payer "$BL/keys/alice.json" -token "$(ppk bubble)" -mailbox "$(ppk mailbox)" -gno-domain $GNO_DOMAIN -to "$TEST1" -amount $PARK | grep '^sent'
TPARK=$(date +%s)
sleep 8
status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); a=j["attempts"]; assert not [k for k in a if k.startswith("deliver:")], a; print("no delivery attempted under the pause (nothing staged)")' || die "a delivery was attempted under the pause: $(status 9121)"
W=$(hlwatch)
echo "$W" | grep -q 'credits by seat 5' || die "hlwatch does not show the gno credits flag: $W"
echo "$W" | grep -q 'the pausable ISM: 1 flags' || die "hlwatch does not show the pausable ISM's flag: $W"
echo "$W" | grep 'credits by seat 5'
failing() { status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); a=[k for k in j["attempts"] if k.startswith("process:")]; assert a, j["attempts"]; print("the burn fails against the credits flag:", j["attempts"][a[0]]["err"][:100])'; }
wait_for 60 failing || die "the burn did not fail against the credits flag: $(status 9121)"
failing

say "a hold: two guardians flag the admin program and the pausable ISM; one dips; hlrecover reads it (status, and execute's deferral)"
for g in g1 g2; do "$BL/bin/hlsol" flag -rpc "$SOLRPC" -key "$BL/keys/$g.json" -pausable "$(ppk pausable)" -admin "$(ppk admin)" | tail -1; done
# hlwatch reads at finalized through both providers: a flag confirmed now is in its view some 15 s later
hold_shown() { W=$(hlwatch); echo "$W" | grep -q 'the admin program: .*HOLD since'; }
wait_for 60 hold_shown || die "hlwatch shows no hold on the admin program: $(hlwatch)"
hlwatch | grep -o 'the admin program: [^;]*;[^;]*' | head -1
"$BL/bin/hlsol" flag -rpc "$SOLRPC" -key "$BL/keys/g2.json" -pausable "$(ppk pausable)" -admin "$(ppk admin)" -clear | tail -1
dip_shown() { W=$(hlwatch); echo "$W" | grep -q 'the admin program: .*HOLD since .*a dip since'; }
wait_for 60 dip_shown || die "hlwatch shows no dip on the admin program: $(hlwatch)"
echo "the hold stands through the dip (a dip shorter than 3 h at factor 8 keeps it)"
hlrecover() { "$BL/bin/hlrecover" "$@" -config "$BL/bridge.json"; }
hlrecover status | tee "$BL/recover-status.log" | grep -E 'holds:|hold history' | head -4
grep -q 'holds: the admin program since 20' "$BL/recover-status.log" || die "hlrecover status shows no hold on the admin program: $(cat "$BL/recover-status.log")"
grep -q 'the hold history: no hold has ended' "$BL/recover-status.log" || die "hlrecover status: no hold history line: $(cat "$BL/recover-status.log")"
grep -q 'there is no recovery path' "$BL/recover-status.log" || die "hlrecover status does not say the recovery path is gone: $(cat "$BL/recover-status.log")"
# the published tool's hand on a decision (AO1): the kind 12 (decision 2) is immature and, under the hold that
# stands now, deferred until the hold ends — execute refuses with the reason the executor would give
out=$(hlrecover execute -payer "$BL/keys/alice.json" -number 2 2>&1 || true)
echo "$out" | grep -q "decision 2 (kind 12) is not executable now: \(matures at\|deferred by the hold on the admin program\)" || die "hlrecover execute did not refuse the immature decision with its reason: $out"
echo "hlrecover execute: $(echo "$out" | grep -o 'decision 2 .*' | cut -c1-120)"
out=$(hlrecover execute -payer "$BL/keys/alice.json" -number 1 2>&1 || true)
echo "$out" | grep -q "no pending decision 1" || die "hlrecover execute did not say decision 1 is gone: $out"
BODY=$(hlrecover body -kind 1 -cancel 0/1 -salt 7 | sed -n 's/^body: //p')
[ "$BODY" = "01000000000000000000000000000000070001000000000000000001" ] || die "hlrecover body built another cancel body: $BODY"
echo "hlrecover body: a cancel of 0/1 under salt 7 is $BODY (the list form of one number, its channel byte 0)"
out=$(hlrecover body -kind 11 -params 00 2>&1 || true)
echo "$out" | grep -q "4, 10 and 11 are reserved" || die "hlrecover body did not refuse kind 11 as reserved: $out"
echo "hlrecover body refuses the reserved kinds (10, 11: the recovery kinds are gone)"

say "a provider that disagrees: a second monitor reading through a mocked provider gives no verdict and alarms solana:providers (G6)"
cat >"$BL/run/proxy.py" <<'EOF'
import http.server, json, sys, urllib.request
UP = sys.argv[2]
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('content-length', 0)); body = self.rfile.read(n)
        req = urllib.request.Request(UP, body, {"Content-Type": "application/json"})
        data = urllib.request.urlopen(req, timeout=30).read()
        try:
            j = json.loads(data); m = json.loads(body).get("method")
            if m == "getTokenSupply" and "result" in j:
                v = j["result"]["value"]; v["amount"] = str(int(v["amount"]) + 1)
                data = json.dumps(j).encode()
        except Exception:
            pass
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(data))); self.end_headers(); self.wfile.write(data)
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(('127.0.0.1', int(sys.argv[1])), H).serve_forever()
EOF
python3 "$BL/run/proxy.py" 8898 "$SOLRPC" &
PIDS="$PIDS $!"
nice -n 10 "$BL/bin/hlbridge" monitor -every 2s -config "$BL/bridge-monitor2.json" -state "$BL/agents/monitor2.json" -listen 127.0.0.1:9125 >"$BL/agents/monitor2.log" 2>&1 &
PIDS="$PIDS $!"
disagree() { status 9125 | python3 -c 'import json,sys; j=json.load(sys.stdin); a=j["alarms"]; assert "solana:providers" in a and not j["identity"], (a, j["identity"]); print("monitor 2:", a["solana:providers"][:120])'; }
wait_for 150 disagree || die "the second monitor did not alarm on the disagreeing provider: $(status 9125)"
disagree

say "the burn is parked after three deterministic failures (the credits flag), then GovDAO's unpause and the key's clear reopen both sides"
parked() { status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); p=[k for k in j["parked"] if k.startswith("process:")]; assert p, j["parked"]; print("parked:", p[0][:30], "…, retried hourly;", j["parked"][p[0]]["err"][:80])'; }
LEFT=$((TPARK + 260 - $(date +%s))); [ "$LEFT" -gt 0 ] || LEFT=1
wait_for "$LEFT" parked || die "the burn did not park: $(status 9121)"
parked
grep -q '"key":"parked:process:' "$BL/alarms.log" || die "no parked alarm reached the sink"
propose_and_execute unpause "hlmailbox.NewUnpauseRequest(cross(cur), \"$NS/hlbubble\", \"credits\", 0)" "$NS/hlmailbox"
"$BL/bin/hlsol" flag -rpc "$SOLRPC" -key "$BL/keys/watch.json" -pausable "$(ppk pausable)" -admin "$(ppk admin)" -clear | tail -1
for g in g1; do "$BL/bin/hlsol" flag -rpc "$SOLRPC" -key "$BL/keys/$g.json" -pausable "$(ppk pausable)" -admin "$(ppk admin)" -clear | tail -1; done
# alice minted SEND, burned BACK and PARK toward gno.land, and now receives the send made under the flag
WANT=$((SEND - BACK - PARK + SEND2)); wait_for 180 is bal || die "alice has $(bal) BUBBLE on solana, want $WANT (the send made under the flag, after the unpause); see $BL/agents/relayer.log"
echo "alice holds $(bal) BUBBLE on solana: the send made under the flag delivered after the unpause"
unmirrored() { status 9122 | python3 -c 'import json,sys; j=json.load(sys.stdin); assert not j["sendsSet"] and not j["pausable"]["anyFlag"], j; print("the monitor cleared its sends flags")'; }
wait_for 60 unmirrored || die "the monitor did not clear its sends flags: $(status 9122)"
unmirrored
W=$(hlwatch)
! echo "$W" | grep -q 'credits by seat 5' || die "the gno credits flag still stands after the unpause: $W"
echo "the parked burn stays parked until its hourly retry (§5.7), in flight for the identity meanwhile"

say "a validator-set change on the gno chain: the client quarantines; a message dispatched before it is re-emitted"
# the updater is stopped so the send waits for a header; the set changes; the updater then presents the announcing
# header (the quarantine starts) and the head signed by the new set, and the message's event is before the current
# set's first height: a Reemit (G11's rule for a route message), with delivery paused by the quarantine
kill "$UPDATER" 2>/dev/null || true; wait "$UPDATER" 2>/dev/null || true
call hlbubble TransferRemote $SOL_DOMAIN "$(pk alice)" $SEND3
callpath gno.land/r/gnops/valopers Register "e2e-valoper" "e2e-second-validator-power-1-never-signs" "cloud" "$TEST1" "$VALOPER_PUB"
propose_and_execute_dao valset "proposal.NewValidatorProposalRequest(cross(cur), \"$TEST1\")" "gno.land/r/gnops/valopers/proposal"
vals() { curl -sf "http://$GNORPC/validators" | python3 -c 'import json,sys; j=json.load(sys.stdin); print(len(j["result"]["validators"]))'; }
WANT=2; wait_for 60 is vals || die "the chain's validator set did not change: $(vals) validators"
echo "the chain has $(vals) validators"
updater; UPDATER=$LASTPID
quarantined() { status 9122 | python3 -c 'import json,sys; j=json.load(sys.stdin); c=j["client"]; assert c["quarantineEnd"] > 0, c; print("the client: set from height", c["setFrom"], "quarantine until", c["quarantineEnd"], "trusted height", c["trustedHeight"])'; }
wait_for 180 quarantined || die "the client did not follow the set change: $(status 9122); see $BL/agents/updater.log"
quarantined
reemitted() { grep -q 're-emitted at height' "$BL/agents/relayer.log"; }
wait_for 150 reemitted || die "the relayer did not re-emit the message dispatched before the set change: $(tail -3 "$BL/agents/relayer.log")"
grep 're-emitted at height' "$BL/agents/relayer.log" | tail -1
quarantine_shown() { W=$(hlwatch); echo "$W" | grep -q 'quarantine until 20'; }
wait_for 60 quarantine_shown || die "hlwatch shows no quarantine: $(hlwatch)"
hlwatch | grep -o 'quarantine until [^,]*'
status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); l=j["last"].get("deliveries",""); assert "quarantine" in l, j["last"]; print("the relayer:", l)' || die "the relayer does not see the quarantine: $(status 9121)"

say "hlsol audit: a build from another config.rs, or another file beside it, FAILS (G20): -so at a corrupted copy, the wrong mailbox pkgpath and the wrong r/hlgov"
mkdir -p "$BL/so-wrong"; cp "$SBF"/*.so "$BL/so-wrong/"; printf 'x' >>"$BL/so-wrong/pausable_ism.so"
"$BL/bin/hlsol" audit -rpc "$SOLRPC" -keys "$BL/keys/programs" -so "$BL/so-wrong" -gno-rpc "http://$GNORPC" -mailbox-pkgpath "$NS/hlfees" -hlgov "$NS/hlfees" -deployer "$(pk payer)" >"$BL/audit-wrong.log" 2>&1 && die "the audit passed with a corrupted .so and the wrong realms"
grep -q 'hlsol audit: FAIL' "$BL/audit-wrong.log" || die "no FAIL line: $(tail -3 "$BL/audit-wrong.log")"
for want in "FAIL pausable ISM ELF sha256" "FAIL client ELF binds the mailbox pkgpath \"$NS/hlfees\"" "FAIL admin program ELF binds r/hlgov's address" "ok   admin program ELF binds the mailbox $(ppk mailbox)" "ok   client ELF sha256"; do
  grep -qF -- "$want" "$BL/audit-wrong.log" || die "the wrong audit lacks: $want"
done
grep -E '^FAIL' "$BL/audit-wrong.log" | cut -c1-140

say "hlsol audit: the setup against §5.8 (the day cap printed beside the expected lock it was compiled from)"
"$BL/bin/hlsol" audit -rpc "$SOLRPC" -keys "$BL/keys/programs" -so "$SBF" -gno-rpc "http://$GNORPC" -mailbox-pkgpath "$NS/hlmailbox" -hlgov "$NS/hlgov" -deployer "$(pk payer)" -expected-lock-gnot $EXPECTED_LOCK -expected-lock-bubble $EXPECTED_LOCK >"$BL/audit.log" 2>&1 || { grep -E '^FAIL|^hlsol audit' "$BL/audit.log"; die "hlsol audit failed"; }
grep -q 'hlsol audit: PASS' "$BL/audit.log" || die "no PASS line: $(tail -3 "$BL/audit.log")"
# the provider URL never printed whole (G25): the curl lines take $RPC, the header the host alone
! grep -q 'curl -s http' "$BL/audit.log" || die "the audit printed the provider URL in a curl line (G25): $(grep -m1 'curl -s http' "$BL/audit.log" | cut -c1-120)"
grep -q 'hlsol audit of http://127.0.0.1:8899 at ' "$BL/audit.log" || die "the audit's header does not name the provider's host: $(head -1 "$BL/audit.log")"
grep -q "pausable ISM day cap for BUBBLE against the expected lock $EXPECTED_LOCK it was compiled from: 100000000000, 10.0% of it" "$BL/audit.log" || die "the audit does not print the day cap beside the expected lock: $(grep 'day cap' "$BL/audit.log")"
grep 'day cap for BUBBLE' "$BL/audit.log" | cut -c1-160
for want in "ok   the admin program's upgrade authority, its own = none (want none" "ok   mailbox default ISM: $(ppk pausable) (want the pausable ISM)" "ok   mailbox owner: " "metadata update authority: none" "metadata pointer authority: none" "freeze authority: none (want none)" "interchain gas paymaster: none" "ok   admin program setup_done: 1" "ok   the pausable ISM's and the admin program's seat lists are the same: true" "ok   the client's set hash against /validators" "ok   the client's trusted hash against /block?height=" "ok   client state layout version 1 (want 1" "admin program cancelled range: numbers 1-1" \
  "ok   the three programs' clock factors agree: client 8, pausable ISM 8, admin program 8" "ok   the client's and the admin program's origin domains agree: $GNO_DOMAIN and $GNO_DOMAIN" "admin program hold history: no hold has ended" "pausable ISM mint limits for BUBBLE (token 1): 25000000000 a message, 100000000000 a day" "pausable ISM mint limits for GNOT (token 0): 25000000000 a message, 100000000000 a day" "admin program pending decision accounts: 1 (" "pending decision #2 kind 12 (set token 1's mint limits: 50000000000 a message, 200000000000 a day" "admin program predecessor: none (the launch deployment)" \
  "ok   r/hlgov's counter: last 4 (the next takes 5) against the admin program's last sequence number 4: gap 0" "ok   pausable ISM ELF binds the mailbox $(ppk mailbox)" "ok   client ELF binds the mailbox pkgpath \"$NS/hlmailbox\"" "ok   admin program ELF binds r/hlgov's address $("$BL/bin/hlkey" pkg "$NS/hlgov" | cut -d' ' -f1)" "ok   admin program ELF binds the GNOT token $(ppk gnot)"; do
  grep -qF -- "$want" "$BL/audit.log" || die "the audit lacks: $want"
done
# the set against the client's bound (G19): read at finalized, the client holds the set of 1 with the set of 2
# announced until the first header of the new set lands, then the set of 2 — either side of the switch is ok
grep -q 'ok   client validator set of [12] (next set announced: [02]) against the measured bound of about 50' "$BL/audit.log" || die "the audit lacks the set-size line against the bound: $(grep 'validator set of' "$BL/audit.log")"
# (the hold of the run never ends: a dip shorter than the 3-hour grace keeps it, so the history stays empty)
! grep -q '^FAIL' "$BL/audit.log" || die "the audit has a FAIL line: $(grep '^FAIL' "$BL/audit.log" | head -3)"
grep -c '^  rpc> curl' "$BL/audit.log" | sed 's/^/the audit printed /; s/$/ RPC calls to repeat by hand/'
grep -E '^(ok  |FAIL) ' "$BL/audit.log" | head -12
echo "..."; grep -E '^(ok  |FAIL) ' "$BL/audit.log" | tail -4

say "the governor's queued decision lands by a stranger's Execute: the relayer's own gas key applies hlfees decision 1 after its $REALM_DELAY s"
LEFT=$((TGOV + REALM_DELAY + 120 - $(date +%s))); [ "$LEFT" -gt 0 ] || LEFT=1
landed() { decision hlfees 1 | python3 -c 'import json,sys; a=json.load(sys.stdin); assert a["state"]=="applied" and a["applied"]>=a["due"], a; print("hlfees decision 1 (set treasurer) applied at", a["applied"], "(due", a["due"], ") —", a["note"])'; }
wait_for "$LEFT" landed || die "the governor's queued treasurer swap did not land: $(qrender hlfees:decisions.json)"
landed
grep -q 'hlfees decision #1 (set treasurer) applied' "$BL/agents/relayer.log" || die "the relayer's Execute did not land it: $(grep 'decision #1' "$BL/agents/relayer.log" | tail -2)"
# (GovDAO's own swap, decision 2, lands $REALM_DELAY s after its section; on a slow run it may have by now)
qrender "hlfees:json" | python3 -c '
import json,sys; j=json.load(sys.stdin); d={x["id"]:x for x in json.loads(sys.argv[1])}
want = sys.argv[3] if d.get(2, {}).get("state") == "applied" else sys.argv[2]
assert j["treasurer"] == want, (j["treasurer"], want); print("the treasurer is", j["treasurer"], "(decision 1 landed by the relayer'"'"'s Execute, a stranger'"'"'s hand; GovDAO'"'"'s decision 2", d.get(2, {}).get("state"), ")")' "$(qrender hlfees:decisions.json)" "$RELAYER_ADDR" "$TEST1" || die "the treasurer is not what the landed decisions name: $(qrender hlfees:json)"


say "hlwatch: green from nothing"
W=$(hlwatch)
echo "$W" | grep -q "^- governor: gno.land/r/gov/dao since height $YH ($TEST1 yielded)" || die "hlwatch: the governor line is not GovDAO since the yield: $W"
echo "$W" | grep -q 'matches gno.land'"'"'s' || die "hlwatch: the client's trusted header does not match the node's: $W"
echo "$W" | grep -q 'consistent with the client'"'"'s counter and running hash' || die "hlwatch: the record is not consistent: $W"
echo "$W" | grep -q 'HOLDS' || die "hlwatch: no identity holds: $W"
! echo "$W" | grep -v 'LIMITS MISMATCH' | grep -q 'FOREIGN\|MISMATCH\|COUNTERS DIFFER\|BROKEN\|NOT READ\|not read:\|VOID:\|NOT LAUNCHED\|UNCONFIRMED\|FORGED CREDIT\|RECIPIENT SWITCH\|REFUNDED\|IMPORTING\|MIGRAT' || die "hlwatch is not green: $W"
# the one alarm this run makes on purpose (AS4): hlbubble's per-message maximum stands at 50,000 (AN5's raise)
# against the pausable ISM's 25,000 until the kind 12 matures — a send between would strand, and both hlwatch and
# the monitor say so
echo "$W" | grep -q "LIMITS MISMATCH: BUBBLE's per-message maximum on gno.land, 50000000000, is above the pausable ISM's 25000000000" || die "hlwatch: no LIMITS MISMATCH line for BUBBLE's raised maximum (AS4): $W"
! echo "$W" | grep -q "LIMITS MISMATCH: GNOT" || die "hlwatch: a LIMITS MISMATCH line for GNOT, whose maxima agree: $W"
grep -q '"key":"limits:mismatch:BUBBLE"' "$BL/alarms.log" || die "the monitor's limits:mismatch:BUBBLE alarm (AS4) never reached the sink"
! grep -q '"key":"limits:mismatch:GNOT"' "$BL/alarms.log" || die "the monitor alarmed limits:mismatch:GNOT, whose maxima agree"
echo "the maxima: BUBBLE's 50,000 on gno.land above the ISM's 25,000 is alarmed (AS4), GNOT's agree"
# the day cap against the lock (AP6, C341): the run's lock is a few thousand BUBBLE under a 100,000 cap, below
# the expected first-week lock the config names, so the rule is quiet — hlwatch says why on each limits line and
# the monitor raises no mint:cap-ratio (it stood by construction from launch before C341)
echo "$W" | grep -q "cap ratio quiet: BUBBLE's day cap (100000000000) is not judged against its lock ([0-9]*) yet: the lock is below the expected first-week lock of $EXPECTED_LOCK" || die "hlwatch: no quiet cap-ratio line for BUBBLE under the expected lock (C341): $W"
! echo "$W" | grep -q 'CAP RATIO:' || die "hlwatch: a CAP RATIO line stands under the expected lock (C341): $W"
status 9122 | python3 -c 'import json,sys; j=json.load(sys.stdin); a=j["alarms"]; assert "mint:cap-ratio:BUBBLE" not in a and "mint:cap-ratio:GNOT" not in a, sorted(a); assert not j["stale"], j["stale"]; print("monitor: mint:cap-ratio quiet for both tokens under the expected lock (C341); the last poll gave a verdict; migrations:", j["migrations"])' || die "the monitor alarms the cap ratio under the expected lock, or is stale: $(status 9122)"
echo "$W" | grep -q "Solana supply $((SEND - BACK + BURST_TOTAL + SEND2 - PARK)) at slot" || die "hlwatch: BUBBLE's Solana supply is not $((SEND - BACK + BURST_TOTAL + SEND2 - PARK)): $W"
echo "$W" | grep -q "Solana supply $GSEND at slot" || die "hlwatch: GNOT's Solana supply is not $GSEND: $W"
echo "$W" | grep -q "set of [12] from height .* (the measured bound is about 50; an alarm from 40)" || die "hlwatch: no set size against the bound (G19): $W"
! echo "$W" | grep -q 'SET BOUND' || die "hlwatch alarms the set bound at 2 validators: $W"
echo "$W" | grep -q 'checked against this node (0 above its head, awaited)' || die "hlwatch: entries left unchecked above the head: $W"
echo "$W" | grep -q 'the hold history .*: no hold has ended' || die "hlwatch: no hold history line: $W"
echo "$W" | grep -q '  - #2 kind 12 (' || die "hlwatch: the kind 12 is not pending at the end: $W"
echo "$W" | grep -E '^- (BUBBLE|GNOT):|Solana supply|the client:|the header record|the admin program:|the hold history|^  - [0-9]: from|the mint limits|^  - (GNOT|BUBBLE):|CAP RATIO|LIMITS MISMATCH|cap ratio quiet' | cut -c1-220
echo "hlwatch build $("$BL/bin/hlwatch" version)"
! { grep '"level":"flag"' "$BL/alarms.log" | grep -q '"key":"identity\|"key":"header\|"key":"decision\|"key":"sources\|"key":"diverted'; } || die "a monitor flagged an identity, header, source or decision finding: $(grep '"level":"flag"' "$BL/alarms.log")"
say "PASS in $(($(date +%s) - T0))s"
