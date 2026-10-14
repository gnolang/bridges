package sol

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"
)

// The admin program's Handle accounts in the order its `handle` reads them (v27, M2-NOTES 9): the program's
// HandleAccountMetas answer — the state, the decision PDA, the system program — then the payer appended as a
// writable signer, and a cancel's authority PDA and pairs after it. The e2e's cancel failed with WrongAccount when
// the authority PDA was put before the payer, as the v26 program's answer listed it.
func TestHandleAccountsFollowTheProgram(t *testing.T) {
	a := Admin{Program: pk(0x33)}
	payer := pk(4)
	check := func(name string, got solana.AccountMetaSlice, want []solana.PublicKey) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %d accounts, want %d", name, len(got), len(want))
		}
		for i, k := range want {
			if !got[i].PublicKey.Equals(k) {
				t.Fatalf("%s: account %d is %s, want %s", name, i, got[i].PublicKey, k)
			}
		}
		if p := got[3]; !p.PublicKey.Equals(payer) || !p.IsSigner || !p.IsWritable {
			t.Fatalf("%s: the payer is not account 3 as a writable signer: %+v", name, p)
		}
		for i, m := range got[:3] {
			if m.IsSigner {
				t.Fatalf("%s: account %d asked to sign", name, i)
			}
		}
	}
	metas := a.HandleMetas(7)
	if !SameMetas(metas, solana.AccountMetaSlice{w(a.State()), w(a.Decision(7)), r(SystemProgram)}) || !metas[0].IsWritable || !metas[1].IsWritable || metas[2].IsWritable {
		t.Fatalf("the expected HandleAccountMetas answer: %+v", metas)
	}
	check("set fee", a.HandleAccounts(metas, KindSetFee, payer, nil),
		[]solana.PublicKey{a.State(), a.Decision(7), SystemProgram, payer})
	pairs := []CancelPair{{3, pk(5)}, {6, pk(6)}}
	check("cancel", a.HandleAccounts(a.HandleMetas(8), KindCancel, payer, pairs),
		[]solana.PublicKey{a.State(), a.Decision(8), SystemProgram, payer, a.Authority(),
			a.Decision(3), pk(5), a.Decision(6), pk(6)})
	// a simulated answer is taken as it comes; one that is not the program's is told apart
	if SameMetas(a.HandleMetas(8), a.HandleMetas(9)) || SameMetas(metas, metas[:2]) {
		t.Fatal("SameMetas")
	}
	if n := len(a.HandleAccounts(nil, KindCancel, payer, nil)); n != 2 {
		t.Fatalf("an empty answer: %d accounts", n)
	}
}

