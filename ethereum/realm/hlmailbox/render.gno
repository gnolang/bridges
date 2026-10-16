package hlmailbox

import (
	"strconv"
	"strings"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/decisions/v0"
	hl "gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hyperlane/v0"
	"gno.land/p/nt/avl/v0"
)

const (
	pageMax      = 20 // messages a "msgs/" page holds
	verifiersMax = 20 // registered verifier paths (and announced successors) the page and "json" name, and a "verifiers/" ("successors/") page holds
)

// Render:
//
//	""                  the mailbox's page: the launch values live or waiting, verifiers, signers, the Ethereum
//	                    deployment's contracts, seats, flags
//	"decisions"         the decisions page: the last 100 pending and executed per kind
//	"json"              domain, hook, count, root, live, signers, verifiers (the first 20 registered paths and the
//	                    count), seats, flags, routes (each with its predecessor and successor, and the height and
//	                    time of the kind 6 that enrolled it on Ethereum: enrolled, enrolledAt), successors (the
//	                    first 20 announced paths, the count, and successorRoutes: the route each announced for),
//	                    ethContracts (the Ethereum deployment's contracts, name → EIP-55 address), namespace (this
//	                    deployment's, the one it was staged under), solanaNamespace and solanaRealms (the Solana
//	                    deployment's namespace and its six realms' addresses, name → address: a burn naming one is
//	                    swept), for the agents
//	"decisions.json"    every decision, as a JSON array
//	"decisions.json/<from>"  the decisions numbered <from> on, at most 20 and no more than a page's text allows
//	                    (DecisionsJSONPage), as a JSON array of the objects "decisions.json" holds; "[]" past the last
//	"msg/<nonce>"       one dispatched message: {"nonce","id","root","height","message"}; "null" if none
//	"msgs/<from>"       up to 20 messages from nonce <from> on, as a JSON array
//	"verifiers/<from>"  up to 20 registered verifier paths from offset <from> on, in path order, as a JSON array
//	"successors/<from>" up to 20 announced successor paths from offset <from> on, in path order, as a JSON array
//	"delivered/<id>"    the block height a message id (hex) was delivered at here, 0 if never
func Render(path string) string {
	switch {
	case path == "":
		return page()
	case path == "decisions":
		return "# Decisions\n\n" + ledger.Page()
	case path == "json":
		return summaryJSON()
	case path == "decisions.json":
		return ledger.JSON()
	case strings.HasPrefix(path, "decisions.json/"):
		return DecisionsJSONPage(&ledger, path[len("decisions.json/"):])
	case strings.HasPrefix(path, "msg/"):
		n, ok := parseNonce(path[len("msg/"):])
		if !ok {
			return "null"
		}
		return msgJSON(n)
	case strings.HasPrefix(path, "msgs/"):
		from, ok := parseNonce(path[len("msgs/"):])
		if !ok {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[")
		for i := uint32(0); i < pageMax && uint64(from)+uint64(i) < uint64(tree.Count); i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(msgJSON(from + i))
		}
		b.WriteString("]")
		return b.String()
	case strings.HasPrefix(path, "verifiers/"):
		from, ok := parseNonce(path[len("verifiers/"):])
		if !ok {
			return "[]"
		}
		return pathsJSON(&verifiers, int(from))
	case strings.HasPrefix(path, "successors/"):
		from, ok := parseNonce(path[len("successors/"):])
		if !ok {
			return "[]"
		}
		return pathsJSON(&successors, int(from))
	case strings.HasPrefix(path, "delivered/"):
		return strconv.FormatInt(Delivered(path[len("delivered/"):]), 10)
	}
	return "unknown path; see the mailbox's page"
}

func parseNonce(s string) (uint32, bool) {
	n, err := strconv.ParseUint(s, 10, 32)
	return uint32(n), err == nil
}

func q(s string) string { return strconv.QuoteToASCII(s) }

// hq is bytes in hex as a JSON string: the digits between plain quotes, byte for byte what q(hx(b)) writes — hex
// needs no escape — at a fraction of its gas: strconv.Quote walks its string rune by rune, ≈93% of what msg/<nonce>
// cost before (E3-RL1). TestHexQuote holds the two forms equal.
func hq(b []byte) string { return "\"" + hx(b) + "\"" }

// msgJSON is one dispatched message as msg/<nonce> and msgs/<from> write it. Its cost grows with the body, which
// MaxBody bounds: at the bound one page costs a small part of a query's gas, and a msgs/ page of twenty such too
// (TestRenderGasMsgAtCap, TestRenderGasMsgsAtCap; eth/scripts/gnotest.sh's render-gas gate).
func msgJSON(n uint32) string {
	v := sent.Get(nonceKey(n))
	if v == nil {
		return "null"
	}
	s := v.(*sentMsg)
	raw := []byte(s.raw)
	id := hl.ID(raw)
	return "{\"nonce\":" + u32(n) + ",\"id\":" + hq(id[:]) + ",\"root\":" + hq(s.root[:]) +
		",\"height\":" + strconv.FormatInt(s.height, 10) + ",\"message\":" + hq(raw) + "}"
}

// DecisionsPageMax is the most decisions a "decisions.json/<from>" page holds, and DecisionsPageText the text —
// kinds, descriptions, states and notes, in bytes — past which a page takes no further decision (DecisionsJSONPage).
const (
	DecisionsPageMax  = 20
	DecisionsPageText = 16_384
)

// DecisionsJSONPage is a bridge realm's "decisions.json/<from>" page of its ledger (the round-3 realms audit's
// E3-RL4): the decisions numbered from <from> on (0 reads as 1, the first number), oldest first, as a JSON array of
// the very objects "decisions.json" holds — each written as p/decisions' JSON writes it, byte for byte, so the pages
// joined are the whole ledger — and "[]" past the last decision or for a <from> that is no number. A page holds at
// most DecisionsPageMax decisions and takes no further one once its text would pass DecisionsPageText bytes,
// always the first: a decision's text is the builder's parameters, up to ≈33 KB for a rotation naming 255 signer
// keys, and strconv.Quote costs ≈20,000 gas a byte, so twenty long ones would not render. A reader therefore pages
// on from the last number it got plus one until a page is "[]" — a page shorter than twenty is no sign of the end.
// "decisions.json" itself renders every decision for the bridge's life (≈8M gas a decision of ≈475 bytes: past
// ≈360 it is out of vm/qrender's 3,000,000,000); it stays as it was for its readers. The machines read these pages —
// gnoside.DecisionsFrom (the executor from its cursor, the monitor and hlwatch from the first) and the status site's
// ReadPendingByPage — and the whole page only of a realm built before them (E3-RL4, E4-CF6 item 11). p/decisions is
// the Solana deployment's too, deployed and immutable, so the page is built here from what it exports (Last, Get, the
// Decision's fields and State). A plain function over its argument, as ParseEthAddress: it writes nothing, takes no
// callback and returns a string; the ledger's p/ methods it calls run under the storage realm that allocated the
// ledger (borrow rule #2 — the caller's own, as every bridge realm passes its own) and only read.
func DecisionsJSONPage(l *decisions.Ledger, from string) string {
	u, err := strconv.ParseUint(from, 10, 63) // digits alone, as msgs/<from> reads its nonce: no sign, at most int64's
	if err != nil {
		return "[]"
	}
	n := int64(u)
	if n == 0 {
		n = 1
	}
	last := l.Last()
	if n > last {
		return "[]"
	}
	var b strings.Builder
	b.WriteString("[")
	count, text := 0, 0
	for id := n; id <= last && count < DecisionsPageMax; id++ {
		d := l.Get(id)
		if d == nil {
			continue // never: a ledger numbers its decisions 1, 2, … and removes none
		}
		st := d.State()
		size := len(d.Kind) + len(d.Text) + len(st) + len(d.Note)
		if count > 0 && text+size > DecisionsPageText {
			break
		}
		if count > 0 {
			b.WriteString(",")
		}
		b.WriteString("{\"id\":" + strconv.FormatInt(d.ID, 10) + ",\"kind\":" + q(d.Kind) + ",\"text\":" + q(d.Text) +
			",\"state\":" + q(st) + ",\"made\":" + strconv.FormatInt(d.Made, 10) + ",\"recorded\":" + strconv.FormatInt(d.Recorded, 10) +
			",\"due\":" + strconv.FormatInt(d.Due, 10) + ",\"applied\":" + strconv.FormatInt(d.Applied, 10) + ",\"cancelled\":" + strconv.FormatInt(d.Cancelled, 10) +
			",\"note\":" + q(d.Note) + "}")
		count++
		text += size
	}
	b.WriteString("]")
	return b.String()
}

// keysJSON is a list of signer keys as the json writes it: each key's hex as a JSON string (hq, byte for byte what
// q(hx(k)) wrote — a set of 255 keys in two groups cost ≈0.7B gas through strconv.Quote, E3-RL1's class).
func keysJSON(ks []Key) string {
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = hq(k[:])
	}
	// echo-ok(strings.Join): the parts are q's or hq's quoting (strconv.QuoteToASCII, hex) of keys, names, addresses and paths
	return "[" + strings.Join(parts, ",") + "]"
}

