package hlfees

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

// TestEchoesStayUTF8 (E4-GS1, E4-RL6): the treasurer's builder, given bytes that are no UTF-8 or a long run of
// multi-byte runes, refuses with a text that stays valid UTF-8 however gno cuts it: the caller's string quoted and
// cut short (echo).
func TestEchoesStayUTF8(cur realm, t *testing.T) {
	for _, s := range badStrings {
		testing.SetRealm(testing.NewUserRealm(testutils.TestAddress("hl-echo")))
		if msg := refusal(func() { NewSetTreasurerRequest(cross(cur), address(s)) }); msg == "" || !chainSafe(msg) {
			t.Errorf("the treasurer handed %d bytes: a refusal of %d bytes, valid UTF-8 %v: %q", len(s), len(msg), utf8.ValidString(msg), head(msg))
		}
	}
	testing.SetRealm(testing.NewUserRealm(testutils.TestAddress("hl-echo")))
	if msg := refusal(func() { NewSetTreasurerRequest(cross(cur), address("\xff\xfe")) }); msg != `hlfees: not an address: "\xff\xfe"` {
		t.Errorf("the treasurer is not quoted: %q", msg)
	}
}
