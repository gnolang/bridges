package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The checkpoint of a fresh install (G29, `-record-from client`): a monitor or an hlwatch on a provider without
// the full history never built the client's record from the deploy, so it starts from the client's own counter,
// running hash, ring and set hash at a slot — everything at or below the slot passed over, the record folded
// forward and checked against the chain from there on. What round 5 added: the trusted header the base rests on
// is checked against the own node's block first and a differing one is a mismatch, never a base (G37); the ring
// is taken in the program's eviction order, not the account's slot order (F21); the open stages are read from
// the client's own stage accounts, whatever their age (G37); and the dispatch index's cursors start at the
// checkpoint's slot too, so the forged-credit rule is not muted for good by transactions the provider no longer
// serves (G36). Every read goes through both providers, byte for byte (G45).

// programAccountsReader lists a program's accounts: solana-go's client has it, and the pair answers what both
// providers say alike.
type programAccountsReader interface {
	GetProgramAccountsWithOpts(ctx context.Context, program solana.PublicKey, opts *rpc.GetProgramAccountsOpts) (rpc.GetProgramAccountsResult, error)
}

// ringInOrder is the client's ring oldest first, as the program evicts (F21): the account lists the slots
// 0..count−1, a ring not yet full was filled in that order, and a full ring's oldest entry sits at ring_next —
// the slot the next push overwrites — so the mirror rotates by it; taken in slot order, the mirror evicted the
// wrong entry for the next 256 pushes, a by-header proof on either side's wrong set made Consistent differ and
// header:gap flagged both objects after two minutes: a false pause. The ring is full at sol.Ring entries
// whatever the account holds (F29: between a growth's resize and its relocation the account has room for more,
// while Ring::push caps at RING — read then, the mirror took slot order for a full ring).
func ringInOrder(cs *clientState) [][32]byte {
	ring := make([][32]byte, 0, len(cs.Ring))
	for _, e := range cs.Ring {
		ring = append(ring, e.Hash)
	}
	next := int(cs.Head.RingNext)
	if capacity := min(sol.RingCapacity(cs.Head, cs.Len), sol.Ring); capacity > 0 && len(ring) >= capacity && next > 0 && next < len(ring) {
		ring = append(append([][32]byte{}, ring[next:]...), ring[:next]...)
	}
	return ring
}

// checkpointBase takes the client's own record at a read as an empty record's base, once the client's trusted
// header agrees with the own node's block at that height (G37: a forger who ran the client one header ahead and
// then fed honest headers would otherwise plant a forked counter and hash below the checkpoint, below every
// later check — the base is refused, and the mismatch names the checkpoint's slot as §6.4's cut). A node with no
// block at that height is an error, and so is a record that holds entries already (StartFrom).
func checkpointBase(ctx context.Context, own gnoReader, cs *clientState, rec *ClientRecord) (*Mismatch, error) {
	T := int64(cs.Head.TrustedHeight)
	b, err := own.Block(ctx, T)
	if err != nil {
		return nil, fmt.Errorf("the own node's block at the client's trusted height %d: %w", T, err)
	}
	client := hex.EncodeToString(cs.Head.TrustedHash[:])
	if b.Hash == "" || !strings.EqualFold(b.Hash, client) {
		return &Mismatch{Height: T, Client: client, Chain: strings.ToLower(b.Hash), Slot: cs.Slot}, nil
	}
	return nil, rec.StartFrom(cs.Body.Records, cs.Body.RunningHash, cs.Slot, ringInOrder(cs), cs.Body.SetHash)
}

// seedStages remembers the client's open stage accounts in a checkpointed record (G37): a stage opened at any
// age before the checkpoint and finished or consumed after it would otherwise be a finish with no staged header
// seen (Gaps++, a false unfillable gap). Each stage's header hash is what the fold needs; an update stage carries
// its header (the height and the validators' hash, for the set-switch rule), a by-ring proof the copied entry's
// height. A reader that cannot list a program's accounts seeds nothing here (the transactions' window stands).
func seedStages(ctx context.Context, c solReader, client sol.LightClient, rec *ClientRecord) (int, error) {
	pa, ok := c.(programAccountsReader)
	if !ok {
		return 0, nil
	}
	res, err := pa.GetProgramAccountsWithOpts(ctx, client.Program, &rpc.GetProgramAccountsOpts{Commitment: rpc.CommitmentFinalized, Encoding: solana.EncodingBase64})
	if err != nil {
		return 0, err
	}
	rec.init()
	n := 0
	for _, ka := range res {
		if ka == nil || ka.Account == nil || ka.Pubkey.Equals(client.State()) {
			continue
		}
		st, err := sol.ParseStage(ka.Account.Data.GetBinary())
		if err != nil || st.HeaderHash == [32]byte{} {
			continue
		}
		ref := stageRef{Hash: hex.EncodeToString(st.HeaderHash[:])}
		switch {
		case st.Header != nil:
			ref.Height, ref.Vals = st.Header.Height, hex.EncodeToString(st.Header.ValidatorsHash)
		case st.ByRing:
			ref.Height = st.RingHeight
		}
		rec.Stages[ka.Pubkey.String()] = ref
		n++
	}
	return n, nil
}

