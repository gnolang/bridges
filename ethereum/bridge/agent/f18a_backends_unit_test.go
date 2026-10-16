package agent

// Fold F18a: the one-node record itself (backends.go, E6-GT2) — what each reading notes, and how a finding ends.

import (
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

func TestF18aOneNodeRecord(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var g gnoSeen
	g.begin("https://node.example/rpc?key=secret", "gno.land/r/x/hlmailbox")
	g.head(60, "a", now)
	g.head(61, "b", now)
	g.mailbox(&gnoside.Mailbox{Count: 3, Delivered: 7}, "c", now)
	g.marks(61, 61, now)
	g.placed(7, 7, 61, now)
	if g.text() != "" {
		t.Fatalf("one node's readings found: %s", g.Backends)
	}
	for _, c := range []struct {
		name string
		read func(*gnoSeen)
	}{
		{"a head lower", func(g *gnoSeen) { g.head(60, "the head", now) }},
		{"a count lower", func(g *gnoSeen) { g.mailbox(&gnoside.Mailbox{Count: 2, Delivered: 7}, "hlmailbox", now) }},
		{"a delivered count lower", func(g *gnoSeen) { g.mailbox(&gnoside.Mailbox{Count: 3, Delivered: 6}, "hlmailbox", now) }},
		{"a mark above a later head", func(g *gnoSeen) { g.marks(62, 61, now) }},
		{"more placed than the count", func(g *gnoSeen) { g.placed(8, 7, 61, now) }},
	} {
		h := g
		c.read(&h)
		if h.text() == "" {
			t.Fatalf("%s: not found", c.name)
		}
		// kept across later readings, and across begin for the same node and mailbox
		h.head(99, "later", now)
		h.begin("https://node.example/rpc?key=secret", "gno.land/r/x/hlmailbox")
		if h.text() == "" {
			t.Fatalf("%s: the finding was not kept", c.name)
		}
		// another node starts afresh
		h.begin("http://127.0.0.1:26657", "gno.land/r/x/hlmailbox")
		if h.text() != "" || h.Head != 0 {
			t.Fatalf("%s: another node did not start afresh: %+v", c.name, h)
		}
	}
	if strings.Contains(g.Node, "secret") || !strings.Contains(g.Node, "gno.land/r/x/hlmailbox") {
		t.Fatalf("the node's address is kept unredacted, or the mailbox not with it: %q", g.Node)
	}
}
