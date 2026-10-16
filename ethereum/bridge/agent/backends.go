package agent

import (
	"fmt"
	"log"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// One node (the Go trust audit's E6-GT2; PLAN.md §11.10). Every signer and monitor reads gno.land through a node of its
// own, and the cut rests on it (§5.9, cutorder.go): a synced node's head is the last block it applied, its hlmailbox
// counts only rise, and a mark it shows was set at or below its head — so a cut whose two hlmailbox reads agree holds, at
// the head read between them, exactly the deliveries at or below that head. An endpoint answering from more than one
// backend (a load balancer before nodes a block apart) breaks each of these: the audit measured an honest credit flagged
// in one poll of eight while it propagated, and a halted signer signing a forged burn in one round in seven (reproduced,
// TestAudit6LoadBalanced*). Nothing in one answer tells two backends apart; across answers this does: a head, or an
// hlmailbox count, reading lower than one read before it (within a round or across rounds: the highest are kept in the
// state), a mark set above a head read after it, or more of the index's burns credited at or below a cut's head than
// hlmailbox's count read after that head. Seen once, the endpoint is not the machine's own node: every verdict is
// refused — a signer then signs nothing past its last verdict, and halts after an hour — and gno:backends is alarmed at
// flag level, kept in the state until the operator gives the machine a node of its own (another gno.rpc starts the
// record afresh; for the same address, once fixed, delete "backends" from the state's "gno" record).

// gnoSeen is what a machine's own node answered so far, and the finding once two answers could not come from one node.
type gnoSeen struct {
	Node      string `json:"node,omitempty"`      // the own node and the mailbox the readings are of (another starts afresh)
	Head      int64  `json:"head,omitempty"`      // the highest head read
	Count     uint32 `json:"count,omitempty"`     // hlmailbox's highest count read (its dispatches)
	Delivered int    `json:"delivered,omitempty"` // hlmailbox's highest delivered count read
	Backends  string `json:"backends,omitempty"`  // the finding: what was read, and why one node cannot answer it
	Since     int64  `json:"since,omitempty"`     // when it was found (unix)
}

// backendsKey is the alarm of a finding.
const backendsKey = "gno:backends"

// begin starts the record for the node and the mailbox a machine reads: readings of another start afresh.
func (g *gnoSeen) begin(node, mailbox string) {
	key := ethx.Redact(node) + " " + mailbox
	if g.Node == key {
		return
	}
	if g.Backends != "" {
		log.Printf("gno: the own node is now %s (was %s): the finding of more than one backend there is left behind: %s", ethx.Redact(node), g.Node, g.Backends)
	}
	*g = gnoSeen{Node: key}
}

func (g *gnoSeen) found(now time.Time, text string) {
	if g.Backends == "" {
		g.Backends, g.Since = text, now.Unix()
		log.Printf("gno: %s", text)
	}
}

// head notes a head read of the own node.
func (g *gnoSeen) head(h int64, what string, now time.Time) {
	if h < g.Head {
		g.found(now, fmt.Sprintf("%s read height %d, lower than the %d read before it", what, h, g.Head))
		return
	}
	g.Head = h
}

// mailbox notes a read of hlmailbox's counts.
func (g *gnoSeen) mailbox(mb *gnoside.Mailbox, what string, now time.Time) {
	if mb == nil {
		return
	}
	switch {
	case mb.Count < g.Count:
		g.found(now, fmt.Sprintf("%s read hlmailbox's count %d, lower than the %d read before it", what, mb.Count, g.Count))
	case mb.Delivered < g.Delivered:
		g.found(now, fmt.Sprintf("%s read hlmailbox's delivered count %d, lower than the %d read before it", what, mb.Delivered, g.Delivered))
	}
	g.Count, g.Delivered = max(g.Count, mb.Count), max(g.Delivered, mb.Delivered)
}

// marks holds the highest delivered mark a round read to a head read after it: on one node a mark is set by the block
// that applies it, at or below every head read later.
func (g *gnoSeen) marks(top, head int64, now time.Time) {
	if top > head {
		g.found(now, fmt.Sprintf("a delivered mark read at height %d, above the head %d read after it", top, head))
	}
}

// placed holds the deliveries a cut places in its state — the index's burns credited at or below its head, and those
// pruned (credited earlier) — to hlmailbox's delivered count read after that head: one node's done tree holds them all.
func (g *gnoSeen) placed(placed, delivered int, head int64, now time.Time) {
	if placed > delivered {
		g.found(now, fmt.Sprintf("%d of the index's burns are marked delivered at or below height %d, and hlmailbox's delivered count read after it is %d", placed, head, delivered))
	}
}

// text is the alarm of a finding ("" while there is none).
func (g *gnoSeen) text() string {
	if g.Backends == "" {
		return ""
	}
	return fmt.Sprintf("this machine's gno endpoint answers from more than one backend (%s, at %s): the cut's reads are not one node's (a load-balanced endpoint, against §11.10's own node: an honest credit flagged, a forged burn signed — E6-GT2), so no verdict is taken until it is fixed — give the machine a node of its own (another gno.rpc starts this record afresh; for the same address, once fixed, delete \"backends\" from the state's \"gno\" record)",
		g.Backends, time.Unix(g.Since, 0).UTC().Format(time.RFC3339))
}
