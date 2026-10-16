package main

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// The constructors and initializers are packed by name from the artifact's own ABI: a parameter moved packs to its
// new place, a parameter the tool does not know is refused by name before anything is sent.

const pkgABI = `[
 {"type":"constructor","inputs":[{"name":"c","type":"tuple","components":[
   {"name":"mailbox","type":"address"},{"name":"origin","type":"uint32"},{"name":"hlgov","type":"bytes32"},
   {"name":"seats","type":"address[4]"},{"name":"startSequence","type":"uint64"}]}]},
 {"type":"function","name":"initialize","stateMutability":"nonpayable","outputs":[],"inputs":[
   {"name":"chainId_","type":"string"},{"name":"trustedHeight_","type":"int64"},
   {"name":"validators","type":"tuple[]","components":[{"name":"pubkey","type":"bytes32"},{"name":"power","type":"int64"}]},
   {"name":"maxPerMessage","type":"uint64[2]"},{"name":"_scale","type":"uint256"}]},
 {"type":"function","name":"moved","stateMutability":"nonpayable","outputs":[],"inputs":[
   {"name":"_scale","type":"uint256"},{"name":"validators","type":"tuple[]","components":[{"name":"power","type":"int64"},{"name":"pubkey","type":"bytes32"}]},
   {"name":"trustedHeight_","type":"int64"},{"name":"maxPerMessage","type":"uint64[2]"},{"name":"chainId_","type":"string"}]}
]`

func TestPackByName(t *testing.T) {
	parsed, err := abi.JSON(strings.NewReader(pkgABI))
	if err != nil {
		t.Fatal(err)
	}
	a := &Artifact{Name: "T", ABI: parsed, Creation: []byte{0xfe}}
	mailbox := common.HexToAddress("0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0")
	var hlgov [32]byte
	hlgov[31] = 7
	seats := [4]common.Address{common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3"), common.HexToAddress("0x4")}
	code, err := a.CreationCode(Values{"c": Values{"mailbox": mailbox, "origin": uint32(1198419796), "hlgov": hlgov, "seats": seats, "startsequence": uint64(5)}})
	if err != nil {
		t.Fatal(err)
	}
	// the tuple's words in order: the mailbox, the origin, hlgov, four seats, the start
	want := "fe" + "000000000000000000000000" + strings.ToLower(mailbox.Hex()[2:]) +
		strings.Repeat("0", 56) + "476e6f54" + hex.EncodeToString(hlgov[:])
	if got := hex.EncodeToString(code); !strings.HasPrefix(got, want) {
		t.Fatalf("the constructor's packing:\n got  %s\n want %s…", got, want)
	}
	vals := Values{"chainid": "dev", "trustedheight": int64(100), "scale": uint64(1), "maxpermessage": [2]uint64{25, 100},
		"validators": []Values{{"pubkey": [32]byte{1}, "power": int64(10)}, {"pubkey": [32]byte{2}, "power": int64(20)}}}
	init1, err := a.CallData("initialize", vals)
	if err != nil {
		t.Fatal(err)
	}
	// the same values packed for a method whose parameters moved: the same arguments by name, in its own order
	moved, err := a.CallData("moved", vals)
	if err != nil {
		t.Fatal(err)
	}
	args1, _ := parsed.Methods["initialize"].Inputs.Unpack(init1[4:])
	args2, _ := parsed.Methods["moved"].Inputs.Unpack(moved[4:])
	if args1[0].(string) != "dev" || args2[4].(string) != "dev" || args1[1].(int64) != 100 || args2[2].(int64) != 100 {
		t.Fatalf("by name: %v / %v", args1, args2)
	}
	// a parameter the tool has no value for is refused by name
	delete(vals, "scale")
	if _, err := a.CallData("initialize", vals); err == nil || !strings.Contains(err.Error(), `"_scale"`) {
		t.Fatalf("a missing parameter: %v", err)
	}
	if _, err := a.CreationCode(Values{"c": Values{"mailbox": mailbox}}); err == nil || !strings.Contains(err.Error(), `"origin"`) {
		t.Fatalf("a missing component: %v", err)
	}
	// a value of the wrong kind is refused, never packed as something else
	if _, err := a.CreationCode(Values{"c": Values{"mailbox": "0x9fE4", "origin": uint32(1), "hlgov": hlgov, "seats": seats, "startsequence": uint64(5)}}); err == nil {
		t.Fatal("a string packed as an address")
	}
	if !bytes.HasPrefix(code, []byte{0xfe}) {
		t.Fatal("the creation code lost its prefix")
	}
}

func TestCurlLinesNeverCarryTheURL(t *testing.T) {
	one := curlLines([]byte(`{"jsonrpc":"2.0","id":7,"method":"eth_call","params":[{"to":"0x01","data":"0x"},"latest"]}`))
	batch := curlLines([]byte(`[{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]},{"jsonrpc":"2.0","id":2,"method":"eth_getCode","params":["0x01","0x5"]}]`))
	if len(one) != 1 || !strings.Contains(one[0], `"method":"eth_call","params":[{"data":"0x","to":"0x01"},"latest"]`) || !strings.Contains(one[0], `curl -s "$RPC"`) {
		t.Fatalf("one call: %v", one)
	}
	if len(batch) != 2 || !strings.Contains(batch[1], `"eth_getCode"`) {
		t.Fatalf("a batch: %v", batch)
	}
}
