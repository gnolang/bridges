// Command hlrecover is any member's tool on the admin program (PLAN.md §6.5; K3, AO1): the state, the holds and
// the hold history (`status`), a decision body for comparing a proposal's bytes (`body`), and the execution of
// a matured decision from any funded key (`execute`). It stages, signs, submits, records, closes and settles
// nothing: every change on Solana is a light-client proof of a GovDAO decision (§6.4), what a hold cannot cure
// is a new deployment (§1), and GovDAO's channel is the mailbox alone.
//
//	hlrecover version
//	hlrecover status  -config bridge.json                                   the client's trusted header, the admin program's numbers, the holds, the hold history
//	hlrecover body    -kind K [-cancel 0/5,0/7 | -cancel 0/5-9 -salt N | -object 0|1 | -seats 3=KEY,6=KEY | -token T -max N -day-cap N | -params HEX]
//	                                                                         the body (sequence 0) r/hlgov's builder would dispatch for the kind, to compare a proposal's bytes
//	hlrecover execute -config bridge.json -payer KEY.json -number N [-max-rent SOL] [-yes]
//	                                                                         Execute a matured mailbox decision from any funded key (AO1: §6.5's "anyone executes it then" —
//	                                                                         GovDAO's replacement of the operator's seats, an upgrade — when the operator's relayer is down);
//	                                                                         a kind 8 that needs its program data grown gets one ExtendProgram for the whole excess — its size
//	                                                                         and rent printed first, refused above -max-rent (0.5 SOL) without -yes (AP5) — then execute
//	                                                                         again next block
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

const usage = "usage: hlrecover version|status|body|execute [flags]"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	config := fs.String("config", "", "the deployment's facts (bridge.json): the programs, a Solana provider")
	rpcURL := fs.String("rpc", "", "a Solana RPC (the config's first provider when empty)")
	number := fs.Uint64("number", 0, "execute: the decision's number")
	kind := fs.Uint("kind", 0, "body: the decision kind (§13; kinds 4, 10 and 11 are reserved — recorded and spent)")
	cancel := fs.String("cancel", "", "body, kind 1: a list (0/5,0/7) or a range (0/5-9) of channel/number (the channel byte is 0: the one channel)")
	salt := fs.Uint64("salt", 0, "body, kind 1: the salt (0: the time now)")
	object := fs.Uint("object", 0, "body, kind 2: 0 the pausable ISM, 1 the admin program")
	seats := fs.String("seats", "", "body, kind 5: seat=KEY pairs, comma separated")
	params := fs.String("params", "", "body: raw parameters in hex, for any kind")
	token := fs.Uint("token", 0, "body, kind 12: the token (0 GNOT, 1 BUBBLE)")
	maxPer := fs.Uint64("max", 0, "body, kind 12: the per-message maximum, base units")
	dayCap := fs.Uint64("day-cap", 0, "body, kind 12: the rolling day's cap, base units")
	payerFile := fs.String("payer", "", "execute: the payer's Solana key file (the transaction's fee, a growth's rent)")
	maxRent := fs.Float64("max-rent", 0.5, "execute: the most SOL of program-data rent a kind 8's growth may sink from the payer without -yes (AP5: printed before it is paid, never returned)")
	yes := fs.Bool("yes", false, "execute: pay a growth's rent above -max-rent")
	_ = fs.Parse(args)

	ctx := context.Background()
	log.SetOutput(agent.RedactWriter{W: os.Stderr}) // no provider URL's key in a log line (G16)
	switch cmd {
	case "version":
		// the reproducible build hash the launch notice pins beside hlwatch's (K36)
		fmt.Println(agent.BuildHash("hlrecover"))
		return
	case "body":
		b, err := buildBody(byte(*kind), *cancel, *salt, byte(*object), *seats, *params, byte(*token), *maxPer, *dayCap)
		check(err)
		fmt.Printf("body: %s\n", hex.EncodeToString(b))
		fmt.Printf("(kind %d, sequence 0: r/hlgov's builder dispatches this body under its own number; a proposal's bytes are compared with these)\n", *kind)
		return
	}
	if *config == "" {
		log.Fatal("hlrecover: -config is required")
	}
	cfg, c, progs := open(*config, *rpcURL)
	_ = cfg
	f, err := agent.ReadAdminFacts(ctx, c.RPC, progs, time.Now().Unix())
	check(err)
	// the admin program is the live one of the line read from the chain (AQ6): the config names the start
	progs = f.Programs
	if f.Line != "" {
		fmt.Println(f.Line)
	}
	switch cmd {
	case "status":
		printStatus(f)
	case "execute":
		if *payerFile == "" || *number == 0 {
			log.Fatal("hlrecover execute: -payer and -number are required")
		}
		k, err := sol.PrivateKeyFromFile(*payerFile)
		check(err)
		c.Payer = k
		what, err := agent.ExecuteDecision(ctx, c, progs, *number, time.Now(), agent.ExtendTerms{MaxRent: uint64(*maxRent * 1e9), Confirmed: *yes, Out: os.Stdout})
		check(err)
		fmt.Println(what)
	default:
		log.Fatal(usage)
	}
}

func open(config, rpcURL string) (*agent.Config, *sol.Client, sol.Programs) {
	cfg, err := agent.LoadConfig(config)
	check(err)
	if !cfg.HasPrograms() {
		log.Fatal("hlrecover: the config names no client, pausable ISM and admin program")
	}
	if rpcURL == "" {
		if len(cfg.Solana.RPCs) == 0 {
			log.Fatal("hlrecover: -rpc or solana.rpcs is needed")
		}
		rpcURL = cfg.Solana.RPCs[0]
	}
	return cfg, sol.NewClient(rpcURL, nil), cfg.Programs()
}