// flagJSON is one standing flag as the json lists it: Flags' line for it as a JSON string, byte for byte what
// q(line) writes, but the ids it names — 64 lower-case hex digits each, commas between, nothing strconv.Quote would
// escape — are written after the quoted head instead of through strconv.Quote, ≈21,700 gas a byte: the four guardians'
// payouts flags naming 256 ids each on the two launch routes put the json past vm/qrender's 3,000,000,000 (E3-RL1's
// class, E3-RL7's mandatory pages; TestMailboxJSONFlags holds the two forms equal).
func flagJSON(head string, ids []string) string {
	s := q(head)
	if len(ids) == 0 {
		// echo-ok(s, strings.Join): q's quoting (strconv.QuoteToASCII) of the flag's line, and the ids it names: 64 hex digits each (parseIDs)
		return s
	}
	// echo-ok(s, strings.Join): q's quoting (strconv.QuoteToASCII) of the flag's line, and the ids it names: 64 hex digits each (parseIDs)
	return s[:len(s)-1] + " naming " + strings.Join(ids, ",") + "\""
}

// pathsJSON is up to verifiersMax keys of a tree of pkgpaths (the registered verifiers, the announced successors)
// from offset from on, in path order: the listing is bounded whatever the count, since nothing is ever removed.
func pathsJSON(t *avl.Tree, from int) string {
	var b strings.Builder
	b.WriteString("[")
	first := true
	t.IterateByOffset(from, verifiersMax, func(k string, _ any) bool {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(q(k))
		return false
	})
	b.WriteString("]")
	return b.String()
}

