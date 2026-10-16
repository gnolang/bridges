package hlgnot

import (
	"chain"
	"chain/runtime"
	"encoding/hex"
	"strconv"
	"testing"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlmailbox"
)

// The migration gate's probe (the round-5 realms audit's E5-RL1): eth/scripts/gnotest.sh runs this suite a second time
// with -run 'TestSetup$|TestTxGas' — TestSetup launches the route, TestTxGasSetup makes a migration and pulls its page
// 0 — and holds TestTxGasPull's gas, one page of a migration at its bound, under a third of gnoland-1's block at its
// node figure (the migration gate). In the whole suite the route has migrated already (TestMigrate): both skip.

// txgasSucc is the successor the probe's migration names: a code realm here, which announces itself and pulls.
const txgasSucc = namespace + "hlgnotgas"

var txgasReady bool

// txgasID is the i-th probe record's id, kind a byte of its own: the claims sort by i, so each page takes the next
// pullPage of them.
func txgasID(kind byte, i int) string {
	var b [32]byte
	b[0], b[1], b[2], b[3], b[31] = byte(i>>24), byte(i>>16), byte(i>>8), byte(i), kind
	return hex.EncodeToString(b[:])
}

// TestTxGasSetup: 2×pullPage claims waiting, each to a recipient of its own, credited as Process credits them
// (st.Credit, enter, tidy) beside a send of their own; a migrate to txgasSucc, applied; page 0 pulled with the lock.
func TestTxGasSetup(cur realm, t *testing.T) {
	if Migrated() != "" {
		t.Skip("the route has migrated (the whole suite): the migration gate's probe runs alone")
	}
	epoch, _ := hlmailbox.PayoutsEpoch(pkgPath)
	for i := 0; i < 2*pullPage; i++ {
		to := chain.PackageAddress("probe/recipient/" + strconv.Itoa(i))
		st.Send(now(), txgasID(0x5e, i), to, 100*gnot)
		st.Credit(now(), runtime.ChainHeight(), epoch, txgasID(0xc1, i), to, 25*gnot, hlroute.BySigners, false)
		enter(txgasID(0xc1, i))
		tidy(tidyStep)
	}
	testing.SetRealm(testing.NewCodeRealm(txgasSucc))
	hlmailbox.AnnounceSuccessor(cross(cur), "GNOT")
	asMember(cur)
	pass(cur, NewMigrateRequest(cross(cur), txgasSucc))
	skipDays(7)
	asRelayer(cur)
	Execute(cross(cur), st.Ledger.Last())
	fundLock()
	testing.SetRealm(testing.NewCodeRealm(txgasSucc))
	if snap := Pull(cross(cur), 0); Migrated() != txgasSucc || len(snap.Claims) != pullPage || !snap.More || waitingByPos.Size() != pullPage {
		t.Fatalf("page 0: migrated %q, %d claims, more %v; the index holds %d", Migrated(), len(snap.Claims), snap.More, waitingByPos.Size())
	}
	txgasReady = true
}

// TestTxGasPull: page 1 of the migration — pullPage open claims, each with its entry in the waiting index — as the
// successor pulls it: the predecessor's half of a migration page (hlgnot2's TestTxGasAdopt holds the whole page).
func TestTxGasPull(cur realm, t *testing.T) {
	if !txgasReady {
		t.Skip("no probe migration (the whole suite)")
	}
	testing.SetRealm(testing.NewCodeRealm(txgasSucc))
	if snap := Pull(cross(cur), 1); len(snap.Claims) != pullPage || snap.Claims[0].State != hlroute.Open {
		t.Fatalf("page 1: %d claims", len(snap.Claims))
	}
}
