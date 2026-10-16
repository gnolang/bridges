package hlmailbox

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
)

// The file runs last (by name): the realm is live and has yielded, so a decision here is GovDAO's (pass).

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

// echoingVerifier refuses every message naming the metadata it was handed, as it came: a verifier GovDAO might
// activate one day, whose refusal the mailbox must not hand on raw.
type echoingVerifier struct{}

func (echoingVerifier) Verify(origin uint32, metadata, message []byte) error {
	return errors.New("hlechoverifier: refused metadata " + string(metadata))
}

func (echoingVerifier) Satisfiable(signers []Key) bool { return true }

// TestEchoesStayUTF8 (E4-GS1, E4-RL6): ParseEthAddress, every builder that reads a caller's string, the flags, the
// routes' and r/hlgov's calls, and Process's hand-on of a verifier's refusal, given bytes that are no UTF-8 or a long
// run of multi-byte runes, refuse with a text that stays valid UTF-8 however gno cuts it: the caller's string quoted
// and cut short (echo), or — a payouts flag's ids, which p/hlroute's ParseRange reads — refused before it is read.
func TestEchoesStayUTF8(cur realm, t *testing.T) {
	// the parser every address of the deployment goes through: each of its five refusals
	for _, s := range append(append([]string{}, badStrings...), "0x"+strings.Repeat("\xff", 40), strings.Repeat("\xff", 64), "0x"+strings.Repeat("\xe2\x80\x94", 21)+"ab") {
		_, err := ParseEthAddress(s)
		if err == nil || !chainSafe(err.Error()) {
			t.Errorf("ParseEthAddress of %d bytes: %v", len(s), err)
		}
	}
	if _, err := ParseEthAddress("\xff\xfe"); err == nil || !strings.Contains(err.Error(), `"\xff\xfe"`) {
		t.Errorf("ParseEthAddress does not quote what it refuses: %v", err)
	}
	guardian := ""
	for n := 1; n <= guardians; n++ {
		if Seat(n) != "" {
			guardian = string(Seat(n))
			break
		}
	}
	if guardian == "" {
		t.Fatalf("no guardian seat is held")
	}
	n := ledger.Last()
	route := launchRouteGNOT
	user := func(a string) func() { return func() { testing.SetRealm(testing.NewUserRealm(address(a))) } }
	code := func(p string) func() { return func() { testing.SetRealm(testing.NewCodeRealm(p)) } }
	for _, s := range badStrings {
		for _, c := range []struct {
			name string
			as   func()
			f    func()
		}{
			{"rotate signers: a key", user(string(member)), func() { NewRotateSignersRequest(cross(cur), s, 1, "", "") }},
			{"rotate seats: the change", user(string(member)), func() { NewRotateSeatsRequest(cross(cur), s) }},
			{"rotate seats: the seat", user(string(member)), func() { NewRotateSeatsRequest(cross(cur), s+":"+string(stranger)) }},
			{"rotate seats: the address", user(string(member)), func() { NewRotateSeatsRequest(cross(cur), "5:"+s) }},
			{"unpause: the route", user(string(member)), func() { NewUnpauseRequest(cross(cur), s, FnSends, 0) }},
			{"unpause: the functions", user(string(member)), func() { NewUnpauseRequest(cross(cur), route, s, 0) }},
			{"set verifier", user(string(member)), func() { NewSetVerifierRequest(cross(cur), 77, s) }},
			{"flag: the route", user(guardian), func() { SetFlag(cross(cur), s, FnSends, "") }},
			{"flag: the function", user(guardian), func() { SetFlag(cross(cur), route, s, "") }},
			{"flag: the ids", user(guardian), func() { SetFlag(cross(cur), route, FnPayouts, s) }},
			{"flag: a range", user(guardian), func() { SetFlag(cross(cur), route, FnPayouts, "since:"+s) }},
			{"announce a successor", code(namespace + "hlechotest"), func() { AnnounceSuccessor(cross(cur), s) }},
			{"a route's MarkDone", code(route), func() { MarkDone(cross(cur), s) }},
			{"a route's ClearFlags", code(route), func() { ClearFlags(cross(cur), s) }},
			{"a route's AddRoute", code(route), func() { AddRoute(cross(cur), s) }},
			{"a route's NotePulled", code(route), func() { NotePulled(cross(cur), s) }},
			{"r/hlgov's NoteEnrolled", code(hlgovPath), func() { NoteEnrolled(cross(cur), s, 1) }},
			{"r/hlgov's ClearEnrolled", code(hlgovPath), func() { ClearEnrolled(cross(cur), s) }},
		} {
			c.as()
			if msg := refusal(c.f); msg == "" || !chainSafe(msg) {
				t.Errorf("%s handed %d bytes: a refusal of %d bytes, valid UTF-8 %v: %q", c.name, len(s), len(msg), utf8.ValidString(msg), head(msg))
			}
		}
	}
	testing.SetRealm(testing.NewUserRealm(address(guardian)))
	if msg := refusal(func() { SetFlag(cross(cur), route, FnPayouts, "heights:1-\xff") }); !strings.HasPrefix(msg, "hlmailbox: the ids holds a byte outside ASCII, at offset 10") {
		t.Errorf("a flag's range is not refused before p/hlroute reads it: %q", msg)
	}
	if ledger.Last() != n {
		t.Fatalf("a refusal recorded a decision")
	}
	// a verifier's refusal that names the caller's bytes: activated for an origin of its own, then handed metadata
	// that is no UTF-8 — the mailbox quotes the refusal it hands on
	const origin = uint32(77)
	path := namespace + "hlechoverifier"
	testing.SetRealm(testing.NewCodeRealm(path))
	RegisterVerifier(cross(cur), echoingVerifier{})
	pass(cur, NewSetVerifierRequest(cross(cur), origin, path))
	skipDays(7)
	testing.SetRealm(testing.NewUserRealm(stranger))
	Execute(cross(cur), ledger.Last())
	if ActiveVerifier(origin) != path {
		t.Fatalf("the echoing verifier is not active for origin %d", origin)
	}
	for i, s := range badStrings {
		// a message of its own each time: Process marks a message delivered before it asks the verifier (the
		// transaction's revert undoes the mark on chain; revive here keeps it)
		msg := hl.Message{Version: hl.Version, Nonce: uint32(70 + i), Origin: origin, Destination: localDomain, Recipient: mustBytes32(recipient), Body: []byte("x")}
		testing.SetRealm(testing.NewUserRealm(recipient))
		got := refusal(func() { Process(cross(cur), []byte(s), msg.Bytes()) })
		if !strings.Contains(got, "hlechoverifier: refused metadata") || !chainSafe(got) {
			t.Errorf("Process hands on a verifier's refusal of %d bytes of metadata as %d bytes, valid UTF-8 %v: %q", len(s), len(got), utf8.ValidString(got), head(got))
		}
	}
}
