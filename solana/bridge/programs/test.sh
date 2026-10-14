#!/bin/sh
# Builds the three programs for SBF and runs every test: the core against the chain's fixtures, the shared crate's
# units, and the programs under program-test (the tests workspace, with SBF_OUT_DIR at the built .so files).
#
#   test.sh            everything
#   test.sh core       the core and the shared crate only
#   test.sh sbf        the SBF builds only
#   test.sh suite      the program-test suite only (after sbf)
#
# Extra arguments after the mode go to cargo test (e.g. `test.sh suite -- --nocapture client`).
# FACTOR8=1 builds and tests the devnet clocks (the compiled factor of 8, §5.9): the programs' `clock-factor-8`
# feature, in the .so files and in the test crates' view of the constants alike.
set -e
here=$(cd "$(dirname "$0")" && pwd)
export PATH="$HOME/.local/share/solana-3.0.14/solana-release/bin:$HOME/.cargo/bin:$PATH"
mode=${1:-all}
[ $# -gt 0 ] && shift
features=""
suite_features=""
if [ -n "$FACTOR8" ]; then
  features="--features clock-factor-8"
  suite_features="--features gno-light-client-ism/clock-factor-8,pausable-ism/clock-factor-8,admin-program/clock-factor-8"
fi

core() {
  (cd "$here" && nice -n 10 cargo test -p gno-light-core -p sol-util --all-features "$@")
}
sbf() {
  for p in gno-light-client-ism pausable-ism admin-program; do
    (cd "$here" && nice -n 10 cargo build-sbf --manifest-path "$p/Cargo.toml" $features)
  done
}
suite() {
  (cd "$here/tests" && SBF_OUT_DIR="$here/target/deploy" nice -n 10 cargo test --no-fail-fast -p suite $suite_features "$@")
}
case "$mode" in
  core) core "$@" ;;
  sbf) sbf ;;
  suite) suite "$@" ;;
  all) core; sbf; suite "$@" ;;
  *) echo "usage: test.sh [core|sbf|suite|all] [cargo test args]"; exit 2 ;;
esac
