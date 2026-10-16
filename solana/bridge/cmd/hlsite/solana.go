package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// solReader is the read side of Solana the site needs, which *rpc.Client gives and a fake supplies in tests.
type solReader interface {
	GetAccountInfoWithOpts(ctx context.Context, account solana.PublicKey, opts *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error)
	GetMultipleAccountsWithOpts(ctx context.Context, accounts []solana.PublicKey, opts *rpc.GetMultipleAccountsOpts) (*rpc.GetMultipleAccountsResult, error)
	GetTransaction(ctx context.Context, sig solana.Signature, opts *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error)
	GetSignaturesForAddressWithOpts(ctx context.Context, account solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error)
	GetLatestBlockhash(ctx context.Context, commitment rpc.CommitmentType) (*rpc.GetLatestBlockhashResult, error)
}

// solanaChain is Solana as the site reads it: our deployment of Hyperlane's mailbox and token programs, the
// pausable ISM, the admin program and the light client, through one provider (the monitors read two that must
// agree, PLAN.md §5.9; the site shows, it never flags).
type solanaChain struct {
	cfg       ChainConfig
	c         solReader
	progs     sol.Programs
	gnoDomain uint32
	// realms are every bridge realm's address on gno.land, refused as a burn's recipient (§5.2, K21): the
	// configured ones and the routes the mailbox registers or dropped, as the server last read them.
	realms func() []string
}

func newSolana(cfg ChainConfig, c solReader, gnoDomain uint32, realms func() []string) *solanaChain {
	s := &solanaChain{cfg: cfg, c: c, gnoDomain: gnoDomain, realms: realms}
	s.progs = sol.Programs{Mailbox: solana.MustPublicKeyFromBase58(cfg.Programs.Mailbox), Client: solana.MustPublicKeyFromBase58(cfg.Programs.Client),
		Pausable: solana.MustPublicKeyFromBase58(cfg.Programs.Pausable), Admin: solana.MustPublicKeyFromBase58(cfg.Programs.Admin)}
	for _, t := range cfg.Tokens {
		if i := tokenIndex(t.Symbol); i >= 0 {
			s.progs.Tokens[i] = solana.MustPublicKeyFromBase58(t.Program)
		}
	}
	return s
}

// tokenIndex is a token's place in the programs' order (config::TOKENS: 0 GNOT, 1 BUBBLE), −1 for another.
func tokenIndex(symbol string) int {
	switch symbol {
	case "GNOT":
		return 0
	case "BUBBLE":
		return 1
	}
	return -1
}

func (s *solanaChain) token(symbol string) *TokenConfig { return s.cfg.Token(symbol) }

func (s *solanaChain) link(kind, k string) string {
	if s.cfg.Explorer == "" {
		return ""
	}
	u := strings.TrimRight(s.cfg.Explorer, "/") + "/" + kind + "/" + k
	if s.cfg.Cluster != "" {
		u += "?cluster=" + s.cfg.Cluster
	}
	return u
}

func (s *solanaChain) TxLink(sig string) string    { return s.link("tx", sig) }
func (s *solanaChain) AccountLink(k string) string { return s.link("address", k) }
func (s *solanaChain) Address(b [32]byte) string   { return solana.PublicKeyFromBytes(b[:]).String() }
func (s *solanaChain) KindName(kind byte) string   { return sol.KindName(kind) }

func (s *solanaChain) IsSignature(ref string) bool {
	_, err := solana.SignatureFromBase58(ref)
	return err == nil
}

