#!/bin/sh
# The mainnet launch rehearsed on one machine: bridge/launch/launch.sh, whole, against a local validator and gnodev
# — bridge/local/e2e.sh's chains (the validator with mainnet's feature set, gnodev with the packages at genesis, the
# BUBBLE token deployed at its live namespace g1leu8d2… as the e2e deploys it) — with a mainnet-shaped launch env: a
# FRESH governor key (neither test1 nor g1leu8d2…, so the nine packages deploy under a namespace of their own and
# the rewrite is proved), FACTOR 1, the compiled launch values, six random program keypairs, the plan's expected
# locks. The chain id stays the e2e's `dev` (GovDAO's dev init, r/gov/dao/init/v0, refuses any other but
# tendermint_test, and hlwatch reads an initialized GovDAO), so the realms derive the test domains, 1198419796 and
# 1399811150 — the domains reach every tool as flags and config, the same paths mainnet's numbers take, and stage.sh
# checks mainnet's (1198419788, 1399811149 on gnoland-1) against the realms' own derivation in the preflight. Then:
# two layouts launch.sh must refuse before it spends anything; the launch in its two halves, as the two keys' machines
# run it (PART=solana with no governor key: the deploy, the setup, the audit; then, on a copy of $OUT checked to hold
# no key, PART=gno with neither keypair nor the Solana CLI, and LAUNCH=no: the uploads and a stop before the
# Launch); a run without `resume`, refused (the packages are up); `resume`, which skips every step and makes the
# governor's Launch; `resume` again, which spends nothing; the machines from the configs
# launch.sh wrote (three signers, the updater, the primary relayer, monitor 1); 10 GNOT and 500 BUBBLE gno.land ->
# Solana and 200 BUBBLE back, credited as a claim the meters pay at the compiled values (the fee first, whole: a
# fresh recipient's meter holds it about an hour after the credit); the monitor's identity holding with the claim
# owed; hlwatch green; the site's
# config served by hlsite; and what it cost — the SOL the setup spent (the programs' rent, the floats, the state),
# the GNOT each upload and the Launch cost the governor (the storage deposit, the gas) — against the figures
# launch.sh's preflight takes. Everything it starts, it stops.
#
#   caffeinate -i -s nice -n 10 sh bridge/launch/rehearse.sh
#
# Needs what e2e.sh needs (go, cargo + the Solana CLI 3.0.14 in $SOLBIN, gnodev, gnokey, python3, rsync) and the
# Mac's budget: no e2e, validator or gnodev up, the load under 15, 10 GB free — else it refuses. Logs to $LOG; the
# last line `== PASS in Ns` decides. State in $RL, wiped each run.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
RL=${RL:-$HOME/.config/bubblerumble-jarvis/bridge-launch-rehearsal}
LOG=${LOG:-$HOME/.config/bubblerumble-jarvis/bridge-e2e-logs/launch-rehearsal.log}
SOLBIN=${SOLBIN:-$HOME/.local/share/solana-3.0.14/solana-release/bin}
SO=${SO:-$HOME/.config/bubblerumble-jarvis/bridge-ref/so}
EX=${EX:-$HOME/src/jaekwon/gno-fixes/examples}
export CARGO_TARGET_DIR=${CARGO_TARGET_DIR:-$HOME/.config/bubblerumble-jarvis/bridge-launch-target}
export PATH="$SOLBIN:$HOME/.cargo/bin:$PATH"
OLD=g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr # the BUBBLE token's live namespace
TEST1=g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5
MNEMONIC="source bonus chronic canvas draft south burst lottery vacant surface solve popular case indicate oppose farm nothing bullet exhibit title speed wink action roast"
CHAIN=dev
GNO_DOMAIN=1198419796 # the realms' DomainTest on any chain but gnoland-1
SOL_DOMAIN=1399811150 # and SolanaLocal
GNORPC=127.0.0.1:26657
SOLRPC=http://127.0.0.1:8899
ONE=1000000
SEND=$((500 * ONE)) # BUBBLE gno.land -> Solana (the minimum 200, the fee 100 on top)
FEE=$((100 * ONE))
BACK=$((200 * ONE)) # BUBBLE Solana -> gno.land: a claim, the fee its first installment
GSEND=$((10 * ONE)) # GNOT gno.land -> Solana (the minimum 10, the fee 5 on top)
GFEE=$((5 * ONE))
EXPECTED_LOCK=$((250000 * ONE)) # the plan's default (§11.11): the day cap a tenth, 25,000
T0=$(date +%s)

# the Mac's budget, before anything starts
die0() { printf 'rehearsal: %s\n' "$*" >&2; exit 1; }
! pgrep -f 'bridge/local/e2e[.]sh' >/dev/null || die0 "an e2e run is up: one local validator run at a time"
! pgrep -f 'solana-test-[v]alidator' >/dev/null || die0 "a solana-test-validator is up: one local validator run at a time"
! pgrep -f '[g]nodev local' >/dev/null || die0 "a gnodev is up"
load=$(uptime | sed 's/.*load averages*: *//' | tr -d ',' | cut -d' ' -f1 | cut -d. -f1)
[ "${load:-99}" -lt 15 ] || die0 "the load average is $load: wait for it to fall under 15"
free=$(df -k "$HOME" | awk 'NR==2 {print int($4 / 1048576)}')
[ "${free:-0}" -ge 10 ] || die0 "only ${free} GB free: 10 wanted"
mkdir -p "$(dirname "$LOG")"
exec 3>&1 >"$LOG" 2>&1
echo "rehearsal: logging to $LOG" >&3

