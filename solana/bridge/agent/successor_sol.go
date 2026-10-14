package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The admin program's line of succession (PLAN.md §6.5; AQ6): a kind 9 hands every authority to a successor and
// writes it into the predecessor's state (`successor`), the successor inheriting the live numbers, seats, record
// and hold history in the same instruction. From that block the successor is the admin program every tool acts
// on — the decisions, the foreign check, the pending list, the one-stroke flag, the relayer's classify and
// executor — with the config's program id as the starting point only: before, each outsider's hlwatch and the
// operator's monitors kept reading and flagging the retired program until every bridge.json was edited, and a
// forged decision delivered to the successor in that week matured with no hold on it.

// successorHops bounds the chain followed from the configured program (a kind 9 a year is a long line).
const successorHops = 4

// retiredAdmin is a program that handed over, with its state as read.
type retiredAdmin struct {
	Admin sol.Admin
	State *sol.AdminState
}

// adminChain is the line from the configured program to the live one.
type adminChain struct {
	Live    sol.Admin
	State   *sol.AdminState // the live program's
	Retired []retiredAdmin  // oldest first: the configured program and every successor before the live one
}

// followSuccessors reads the admin program's state from the configured id and follows `successor` as far as it
// goes (at most successorHops), every state read through the reader given (the monitor's and hlwatch's pair,
// the relayer's one provider).
func followSuccessors(ctx context.Context, c solReader, start sol.Admin, commitment rpc.CommitmentType) (*adminChain, error) {
	chain := &adminChain{Live: start}
	for hop := 0; ; hop++ {
		st, err := readAdmin(ctx, c, chain.Live, commitment)
		if err != nil {
			if hop == 0 {
				return nil, err
			}
			return nil, fmt.Errorf("the successor %s (hop %d from %s): %w", chain.Live.Program, hop, start.Program, err)
		}
		chain.State = st
		if st.Successor == nil || st.Successor.IsZero() {
			return chain, nil
		}
		if hop >= successorHops {
			return nil, fmt.Errorf("the admin program's line of succession from %s runs past %d hops at %s → %s", start.Program, successorHops, chain.Live.Program, *st.Successor)
		}
		next := *st.Successor
		for _, r := range chain.Retired {
			if r.Admin.Program.Equals(next) {
				return nil, fmt.Errorf("the admin program's line of succession from %s loops at %s", start.Program, next)
			}
		}
		if next.Equals(chain.Live.Program) {
			return nil, fmt.Errorf("the admin program %s names itself as its successor", next)
		}
		chain.Retired = append(chain.Retired, retiredAdmin{Admin: chain.Live, State: st})
		chain.Live = sol.Admin{Program: next}
	}
}

// Programs is the deployment's programs with the live admin program in the admin program's place.
func (c *adminChain) Programs(p sol.Programs) sol.Programs {
	if c == nil {
		return p
	}
	p.Admin = c.Live.Program
	return p
}

// IsLive and IsRetired say what a program id is in the line.
func (c *adminChain) IsLive(k solana.PublicKey) bool { return c != nil && c.Live.Program.Equals(k) }
func (c *adminChain) IsRetired(k solana.PublicKey) bool {
	if c == nil {
		return false
	}
	for _, r := range c.Retired {
		if r.Admin.Program.Equals(k) {
			return true
		}
	}
	return false
}

// Knows says whether a program id is the live program or a retired one of the line.
func (c *adminChain) Knows(k solana.PublicKey) bool { return c.IsLive(k) || c.IsRetired(k) }

// StateOf is the state read for a program of the line, nil for a stranger.
func (c *adminChain) StateOf(k solana.PublicKey) *sol.AdminState {
	if c == nil {
		return nil
	}
	if c.Live.Program.Equals(k) {
		return c.State
	}
	for _, r := range c.Retired {
		if r.Admin.Program.Equals(k) {
			return r.State
		}
	}
	return nil
}

// LiveHex is the live program as 32 bytes of hex (r/hlgov's recipient is rendered the same way).
func (c *adminChain) LiveHex() string { return hex.EncodeToString(c.Live.Program[:]) }

// RetiredHex lists the retired programs as hex, oldest first.
func (c *adminChain) RetiredHex() []string {
	var out []string
	for _, r := range c.Retired {
		out = append(out, hex.EncodeToString(r.Admin.Program[:]))
	}
	return out
}

// Text names the line for a status.
func (c *adminChain) Text() string {
	if c == nil || len(c.Retired) == 0 {
		return ""
	}
	var parts []string
	for _, r := range c.Retired {
		parts = append(parts, r.Admin.Program.String())
	}
	return fmt.Sprintf("the admin program is %s (a successor by kind 9; retired before it: %s)", c.Live.Program, strings.Join(parts, " → "))
}

// retiredWithSeat lists the retired programs whose state still stands and holds a seat for a key: the old
// states a one-stroke flag also marks while they stand (AQ6, §5.8).
func (c *adminChain) retiredWithSeat(key solana.PublicKey) []sol.Admin {
	var out []sol.Admin
	if c == nil {
		return nil
	}
	for _, r := range c.Retired {
		if r.State != nil && r.State.Guardians.Position(key) >= 0 {
			out = append(out, r.Admin)
		}
	}
	return out
}