// pathsLine is a tree of pkgpaths on the page: the first verifiersMax in path order and, past them, where the rest
// are (page is the Render path that pages them); "none" for an empty tree.
func pathsLine(t *avl.Tree, page string) string {
	n := t.Size()
	if n == 0 {
		return "none"
	}
	var b strings.Builder
	i := 0
	t.IterateByOffset(0, verifiersMax, func(k string, _ any) bool {
		if i > 0 {
			b.WriteString(", ")
		}
		i++
		// echo-ok(k, v, m, page): registered pkgpaths (ASCII by tm2's path rules), a route's symbol, a count's digits and a page's name (a constant)
		b.WriteString("`" + k + "`")
		return false
	})
	if n > verifiersMax {
		m := strconv.Itoa(verifiersMax)
		// echo-ok(k, v, m, page): registered pkgpaths (ASCII by tm2's path rules), a route's symbol, a count's digits and a page's name (a constant)
		b.WriteString(", … (the first " + m + "; the rest at [" + page + "/" + m + "](" + pkgPathLink() + ":" + page + "/" + m + "))")
	}
	return b.String()
}

func summaryJSON() string {
	var b strings.Builder
	b.WriteString("{\"domain\":" + u32(localDomain) + ",\"hook\":" + q(Hook()) + ",\"address\":" + q(string(Address())) +
		",\"count\":" + u32(tree.Count) + ",\"root\":" + q(Root()) + ",\"delivered\":" + strconv.Itoa(done.Size()) +
		",\"live\":" + strconv.FormatBool(live) + ",\"launchedAt\":" + strconv.FormatInt(launchedAt, 10) +
		"," + decisions.GovernorJSON(launchGovernor, yielded) +
		",\"launchOrigin\":" + u32(launchOrigin) + ",\"namespace\":" + q(namespace) + ",\"solanaNamespace\":" + q(solanaNamespace) +
		",\"solanaRealms\":" + solanaRealmsJSON() + ",\"ethContracts\":" + ethContractsJSON() + ",\"clocks\":" + decisions.ClocksJSON() + ",\"decisions\":" + strconv.FormatInt(ledger.Last(), 10) +
		",\"signers\":{\"keys\":" + keysJSON(signerKeys) + ",\"threshold\":" + strconv.Itoa(signerRule.Threshold) +
		",\"groupA\":" + keysJSON(signerRule.GroupA) + ",\"groupB\":" + keysJSON(signerRule.GroupB) + "}" +
		",\"verifiers\":{")
	first := true
	active.Iterate("", "", func(k string, v any) bool {
		origin, _ := strconv.ParseUint(k, 10, 32)
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(q(strconv.FormatUint(origin, 10)) + ":{\"pkgpath\":" + q(v.(string)) + ",\"registered\":" + strconv.FormatBool(verifiers.Has(v.(string))) + "}")
		return false
	})
	b.WriteString("},\"registered\":" + pathsJSON(&verifiers, 0) + ",\"registeredCount\":" + strconv.Itoa(verifiers.Size()) + ",\"seats\":[")
	for i := 1; i <= seatCount; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		b.WriteString(q(string(seats[i])))
	}
	b.WriteString("],\"routes\":{")
	first = true
	routes.Iterate("", "", func(k string, v any) bool {
		r := v.(*routeRec)
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(q(k) + ":{\"epoch\":" + strconv.FormatInt(r.epoch, 10) + ",\"epochAt\":" + strconv.FormatInt(r.epochAt, 10) +
			",\"sends\":" + strconv.FormatBool(Paused(k, FnSends)) + ",\"credits\":" + strconv.FormatBool(Paused(k, FnCredits)) +
			",\"payouts\":" + strconv.FormatBool(Paused(k, FnPayouts)) + ",\"flags\":[")
		n := 0
		eachFlag(k, func(head string, ids []string) {
			if n > 0 {
				b.WriteString(",")
			}
			n++
			b.WriteString(flagJSON(head, ids))
		})
		b.WriteString("],\"predecessor\":" + q(r.predecessor) + ",\"successor\":" + q(r.successor) +
			",\"enrolled\":" + strconv.FormatInt(r.enrolled, 10) + ",\"enrolledAt\":" + strconv.FormatInt(r.enrolledAt, 10) +
			",\"pulled\":" + strconv.FormatBool(r.pulled) + ",\"dropped\":" + quotedJSON(r.dropped) + "}")
		return false
	})
	b.WriteString("},\"droppedRoutes\":" + droppedRoutesJSON() + ",\"successors\":" + pathsJSON(&successors, 0) + ",\"successorCount\":" + strconv.Itoa(successors.Size()) + ",\"successorRoutes\":" + successorRoutesJSON() + "}")
	return b.String()
}

