package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Governed changes (E3-CF9): the audit is the members' at the yield too, against the bridge as it stands — after the
// governor's own seating of the outsiders (a kind 5), a fee (kind 7), new limits (kind 12), an upgrade (kind 8) or a
// successor (kind 9). Each value such a decision moves is held to the executed decision that moved it: the line of
// HlAdmins (the launch one, then each successor a kind 9 named) is read from the chain, every decision's end from
// its logs (DecisionRecorded, DecisionExecuted, DecisionSpent, DecisionCancelled), each executed decision's
// parameters from its own record the block before it ran (decision(n), what HlAdmin executed; r/hlgov's body of
// that number when the provider keeps no such state), held to r/hlgov's body of the number when gno.land is read;
// and the launch's values are replayed through them in the order they ran. A value the replay explains passes,
// said with the decision that set it; any other difference FAILs.

// lineHops bounds the successors followed from the launch HlAdmin.
const lineHops = 8

// lineDecision is one decision an HlAdmin of the line recorded, and how it ended.
type lineDecision struct {
	Admin    common.Address
	N        uint64
	Kind     uint8
	Recorded types.Log
	End      string // "executed", "spent", "cancelled"; "" while it pends
	EndLog   types.Log
	Why      string // a spend's reason
	Params   []byte // an executed decision's parameters
	From     string // where they were read
}

func (d *lineDecision) String() string {
	return fmt.Sprintf("decision #%d (kind %d, %s at block %d)", d.N, d.Kind, d.End, d.EndLog.BlockNumber)
}

// lineFacts are the HlAdmin line, its decisions and the values the executed ones moved, replayed over the launch's.
type lineFacts struct {
	Admins    []common.Address // the launch HlAdmin first, then each successor
	Live      common.Address   // the last of them: the one that holds the authorities now
	Decisions []*lineDecision
	Executed  []*lineDecision                  // in the order they ran
	Retired   map[common.Address]*lineDecision // an HlAdmin of the line → the kind 9 of its that ran (its successor's)

	Seats       [6]common.Address // the line's seats and watch slots (the gate's)
	AdminSeats  [6]common.Address // the launch HlAdmin's own, which a successor's decisions no longer move
	Limits      [2][2]uint64      // each token's maximum and day cap
	Fee         uint64
	Beneficiary common.Address
	ISM         [2]common.Address
	Routers     [2]map[uint32][32]byte
	Impl        map[string]common.Address  // a proxy's field → its implementation
	Upgrades    map[string][]*lineDecision // a proxy's field → the kind 8s that ran on it, in order
	Why         map[string]*lineDecision   // a value's name → the last executed decision that set it
	Unread      map[*lineDecision]bool     // executed decisions whose parameters could not be read
	foreign     map[*lineDecision]string   // executed decisions r/hlgov did not dispatch as they ran (why)
}

// explained says which executed decision last set a value, "" when none did (the launch's).
func (f *lineFacts) explained(name string) string {
	if d := f.Why[name]; d != nil {
		return "; " + d.String() + " set it"
	}
	return ""
}

// proxyFields are the five proxies a kind 8 may upgrade, by their record fields.
var proxyFields = []string{"mailbox", "token0", "token1", "client", "gate"}

