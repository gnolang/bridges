package ethx

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

// A call that reverts — refused by eth_estimateGas or eth_call before anything is sent, or mined and reverted —
// comes back as a *Revert carrying the revert data, decoded: Solidity's Error(string) (a require's text, Hyperlane's
// "Mailbox: already delivered" among them), Panic(uint256) (an assert, an overflow), or a custom error of one of the
// bridge's contracts, found by its 4-byte selector in the ABIs this package embeds (our three contracts' errors are
// named by their implementations' ABIs once lane E-M2b's land, abi/gen.sh).

var (
	errorSelector = []byte{0x08, 0xc3, 0x79, 0xa0} // Error(string)
	panicSelector = []byte{0x4e, 0x48, 0x7b, 0x71} // Panic(uint256)
)

// Revert is a call the chain refused.
type Revert struct {
	Data     []byte // the revert data, when the provider gave it
	Name     string // "Error", "Panic", a custom error's name, or "" when undecoded
	Reason   string // Error's text, Panic's meaning, a custom error's arguments
	Contract string // the ABI that named a custom error
	Message  string // the provider's own text
	Mined    bool   // mined and reverted (gas spent), rather than refused before sending
}

func (r *Revert) Error() string {
	var b strings.Builder
	if r.Mined {
		b.WriteString("mined and reverted")
	} else {
		b.WriteString("reverts")
	}
	switch {
	case r.Name == "Error":
		fmt.Fprintf(&b, ": %q", r.Reason)
	case r.Name != "":
		b.WriteString(": ")
		if r.Contract != "" {
			b.WriteString(r.Contract + ".")
		}
		b.WriteString(r.Name)
		if r.Reason != "" {
			b.WriteString(" " + r.Reason)
		}
	case len(r.Data) > 0:
		fmt.Fprintf(&b, ": undecoded data %s", hexutil.Encode(r.Data))
	case r.Message != "":
		b.WriteString(": " + Redact(r.Message))
	}
	return b.String()
}

// Named names a revert by its error name or its Error(string) text: Named("OverDayCap"), Named("already delivered").
func (r *Revert) Named(name string) bool {
	if r == nil {
		return false
	}
	return r.Name == name || (r.Name == "Error" && strings.Contains(r.Reason, name)) || (r.Name == "" && strings.Contains(r.Message, name))
}

// AsRevert finds a *Revert in an error's chain.
func AsRevert(err error) (*Revert, bool) {
	var r *Revert
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// RevertIs says whether an error is a revert of that name (Revert.Named).
func RevertIs(err error, name string) bool {
	r, ok := AsRevert(err)
	return ok && r.Named(name)
}

var revertedRE = regexp.MustCompile(`(?i)reverted|revert`)
var hexDataRE = regexp.MustCompile(`0x[0-9a-fA-F]{8,}`)

// revertFromRPC reads an RPC error as a revert: geth's code 3 "execution reverted" with the data as a hex string,
// another client's data under an object, or the data only in the message. Not a revert: nil.
func revertFromRPC(err error) *Revert {
	var de rpc.DataError
	msg := err.Error()
	var data []byte
	if errors.As(err, &de) {
		data = dataBytes(de.ErrorData())
	}
	if data == nil && !revertedRE.MatchString(msg) {
		return nil
	}
	if data == nil {
		if m := hexDataRE.FindString(msg); m != "" {
			if b, e := hexutil.Decode(m); e == nil && len(b) >= 4 {
				data = b
			}
		}
	}
	if data == nil && !strings.Contains(strings.ToLower(msg), "execution reverted") && !strings.Contains(strings.ToLower(msg), "reverted") {
		return nil
	}
	return DecodeRevert(data, msg)
}

// dataBytes reads an RPC error's data: a hex string, or an object with a "data" or "result" member.
func dataBytes(v any) []byte {
	switch d := v.(type) {
	case string:
		b, err := hexutil.Decode(d)
		if err != nil {
			return nil
		}
		return b
	case map[string]any:
		for _, k := range []string{"data", "result", "originalError"} {
			if x, ok := d[k]; ok {
				if b := dataBytes(x); b != nil {
					return b
				}
			}
		}
	}
	return nil
}

// DecodeRevert names revert data: Error(string), Panic(uint256), or a custom error of the embedded ABIs.
func DecodeRevert(data []byte, message string) *Revert {
	r := &Revert{Data: data, Message: message}
	if len(data) < 4 {
		return r
	}
	switch {
	case bytes.Equal(data[:4], errorSelector):
		if s, err := abi.UnpackRevert(data); err == nil {
			r.Name, r.Reason = "Error", s
		}
		return r
	case bytes.Equal(data[:4], panicSelector):
		r.Name = "Panic"
		if len(data) >= 36 {
			code := new(big.Int).SetBytes(data[4:36])
			r.Reason = fmt.Sprintf("0x%x (%s)", code, panicMeaning(code.Uint64()))
		}
		return r
	}
	var sel [4]byte
	copy(sel[:], data[:4])
	for _, c := range allContracts() {
		e, err := c.ABI.ErrorByID(sel)
		if err != nil {
			continue
		}
		r.Name, r.Contract = e.Name, c.Name
		if v, err := e.Unpack(data); err == nil {
			if args, ok := v.([]any); ok && len(args) > 0 {
				r.Reason = fmt.Sprint(args...)
			}
		}
		return r
	}
	return r
}

func panicMeaning(code uint64) string {
	switch code {
	case 0x01:
		return "assert failed"
	case 0x11:
		return "arithmetic overflow or underflow"
	case 0x12:
		return "division by zero"
	case 0x21:
		return "bad enum value"
	case 0x22:
		return "bad storage byte array"
	case 0x31:
		return "pop of an empty array"
	case 0x32:
		return "index out of bounds"
	case 0x41:
		return "out of memory"
	case 0x51:
		return "uninitialized function"
	}
	return "unknown panic"
}
