#!/bin/bash
# gno tests for the Ethereum deployment's realms (eth/realm/*), run STAGED as scripts/gnotest.sh runs the Solana
# deployment's: gno resolves a sibling package only under $GNOROOT/examples at its import path, so a private shadow of
# the toolchain's GNOROOT is built per run — every top-level entry symlinked, examples copied, then placed under it:
# the Solana tree's pure packages (realm/p/*: the three these realms import at their own paths — hyperlane, decisions,
# hlroute — and the BUBBLE token's governor, grc20votes and checkpoint), the BUBBLE token realm (realm/bubble, which
# eth/hlbubble and eth/hlfees import at its live path), and this tree's realms under gno.land/r/<ADDR>/eth/<name>.
# `gno test -v` runs in each realm, its output kept, and the shadow is removed. The toolchain is the pinned mainnet one
# (gno v1.2.0, kourt's `make toolchain` puts it in ~/.cache/gno-toolchains/v1.2.0/gno); the gno on PATH does not match
# its GNOROOT.
#   eth/scripts/gnotest.sh              every realm of eth/realm, in the order they import each other
#   eth/scripts/gnotest.sh hlgov        one of: hlmailbox hlverifier hlfees hlgnot hlbubble hlgov hlgnot2
#   GNOTEST_ARGS="-run Name" eth/scripts/gnotest.sh hlgnot
#   ETH_REALM_DIR=<dir> stages the realms from <dir> instead of eth/realm (a scratch copy, e.g. a mutant for a
#     negative control; the pure packages and the token realm still come from this tree), and GNOTEST_LOGS=<dir> keeps
#     each realm's full output there as <name>.log.
#   GNOTEST_LINT=1 also runs `gno lint` in each realm, against the same staged tree.
#   RENDER_GAS_MAX=<gas> sets the render-gas gate's bound (below; 1,000,000,000 by default).
#   The echo checker runs first, over the realms staged (eth/scripts/check-echoes.go, the round-4 audits' E4-GS1 and
#     E4-RL6: no panic, error or event copies a caller's string as it came — a byte that is not UTF-8 in a failed
#     transaction's error makes its block unprovable to Ethereum); it needs go (GO=<path>, else the one on PATH, else
#     /usr/local/go/bin/go), and GNOTEST_ECHOES=0 alone skips it.
# Each realm prints its last lines and a count: "passed N, failed M" from the --- PASS / --- FAIL lines of -v; then
# the render-gas gate: a test named TestRenderGas… renders one page the bridge's machines read, at its bound, and does
# nothing else, so the "--- GAS:" line -v prints for it is that page's cost in this harness (the round-3 realms
# audit's E3-RL1 and E3-RL7: nothing measured what a page costs, and a stranger's 50 KB message put msg/<nonce> past
# vm/qrender's 3,000,000,000 on gnoland-1). Every page the machines read has a probe (E4-RL2, E4-FC3): hlmailbox's
# json, msg/, msgs/, decisions.json/<from> and delivered/<id>; a route's json, waiting/<from> (the machines' read of
# the waiting claims, E4-RL1), claim/<id>, send/<id> and decisions.json/<from>; r/hlgov's json, decision/<n>, its page
# (hleth's audit reads the seats there) and decisions.json/<from>; r/hlfees' json and decisions.json/<from>. The
# harness is no node: it charges an object's load once and serves it from a cache after, where a node loads every
# object of every query, so for a page of many small records it counts as little as about a tenth of a node's gas
# (delivered/<id>'s factor is 9.55, E5-RT4; p/hlroute's claims walk: 766M here, out of gas past 3B on a v1.5.1 node,
# E4-RL1) and for a page of a few long
# strings about what a node does. So each probe carries its page's node factor (node_factor: the page's gas on a
# gno v1.5.1 node over the probe's figure here, measured on the same page, F15d's calibration in
# fold-r4/f15d.md), and the gate holds the probe's figure × its factor — the node's figure — at or under
# RENDER_GAS_MAX, a third of gnoland-1's 3B. The gate fails, too, when a probe has no factor (a new page is measured on
# a node before it is gated), when a probe a realm declares printed no gas (unless GNOTEST_ARGS holds a -run filter),
# when a probe it must declare (required_probes) is gone — the probes alone hold what byte-identical output cannot,
# such as strconv.Quote put back — and when a figure is no number.
# Then the migration gate (the round-5 realms audit's E5-RL1: a migration page of 500 records outgrew gnoland-1's
# block of 3,000,000,000, and a route whose migration stalls is frozen for good): a realm with migration probes
# (required_tx_probes: hlgnot's TestTxGasPull, the predecessor's half of a page; hlgnot2's TestTxGasAdopt, a page
# adopted whole) is run a second time with -run 'TestSetup$|TestTxGas' — TestSetup launches the route, TestTxGasSetup
# makes a migration of 2×pullPage open claims and takes its page 0, each probe then takes page 1, one transaction's
# work — and each probe's figure × its node factor (tx_factor: the costliest page a v1.5.1 node measured in a
# migration of 10,000 open claims over the probe's figure, E6-RL3) is held at or under TX_GAS_MAX (1,000,000,000 by
# default: a third of the block). The void gate the same (the round-6 realms audit's E6-RL1: a void at p/hlroute's
# bounds, 256 ids or 1,000 credits, outgrew the block): a realm with void probes (required_void_probes: hlgnot's
# TestTxVoidList, TestTxVoidRange, TestTxVoidRangeExecute — one transaction each at the realm's bounds, voids.gno — and
# a void's cure, the restore, TestTxVoidRestore and TestTxVoidRestoreExecute at maxRestoreIDs, waiting.gno) is
# run a third time with -run 'TestSetup$|TestTxVoid', each figure × its void_factor (the node's figure for the same
# transaction at 10,000 claims) held at or under TX_GAS_MAX. Each fails, too, when a probe has no factor, did not pass
# in its run, or is no longer declared; GNOTEST_TXGAS=0 skips both. In the whole suite the probes skip (the route has
# migrated already). Last, the tests a realm runs alone on a freshly launched route (required_alone: hlgnot's
# installments tests, E6-RL4; its void bounds, E6-RL1) are run a fourth time with -run 'TestSetup$|TestAlone', each held
# to passing.
set -euo pipefail
cd "$(dirname "$0")/../.."
# The two layouts this runs in: the private tree (realm/ is the Solana deployment's, eth/realm this one's) and the
# public gnolang/bridges (solana/realm, ethereum/realm).
if [ -d solana/realm ] && [ -d ethereum/realm ]; then SOL=solana; ETH=ethereum; else SOL=.; ETH=eth; fi
ADDR=g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr
GNO=${GNO:-$HOME/.cache/gno-toolchains/v1.2.0/gno}
[ -x "$GNO" ] || { echo "no pinned gno at $GNO: GOBIN=$(dirname "$GNO") go install github.com/gnolang/gno/gnovm/cmd/gno@v1.2.0"; exit 1; }
REAL=$("$GNO" env GNOROOT)
[ -d "$REAL/examples" ] || { echo "GNOROOT $REAL has no examples"; exit 1; }
SRC=${ETH_REALM_DIR:-$ETH/realm}
[ -d "$SRC" ] || { echo "no realm directory $SRC"; exit 1; }
REALMS="hlmailbox hlverifier hlfees hlgnot hlbubble hlgov hlgnot2"
RENDER_GAS_MAX=${RENDER_GAS_MAX:-1000000000}
case $RENDER_GAS_MAX in ''|*[!0-9]*) echo "RENDER_GAS_MAX is no number: $RENDER_GAS_MAX"; exit 2 ;; esac
TX_GAS_MAX=${TX_GAS_MAX:-1000000000}
case $TX_GAS_MAX in ''|*[!0-9]*) echo "TX_GAS_MAX is no number: $TX_GAS_MAX"; exit 2 ;; esac
# required_probes are the render-gas probes a realm must declare: a probe renamed or deleted fails the gate instead of
# going unread
required_probes() {
  case $1 in
    hlmailbox) echo "TestRenderGasMsgAtCap TestRenderGasMsgsAtCap TestRenderGasDecisionsPage TestRenderGasDecisionsFull TestRenderGasDecisionsLongest TestRenderGasMailboxJSON TestRenderGasDelivered" ;;
    hlgnot | hlgnot2) echo "TestRenderGasRouteJSON" ;;
    hlbubble) echo "TestRenderGasWaitingPage TestRenderGasRouteClaim TestRenderGasRouteSend TestRenderGasRouteJSON TestRenderGasRouteDecisions" ;;
    hlgov) echo "TestRenderGasGovPage TestRenderGasGovDecision TestRenderGasGovJSON TestRenderGasGovDecisions" ;;
    hlfees) echo "TestRenderGasFeesJSON TestRenderGasFeesDecisions" ;;
  esac
}
# node_factor is a probe's calibration in hundredths: its page's gas on a gno v1.5.1 node (the rl1 lane's gas-logging
# gnodev, 2026-10-15) over the probe's figure in this harness, for the same page — the probes' states made by the same
# code on the node — rounded up to the next 0.05 and never below 1.00; where the node's state is smaller than the
# probe's (hlmailbox's and r/hlgov's json), the larger of that and the ratio over a harness run of the node's own state.
# The comment is the node's figure the factor comes from.
node_factor() {
  case $1/$2 in
    hlmailbox/TestRenderGasMsgAtCap) echo 215 ;;         # node 16,978,039
    hlmailbox/TestRenderGasMsgsAtCap) echo 115 ;;        # node 178,264,560
    hlmailbox/TestRenderGasDecisionsPage) echo 110 ;;    # node 174,539,309
    hlmailbox/TestRenderGasDecisionsFull) echo 105 ;;    # node 391,544,218
    hlmailbox/TestRenderGasDecisionsLongest) echo 100 ;; # node 835,264,702 (the probe's 865M is its first load here)
    hlmailbox/TestRenderGasMailboxJSON) echo 100 ;;      # node 219,993,085 of a smaller state (226,277,203 here)
    hlmailbox/TestRenderGasDelivered) echo 955 ;;        # node 9,692,299
    hlbubble/TestRenderGasWaitingPage) echo 165 ;;       # node 218,673,791 (and 218,393,679 for its first page)
    hlbubble/TestRenderGasRouteClaim) echo 375 ;;        # node 19,402,091
    hlbubble/TestRenderGasRouteSend) echo 395 ;;         # node 19,033,612
    hlbubble/TestRenderGasRouteJSON) echo 360 ;;         # node 28,303,927
    hlbubble/TestRenderGasRouteDecisions) echo 115 ;;    # node 406,615,192
    hlgnot/TestRenderGasRouteJSON) echo 265 ;;           # node 23,618,369
    hlgnot2/TestRenderGasRouteJSON) echo 360 ;;          # not on the node: the routes' larger json factor (hlbubble's)
    hlgov/TestRenderGasGovPage) echo 210 ;;              # node 51,356,077
    hlgov/TestRenderGasGovDecision) echo 115 ;;          # node 56,485,807
    hlgov/TestRenderGasGovJSON) echo 195 ;;              # node 20,055,055 of a smaller state (10,321,107 here)
    hlgov/TestRenderGasGovDecisions) echo 110 ;;         # node 365,228,456
    hlfees/TestRenderGasFeesJSON) echo 505 ;;            # node 16,363,989
    hlfees/TestRenderGasFeesDecisions) echo 125 ;;       # node 88,692,609
  esac
}
# required_tx_probes are the migration gate's probes a realm must declare (TestTxGas…, its setup TestTxGasSetup aside)
required_tx_probes() {
  case $1 in
    hlgnot) echo "TestTxGasPull" ;;
    hlgnot2) echo "TestTxGasAdopt" ;;
  esac
}
# tx_factor is a migration probe's calibration in hundredths: the costliest page's gas on a gno v1.5.1 node (the rl1
# lane's gas-logging gnodev, 2026-10-16) in a migration of 10,000 open claims, each to a recipient of its own beside a
# send of its own, made by the same code, over the probe's figure here (a page of its own small state), rounded up to
# the next 0.05 — so the gate holds the page the node measured costliest in that backlog, not the probe's own (the
# round-6 realms audit's E6-RL3: a page's cost grows with the route, ≈+0.2B from 3,000 claims to 10,000 at 50 a page,
# which a factor of the probe's state could not see). The comment is the node's figure (fold-r6/f18b.md).
tx_factor() {
  case $1/$2 in
    hlgnot/TestTxGasPull) echo 960 ;;   # node 323,812,185 (page 0 of 800, the lock with it, pulled by a pull-only probe realm, its MsgCall in)
    hlgnot2/TestTxGasAdopt) echo 560 ;; # node 697,893,489 (page 256 of 800, adopted whole)
  esac
}
# required_void_probes are the void gate's probes a realm must declare (TestTxVoid…, its setups — any name ending in
# Setup: TestTxVoidSetup, TestTxVoidRestoreSetup — aside)
required_void_probes() {
  case $1 in
    hlgnot) echo "TestTxVoidList TestTxVoidRange TestTxVoidRangeExecute TestTxVoidRestore TestTxVoidRestoreExecute" ;;
  esac
}
# void_factor is a void probe's calibration in hundredths, as tx_factor's: the transaction's gas on a gno v1.5.1 node
# (2026-10-16) at 10,000 credited claims, each to a recipient of its own, three guardians' lists of 256 standing and
# the fourth's flag a range (for the list, the costlier four lists, the ids in the fourth's) — the probe's transaction,
# made afresh there by the same code — over the probe's figure here (its own state: 968 claims), rounded up to the
# next 0.05. The comment is the node's figure (fold-r6/f18b.md).
void_factor() {
  case $1/$2 in
    hlgnot/TestTxVoidList) echo 205 ;;         # node 958,798,080 (Govern of a void at once of 64 ids of the fourth list; of the third, three lists and a range standing, 923,571,264)
    hlgnot/TestTxVoidRange) echo 505 ;;        # node 930,787,627 (Govern of a range void at once of 100 credits, one a height)
    hlgnot/TestTxVoidRangeExecute) echo 835 ;; # node 790,482,574 (the Execute of a range void of 100 credits, one a height)
    hlgnot/TestTxVoidRestore) echo 195 ;;      # node 825,510,806 (Govern of a restore at once of 40 withheld claims; fold-r6/i4.md)
    hlgnot/TestTxVoidRestoreExecute) echo 270 ;; # node 623,235,508 (the Execute of a queued restore of 40 forged claims)
  esac
}
# required_alone are the tests a realm runs alone, on a route TestSetup has just launched (-run 'TestSetup$|TestAlone'):
# tests that need a ring of their own recipients and their meters at known levels, or more records than the whole
# suite's migration takes in one page (the round-6 realms audit's E6-RL4: the installments rule's turns and the bridge
# meter's half; E6-RL1: the void's bounds and the credit tally). Each must pass there; in the whole suite they skip.
required_alone() {
  case $1 in
    hlgnot) echo "TestAloneFeeEachTurn TestAloneBridgeMeter TestAloneVoidBounds" ;;
  esac
}
# alone REALM runs REALM's alone tests (required_alone, the prefix TestAlone) and holds each to passing.
alone() {
  local r=$1 want alog p
  want=$(required_alone "$r")
  [ -n "$want" ] || return 0
  alog="$LOGS/$r.alone.log"
  if ! (cd "$R/eth/$r" && GNOROOT="$ROOT" "$GNO" test -v -run 'TestSetup$|TestAlone' . >"$alog" 2>&1); then
    echo "    alone: the run failed: FAIL"; grep -E -- '^--- FAIL|^panic: ' "$alog" | head -3 | cut -c1-200 | sed 's/^/      /'; rc=1
  fi
  for p in $want; do
    if grep -q -- "^--- PASS: $p " "$alog"; then echo "    alone: $p ok"; else echo "    alone: $p did not pass: FAIL"; rc=1; fi
  done
}
# txgate GATE PREFIX REQUIRED FACTOR REALM runs REALM's suite a second time with -run 'TestSetup$|PREFIX' and holds each
# PREFIX… probe it declares (its PREFIXSetup aside) — every one REQUIRED names, each passed — at its figure × its node
# factor (FACTOR) under TX_GAS_MAX.
txgate() {
  local gate=$1 prefix=$2 req=$3 fac=$4 r=$5 txp txlog declared measured p g f n verdict
  txp=$($req "$r")
  [ -n "$txp" ] || return 0
  txlog="$LOGS/$r.$prefix.log"
  if ! (cd "$R/eth/$r" && GNOROOT="$ROOT" "$GNO" test -v -run "TestSetup\$|$prefix" . >"$txlog" 2>&1); then
    echo "    $gate gate: the probes' run failed: FAIL"; grep -E -- '^--- FAIL|^panic: ' "$txlog" | head -3 | cut -c1-200 | sed 's/^/      /'; rc=1
  fi
  declared=$(grep -ho "^func $prefix[A-Za-z0-9_]*" "$R/eth/$r"/*_test.gno 2>/dev/null | sed 's/^func //' | grep -v 'Setup$' | sort -u || true)
  for p in $txp; do
    case " $(echo $declared) " in *" $p "*) ;; *) echo "    $gate gas: $p is no longer declared: FAIL"; rc=1 ;; esac
  done
  measured=$(awk -v pre="^$prefix" '/^=== RUN /{n=$3} /^--- GAS:/{ if (n ~ pre) print n, $3; n="" }' "$txlog")
  for p in $declared; do
    if ! grep -q -- "^--- PASS: $p " "$txlog"; then echo "    $gate gas: $p did not pass in the probes' run: FAIL"; rc=1; continue; fi
    g=$(echo "$measured" | awk -v p="$p" '$1 == p {print $2; exit}')
    case $g in ''|*[!0-9]*) echo "    $gate gas: $p printed no gas figure ($g): FAIL"; rc=1; continue ;; esac
    f=$($fac "$r" "$p")
    case $f in ''|*[!0-9]*) echo "    $gate gas: $p $g here, and no node factor (measure its transaction on a node first): FAIL"; rc=1; continue ;; esac
    n=$((g * f / 100))
    if [ "$n" -gt "$TX_GAS_MAX" ]; then verdict=FAIL; rc=1; else verdict=ok; fi
    echo "    $gate gas: $p $g here × $((f / 100)).$(printf %02d $((f % 100))) = $n on a node (max $TX_GAS_MAX) $verdict"
  done
}
want=${1:-all}
if [ "$want" != all ]; then
  case " $REALMS " in *" $want "*) ;; *) echo "unknown realm $want: one of $REALMS"; exit 2 ;; esac
fi
ROOT=$(mktemp -d "${TMPDIR:-/tmp}/eth-gnoroot.XXXXXX")
trap 'chmod -R u+w "$ROOT" 2>/dev/null; rm -rf "$ROOT"' EXIT
for e in "$REAL"/* "$REAL"/.[!.]*; do n=$(basename "$e"); [ "$n" = examples ] || [ "$n" = .git ] || ln -s "$e" "$ROOT/$n"; done
cp -R "$REAL/examples" "$ROOT/examples"; chmod -R u+w "$ROOT/examples"
P="$ROOT/examples/gno.land/p/$ADDR"; R="$ROOT/examples/gno.land/r/$ADDR"
# the Solana tree's pure packages, every one (the realms import three; the token realm the rest), test files left out
for d in "$SOL"/realm/p/*/v0; do
  p=$(basename "$(dirname "$d")")
  mkdir -p "$P/$p/v0"; cp "$d"/gnomod.toml "$P/$p/v0/"
  for f in "$d"/*.gno; do case $f in *_test.gno|*_filetest.gno) ;; *) cp "$f" "$P/$p/v0/" ;; esac; done
done
# the BUBBLE token realm at its live path, test files left out
mkdir -p "$R/bubble"; cp "$SOL"/realm/bubble/gnomod.toml "$R/bubble/"
for f in "$SOL"/realm/bubble/*.gno; do case $f in *_test.gno|*_filetest.gno) ;; *) cp "$f" "$R/bubble/" ;; esac; done
# this deployment's realms, tests included
for r in $REALMS; do [ -d "$SRC/$r" ] || { echo "no realm $SRC/$r"; exit 1; }; mkdir -p "$R/eth/$r"; cp "$SRC/$r"/*.gno "$SRC/$r"/gnomod.toml "$R/eth/$r/"; done
LOGS=${GNOTEST_LOGS:-$ROOT/logs}; mkdir -p "$LOGS"
rc=0
# the three routes keep waiting.gno (E4-RL1, E5-RL1, E5-RL2), installments.gno (E5-GS1) and voids.gno (E6-RL1) the same
# but for the package line: hlbubble's probes stand for all three, and every route pages a migration, pays an
# installment and bounds a void alike
for f in waiting.gno installments.gno voids.gno; do
  for r in hlbubble hlgnot2; do
    if ! cmp -s <(tail -n +2 "$SRC/hlgnot/$f") <(tail -n +2 "$SRC/$r/$f"); then
      echo "$f: $r's is not hlgnot's (beyond the package line): FAIL"; rc=1
    fi
  done
done
if [ "${GNOTEST_ECHOES:-1}" != 0 ]; then
  GO=${GO:-$(command -v go || echo /usr/local/go/bin/go)}
  [ -x "$GO" ] || { echo "no go to run $ETH/scripts/check-echoes.go (set GO=, or GNOTEST_ECHOES=0 to skip the echo checker)"; exit 1; }
  if "$GO" run "$ETH/scripts/check-echoes.go" "$SRC" >"$LOGS/echoes.log" 2>&1; then
    echo "echoes: $(tail -1 "$LOGS/echoes.log")"
  else
    echo "echoes: FAIL"; sed 's/^/    /' "$LOGS/echoes.log"; rc=1
  fi
fi
for r in $REALMS; do
  { [ "$want" = all ] || [ "$want" = "$r" ]; } || continue
  log="$LOGS/$r.log"
  if (cd "$R/eth/$r" && GNOROOT="$ROOT" "$GNO" test -v ${GNOTEST_ARGS:-} . >"$log" 2>&1); then st=ok; else st=FAIL; rc=1; fi
  passed=$(grep -c -- '^--- PASS' "$log" || true); failed=$(grep -c -- '^--- FAIL' "$log" || true)
  # an unrecovered panic ends the run with no --- FAIL line: the test it hit counts as failed, the ones after as not run
  if grep -q '^panic: ' "$log"; then failed="$failed + 1 aborted by a panic ($(grep -m1 '^panic: ' "$log" | cut -c1-160))"; fi
  printf 'r/eth/%s: %s, passed %s, failed %s\n' "$r" "$st" "$passed" "$failed"
  tail -${GNOTEST_TAIL:-3} "$log" | sed 's/^/    /'
  probes=$(grep -ho '^func TestRenderGas[A-Za-z0-9_]*' "$R/eth/$r"/*_test.gno 2>/dev/null | sed 's/^func //' | sort -u || true)
  for p in $(required_probes "$r"); do
    case " $(echo $probes) " in *" $p "*) ;; *) echo "    render gas: $p is no longer declared: FAIL"; rc=1 ;; esac
  done
  if [ -n "$probes" ]; then
    measured=$(awk '/^=== RUN /{n=$3} /^--- GAS:/{ if (n ~ /^TestRenderGas/) print n, $3; n="" }' "$log")
    for p in $probes; do
      g=$(echo "$measured" | awk -v p="$p" '$1 == p {print $2; exit}')
      if [ -z "$g" ]; then
        case " ${GNOTEST_ARGS:-} " in *" -run "*|*" -run="*) ;; *) echo "    render gas: $p printed no gas: FAIL"; rc=1 ;; esac
        continue
      fi
      case $g in *[!0-9]*) echo "    render gas: $p printed no number ($g): FAIL"; rc=1; continue ;; esac
      f=$(node_factor "$r" "$p")
      if [ -z "$f" ]; then echo "    render gas: $p $g here, and no node factor (measure its page on a node first): FAIL"; rc=1; continue; fi
      n=$((g * f / 100))
      if [ "$n" -gt "$RENDER_GAS_MAX" ]; then verdict=FAIL; rc=1; else verdict=ok; fi
      echo "    render gas: $p $g here × $((f / 100)).$(printf %02d $((f % 100))) = $n on a node (max $RENDER_GAS_MAX) $verdict"
    done
  fi
  if [ "${GNOTEST_TXGAS:-1}" != 0 ]; then
    txgate migration TestTxGas required_tx_probes tx_factor "$r"
    txgate void TestTxVoid required_void_probes void_factor "$r"
  fi
  alone "$r"
  if [ "${GNOTEST_LINT:-}" = 1 ]; then
    if (cd "$R/eth/$r" && GNOROOT="$ROOT" "$GNO" lint -root-dir "$ROOT" . >"$LOGS/$r.lint" 2>&1); then
      echo "    lint: ok"
    else
      echo "    lint: FAIL"; sed 's/^/      /' "$LOGS/$r.lint"; rc=1
    fi
  fi
done
exit $rc
