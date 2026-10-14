package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/solana/bridge/sol"
)

// The audit (PLAN.md §5.8): every authority and setting, read from the chain with every RPC call printed
// before its answer so a member can repeat each by hand, and the verdict: no human key holds an authority.

type audit struct {
	ctx        context.Context
	rpcURL     string
	programs   sol.Programs
	soDir      string
	gnoRPC     string
	mailboxPkg string
	hlgov      string // r/hlgov's pkgpath (or its g1… address): the admin program's compiled sender (G20), its counter (K15)
	deployer   string
	// expectedLock are the locks the launch notice expects (base units, 0 GNOT, 1 BUBBLE), when -expected-lock-* is
	// given: the day cap is printed beside the lock it was compiled from (F10, AP6), and the per-message maximum
	// beside both (AR4); launchMaximum the realms' compiled maximum when -launch-maximum names it (0: their
	// source's 25,000).
	expectedLock  [2]uint64
	launchMaximum uint64
	quiet         bool               // the setup's own run: the calls are not printed, the lines are
	commitment    rpc.CommitmentType // finalized for a holder's audit; confirmed right after the setup step, whose last transactions are seconds old
	rpc           *rpc.Client
	fails         []string
}

// printingTransport prints every JSON-RPC request before sending it.
type printingTransport struct{ quiet bool }