// ethContractKeys are the json's names of the deployment's Ethereum contracts, in the index's order (ethdeploy.gno).
var ethContractKeys = [EthContracts]string{"mailbox", "token0", "token1", "client", "gate", "hlAdmin", "proxyAdmin",
	"merkleTreeHook", "protocolFee", "mailboxImpl", "token0Impl", "token1Impl", "clientImpl", "gateImpl",
	"ed25519Verifier", "hlAdminExec"}

// ethContractsJSON is the deployment's Ethereum contracts as an object of name → address in EIP-55 case.
func ethContractsJSON() string {
	parts := make([]string, EthContracts)
	for i := range ethContracts {
		parts[i] = q(ethContractKeys[i]) + ":" + q(EthAddress(ethContracts[i]))
	}
	// echo-ok(strings.Join): the parts are q's or hq's quoting (strconv.QuoteToASCII, hex) of keys, names, addresses and paths
	return "{" + strings.Join(parts, ",") + "}"
}

// solanaRealmsJSON is the Solana deployment's six realms as an object of name → address.
func solanaRealmsJSON() string {
	parts := make([]string, len(solanaRealms))
	for i, a := range solanaRealms {
		parts[i] = q(solanaNames[i]) + ":" + q(string(a))
	}
	// echo-ok(strings.Join): the parts are q's or hq's quoting (strconv.QuoteToASCII, hex) of keys, names, addresses and paths
	return "{" + strings.Join(parts, ",") + "}"
}

// quotedJSON is a list of strings as a JSON array.
func quotedJSON(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = q(s)
	}
	// echo-ok(strings.Join): the parts are q's or hq's quoting (strconv.QuoteToASCII, hex) of keys, names, addresses and paths
	return "[" + strings.Join(parts, ",") + "]"
}

// droppedRoutesJSON is every dropped successor (R35) with the predecessor whose re-point dropped it, as an object
// of pkgpath → pkgpath: the tombstones, bounded by the migrations a bridge sees in its life.
func droppedRoutesJSON() string {
	var b strings.Builder
	b.WriteString("{")
	first := true
	droppedRoutes.Iterate("", "", func(k string, v any) bool {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(q(k) + ":" + q(v.(string)))
		return false
	})
	b.WriteString("}")
	return b.String()
}

