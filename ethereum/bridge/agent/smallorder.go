package agent

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Small-order validator keys (the code audit's E2-ED3, round 2). A validator key whose 32 bytes decode — as Go's
// crypto/ed25519 decodes them, laxly: y reduced mod p, the sign of a zero x ignored — to a point of order dividing 8
// is a key anyone can sign for: [k]A is then one of eight points whatever the message, and R = [S]B − [k]A verifies
// for any S (for the identity key 0x0100…00, R = 0x0100…00 and S = 0 sign every message). tm2 verifies with Go, and
// the light client on Ethereum agrees with Go on every input (Ed25519.sol, PLAN.md §6.6), so such a slot's power
// counts toward 2/3 for anyone. Refusing such keys would diverge from gno.land and could stall the client; the
// machines alarm instead — the monitor and hlwatch for the sets the bridge trusts or is about to (the client's
// trusted set and the one it announced, gno.land's current set and the one its head announces), the updater for the
// sets an announcing header carries before it submits it. GovDAO's alert, never a refusal.

var (
	// edP is 2^255 − 19.
	edP = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	// edD is −121665/121666 mod p.
	edD = edMul(new(big.Int).Sub(edP, big.NewInt(121665)), new(big.Int).ModInverse(big.NewInt(121666), edP))
	// edSqrtM1 is 2^((p−1)/4) mod p, a square root of −1.
	edSqrtM1 = new(big.Int).Exp(big.NewInt(2), new(big.Int).Rsh(new(big.Int).Sub(edP, big.NewInt(1)), 2), edP)
	// edSqrtExp is (p+3)/8: for a square a, a^((p+3)/8) is a root of a or of −a.
	edSqrtExp = new(big.Int).Rsh(new(big.Int).Add(edP, big.NewInt(3)), 3)
)

func edMod(a *big.Int) *big.Int    { return new(big.Int).Mod(a, edP) }
func edMul(a, b *big.Int) *big.Int { return edMod(new(big.Int).Mul(a, b)) }

// edPoint is a point of edwards25519 in affine coordinates (−x² + y² = 1 + d·x²·y²).
type edPoint struct{ x, y *big.Int }

// add is the complete affine addition law (complete on edwards25519: a = −1 is a square, d is not).
func (a edPoint) add(b edPoint) edPoint {
	xy := edMul(edMul(a.x, b.x), edMul(a.y, b.y))
	dxy := edMul(edD, xy)
	one := big.NewInt(1)
	x := edMul(new(big.Int).Add(edMul(a.x, b.y), edMul(a.y, b.x)), new(big.Int).ModInverse(edMod(new(big.Int).Add(one, dxy)), edP))
	y := edMul(new(big.Int).Add(edMul(a.y, b.y), edMul(a.x, b.x)), new(big.Int).ModInverse(edMod(new(big.Int).Sub(one, dxy)), edP))
	return edPoint{x, y}
}

// edDecode decodes 32 bytes as Go's crypto/ed25519 decodes a public key: y the little-endian value of the low 255
// bits reduced mod p (a non-canonical y is taken), x the root of (y² − 1)/(d·y² + 1) whose parity is the top bit, a
// zero x whatever that bit says; false when x² has no root (Go refuses such a key: no signature verifies under it).
func edDecode(k [32]byte) (edPoint, bool) {
	var be [32]byte
	for i := range k {
		be[31-i] = k[i]
	}
	sign := be[0] >> 7
	be[0] &= 0x7f
	y := edMod(new(big.Int).SetBytes(be[:]))
	yy := edMul(y, y)
	u := edMod(new(big.Int).Sub(yy, big.NewInt(1)))
	v := edMod(new(big.Int).Add(edMul(edD, yy), big.NewInt(1)))
	xx := edMul(u, new(big.Int).ModInverse(v, edP))
	x := new(big.Int).Exp(xx, edSqrtExp, edP)
	if edMul(x, x).Cmp(xx) != 0 {
		x = edMul(x, edSqrtM1)
		if edMul(x, x).Cmp(xx) != 0 {
			return edPoint{}, false
		}
	}
	if x.Sign() != 0 && uint(x.Bit(0)) != uint(sign) {
		x = new(big.Int).Sub(edP, x)
	}
	return edPoint{x, y}, true
}

// smallOrderKey says whether a key decodes to a point of order dividing 8: [8]A is the identity (0, 1).
func smallOrderKey(k [32]byte) bool {
	p, ok := edDecode(k)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		p = p.add(p)
	}
	return p.x.Sign() == 0 && p.y.Cmp(big.NewInt(1)) == 0
}

// smallKey is one small-order key of a set and its power.
type smallKey struct {
	Key   [32]byte
	Power int64
}

// smallOrderOf are a set's small-order keys and the set's total power.
func smallOrderOf(set []gnolight.Validator) (keys []smallKey, total int64) {
	for _, v := range set {
		total += v.Power
		if smallOrderKey(v.PubKey) {
			keys = append(keys, smallKey{Key: v.PubKey, Power: v.Power})
		}
	}
	return keys, total
}

// smallOrderAlarmKey is a small-order key's alarm.
func smallOrderAlarmKey(k [32]byte) string { return "valset:small-order:" + hex.EncodeToString(k[:8]) }

