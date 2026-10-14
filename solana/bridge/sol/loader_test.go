package sol

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gagliardetto/solana-go"
)

// AP5, F15: a growth's rent is the chain's rent-exempt minimum at the new length less what the programdata
// holds, read live (getMinimumBalanceForRentExemption) — never a compiled rate.
func TestExtendRent(t *testing.T) {
	program := solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")
	const lamports, dataLen, perByte, base = 1_000_000, 1_045, 5_080, 128 * 5_080
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "getAccountInfo":
			if fmt.Sprint(req.Params[0]) == ProgramData(program).String() {
				result = map[string]any{"context": map[string]any{"slot": 1}, "value": map[string]any{"data": []string{base64.StdEncoding.EncodeToString(make([]byte, dataLen)), "base64"}, "executable": false, "lamports": lamports, "owner": LoaderID.String(), "rentEpoch": 0}}
			} else {
				result = map[string]any{"context": map[string]any{"slot": 1}, "value": nil}
			}
		case "getMinimumBalanceForRentExemption":
			size, _ := req.Params[0].(float64)
			result = base + uint64(size)*perByte
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()
	c := NewRPC(srv.URL)
	got, err := ExtendRent(context.Background(), c, program, 10_240)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(base+(dataLen+10_240)*perByte) - lamports; got != want {
		t.Fatalf("rent %d, want %d (the minimum at the new length less the account's lamports)", got, want)
	}
	if _, err := ExtendRent(context.Background(), c, solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr"), 10_240); err == nil {
		t.Fatal("a program with no programdata priced")
	}
	if s := SOL(127_000_000); s != "0.1270 SOL" {
		t.Fatal(s)
	}
}

// F7: one ExtendProgram carries the whole excess, at or above the loader's 10,240-byte minimum, or exactly the
// headroom below the 10 MiB bound; nothing when the program data already holds the buffer.
func TestExtendBy(t *testing.T) {
	cases := []struct {
		pd, buf int
		want    uint32
	}{
		{100_000, 100_000, 0},
		{100_000, 90_000, 0},
		{100_000, 100_001, ExtendStep},
		{100_000, 110_240, ExtendStep},
		{100_000, 125_000, 25_000},
		{100_000, 1_100_000, 1_000_000},
		{MaxProgramDataLen - ProgramDataHeader - 5_000, MaxProgramDataLen, 5_000},
		{MaxProgramDataLen - ProgramDataHeader, MaxProgramDataLen + 1, 0},
	}
	for _, c := range cases {
		if got := ExtendBy(c.pd, c.buf); got != c.want {
			t.Errorf("ExtendBy(%d, %d) = %d, want %d", c.pd, c.buf, got, c.want)
		}
	}
}

// G20: a compiled 32-byte constant is found in an ELF contiguous, or as the four lddw immediates the compiler
// emits for a chunked compare (the pausable ISM's MAILBOX and the admin program's HLGOV_SENDER on the e2e's
// builds); a key absent in both forms is not bound.
func TestELFBindsKey(t *testing.T) {
	key := solana.MustPublicKeyFromBase58("4RGdtbf8oxCqysLCmVDRKLLZTrTmjPWiLY1xUTiq9HXU")
	var elf []byte
	elf = append(elf, []byte("\x7fELF padding")...)
	elf = append(elf, key[:]...)
	elf = append(elf, []byte("tail")...)
	if !ELFBindsKey(elf, key) {
		t.Fatal("a contiguous key not found")
	}
	other := solana.MustPublicKeyFromBase58("9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr")
	if ELFBindsKey(elf, other) {
		t.Fatal("an absent key found")
	}
	// the lddw form: 18 0r 00 00 lo(4) 00 00 00 00 hi(4) per 8-byte chunk, in any order, with code between
	var code []byte
	code = append(code, []byte("\x7fELF")...)
	for i := 24; i >= 0; i -= 8 {
		code = append(code, 0x18, byte(i/8+1), 0, 0)
		code = append(code, other[i:i+4]...)
		code = append(code, 0, 0, 0, 0)
		code = append(code, other[i+4:i+8]...)
		code = append(code, 0x5d, 0x12, 0x09, 0, 0, 0, 0, 0) // a jne
	}
	if !ELFBindsKey(code, other) {
		t.Fatal("a key in lddw immediates not found")
	}
	if ELFBindsKey(code, key) {
		t.Fatal("an absent key found in the code")
	}
	// one chunk missing: not bound
	broken := bytes.Replace(code, other[0:4], []byte{9, 9, 9, 9}, 1)
	if ELFBindsKey(broken, other) {
		t.Fatal("three chunks of four counted as bound")
	}
	if !ELFBindsText(append([]byte("x"), []byte("gno.land/r/x/hlmailbox")...), "gno.land/r/x/hlmailbox") || ELFBindsText(code, "gno.land/r/x/hlmailbox") {
		t.Fatal("the text binding")
	}
	// G51: a 64-bit constant, contiguous or as one lddw immediate (the pausable ISM's DEFAULT_DAY_CAP and
	// DEFAULT_MAX_PER_MESSAGE in the e2e's build)
	v := uint64(25_000_000_000) // 0x5_D21D_BA00
	contiguous := append([]byte("\x7fELF"), 0x00, 0xba, 0x1d, 0xd2, 0x05, 0, 0, 0)
	if !ELFBindsU64(contiguous, v) || ELFBindsU64(contiguous, v+1) {
		t.Fatal("a contiguous u64")
	}
	one := []byte{0x18, 0x01, 0, 0, 0x00, 0xba, 0x1d, 0xd2, 0, 0, 0, 0, 0x05, 0, 0, 0}
	if !ELFBindsU64(append([]byte("\x7fELF"), one...), v) || ELFBindsU64(append([]byte("\x7fELF"), one...), 100_000_000_000) {
		t.Fatal("a u64 in an lddw immediate")
	}
}
