package main

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// The client's Ed25519 verifier (E-M2a's Ed25519Verifier, an immutable of the client's implementation): a client
// whose constructor takes an address gets the recorded verifier under that parameter's own name; a client with the
// library inline takes none and no verifier is deployed.
func TestClientVerifierParams(t *testing.T) {
	inline, err := abi.JSON(strings.NewReader(`[{"type":"constructor","inputs":[{"name":"mailboxPkgPathHash_","type":"bytes32"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := clientVerifierParams(&Artifact{Name: "GnoLightClient", ABI: inline}); len(got) != 0 {
		t.Fatalf("an inline client takes a verifier: %v", got)
	}
	split, err := abi.JSON(strings.NewReader(`[{"type":"constructor","inputs":[{"name":"mailboxPkgPathHash_","type":"bytes32"},{"name":"verifier_","type":"address"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	a := &Artifact{Name: "GnoLightClient", ABI: split, Creation: []byte{0x60, 0x80}}
	params := clientVerifierParams(a)
	if len(params) != 1 || norm(params[0]) != "verifier" {
		t.Fatalf("the split client's verifier parameter: %v", params)
	}
	v := Values{"mailboxpkgpathhash": common.Hash{1}}
	if _, err := a.CreationCode(v); err == nil || !strings.Contains(err.Error(), "verifier_") {
		t.Fatalf("a client without its verifier was packed: %v", err)
	}
	ver := common.HexToAddress("0x00000000000000000000000000000000000000e5")
	v[norm(params[0])] = ver
	code, err := a.CreationCode(v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(common.Bytes2Hex(code), common.Bytes2Hex(common.LeftPadBytes(ver.Bytes(), 32))) {
		t.Fatal("the verifier's address is not the constructor's last argument")
	}
}