// AT3: InitIx carries the number the sequence starts at as its last field — u64 little-endian after the
// predecessor — 0 for a chain's first deployment, r/hlgov's Last() for a fresh deployment under the same
// r/hlgov; SuccessorInitIx carries 0 (a successor's number comes by the hand-over). The programs' suite pins the
// same bytes (`the_hand_overs_wire_contract_is_frozen`), and the vectors pin admin_init and admin_init_fresh_37.
func TestInitIxCarriesTheStartingNumber(t *testing.T) {
	a := Admin{Program: pk(0x33)}
	var seats [Seats]solana.PublicKey
	var watch [Watch]solana.PublicKey
	for i := range seats {
		seats[i] = pk(byte(0x0b + i))
	}
	for i := range watch {
		watch[i] = pk(byte(0x0f + i))
	}
	d, err := a.InitIx(pk(4), pk(4), seats, watch, 1198419788, solana.PublicKey{}, 37).Data()
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 1+192+4+32+8 || d[0] != 0 {
		t.Fatalf("the init's %d bytes, tag %d", len(d), d[0])
	}
	if got := d[229:]; !bytes.Equal(got, []byte{37, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("the starting number is not the last field, u64 LE: %x", got)
	}
	if got := d[197:229]; !bytes.Equal(got, make([]byte, 32)) {
		t.Fatalf("a launch init's predecessor is not zero: %x", got)
	}
	s, _ := a.SuccessorInitIx(pk(4), pk(4), 1198419788, pk(0x44)).Data()
	if !bytes.Equal(s[197:229], pk(0x44).Bytes()) || !bytes.Equal(s[229:], make([]byte, 8)) || !bytes.Equal(s[1:193], make([]byte, 192)) {
		t.Fatalf("a successor's init: predecessor %x, number %x, seats and watch %x", s[197:229], s[229:], s[1:193])
	}
}

// A pending decision's account is M2j's layout, the refund key its last field (no channel, trigger or marker);
// the length is exact, the account's room for a refund key (32 zero bytes while it is None) counted, and the
// overhead beside the parameters is DecisionOverhead. A replacement's floor under a hold comes from the state's
// hold history (AN4).
func TestParseDecisionAndTheFloorFromTheHistory(t *testing.T) {
	params := EncodeParams(&Params{Kind: KindSetGuardians, Seats: []SeatChange{{3, pk(7)}}})
	encode := func(refund *solana.PublicKey, pad int) []byte {
		var e enc
		e.u8(1)
		e.u64(5)
		e.u8(KindSetGuardians)
		e.vec(params)
		e.u64(100)
		e.u64(1_700_000_000)
		e.key(pk(9))
		e.optKey(refund)
		for i := 0; i < pad; i++ {
			e.u8(0)
		}
		return e.Bytes()
	}
	d, err := ParseDecision(encode(nil, 32))
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != KindSetGuardians || d.Number != 5 || d.DeliveredTime != 1_700_000_000 || !d.Deliverer.Equals(pk(9)) || d.RefundKey != nil {
		t.Fatalf("parsed: %+v", d)
	}
	refund := pk(8)
	if full := encode(&refund, 0); len(full) != DecisionOverhead+len(params) {
		t.Fatalf("the overhead beside the parameters is %d, want %d", len(full)-len(params), DecisionOverhead)
	}
	if d, err := ParseDecision(encode(&refund, 0)); err != nil || d.RefundKey == nil || !d.RefundKey.Equals(refund) {
		t.Fatalf("with a refund key the account is full: %+v %v", d, err)
	}
	if _, err := ParseDecision(encode(nil, 33)); err == nil {
		t.Fatal("a trailing byte parsed")
	}
	if _, err := ParseDecision(encode(&refund, 32)); err == nil {
		t.Fatal("spare bytes with a refund key parsed")
	}
	old := append(encode(nil, 0), make([]byte, 1+8+32)...) // the pre-M2j layout: a trigger and a marker, then the spare
	if _, err := ParseDecision(old); err == nil {
		t.Fatal("the pre-M2j layout (a trailing trigger and marker) parsed")
	}
	doctored := encode(nil, 32)
	doctored[len(doctored)-1] = 1
	if _, err := ParseDecision(doctored); err == nil {
		t.Fatal("a non-zero spare byte parsed")
	}
	// the floor while a hold stands is 21 days after the earliest hold overlapping [delivery, now]: the standing
	// one alone with no history, an ended one whose end is at or after the delivery otherwise (AN4)
	hold := &Flag{Time: 1_700_000_000 + 5*Day}
	if d.MaturesAt(1, hold, nil) != hold.Time+21*Day || d.FloorUnder(hold, nil, 1) != hold.Time+21*Day {
		t.Fatal("no history: the standing hold's 21st day")
	}
	st := &AdminState{Holds: []Hold{{Start: d.DeliveredTime - 10*Day, End: d.DeliveredTime - Day}, {Start: d.DeliveredTime - 2*Day, End: d.DeliveredTime + 3*Day}}}
	if got := d.MaturesAt(1, hold, st); got != d.DeliveredTime-2*Day+21*Day {
		t.Fatalf("the earliest hold overlapping the pending life gives the floor: %d", got-d.DeliveredTime)
	}
	if got := st.ReplacementFloor(d.DeliveredTime, hold.Time, 21*Day); got != d.DeliveredTime-2*Day+21*Day {
		t.Fatalf("ReplacementFloor: %d", got)
	}
	// a hold that ended before the delivery does not count; a later hold (two keys re-flagging at the grace's last
	// second) never raises the floor past the earliest overlapping one
	st.Holds = st.Holds[:1]
	if got := d.MaturesAt(1, hold, st); got != hold.Time+21*Day {
		t.Fatalf("a hold ended before the delivery counted: %d", got)
	}
	later := &Flag{Time: hold.Time + 5*Day}
	st.Holds = append(st.Holds, Hold{Start: hold.Time, End: later.Time - 1})
	if got := d.MaturesAt(1, later, st); got != hold.Time+21*Day {
		t.Fatalf("a later hold raised the floor: %d", got)
	}
	if d.MaturesAt(1, nil, st) != d.DeliveredTime+Week(1) {
		t.Fatal("with no hold standing the floor is moot: the 7th day")
	}
	if d.MaturesAt(8, nil, nil) != d.DeliveredTime+Week(8) {
		t.Fatal("the factor divides the week")
	}
}

// The admin state's M2j layout parses: the cancelled ranges (no channel), the hold history, the one pending count,
// the predecessor and whether it inherited, with the account's reserve (zeros) behind them; the pausable state's
// mint limits parse with their bucket.
func TestParseAdminAndPausableStateR1(t *testing.T) {
	var e enc
	e.u8(1)
	e.u8(8)
	e.u8(1)
	e.u32(1198419796)
	e.u64(3)
	for i := 0; i < 6; i++ {
		e.key(pk(byte(10 + i)))
	}
	for i := 0; i < 6; i++ {
		e.u8(0)
	}
	e.u8(0)
	e.u8(0)
	e.u8(0) // no successor
	e.u8(254)
	e.u32(1)
	e.u64(1)
	e.u64(1)
	e.u32(2)
	e.i64(100)
	e.i64(200)
	e.i64(300)
	e.i64(400)
	e.u32(1)
	e.key(pk(0x33))
	e.u8(1)
	raw := append(e.Bytes(), make([]byte, 500)...) // the reserve
	st, err := ParseAdminState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Cancelled) != 1 || st.Cancelled[0] != (CancelledRange{1, 1}) || !st.IsCancelled(1) || st.IsCancelled(2) {
		t.Fatalf("cancelled: %+v", st.Cancelled)
	}
	if len(st.Holds) != 2 || st.Holds[1] != (Hold{300, 400}) || st.Pending != 1 || !st.Predecessor.Equals(pk(0x33)) || st.Inherited != 1 || st.LastSeq != 3 {
		t.Fatalf("state: %+v", st)
	}
	if _, err := ParseAdminState(raw[:len(raw)-520]); err == nil {
		t.Fatal("a short state parsed")
	}
	var p enc
	p.u8(1)
	p.u8(8)
	p.key(pk(1))
	p.key(pk(2))
	p.u8(1)
	p.u64(25_000_000_000)
	p.u64(100_000_000_000)
	p.u64(50_000_000_000)
	p.i64(1_700_000_000)
	p.u64(7)
	p.u64(9)
	p.u64(0)
	p.i64(0)
	ps, err := ParsePausableState(p.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if ps.Limits[0].MaxPerMessage != 25_000_000_000 || ps.Limits[0].Used != 50_000_000_000 || ps.Limits[1].DayCap != 9 {
		t.Fatalf("limits: %+v", ps.Limits)
	}
	day := UnpauseDelay(8)
	l := ps.Limits[0]
	if l.Level(l.UsedAt, day) != 50_000_000_000 || l.Level(l.UsedAt+day/4, day) != 25_000_000_000 || l.Level(l.UsedAt+day, day) != 0 || l.Level(l.UsedAt-5, day) != 50_000_000_000 {
		t.Fatalf("the bucket's level: %d %d %d", l.Level(l.UsedAt, day), l.Level(l.UsedAt+day/4, day), l.Level(l.UsedAt+day, day))
	}
	// the v27 pausable state (no limits) is refused
	if _, err := ParsePausableState(p.Bytes()[:67]); err == nil {
		t.Fatal("the v27 pausable state parsed")
	}
}

// Kind 1's parameters start with the salt (v27 §13) and their channel byte must be 0 (M2j: any other value is
// spent); kind 4 is reserved; the error names are M2j's compacted table.
func TestSaltedCancelAndReserved(t *testing.T) {
	p := &Params{Kind: KindCancel, Salt: 0x0102030405060708, Cancel: []CancelRange{{0, 2, 2}, {0, 5, 5}}}
	enc := EncodeParams(p)
	if len(enc) != 8+2+2*9 || enc[0] != 1 || enc[7] != 8 || enc[8] != 0 || enc[9] != 2 {
		t.Fatalf("encoded %x", enc)
	}
	q, err := ParseParams(KindCancel, enc)
	if err != nil || q.Salt != p.Salt || len(q.Cancel) != 2 || q.Cancel[1].From != 5 {
		t.Fatalf("parsed %+v: %v", q, err)
	}
	// the body: kind ‖ seq ‖ salt ‖ form ‖ …, as r/hlgov dispatches it; the parameters are the bytes past the prefix
	body := DecisionBody(KindCancel, 3, enc)
	if len(body) != 9+len(enc) || body[0] != 1 || body[8] != 3 {
		t.Fatalf("body %x", body)
	}
	if q, err := ParseParams(KindCancel, body[9:]); err != nil || q.Salt != p.Salt {
		t.Fatalf("the parameters past the prefix: %+v %v", q, err)
	}
	// a range form carries the salt too, and a body short of the salt does not parse
	r := &Params{Kind: KindCancel, Salt: 7, Cancel: []CancelRange{{0, 2, 9}}}
	if e := EncodeParams(r); len(e) != 8+1+1+16 || e[8] != 1 {
		t.Fatalf("range %x", e)
	}
	if _, err := ParseParams(KindCancel, []byte{0, 1, 0, 0, 0, 0, 0, 0, 2}); err == nil {
		t.Fatal("a v26 body (no salt) parsed")
	}
	if _, err := ParseParams(KindCancel, EncodeParams(&Params{Kind: KindCancel, Salt: 7, Cancel: []CancelRange{{1, 2, 9}}})); !errors.Is(err, ErrParams) {
		t.Fatal("a cancel naming channel 1 parsed (there is one channel since M2j)")
	}
	// kind 4 is reserved: nothing parses, nothing encodes, no extra accounts
	if _, err := ParseParams(KindReserved, []byte{1, 2, 3}); !errors.Is(err, ErrParams) || !errors.Is(err, ErrReserved) {
		t.Fatalf("kind 4: %v", err)
	}
	if _, err := ParseParams(KindReserved, nil); !errors.Is(err, ErrReserved) {
		t.Fatalf("kind 4 empty: %v", err)
	}
	if len(EncodeParams(&Params{Kind: KindReserved})) != 0 {
		t.Fatal("kind 4 encodes something")
	}
	if extra, fl := (Admin{}).ExtraFor(Programs{}, KindReserved, nil); extra != nil || fl {
		t.Fatal("kind 4 takes extra accounts")
	}
	if AdminError(3023) != "admin program: WrongAccount" || AdminError(3024) != "admin program: WrongDeliverer" || AdminError(3025) != "admin program: Retired" || AdminError(3026) != "admin program: NeedsExtend" || AdminError(3027) != "admin program: NotInherited" || AdminError(3028) != "" {
		t.Fatalf("error names: %q %q", AdminError(3024), AdminError(3027))
	}
	if PausableError(2017) != "pausable ISM: LayoutFixed" || PausableError(2018) != "pausable ISM: NotTheMailbox" || PausableError(2020) != "pausable ISM: OverMax" || PausableError(2022) != "pausable ISM: BadToken" || PausableError(2023) != "" {
		t.Fatalf("pausable error names: %q", PausableError(2020))
	}
	if ClientError(1034) != "client: HistoryFull" || ClientError(1035) != "client: ChunkOutOfOrder" || ClientError(1036) != "client: RoundMismatch" || ClientError(1037) != "" {
		t.Fatalf("client error names: %q", ClientError(1034))
	}
	if ProgramErrorName(3027) != "admin program: NotInherited" || ProgramErrorName(2017) != "pausable ISM: LayoutFixed" {
		t.Fatal("ProgramErrorName")
	}
	// kind 12 (AN5): token u8 ‖ max u64 ‖ day cap u64, 17 bytes; a third token does not parse (spent); the 7-day
	// group; its Execute accounts are the pausable ISM's state (writable) and program
	ml := &Params{Kind: KindSetMintLimits, Token: 1, Max: 7_000_000_000, DayCap: 9_000_000_000}
	mle := EncodeParams(ml)
	if len(mle) != 17 || mle[0] != 1 {
		t.Fatalf("kind 12 encodes to %x", mle)
	}
	if q, err := ParseParams(KindSetMintLimits, mle); err != nil || q.Token != 1 || q.Max != ml.Max || q.DayCap != ml.DayCap {
		t.Fatalf("kind 12 parsed %+v: %v", q, err)
	}
	if _, err := ParseParams(KindSetMintLimits, append([]byte{2}, mle[1:]...)); !errors.Is(err, ErrParams) {
		t.Fatal("a third token parsed")
	}
	if !SevenDay(KindSetMintLimits) || !MailboxKind(KindSetMintLimits) || MailboxKind(10) || MailboxKind(11) || KindName(10) != "unknown" || KindName(11) != "unknown" || KindName(12) != "set mint limits" {
		t.Fatal("kind 12's groups")
	}
	if s := DescribeParams(KindSetMintLimits, mle); !strings.Contains(s, "token 1's mint limits: 7000000000 a message, 9000000000 a day") {
		t.Fatalf("kind 12: %s", s)
	}
	progs := Programs{Pausable: pk(0x22), Client: pk(0x11)}
	if extra, fl := (Admin{}).ExtraFor(progs, KindSetMintLimits, ml); len(extra) != 2 || !extra[0].PublicKey.Equals(progs.PausableISM().State()) || !extra[0].IsWritable || !extra[1].PublicKey.Equals(pk(0x22)) || fl {
		t.Fatalf("kind 12's accounts: %+v", extra)
	}
	// kind 9 names the client's state (writable) before the client program, then the successor's state and the
	// system program (M2j: the client's state is among no kind's fixed accounts); kinds 10 and 11 are unknown
	succ := pk(0x77)
	// the successor's authority PDA, the fourth, is writable (S-25: the hand-over sweeps the float to it)
	if extra, fl := (Admin{}).ExtraFor(progs, KindTransfer, &Params{Kind: KindTransfer, Program: succ}); len(extra) != 21 || !extra[3].PublicKey.Equals(PDA(succ, b("authority"))) || !extra[3].IsWritable || !extra[17].PublicKey.Equals(progs.LightClient().State()) || !extra[17].IsWritable || !extra[18].PublicKey.Equals(pk(0x11)) || !extra[19].PublicKey.Equals(PDA(succ, b("state"))) || !extra[19].IsWritable || !extra[20].PublicKey.Equals(SystemProgram) || fl {
		t.Fatalf("kind 9's accounts: %d %+v", len(extra), extra)
	}
	if extra, _ := (Admin{}).ExtraFor(progs, 11, nil); extra != nil || DescribeParams(11, []byte{1}) != "unknown kind 11: recorded and spent" {
		t.Fatal("kind 11 is unknown since M2j")
	}
	// what hlwatch and the audit say of a decision: a cancel with its salt, kind 4 reserved, a bad body spent
	if s := DescribeParams(KindCancel, enc); !strings.Contains(s, "salt 0x0102030405060708") || !strings.Contains(s, "0/2,0/5") {
		t.Fatalf("a cancel: %s", s)
	}
	if s := DescribeParams(KindCancel, EncodeParams(r)); !strings.Contains(s, "0/2-9") || !strings.Contains(s, "salt 0x0000000000000007") {
		t.Fatalf("a range cancel: %s", s)
	}
	if s := DescribeParams(KindReserved, []byte{9}); !strings.Contains(s, "reserved") || !strings.Contains(s, "spent") {
		t.Fatalf("kind 4: %s", s)
	}
	if s := DescribeParams(KindSetFee, []byte{1}); !strings.Contains(s, "do not parse") {
		t.Fatalf("a bad body: %s", s)
	}
	if s := DescribeParams(KindSetFee, EncodeParams(&Params{Kind: KindSetFee, Lamports: 1000, Beneficiary: pk(9)})); !strings.Contains(s, "1000 lamports") {
		t.Fatalf("a fee: %s", s)
	}
	if s := DescribeParams(13, nil); !strings.Contains(s, "unknown kind 13") || KindName(13) != "unknown" || KindName(KindReserved) != "reserved" || KindName(KindCancel) != "cancel" {
		t.Fatalf("an unknown kind: %s", s)
	}
}