// readLine reads the HlAdmin line, its decisions' logs, the executed decisions' parameters, and replays the launch's
// values (a.launch: the independent expectations, the record's where none exists) through them.
func (a *audit) readLine(ctx context.Context) {
	d := a.d
	f := &lineFacts{Why: map[string]*lineDecision{}, Unread: map[*lineDecision]bool{}, foreign: map[*lineDecision]string{}, Impl: map[string]common.Address{}, Upgrades: map[string][]*lineDecision{}, Retired: map[common.Address]*lineDecision{}}
	a.line = f
	launch := d.Addr("hlAdmin")
	f.Admins = []common.Address{launch}
	cur := launch
	seen := map[common.Address]bool{launch: true}
	for hop := 0; ; hop++ {
		s, err := read[common.Address](ctx, a.cn, a.at, cur, a.arts["HlAdmin"], "successor")
		if err != nil {
			a.fail(fmt.Sprintf("the successor() of HlAdmin %s (its line)", cur.Hex()), err)
			break
		}
		if s == (common.Address{}) {
			break
		}
		if hop >= lineHops || seen[s] {
			a.check(false, "HlAdmin %s's line of successors runs past %d hops or loops at %s", launch.Hex(), lineHops, s.Hex())
			break
		}
		seen[s] = true
		f.Admins = append(f.Admins, s)
		cur = s
	}
	f.Live = f.Admins[len(f.Admins)-1]

	for _, admin := range f.Admins {
		a.readDecisions(ctx, admin)
	}
	sort.SliceStable(f.Executed, func(i, k int) bool {
		x, y := f.Executed[i].EndLog, f.Executed[k].EndLog
		return x.BlockNumber < y.BlockNumber || (x.BlockNumber == y.BlockNumber && x.Index < y.Index)
	})
	for _, x := range f.Executed {
		a.paramsOf(ctx, x)
	}
	a.replay()
}

// readDecisions reads one HlAdmin's decisions from its logs: each number it recorded, with its kind, and its end.
func (a *audit) readDecisions(ctx context.Context, admin common.Address) {
	f := a.line
	logs := func(name string) []types.Log {
		ls, err := a.logsOf(ctx, admin, ethx.Admin.Event(name))
		if err != nil {
			a.fail(fmt.Sprintf("HlAdmin %s's %s logs", admin.Hex(), name), err)
		}
		return ls
	}
	num := func(l types.Log) (uint64, bool) {
		if len(l.Topics) < 2 {
			return 0, false
		}
		return new(big.Int).SetBytes(l.Topics[1][:]).Uint64(), true
	}
	byN := map[uint64]*lineDecision{}
	for _, l := range logs("DecisionRecorded") {
		n, ok := num(l)
		vals, err := ethx.Admin.ABI.Events["DecisionRecorded"].Inputs.NonIndexed().Unpack(l.Data)
		if !ok || err != nil || len(vals) != 2 {
			continue
		}
		x := &lineDecision{Admin: admin, N: n, Kind: vals[0].(uint8), Recorded: l}
		byN[n] = x
		f.Decisions = append(f.Decisions, x)
	}
	for _, ev := range []string{"DecisionExecuted", "DecisionSpent", "DecisionCancelled"} {
		for _, l := range logs(ev) {
			n, ok := num(l)
			x := byN[n]
			if !ok || x == nil || x.End != "" {
				continue
			}
			x.EndLog = l
			switch ev {
			case "DecisionExecuted":
				x.End = "executed"
				if x.Kind != kindCancel {
					f.Executed = append(f.Executed, x)
				}
			case "DecisionSpent":
				x.End = "spent"
				if vals, err := ethx.Admin.ABI.Events["DecisionSpent"].Inputs.NonIndexed().Unpack(l.Data); err == nil && len(vals) == 2 {
					x.Why, _ = vals[1].(string)
				}
			case "DecisionCancelled":
				x.End = "cancelled"
			}
		}
	}
}