func printStatus(f *agent.AdminFacts) {
	fmt.Println(agent.BuildHash("hlrecover")) // the build the notice pins (K36)
	th := f.TrustedHash()
	fmt.Printf("chain %s, clock factor %d; the client's trusted height %d (%x), last accepted %s\n", f.ChainID(), f.Factor, f.Client.Head.TrustedHeight, th[:8], time.Unix(f.LastAccepted(), 0).UTC().Format(time.RFC3339))
	fmt.Printf("the admin program: last decision number %d (the next takes %d), %d pending decision accounts\n", f.Admin.LastSeq, f.Admin.LastSeq+1, f.Admin.Pending)
	fmt.Printf("holds: the admin program %s; the pausable ISM %s\n", flagText(f.Hold), flagText(f.PHold))
	if len(f.Admin.Holds) == 0 {
		fmt.Println("the hold history: no hold has ended")
	}
	for i, h := range f.Admin.Holds {
		fmt.Printf("the hold history %d: from %s to %s\n", i, time.Unix(h.Start, 0).UTC().Format(time.RFC3339), time.Unix(h.End, 0).UTC().Format(time.RFC3339))
	}
	fmt.Println("a hold defers GovDAO's decisions until it ends, a replacement of guardians to the hold's 21st day at the earliest (§6.5); there is no recovery path: what a hold cannot cure is a new deployment (§1).")
	fmt.Println("a matured decision is executed by anyone (§6.5): hlrecover execute -number N -payer KEY.json, from any funded key, when the operator's relayer is not doing it.")
}

func flagText(f *sol.Flag) string {
	if f == nil {
		return "none"
	}
	return "since " + time.Unix(f.Time, 0).UTC().Format(time.RFC3339)
}

// mailboxKind says whether a kind is one r/hlgov dispatches (§13: the mailbox kinds; 4, 10 and 11 are reserved,
// recorded and spent).
func mailboxKind(kind byte) bool {
	switch kind {
	case sol.KindCancel, sol.KindUnpause, sol.KindSetTokenISM, sol.KindSetGuardians, sol.KindEnrollRouter, sol.KindSetFee, sol.KindUpgrade, sol.KindTransfer, sol.KindSetMintLimits:
		return true
	}
	return false
}

// buildBody writes a decision body (sequence 0) from the kind's flags, or raw parameters.
func buildBody(kind byte, cancel string, salt uint64, object byte, seats, raw string, token byte, maxPer, dayCap uint64) ([]byte, error) {
	if !mailboxKind(kind) {
		return nil, errors.New("-kind is a mailbox kind of §13 (1, 2, 3, 5, 6, 7, 8, 9, 12); 4, 10 and 11 are reserved — recorded and spent")
	}
	if raw != "" {
		p, err := hex.DecodeString(raw)
		if err != nil {
			return nil, err
		}
		if _, err := sol.ParseParams(kind, p); err != nil {
			return nil, fmt.Errorf("the parameters do not parse for kind %d: %w", kind, err)
		}
		return sol.DecisionBody(kind, 0, p), nil
	}
	p := &sol.Params{Kind: kind}
	switch kind {
	case sol.KindCancel:
		if cancel == "" {
			return nil, errors.New("-cancel names a list (0/5,0/7) or a range (0/5-9)")
		}
		if salt == 0 {
			salt = uint64(time.Now().UnixNano())
		}
		p.Salt = salt
		for _, item := range strings.Split(cancel, ",") {
			ch, rest, ok := strings.Cut(strings.TrimSpace(item), "/")
			if !ok {
				return nil, fmt.Errorf("%q is not channel/number", item)
			}
			chn, err := strconv.ParseUint(ch, 10, 8)
			if err != nil || chn != 0 {
				return nil, fmt.Errorf("%q: the channel is 0 (the mailbox; there is no other)", item)
			}
			from, to, isRange := strings.Cut(rest, "-")
			a, err := strconv.ParseUint(from, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%q: %w", item, err)
			}
			b := a
			if isRange {
				if b, err = strconv.ParseUint(to, 10, 64); err != nil {
					return nil, fmt.Errorf("%q: %w", item, err)
				}
			}
			p.Cancel = append(p.Cancel, sol.CancelRange{Channel: byte(chn), From: a, To: b})
		}
	case sol.KindUnpause:
		p.Object = object
	case sol.KindSetGuardians:
		for _, item := range strings.Split(seats, ",") {
			s, k, ok := strings.Cut(strings.TrimSpace(item), "=")
			if !ok {
				return nil, fmt.Errorf("%q is not seat=KEY", item)
			}
			n, err := strconv.ParseUint(s, 10, 8)
			if err != nil {
				return nil, err
			}
			key, err := solana.PublicKeyFromBase58(k)
			if err != nil {
				return nil, err
			}
			p.Seats = append(p.Seats, sol.SeatChange{Seat: byte(n), Key: key})
		}
	case sol.KindSetMintLimits:
		if token > 1 || maxPer == 0 || dayCap < maxPer {
			return nil, errors.New("kind 12 needs -token 0|1, -max at least 1 and -day-cap at least the maximum (base units)")
		}
		p.Token, p.Max, p.DayCap = token, maxPer, dayCap
	default:
		return nil, fmt.Errorf("kind %d takes -params HEX (its parameters are not built from flags here)", kind)
	}
	return sol.DecisionBody(kind, 0, sol.EncodeParams(p)), nil
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
