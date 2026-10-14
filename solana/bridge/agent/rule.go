package agent

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"

	"github.com/gnolang/bridges/solana/bridge/hl"
)

// The signature rule (PLAN.md §5.7, M1-NOTES §5.1): hlmailbox's verifier checks every signature the metadata
// carries, in increasing signer order, and then the rule — so the relayer sends exactly the signatures the rule
// needs and not one more: the lowest-indexed valid ones up to the threshold, or the lowest-indexed valid one of
// each group.

// pickSignatures chooses, from the signatures gathered (by signer index), the ones the rule needs. Every
// signature given must already verify. It errors when the rule cannot be met with them.
func pickSignatures(rule Rule, have map[int][64]byte) ([]hl.EdSignature, error) {
	var idx []int
	for i := range have {
		if i >= 0 && i < len(rule.Keys) {
			idx = append(idx, i)
		}
	}
	sort.Ints(idx)
	var chosen []int
	if rule.TwoGroups() {
		a, b := -1, -1
		for _, i := range idx {
			if a < 0 && contains(rule.GroupA, i) {
				a = i
			} else if b < 0 && contains(rule.GroupB, i) {
				b = i
			}
		}
		if a < 0 || b < 0 {
			return nil, fmt.Errorf("the rule (%s) needs one of each group; have %v", rule, idx)
		}
		chosen = []int{a, b}
		sort.Ints(chosen)
	} else {
		if len(idx) < rule.Threshold {
			return nil, fmt.Errorf("the rule (%s) needs %d; have %d", rule, rule.Threshold, len(idx))
		}
		chosen = idx[:rule.Threshold]
	}
	var out []hl.EdSignature
	for _, i := range chosen {
		out = append(out, hl.EdSignature{Validator: uint8(i), Sig: have[i]})
	}
	return out, nil
}

// verifyOne checks one signer's signature over a message's checkpoint digest.
func verifyOne(key ed25519.PublicKey, origin uint32, cp hl.Checkpoint, id [32]byte, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	d := hl.CheckpointDigest(origin, cp.Hook, cp.Root, cp.Index, id)
	return ed25519.Verify(key, d[:], sig)
}

// satisfied says whether the rule is met by the signers that signed, as the verifier judges it.
func satisfied(rule Rule, signed []int) bool {
	if rule.TwoGroups() {
		a, b := false, false
		for _, i := range signed {
			a = a || contains(rule.GroupA, i)
			b = b || contains(rule.GroupB, i)
		}
		return a && b
	}
	return rule.Threshold >= 1 && len(signed) >= rule.Threshold
}

func contains(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

var errNoSigners = errors.New("no signer in the config holds a key of hlmailbox's set")
