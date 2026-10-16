#!/bin/sh
# Writes ethx's calldata vectors with Foundry's cast (an encoder independent of go-ethereum's), from gnoland-1's own
# blocks (bridge/programs/fixtures): run on the devbox from this directory.
#
#   update_vectors.txt: "<height> <next-set height or 0> <calldata of update(...)>", the precommits chosen here by
#     this program's own greedy (largest powers first, ties by slot, until more than 2/3);
#   meta_vectors.txt:   "<height> <leaf index> <verify metadata> <process(metadata, a fixed message) calldata>";
#   sigs.txt:           selectors and event topics by cast sig / cast keccak.
set -eu
PATH=$PATH:/usr/local/go/bin:/root/.foundry/bin
F=../../../../../bridge/programs/fixtures
GOFLAGS=-mod=mod go build -o /tmp/ethx-gen .
SIG="update(bytes[],(uint32,bytes32,int64,(uint16,int64,int32,bytes32,bytes32)[]),(bytes32,int64)[],(bytes32,int64)[])"
for spec in "720333" "720332 720333" "741103" "487883"; do
	set -- $spec
	/tmp/ethx-gen $F $1 ${2:-} >/tmp/ethx-args.txt
	echo "$1 ${2:-0} $(cast calldata "$SIG" "$(sed -n 1p /tmp/ethx-args.txt)" "$(sed -n 2p /tmp/ethx-args.txt)" "$(sed -n 3p /tmp/ethx-args.txt)" "$(sed -n 4p /tmp/ethx-args.txt)")"
done >../update_vectors.txt
MSG=0x0300000007476e6f540000000000000000000000000000000000000000000000000000000000000001deadbeef
for spec in "740913 0" "740913 1" "720331 0"; do
	set -- $spec
	/tmp/ethx-gen meta $F $1 $2 >/tmp/ethx-meta.txt
	M=$(cast abi-encode "f(bytes32,uint32,uint32,bytes32[],bytes)" "$(sed -n 1p /tmp/ethx-meta.txt)" "$(sed -n 2p /tmp/ethx-meta.txt)" "$(sed -n 3p /tmp/ethx-meta.txt)" "$(sed -n 4p /tmp/ethx-meta.txt)" "$(sed -n 5p /tmp/ethx-meta.txt)")
	echo "$1 $2 $M $(cast calldata "process(bytes,bytes)" "$M" "$MSG")"
done >../meta_vectors.txt
for s in "process(bytes,bytes)" "delivered(bytes32)" \
	"update(bytes[],(uint32,bytes32,int64,(uint16,int64,int32,bytes32,bytes32)[]),(bytes32,int64)[],(bytes32,int64)[])" \
	"proveHeader(bytes[],(uint32,bytes32,int64,(uint16,int64,int32,bytes32,bytes32)[]),(bytes32,int64)[])" \
	"verify(bytes,bytes)" "execute(uint64)" "transferRemote(uint32,bytes32,uint256)" "entry(bytes32)"; do
	echo "sig $(cast sig "$s") $s"
done >../sigs.txt
for e in "Dispatch(address,uint32,bytes32,bytes)" "Process(uint32,bytes32,address)" "ProcessId(bytes32)" "DispatchId(bytes32)" \
	"HeaderRecorded(bytes32,int64,uint64,uint64,bytes32,bool)" "StageProgress(bytes32,address,bytes32,int64,int64)" \
	"DecisionRecorded(uint64,uint8,uint64)" "SentTransferRemote(uint32,bytes32,uint256)"; do
	echo "topic $(cast keccak "$e") $e"
done >>../sigs.txt