// signatureAtOrBelow is the newest signature of an account finalized at or below a slot, "" when every signature
// is above it or the account has none: the dispatch index's cursor at a checkpoint (G36), so the scan lists
// nothing the provider may no longer serve — the claims credited before the checkpoint are trusted as the headers
// below it are.
func signatureAtOrBelow(ctx context.Context, c solReader, addr solana.PublicKey, slot uint64) (string, error) {
	var before solana.Signature
	limit := 1000
	for page := 0; page < 1000; page++ {
		sigs, err := c.GetSignaturesForAddressWithOpts(ctx, addr, &rpc.GetSignaturesForAddressOpts{Limit: &limit, Commitment: rpc.CommitmentFinalized, Before: before})
		if err != nil {
			return "", err
		}
		for _, s := range sigs {
			if s.Slot <= slot {
				return s.Signature.String(), nil
			}
		}
		if len(sigs) < limit {
			return "", nil
		}
		before = sigs[len(sigs)-1].Signature
	}
	return "", errors.New("a thousand pages of signatures above the checkpoint's slot")
}

// seedCursors starts each route's empty scan cursors at the checkpoint's slot on each provider (G36): before,
// a fresh install's first scan listed every signature of each token program since the deploy, the old ones
// answered null for good and the forged-credit rule never ran on that machine. A cursor set already is kept. Each
// one seeded is read through the checkpoint's slot (through, E6-SF1): nothing below it is read, by design, so a
// provider with a day of history that no longer knows the seeded signature lists back to that slot instead.
func seedCursors(ctx context.Context, providers [2]solReader, cfg *Config, slot uint64, cursors map[string]string, through map[string]uint64) (int, error) {
	n := 0
	for _, r := range cfg.Routes {
		if r.Token == "" {
			continue
		}
		for i, c := range providers {
			key := r.Symbol + "#" + strconv.Itoa(i)
			if cursors[key] != "" || through[key] > 0 {
				continue
			}
			sig, err := signatureAtOrBelow(ctx, c, r.TokenKey(), slot)
			if err != nil {
				return n, fmt.Errorf("%s on provider %d: %w", r.Symbol, i+1, err)
			}
			if sig != "" {
				cursors[key] = sig
				n++
			}
			through[key] = slot
		}
	}
	return n, nil
}

// The dispatch index at a checkpoint (AS3, G49, K40): the scan cursors start at the checkpoint's slot, so the
// transactions below it are never read — and every waiting claim credited before it would have read as a forged
// credit on the second poll, flagging credits, payouts and the pausable ISM on a fresh install (the ordinary
// state under load: claims paid in installments over days, every claim waiting under a payouts flag — the very
// incident a monitor gets rebuilt in). So the index is seeded from the mailbox's dispatched-message accounts
// themselves — one getProgramAccounts on the mailbox program filtered to the dispatched-message discriminator,
// through both providers with equal sets, the accounts never being closed, so the index is complete whatever
// the provider's history — and, where a provider refuses the call (the public RPCs answer 403 for a large
// program), the rule falls back to the cut's height: a claim credited at or below the own node's head at the
// checkpoint (IndexFromHeight) is trusted as the headers below the checkpoint are, and one credited after it
// whose burn may predate the checkpoint's slot is alarmed (credit:unindexed-before-base:<id>), never flagged,
// until the accounts list (the seed is retried hourly).

// seedDispatches folds the mailbox's dispatched-message accounts toward gno.land into the index: every burn of a
// configured route's token program, with both providers' bits set (the pair compared the bytes). It answers how
// many it indexed; a reader that cannot list the program's accounts errors.
func seedDispatches(ctx context.Context, c solReader, cfg *Config, mailbox sol.Mailbox, gnoDomain uint32, index map[string]*dispatchRec, now time.Time, alarm *Alarmer) (int, error) {
	pa, ok := c.(programAccountsReader)
	if !ok {
		return 0, errors.New("the reader lists no program accounts")
	}
	res, err := pa.GetProgramAccountsWithOpts(ctx, mailbox.Program, &rpc.GetProgramAccountsOpts{Commitment: rpc.CommitmentFinalized, Encoding: solana.EncodingBase64,
		Filters: []rpc.RPCFilter{{Memcmp: &rpc.RPCFilterMemcmp{Offset: 1, Bytes: solana.Base58(dispatchTag)}}}})
	if err != nil {
		return 0, err
	}
	tokens := map[solana.PublicKey]string{}
	for _, r := range cfg.Routes {
		if r.Token != "" {
			tokens[r.TokenKey()] = r.Symbol
		}
	}
	n := 0
	for _, ka := range res {
		if ka == nil || ka.Account == nil || !ka.Account.Owner.Equals(mailbox.Program) {
			continue
		}
		ad := ka.Account.Data.GetBinary()
		if len(ad) < 53+hl.HeaderLen || ad[0] != 1 || !bytes.Equal(ad[1:9], dispatchTag) {
			continue
		}
		d := solDispatch{Slot: binary.LittleEndian.Uint64(ad[13:21]), Raw: append(hexBytes{}, ad[53:]...)}
		if err := d.fill(); err != nil || binary.LittleEndian.Uint32(ad[9:13]) != d.Nonce {
			continue
		}
		symbol, ours := tokens[solana.PublicKeyFromBytes(d.Msg.Sender[:])]
		if !ours || d.Msg.Origin != cfg.Solana.Domain || d.Msg.Destination != gnoDomain {
			continue
		}
		if _, known := index[d.IDHex()]; known {
			continue
		}
		foldDispatch(index, symbol, 0, d, now, alarm)
		foldDispatch(index, symbol, 1, d, now, alarm)
		n++
	}
	return n, nil
}