func (t printingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil && !t.quiet {
		b, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(b))
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			params, _ := json.Marshal(m["params"])
			fmt.Println(curlLine(fmt.Sprint(m["method"]), params))
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

// curlLine is one RPC call as a member repeats it by hand: the provider stands as $RPC, never printed (G25: a
// paid provider's key rides in its URL, and an audit log is posted to the members' channel to show the PASS).
func curlLine(method string, params []byte) string {
	return fmt.Sprintf("  rpc> curl -s \"$RPC\" -H 'content-type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":%q,\"params\":%s}'", method, params)
}

// auditHeader names the provider by its scheme and host alone (G25).
func auditHeader(rpcURL string, at time.Time) string {
	return fmt.Sprintf("hlsol audit of %s at %s (the rpc> lines take RPC=<this provider's URL>, whose key is never printed)", agent.Redact(rpcURL), at.UTC().Format(time.RFC3339))
}

func (a *audit) line(ok bool, format string, args ...any) {
	mark := "ok  "
	if !ok {
		mark = "FAIL"
		a.fails = append(a.fails, fmt.Sprintf(format, args...))
	}
	fmt.Printf("%s %s\n", mark, fmt.Sprintf(format, args...))
}

func (a *audit) note(format string, args ...any) {
	fmt.Printf("     %s\n", fmt.Sprintf(format, args...))
}

func (a *audit) data(k solana.PublicKey) ([]byte, *rpc.Account, error) {
	info, err := a.rpc.GetAccountInfoWithOpts(a.ctx, k, &rpc.GetAccountInfoOpts{Commitment: a.commitment})
	if err != nil {
		return nil, nil, err
	}
	if info.Value == nil {
		return nil, nil, fmt.Errorf("no account %s", k)
	}
	return info.Value.Data.GetBinary(), info.Value, nil
}

func keyOr(k *solana.PublicKey) string {
	if k == nil {
		return "none"
	}
	return k.String()
}

func (a *audit) run() (bool, error) {
	a.rpc = rpc.NewWithCustomRPCClient(jsonrpc.NewClientWithOpts(a.rpcURL, &jsonrpc.RPCClientOpts{HTTPClient: &http.Client{Transport: printingTransport{quiet: a.quiet}, Timeout: 30 * time.Second}}))
	p := a.programs
	pda := p.AdminProgram().Authority()
	fmt.Println(auditHeader(a.rpcURL, time.Now()))
	fmt.Printf("  programs: mailbox %s, GNOT %s, BUBBLE %s, client %s, pausable ISM %s, admin program %s\n", p.Mailbox, p.Tokens[0], p.Tokens[1], p.Client, p.Pausable, p.Admin)
	fmt.Printf("  the admin program's authority PDA: %s; its state PDA: %s; the client's state PDA (one, fixed seed): %s; the pausable ISM's flags PDA: %s\n", pda, p.AdminProgram().State(), p.LightClient().State(), p.PausableISM().Flags())
	fmt.Printf("  the mailbox's process authority for the admin program: %s\n", p.MailboxProgram().ProcessAuthority(p.Admin))

	// 1. the six upgrade authorities and the ELF hashes
	fmt.Println("\n# upgrade authorities (five = the admin program, its own = none)")
	for _, x := range []struct {
		name string
		id   solana.PublicKey
		so   string
	}{{"mailbox", p.Mailbox, "mailbox.so"}, {"GNOT", p.Tokens[0], "token.so"}, {"BUBBLE", p.Tokens[1], "token.so"}, {"client", p.Client, "gno_light_client_ism.so"}, {"pausable ISM", p.Pausable, "pausable_ism.so"}, {"admin program", p.Admin, "admin_program.so"}} {
		pd, _, err := a.data(sol.ProgramData(x.id))
		if err != nil {
			a.line(false, "%s programdata: %v", x.name, err)
			continue
		}
		slot, auth, elf, err := sol.ParseProgramData(pd)
		if err != nil {
			a.line(false, "%s programdata: %v", x.name, err)
			continue
		}
		want := pda.String()
		if x.name == "admin program" {
			// its own authority is none; its ELF is hashed and its bindings checked like the others' (G20: the
			// admin program's hash line was skipped here before)
			a.line(auth == nil, "the admin program's upgrade authority, its own = %s (want none: burned last, which finish_setup requires); deployed at slot %d", keyOr(auth), slot)
		} else {
			a.line(keyOr(auth) == want, "%s upgrade authority: %s (want the admin program %s); deployed at slot %d", x.name, keyOr(auth), want, slot)
		}
		end := sol.ELFEnd(elf)
		sum := sha256.Sum256(elf[:end])
		a.bindings(x.name, elf[:end])
		if a.soDir != "" {
			if f, err := os.ReadFile(filepath.Join(a.soDir, x.so)); err == nil {
				fsum := sha256.Sum256(f)
				a.line(fsum == sum, "%s ELF sha256 %s (%d bytes; the file %s: %s)", x.name, hex.EncodeToString(sum[:]), end, x.so, hex.EncodeToString(fsum[:]))
				continue
			}
		}
		a.note("%s ELF sha256 %s (%d bytes)", x.name, hex.EncodeToString(sum[:]), end)
	}

	// 2. the mailbox
	fmt.Println("\n# the mailbox")
	m := p.MailboxProgram()
	if d, _, err := a.data(m.Outbox()); err != nil {
		a.line(false, "outbox: %v", err)
	} else if ob, err := sol.ParseOutbox(d); err != nil {
		a.line(false, "outbox: %v", err)
	} else {
		a.line(ob.Owner != nil && ob.Owner.Equals(pda), "mailbox owner: %s (want the admin program)", keyOr(ob.Owner))
		a.line(ob.MaxProtocolFee == sol.MaxProtocolFee, "mailbox max_protocol_fee: %d lamports (want %d, 0.05 SOL)", ob.MaxProtocolFee, sol.MaxProtocolFee)
		a.note("mailbox local domain %d, protocol fee %d lamports, beneficiary %s (the treasurer's key), %d messages dispatched, root %x", ob.LocalDomain, ob.Fee, ob.Beneficiary, ob.Count, ob.Root[:8])
	}
	if d, _, err := a.data(m.Inbox()); err != nil {
		a.line(false, "inbox: %v", err)
	} else if in, err := sol.ParseInbox(d); err != nil {
		a.line(false, "inbox: %v", err)
	} else {
		a.line(in.DefaultISM.Equals(p.Pausable), "mailbox default ISM: %s (want the pausable ISM)", in.DefaultISM)
		a.note("inbox local domain %d, %d messages processed", in.LocalDomain, in.ProcessedCount)
	}

	// 3. the tokens
	for i, name := range []string{"GNOT", "BUBBLE"} {
		fmt.Printf("\n# the %s token (%s)\n", name, p.Tokens[i])
		t := sol.Synthetic{Program: p.Tokens[i]}
		d, _, err := a.data(t.Token())
		if err != nil {
			a.line(false, "%s token account: %v", name, err)
			continue
		}
		tok, err := sol.ParseToken(d)
		if err != nil {
			a.line(false, "%s token account: %v", name, err)
			continue
		}
		a.line(tok.Owner != nil && tok.Owner.Equals(pda), "%s owner: %s (want the admin program)", name, keyOr(tok.Owner))
		a.line(tok.ISM != nil && tok.ISM.Equals(p.Pausable), "%s ISM pointer: %s (want the pausable ISM)", name, keyOr(tok.ISM))
		a.line(tok.Mailbox.Equals(p.Mailbox), "%s mailbox pointer: %s", name, tok.Mailbox)
		a.line(tok.Decimals == 6 && tok.RemoteDecimals == 6, "%s decimals %d, remote %d (want 6 and 6)", name, tok.Decimals, tok.RemoteDecimals)
		a.line(tok.IGP == nil, "%s interchain gas paymaster: %s (want none)", name, keyOr(tok.IGP))
		a.line(len(tok.DestinationGas) == 0, "%s destination gas configs: %d (want none)", name, len(tok.DestinationGas))
		a.line(tok.FeeConfig == nil, "%s fee config: %v (want none)", name, tok.FeeConfig != nil)
		for _, dom := range sol.SortedDomains(tok.RemoteRouters) {
			r := tok.RemoteRouters[dom]
			a.note("%s remote router for domain %d: %x (the route realm's address %s)", name, dom, r, gnoAddr(r))
		}
		if len(tok.RemoteRouters) == 0 {
			a.line(false, "%s has no remote router enrolled", name)
		}
		md, _, err := a.data(t.Mint())
		if err != nil {
			a.line(false, "%s mint: %v", name, err)
			continue
		}
		m2, err := sol.ParseMint2022(md)
		if err != nil {
			a.line(false, "%s mint: %v", name, err)
			continue
		}
		a.line(m2.MintAuthority != nil && m2.MintAuthority.Equals(t.Mint()), "%s mint authority: %s (want its mint PDA %s)", name, keyOr(m2.MintAuthority), t.Mint())
		a.line(m2.FreezeAuthority == nil, "%s freeze authority: %s (want none)", name, keyOr(m2.FreezeAuthority))
		a.line(m2.HasPointer && m2.PointerAuthority == nil, "%s metadata pointer authority: %s (want none; pointer to %s)", name, keyOr(m2.PointerAuthority), keyOr(m2.PointerAddress))
		a.line(m2.HasMetadata && m2.UpdateAuthority == nil, "%s metadata update authority: %s (want none once name, symbol and URI are final: %q %q %q)", name, keyOr(m2.UpdateAuthority), m2.Name, m2.Symbol, m2.URI)
		a.note("%s supply %d (decimals %d)", name, m2.Supply, m2.Decimals)
		if bal, err := a.rpc.GetBalance(a.ctx, t.ATAPayer(), a.commitment); err == nil {
			a.note("%s ATA payer %s holds %.4f SOL", name, t.ATAPayer(), float64(bal.Value)/1e9)
		}
	}

	// 4. the pausable ISM
	fmt.Println("\n# the pausable ISM")
	pz := p.PausableISM()
	var pausableKeys [sol.Keys]solana.PublicKey
	var pausableFactor byte
	now := time.Now().Unix()
	if d, _, err := a.data(pz.State()); err != nil {
		a.line(false, "pausable ISM state: %v", err)
	} else if ps, err := sol.ParsePausableState(d); err != nil {
		a.line(false, "pausable ISM state: %v", err)
	} else {
		pausableFactor = ps.ClockFactor
		a.line(ps.Owner.Equals(pda), "pausable ISM owner: %s (want the admin program)", ps.Owner)
		a.line(ps.Target.Equals(p.Client), "pausable ISM target: %s, layout version %d (want the client, version %d)", ps.Target, ps.TargetVersion, sol.ClientStateVersion)
		a.note("pausable ISM clock factor %d", ps.ClockFactor)
		day := sol.UnpauseDelay(max(int64(ps.ClockFactor), 1))
		for i, l := range ps.Limits {
			a.note("pausable ISM mint limits for %s (token %d): %d a message, %d a day (base units; kind 12 moves them); the day's bucket holds %d now (charged %d at %s)", []string{"GNOT", "BUBBLE"}[i], i, l.MaxPerMessage, l.DayCap, l.Level(now, day), l.Used, time.Unix(l.UsedAt, 0).UTC().Format(time.RFC3339))
			if lock := a.expectedLock[i]; lock > 0 {
				// the cap beside the lock it was compiled from (F10: the smaller of 100,000 and a tenth of the expected
				// lock; the machines alarm mint:cap-ratio against the lock on gno.land as it stands, AP6), and the
				// per-message maximum beside both (AR4: the smaller of 25,000 and the day cap, never below the realms')
				// a line, not a note (G51): the day cap must be what the expected locks compile — the smaller of
				// 100,000 and a tenth of the smaller lock, one figure for both tokens
				a.line(l.DayCap == launchDayCap(a.expectedLock), "pausable ISM day cap for %s against the expected lock %d it was compiled from: %d, %.1f%% of it (a tenth is %d; §5.4 bounds a forged day's mint before the flags by a tenth of the lock as it stands; want %d, the smaller of %d and a tenth of the smaller expected lock)", []string{"GNOT", "BUBBLE"}[i], lock, l.DayCap, 100*float64(l.DayCap)/float64(lock), lock/10, launchDayCap(a.expectedLock), uint64(launchDayCapMax))
				realms := a.launchMaximum
				if realms == 0 {
					realms = launchMaximumDefault
				}
				a.line(l.MaxPerMessage == launchMaxPerMessage(l.DayCap) && l.MaxPerMessage >= realms, "pausable ISM per-message maximum for %s beside the expected lock %d and the day cap %d: %d — the smaller of %d and the day cap (AR4), at or above the realms' compiled maximum %d (§5.4: the ISM's maximum is never below gno.land's; a send between them would park for ever)", []string{"GNOT", "BUBBLE"}[i], lock, l.DayCap, l.MaxPerMessage, uint64(launchMaximumDefault), realms)
			}
		}
	}
	if d, _, err := a.data(pz.Flags()); err != nil {
		a.line(false, "pausable ISM flags: %v", err)
	} else if g, err := sol.ParsePausableFlags(d); err != nil {
		a.line(false, "pausable ISM flags: %v", err)
	} else {
		pausableKeys = g.Keys
		a.printGuardians("pausable ISM", g)
	}

	// 5. the client
	fmt.Println("\n# the light client")
	cl := p.LightClient()
	var clientSetHash, clientTrustedHash [32]byte
	var clientTrusted uint64
	var clientHead *sol.ClientHead
	var clientBody *sol.ClientBody
	if d, _, err := a.data(cl.State()); err != nil {
		a.line(false, "client state: %v", err)
	} else if head, body, ring, err := sol.ParseClientState(d); err != nil {
		a.line(false, "client state: %v", err)
	} else {
		clientSetHash, clientTrusted, clientTrustedHash = body.SetHash, head.TrustedHeight, head.TrustedHash
		clientHead, clientBody = head, body
		a.line(body.Admin.Equals(pda), "client admin role (SetAdmin; no reset exists): %s (want the admin program)", body.Admin)
		a.line(head.Version == sol.ClientStateVersion, "client state layout version %d (want %d: fixed for the deployment's life, kind 4 reserved; the pausable ISM pins it at Verify)", head.Version, sol.ClientStateVersion)
		a.note("client chain %q, origin domain %d, trusted height %d hash %x, set of %d (hash %x) from height %d, next set announced %v, %d records (running hash %x), ring %d of %d, quarantine end %d, last accepted %s, clock factor %d",
			body.ChainID, body.OriginDomain, head.TrustedHeight, head.TrustedHash[:8], len(body.Set), body.SetHash[:8], body.CurrentSetFrom(), body.NextSet != nil, body.Records, body.RunningHash[:8], len(ring), sol.RingCapacity(head, len(d)), head.QuarantineEnd, time.Unix(head.LastAcceptedTime, 0).UTC().Format(time.RFC3339), head.ClockFactor)
		a.line(sol.RingCapacity(head, len(d)) >= sol.Ring, "client ring capacity %d (want %d)", sol.RingCapacity(head, len(d)), sol.Ring)
		for i, v := range body.Set {
			a.note("client validator %d: %x power %d", i, v.PubKey, v.Power)
		}
		// the set against the client's measured bound (G19/AO4): the heap frame is due before gno.land nears it
		a.line(len(body.Set) < sol.SetSizeWarn && len(body.NextSet) < sol.SetSizeWarn, "client validator set of %d (next set announced: %d) against the measured bound of about %d through any history (§6.1; the machines alarm client:set-bound from %d: the heap frame is due before it)", len(body.Set), len(body.NextSet), sol.SetSizeBound, sol.SetSizeWarn)
	}

	// 6. the admin program
	fmt.Println("\n# the admin program")
	ad := p.AdminProgram()
	var adminKeys [sol.Keys]solana.PublicKey
	var adminLast uint64
	if d, _, err := a.data(ad.State()); err != nil {
		a.line(false, "admin state: %v", err)
	} else if st, err := sol.ParseAdminState(d); err != nil {
		a.line(false, "admin state: %v", err)
	} else {
		adminKeys = st.Guardians.Keys
		a.line(st.SetupDone == 1, "admin program setup_done: %d (want 1)", st.SetupDone)
		a.note("admin program origin domain %d, last sequence number %d, clock factor %d, successor %s, %d cancelled ranges stored", st.OriginDomain, st.LastSeq, st.ClockFactor, keyOr(st.Successor), len(st.Cancelled))
		adminLast = st.LastSeq
		a.note("admin program pending decision accounts: %d (the stored cancelled ranges are pruned at zero)", st.Pending)
		for _, r := range st.Cancelled {
			a.note("admin program cancelled range: numbers %d-%d (dead; CloseCancelled closes any pending account among them)", r.From, r.To)
		}
		if st.Predecessor.IsZero() {
			a.note("admin program predecessor: none (the launch deployment)")
		} else {
			a.note("admin program predecessor %s, inherited %d (a successor takes no decision before its predecessor's kind 9 wrote the live numbers, seats, record and hold history into it)", st.Predecessor, st.Inherited)
		}
		a.printGuardians("admin program", &st.Guardians)
		factor := int64(st.ClockFactor)
		if factor == 0 {
			factor = 1
		}
		hold := st.Guardians.Hold(now, sol.UnpauseDelay(factor))
		// the three programs must agree (S-06): the clock factors, the origin domain, the guardian lists
		if clientHead != nil {
			a.line(clientHead.ClockFactor == st.ClockFactor && pausableFactor == st.ClockFactor, "the three programs' clock factors agree: client %d, pausable ISM %d, admin program %d (§8.3 wants 1 on mainnet; a devnet build is 8)", clientHead.ClockFactor, pausableFactor, st.ClockFactor)
			a.line(clientBody.OriginDomain == st.OriginDomain, "the client's and the admin program's origin domains agree: %d and %d", clientBody.OriginDomain, st.OriginDomain)
		} else {
			a.line(false, "the clock factors and origin domains cannot be compared: the client's state was not read")
		}
		if len(st.Holds) == 0 {
			a.note("admin program hold history: no hold has ended")
		}
		for i, h := range st.Holds {
			a.note("admin program hold history %d: a hold from %s to %s (a replacement's floor under a hold is day %d of the earliest hold overlapping its pending life)", i, time.Unix(h.Start, 0).UTC().Format(time.RFC3339), time.Unix(h.End, 0).UTC().Format(time.RFC3339), sol.HoldReplacementDays)
		}
		from := uint64(1)
		if st.LastSeq > 200 {
			from = st.LastSeq - 199
		}
		for n := from; n <= st.LastSeq; n++ {
			d, _, err := a.data(ad.Decision(n))
			if err != nil {
				continue
			}
			dec, err := sol.ParseDecision(d)
			if err != nil {
				a.line(false, "pending decision #%d: %v", n, err)
				continue
			}
			floor := ""
			if hold != nil && dec.HeldToTheDay() {
				floor = fmt.Sprintf(", floor %s (day %d of the earliest hold overlapping its pending life, from the hold history)", time.Unix(dec.FloorUnder(hold, st, factor), 0).UTC().Format(time.RFC3339), sol.HoldReplacementDays)
			}
			a.note("pending decision #%d kind %d (%s) params %x, delivered %s by %s, matures at %s%s", dec.Number, dec.Kind, sol.DescribeParams(dec.Kind, dec.Params), dec.Params, time.Unix(dec.DeliveredTime, 0).UTC().Format(time.RFC3339), dec.Deliverer, time.Unix(dec.MaturesAt(factor, hold, st), 0).UTC().Format(time.RFC3339), floor)
		}
		same := pausableKeys == adminKeys
		a.line(same, "the pausable ISM's and the admin program's seat lists are the same: %v", same)
	}
	if bal, err := a.rpc.GetBalance(a.ctx, pda, a.commitment); err == nil {
		a.note("the authority PDA holds %.4f SOL (the realloc float, M2-NOTES 17b)", float64(bal.Value)/1e9)
	}
	if a.deployer != "" {
		if k, err := solana.PublicKeyFromBase58(a.deployer); err == nil {
			if bal, err := a.rpc.GetBalance(a.ctx, k, a.commitment); err == nil {
				a.note("the deployer %s holds %.4f SOL", k, float64(bal.Value)/1e9)
			}
		}
	}

	// 7. gno.land: the validators against the client's set, hlmailbox's seats against the two lists, r/hlgov's
	// counter against the admin program's
	if a.gnoRPC != "" {
		fmt.Println("\n# gno.land (" + a.gnoRPC + ")")
		g := &gno.Client{RPC: a.gnoRPC}
		if clientTrusted > 0 {
			if set, err := g.ValidatorsAt(a.ctx, int64(clientTrusted)); err != nil {
				a.line(false, "/validators?height=%d: %v", clientTrusted, err)
			} else {
				sh, _ := set.Hash()
				a.line(sh == clientSetHash, "the client's set hash against /validators?height=%d: %x vs %x", clientTrusted, clientSetHash[:8], sh[:8])
				a.note("gno.land's validator set at height %d: %d validators (the client's measured bound is about %d; the machines alarm from %d)", clientTrusted, len(set.Validators), sol.SetSizeBound, sol.SetSizeWarn)
			}
			if blk, err := g.BlockAt(a.ctx, int64(clientTrusted)); err != nil {
				a.line(false, "/block?height=%d: %v", clientTrusted, err)
			} else {
				a.line(blk.Hash == clientTrustedHash, "the client's trusted hash against /block?height=%d: %x vs the chain's %x (the full hashes; a client started from a forked or forged header fails here)", clientTrusted, clientTrustedHash, blk.Hash)
				if age := time.Since(time.Unix(blk.Header.Time.Secs, 0)); age > time.Hour {
					a.note("the trusted header is %s old (the client's own clock of headers is the updater's; §6.1 wanted the deploy's trusted header less than an hour old)", age.Round(time.Minute))
				}
			}
		}
		if a.mailboxPkg != "" {
			var j struct {
				Seats  []string `json:"seats"`
				Clocks struct {
					Factor   int64 `json:"factor"`
					Delay    int64 `json:"delay"`
					Deadline int64 `json:"deadline"`
				} `json:"clocks"`
			}
			if err := g.RenderJSON(a.ctx, a.mailboxPkg, "json", &j); err != nil {
				if realmAbsent(err) {
					// AN10's order: the Solana side passes its audit before the realms go up, so the realm is not there yet
					a.note("hlmailbox (%s) is not on the chain yet: the realms go up after the Solana side passes this audit (§5.8's order); its seats and clocks are read once they are", a.mailboxPkg)
				} else {
					a.line(false, "hlmailbox's seats: %v", err)
				}
			} else {
				a.note("hlmailbox's seats (gno keys; the three lists are the same people, §8.3): %s", strings.Join(j.Seats, ", "))
				a.note("the realms' clocks: factor %d (§8.3 wants 1 on mainnet; a devnet build is 8), the 7-day delay %d s, the 14-day deadline %d s; the meters are rates per real day, never divided", j.Clocks.Factor, j.Clocks.Delay, j.Clocks.Deadline)
			}
		}
		// r/hlgov's counter against the admin program's last number (K15, §5.3: both counters and the gap)
		if strings.HasPrefix(a.hlgov, "gno.land/") {
			var gov struct {
				Last uint64 `json:"last"`
				Next uint64 `json:"next"`
			}
			if err := g.RenderJSON(a.ctx, a.hlgov, "json", &gov); err != nil {
				if realmAbsent(err) {
					a.note("r/hlgov (%s) is not on the chain yet: its counter is read once it is", a.hlgov)
				} else {
					a.line(false, "r/hlgov's counter: %v", err)
				}
			} else {
				ok, text := counterLine(adminLast, gov.Last, gov.Next)
				a.line(ok, "%s", text)
			}
		}
	}

	fmt.Println()
	ok := len(a.fails) == 0
	if ok {
		fmt.Println("hlsol audit: PASS — every authority is the admin program's or none; no human key holds one")
	} else {
		fmt.Printf("hlsol audit: FAIL — %d lines:\n", len(a.fails))
		for _, f := range a.fails {
			fmt.Println("  - " + f)
		}
	}
	return ok, nil
}

// counterLine is r/hlgov's counter against the admin program's last number (K15, §5.3: both counters and the
// gap) — a FAIL on any gap, each direction named. The admin program's number below r/hlgov's is AT3's: a fresh
// deployment whose init started below r/hlgov's counter (hlsol setup -start-sequence), which refuses r/hlgov's
// next decision OutOfOrder for ever since the counter never goes down — or GovDAO's decisions dispatched and not
// yet delivered. Above r/hlgov's: a foreign decision took a number, or GovDAO's was lost (sync sequence).
func counterLine(adminLast, govLast, govNext uint64) (bool, string) {
	head := fmt.Sprintf("r/hlgov's counter: last %d (the next takes %d) against the admin program's last sequence number %d: gap %d", govLast, govNext, adminLast, int64(adminLast)-int64(govLast))
	switch {
	case adminLast < govLast:
		return false, head + fmt.Sprintf(" — THE ADMIN PROGRAM'S NUMBER IS BELOW r/hlgov's: a fresh deployment started below r/hlgov's counter (hlsol setup -start-sequence must be r/hlgov's Last(), %d, at the init: the counter never goes down, so r/hlgov's next decision #%d is refused OutOfOrder for ever, AT3), or r/hlgov's decisions %d–%d are dispatched and not yet delivered (deliver them, then read again)", govLast, govNext, adminLast+1, govLast)
	case adminLast > govLast:
		return false, head + " (a foreign decision took a number, or GovDAO's was lost: sync sequence)"
	}
	return true, head + " (the two counters agree)"
}

// bindings checks the compiled constants on a deployed ELF (G20): the pausable ISM's MAILBOX and TOKENS, the
// client's MAILBOX_PKG_PATH, the admin program's MAILBOX, TOKENS and HLGOV_SENDER — the ids the audit's own
// flags name, found contiguous in the ELF or as the compiler's lddw immediates (sol.ELFBindsKey). A build from
// another config.rs passes every other line; this one fails it.
func (a *audit) bindings(name string, elf []byte) {
	p := a.programs
	bind := func(what string, key solana.PublicKey) {
		a.line(sol.ELFBindsKey(elf, key), "%s ELF binds %s %s", name, what, key)
	}
	switch name {
	case "pausable ISM":
		bind("the mailbox", p.Mailbox)
		bind("the GNOT token", p.Tokens[0])
		bind("the BUBBLE token", p.Tokens[1])
	case "client":
		if a.mailboxPkg == "" {
			a.note("client ELF: no -mailbox-pkgpath to check MAILBOX_PKG_PATH against")
		} else {
			a.line(sol.ELFBindsText(elf, a.mailboxPkg), "client ELF binds the mailbox pkgpath %q (MAILBOX_PKG_PATH)", a.mailboxPkg)
		}
	case "admin program":
		bind("the mailbox", p.Mailbox)
		bind("the GNOT token", p.Tokens[0])
		bind("the BUBBLE token", p.Tokens[1])
		if a.hlgov == "" {
			a.note("admin program ELF: no -hlgov to check HLGOV_SENDER against")
		} else {
			k := gnoKey32(a.hlgov)
			a.line(sol.ELFBindsKey(elf, solana.PublicKeyFromBytes(k[:])), "admin program ELF binds r/hlgov's address %s (HLGOV_SENDER %x)", gnoAddr(k), k[12:])
		}
	}
}

func (a *audit) printGuardians(name string, g *sol.Guardians) {
	for i, k := range g.Keys {
		kind := "guardian seat"
		if i >= sol.Seats {
			kind = "watch slot"
		}
		flag := "no flag"
		if g.Flags[i] != nil {
			flag = fmt.Sprintf("FLAG set at slot %d (%s)", g.Flags[i].Slot, time.Unix(g.Flags[i].Time, 0).UTC().Format(time.RFC3339))
		}
		a.note("%s %s %d: %s — %s", name, kind, i+1, k, flag)
	}
	if g.HoldSince != nil {
		a.note("%s holdSince: slot %d (%s); dip since: %v", name, g.HoldSince.Slot, time.Unix(g.HoldSince.Time, 0).UTC().Format(time.RFC3339), g.DipSince != nil)
	} else {
		a.note("%s: no hold", name)
	}
}

// gnoAddr is a 32-byte router as a g1… address (the last twenty bytes; the first twelve must be zero).
func gnoAddr(r [32]byte) string {
	a, err := hl.Bytes32ToGnoAddress(r)
	if err != nil {
		return "(not a gno address)"
	}
	return a
}
