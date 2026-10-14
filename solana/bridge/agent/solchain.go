package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// What the machines read of our three programs on Solana, and how a Solana failure is judged.

// clientState is the light client's state account read once: the head, the body and the ring.
type clientState struct {
	Head *sol.ClientHead
	Body *sol.ClientBody
	Ring []sol.RingEntry
	Slot uint64 // the slot the read was answered at
	Len  int
}

// InRing says whether a header hash is among the current set's accepted headers the client remembers.
func (s *clientState) InRing(hash [32]byte) bool {
	for _, e := range s.Ring {
		if e.Hash == hash {
			return true
		}
	}
	return false
}

func readClientState(ctx context.Context, c solReader, client sol.LightClient, commitment rpc.CommitmentType) (*clientState, error) {
	res, err := c.GetAccountInfoWithOpts(ctx, client.State(), &rpc.GetAccountInfoOpts{Commitment: commitment})
	if err != nil {
		return nil, fmt.Errorf("the client's state: %w", err)
	}
	if res.Value == nil || !res.Value.Owner.Equals(client.Program) {
		return nil, errors.New("the client's state account is not the client's")
	}
	data := res.Value.Data.GetBinary()
	head, body, ring, err := sol.ParseClientState(data)
	if err != nil {
		return nil, err
	}
	return &clientState{Head: head, Body: body, Ring: ring, Slot: res.Context.Slot, Len: len(data)}, nil
}

func readPausable(ctx context.Context, c solReader, p sol.Pausable, commitment rpc.CommitmentType) (*sol.PausableState, *sol.Guardians, error) {
	st, err := c.GetAccountInfoWithOpts(ctx, p.State(), &rpc.GetAccountInfoOpts{Commitment: commitment})
	if err != nil {
		return nil, nil, fmt.Errorf("the pausable ISM's state: %w", err)
	}
	fl, err := c.GetAccountInfoWithOpts(ctx, p.Flags(), &rpc.GetAccountInfoOpts{Commitment: commitment})
	if err != nil {
		return nil, nil, fmt.Errorf("the pausable ISM's flags: %w", err)
	}
	if st.Value == nil || fl.Value == nil || !st.Value.Owner.Equals(p.Program) || !fl.Value.Owner.Equals(p.Program) {
		return nil, nil, errors.New("the pausable ISM's accounts are not its own")
	}
	s, err := sol.ParsePausableState(st.Value.Data.GetBinary())
	if err != nil {
		return nil, nil, err
	}
	g, err := sol.ParsePausableFlags(fl.Value.Data.GetBinary())
	if err != nil {
		return nil, nil, err
	}
	return s, g, nil
}

func readAdmin(ctx context.Context, c solReader, a sol.Admin, commitment rpc.CommitmentType) (*sol.AdminState, error) {
	res, err := c.GetAccountInfoWithOpts(ctx, a.State(), &rpc.GetAccountInfoOpts{Commitment: commitment})
	if err != nil {
		return nil, fmt.Errorf("the admin program's state: %w", err)
	}
	if res.Value == nil || !res.Value.Owner.Equals(a.Program) {
		return nil, errors.New("the admin program's state account is not its own")
	}
	return sol.ParseAdminState(res.Value.Data.GetBinary())
}