// unindexedVerdict is the forged-credit rule's reading of a waiting claim whose id the index lacks.
type unindexedVerdict int

const (
	unindexedCandidate  unindexedVerdict = iota // judged as before: a forged credit once it has stood a poll
	unindexedBeforeBase                         // credited at or below the checkpoint's height: trusted, not judged
	unindexedUnseeded                           // credited after the cut while the index was never seeded: alarmed, never flagged
)

// judgeUnindexed reads an unindexed claim against the checkpoint (G49, AS3): a record built from the deploy, or
// an index seeded whole from the mailbox's accounts, judges every claim; a checkpointed index trusts a claim
// credited at or below the cut's height, and one credited after it only alarms while the accounts were never
// listed, since its burn may predate the checkpoint's slot (a burn parked a week and delivered after it).
func judgeUnindexed(height, fromHeight int64, seeded bool) unindexedVerdict {
	switch {
	case fromHeight <= 0:
		return unindexedCandidate
	case height <= fromHeight:
		return unindexedBeforeBase
	case !seeded:
		return unindexedUnseeded
	}
	return unindexedCandidate
}

// indexSeedRetry is how long an unseeded index waits before the mailbox's accounts are listed again.
const indexSeedRetry = time.Hour

// beforeBaseText is the alarm's text for a claim credited after the cut on an unseeded index.
func beforeBaseText(id, symbol, to string, amount, height, fromHeight int64) string {
	return fmt.Sprintf("claim %s on %s (credited at height %d to %s for %d) is unindexed and the index starts at a checkpoint whose accounts could not be listed: its burn may predate the checkpoint's slot (a burn parked and delivered after it), or there is none — alarmed, never flagged, until the mailbox's dispatched-message accounts list (retried hourly; the identity rule still judges the supply); the claims at or below height %d are trusted as the headers below the checkpoint are (G49, AS3)", id, symbol, height, to, amount, fromHeight)
}

// checkpointText says what a checkpoint did, for the log and the standing alarm.
func checkpointText(cs *clientState, stages, cursors int, fromHeight int64, seeded int, seedErr error) string {
	index := fmt.Sprintf("the dispatch index seeded whole from %d dispatched-message accounts of the mailbox (complete whatever the provider's history: the forged-credit rule judges every claim)", seeded)
	if seedErr != nil {
		index = fmt.Sprintf("the mailbox's dispatched-message accounts could not be listed (%v): the index starts at the slot — claims credited at or below height %d are trusted as the headers below the checkpoint are, later unindexed ones alarmed and never flagged until the accounts list (retried hourly)", seedErr, fromHeight)
	}
	return fmt.Sprintf("the record starts from the client's own count of %d (running hash %x…) at slot %d (-record-from client): the headers before it are not checked against the chain; from here on they are — the trusted header %d agrees with this node, %d open stages seeded from the client's accounts, %d scan cursors start at the slot, the cut's height %d; %s (G36, G37, F21, G49)", cs.Body.Records, cs.Body.RunningHash[:8], cs.Slot, cs.Head.TrustedHeight, stages, cursors, fromHeight, index)
}

// checkpointMismatchText names a checkpoint refused on a forked trusted header: §6.4's cut at the checkpoint's
// slot, for a flag.
func checkpointMismatchText(mm *Mismatch) string {
	return fmt.Sprintf("HEADER MISMATCH AT THE CHECKPOINT: the client's trusted header %d hashes to %s, this node's block to %s — the base is refused (nothing below slot %d is trusted); the cut is slot %d (§6.4): flag the objects, read the record from the deploy on a provider that serves it (G37)", mm.Height, short16(mm.Client), short16(mm.Chain), mm.Slot, mm.Slot)
}

func short16(s string) string {
	if len(s) > 16 {
		return s[:16] + "…"
	}
	return s
}