// paramsOf reads an executed decision's parameters: HlAdmin's own record of it the block before it ran (what it
// executed), else — a provider without that block's state — r/hlgov's body of the number; and holds them to r/hlgov's
// body when gno.land is read (a decision r/hlgov did not dispatch so is foreign: its values are never explained).
func (a *audit) paramsOf(ctx context.Context, x *lineDecision) {
	f := a.line
	if x.EndLog.BlockNumber > 0 {
		ds, err := ethx.ReadDecisions(ctx, a.cn.c, x.Admin, []uint64{x.N}, ethx.AtNumber(x.EndLog.BlockNumber-1))
		if err == nil && ds[0] != nil && ds[0].Kind == x.Kind {
			x.Params, x.From = ds[0].Params, fmt.Sprintf("HlAdmin's record of it at block %d", x.EndLog.BlockNumber-1)
		}
	}
	gov := a.govRead(ctx)
	if gov.Gov == nil {
		if x.Params == nil {
			f.Unread[x] = true
		}
		return
	}
	gd, err := a.govDecision(ctx, x.N)
	switch {
	case err != nil:
		a.fail(fmt.Sprintf("r/hlgov's decision/%d", x.N), err)
		if x.Params == nil {
			f.Unread[x] = true
		}
		return
	case gd == nil:
		f.foreign[x] = "r/hlgov dispatched no decision under that number"
		if x.Params == nil {
			f.Unread[x] = true
		}
		return
	}
	kind, params, err := govBody(gd, x.N)
	switch {
	case err != nil:
		f.foreign[x] = err.Error()
	case kind != x.Kind:
		f.foreign[x] = fmt.Sprintf("r/hlgov's decision %d is of kind %d", x.N, kind)
	case x.Params == nil:
		x.Params, x.From = params, fmt.Sprintf("r/hlgov's decision/%d (no state of block %d at this provider)", x.N, x.EndLog.BlockNumber-1)
	case !bytes.Equal(params, x.Params):
		f.foreign[x] = fmt.Sprintf("r/hlgov's parameters %x are not the ones HlAdmin ran, %x", params, x.Params)
	}
	if x.Params == nil {
		f.Unread[x] = true
	}
}

// replay runs the launch's values through the executed decisions in the order they ran (HlAdminExec.run's kinds).
func (a *audit) replay() {
	f, l := a.line, a.launch
	launch := a.d.Addr("hlAdmin")
	f.Seats = launchKeys(l)
	f.AdminSeats = f.Seats
	f.Limits = [2][2]uint64{{l.MaxPerMessage[0], l.DayCap[0]}, {l.MaxPerMessage[1], l.DayCap[1]}}
	f.Beneficiary = common.HexToAddress(l.Beneficiary)
	gate := a.d.Addr("gate")
	f.ISM = [2]common.Address{gate, gate}
	for i := range f.Routers {
		f.Routers[i] = map[uint32][32]byte{a.d.GnoDomain: a.route[i]}
	}
	for _, p := range proxyFields {
		f.Impl[p] = a.d.Addr(p + "Impl")
	}
	for _, x := range f.Executed {
		if f.Unread[x] || f.foreign[x] != "" {
			continue
		}
		p := x.Params
		switch x.Kind {
		case kindTokenISM:
			if len(p) == 33 && p[0] < 2 {
				f.ISM[p[0]] = common.BytesToAddress(p[13:33])
				f.Why[fmt.Sprintf("ism %d", p[0])] = x
			}
		case kindGuardians:
			for i := 0; len(p) > 0 && i < int(p[0]) && len(p) >= 1+33*(i+1); i++ {
				at := 1 + 33*i
				s := int(p[at])
				if s < 1 || s > 6 {
					continue
				}
				k := common.BytesToAddress(p[at+13 : at+33])
				f.Seats[s-1] = k
				if x.Admin == launch {
					f.AdminSeats[s-1] = k
				}
				f.Why[fmt.Sprintf("seat %d", s)] = x
			}
		case kindRouter:
			if len(p) == 37 && p[0] < 2 {
				dom := binary.BigEndian.Uint32(p[1:5])
				var r [32]byte
				copy(r[:], p[5:37])
				if r == ([32]byte{}) {
					delete(f.Routers[p[0]], dom)
				} else {
					f.Routers[p[0]][dom] = r
				}
				f.Why[fmt.Sprintf("router %d %d", p[0], dom)] = x
				f.Why[fmt.Sprintf("domains %d", p[0])] = x
			}
		case kindFee:
			if len(p) == 40 {
				f.Fee = binary.BigEndian.Uint64(p[:8])
				f.Beneficiary = common.BytesToAddress(p[20:40])
				f.Why["fee"], f.Why["beneficiary"] = x, x
			}
		case kindUpgrade:
			if len(p) >= 96 {
				proxy, impl := common.BytesToAddress(p[12:32]), common.BytesToAddress(p[44:64])
				for _, field := range proxyFields {
					if a.d.Addr(field) == proxy {
						f.Impl[field] = impl
						f.Upgrades[field] = append(f.Upgrades[field], x)
						f.Why["impl "+field] = x
					}
				}
			}
		case kindSuccessor:
			if len(p) == 64 {
				f.Retired[x.Admin] = x
			}
		case kindMintLimits:
			if len(p) == 17 && p[0] < 2 {
				f.Limits[p[0]] = [2]uint64{binary.BigEndian.Uint64(p[1:9]), binary.BigEndian.Uint64(p[9:17])}
				f.Why[fmt.Sprintf("limits %d", p[0])] = x
			}
		}
	}
}