// smallOrderText says what a small-order key in a set means.
func smallOrderText(k smallKey, total int64, what string, height int64) string {
	share := 0.0
	if total > 0 {
		share = 100 * float64(k.Power) / float64(total)
	}
	return fmt.Sprintf("validator key 0x%s (power %d of %d, %.1f%%) in %s at height %d decodes to a point of small order (its order divides 8): anyone can sign for it — Go's crypto/ed25519, which tm2 and gno.land's nodes run, accepts such signatures, and so does the light client on Ethereum — so its power counts toward 2/3 for anyone (the code audit's E2-ED3): an alert for GovDAO and the guardians, never a refusal",
		hex.EncodeToString(k.Key[:]), k.Power, total, share, what, height)
}

// keyReader is a gno node's headers and validator sets by height (gno.Client, the updater's gnoside.Light).
type keyReader interface {
	BlockAt(ctx context.Context, h int64) (*gnolight.Block, error)
	ValidatorsAt(ctx context.Context, h int64) (*gnolight.ValidatorSet, error)
}

// setCheck is a set's small-order keys, kept by its hash (a set is read once).
type setCheck struct {
	keys  []smallKey
	total int64
}

// checkKeys alarms every small-order key of the sets the bridge trusts or is about to: the client's trusted set
// (height T) and the set it announced (T+1, while the client waits for it), gno.land's set at the own node's head H
// and the set H announces (H+1). Each set is read once by its hash; a set that cannot be read leaves the standing
// alarms as they are (no verdict on what was not read), and an alarm clears once no set read holds its key.
func (m *Monitor) checkKeys(ctx context.Context, H int64, cs *ethx.ClientState) {
	kr, ok := m.own.(keyReader)
	if !ok {
		return
	}
	if m.setChecks == nil || len(m.setChecks) > 64 {
		m.setChecks = map[[32]byte]*setCheck{}
	}
	type at struct {
		h    int64
		what string
	}
	var hs []at
	if cs != nil && cs.TrustedHeight > 0 && cs.TrustedHeight <= H {
		hs = append(hs, at{cs.TrustedHeight, "the light client's trusted set"})
		if cs.Announced() {
			hs = append(hs, at{cs.TrustedHeight + 1, "the set the light client was announced"})
		}
	}
	hs = append(hs, at{H, "gno.land's set"}, at{H + 1, "the set gno.land's head announces"})
	standing := map[string]string{}
	complete := true
	for _, x := range hs {
		var hash [32]byte
		if x.h <= H {
			blk, err := kr.BlockAt(ctx, x.h)
			if err != nil {
				complete = false
				continue
			}
			m.noteHeader(blk) // held to the client's header encoding (E3-AS3)
			hash = blk.Header.ValidatorsHashArray()
		} else {
			blk, err := kr.BlockAt(ctx, x.h-1)
			if err != nil {
				complete = false
				continue
			}
			m.noteHeader(blk)
			hash = blk.Header.NextValidatorsHashArray()
		}
		sc, ok := m.setChecks[hash]
		if !ok {
			set, err := kr.ValidatorsAt(ctx, x.h)
			if err != nil {
				complete = false
				continue
			}
			if sh, _ := set.Hash(); sh != hash {
				m.encodingFindings = append(m.encodingFindings, fmt.Sprintf("%s at height %d hashes to %s by the client's validator leaf, and the header's hash of it is %s (the validator leaf's encoding)", x.what, x.h, short(hex.EncodeToString(sh[:])), short(hex.EncodeToString(hash[:]))))
				complete = false
				continue
			}
			keys, total := smallOrderOf(set.Validators)
			sc = &setCheck{keys: keys, total: total}
			m.setChecks[hash] = sc
		}
		for _, k := range sc.keys {
			key := smallOrderAlarmKey(k.Key)
			if _, seen := standing[key]; !seen {
				standing[key] = smallOrderText(k, sc.total, x.what, x.h)
			}
		}
	}
	keys := make([]string, 0, len(standing))
	for k := range standing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m.Alarm.Alarm(k, standing[k])
	}
	if !complete {
		// a set that did not read is said, never passed in silence (E4-GT5's class): the page is not green over it
		m.blind("the small-order keys rule (E2-ED3): a validator set it reads (the client's, or gno.land's at the head and the one it announces) could not be read from this node this poll")
		return
	}
	for k := range m.Alarm.Standing() {
		if strings.HasPrefix(k, "valset:small-order:") {
			if _, ok := standing[k]; !ok {
				m.Alarm.Clear(k)
			}
		}
	}
}

// smallOrderBeforeAnnouncing alarms the small-order keys of the sets an announcing header carries — the set that
// signs it and the set it announces — before the updater submits it (it submits it all the same: the client follows
// what gno.land's validators sign).
func smallOrderBeforeAnnouncing(alarm *Alarmer, sh *ethx.SignedHeader) {
	for _, x := range []struct {
		set  *gnolight.ValidatorSet
		what string
		h    int64
	}{{sh.Set, "the set signing the announcing header", sh.Header.Height}, {sh.Next, "the set the announcing header announces", sh.Header.Height + 1}} {
		if x.set == nil {
			continue
		}
		keys, total := smallOrderOf(x.set.Validators)
		for _, k := range keys {
			alarm.Alarm(smallOrderAlarmKey(k.Key), smallOrderText(k, total, x.what, x.h))
		}
	}
}