// strokeIxs is the one-stroke flag's instructions: the pausable ISM and the live admin program in the first
// transaction (what §5.8's stroke and §5.9's header and decision rules rest on), each retired program whose
// state still holds the key's seat in a second (best effort: a retired program answers for itself, and a
// refusal there must not undo the stroke on the live objects).
func strokeIxs(progs sol.Programs, chain *adminChain, key solana.PublicKey, set, adminToo bool) (first, retired []solana.Instruction) {
	pausable := progs.PausableISM()
	live := chain.Programs(progs).AdminProgram()
	if set {
		first = append(first, pausable.SetFlagIx(key))
		if adminToo {
			first = append(first, live.SetFlagIx(key))
			for _, a := range chain.retiredWithSeat(key) {
				retired = append(retired, a.SetFlagIx(key))
			}
		}
		return first, retired
	}
	first = append(first, pausable.ClearFlagIx(key))
	if adminToo {
		first = append(first, live.ClearFlagIx(key))
		for _, a := range chain.retiredWithSeat(key) {
			retired = append(retired, a.ClearFlagIx(key))
		}
	}
	return first, retired
}

// FlagSolana sets (or clears) a key's flag on the pausable ISM and the admin program in one transaction: the
// one-stroke flag of §5.8's hlwatch, and the monitor's on the header and decision rules. The admin program is
// the live one of the line read from the chain (AQ6); each retired program whose state still holds the key's
// seat is flagged after it in a second transaction, its failure reported, never fatal. When the line cannot be
// read — a loop, a fifth hop, a successor's state this build does not parse — the stroke still goes to the
// pausable ISM and the configured program (G40: the flag §1's leak walk pauses the mints with must never die
// with a forged line), and the line's error comes back beside the signature: reads fail closed, the pausable
// half never.
func FlagSolana(ctx context.Context, c *sol.Client, progs sol.Programs, set bool) (solana.Signature, error) {
	chain, lineErr := followSuccessors(ctx, c.RPC, progs.AdminProgram(), rpc.CommitmentConfirmed)
	if lineErr != nil {
		chain = nil // the configured program stands in (strokeIxs on a nil line)
	}
	sig, err := flagSolanaOn(ctx, c, progs, chain, set)
	if err != nil {
		// the stroke failed, or was not confirmed: Send names the signature in its error for the holder to look up
		if lineErr != nil {
			return sig, fmt.Errorf("%w (the admin program's line of succession could not be read either: %v)", err, lineErr)
		}
		return sig, err
	}
	if lineErr != nil {
		return sig, &lineUnread{sig: sig, err: lineErr}
	}
	return sig, nil
}

// lineUnread is FlagSolana's error when the stroke landed and only the admin program's line could not be read
// (G40): its own type, so the callers tell it from a stroke that failed or went unconfirmed — Send returns the
// signature beside those errors too, and before, any error beside a signature printed as "the flag … set on the
// pausable ISM and the configured admin program" over objects that were not flagged (G50).
type lineUnread struct {
	sig solana.Signature
	err error
}

func (e *lineUnread) Error() string {
	return fmt.Sprintf("the stroke landed on the pausable ISM and the configured admin program (%s), but the admin program's line of succession could not be read, so a live successor may stand unflagged: %v", e.sig, e.err)
}

func (e *lineUnread) Unwrap() error { return e.err }

// LineUnreadAfterStroke says whether a FlagSolana error is the line's alone — the stroke itself landed: the
// caller prints the signature and the warning. It reads the error's type, never the signature's presence (G50).
func LineUnreadAfterStroke(sig solana.Signature, err error) bool {
	var lu *lineUnread
	return errors.As(err, &lu)
}

// flagSolanaOn is FlagSolana with the line read already.
func flagSolanaOn(ctx context.Context, c *sol.Client, progs sol.Programs, chain *adminChain, set bool) (solana.Signature, error) {
	key := c.Payer.PublicKey()
	first, retired := strokeIxs(progs, chain, key, set, true)
	sig, err := c.Send(ctx, first)
	if err != nil {
		return sig, err
	}
	if len(retired) > 0 {
		if rsig, rerr := c.Send(ctx, retired); rerr != nil {
			log.Printf("the stroke landed on the pausable ISM and the live admin program (%s); the retired program(s) refused it: %v", sig, rerr)
		} else {
			log.Printf("the stroke landed on the pausable ISM and the live admin program (%s) and on %d retired program(s) still standing (%s)", sig, len(retired), rsig)
		}
	}
	return sig, nil
}

// LiveAdmin is the admin program a tool acts on now: the line from the configured program followed on the
// chain (AQ6); exported for the commands (hlrecover).
func LiveAdmin(ctx context.Context, c solReader, progs sol.Programs) (sol.Programs, string, error) {
	chain, err := followSuccessors(ctx, c, progs.AdminProgram(), rpc.CommitmentConfirmed)
	if err != nil {
		return progs, "", err
	}
	return chain.Programs(progs), chain.Text(), nil
}
