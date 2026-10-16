package hlgnot2

import (
	"chain"
	"strconv"
	"testing"

	"gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/hlgnot"
)

// The migration gate's probe (the round-5 realms audit's E5-RL1): eth/scripts/gnotest.sh runs this suite a second time
// with -run 'TestSetup$|TestTxGas' — TestSetup launches the realms under the verifier that takes anything,
// TestTxGasSetup makes hlgnot's migration to this realm and adopts its page 0 — and holds TestTxGasAdopt's gas, one
// page at its bound adopted whole (hlgnot's Pull and this realm's import), under a third of gnoland-1's block at its
// node figure (the migration gate). In the whole suite hlgnot has migrated already (TestMigration): both skip.

var txgasReady bool

// TestTxGasSetup: a send of alice's locks 20,000 GNOT in hlgnot; 2×pullPage burns credit as many claims there, each to
// a recipient of its own (Process: the waiting index's entry and tidy with each); hlgnot's migrate to this realm,
// applied; page 0 adopted, the lock with it.
func TestTxGasSetup(cur realm, t *testing.T) {
	if hlgnot.Migrated() != "" {
		t.Skip("hlgnot has migrated (the whole suite): the migration gate's probe runs alone")
	}
	envelope(hlgnotPath, 20_000*gnot, hlgnot.Fee(), true)
	testing.SetRealm(testing.NewUserRealm(alice))
	hlgnot.TransferRemote(cross(cur), ethTest, ethWallet, 20_000*gnot)
	for i := 0; i < 2*pullPage; i++ {
		msg, _ := burn(uint32(5_000+i), hlgnot.Address(), chain.PackageAddress("probe/recipient/"+strconv.Itoa(i)), 50*gnot)
		asRelayer(cur)
		hlgnot.Process(cross(cur), "", msg)
	}
	asMember(cur)
	passGnot(cur, hlgnot.NewMigrateRequest(cross(cur), pkgPath))
	skipDays(7)
	asRelayer(cur)
	hlgnot.Execute(cross(cur), decisionsOf(hlgnot.Render("json")))
	testing.SetRealm(testing.NewUserRealm(stranger1))
	Adopt(cross(cur), 0)
	if hlgnot.Migrated() != pkgPath || Imported() != 1 || !Importing() || waitingByPos.Size() != pullPage || Locked() != 20_000*gnot {
		t.Fatalf("page 0: migrated %q, imported %d importing %v, the index holds %d, lock %d", hlgnot.Migrated(), Imported(), Importing(), waitingByPos.Size(), Locked())
	}
	txgasReady = true
}

// TestTxGasAdopt: page 1 — pullPage open claims, each to a recipient of its own, each leaving hlgnot's waiting index and
// entering this realm's — adopted by a stranger: the whole of a migration page.
func TestTxGasAdopt(cur realm, t *testing.T) {
	if !txgasReady {
		t.Skip("no probe migration (the whole suite)")
	}
	testing.SetRealm(testing.NewUserRealm(stranger2))
	Adopt(cross(cur), 1)
	if Imported() != 2 || waitingByPos.Size() != 2*pullPage {
		t.Fatalf("page 1: imported %d, the index holds %d", Imported(), waitingByPos.Size())
	}
}