// droppedLine is the dropped successors on the page, each with the predecessor that re-pointed away from it; ""
// when none.
func droppedLine() string {
	if droppedRoutes.Size() == 0 {
		return ""
	}
	var parts []string
	droppedRoutes.Iterate("", "", func(k string, v any) bool {
		parts = append(parts, "`"+k+"` (by `"+v.(string)+"`)")
		return false
	})
	// echo-ok(strings.Join): dropped routes' pkgpaths, ASCII by tm2's path rules
	return "- dropped (routes no more, re-pointed away): " + strings.Join(parts, ", ") + "\n"
}

// successorRoutesJSON is the first verifiersMax announced successors with the route each announced for (AQ5), as
// an object of pkgpath → "GNOT" or "BUBBLE".
func successorRoutesJSON() string {
	var b strings.Builder
	b.WriteString("{")
	first := true
	successors.IterateByOffset(0, verifiersMax, func(k string, v any) bool {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(q(k) + ":" + q(v.(string)))
		return false
	})
	b.WriteString("}")
	return b.String()
}

// successorsLine is the announced successors on the page, each with the route it announced for (AQ5): the first
// verifiersMax in path order and, past them, where the rest are; "none" for an empty tree.
func successorsLine() string {
	n := successors.Size()
	if n == 0 {
		return "none"
	}
	var b strings.Builder
	i := 0
	successors.IterateByOffset(0, verifiersMax, func(k string, v any) bool {
		if i > 0 {
			b.WriteString(", ")
		}
		i++
		// echo-ok(k, v, m, page): registered pkgpaths (ASCII by tm2's path rules), a route's symbol, a count's digits and a page's name (a constant)
		b.WriteString("`" + k + "` (" + v.(string) + ")")
		return false
	})
	if n > verifiersMax {
		m := strconv.Itoa(verifiersMax)
		// echo-ok(k, v, m, page): registered pkgpaths (ASCII by tm2's path rules), a route's symbol, a count's digits and a page's name (a constant)
		b.WriteString(", … (the first " + m + "; the rest at [successors/" + m + "](" + pkgPathLink() + ":successors/" + m + "))")
	}
	return b.String()
}

