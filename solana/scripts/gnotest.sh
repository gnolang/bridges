#!/bin/bash
# gno tests for the vendored governance packages and the bubble realm, run STAGED: gno resolves a sibling package
# only under $GNOROOT/examples at its import path, so a private shadow of the toolchain's GNOROOT is built per run
# (kourt's scripts/gnoroot.py idea, in twenty lines): every top-level entry symlinked, examples copied, our packages
# placed under it, `gno test` run in each, the shadow removed. The toolchain is the pinned mainnet one (gno v1.2.0,
# kourt's `make toolchain` puts it in ~/.cache/gno-toolchains/v1.2.0/gno); the gno on PATH does not match its GNOROOT.
#   scripts/gnotest.sh            every package and the realm
#   scripts/gnotest.sh governor   one of: checkpoint grc20votes governor hyperlane decisions hlroute bubble bubbletreasury
#                                 bubblerumble5 wbubble hlmailbox hlverifier hlfees hlgnot hlbubble hlgov hlgnot2
#   GNOTEST_ARGS="-run Name -v" scripts/gnotest.sh bubble
set -euo pipefail
cd "$(dirname "$0")/.."
ADDR=g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr
GNO=${GNO:-$HOME/.cache/gno-toolchains/v1.2.0/gno}
[ -x "$GNO" ] || { echo "no pinned gno at $GNO: GOBIN=$(dirname "$GNO") go install github.com/gnolang/gno/gnovm/cmd/gno@v1.2.0"; exit 1; }
REAL=$("$GNO" env GNOROOT)
[ -d "$REAL/examples" ] || { echo "GNOROOT $REAL has no examples"; exit 1; }
ROOT=$(mktemp -d "${TMPDIR:-/tmp}/bubble-gnoroot.XXXXXX")
trap 'chmod -R u+w "$ROOT" 2>/dev/null; rm -rf "$ROOT"' EXIT
for e in "$REAL"/* "$REAL"/.[!.]*; do n=$(basename "$e"); [ "$n" = examples ] || [ "$n" = .git ] || ln -s "$e" "$ROOT/$n"; done
cp -R "$REAL/examples" "$ROOT/examples"; chmod -R u+w "$ROOT/examples"
P="$ROOT/examples/gno.land/p/$ADDR"; R="$ROOT/examples/gno.land/r/$ADDR"
PKGS="checkpoint grc20votes governor hyperlane decisions hlroute"
for p in $PKGS; do if [ -d "realm/p/$p/v0" ]; then mkdir -p "$P/$p/v0"; cp realm/p/$p/v0/*.gno realm/p/$p/v0/gnomod.toml "$P/$p/v0/"; fi; done
# every realm that imports a sibling: staged beside the packages, run in order (the bridge realms after hlmailbox)
REALMS="bubble bubbletreasury bubblerumble5 wbubble hlmailbox hlverifier hlfees hlgnot hlbubble hlgov hlgnot2"
for r in $REALMS; do if [ -d "realm/$r" ]; then mkdir -p "$R/$r"; cp realm/$r/*.gno realm/$r/gnomod.toml "$R/$r/"; fi; done
want=${1:-all}; rc=0
run() { printf '%s: ' "$1"; (cd "$2" && GNOROOT="$ROOT" "$GNO" test ${GNOTEST_ARGS:-} . 2>&1 | tail -${GNOTEST_TAIL:-3}) || rc=1; }
for p in $PKGS; do [ -d "realm/p/$p/v0" ] && { [ "$want" = all ] || [ "$want" = "$p" ]; } && run "p/$p" "$P/$p/v0"; done
for r in $REALMS; do [ -d "realm/$r" ] && { [ "$want" = all ] || [ "$want" = "$r" ]; } && run "r/$r" "$R/$r"; done
exit $rc
