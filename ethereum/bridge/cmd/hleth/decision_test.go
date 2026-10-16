package main

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// The bodies are r/hlgov's, byte for byte: each vector below is one of E-M1's realm tests
// (eth/realm/hlgov/hlgov_test.gno: bodyHex(kind, seq, params)), with the same parameters.

func padHex(a string) string {
	return strings.Repeat("0", 24) + strings.ToLower(strings.TrimPrefix(a, "0x"))
}

func TestBodiesAreRHlgovs(t *testing.T) {
	keyA := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	keyB := common.HexToAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC")
	gate := common.HexToAddress("0x0202020202020202020202020202020202020202")
	mailbox := common.HexToAddress("0x0404040404040404040404040404040404040404")
	var hashC [32]byte
	for i := range hashC {
		hashC[i] = 0xcc
	}
	var route [32]byte
	copy(route[12:], common.HexToAddress("0x3cb9d0388661c46641f59dc080f10c16aa570681").Bytes())
	seq := func(n byte) string { return "00000000000000" + hex.EncodeToString([]byte{n}) }
	must := func(b []byte, err error) []byte {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	guards := must(guardiansParams([]seatKey{{1, keyA}, {5, keyB}}))
	cases := []struct {
		name string
		got  []byte
		want string
	}{
		{"unpause HlAdmin", decisionBody(kindUnpause, 1, []byte{1}), "02" + seq(1) + "01"},
		{"token 1's ISM to none", decisionBody(kindTokenISM, 2, tokenISMParams(1, common.Address{})), "03" + seq(2) + "01" + strings.Repeat("00", 32)},
		{"token 0's ISM to the gate", decisionBody(kindTokenISM, 3, tokenISMParams(0, gate)), "03" + seq(3) + "00" + padHex(gate.Hex())},
		{"enroll GNOT's router for GnoT", decisionBody(kindRouter, 4, routerParams(0, 1198419796, route)), "06" + seq(4) + "00" + "476e6f54" + hex.EncodeToString(route[:])},
		{"protocol fee 256 wei", decisionBody(kindFee, 5, feeParams(256, keyB)), "07" + seq(5) + "0000000000000100" + padHex(keyB.Hex())},
		{"upgrade the mailbox", decisionBody(kindUpgrade, 6, must(upgradeParams(mailbox, keyB, hashC, nil))), "08" + seq(6) + padHex(mailbox.Hex()) + padHex(keyB.Hex()) + hex.EncodeToString(hashC[:])},
		{"successor", decisionBody(kindSuccessor, 7, successorParams(keyA, hashC)), "09" + seq(7) + padHex(keyA.Hex()) + hex.EncodeToString(hashC[:])},
		{"guardians", decisionBody(kindGuardians, 8, guards), "05" + seq(8) + "02" + "01" + padHex(keyA.Hex()) + "05" + padHex(keyB.Hex())},
		{"cancel range 4–7", decisionBody(kindCancel, 10, must(cancelRangeParams(0x0102030405060708, 4, 7))), "01" + seq(10) + "0102030405060708" + "01" + "00" + "0000000000000004" + "0000000000000007"},
		{"cancel list 3, 5", decisionBody(kindCancel, 11, must(cancelListParams(9, []uint64{3, 5}))), "01" + seq(11) + "0000000000000009" + "00" + "02" + "00" + "0000000000000003" + "00" + "0000000000000005"},
		{"mint limits 25,000/25,000", decisionBody(kindMintLimits, 12, mintLimitsParams(0, 25_000_000_000, 25_000_000_000)), "0c" + seq(12) + "00" + "00000005d21dba00" + "00000005d21dba00"},
		{"mint limits 25,000/100,000", decisionBody(kindMintLimits, 13, mintLimitsParams(1, 25_000_000_000, 100_000_000_000)), "0c" + seq(13) + "01" + "00000005d21dba00" + "000000174876e800"},
	}
	for _, c := range cases {
		if got := hex.EncodeToString(c.got); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
		// every body's parameters pass HlAdmin's own checks, and say what they do
		if _, err := describeParams(c.got[0], c.got[9:]); err != nil {
			t.Errorf("%s: HlAdmin would spend it: %v", c.name, err)
		}
	}
}

// HlAdmin's checks (HlAdmin._parseCancel, HlAdminExec.run) refuse what it spends: each malformed parameter is named.
func TestDescribeRefusesWhatHlAdminSpends(t *testing.T) {
	nonAddr := make([]byte, 32)
	nonAddr[0] = 1
	cases := []struct {
		kind uint8
		p    string
	}{
		{kindCancel, "00"}, // shorter than its salt and form
		{kindCancel, "0000000000000000" + "00" + "00"},                                           // a list of none
		{kindCancel, "0000000000000000" + "00" + "01" + "01" + "0000000000000003"},               // channel 1
		{kindCancel, "0000000000000000" + "01" + "00" + "0000000000000007" + "0000000000000004"}, // a range backwards
		{kindCancel, "0000000000000000" + "02" + "00"},                                           // form 2
		{kindUnpause, "02"},                                                                  // object 2
		{kindUnpause, "0000"},                                                                // two bytes
		{kindTokenISM, "02" + strings.Repeat("00", 32)},                                      // token 2
		{kindTokenISM, "00" + hex.EncodeToString(nonAddr)},                                   // no address
		{kindGuardians, "05" + strings.Repeat("00", 33*5)},                                   // five changes
		{kindGuardians, "01" + "01" + hex.EncodeToString(nonAddr)},                           // no address
		{kindRouter, "00" + "476e6f54"},                                                      // short
		{kindFee, "00038d7ea4c68001" + padHex("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC")}, // above 0.001 ETH
		{kindFee, "0000000000000001" + strings.Repeat("00", 32)},                             // the zero beneficiary
		{kindUpgrade, strings.Repeat("00", 95)},                                              // short
		{kindSuccessor, strings.Repeat("00", 63)},                                            // short
		{kindMintLimits, "02" + strings.Repeat("00", 16)},                                    // token 2
		{10, ""}, // reserved: recorded and spent at recording
	}
	for _, c := range cases {
		p, _ := hex.DecodeString(c.p)
		if _, err := describeParams(c.kind, p); err == nil {
			t.Errorf("kind %d params %s: no refusal", c.kind, c.p)
		}
	}
	if _, err := cancelListParams(1, make([]uint64, 17)); err == nil {
		t.Error("a list of 17 numbers was built")
	}
	if _, err := guardiansParams([]seatKey{{1, common.HexToAddress("0x1")}, {2, common.HexToAddress("0x2")}, {3, common.HexToAddress("0x3")}}); err == nil {
		t.Error("three guardian seats in one decision were built")
	}
	if _, err := upgradeParams(common.Address{}, common.Address{}, [32]byte{}, make([]byte, maxCall+1)); err == nil {
		t.Error("an upgrade call past the 512-byte body was built")
	}
}

// The maturity of each kind is HlAdmin.execute's (factor 8 here: a day is 3 hours, a week 21).
func TestMaturityIsHlAdmins(t *testing.T) {
	const factor = 8
	c := clocksOf(factor)
	delivered := int64(1_000_000)
	st := func() *ethx.AdminState { return &ethx.AdminState{ClockFactor: factor} }
	dec := func(kind uint8, params ...byte) *ethx.Decision {
		return &ethx.Decision{Number: 7, Kind: kind, DeliveredBlock: 10, DeliveredTime: uint64(delivered), Params: params}
	}
	// an unpause: 24 hours (at the factor) after delivery, and never deferred by a flag
	s := st()
	s.Flags[0] = ethx.Flag{BlockNumber: 5, Time: 1}
	if m := maturityOf(dec(kindUnpause, 1), s, ethx.Flag{}, delivered+c.day-1); m.Ready || m.At != delivered+c.day {
		t.Fatalf("an unpause a second early: %+v", m)
	}
	if m := maturityOf(dec(kindUnpause, 1), s, ethx.Flag{}, delivered+c.day); !m.Ready {
		t.Fatalf("an unpause at its day: %+v", m)
	}
	// ... spent under a hold on its object, said so (it still executes: HlAdmin spends it)
	if m := maturityOf(dec(kindUnpause, 0), st(), ethx.Flag{BlockNumber: 3, Time: 5}, delivered+c.day); !m.Ready || !strings.Contains(m.Deferred, "the gate") {
		t.Fatalf("an unpause of the gate under the gate's hold: %+v", m)
	}
	// the 7-day group: deferred by a lone flag on HlAdmin, and by a hold
	if m := maturityOf(dec(kindMintLimits, 0), s, ethx.Flag{}, delivered+c.week); m.Ready || !strings.Contains(m.Deferred, "flag") {
		t.Fatalf("a kind 12 under a flag: %+v", m)
	}
	if m := maturityOf(dec(kindMintLimits, 0), st(), ethx.Flag{}, delivered+c.week); !m.Ready {
		t.Fatalf("a kind 12 on its 7th day: %+v", m)
	}
	// kind 5 lands through a lone flag on its 7th day
	if m := maturityOf(dec(kindGuardians), s, ethx.Flag{}, delivered+c.week); !m.Ready {
		t.Fatalf("a kind 5 under a lone flag: %+v", m)
	}
	// ... and under a hold no sooner than the 21st day of the earliest hold overlapping its pending life
	h := st()
	holdStart := delivered + 2*c.day
	h.Hold = ethx.Flag{BlockNumber: 20, Time: uint64(holdStart)}
	h.Holds = []ethx.Hold{{Start: uint64(delivered - c.day), End: uint64(delivered + c.day)}} // an earlier hold that ended after the delivery
	m := maturityOf(dec(kindGuardians), h, ethx.Flag{}, delivered+c.week)
	if want := delivered - c.day + c.day21; m.At != want || m.Ready {
		t.Fatalf("a kind 5 under a hold with an overlapping earlier one: matures %d, want %d (%+v)", m.At, want, m)
	}
	// a hold that ended before the delivery does not lower the floor
	h.Holds = []ethx.Hold{{Start: uint64(delivered - 5*c.day), End: uint64(delivered - c.day)}}
	if m := maturityOf(dec(kindGuardians), h, ethx.Flag{}, 0); m.At != holdStart+c.day21 {
		t.Fatalf("a kind 5 under a hold: matures %d, want the standing hold's 21st day %d", m.At, holdStart+c.day21)
	}
	// a number in a cancelled range closes at its next execute
	cr := st()
	cr.Cancelled = [][2]uint64{{5, 9}}
	if m := maturityOf(dec(kindUpgrade), cr, ethx.Flag{}, delivered); !m.Ready || !strings.Contains(m.Deferred, "cancelled") {
		t.Fatalf("a cancelled number: %+v", m)
	}
}

// HlAdmin's refusals are named from their selectors alone, as a provider's revert data gives them: ethx's decoding
// knows HlAdmin's, HlAdminExec's and the Guardians library's errors (the file-level NoCodeYet among them).
func TestRevertNameByHlAdminsSelectors(t *testing.T) {
	for _, sig := range []string{"NotMature()", "Deferred()", "WrongDecision()", "NoCodeYet(address)", "AuthorityLost(address)", "SetupIncomplete(uint8)", "NotDelegated()", "CurrentKey()", "ZeroSetting()", "CallStarved()"} {
		data := append(crypto.Keccak256([]byte(sig))[:4], make([]byte, 32*strings.Count(sig, ",")+32*boolInt(!strings.HasSuffix(sig, "()")))...)
		err := ethx.DecodeRevert(data, "execution reverted")
		if got, want := revertName(err), sig[:strings.Index(sig, "(")]; got != want {
			t.Errorf("%s named %q", sig, got)
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestBurnRecipientRefusesABridgeRealm(t *testing.T) {
	const route = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlbubble"
	if _, err := burnRecipient("g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5", []string{route}); err != nil {
		t.Fatalf("a user's address refused: %v", err)
	}
	addr := hl.GnoPackageAddress(route)
	if _, err := burnRecipient(addr, []string{"", route}); err == nil || !strings.Contains(err.Error(), "a bridge realm") {
		t.Fatalf("the route realm's own address as the recipient: %v", err)
	}
	if _, err := burnRecipient("0x1234", nil); err == nil {
		t.Fatal("no gno address accepted")
	}
}