say() { printf '\n== %s (%ss)\n' "$*" "$(($(date +%s) - T0))"; }
die() { printf 'FAIL: %s\n' "$*"; exit 1; }
PIDS=""
PASSED=""
cleanup() {
  exec >&3 2>&3
  for p in $PIDS; do kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
  if [ -n "$PASSED" ]; then echo "rehearsal: PASS in $(($(date +%s) - T0))s ($LOG)"; else echo "rehearsal: FAIL ($LOG: $(tail -1 "$LOG"))"; fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM
wait_for() { n=$1; shift; while ! "$@" >/dev/null 2>&1; do n=$((n - 1)); [ "$n" -gt 0 ] || return 1; sleep 1; done; }
echo "start $(date -u +%Y-%m-%dT%H:%M:%SZ) at $(cd "$REPO" && git log --oneline -1 | cut -c1-80); load $load; ${free} GB free"

rm -rf "$RL"
mkdir -p "$RL/bin" "$RL/keys/programs" "$RL/signers" "$RL/agents" "$RL/run" "$RL/realms"

say build
(cd "$REPO/bridge" && nice -n 10 go build -o "$RL/bin/" ./cmd/...)
[ -f "$SO/token.so" ] || python3 "$REPO/bridge/local/dump-programs.py" "$SO"

say "keys: a fresh governor, six random program keypairs, the seats, the signers, the floats"
for k in deployer relayer updater alice g1 g2 g3 g4 watch5 watch6 treasurer; do
  "$SOLBIN/solana-keygen" new --no-bip39-passphrase --silent -o "$RL/keys/$k.json" >/dev/null
done
for p in mailbox gnot bubble client pausable admin; do
  "$SOLBIN/solana-keygen" new --no-bip39-passphrase --silent -o "$RL/keys/programs/$p.json" >/dev/null
done
pk() { "$SOLBIN/solana-keygen" pubkey "$RL/keys/$1.json"; }
ppk() { "$SOLBIN/solana-keygen" pubkey "$RL/keys/programs/$1.json"; }
for i in 1 2 3; do "$RL/bin/hlkey" new "$RL/signers/s$i.json" >"$RL/signers/s$i.pub"; done
GK=$RL/gnokey   # the machines' keybase (the relayers' gas key, the watch keys) and test1, the users' wallet
GOVHOME=$RL/governor
mkdir -p "$GK" "$GOVHOME"
printf '%s\n\n\n' "$MNEMONIC" | gnokey add -recover -insecure-password-stdin -home "$GK" -quiet test1 >/dev/null 2>&1
for k in relayer watch5 watch6 g1 g2 g3 g4 treasurer; do printf '\n\n' | gnokey add -insecure-password-stdin -home "$GK" -quiet "$k" >/dev/null 2>&1; done
printf '\n\n' | gnokey add -insecure-password-stdin -home "$GOVHOME" -quiet governor >/dev/null 2>&1
addr() { gnokey list -home "$2" 2>/dev/null | sed -n "s/^.* $1 (local) - addr: \(g1[a-z0-9]*\).*/\1/p"; }
GOV=$(addr governor "$GOVHOME")
[ -n "$GOV" ] && [ "$GOV" != "$TEST1" ] && [ "$GOV" != "$OLD" ] || die "the governor's key: $GOV"
RELAYER_ADDR=$(addr relayer "$GK")
NSR=gno.land/r/$GOV
echo "the governor $GOV (a fresh key): the packages go up under gno.land/{p,r}/$GOV/"
echo "programs: mailbox $(ppk mailbox) gnot $(ppk gnot) bubble $(ppk bubble) client $(ppk client) pausable $(ppk pausable) admin $(ppk admin)"

say "gno chain: gnodev as $CHAIN, the BUBBLE token's packages and GovDAO at genesis (the bridge's go up by the launch)"
ST=$RL/gno
mkdir -p "$ST"
stage() { # stage SRC DST: a package's non-test files and its gnomod.toml
  mkdir -p "$2"
  for f in "$1"/*.gno; do case "$f" in *_test.gno | *_filetest.gno) ;; *) cp "$f" "$2/" ;; esac; done
  cp "$1/gnomod.toml" "$2/"
}
# the token's own p/ packages at its live namespace, as gnoland-1 has them — and none of the bridge's, which must
# come up under the governor's namespace or not at all
for p in checkpoint grc20votes governor; do stage "$REPO/realm/p/$p/v0" "$ST/p/$OLD/$p/v0"; done
stage "$REPO/realm/bubble" "$RL/realms/bubble"
for p in avl/v0 bptree/v0 ufmt/v0 cford32/v0 seqid/v0; do stage "$EX/gno.land/p/nt/$p" "$ST/p/nt/$p"; done
cat >"$RL/run/closure.py" <<'EOF'
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
        head = open(os.path.join(d, f)).read().split('\nfunc ', 1)[0]
        for m in imp.findall(head):
            if m not in seen: todo.append(m)
print('\n'.join(seen))
EOF
for p in $(python3 "$RL/run/closure.py" "$EX" gno.land/r/gov/dao gno.land/r/gov/dao/impl/v0 gno.land/r/gov/dao/memberstore/v0 gno.land/r/gov/dao/init/v0); do
  d=$ST/$(echo "$p" | sed 's|^gno.land/||')
  [ -d "$d" ] || stage "$EX/$p" "$d"
done
sed -i.bak "s|^const premineTo = address(\"[a-z0-9]*\")|const premineTo = address(\"$TEST1\")|" "$RL/realms/bubble/mint.gno" && rm -f "$RL/realms/bubble/mint.gno.bak"
grep -q "premineTo = address(\"$TEST1\")" "$RL/realms/bubble/mint.gno" || die "the premine swap"
(cd "$ST" && exec nice -n 10 gnodev local -chain-id "$CHAIN" -no-watch -no-web -empty-blocks -empty-blocks-interval 1 -extra-root "$ST" >"$RL/gnodev.log" 2>&1) &
PIDS="$PIDS $!"
wait_for 120 curl -sf "http://$GNORPC/status" || die "gnodev did not start"
# gnodev answers /status before its first reset (NODE_RESET) is over, and a transaction landing in the reset meets
# empty params ("too many signatures … limit: 0"): wait for a few blocks of the settled node
height() { curl -sf "http://$GNORPC/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"])'; }
settled() { [ "$(height)" -ge 5 ]; }
wait_for 120 settled || die "gnodev produced no blocks: $(tail -5 "$RL/gnodev.log")"
! grep -q "type check failed" "$RL/gnodev.log" || die "a package failed to load; see $RL/gnodev.log"
gnotx() { # gnotx WHAT ARGS...: one gnokey maketx from test1, refused unless gnokey says OK!
  what=$1; shift
  out=$(echo | gnokey "$@" -broadcast -chainid "$CHAIN" -remote "$GNORPC" -insecure-password-stdin -home "$GK" test1 2>&1) || die "$what: $out"
  echo "$out" | grep -q '^OK!' || die "$what: $out"
  echo "$what: $(echo "$out" | grep 'GAS USED' | tr -s ' ')"
}
send() { gnotx "send $2 to $1" maketx send -to "$1" -send "$2" -gas-fee 1000000ugnot -gas-wanted 2000000; }
call() { # call PKGPATH FUNC ARGS...: a MsgCall from test1
  pkg=$1; fn=$2; shift 2
  gnotx "$(basename "$pkg").$fn" maketx call -pkgpath "$pkg" -func "$fn" $(for a in "$@"; do printf -- '-args %s ' "$a"; done) -gas-fee 1000000ugnot -gas-wanted 100000000
}
callsend() { # callsend SEND PKGPATH FUNC ARGS...: a MsgCall from test1 carrying coins
  coins=$1; pkg=$2; fn=$3; shift 3
  gnotx "$(basename "$pkg").$fn (sending $coins)" maketx call -pkgpath "$pkg" -func "$fn" -send "$coins" $(for a in "$@"; do printf -- '-args %s ' "$a"; done) -gas-fee 1000000ugnot -gas-wanted 100000000
}
qeval() { gnokey query vm/qeval -data "$1" -remote "$GNORPC" 2>/dev/null | sed -n 's/^data: (\([-0-9]*\) .*/\1/p'; }
qrender() { gnokey query vm/qrender -data "$1" -remote "$GNORPC" 2>/dev/null | sed -n 's/^data: //p'; }
gnotx "addpkg gno.land/r/$OLD/bubble (the token, at its live namespace, as e2e.sh deploys it)" maketx addpkg -pkgpath "gno.land/r/$OLD/bubble" -pkgdir "$RL/realms/bubble" -gas-fee 1000000ugnot -gas-wanted 400000000
cat >"$RL/run/init_govdao.gno" <<EOF
package main

import govinit "gno.land/r/gov/dao/init/v0"

func main(cur realm) {
	govinit.InitWithUsers(cross(cur), "$TEST1")
}
EOF
out=$(echo | gnokey maketx run -gas-fee 1000000ugnot -gas-wanted 200000000 -broadcast -chainid "$CHAIN" -remote "$GNORPC" -insecure-password-stdin -home "$GK" test1 "$RL/run/init_govdao.gno" 2>&1) || die "GovDAO's init: $out"
echo "$out" | grep -q '^OK!' || die "GovDAO's init: $out"
send "$GOV" 1000000000ugnot
send "$RELAYER_ADDR" 100000000ugnot
send "$(addr watch5 "$GK")" 20000000ugnot
send "$(addr watch6 "$GK")" 20000000ugnot
echo "the governor $GOV holds 1,000 GNOT; the relayer's gas key 100; the watch keys 20 each"
for k in storage_price default_deposit syscla_pkgpath sysnames_pkgpath; do
  echo "gnodev's vm:p:$k: $(gnokey query "params/vm:p:$k" -remote "$GNORPC" 2>/dev/null | sed -n 's/^data: //p')"
done
echo "gnodev's auth/gasprice: $(gnokey query auth/gasprice -remote "$GNORPC" 2>/dev/null | sed -n '/^data:/,$p' | tr -d '\n ' | sed 's/^data://')"

say "solana validator (mainnet's feature set, as e2e.sh runs it)"
nice -n 10 "$SOLBIN/solana-test-validator" --reset --quiet --ledger "$RL/ledger" --rpc-port 8899 --limit-ledger-size 50000000 \
  --url https://api.mainnet-beta.solana.com --clone-feature-set \
  --clone-upgradeable-program TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb --clone ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL \
  --bpf-program noopb9bkMVfRPU8AsbpTUg8AQkHtKwMYZiFUjNRtMmV "$SO/noop.so" >"$RL/solana.log" 2>&1 &
PIDS="$PIDS $!"
wait_for 120 "$SOLBIN/solana" -u "$SOLRPC" cluster-version || die "solana validator did not start"
"$SOLBIN/solana" -u "$SOLRPC" airdrop 20 "$RL/keys/deployer.json" >/dev/null
for k in relayer alice; do "$SOLBIN/solana" -u "$SOLRPC" airdrop 10 "$RL/keys/$k.json" >/dev/null; done
for k in updater watch5 watch6; do "$SOLBIN/solana" -u "$SOLRPC" airdrop 1 "$RL/keys/$k.json" >/dev/null; done
echo "the deployer $(pk deployer) holds 20 SOL; the local rent: $("$SOLBIN/solana" rent 357789 -u "$SOLRPC" | tr -s ' ') for token.so's programdata"

say "the launch env (mainnet-shaped: the fresh governor, factor 1, the compiled launch values; chain $CHAIN, so domains $GNO_DOMAIN and $SOL_DOMAIN)"
# the four providers of the signers' rule are four spellings of the one local validator: A B, C D, A C — each in at
# most two signers, no pair shared; the monitors' pairs, A D and B C, are no signer's
A=http://127.0.0.1:8899; B=http://localhost:8899; C=http://127.0.0.1:8899/; D=http://localhost:8899/
cat >"$RL/launch.env" <<EOF
OUT=$RL/launch
GNO_RPC=http://$GNORPC
GNO_CHAIN_ID=$CHAIN
GNO_DOMAIN=$GNO_DOMAIN
GNO_GAS_PRICE_FLOOR=0.001
GOV_ADDR=$GOV
GOV_HOME=$GOVHOME
GOV_KEY=governor
BUBBLE_TOKEN=gno.land/r/$OLD/bubble
GUARDIAN1_GNO=$(addr g1 "$GK")
GUARDIAN1_SOL=$(pk g1)
GUARDIAN2_GNO=$(addr g2 "$GK")
GUARDIAN2_SOL=$(pk g2)
GUARDIAN3_GNO=$(addr g3 "$GK")
GUARDIAN3_SOL=$(pk g3)
GUARDIAN4_GNO=$(addr g4 "$GK")
GUARDIAN4_SOL=$(pk g4)
WATCH5_GNO=$(addr watch5 "$GK")
WATCH5_SOL=$(pk watch5)
WATCH6_GNO=$(addr watch6 "$GK")
WATCH6_SOL=$(pk watch6)
SIGNER1_PUB=$(cat "$RL/signers/s1.pub")
SIGNER1_URL=http://127.0.0.1:9111
SIGNER2_PUB=$(cat "$RL/signers/s2.pub")
SIGNER2_URL=http://127.0.0.1:9112
SIGNER3_PUB=$(cat "$RL/signers/s3.pub")
SIGNER3_URL=http://127.0.0.1:9113
SIGNER_THRESHOLD=2
TREASURER_GNO=$(addr treasurer "$GK")
TREASURER_SOL=$(pk treasurer)
EXPECTED_LOCK_GNOT=$EXPECTED_LOCK
EXPECTED_LOCK_BUBBLE=$EXPECTED_LOCK
SOL_RPC=$SOLRPC
SOL_DOMAIN=$SOL_DOMAIN
DEPLOYER=$RL/keys/deployer.json
PROGRAM_KEYS=$RL/keys/programs
SOLBIN=$SOLBIN
HYPERLANE_SO=$SO
FACTOR=1
ATA_FUND=250000000
AUTHORITY_FUND=10000000
PROVIDERS="signer1=$A,$B signer2=$C,$D signer3=$A,$C monitor1=$A,$D monitor2=$B,$C"
NODE_RPC=http://$GNORPC
GNO_SOURCES=http://$GNORPC
MACHINE_GNOKEY=$(command -v gnokey)
MACHINE_GNOKEY_HOME=$GK
RELAYER_GNO_KEY=relayer
RELAYER_GNO_ADDR=$RELAYER_ADDR
RELAYER_SOL_KEY=$RL/keys/relayer.json
UPDATER_SOL_PUB=$(pk updater)
UPDATER_SOL_KEY=$RL/keys/updater.json
WATCH5_GNO_KEY=watch5
WATCH5_SOL_KEY=$RL/keys/watch5.json
WATCH6_GNO_KEY=watch6
WATCH6_SOL_KEY=$RL/keys/watch6.json
ALARM_WEBHOOK=http://127.0.0.1:9130/alarm
SITE_GNO_RPC=http://$GNORPC
SITE_SOL_RPC=$SOLRPC
SITE_EXPLORER=https://explorer.solana.com
SITE_CLUSTER=custom
LAUNCH=yes
EOF
grep -E '^(GOV_ADDR|PROVIDERS|GNO_CHAIN_ID|FACTOR|EXPECTED_LOCK_GNOT)=' "$RL/launch.env"
govbal() { gnokey query "bank/balances/$GOV" -remote "$GNORPC" 2>/dev/null | sed -n 's/^data: "\([0-9]*\)ugnot"$/\1/p'; }
solbal() { "$SOLBIN/solana" balance "$1" --lamports -u "$SOLRPC" | sed -n 's/^\([0-9]*\) lamports$/\1/p'; }

say "launch.sh refuses a layout the config check refuses, before it spends anything"
G0=$(govbal); S0=$(solbal "$(pk deployer)")
refuse() { # refuse WHAT PROVIDERS PATTERN
  sed "s|^PROVIDERS=.*|PROVIDERS=\"$2\"|" "$RL/launch.env" >"$RL/launch-bad.env"
  rc=0; sh "$HERE/launch.sh" "$RL/launch-bad.env" >"$RL/launch-bad.log" 2>&1 || rc=$?
  [ "$rc" -ne 0 ] || die "launch.sh took $1"
  grep -q "$3" "$RL/launch-bad.log" || die "launch.sh did not refuse $1 with '$3': $(tail -3 "$RL/launch-bad.log")"
  echo "refused, $1: $(grep 'FAIL' "$RL/launch-bad.log" | tail -1 | cut -c1-220)"
}
refuse "a pair shared by two signers" "signer1=$A,$B signer2=$C,$D signer3=$B,$A monitor1=$A,$D monitor2=$B,$C" "is shared by"
refuse "a monitor on a signer's pair" "signer1=$A,$B signer2=$C,$D signer3=$A,$C monitor1=$C,$D monitor2=$B,$C" "monitor1's pair is a signer's"
[ "$(govbal)" = "$G0" ] && [ "$(solbal "$(pk deployer)")" = "$S0" ] || die "a refused launch spent something"
echo "nothing spent: the governor holds $G0 ugnot, the deployer $S0 lamports"

say "the deployer's machine: launch.sh PART=solana — the deploy, the setup, the audit — with no governor key there"
mkdir -p "$RL/no-governor"
sed 's/^PART=.*//; s|^GOV_HOME=.*|GOV_HOME='"$RL"'/no-governor|' "$RL/launch.env" >"$RL/launch-solana.env"
echo "PART=solana" >>"$RL/launch-solana.env"
rc=0; sh "$HERE/launch.sh" "$RL/launch-solana.env" >"$RL/launch1.log" 2>&1 || rc=$?
cat "$RL/launch1.log"
[ "$rc" -eq 0 ] || die "launch.sh PART=solana failed (exit $rc)"
grep -q '^== launch: DONE (the Solana half)' "$RL/launch1.log" || die "the Solana half did not finish"
[ "$(govbal)" = "$G0" ] || die "the Solana half spent the governor's GNOT"
qrender "$NSR/hlmailbox:json" | grep -q '"live"' && die "the Solana half put a realm up"
echo "the Solana half: the programs deployed and audited, the governor's GNOT untouched, no realm up"

say "the governor's machine: \$OUT copied (no key in it), launch.sh PART=gno with LAUNCH=no — the uploads, then a stop before the Launch (§6.6: the checklist first)"
cp -R "$RL/launch" "$RL/launch-gov"
python3 - "$RL/launch-gov" "$RL"/keys/*.json "$RL"/keys/programs/*.json "$RL"/signers/*.json <<'EOF' || die "a key is in the copied \$OUT"
import json, os, sys
out, keys = sys.argv[1], sys.argv[2:]
secrets = []  # (file, secret text): a Solana keypair's byte list, an hlkey file's long string values
for k in keys:
    raw = open(k).read()
    j = json.loads(raw)
    if isinstance(j, list):
        secrets.append((k, raw.strip()[:60]))
    else:
        secrets += [(k, v) for v in j.values() if isinstance(v, str) and len(v) >= 32]
hits = []
for root, dirs, files in os.walk(out):
    dirs[:] = [d for d in dirs if d != "bin"]  # the tools, built from the repo
    for f in files:
        data = open(os.path.join(root, f), "rb").read()
        hits += [(os.path.join(root, f), k) for k, s in secrets if s.encode() in data]
if hits:
    print("FAIL: keys in the copied $OUT:", hits)
    sys.exit(1)
print("no key in the copied $OUT: %d secrets of %d key files searched" % (len(secrets), len(keys)))
EOF
sed 's/^PART=.*//; s/^LAUNCH=.*//; s|^OUT=.*|OUT='"$RL"'/launch-gov|; s|^DEPLOYER=.*|DEPLOYER='"$RL"'/nowhere/deployer.json|; s|^PROGRAM_KEYS=.*|PROGRAM_KEYS='"$RL"'/nowhere|; s|^SOLBIN=.*|SOLBIN='"$RL"'/nowhere|; s|^HYPERLANE_SO=.*|HYPERLANE_SO='"$RL"'/nowhere|' "$RL/launch.env" >"$RL/launch-gno.env"
printf 'PART=gno\nLAUNCH=no\n' >>"$RL/launch-gno.env"
NOSOL=$(echo "$PATH" | tr ':' '\n' | grep -v 'solana-release' | paste -sd: -) # and no Solana CLI on its PATH
rc=0; env PATH="$NOSOL" sh "$HERE/launch.sh" "$RL/launch-gno.env" >"$RL/launch1b.log" 2>&1 || rc=$?
cat "$RL/launch1b.log"
[ "$rc" -eq 0 ] || die "launch.sh PART=gno failed (exit $rc)"
grep -q '^== launch: DONE' "$RL/launch1b.log" || die "launch.sh did not finish"
grep -qF 'the Solana side passes its audit: nothing to set up (skipped)' "$RL/launch1b.log" || die "the gno half did not find the Solana side set up"
grep -q '^LAUNCH=no: stopping before the Launch' "$RL/launch1b.log" || die "launch.sh did not stop before the Launch"
OUT=$RL/launch-gov
CF=$OUT/configs
sed -i.bak 's|^OUT=.*|OUT='"$OUT"'|' "$RL/launch.env" && rm -f "$RL/launch.env.bak" # the runs after it, on one machine: $OUT the copy
qrender "$NSR/hlmailbox:json" | grep -q '"live":false' || die "the mailbox is live after a LAUNCH=no run: $(qrender "$NSR/hlmailbox:json" | cut -c1-200)"
echo "the realms are up and not live: the mailbox renders \"live\":false until the governor's Launch"

say "launch.sh again without resume: refused (the packages are up), nothing spent"
G1=$(govbal); S1=$(solbal "$(pk deployer)")
rc=0; sh "$HERE/launch.sh" "$RL/launch.env" >"$RL/launch2.log" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || die "a second launch without resume was taken"
grep -q "is on the chain already: a launch that stopped is resumed" "$RL/launch2.log" || die "the second launch was not refused for its uploaded packages: $(tail -3 "$RL/launch2.log")"
[ "$(govbal)" = "$G1" ] && [ "$(solbal "$(pk deployer)")" = "$S1" ] || die "the refused launch spent something"
echo "refused: $(grep 'FAIL' "$RL/launch2.log" | tail -1 | cut -c1-200)"

say "launch.sh resume with LAUNCH=yes: every step reads the chain and skips, then the governor's Launch"
rc=0; sh "$HERE/launch.sh" "$RL/launch.env" resume >"$RL/launch3.log" 2>&1 || rc=$?
[ "$rc" -eq 0 ] || { tail -20 "$RL/launch3.log"; die "launch.sh resume failed (exit $rc)"; }
for want in "the Solana side passes its audit: nothing to set up (skipped)" "== launch: DONE"; do
  grep -qF -- "$want" "$RL/launch3.log" || die "the resumed launch lacks: $want"
done
[ "$(grep -c 'on the chain already, its files the stage.s (skipped)' "$RL/launch3.log")" = 9 ] || die "the resumed launch did not skip the nine uploads: $(grep '^addpkg' "$RL/launch3.log")"
grep -q '^the Launch: height' "$RL/launch3.log" || die "the resumed launch did not make the Launch: $(grep -A3 '^== (f)' "$RL/launch3.log")"
[ "$(solbal "$(pk deployer)")" = "$S1" ] || die "the resumed launch spent SOL"
grep -E 'skipped|preflight: PASS|^the Launch|^hlmailbox:' "$RL/launch3.log" | cut -c1-200
echo "the governor paid $((G1 - $(govbal))) ugnot for the Launch, the deployer nothing"

say "launch.sh resume again: every step skips, nothing spent"
G2=$(govbal)
rc=0; sh "$HERE/launch.sh" "$RL/launch.env" resume >"$RL/launch4.log" 2>&1 || rc=$?
[ "$rc" -eq 0 ] || { tail -20 "$RL/launch4.log"; die "the second resume failed (exit $rc)"; }
for want in "the Solana side passes its audit: nothing to set up (skipped)" "launched already (skipped)" "== launch: DONE"; do
  grep -qF -- "$want" "$RL/launch4.log" || die "the second resume lacks: $want"
done
[ "$(grep -c 'on the chain already, its files the stage.s (skipped)' "$RL/launch4.log")" = 9 ] || die "the second resume did not skip the nine uploads"
[ "$(govbal)" = "$G2" ] && [ "$(solbal "$(pk deployer)")" = "$S1" ] || die "the second resume spent something: governor $(govbal) (was $G2), deployer $(solbal "$(pk deployer)") (was $S1)"
grep -cE 'skipped' "$RL/launch4.log" | sed 's/^/steps skipped: /'
echo "nothing spent on the second resume"

say "the machines from the configs launch.sh wrote: three signers, the updater, the primary relayer, monitor 1"
cat >"$RL/run/sink.py" <<'EOF'
import http.server, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('content-length', 0)); b = self.rfile.read(n)
        open(sys.argv[2], 'ab').write(b + b'\n'); self.send_response(204); self.end_headers()
    def log_message(self, *a): pass
http.server.HTTPServer(('127.0.0.1', int(sys.argv[1])), H).serve_forever()
EOF
: >"$RL/alarms.log"
python3 "$RL/run/sink.py" 9130 "$RL/alarms.log" &
PIDS="$PIDS $!"
for i in 1 2 3; do
  nice -n 10 "$RL/bin/hlbridge" signer -every 1s -config "$CF/signer-$i.json" -key "$RL/signers/s$i.json" -store "$RL/agents/s$i" -listen "127.0.0.1:911$i" -status-listen "127.0.0.1:914$i" >"$RL/agents/s$i.log" 2>&1 &
  PIDS="$PIDS $!"
done
nice -n 10 "$RL/bin/hlbridge" updater -every 2s -interval 20s -config "$CF/updater.json" -state "$RL/agents/updater.json" -listen 127.0.0.1:9123 >"$RL/agents/updater.log" 2>&1 &
PIDS="$PIDS $!"
nice -n 10 "$RL/bin/hlbridge" relayer -every 1s -role primary -config "$CF/relayer-primary.json" -state "$RL/agents/relayer.json" -listen 127.0.0.1:9121 -gno-gas 60000000 >"$RL/agents/relayer.log" 2>&1 &
PIDS="$PIDS $!"
nice -n 10 "$RL/bin/hlbridge" monitor -every 2s -config "$CF/monitor-1.json" -state "$RL/agents/monitor.json" -listen 127.0.0.1:9122 >"$RL/agents/monitor.log" 2>&1 &
PIDS="$PIDS $!"
status() { curl -sf "http://127.0.0.1:$1/v1/status"; }
for i in 1 2 3; do signing_i() { status "914$i" | grep -q '"signing":true'; }; wait_for 90 signing_i || die "signer $i is not signing: $(status "914$i"); see $RL/agents/s$i.log"; done
echo "the three signers sign: $(status 9141 | python3 -c 'import json,sys; j=json.load(sys.stdin); print(j["cut"])')"

say "gno.land -> Solana: 500 BUBBLE (+100 fee) and 10 GNOT (+5 fee) to alice, under the light client"
ROUTE=$("$RL/bin/hlkey" pkg "$NSR/hlbubble" | cut -d' ' -f1)
FEES_ADDR=$("$RL/bin/hlkey" pkg "$NSR/hlfees" | cut -d' ' -f1)
B0=$(qeval "gno.land/r/$OLD/bubble.BalanceOf(\"$TEST1\")")
call "gno.land/r/$OLD/bubble" Approve "$ROUTE" $((SEND + FEE))
call "$NSR/hlbubble" TransferRemote $SOL_DOMAIN "$(pk alice)" $SEND
[ "$(qeval "$NSR/hlbubble.Locked()")" = "$SEND" ] || die "BUBBLE locked $(qeval "$NSR/hlbubble.Locked()"), want $SEND"
callsend "$((GSEND + GFEE))ugnot" "$NSR/hlgnot" TransferRemote $SOL_DOMAIN "$(pk alice)" $GSEND
[ "$(qeval "$NSR/hlgnot.Locked()")" = "$GSEND" ] || die "GNOT locked $(qeval "$NSR/hlgnot.Locked()"), want $GSEND"
bal() { "$RL/bin/hlsol" balance -rpc "$SOLRPC" -token "$(ppk "$1")" -owner "$(pk alice)"; }
is() { [ "$("$@" | tail -1)" = "$WANT" ]; }
WANT=$SEND; wait_for 240 is bal bubble || die "alice holds $(bal bubble) BUBBLE on Solana, want $WANT; see $RL/agents/relayer.log and updater.log"
WANT=$GSEND; wait_for 240 is bal gnot || die "alice holds $(bal gnot) GNOT on Solana, want $WANT; see $RL/agents"
echo "alice holds $(bal bubble) BUBBLE and $(bal gnot) GNOT on Solana; the updater: $(status 9123 | python3 -c 'import json,sys; j=json.load(sys.stdin); print(j["submits"], "headers submitted, trusted height", j["trustedHeight"])')"

say "Solana -> gno.land: 200 BUBBLE back to test1, credited as a claim the meters pay at the launch values"
"$RL/bin/hlsol" send -rpc "$SOLRPC" -payer "$RL/keys/alice.json" -token "$(ppk bubble)" -mailbox "$(ppk mailbox)" -gno-domain $GNO_DOMAIN -to "$TEST1" -amount $BACK -config "$CF/monitor-1.json" | grep '^sent'
credited() { qrender "$NSR/hlbubble:json" | python3 -c 'import json,sys; j=json.load(sys.stdin); assert j["claims"] == 1 and j["openClaims"] == 1 and j["owed"] == '"$BACK"' and j["paidTotal"] == 0, j'; }
wait_for 240 credited || die "the burn was not credited as a claim of $BACK: $(qrender "$NSR/hlbubble:json" | cut -c1-400); see $RL/agents/relayer.log and the signers'"
WANT=$((SEND - BACK)); is bal bubble || die "alice holds $(bal bubble) BUBBLE after the burn, want $WANT"
gbal() { qeval "gno.land/r/$OLD/bubble.BalanceOf(\"$TEST1\")"; }
[ "$(gbal)" = $((B0 - SEND - FEE)) ] || die "test1 holds $(gbal) BUBBLE: the claim paid before its recipient's meter held the fee"
[ "$(qeval "$NSR/hlbubble.Locked()")" = "$SEND" ] || die "BUBBLE locked $(qeval "$NSR/hlbubble.Locked()"), want $SEND (the claim unpaid)"
# the payout at the compiled values (§5.4): a recipient's meter, a quarter of the day cap, starts empty at its first
# credit, and the fee is a claim's first installment, whole — e2e.sh pays at once only because it raises the cap
# floor, a test-only swap the launch never makes
qrender "$NSR/hlbubble:json" | python3 -c '
import json,sys; j=json.load(sys.stdin); one=1e6
rate = j["cap"] / 4 / 86400  # a recipient meter, base units a second
print("credited: claim 1 of %d BUBBLE to test1, owed %d, the lock %d, paid 0; the day cap %d (the floor %d), a recipient meter %d a day" % (j["owed"] / one, j["owed"] / one, j["lock"] / one, j["cap"] / one, j["capFloor"] / one, j["cap"] / 4 / one))
print("the payout: the %d BUBBLE fee, the first installment, whole, once the meter holds it — %.0f minutes after the credit; the %d BUBBLE after it by %.0f minutes (the relayers call Claim continuously)" % (j["fee"] / one, j["fee"] / rate / 60, (j["owed"] - j["fee"]) / one, j["owed"] / rate / 60))'
status 9121 | python3 -c 'import json,sys; j=json.load(sys.stdin); print("the relayer claims:", json.dumps(j.get("claims")))'
echo "locked: BUBBLE $(qeval "$NSR/hlbubble.Locked()") (owed $BACK of it), GNOT $(qeval "$NSR/hlgnot.Locked()"); test1 holds $(gbal) BUBBLE on gno.land, alice $(bal bubble) on Solana"

say "the monitor's identity holds, no flag"
identity_ok() { status 9122 | python3 -c '
import json,sys; j=json.load(sys.stdin)
b=j["identity"]["BUBBLE"]; g=j["identity"]["GNOT"]
assert b["ok"] and b["lock"] == '"$SEND"' and b["supply"] == '"$((SEND - BACK))"' and b["owed"] == '"$BACK"' and b["gap"] == 0, b
assert g["ok"] and g["lock"] == g["supply"] == '"$GSEND"' and g["gap"] == 0, g
r=j["record"]; assert r["count"] >= 2 and r["mismatch"] is None and r["checked"] == r["count"], r
print("monitor:", b["text"]); print("monitor:", g["text"]); print("monitor: record", r["count"], "headers rebuilt and checked, no mismatch; cut", j["cut"])'; }
wait_for 120 identity_ok || die "the monitor's identity lines are not right: $(status 9122)"
identity_ok
! grep -q '"level":"flag"' "$RL/alarms.log" || die "a flag was raised: $(cat "$RL/alarms.log")"
for i in 1 2 3; do signing_i() { status "914$i" | grep -q '"signing":true'; }; wait_for 60 signing_i || die "signer $i stopped: $(status "914$i")"; done
echo "no flag; the three signers sign; alarms: $(wc -l <"$RL/alarms.log" | tr -d ' ')"

say "hlwatch: green, the governor until the yield"
W=$("$RL/bin/hlwatch" status -config "$CF/monitor-1.json" -node "http://$GNORPC" -state "$RL/agents/hlwatch.json") || die "hlwatch: $W"
echo "$W" | grep -q "^- governor: $GOV (until the yield) — it alone decides, by Govern from its own key" || die "hlwatch does not show the governor: $W"
echo "$W" | grep -q 'HOLDS' || die "hlwatch: no identity holds: $W"
echo "$W" | grep -q "matches gno.land's" || die "hlwatch: the client's trusted header does not match the node's: $W"
! echo "$W" | grep -q 'FOREIGN\|MISMATCH\|COUNTERS DIFFER\|BROKEN\|NOT READ\|not read:\|VOID:\|NOT LAUNCHED\|UNCONFIRMED\|FORGED CREDIT\|RECIPIENT SWITCH\|REFUNDED\|IMPORTING\|MIGRAT\|CAP RATIO:' || die "hlwatch is not green: $W"
echo "$W" | grep -q "Solana supply $((SEND - BACK)) at slot" || die "hlwatch: BUBBLE's Solana supply is not $((SEND - BACK)): $W"
echo "$W" | grep -q "Solana supply $GSEND at slot" || die "hlwatch: GNOT's Solana supply is not $GSEND: $W"
echo "$W" | grep -E '^- governor:|^- (BUBBLE|GNOT):|Solana supply|the client:|the header record|the admin program:|cap ratio quiet' | cut -c1-220
echo "hlwatch build $("$RL/bin/hlwatch" version)"

say "the site: hlsite serves the launch's site.json against the deployment"
"$RL/bin/hlsite" -config "$CF/site.json" -site "$REPO/bridge/site" -listen 127.0.0.1:9195 >"$RL/agents/hlsite.log" 2>&1 &
PIDS="$PIDS $!"
wait_for 30 curl -sf http://127.0.0.1:9195/api/chains || die "hlsite did not serve /api/chains: $(cat "$RL/agents/hlsite.log")"
curl -sf http://127.0.0.1:9195/api/chains | python3 -c '
import json,sys; j=json.load(sys.stdin); s=json.dumps(j)
for want in sys.argv[1:]: assert want in s, (want, s[:400])
print("hlsite /api/chains: the tokens", [t["symbol"] for c in j["chains"] for t in c["tokens"]], "under", sys.argv[1])' "$NSR/hlmailbox" "$(ppk gnot)" "$(ppk bubble)" || die "hlsite's chains: $(curl -s http://127.0.0.1:9195/api/chains | cut -c1-300)"
sitestatus() { curl -sf http://127.0.0.1:9195/api/solana/status | python3 -c 'import json,sys; j=json.load(sys.stdin); s=json.dumps(j); assert "BUBBLE" in s and "GNOT" in s, s[:300]; print("hlsite /api/solana/status:", len(s), "bytes, both tokens")'; }
wait_for 30 sitestatus || die "hlsite's status: $(curl -s http://127.0.0.1:9195/api/solana/status | cut -c1-300)"
sitestatus

say "what it cost: the governor's GNOT per upload and the deployer's SOL, against the preflight's figures"
python3 - "$OUT/costs" "$HERE/launch.sh" "$REPO" <<'EOF'
import glob, json, os, re, sys, urllib.request
costs, launch, repo = sys.argv[1:4]
src = open(launch).read()
measured = dict((k, (int(b), int(g))) for k, b, g in re.findall(r"(\S+):(\d+):(\d+)", re.search(r'^MEASURED_GNO="([^"]*)"', src, re.M).group(1)))
state_measured = int(re.search(r"^MEASURED_STATE_LAMPORTS=(\d+)", src, re.M).group(1))
order = ["p/hyperlane/v0", "p/decisions/v0", "p/hlroute/v0", "r/hlmailbox", "r/hlverifier", "r/hlfees", "r/hlgnot", "r/hlbubble", "r/hlgov", "launch"]
rows, short, total = {}, [], 0
for f in glob.glob(os.path.join(costs, "*.tsv")):
    parts = open(f).read().split()
    if parts[0] != "solana-setup":
        rows[parts[0]] = [int(x) for x in parts[1:]]
print("%-16s %12s %12s %10s %10s %12s %12s" % ("GNOT", "gas used", "gas wanted", "fee", "bytes", "deposit", "spent"))
line = []
for k in order:
    used, wanted, fee, nbytes, dep, spent = rows[k]
    total += spent
    print("%-16s %12d %12d %10.6f %10d %12.6f %12.6f" % (k, used, wanted, fee / 1e6, nbytes, dep / 1e6, spent / 1e6))
    mb, mg = measured[k]
    if nbytes > mb * 3 // 2 or used > mg * 3 // 2:
        short.append("%s: %d bytes, %d gas against the table's %d and %d" % (k, nbytes, used, mb, mg))
    line.append("%s:%d:%d" % (k, -(-nbytes * 11 // 10000) * 1000, -(-used * 11 // 100000000) * 10000000))
print("%-16s %12s %12s %10s %10s %12s %12.6f" % ("all", "", "", "", "", "", total / 1e6))
print('the measured line, 10%% over and rounded up, for launch.sh: MEASURED_GNO="%s"' % " ".join(line))
s = open(os.path.join(costs, "solana-setup.tsv")).read().split()
spent, prog, floats, state = (int(x) for x in s[1:])
print("SOL: the setup spent %.6f: %.6f the six programs' rent, %.6f the floats, %.6f the state accounts and fees (the table's %.6f)" % (spent / 1e9, prog / 1e9, floats / 1e9, state / 1e9, state_measured / 1e9))
if state > state_measured * 3 // 2:
    short.append("the state and fees: %d lamports against the table's %d" % (state, state_measured))
print("the measured state, 10%% over: MEASURED_STATE_LAMPORTS=%d" % (-(-state * 11 // 10000000) * 1000000))
sizes = {}
for n, f in (("mailbox", "mailbox.so"), ("gnot", "token.so"), ("bubble", "token.so"), ("client", "gno_light_client_ism.so"), ("pausable", "pausable_ism.so"), ("admin", "admin_program.so")):
    sizes[n] = os.path.getsize(os.path.join(os.path.dirname(costs), "so", f))
print("the ELFs as built: MEASURED_ELF=\"%s\"" % " ".join("%s:%d" % kv for kv in sizes.items()))
# the same six programs' rent at mainnet's rate (a read of the public RPC's rent; nothing is sent)
try:
    def rent(n):
        req = urllib.request.Request("https://api.mainnet-beta.solana.com", json.dumps({"jsonrpc": "2.0", "id": 1, "method": "getMinimumBalanceForRentExemption", "params": [n]}).encode(), {"Content-Type": "application/json"})
        return json.load(urllib.request.urlopen(req, timeout=30))["result"]
    main = sum(rent(sz + 45) + rent(36) for sz in sizes.values())
    print("at mainnet's rent (read now): the six programs %.6f SOL, so the setup about %.6f SOL with these floats and this state" % (main / 1e9, (main + floats + state) / 1e9))
except Exception as e:
    print("mainnet's rent could not be read: %s" % e)
if short:
    print("FAIL: the preflight's table is short of what the rehearsal measured: " + "; ".join(short))
    sys.exit(1)
print("the preflight's table covers every figure (each at most 150% of it)")
EOF

PASSED=1
printf '\n== PASS in %ss\n' "$(($(date +%s) - T0))"
