#!/bin/sh
# Hyperlane 12.2.0's and OpenZeppelin 4.9.3's contracts as the bridge deploys them (eth/bridge/PLAN.md §5.8, §5.9):
# compiled from the RELEASE's own compiler input — `dist/buildArtifact.json` in the npm tarball of
# @hyperlane-xyz/core@12.2.0 (solc 0.8.33+commit.64118f21, cancun, the optimizer at 10,000 runs, 343 sources) — with
# that very solc, never from a hand copy and never from another build's settings, so "byte for byte as compiled from
# the compiler input Hyperlane published" holds for what `hleth setup` deploys and what `hleth audit` compares.
#
# One JSON per contract in <outdir>, in forge's artifact shape so `hleth` reads both alike: abi, bytecode.object
# (the creation code), deployedBytecode.object (the runtime code), deployedBytecode.immutableReferences (where each
# immutable's 32 bytes sit in the runtime code, by the declaration's AST id), and immutableNames (each id as
# "<declaring contract>.<variable>", read from the same compile's ASTs: the audit names every masked value).
#
#   usage: hyperlane-artifacts.sh <core-12.2.0.tgz | buildArtifact.json> <solc-0.8.33> <outdir>
#
# Run it on the devbox (BUILD.md): the tarball is fetched once with `npm pack @hyperlane-xyz/core@12.2.0`; lane
# E-M2b vendors the same buildArtifact.json at eth/bridge/contracts/lib/hyperlane-core/dist/.
set -eu
SRC=${1:?the release tarball or its buildArtifact.json}
SOLC=${2:?solc 0.8.33}
OUT=${3:?the output directory}
mkdir -p "$OUT"
case "$SRC" in
*.tgz)
	X=$(mktemp -d)
	trap 'rm -rf "$X"' EXIT
	tar -xzf "$SRC" -C "$X" package/dist/buildArtifact.json
	ART="$X/package/dist/buildArtifact.json"
	;;
*) ART="$SRC" ;;
esac
python3 -I - "$ART" "$SOLC" "$OUT" <<'EOF'
import json, subprocess, sys
art, solc, outdir = sys.argv[1], sys.argv[2], sys.argv[3]
d = json.load(open(art))
assert d["solcLongVersion"] == "0.8.33+commit.64118f21", d["solcLongVersion"]
ver = subprocess.run([solc, "--version"], capture_output=True, check=True, text=True).stdout
assert "0.8.33+commit.64118f21" in ver, ver
inp = d["input"]
want = {
    "contracts/Mailbox.sol": "Mailbox",
    "contracts/token/HypERC20.sol": "HypERC20",
    "contracts/hooks/MerkleTreeHook.sol": "MerkleTreeHook",
    "contracts/hooks/ProtocolFee.sol": "ProtocolFee",
    "@openzeppelin/contracts/proxy/transparent/ProxyAdmin.sol": "ProxyAdmin",
    "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol": "TransparentUpgradeableProxy",
}
sel = {"*": {"": ["ast"]}}
for k, c in want.items():
    sel.setdefault(k, {"": ["ast"]})[c] = ["abi", "evm.bytecode.object", "evm.deployedBytecode.object", "evm.deployedBytecode.immutableReferences"]
inp["settings"]["outputSelection"] = sel
out = json.loads(subprocess.run([solc, "--standard-json"], input=json.dumps(inp).encode(), capture_output=True, check=True).stdout)
errs = [e["formattedMessage"] for e in out.get("errors", []) if e.get("severity") == "error"]
assert not errs, errs
# every immutable state variable of the compile, by its AST id, named "<declaring contract>.<variable>"
names = {}
def walk(node, contract):
    if isinstance(node, dict):
        if node.get("nodeType") == "ContractDefinition":
            contract = node.get("name", contract)
        if node.get("nodeType") == "VariableDeclaration" and node.get("mutability") == "immutable":
            names[str(node["id"])] = contract + "." + node["name"]
        for v in node.values():
            walk(v, contract)
    elif isinstance(node, list):
        for v in node:
            walk(v, contract)
for src in out["sources"].values():
    walk(src.get("ast"), "")
for k, c in want.items():
    o = out["contracts"][k][c]
    refs = o["evm"]["deployedBytecode"].get("immutableReferences", {})
    missing = [i for i in refs if i not in names]
    assert not missing, (c, missing)
    a = {
        "contract": c,
        "source": k,
        "compiler": d["solcLongVersion"],
        "abi": o["abi"],
        "bytecode": {"object": "0x" + o["evm"]["bytecode"]["object"]},
        "deployedBytecode": {"object": "0x" + o["evm"]["deployedBytecode"]["object"], "immutableReferences": refs},
        "immutableNames": {i: names[i] for i in refs},
    }
    json.dump(a, open(f"{outdir}/{c}.json", "w"), indent=1, sort_keys=True)
    print(c, len(o["evm"]["deployedBytecode"]["object"]) // 2, "bytes of runtime code;", ", ".join(sorted(names[i] for i in refs)) or "no immutables")
EOF