// lineSection says the line and every executed decision, and fails what cannot explain anything: an executed
// decision whose parameters do not read, or one r/hlgov did not dispatch as it ran.
func (a *audit) lineSection() {
	f := a.line
	for i, ad := range f.Admins {
		switch i {
		case 0:
			a.note("the launch HlAdmin %s", ad.Hex())
		default:
			a.note("its successor %s (a kind 9's: not audited here — its code, bindings and record are its own; hlwatch's successor check read them before the vote)", ad.Hex())
		}
	}
	ended := map[string]int{}
	for _, x := range f.Decisions {
		ended[x.End]++
	}
	a.note("the line recorded %d decision(s): %d executed, %d spent, %d cancelled, %d pending", len(f.Decisions), ended["executed"], ended["spent"], ended["cancelled"], ended[""])
	for _, x := range f.Executed {
		switch {
		case f.foreign[x] != "":
			a.check(false, "%s on HlAdmin %s is not r/hlgov's: %s — a decision gno.land never sent moved what it moved, which nothing here explains", x, x.Admin.Hex(), f.foreign[x])
		case f.Unread[x]:
			a.check(false, "%s on HlAdmin %s: its parameters could not be read (HlAdmin's record of it the block before it ran — an archive provider — or r/hlgov's decision/%d): what it moved stays unexplained", x, x.Admin.Hex(), x.N)
		default:
			text, err := describeParams(x.Kind, x.Params)
			if err != nil {
				text = "parameters HlAdmin's checks refuse (" + err.Error() + ")"
			}
			a.note("%s on HlAdmin %s: %s (from %s)", x, x.Admin.Hex(), text, x.From)
		}
	}
}

// retiredBy says the kind 9 that named next as an HlAdmin's successor, or that none did.
func retiredBy(x *lineDecision, next common.Address) string {
	switch {
	case x == nil:
		return " — NO KIND 9 OF ITS EXECUTED: nothing explains the hand-over"
	case common.BytesToAddress(x.Params[12:32]) != next:
		return fmt.Sprintf(" — but its executed kind 9, %s, named %s", x, common.BytesToAddress(x.Params[12:32]).Hex())
	}
	return fmt.Sprintf(", by %s (code hash 0x%x as decided)", x, x.Params[32:64])
}

// seatsExplained names the executed kind 5s that set the seats, in their order ("" when none ran).
func (f *lineFacts) seatsExplained() string {
	var parts []string
	for s := 1; s <= 6; s++ {
		if x := f.Why[fmt.Sprintf("seat %d", s)]; x != nil {
			parts = append(parts, fmt.Sprintf("seat %d by %s", s, x))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ", then " + strings.Join(parts, ", ")
}

// sortedDomains lists a set of domains in order.
func sortedDomains(m map[uint32]bool) []uint32 {
	var out []uint32
	for d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, k int) bool { return out[i] < out[k] })
	return out
}
