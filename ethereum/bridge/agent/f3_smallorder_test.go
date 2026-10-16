package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// The machines' alarms for a validator key anyone can sign for (the code audit's E2-ED3): the monitor and hlwatch for
// the sets the bridge trusts or is about to, the updater before it submits an announcing header. An alarm, never a
// refusal or a flag. (The key test itself: smallorder_test.go.)

// libsodiumSmallOrder are libsodium's ge25519_has_small_order blocklist (the top bit, x's sign, cleared): 0 (order 4),
// 1 (order 1), the two order-8 y values, p − 1 (order 2), p (≡ 0) and p + 1 (≡ 1).
var libsodiumSmallOrder = []string{
	"0000000000000000000000000000000000000000000000000000000000000000",
	"0100000000000000000000000000000000000000000000000000000000000000",
	"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
	"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
	"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
	"edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
	"eeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
}

func key32(t *testing.T, h string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 32 {
		t.Fatalf("key %s", h)
	}
	var k [32]byte
	copy(k[:], b)
	return k
}

// keyNode is the monitor's own node with headers and validator sets by height (gno.Client's BlockAt and ValidatorsAt).
type keyNode struct {
	*m3bGnoNode
	sets map[int64]*gnolight.ValidatorSet // a set from its height on
}

func (k *keyNode) setAt(h int64) *gnolight.ValidatorSet {
	var best int64
	for from := range k.sets {
		if from <= h && from > best {
			best = from
		}
	}
	return k.sets[best]
}

func (k *keyNode) BlockAt(_ context.Context, h int64) (*gnolight.Block, error) {
	vh, _ := k.setAt(h).Hash()
	nh, _ := k.setAt(h + 1).Hash()
	hdr := &gnolight.Header{Height: h, ValidatorsHash: vh[:], NextValidatorsHash: nh[:]}
	return &gnolight.Block{Header: hdr, Hash: hdr.Hash()}, nil // a node's block hash is its header's (the monitor holds it to that, E3-AS3)
}

func (k *keyNode) ValidatorsAt(_ context.Context, h int64) (*gnolight.ValidatorSet, error) {
	return k.setAt(h), nil
}

// validatorSet is a sorted set of keys, each of power 10.
func validatorSet(keys ...[32]byte) *gnolight.ValidatorSet {
	s := &gnolight.ValidatorSet{}
	for _, k := range keys {
		s.Validators = append(s.Validators, gnolight.Validator{PubKey: k, Power: 10})
	}
	s.Sort()
	return s
}

func ordinaryKey(t *testing.T) [32]byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var k [32]byte
	copy(k[:], pub)
	return k
}

// The monitor (and so hlwatch) alarms a small-order key in a set the bridge trusts or is about to — here one gno.land
// announces at its head — names it on the holder's page and is not green; the alarm clears once no set read holds it.
// Nothing is refused or flagged.
func TestMonitorAlarmsASmallOrderKey(t *testing.T) {
	r := newMonRig(t)
	r.m.ReadOnly = true
	identity := key32(t, libsodiumSmallOrder[1])
	a, b, c := ordinaryKey(t), ordinaryKey(t), ordinaryKey(t)
	node := &keyNode{m3bGnoNode: r.gno, sets: map[int64]*gnolight.ValidatorSet{1: validatorSet(a, b, c), 61: validatorSet(a, b, c, identity)}}
	r.m.own = node
	w := &Watch{m: r.m, Supermajority: 66.66}
	v, err := w.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key := "valset:small-order:" + hex.EncodeToString(identity[:8])
	text, ok := v.Alarms[key]
	if !ok || !strings.Contains(text, "the set gno.land's head announces at height 61") || !strings.Contains(text, "power 10 of 40") {
		t.Fatalf("no alarm for the announced small-order key: %v", v.Alarms)
	}
	if page := v.Text("build x"); !strings.Contains(page, "SMALL-ORDER VALIDATOR KEY: validator key 0x0100") {
		t.Fatalf("the page does not name it:\n%s", page)
	}
	if ok, why := v.Green(); ok || !strings.Contains(strings.Join(why, ";"), key) {
		t.Fatalf("green with a small-order key announced: %v", why)
	}
	if r.snd.count() != 0 || len(r.gnoFlags()) != 0 {
		t.Fatal("a small-order key was flagged: it is an alarm only")
	}
	// gno.land announces another set without it: the alarm clears
	node.sets[61] = validatorSet(a, b, c, ordinaryKey(t))
	r.step(10 * time.Second)
	if v, err = w.View(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Alarms[key]; ok {
		t.Fatal("the alarm stands once no set holds the key")
	}
	if page := v.Text("build x"); !strings.Contains(page, "anyone can sign for (small order, E2-ED3): none") {
		t.Fatalf("the page:\n%s", page)
	}
}

// Before it submits an announcing header the updater alarms a small-order key of the set it announces — and submits
// the header all the same (the client follows what gno.land's validators sign).
func TestUpdaterAlarmsASmallOrderKeyBeforeAnnouncing(t *testing.T) {
	w := newUpdWorld(t, 100)
	identity := key32(t, libsodiumSmallOrder[1])
	next := newFakeSet(2, 60, 60, 60, 60, 60, 60)
	next.set.Validators = append(next.set.Validators, gnolight.Validator{PubKey: identity, Power: 60})
	next.set.Sort()
	w.g.sets[150] = next
	w.g.head = 160
	w.tick(t)
	if w.e.trusted != 149 || len(w.e.sentOf("update")) != 1 {
		t.Fatalf("the announcing header was not submitted: trusted %d", w.e.trusted)
	}
	text, ok := w.u.Alarm.Standing()["valset:small-order:"+hex.EncodeToString(identity[:8])]
	if !ok || !strings.Contains(text, "the set the announcing header announces at height 150") {
		t.Fatalf("no alarm for the small-order key announced: %v", w.u.Alarm.Standing())
	}
}