// account is an account's data at a commitment; exists false when it does not stand.
func (s *solanaChain) account(ctx context.Context, k solana.PublicKey, commitment rpc.CommitmentType) (data []byte, exists bool, err error) {
	res, err := s.c.GetAccountInfoWithOpts(ctx, k, &rpc.GetAccountInfoOpts{Commitment: commitment})
	if errors.Is(err, rpc.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if res.Value == nil {
		return nil, false, nil
	}
	return res.Value.Data.GetBinary(), true, nil
}

func (s *solanaChain) mustAccount(ctx context.Context, what string, k solana.PublicKey) ([]byte, error) {
	data, ok, err := s.account(ctx, k, rpc.CommitmentConfirmed)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if !ok {
		return nil, fmt.Errorf("%s: no account %s", what, k)
	}
	return data, nil
}

func guardiansJSON(g *sol.Guardians, now, grace int64) *GuardiansJSON {
	v := &GuardiansJSON{Hold: g.Hold(now, grace) != nil}
	for i, k := range g.Keys {
		v.Keys = append(v.Keys, k.String())
		v.Flags = append(v.Flags, flagJSON(g.Flags[i]))
	}
	v.HoldSince = flagJSON(g.HoldSince)
	return v
}

func flagJSON(f *sol.Flag) *FlagJSON {
	if f == nil {
		return nil
	}
	return &FlagJSON{Slot: f.Slot, Time: f.Time}
}

// Snapshot reads the Solana side: the supplies first (the cut's first step, §5.9), then the two objects' flags,
// the mint limits, the client's head and the admin program's pending decisions.
func (s *solanaChain) Snapshot(ctx context.Context, now time.Time) *OtherSnapshot {
	v := &OtherSnapshot{At: now.Unix(), Supplies: map[string]int64{}, Limits: map[string]LimitJSON{}}
	fail := func(what string, err error) { v.Errors = append(v.Errors, what+": "+err.Error()) }
	for _, t := range s.cfg.Tokens {
		mint := solana.MustPublicKeyFromBase58(t.Mint)
		supply, slot, err := sol.MintSupplyAt(ctx, s.c, mint, rpc.CommitmentFinalized) // the mint's own field: no indexed call
		if err != nil {
			fail(t.Symbol+" supply", err)
			continue
		}
		n, err := int64(supply), error(nil)
		if supply > 1<<62 {
			err = fmt.Errorf("%d is out of range", supply)
		}
		if err != nil {
			fail(t.Symbol+" supply", err)
			continue
		}
		v.Supplies[t.Symbol] = n
		if v.Slot == 0 || slot < v.Slot {
			v.Slot = slot
		}
	}
	factor := int64(1)
	client := s.progs.LightClient()
	if data, err := s.mustAccount(ctx, "the client", client.State()); err != nil {
		fail("the client", err)
	} else if h, err := sol.ParseClientHead(data); err != nil {
		fail("the client", err)
	} else {
		if h.ClockFactor > 0 {
			factor = int64(h.ClockFactor)
		}
		v.Client = &ClientJSON{TrustedHeight: h.TrustedHeight, QuarantineEnd: h.QuarantineEnd, LastAccepted: h.LastAcceptedTime, ClockFactor: factor, Powers: []int64{}}
		// the body: the set the headers are checked against (the same account; nothing more is read)
		if _, body, _, err := sol.ParseClientState(data); err == nil {
			v.Client.Validators, v.Client.TrustedTime = len(body.Set), body.Trusted.TimeSecs
			for _, val := range body.Set {
				v.Client.Powers = append(v.Client.Powers, val.Power)
			}
		}
	}
	grace := sol.UnpauseDelay(factor)
	pausable := s.progs.PausableISM()
	if data, err := s.mustAccount(ctx, "the pausable ISM's flags", pausable.Flags()); err != nil {
		fail("the pausable ISM's flags", err)
	} else if g, err := sol.ParsePausableFlags(data); err != nil {
		fail("the pausable ISM's flags", err)
	} else {
		v.Pausable = guardiansJSON(g, now.Unix(), grace)
	}
	if data, err := s.mustAccount(ctx, "the pausable ISM's limits", pausable.State()); err != nil {
		fail("the pausable ISM's limits", err)
	} else if st, err := sol.ParsePausableState(data); err != nil {
		fail("the pausable ISM's limits", err)
	} else {
		for _, t := range s.cfg.Tokens {
			if i := tokenIndex(t.Symbol); i >= 0 {
				l := st.Limits[i]
				v.Limits[t.Symbol] = LimitJSON{MaxPerMessage: l.MaxPerMessage, DayCap: l.DayCap, Used: l.Level(now.Unix(), sol.Day/factor)}
			}
		}
	}
	admin := s.progs.AdminProgram()
	data, err := s.mustAccount(ctx, "the admin program", admin.State())
	if err != nil {
		fail("the admin program", err)
		return v
	}
	st, err := sol.ParseAdminState(data)
	if err != nil {
		fail("the admin program", err)
		return v
	}
	v.Admin = guardiansJSON(&st.Guardians, now.Unix(), grace)
	v.LastSeq, v.SetupDone = st.LastSeq, st.SetupDone == 1
	hold := st.Guardians.Hold(now.Unix(), grace)
	pending, err := agent.ReadPendingDecisions(ctx, s.c, admin, st)
	if err != nil {
		fail("the pending decisions", err)
	}
	for _, d := range pending {
		p := PendingJSON{Number: d.Number, Kind: d.Kind, KindName: sol.KindName(d.Kind), Text: sol.DescribeParams(d.Kind, d.Params), Delivered: d.DeliveredTime, MaturesAt: d.MaturesAt(factor, hold, st)}
		switch {
		case hold != nil && d.HeldUntilTheEnd():
			p.Deferred = "two guardians hold the admin program: it waits until the hold ends"
		case hold != nil && d.HeldToTheDay():
			p.Deferred = "two guardians hold the admin program: it lands on the hold's 21st day at the earliest"
		case st.Guardians.AnyFlag() && sol.SevenDay(d.Kind):
			p.Deferred = "a flag stands on the admin program: it waits for the flag to clear"
		}
		v.Pending = append(v.Pending, p)
	}
	return v
}

// DeliveryOf is the transaction that processed a message id here (the newest signature on its processed-message
// record), nil when the record does not stand.
func (s *solanaChain) DeliveryOf(ctx context.Context, id [32]byte) (*TxRef, error) {
	rec := s.progs.MailboxProgram().ProcessedMessage(id)
	_, ok, err := s.account(ctx, rec, rpc.CommitmentFinalized)
	if err != nil || !ok {
		return nil, err
	}
	one := 1
	sigs, err := s.c.GetSignaturesForAddressWithOpts(ctx, rec, &rpc.GetSignaturesForAddressOpts{Limit: &one, Commitment: rpc.CommitmentFinalized})
	if err != nil {
		return nil, err
	}
	ref := &TxRef{Link: s.AccountLink(rec.String())}
	if len(sigs) > 0 && sigs[0] != nil {
		ref.Sig, ref.Slot = sigs[0].Signature.String(), sigs[0].Slot
		if sigs[0].BlockTime != nil {
			ref.Time = int64(*sigs[0].BlockTime)
		}
		ref.Link = s.TxLink(ref.Sig)
	}
	return ref, nil
}

var dispatchTag = []byte("DISPATCH")

// maxTxVersion pins the transaction version the RPC is asked for (the agent's G28: v1 is on mainnet).
const maxTxVersion = uint64(1)

// Burn reads every dispatch our mailbox recorded in one finalized transaction, as the agent's scanner does: a
// dispatched-message account the mailbox owns, tagged, made in that slot, carrying a message from one of our
// token programs toward gno.land.
func (s *solanaChain) Burn(ctx context.Context, ref string) ([]Burn, error) {
	sig, err := solana.SignatureFromBase58(ref)
	if err != nil {
		return nil, err
	}
	v := maxTxVersion
	res, err := s.c.GetTransaction(ctx, sig, &rpc.GetTransactionOpts{Encoding: solana.EncodingBase64, Commitment: rpc.CommitmentFinalized, MaxSupportedTransactionVersion: &v})
	if err != nil {
		if errors.Is(err, rpc.ErrNotFound) {
			return nil, errNoSuchTx
		}
		return nil, err
	}
	if res == nil || res.Transaction == nil {
		return nil, errNoSuchTx
	}
	if res.Meta != nil && res.Meta.Err != nil {
		return nil, fmt.Errorf("the transaction failed on Solana: %v", res.Meta.Err)
	}
	tx, err := res.Transaction.GetTransaction()
	if err != nil {
		return nil, err
	}
	keys := append(solana.PublicKeySlice{}, tx.Message.AccountKeys...)
	if res.Meta != nil {
		keys = append(keys, res.Meta.LoadedAddresses.Writable...)
		keys = append(keys, res.Meta.LoadedAddresses.ReadOnly...)
	}
	mailbox := s.progs.MailboxProgram()
	var out []Burn
	for start := 0; start < len(keys); start += sol.MultiMax {
		end := min(start+sol.MultiMax, len(keys))
		accts, err := s.c.GetMultipleAccountsWithOpts(ctx, keys[start:end], &rpc.GetMultipleAccountsOpts{Commitment: rpc.CommitmentFinalized})
		if err != nil {
			return nil, err
		}
		for _, a := range accts.Value {
			if a == nil || !a.Owner.Equals(mailbox.Program) {
				continue
			}
			ad := a.Data.GetBinary()
			if len(ad) < 53+hl.HeaderLen || ad[0] != 1 || !bytes.Equal(ad[1:9], dispatchTag) || binary.LittleEndian.Uint64(ad[13:21]) != res.Slot {
				continue
			}
			raw := ad[53:]
			m, err := hl.ParseMessage(raw)
			if err != nil || binary.LittleEndian.Uint32(ad[9:13]) != m.Nonce || m.Destination != s.gnoDomain {
				continue
			}
			id := hl.ID(raw)
			b := Burn{ID: hex.EncodeToString(id[:]), Nonce: m.Nonce, Slot: res.Slot, Sig: ref}
			if res.BlockTime != nil {
				b.Time = int64(*res.BlockTime)
			}
			for _, t := range s.cfg.Tokens {
				if bytes.Equal(solana.MustPublicKeyFromBase58(t.Program).Bytes(), m.Sender[:]) {
					b.Token = t.Symbol
				}
			}
			if rcpt, amt, _, err := hl.ParseTokenMessage(m.Body); err == nil && amt <= 1<<63-1 {
				b.Amount = int64(amt)
				if to, err := hl.Bytes32ToGnoAddress(rcpt); err == nil {
					b.To = to
				} else {
					b.BadTo = true
				}
			}
			out = append(out, b)
		}
	}
	return out, nil
}

// realmOf is the bridge realm a g1… address belongs to, "" for a wallet (the compare ignores case, as bech32
// does: the agent's G27).
func (s *solanaChain) realmOf(addr string) string { return realmOfAddress(s.realms, addr) }

// realmOfAddress is the bridge realm among realms() that a g1… address belongs to, "" for a wallet.
func realmOfAddress(realms func() []string, addr string) string {
	if realms == nil {
		return ""
	}
	for _, p := range realms() {
		if strings.EqualFold(hl.GnoPackageAddress(p), addr) {
			return p
		}
	}
	return ""
}

// BurnTx is the transaction `hlsol send` builds (cmd/hlsol), unsigned by the wallet: a compute budget, then the
// token program's transfer_remote toward gno.land, the sender paying. The throwaway key that names the dispatch
// record signs here and is forgotten; the wallet signs as the payer and sends through its own RPC.
func (s *solanaChain) BurnTx(ctx context.Context, t TokenConfig, sender, recipient string, amount uint64) (*BurnTx, error) {
	payer, err := solana.PublicKeyFromBase58(strings.TrimSpace(sender))
	if err != nil {
		return nil, errors.New("the sender is not a Solana address")
	}
	rcpt, err := hl.GnoAddressToBytes32(strings.TrimSpace(recipient))
	if err != nil {
		return nil, errors.New("the recipient is not a gno.land address (g1…)")
	}
	if realm := s.realmOf(strings.TrimSpace(recipient)); realm != "" {
		return nil, fmt.Errorf("%s is the address of %s, a bridge realm: a burn to it is swept on gno.land and paid only by a governance decision; name a wallet", strings.TrimSpace(recipient), realm)
	}
	if amount == 0 || amount > 1<<63-1 {
		return nil, errors.New("the amount must be above zero")
	}
	program, err := solana.PublicKeyFromBase58(t.Program)
	if err != nil {
		return nil, errors.New("unknown token")
	}
	bh, err := s.c.GetLatestBlockhash(ctx, rpc.CommitmentConfirmed)
	if err != nil {
		return nil, fmt.Errorf("a recent blockhash: %w", err)
	}
	unique := solana.NewWallet().PrivateKey
	token := sol.Synthetic{Program: program}
	mailbox := s.progs.MailboxProgram()
	tx, err := solana.NewTransaction([]solana.Instruction{
		sol.ComputeLimitIx(400_000),
		token.TransferRemoteIx(mailbox, payer, unique.PublicKey(), s.gnoDomain, rcpt, amount),
	}, bh.Value.Blockhash, solana.TransactionPayer(payer))
	if err != nil {
		return nil, err
	}
	if _, err := tx.PartialSign(func(k solana.PublicKey) *solana.PrivateKey {
		if k.Equals(unique.PublicKey()) {
			return &unique
		}
		return nil
	}); err != nil {
		return nil, err
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return &BurnTx{Tx: base64.StdEncoding.EncodeToString(raw), Unique: unique.PublicKey().String(), MessageAccount: mailbox.DispatchedMessage(unique.PublicKey()).String(),
		Blockhash: bh.Value.Blockhash.String(), LastValidBlockHeight: bh.Value.LastValidBlockHeight}, nil
}
