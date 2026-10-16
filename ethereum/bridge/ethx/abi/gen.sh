#!/bin/sh
# Regenerates the JSON ABIs ethx embeds (run on the devbox, never the Mac):
#
#   - ours (IGnoLightClient, IPausableGate, IHlAdmin) from eth/bridge/contracts/src/interfaces with forge inspect,
#     solc 0.8.33, and beside them the implementations' (GnoLightClient, PausableGate, HlAdmin and its delegate
#     HlAdminExec) from src/ — an implementation's ABI adds its custom errors, which the revert
#     decoder names — all from one build of a copy of the whole Foundry project (its remappings and lib); ethx loads
#     an implementation's file before its interface's;
#   - Hyperlane's and OpenZeppelin's (Mailbox, HypERC20, MerkleTreeHook, ProtocolFee, ProxyAdmin,
#     TransparentUpgradeableProxy) by compiling @hyperlane-xyz/core@12.2.0's own compiler input
#     (dist/buildArtifact.json in the npm tarball: solc 0.8.33+commit.64118f21, cancun, 10,000 runs, 343 sources)
#     with that solc, output selection "abi" only — the release's real compile, not a hand copy.
#
# usage: gen.sh <worktree> <scratch dir>
set -eu
WT=${1:?worktree}
SCRATCH=${2:?scratch dir}
OUT="$WT/eth/bridge/ethx/abi"
PATH=$PATH:/root/.foundry/bin
SOLC=${SOLC:-/root/.local/share/svm/0.8.33/solc-0.8.33}

# ours: a copy of the whole Foundry project (its foundry.toml with the remappings — the implementations import
# bridge-clock/, Hyperlane and OpenZeppelin — its src and its lib), built once; the interfaces' ABIs and the
# implementations' (each adds its custom errors, which the revert decoder names) are inspected from that one build
rm -rf "$SCRATCH/impl" && mkdir -p "$SCRATCH/impl"
cp -a "$WT/eth/bridge/contracts/foundry.toml" "$WT/eth/bridge/contracts/src" "$WT/eth/bridge/contracts/lib" "$SCRATCH/impl/"
(cd "$SCRATCH/impl" && forge build --skip test --skip script >/dev/null)
for c in IGnoLightClient IPausableGate IHlAdmin; do
	(cd "$SCRATCH/impl" && forge inspect "src/interfaces/$c.sol:$c" abi --json) >"$OUT/$c.abi.json"
done
for c in GnoLightClient PausableGate HlAdmin admin/HlAdminExec lib/Guardians; do
	n=$(basename "$c")
	(cd "$SCRATCH/impl" && forge inspect "src/$c.sol:$n" abi --json) >"$OUT/$n.abi.json"
done

# Hyperlane 12.2.0 and its OpenZeppelin 4.9.3, from the release's compiler input
mkdir -p "$SCRATCH/hl"
if [ ! -f "$SCRATCH/hl/hyperlane-xyz-core-12.2.0.tgz" ]; then
	(cd "$SCRATCH/hl" && npm pack @hyperlane-xyz/core@12.2.0 >/dev/null)
fi
rm -rf "$SCRATCH/hl/x" && mkdir -p "$SCRATCH/hl/x"
tar -xzf "$SCRATCH/hl/hyperlane-xyz-core-12.2.0.tgz" -C "$SCRATCH/hl/x"
python3 -I - "$SCRATCH/hl/x/package/dist/buildArtifact.json" "$SOLC" "$OUT" <<'EOF'
import json, subprocess, sys
art, solc, outdir = sys.argv[1], sys.argv[2], sys.argv[3]
d = json.load(open(art))
assert d["solcLongVersion"] == "0.8.33+commit.64118f21", d["solcLongVersion"]
inp = d["input"]
want = {
    "contracts/Mailbox.sol": "Mailbox",
    "contracts/token/HypERC20.sol": "HypERC20",
    "contracts/hooks/MerkleTreeHook.sol": "MerkleTreeHook",
    "contracts/hooks/ProtocolFee.sol": "ProtocolFee",
    "@openzeppelin/contracts/proxy/transparent/ProxyAdmin.sol": "ProxyAdmin",
    "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol": "TransparentUpgradeableProxy",
}
inp["settings"]["outputSelection"] = {k: {c: ["abi"]} for k, c in want.items()}
out = json.loads(subprocess.run([solc, "--standard-json"], input=json.dumps(inp).encode(), capture_output=True, check=True).stdout)
errs = [e["formattedMessage"] for e in out.get("errors", []) if e.get("severity") == "error"]
assert not errs, errs
for k, c in want.items():
    json.dump(out["contracts"][k][c]["abi"], open(f"{outdir}/{c}.abi.json", "w"), indent=1, sort_keys=True)
EOF
ls -la "$OUT"
