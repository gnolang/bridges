package hlverifier

import (
	"encoding/hex"
	"strings"
	"testing"

	"gno.land/p/nt/testutils/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlmailbox"
	"gno.land/r/gov/dao"
	govinit "gno.land/r/gov/dao/init/v0"
)

const (
	solanaTestnet = uint32(1399811150)
	dayHeights    = 86400 / 5
)

var (
	member    = testutils.TestAddress("hl-member")
	recipient = testutils.TestAddress("hlm-recipient")
)

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// pass makes a mailbox decision as the governor, directly (M1k): the whole path before the yield, which this suite
// never reaches — a GovDAO proposal would be refused.
func pass(cur realm, req hlmailbox.Request) {
	testing.SetRealm(testing.NewUserRealm(hlmailbox.Governor()))
	hlmailbox.Govern(cross(cur), req)
}

// refusedByDAO: GovDAO's proxy cannot make the decision before the yield.
func refusedByDAO(cur realm, t *testing.T, req hlmailbox.Request) {
	t.Helper()
	testing.SetRealm(testing.NewUserRealm(member))
	pid := dao.MustCreateProposal(cross(cur), req.Proposal())
	testing.SetRealm(testing.NewUserRealm(member))
	dao.MustVoteOnProposalSimple(cross(cur), int64(pid), "YES")
	testing.SetRealm(testing.NewUserRealm(member))
	r := revive(func() { dao.ExecuteProposal(cross(cur), pid) })
	if r == nil {
		t.Fatalf("GovDAO's proposal executed before the yield")
	}
	if msg, _ := r.(string); !strings.Contains(msg, "until the governor yields") {
		if e, ok := r.(error); !ok || !strings.Contains(e.Error(), "until the governor yields") {
			t.Fatalf("abort %v is not the governor's refusal", r)
		}
	}
}

func asMember(cur realm) { testing.SetRealm(testing.NewUserRealm(member)) }

func errIs(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v does not contain %q", err, want)
	}
}

// keyList picks signers by index from the mailbox's set (a realm cannot make values of another realm's named type).
func keyList(idx ...int) []hlmailbox.Key {
	ks, _ := hlmailbox.Signers()
	var out []hlmailbox.Key
	for _, i := range idx {
		out = append(out, ks[i])
	}
	return out
}

func TestRegisteredAtDeploy(cur realm, t *testing.T) {
	if !Registered() || hlmailbox.ActiveVerifier(solanaTestnet) != pkgPath {
		t.Fatalf("not the registered, active launch verifier")
	}
	if !strings.Contains(Render(""), "the active verifier for origin 1399811150: 3 signers in r/hlmailbox under the rule 2 of the set") {
		t.Fatalf("page: %s", Render(""))
	}
}

// TestThreshold: the launch rule, 2 of 3.
func TestThreshold(cur realm, t *testing.T) {
	v := verifier{}
	errIs(t, v.Verify(solanaTestnet, unhex(inHelloMeta), unhex(inHelloMsg)), "")
	errIs(t, v.Verify(solanaTestnet, unhex(inMainnetMeta), unhex(inMainnetMsg)), "") // the destination is the mailbox's check, not this one's
	errIs(t, v.Verify(solanaTestnet, unhex(inOneSigMeta), unhex(inOneSigMsg)), "do not meet the rule: 2 of the set")
	errIs(t, v.Verify(1, unhex(inHelloMeta), unhex(inHelloMsg)), "from domain 1399811150, not 1")
	bad := unhex(inHelloMeta)
	bad[len(bad)-1] ^= 1
	errIs(t, v.Verify(solanaTestnet, bad, unhex(inHelloMsg)), "does not verify")
	errIs(t, v.Verify(solanaTestnet, unhex(inHelloMeta)[:68], unhex(inHelloMsg)), "no signatures")
	errIs(t, v.Verify(solanaTestnet, unhex(inHelloMeta)[:70], unhex(inHelloMsg)), "whole 65-byte signatures")
	// a signature naming a signer past the set, and two in the wrong order
	named := unhex(inHelloMeta)
	named[68] = 7
	errIs(t, v.Verify(solanaTestnet, named, unhex(inHelloMsg)), "names no signer")
	swapped := append([]byte(nil), unhex(inHelloMeta)[:68]...)
	swapped = append(swapped, unhex(inHelloMeta)[68+65:]...)
	swapped = append(swapped, unhex(inHelloMeta)[68:68+65]...)
	errIs(t, v.Verify(solanaTestnet, swapped, unhex(inHelloMsg)), "out of signer order")
	errIs(t, v.Verify(solanaTestnet, unhex(inHelloMeta), unhex(inHelloMsg)[:10]), "shorter than its header")
	if !v.Satisfiable(keyList(0, 2)) || v.Satisfiable(keyList(1)) || v.Satisfiable(nil) {
		t.Fatalf("satisfiable under 2 of 3")
	}
}

// TestTwoGroups: GovDAO rotates to "one of A and one of B"; the check and Satisfiable follow.
func TestTwoGroups(cur realm, t *testing.T) {
	ks := strings.Split(testValidators, ",")
	asMember(cur)
	govinit.InitWithUsers(cross(cur), member)
	asMember(cur)
	refusedByDAO(cur, t, hlmailbox.NewLaunchRequest(cross(cur)))
	asMember(cur)
	pass(cur, hlmailbox.NewLaunchRequest(cross(cur)))
	asMember(cur)
	pass(cur, hlmailbox.NewRotateSignersRequest(cross(cur), testValidators, 0, ks[0], ks[1]+","+ks[2]))
	testing.SkipHeights(7*dayHeights + 1)
	testing.SetRealm(testing.NewUserRealm(recipient))
	hlmailbox.Execute(cross(cur), 2)
	_, rule := hlmailbox.Signers()
	if !rule.TwoGroups() {
		t.Fatalf("rule %+v", rule)
	}
	v := verifier{}
	errIs(t, v.Verify(solanaTestnet, unhex(inHelloMeta), unhex(inHelloMsg)), "")     // signers 0 and 2: one of each
	errIs(t, v.Verify(solanaTestnet, unhex(inMainnetMeta), unhex(inMainnetMsg)), "") // 0 and 1
	errIs(t, v.Verify(solanaTestnet, unhex(inOneSigMeta), unhex(inOneSigMsg)), "do not meet the rule: one of group A")
	errIs(t, v.Verify(solanaTestnet, unhex(inNoGroupAMeta), unhex(inNoGroupAMsg)), "do not meet the rule") // 1 and 2: both group B
	if !v.Satisfiable(keyList(0, 1)) || v.Satisfiable(keyList(1, 2)) || v.Satisfiable(keyList(0)) {
		t.Fatalf("satisfiable under two groups")
	}
	if !strings.Contains(Render(""), "under the rule one of group A ("+ks[0]+")") {
		t.Fatalf("page: %s", Render(""))
	}
	// the mailbox takes a message through this verifier
	testing.SetRealm(testing.NewUserRealm(recipient))
	hlmailbox.Process(cross(cur), unhex(inHelloMeta), unhex(inHelloMsg))
}