// readDecision is a pending decision's account, nil when none stands (closed, or never recorded).
func readDecision(ctx context.Context, c solReader, a sol.Admin, number uint64, commitment rpc.CommitmentType) (*sol.Decision, error) {
	res, err := c.GetAccountInfoWithOpts(ctx, a.Decision(number), &rpc.GetAccountInfoOpts{Commitment: commitment})
	if errors.Is(err, rpc.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if res.Value == nil || !res.Value.Owner.Equals(a.Program) || len(res.Value.Data.GetBinary()) == 0 {
		return nil, nil
	}
	return sol.ParseDecision(res.Value.Data.GetBinary())
}

// readPendingDecisions reads every pending decision's account (the last 500 numbers), parsed, by number; an
// account that is not M2j's layout is an error, never read short.
func readPendingDecisions(ctx context.Context, c solReader, a sol.Admin, st *sol.AdminState, commitment rpc.CommitmentType) ([]*sol.Decision, error) {
	var out []*sol.Decision
	from := uint64(1)
	if st.LastSeq > 500 {
		from = st.LastSeq - 499
	}
	var keys []solana.PublicKey
	for n := from; n <= st.LastSeq; n++ {
		keys = append(keys, a.Decision(n))
	}
	for start := 0; start < len(keys); start += sol.MultiMax {
		end := min(start+sol.MultiMax, len(keys))
		res, err := c.GetMultipleAccountsWithOpts(ctx, keys[start:end], &rpc.GetMultipleAccountsOpts{Commitment: commitment})
		if err != nil {
			return out, err
		}
		for i, acc := range res.Value {
			if acc == nil || !acc.Owner.Equals(a.Program) || len(acc.Data.GetBinary()) == 0 {
				continue
			}
			d, err := sol.ParseDecision(acc.Data.GetBinary())
			if err != nil {
				return out, fmt.Errorf("decision %d: %w", from+uint64(start+i), err)
			}
			out = append(out, d)
		}
	}
	return out, nil
}

// ReadPendingDecisions is every pending decision at confirmed (exported for cmd/hlwatch's successor check).
func ReadPendingDecisions(ctx context.Context, c solReader, admin sol.Admin, st *sol.AdminState) ([]*sol.Decision, error) {
	return readPendingDecisions(ctx, c, admin, st, rpc.CommitmentConfirmed)
}

// accountData is an account's data at confirmed; an error when it does not stand.
func accountData(ctx context.Context, c solReader, k solana.PublicKey) ([]byte, error) {
	return accountDataAt(ctx, c, k, rpc.CommitmentConfirmed)
}

// accountDataAt is an account's data at a commitment; an error when it does not stand.
func accountDataAt(ctx context.Context, c solReader, k solana.PublicKey, commitment rpc.CommitmentType) ([]byte, error) {
	exists, data, err := accountExists(ctx, c, k, commitment)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("no account %s", k)
	}
	return data, nil
}

// accountExists is whether an account with data stands, at a commitment.
func accountExists(ctx context.Context, c solReader, k solana.PublicKey, commitment rpc.CommitmentType) (bool, []byte, error) {
	res, err := c.GetAccountInfoWithOpts(ctx, k, &rpc.GetAccountInfoOpts{Commitment: commitment})
	if errors.Is(err, rpc.ErrNotFound) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if res.Value == nil {
		return false, nil, nil
	}
	return true, res.Value.Data.GetBinary(), nil
}

var (
	customErrRE  = regexp.MustCompile(`custom program error: 0x([0-9a-fA-F]+)`)
	customErrRE2 = regexp.MustCompile(`Custom[(":\s]+(\d+)`)
)

// solErrCode is the custom program error code behind a failed send or simulation, if any.
func solErrCode(err error) (uint32, bool) {
	if err == nil {
		return 0, false
	}
	s := err.Error()
	if m := customErrRE.FindStringSubmatch(s); m != nil {
		v, e := strconv.ParseUint(m[1], 16, 32)
		return uint32(v), e == nil
	}
	if m := customErrRE2.FindStringSubmatch(s); m != nil {
		v, e := strconv.ParseUint(m[1], 10, 32)
		return uint32(v), e == nil
	}
	return 0, false
}

// solErrName names a program's refusal behind a failure ("" when it is not one of ours).
func solErrName(err error) string {
	if code, ok := solErrCode(err); ok {
		return sol.ProgramErrorName(code)
	}
	return ""
}

var solTransientRE = regexp.MustCompile(`(?i)connection refused|connection reset|no such host|timeout|timed out|EOF|dial tcp|unavailable|too many requests|503|502|504|blockhash not found|not confirmed|network is unreachable|context deadline|temporarily|rate limit|429`)

// solDeterministic says whether a Solana failure would repeat: a program refused it (a simulation failure or an
// on-chain error) rather than the transaction having been lost on the way.
func solDeterministic(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if solTransientRE.MatchString(s) {
		return false
	}
	return strings.Contains(s, "simulation failed") || strings.Contains(s, "failed:") || strings.Contains(s, "custom program error") || strings.Contains(s, "InstructionError") || strings.Contains(s, "Error processing Instruction")
}

// solErrIs says whether a failure is the named refusal of one of our programs.
func solErrIs(err error, name string) bool {
	return strings.HasSuffix(solErrName(err), ": "+name)
}
