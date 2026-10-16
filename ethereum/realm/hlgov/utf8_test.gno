package hlgov

import (
	"strings"
	"testing"
	"unicode/utf8"

	"gno.land/p/nt/testutils/v0"
)

// The file runs last (by name): the deployment is live.

// refusal is the text f panics with, "" if it does not.
func refusal(f func()) string {
	switch e := revive(f).(type) {
	case error:
		return e.Error()
	case string:
		return e
	}
	return ""
}

// chainSafe is whether a refusal's text stays valid UTF-8 however gno cuts it (E4-GS1): a failed transaction's panic
// text, and its Log line, are cut at 1,024 bytes by byte offset, so the text is all ASCII, or valid UTF-8 and short
// enough that no cut reaches it.
func chainSafe(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return len(s) <= 900
		}
	}
	return true
}

// badStrings are what a stranger hands a builder to poison a block (E4-GS1): two bytes no rune begins with (the
// audit's PoC, gno v1.5.1), and 1,200 bytes of em dashes — valid UTF-8 that gno's 1,024-byte cut splits mid-rune
// once a refusal echoes them.
var badStrings = []string{"\xff\xfe", strings.Repeat("\xe2\x80\x94", 400)}

// head is the start of a long text, for a failure's message.
func head(s string) string {
	if len(s) > 160 {
		return s[:160]
	}
	return s
}

// TestEchoesStayUTF8 (E4-GS1, E4-RL6): every builder that reads a caller's string — the numbers a cancel names, an
// Ethereum address (hlmailbox.ParseEthAddress), a seat change, a code hash, a call — given bytes that are no UTF-8 or
// a long run of multi-byte runes, refuses with a text that stays valid UTF-8 however gno cuts it: the caller's string
// quoted and cut short (echo). Nothing is recorded.
func TestEchoesStayUTF8(cur realm, t *testing.T) {
	n := ledger.Last()
	a1, a2 := "0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"
	hash := "0x" + strings.Repeat("ab", 32)
	for _, s := range badStrings {
		for _, c := range []struct {
			name string
			f    func()
		}{
			{"cancel: a number", func() { NewCancelRequest(cross(cur), s) }},
			{"cancel: a range", func() { NewCancelRequest(cross(cur), s+"-3") }},
			{"token ISM", func() { NewSetTokenISMRequest(cross(cur), 0, s) }},
			{"guardians: the change", func() { NewSetGuardiansRequest(cross(cur), s) }},
			{"guardians: the seat", func() { NewSetGuardiansRequest(cross(cur), s+":"+a1) }},
			{"guardians: the address", func() { NewSetGuardiansRequest(cross(cur), "1:"+s) }},
			{"enroll router", func() { NewEnrollRouterRequest(cross(cur), 0, 7, s) }},
			{"protocol fee", func() { NewSetProtocolFeeRequest(cross(cur), 0, s) }},
			{"upgrade: the proxy", func() { NewUpgradeRequest(cross(cur), s, a2, hash, "") }},
			{"upgrade: the implementation", func() { NewUpgradeRequest(cross(cur), a1, s, hash, "") }},
			{"upgrade: the code hash", func() { NewUpgradeRequest(cross(cur), a1, a2, s, "") }},
			{"upgrade: the call", func() { NewUpgradeRequest(cross(cur), a1, a2, hash, s) }},
			{"successor: the address", func() { NewTransferToSuccessorRequest(cross(cur), s, hash) }},
			{"successor: the code hash", func() { NewTransferToSuccessorRequest(cross(cur), a1, s) }},
			{"recipient: the one replaced", func() { NewSetDecisionRecipientRequest(cross(cur), s, a1) }},
			{"recipient: the new one", func() { NewSetDecisionRecipientRequest(cross(cur), a1, s) }},
		} {
			testing.SetRealm(testing.NewUserRealm(testutils.TestAddress("hl-echo")))
			if msg := refusal(c.f); msg == "" || !chainSafe(msg) {
				t.Errorf("%s handed %d bytes: a refusal of %d bytes, valid UTF-8 %v: %q", c.name, len(s), len(msg), utf8.ValidString(msg), head(msg))
			}
		}
	}
	testing.SetRealm(testing.NewUserRealm(testutils.TestAddress("hl-echo")))
	if msg := refusal(func() { NewCancelRequest(cross(cur), "\xff\xfe") }); msg != `hlgov: not a decision number: "\xff\xfe"` {
		t.Errorf("a cancel's number is not quoted: %q", msg)
	}
	if ledger.Last() != n {
		t.Fatalf("a refusal recorded a decision")
	}
}