func page() string {
	var b strings.Builder
	b.WriteString("# The bridge's mailbox\n\n")
	b.WriteString("gno.land's end of the GNOT and BUBBLE bridge to Ethereum: messages out to Ethereum (the Dispatch event its light client, GnoLightClient, proves), and in from it under the active verifier. Every setting here moves only by a decision made with this realm's builders — the governor's own until it yields, a GovDAO proposal after; the guardians and watch keys can only pause.\n\n")
	if live {
		b.WriteString("- **live** since " + strconv.FormatInt(launchedAt, 10) + " (the Launch decision executed)\n")
	} else {
		b.WriteString("- **not launched**: the values below are the compiled launch values, waiting for the Launch decision; the routes refuse sends and credits. A passed Launch executes within 14 days of its proposal, like every decision; one nobody executed by then lapses and is proposed again\n")
	}
	b.WriteString("- " + decisions.GovernorLine(launchGovernor, yielded) + "\n")
	b.WriteString("- a proposal made with a builder (of this realm, a route, r/hlfees or r/hlgov) is executed with r/gov/dao's `ExecuteOrRejectProposal`: the one error a closure returns — the 14-day deadline — then denies it for good; under `ExecuteProposal` that error reverts the transaction instead and the proposal stands, every retry failing the same way. Anything else a closure refuses is a panic, which leaves the proposal for a retry under either call\n")
	b.WriteString("- domain: " + u32(localDomain) + "; Ethereum: " + u32(launchOrigin) + "\n")
	b.WriteString("- " + decisions.Clocks() + "\n")
	b.WriteString("- merkle tree hook: `" + Hook() + "`\n")
	b.WriteString("- messages out: " + u32(tree.Count) + ", root `" + Root() + "`; in: " + strconv.Itoa(done.Size()) + "\n")
	b.WriteString("- decisions: " + strconv.FormatInt(ledger.Last(), 10) + " ([page](" + pkgPathLink() + ":decisions))\n")
	b.WriteString("\n## Verifiers\n\n")
	active.Iterate("", "", func(k string, v any) bool {
		origin, _ := strconv.ParseUint(k, 10, 32)
		note := ""
		if !verifiers.Has(v.(string)) {
			note = " (**not registered**: nothing from this origin is taken until it is)"
		}
		// echo-ok(v, note): an active verifier's pkgpath (ASCII by tm2's path rules) and a note of words
		b.WriteString("- from " + strconv.FormatUint(origin, 10) + ": `" + v.(string) + "`" + note + "\n")
		return false
	})
	b.WriteString("- registered (" + strconv.Itoa(verifiers.Size()) + "): " + pathsLine(&verifiers, "verifiers"))
	// echo-ok(signerRule): the signer rule's text: numbers and hex keys, ASCII
	b.WriteString("\n\n## Signers\n\n- rule: " + signerRule.String() + "\n")
	for i, k := range signerKeys {
		b.WriteString("- " + strconv.Itoa(i) + ": `" + hx(k[:]) + "`\n")
	}
	b.WriteString("\n## Ethereum contracts\n\n")
	b.WriteString("The deployment's contracts on Ethereum, compiled (eth §5.8): r/hlgov's kind 8 upgrades the first five, its first decision recipient is HlAdmin, and no send names any of them as its recipient (nor an address at or below 0xffff).\n\n")
	for i := range ethContracts {
		// echo-ok(ethContractNames): a contract's name, a constant
		b.WriteString("- " + ethContractNames[i] + ": `" + EthAddress(ethContracts[i]) + "`\n")
	}
	b.WriteString("\n## Seats\n\n")
	for i := 1; i <= seatCount; i++ {
		kind := "guardian"
		if !IsGuardianSeat(i) {
			kind = "watch key"
		}
		a := string(seats[i])
		if a == "" {
			a = "(empty)"
		}
		// echo-ok(kind, a): guardian or watch key (a constant), and a seat's holder: a canonical bech32 address or none
		b.WriteString("- seat " + strconv.Itoa(i) + " (" + kind + "): " + a + "\n")
	}
	b.WriteString("\n## Routes and flags\n\n")
	routes.Iterate("", "", func(k string, v any) bool {
		r := v.(*routeRec)
		// echo-ok(k, r, strings.Join): routes' pkgpaths as the mailbox recorded them, ASCII by tm2's path rules
		b.WriteString("- `" + k + "`: payouts epoch " + strconv.FormatInt(r.epoch, 10))
		if r.predecessor != "" {
			// echo-ok(k, r, strings.Join): routes' pkgpaths as the mailbox recorded them, ASCII by tm2's path rules
			b.WriteString(", successor of `" + r.predecessor + "`")
		}
		if r.pulled {
			b.WriteString(", page 0 pulled")
		}
		if r.successor != "" {
			// echo-ok(k, r, strings.Join): routes' pkgpaths as the mailbox recorded them, ASCII by tm2's path rules
			b.WriteString(", migrated to `" + r.successor + "`")
		}
		if len(r.dropped) > 0 {
			// echo-ok(k, r, strings.Join): routes' pkgpaths as the mailbox recorded them, ASCII by tm2's path rules
			b.WriteString(", dropped `" + strings.Join(r.dropped, "`, `") + "`")
		}
		if r.enrolled != 0 {
			b.WriteString(", enrolled on Ethereum by kind 6 at height " + strconv.FormatInt(r.enrolled, 10) + " (" + strconv.FormatInt(r.enrolledAt, 10) + ")")
		}
		fs := Flags(k)
		if len(fs) == 0 {
			b.WriteString(", no flag\n")
		} else {
			b.WriteString("\n")
			for _, f := range fs {
				// echo-ok(f): a flag's line (Flags): a function's name, a seat's number and holder (bech32), a time, a range's words or ids in hex
				b.WriteString("  - **paused** " + f + "\n")
			}
		}
		return false
	})
	b.WriteString(droppedLine())
	b.WriteString("- successors announced (" + strconv.Itoa(successors.Size()) + "): " + successorsLine() + " (a route's migrate names only one of them, announced for its own route)\n")
	if tree.Count > 0 {
		b.WriteString("\n## Latest out\n\n")
		sent.ReverseIterateByOffset(0, 10, func(k string, v any) bool {
			s := v.(*sentMsg)
			m, _ := hl.ParseMessage([]byte(s.raw))
			id := hl.ID([]byte(s.raw))
			b.WriteString("- #" + u32(m.Nonce) + " to " + u32(m.Destination) + " at block " + strconv.FormatInt(s.height, 10) + ": `" + hx(id[:]) + "`\n")
			return false
		})
	}
	return b.String()
}

// echo-ok(strings.TrimPrefix): this realm's own pkgpath, a constant
func pkgPathLink() string { return strings.TrimPrefix(pkgPath, "gno.land") }
