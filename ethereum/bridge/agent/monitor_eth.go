package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gno"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The monitor's Ethereum side (eth/bridge/PLAN.md §5.9, §6.2, §6.3, §6.5): the client's header record rebuilt from its
// HeaderRecorded events and held against the own node's headers (the fork rule: the cut is the Ethereum block of the
// first header that differs, §6.4) and against the client's own counter and running hash (a gap); every staged tally
// (StageProgress) held against gno.land's headers — a stage for a header gno.land does not have alarms (§6.3: a thief
// holding a leaked set pre-staging forgeries); every decision HlAdmin recorded held against r/hlgov's (a foreign
// decision); every proxy a kind 8 upgraded, its EIP-1967 slots read themselves and held to that decision and the
// ProxyAdmin (E3-RT8, E3-AS7); the watch key's flags on the gate and HlAdmin, set again only when found cleared or
// when an unpause recorded at a final block would clear them (§5.5); sends on gno.land while deliveries are paused;
// the day caps against the locks. Every reading here goes through both providers, pinned to the snapshot's finalized
// block.

// recordEntry is one header the client recorded (accepted, or proved).
type recordEntry struct {
	Record uint64 `json:"record"`
	Hash   string `json:"hash"`
	Height int64  `json:"height"`
	Block  uint64 `json:"block"` // the Ethereum block it was recorded in: §6.4's cut for the first that differs
	Proven bool   `json:"proven"`
	Tx     string `json:"tx"`
}

// recordMismatch is the first header of the record that differs from gno.land's.
type recordMismatch struct {
	Height int64  `json:"height"`
	Client string `json:"client"` // the header hash the client recorded
	Chain  string `json:"chain"`  // the own node's block hash at the height
	Second string `json:"second"` // the second source's ("" when none answered)
	Block  uint64 `json:"block"`  // the cut: the Ethereum block the differing header was recorded in
	Tx     string `json:"tx"`
}

func (mm *recordMismatch) String() string {
	return fmt.Sprintf("the client recorded %s at height %d (Ethereum block %d, tx %s); this node's block there is %s, the second source's %s — the cut is block %d (§6.4)",
		short(mm.Client), mm.Height, mm.Block, mm.Tx, short(mm.Chain), orNone(short(mm.Second)), mm.Block)
}

// headerRecord is the record rebuilt from the client's events, and the scan's place.
type headerRecord struct {
	Cursor    uint64          `json:"cursor"` // the last block scanned
	Count     uint64          `json:"count"`
	Hash      string          `json:"hash"` // the running hash after the last entry
	Recent    []recordEntry   `json:"recent"`
	Unchecked []recordEntry   `json:"unchecked,omitempty"` // entries above the own node's head when last read
	Checked   uint64          `json:"checked"`
	Mismatch  *recordMismatch `json:"mismatch,omitempty"`
	Gap       string          `json:"gap,omitempty"`
}

const recordRecent = 32

// recordRunning is the client's running hash after one more header: keccak256(previous ‖ hash ‖ height u64 BE ‖
// block.number u64 BE) (GnoLightClient._appendRecord).
func recordRunning(prev, hash [32]byte, height int64, block uint64) [32]byte {
	var h, b [8]byte
	binary.BigEndian.PutUint64(h[:], uint64(height))
	binary.BigEndian.PutUint64(b[:], block)
	return crypto.Keccak256Hash(prev[:], hash[:], h[:], b[:])
}

// fold adds one HeaderRecorded event: its record number must be the next and its running hash the one the previous
// gives (else the record has a gap: an event missing, or a hash that does not follow).
func (r *headerRecord) fold(ev *ethx.HeaderRecorded) {
	var prev [32]byte
	if r.Hash != "" {
		b, _ := hex.DecodeString(r.Hash)
		copy(prev[:], b)
	}
	want := recordRunning(prev, ev.Hash, ev.Height, ev.Block)
	switch {
	case ev.Record != r.Count+1 && r.Gap == "":
		r.Gap = fmt.Sprintf("the client's record %d (block %d) follows record %d: an entry is missing", ev.Record, ev.Block, r.Count)
	case want != ev.RunningHash && r.Gap == "":
		r.Gap = fmt.Sprintf("the client's record %d (block %d) carries running hash %s, the record rebuilt gives %s", ev.Record, ev.Block, short(hexOf(ev.RunningHash)), short(hexOf(want)))
	}
	r.Count = ev.Record
	r.Hash = hexOf(ev.RunningHash)
	e := recordEntry{Record: ev.Record, Hash: hexOf(ev.Hash), Height: ev.Height, Block: ev.Block, Proven: ev.Proven, Tx: ev.Tx.Hex()}
	r.Recent = append(r.Recent, e)
	if len(r.Recent) > recordRecent {
		r.Recent = r.Recent[len(r.Recent)-recordRecent:]
	}
	r.Unchecked = append(r.Unchecked, e)
}

// syncRecord folds the client's HeaderRecorded events of the blocks after the cursor up to F (both providers alike). A
// fresh record starts at the scans' first block, eth.deployBlock (finishSetup's: E2-RT6), from the client's counter and
// running hash at the block before it — the client's initialization recorded the trusted header earlier, at step 4 of
// the setup — so the rebuilt record is held to the client's to the unit from there on.
func (m *Monitor) syncRecord(ctx context.Context, client common.Address, F uint64) (bool, error) {
	r := &m.state.Record
	lo := r.Cursor + 1
	if r.Cursor == 0 {
		lo = m.state.From
		if r.Count == 0 && r.Hash == "" && lo > 1 {
			n, h, err := m.recordBefore(ctx, client, lo)
			if err != nil {
				return false, err
			}
			r.Count, r.Hash = n, h
		}
	}
	if lo > F {
		return true, nil
	}
	hi := min(F, lo+burnScanMax-1)
	logs, err := m.pair.Logs(ctx, ethx.LogQuery{Addresses: []common.Address{client}, Topics: [][]common.Hash{{ethx.LightClient.Event("HeaderRecorded")}}}, lo, hi)
	if err != nil {
		return false, err
	}
	for _, l := range logs {
		ev, err := ethx.DecodeHeaderRecorded(l)
		if err != nil {
			return false, fmt.Errorf("a HeaderRecorded log of block %d: %w", l.BlockNumber, err)
		}
		r.fold(ev)
	}
	r.Cursor = hi
	return hi == F, nil
}

// recordBefore is the client's counter and running hash at the block before `from`, both providers agreeing (pinned
// by its canonical hash); (0, "") where the client has no code yet.
func (m *Monitor) recordBefore(ctx context.Context, client common.Address, from uint64) (uint64, string, error) {
	h, err := m.pair.CanonicalHash(ctx, from-1)
	if err != nil {
		return 0, "", fmt.Errorf("the block before the scans' first (%d): %w", from-1, err)
	}
	at := ethx.AtHash(h)
	if code, err := m.pair.A.CodeAt(ctx, client, at); err == nil && len(code) == 0 {
		return 0, "", nil
	}
	cs, err := ethx.ReadClient(ctx, m.pair, client, at)
	if err != nil {
		return 0, "", fmt.Errorf("the client's record at block %d, where the scans start from: %w", from-1, err)
	}
	if cs.Records == 0 {
		return 0, "", nil
	}
	return cs.Records, hexOf(cs.RunningHash), nil
}

// checkRecord compares every entry not yet compared with the own node's block at its height — and, on a difference,
// with the second source's: a mismatch both show (or with no second source) is the fork; the own node alone differing
// is the sources rule's (an alarm here). An entry above the node's head waits.
func (m *Monitor) checkRecord(ctx context.Context, head int64) {
	r := &m.state.Record
	var keep []recordEntry
	unread := 0
	defer func() {
		if unread > 0 {
			m.blind(fmt.Sprintf("%d header(s) the client recorded could not be read from this node to compare (the fork rule): read again next poll", unread))
		}
	}()
	for _, e := range r.Unchecked {
		if e.Height > head {
			keep = append(keep, e)
			continue
		}
		b, err := m.own.Block(ctx, e.Height)
		if err != nil {
			keep = append(keep, e)
			unread++
			continue
		}
		r.Checked++
		if strings.EqualFold(b.Hash, e.Hash) {
			continue
		}
		second := ""
		if len(m.sources) > 0 {
			if sb, err := m.sources[0].Block(ctx, e.Height); err == nil {
				second = sb.Hash
			}
		}
		if second != "" && strings.EqualFold(second, e.Hash) {
			m.Alarm.Alarm("header:own-odd", fmt.Sprintf("the client's header at height %d (%s) is the second source's, not this node's (%s): the own node is the odd one out (the sources rule decides)", e.Height, short(e.Hash), short(b.Hash)))
			continue
		}
		if r.Mismatch == nil {
			r.Mismatch = &recordMismatch{Height: e.Height, Client: e.Hash, Chain: strings.ToLower(b.Hash), Second: strings.ToLower(second), Block: e.Block, Tx: e.Tx}
		}
	}
	r.Unchecked = keep
}

// checkTrusted holds the client's trusted header at F to gno.land (E3-CF2's monitor half, E4-TL3): the trust root is
// recorded at the setup's step 4, before eth.deployBlock where the record's scan starts, so a client nobody updated
// since a root that is not gno.land's block was counted and never compared. The trusted header is read every poll and
// held to this node's block at its height and, on a difference, the second source's: both differing alarm
// header:trusted (never green); the second source alone agreeing is the sources rule's (header:own-odd). An alarm only:
// a header recorded after the root is the record rule's, which flags; `hleth audit` with a gno source FAILs the root.
func (m *Monitor) checkTrusted(ctx context.Context, cs *ethx.ClientState, head int64) {
	const key = "header:trusted"
	if cs.TrustedHeight <= 0 || cs.TrustedHeight > head {
		return // above the head: the ahead rule's (§5.6)
	}
	b, err := m.own.Block(ctx, cs.TrustedHeight)
	if err != nil {
		m.blind(fmt.Sprintf("the client's trusted header at height %d could not be read from this node to compare (%v)", cs.TrustedHeight, err))
		return
	}
	want := hexOf(cs.TrustedHash)
	const oddKey = "header:own-odd:trusted" // its own key, cleared here (checkRecord's header:own-odd is a past entry's)
	if strings.EqualFold(b.Hash, want) {
		m.Alarm.Clear(key)
		m.Alarm.Clear(oddKey)
		return
	}
	second := ""
	if len(m.sources) > 0 {
		if sb, err := m.sources[0].Block(ctx, cs.TrustedHeight); err == nil {
			second = sb.Hash
		}
	}
	if second != "" && strings.EqualFold(second, want) {
		m.Alarm.Clear(key)
		m.Alarm.Alarm(oddKey, fmt.Sprintf("the client's trusted header at height %d (%s) is the second source's, not this node's (%s): the own node is the odd one out (the sources rule decides)", cs.TrustedHeight, short(want), short(b.Hash)))
		return
	}
	m.Alarm.Clear(oddKey)
	m.Alarm.Alarm(key, fmt.Sprintf("the client trusts header %s at height %d, and gno.land's block there is %s (this node) and %s (the second source): a trust root, or a trusted header, that is not gno.land's — the client then accepts nothing an honest set signs past it, and its signers are not gno.land's; hleth audit with a gno source FAILs it (E3-CF2, E4-TL3)", short(want), cs.TrustedHeight, short(b.Hash), orNone(short(second))))
}

// stageSeen is one stage the watch read: the header and the height its StageProgress event carries.
type stageSeen struct {
	Header  string `json:"header"`
	Height  int64  `json:"height"`
	Tx      string `json:"tx"`
	Block   uint64 `json:"block"`
	Verdict string `json:"verdict"` // "ok", "unknown: …", "" while the node has not reached the height
}

// stageWatch is every StageProgress event read, by stage key, and the scan's place.
type stageWatch struct {
	Cursor uint64               `json:"cursor"`
	Seen   map[string]stageSeen `json:"seen"`
}

func (s *stageWatch) init() {
	if s.Seen == nil {
		s.Seen = map[string]stageSeen{}
	}
}

// syncStages reads the StageProgress events after the cursor up to F and judges each stage's header against gno.land
// (§6.3): the event carries the header's hash and, since fold F4, its height, so no call is read and a stage made
// through another contract is judged like any other; a header the own node and the second source both lack at that
// height alarms (stage:unknown) — an alarm only. Every stage carries at least one real signature over its header (a
// call that verifies none reverts NoNewSignature), so the alarm always stands on one.
func (m *Monitor) syncStages(ctx context.Context, client common.Address, F uint64, head int64) error {
	s := &m.state.Stages
	s.init()
	lo := s.Cursor + 1
	if s.Cursor == 0 {
		lo = m.state.From
	}
	if lo <= F {
		hi := min(F, lo+burnScanMax-1)
		logs, err := m.pair.Logs(ctx, ethx.LogQuery{Addresses: []common.Address{client}, Topics: [][]common.Hash{{ethx.LightClient.Event("StageProgress")}}}, lo, hi)
		if err != nil {
			return err
		}
		for _, l := range logs {
			ev, err := ethx.DecodeStageProgress(l)
			if err != nil {
				continue
			}
			key := hexOf(ev.Key)
			if _, ok := s.Seen[key]; ok {
				continue
			}
			s.Seen[key] = stageSeen{Header: hexOf(ev.HeaderHash), Height: ev.Height, Tx: ev.Tx.Hex(), Block: ev.Block}
		}
		s.Cursor = hi
	}
	for key, st := range s.Seen {
		if strings.HasPrefix(st.Verdict, "unread") {
			// a stage an earlier build could not read (it read the call's leaves): judged now by the event's height,
			// once read again (the record holds no height for it)
			delete(s.Seen, key)
			m.Alarm.Clear("stage:unread:" + short(key))
			continue
		}
		if st.Verdict != "" && st.Verdict != "ok" {
			m.Alarm.AlarmEvery("stage:unknown:"+short(st.Header), fmt.Sprintf("a stage of the client (tx %s, Ethereum block %d) tallies header %s at height %d, which gno.land does not have (%s): a set's keys staging a header that never happened (§6.3)", st.Tx, st.Block, short(st.Header), st.Height, strings.TrimPrefix(st.Verdict, "unknown: ")), 24*time.Hour)
			continue
		}
		if st.Verdict == "ok" {
			continue
		}
		if st.Height > head {
			// a stage of a header above the head waits for the chain — unless it is more than an hour of blocks above
			// this node's head and the second source's (or no second source answers): a header gno.land does not have
			// and cannot have within the hour, the leaked key's early warning §6.3 names (E4-GT5)
			allowance := int64(m.Cfg.AheadAllowance())
			if st.Height-head <= allowance {
				continue
			}
			second, ok := gnoSecondHead(ctx, m.sources)
			if ok && st.Height-int64(second) <= allowance {
				continue // the own node lags the second source: judged once it has the height
			}
			secondText := "no second source answered"
			if ok {
				secondText = fmt.Sprintf("the second source's head is %d", second)
			}
			st.Verdict = fmt.Sprintf("unknown: at height %d, more than an hour of blocks (%d) above this node's head %d, and %s", st.Height, allowance, head, secondText)
			s.Seen[key] = st
			m.Alarm.AlarmEvery("stage:unknown:"+short(st.Header), fmt.Sprintf("a stage of the client (tx %s, Ethereum block %d) tallies header %s at height %d, which gno.land does not have (%s): a set's keys staging a header that never happened (§6.3)", st.Tx, st.Block, short(st.Header), st.Height, strings.TrimPrefix(st.Verdict, "unknown: ")), 24*time.Hour)
			continue
		}
		b, err := m.own.Block(ctx, st.Height)
		if err != nil {
			m.blind(fmt.Sprintf("the stage of header %s at height %d (tx %s) could not be read from this node to compare (%v): read again next poll", short(st.Header), st.Height, st.Tx, err))
			continue
		}
		if strings.EqualFold(b.Hash, st.Header) {
			st.Verdict = "ok"
			s.Seen[key] = st
			continue
		}
		second := ""
		if len(m.sources) > 0 {
			if sb, err := m.sources[0].Block(ctx, st.Height); err == nil {
				second = sb.Hash
			}
		}
		if second != "" && strings.EqualFold(second, st.Header) {
			st.Verdict = "ok"
			s.Seen[key] = st
			continue
		}
		st.Verdict = fmt.Sprintf("unknown: this node's block at %d is %s, the second source's %s", st.Height, short(b.Hash), orNone(short(second)))
		s.Seen[key] = st
		m.Alarm.AlarmEvery("stage:unknown:"+short(st.Header), fmt.Sprintf("a stage of the client (tx %s, Ethereum block %d) tallies header %s at height %d, which gno.land does not have (%s): a set's keys staging a header that never happened (§6.3)", st.Tx, st.Block, short(st.Header), st.Height, strings.TrimPrefix(st.Verdict, "unknown: ")), 24*time.Hour)
	}
	return nil
}

// txInput is a transaction's calldata as both providers give it, refused unless it is a call to `to`.
func (m *Monitor) txInput(ctx context.Context, h common.Hash, to common.Address) ([]byte, error) {
	a, err := m.pair.A.Transaction(ctx, h)
	if err != nil {
		return nil, err
	}
	b, err := m.pair.B.Transaction(ctx, h)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(a.Input, b.Input) || (a.To == nil) != (b.To == nil) || (a.To != nil && *a.To != *b.To) {
		return nil, fmt.Errorf("%w on transaction %s", ethx.ErrDisagree, h.Hex())
	}
	if a.To == nil || *a.To != to {
		return nil, fmt.Errorf("transaction %s is not a call to %s (made through another contract)", h.Hex(), to.Hex())
	}
	return a.Input, nil
}

// decisionLog is every decision HlAdmin recorded (DecisionRecorded), with the body its delivery carried when the
// delivery's calldata could be read, and the scan's place.
type decisionLog struct {
	Cursor uint64            `json:"cursor"`
	Kinds  map[string]uint8  `json:"kinds"`  // number → kind
	Blocks map[string]uint64 `json:"blocks"` // number → the block it was recorded in
	Bodies map[string]string `json:"bodies"` // number → the body delivered, hex
	Unread map[string]string `json:"unread"` // number → why its body could not be read
	// ExecCursor is the place of the scan of the line's DecisionExecuted events, Upgrades every kind 8 it found
	// executed, in the order the chain ran them (each proxy's EIP-1967 slots are held to its newest: upgrades)
	ExecCursor uint64        `json:"execCursor,omitempty"`
	Upgrades   []upgradeSeen `json:"upgrades,omitempty"`
}

// upgradeSeen is a kind 8 HlAdmin's line executed: its number, where its DecisionExecuted log lies, and its parameters
// (proxy 32 ‖ implementation 32 ‖ code hash 32 ‖ call, hex) once read.
type upgradeSeen struct {
	Number uint64 `json:"number"`
	Block  uint64 `json:"block"`
	Index  uint   `json:"index"`
	Params string `json:"params,omitempty"`
}

func (d *decisionLog) init() {
	if d.Kinds == nil {
		d.Kinds = map[string]uint8{}
	}
	if d.Blocks == nil {
		d.Blocks = map[string]uint64{}
	}
	if d.Bodies == nil {
		d.Bodies = map[string]string{}
	}
	if d.Unread == nil {
		d.Unread = map[string]string{}
	}
}

// syncDecisions reads HlAdmin's DecisionRecorded events (every program of its line) after the cursor up to F, and the
// body each delivery carried: the transaction's `Mailbox.process(metadata, message)` calldata, the message checked to
// be HlAdmin's and to carry the number.
func (m *Monitor) syncDecisions(ctx context.Context, admins []common.Address, F uint64) error {
	d := &m.state.Decisions
	d.init()
	lo := d.Cursor + 1
	if d.Cursor == 0 {
		lo = m.state.From
	}
	if lo > F {
		return nil
	}
	hi := min(F, lo+burnScanMax-1)
	logs, err := m.pair.Logs(ctx, ethx.LogQuery{Addresses: admins, Topics: [][]common.Hash{{ethx.Admin.Event("DecisionRecorded")}}}, lo, hi)
	if err != nil {
		return err
	}
	for _, l := range logs {
		ev, err := ethx.DecodeDecisionRecorded(l)
		if err != nil {
			continue
		}
		n := strconv.FormatUint(ev.Number, 10)
		d.Kinds[n], d.Blocks[n] = ev.Kind, ev.Block
		body, err := m.deliveredBody(ctx, l, ev.Number)
		if err != nil {
			d.Unread[n] = err.Error()
			continue
		}
		d.Bodies[n] = hex.EncodeToString(body)
	}
	d.Cursor = hi
	return nil
}

// deliveredBody is the decision body a DecisionRecorded log's transaction delivered: a direct Mailbox.process call
// whose message is to the log's HlAdmin and carries the number.
func (m *Monitor) deliveredBody(ctx context.Context, l types.Log, number uint64) ([]byte, error) {
	input, err := m.txInput(ctx, l.TxHash, Address(m.Cfg.Eth.Mailbox))
	if err != nil {
		return nil, err
	}
	meth, err := ethx.Mailbox.ABI.MethodById(input[:min(4, len(input))])
	if err != nil || meth.Name != "process" {
		return nil, errors.New("the delivery is not a Mailbox.process call")
	}
	vals, err := meth.Inputs.Unpack(input[4:])
	if err != nil || len(vals) != 2 {
		return nil, errors.New("the process call does not decode")
	}
	msg, ok := vals[1].([]byte)
	if !ok || len(msg) < 77+9 {
		return nil, errors.New("the message is shorter than a decision")
	}
	rcpt, ok := ethx.AddressFrom32([32]byte(msg[45:77]))
	if !ok || rcpt != l.Address {
		return nil, errors.New("the message delivered is not to this HlAdmin")
	}
	body := msg[77:]
	if binary.BigEndian.Uint64(body[1:9]) != number {
		return nil, fmt.Errorf("the message delivered carries number %d, not %d", binary.BigEndian.Uint64(body[1:9]), number)
	}
	return body, nil
}

// syncUpgrades reads the DecisionExecuted events of HlAdmin's line (every program of it) after its cursor up to F and
// keeps the kind 8s, in the order they ran.
func (m *Monitor) syncUpgrades(ctx context.Context, admins []common.Address, F uint64) error {
	d := &m.state.Decisions
	lo := d.ExecCursor + 1
	if d.ExecCursor == 0 {
		lo = m.state.From
	}
	if lo > F {
		return nil
	}
	hi := min(F, lo+burnScanMax-1)
	logs, err := m.pair.Logs(ctx, ethx.LogQuery{Addresses: admins, Topics: [][]common.Hash{{ethx.Admin.Event("DecisionExecuted")}}}, lo, hi)
	if err != nil {
		return err
	}
	for _, l := range logs {
		ev, err := ethx.DecodeDecisionExecuted(l)
		if err != nil || ev.Kind != 8 {
			continue
		}
		d.Upgrades = append(d.Upgrades, upgradeSeen{Number: ev.Number, Block: ev.Block, Index: ev.Index})
	}
	d.ExecCursor = hi
	return nil
}

// upgrades holds every proxy a kind 8 upgraded to that decision (the plan red team's E3-RT8, the assumptions audit's
// E3-AS7). HlAdmin's checks after an upgrade read the proxy's own answers — admin(), implementation(), owner(), the
// bindings — which are the new code's, and code whose call rewrote the EIP-1967 slots answers them as the deployment's.
// So once a kind 8 has executed, the proxy's two slots themselves are read (eth_getStorageAt, both providers alike, at
// F): the implementation slot must hold the implementation the newest kind 8 of that proxy installed, the admin slot
// the ProxyAdmin (the live HlAdmin's proxyAdmin()). A difference alarms proxy:slots:<proxy> — hlwatch is never green
// over it — until they hold; read every poll, since such code can rewrite them at any later call.
func (m *Monitor) upgrades(ctx context.Context, o *ethObjects, snap *ethSnapshot, H int64) {
	addrs := append(append([]common.Address{}, o.admins.Retired...), o.admins.Live)
	if err := m.syncUpgrades(ctx, addrs, snap.Block.Number); err != nil {
		m.ethErr("HlAdmin's executed decisions", err)
		m.blind("HlAdmin's executed upgrades (the proxies' EIP-1967 slots): " + ethReadText(err))
		return
	}
	d := &m.state.Decisions
	if d.ExecCursor < snap.Block.Number {
		m.behind(fmt.Sprintf("HlAdmin's executed decisions are read to block %d of the finalized block %d", d.ExecCursor, snap.Block.Number))
		return
	}
	sort.SliceStable(d.Upgrades, func(i, j int) bool {
		a, b := d.Upgrades[i], d.Upgrades[j]
		return a.Block < b.Block || (a.Block == b.Block && a.Index < b.Index)
	})
	newest := map[common.Address]upgradeSeen{}
	var proxies []common.Address
	for i := range d.Upgrades {
		u := &d.Upgrades[i]
		if u.Params == "" {
			p, err := m.upgradeParams(ctx, u.Number, H)
			if err != nil {
				// an unread one may be a proxy's newest: no proxy is held to an older one in its place
				m.blind(fmt.Sprintf("decision #%d (kind 8) executed, and its parameters could not be read (%v): the proxies' EIP-1967 slots are not judged", u.Number, err))
				return
			}
			u.Params = hex.EncodeToString(p)
		}
		p, err := hex.DecodeString(u.Params)
		if err != nil || len(p) < 96 {
			continue
		}
		proxy, ok := ethx.AddressFrom32([32]byte(p[0:32]))
		if !ok {
			continue // HlAdmin spends a kind 8 whose proxy is no address: it never executes
		}
		if _, seen := newest[proxy]; !seen {
			proxies = append(proxies, proxy)
		}
		newest[proxy] = *u
	}
	sort.Slice(proxies, func(i, j int) bool { return bytes.Compare(proxies[i][:], proxies[j][:]) < 0 })
	pa := o.admins.State.ProxyAdmin
	for _, proxy := range proxies {
		u := newest[proxy]
		p, _ := hex.DecodeString(u.Params)
		impl, ok := ethx.AddressFrom32([32]byte(p[32:64]))
		if !ok {
			continue
		}
		key := "proxy:slots:" + proxy.Hex()
		iw, err := m.pair.StorageAt(ctx, proxy, ethx.SlotImplementation, snap.Pin)
		var aw common.Hash
		if err == nil {
			aw, err = m.pair.StorageAt(ctx, proxy, ethx.SlotAdmin, snap.Pin)
		}
		if err != nil {
			m.ethErr("the proxy "+proxy.Hex()+"'s EIP-1967 slots", err)
			m.blind("the proxy " + proxy.Hex() + "'s EIP-1967 slots (held to decision #" + strconv.FormatUint(u.Number, 10) + "): " + ethReadText(err))
			continue
		}
		var why []string
		if iw != common.BytesToHash(impl.Bytes()) {
			why = append(why, fmt.Sprintf("its implementation slot holds %s, not %s, which decision #%d (kind 8) installed", slotWord(iw), impl.Hex(), u.Number))
		}
		if pa == (common.Address{}) || aw != common.BytesToHash(pa.Bytes()) {
			why = append(why, fmt.Sprintf("its admin slot holds %s, not the ProxyAdmin %s", slotWord(aw), pa.Hex()))
		}
		if len(why) == 0 {
			m.Alarm.Clear(key)
			continue
		}
		m.Alarm.Alarm(key, fmt.Sprintf("the proxy %s, upgraded by decision #%d (kind 8): %s — read from the slots themselves (eth_getStorageAt): the proxy's own answers, which HlAdmin's checks read, are its code's, and code that rewrote its slots answers them as the deployment's (E3-RT8, E3-AS7) — the proxy may be out of the deployment's hands, and GovDAO's next decision to it may not reach it", proxy.Hex(), u.Number, strings.Join(why, "; ")))
	}
}

// upgradeParams is an executed kind 8's parameters (proxy 32 ‖ implementation 32 ‖ code hash 32 ‖ call): the body its
// delivery carried (the foreign-decision rule holds it to r/hlgov's), else r/hlgov's own record of the number.
func (m *Monitor) upgradeParams(ctx context.Context, n uint64, H int64) ([]byte, error) {
	if b, ok := m.state.Decisions.Bodies[strconv.FormatUint(n, 10)]; ok {
		if body, err := hex.DecodeString(b); err == nil && len(body) >= 9+96 && body[0] == 8 {
			return body[9:], nil
		}
	}
	if m.Cfg.Gno.Gov == "" {
		return nil, errors.New("its delivery was not read, and the config names no r/hlgov")
	}
	gd, err := gnoside.GovDecisionAt(ctx, m.own, m.Cfg.Gno.Gov, n, H)
	if err != nil {
		return nil, err
	}
	if gd == nil {
		return nil, fmt.Errorf("its delivery was not read, and r/hlgov holds no decision %d", n)
	}
	body, err := hex.DecodeString(strings.TrimPrefix(gd.Body, "0x"))
	if err != nil || len(body) < 9+96 || body[0] != 8 {
		return nil, fmt.Errorf("r/hlgov's decision %d is no kind 8", n)
	}
	return body[9:], nil
}

// slotWord is a storage word as an address when its top twelve bytes are zero, else the whole word.
func slotWord(w common.Hash) string {
	if a, ok := ethx.AddressFrom32([32]byte(w)); ok {
		return a.Hex()
	}
	return w.Hex()
}

// ethObjects is the gate's and HlAdmin's state at the snapshot, read once a poll (both providers).
type ethObjects struct {
	gate     *ethx.GateState
	admins   *adminLine
	pending  []*ethx.Decision // HlAdmin's pending decisions (the live one's): every one read so far (pendingAll)
	gateErr  error
	adminErr error
}

func (m *Monitor) objects(ctx context.Context, snap *ethSnapshot) *ethObjects {
	if m.last != nil && m.last.objects != nil {
		return m.last.objects
	}
	o := &ethObjects{}
	if m.Cfg.Eth.Gate != "" {
		o.gate, o.gateErr = ethx.ReadGate(ctx, m.pair, Address(m.Cfg.Eth.Gate), snap.Pin)
	} else {
		o.gateErr = errors.New("the config names no gate")
	}
	if m.Cfg.Eth.Admin != "" {
		o.admins, o.adminErr = followAdmins(ctx, m.pair, Address(m.Cfg.Eth.Admin), snap.Pin)
		if o.adminErr == nil {
			o.pending, o.adminErr = m.pendingAll(ctx, o.admins, snap)
		}
	} else {
		o.adminErr = errors.New("the config names no HlAdmin")
	}
	if m.last != nil {
		m.last.objects = o
	}
	return o
}

// hlAdminWordSlot is HlAdmin's ERC-7201 namespace (HlAdminStorage.SLOT): its first word packs lastSeq (bytes 24–31 of the
// big-endian word) and startSeq (16–23), among others (cmd/hleth's audit reads it so).
var hlAdminWordSlot = common.HexToHash("0xa549383900acf2d7f17f70f3a8e005874b124005220e85417ec5628fc6dcfa00")

// pendingAll reads every decision pending on the live HlAdmin at F (E4-CF4: the last 256 numbers alone hid an older
// one — a kind 8 waiting for code for a year, never listed nor checked, the page green): as the executor's cursor
// (E3-GS9), the numbers from a cursor below which every number was read at a finalized block, adminScan a poll, and
// those found pending below it, read again every poll. A fresh cursor starts past the HlAdmin's stored start — no number
// at or below it ever held a decision — and while it is below the last number the reading is behind (named; hlwatch
// polls again, and is not green until it is read).
func (m *Monitor) pendingAll(ctx context.Context, line *adminLine, snap *ethSnapshot) ([]*ethx.Decision, error) {
	st, key := line.State, strings.ToLower(line.Live.Hex())
	cur := m.state.AdminLow[key]
	if cur == 0 {
		w, err := m.pair.StorageAt(ctx, line.Live, hlAdminWordSlot, snap.Pin)
		if err != nil {
			return nil, fmt.Errorf("HlAdmin %s's stored start (its first storage word, where a fresh cursor begins): %w", line.Live.Hex(), err)
		}
		cur = min(binary.BigEndian.Uint64(w[16:24]), st.LastSeq) + 1
	}
	numbers := append([]uint64{}, m.state.AdminPending[key]...)
	window := uint64(0)
	for n := cur; n <= st.LastSeq && window < adminScan; n++ {
		numbers = append(numbers, n)
		window++
	}
	ds, err := ethx.ReadDecisions(ctx, m.pair, line.Live, numbers, snap.Pin)
	if err != nil {
		return nil, err
	}
	var out []*ethx.Decision
	var still []uint64
	for i, d := range ds {
		if d == nil {
			continue
		}
		out = append(out, d)
		still = append(still, numbers[i])
	}
	cur += window
	m.state.AdminLow[key] = cur
	if len(still) > 0 {
		m.state.AdminPending[key] = still
	} else {
		delete(m.state.AdminPending, key)
	}
	m.adminCursor = cur
	if cur <= st.LastSeq {
		m.behind(fmt.Sprintf("HlAdmin %s's numbers are read to %d of its last %d at the finalized block (%d a poll): a decision pending above is not listed yet", line.Live.Hex(), cur-1, st.LastSeq, adminScan))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// ethChecks runs the Ethereum-side rules at every poll that read the snapshot and the own node's head, whatever the gno
// cut gave (E6-GT1): mb and routes are hlmailbox and the routes as the cut's window first read them, nil when they did
// not read (the pause mirror and the caps then wait).
func (m *Monitor) ethChecks(ctx context.Context, snap *ethSnapshot, mb *gnoside.Mailbox, routes map[string]*gnoside.Route, H int64, view *monitorView) {
	cs := snap.Client
	if cs == nil {
		m.blind("the config names no light client (eth.client): the header record, the stages, the decisions, the pause mirror and the caps are not judged")
		return
	}
	client := Address(m.Cfg.Eth.Client)
	// (no alarm on the client's age: a header is bought only when it buys something, §5.5, so a quiet bridge's client
	// is old by design; a send waiting seven hours alarms instead, the stalls' delivery age)
	m.mu.Lock()
	m.status.Client = map[string]any{"trustedHeight": cs.TrustedHeight, "trustedHash": hexOf(cs.TrustedHash), "trustedTime": cs.TrustedTime, "records": cs.Records,
		"runningHash": hexOf(cs.RunningHash), "epoch": cs.Epoch, "quarantineEnd": cs.QuarantineEnd, "announced": cs.Announced(), "clockFactor": cs.ClockFactor, "block": snap.Block.Number}
	m.mu.Unlock()

	// the header record: rebuilt to F, held against the node (the fork rule) and against the client's counter (a gap)
	done, err := m.syncRecord(ctx, client, snap.Block.Number)
	if err != nil {
		m.ethErr("the client's record", err)
		m.persist("record:scan", "the client's record scan fails (the fork and gap rules are blind until it passes): "+ethReadText(err), monCutFail)
		m.blind("the client's header record (the fork and gap rules): " + ethReadText(err))
	} else {
		m.clearPersisted("record:scan")
		if !done {
			m.behind(fmt.Sprintf("the client's header record is read to block %d of the finalized block %d", m.state.Record.Cursor, snap.Block.Number))
		}
		m.checkRecord(ctx, H)
		if mm := m.state.Record.Mismatch; mm != nil {
			text := "header mismatch (a fork or a forged header): " + mm.String()
			if m.persisted("header:mismatch", monCutFail) {
				m.flagEth(ctx, "header:mismatch", text, true)
			} else {
				m.Alarm.Alarm("header:mismatch", "flagged once it has stood "+monCutFail.String()+": "+text)
			}
		} else {
			m.clearPersisted("header:mismatch")
		}
		gap := m.state.Record.Gap
		if gap == "" && done && (m.state.Record.Count != cs.Records || !strings.EqualFold(m.state.Record.Hash, hexOf(cs.RunningHash))) {
			gap = fmt.Sprintf("the record rebuilt to block %d holds %d headers (running hash %s), the client says %d (%s)", snap.Block.Number, m.state.Record.Count, short(m.state.Record.Hash), cs.Records, short(hexOf(cs.RunningHash)))
		}
		if gap != "" {
			if m.persisted("header:gap", monCutFail) {
				m.flagEth(ctx, "header:gap", "the client's header record: "+gap, true)
			} else {
				m.Alarm.Alarm("header:gap:pending", "flagged once it has stood "+monCutFail.String()+": "+gap)
			}
		} else {
			m.clearPersisted("header:gap")
			m.Alarm.Clear("header:gap:pending")
		}
	}
	m.checkTrusted(ctx, cs, H)
	if err := m.syncStages(ctx, client, snap.Block.Number, H); err != nil {
		m.ethErr("the client's stages", err)
		m.blind("the client's stages (the stage rule): " + ethReadText(err))
	} else if c := m.state.Stages.Cursor; c < snap.Block.Number {
		m.behind(fmt.Sprintf("the client's stages are read to block %d of the finalized block %d", c, snap.Block.Number))
	}

	// the gate and HlAdmin at F
	o := m.objects(ctx, snap)
	if o.gateErr != nil {
		m.ethErr("the gate", o.gateErr)
		m.blind("the gate: " + ethReadText(o.gateErr))
	}
	if o.adminErr != nil {
		m.ethErr("HlAdmin", o.adminErr)
		m.blind("HlAdmin's line (the decision rules): " + ethReadText(o.adminErr))
		m.persist("decision:line-unfollowed", "HlAdmin's line of succession cannot be read from the configured address ("+ethReadText(o.adminErr)+"): the decision rules and the flags on HlAdmin are blind until it can", monCutFail)
	} else {
		m.clearPersisted("decision:line-unfollowed")
		if text := o.admins.Text(); text != "" {
			m.Alarm.AlarmEvery("admin:successor", text+": every rule and flag of this monitor acts on the live one", 24*time.Hour)
		} else {
			m.Alarm.Clear("admin:successor")
		}
		m.foreignDecisions(ctx, o, snap, H)
		m.upgrades(ctx, o, snap, H)
	}
	if o.gate != nil {
		now := m.now().Unix()
		paused, why := deliveriesPaused(o.gate, cs.QuarantineEnd, now)
		m.mu.Lock()
		m.status.Gate = map[string]any{"anyFlag": o.gate.AnyFlag(), "hold": o.gate.Hold.Set(), "quarantineEnd": cs.QuarantineEnd, "paused": paused, "why": why, "keys": addrStrings(o.gate.Keys[:]), "flags": flagStrings(o.gate.Flags[:])}
		m.mu.Unlock()
		if mb != nil {
			m.mirrorSends(ctx, mb, paused, why)
		} else {
			m.blind("the pause mirror (sends flagged on gno.land while deliveries are paused): hlmailbox did not read this poll")
		}
		m.capRatios(o.gate, routes, mb)
	}
	if view != nil {
		view.Objects = o
	}
}

// foreignDecisions compares every number HlAdmin recorded with r/hlgov's dispatched decisions (§5.9): a number r/hlgov
// never dispatched, a kind or a body that is not r/hlgov's, is foreign. A number r/hlgov lacks at H is read again at
// the node's head first (a decision dispatched after H and delivered before this read is a lag, judged later); a
// re-read that fails is no reading, and the number stays unchecked.
func (m *Monitor) foreignDecisions(ctx context.Context, o *ethObjects, snap *ethSnapshot, H int64) {
	if m.Cfg.Gno.Gov == "" {
		m.blind("the config names no r/hlgov (gno.gov): HlAdmin's decisions are not held to r/hlgov's")
		return
	}
	st := o.admins.State
	addrs := append(append([]common.Address{}, o.admins.Retired...), o.admins.Live)
	if err := m.syncDecisions(ctx, addrs, snap.Block.Number); err != nil {
		m.ethErr("HlAdmin's decisions", err)
		m.blind("HlAdmin's recorded decisions (the foreign-decision rule): " + ethReadText(err))
		return
	}
	if m.state.Decisions.Cursor < snap.Block.Number {
		m.behind(fmt.Sprintf("HlAdmin's recorded decisions are read to block %d of the finalized block %d", m.state.Decisions.Cursor, snap.Block.Number))
		return // the events are read to block Cursor so far: no number is judged before its recording is read
	}
	gov, err := gnoside.GovAt(ctx, m.own, m.Cfg.Gno.Gov, H)
	if err != nil {
		log.Printf("monitor: r/hlgov: %v", err)
		m.blind("r/hlgov (the foreign-decision rule): " + err.Error())
		return
	}
	pending := map[uint64]*ethx.Decision{}
	for _, d := range o.pending {
		pending[d.Number] = d
	}
	var findings []foreignFinding
	checked := m.state.DecisionsChecked
	readErr := false
	for n := m.state.DecisionsChecked + 1; n <= st.LastSeq; n++ {
		gd, err := gnoside.GovDecisionAt(ctx, m.own, m.Cfg.Gno.Gov, n, H)
		if err != nil {
			readErr = true
			break
		}
		if gd == nil {
			again, err := gnoside.GovDecisionAt(ctx, m.own, m.Cfg.Gno.Gov, n, 0)
			if err != nil {
				readErr = true
				break
			}
			if again != nil {
				break // dispatched after H: judged at a height that holds it
			}
			findings = append(findings, foreignFinding{n, fmt.Sprintf("foreign decision: HlAdmin recorded number %d, which r/hlgov never dispatched (r/hlgov's counter is %d)", n, gov.Last)})
			checked = n
			continue
		}
		body, err := hex.DecodeString(strings.TrimPrefix(gd.Body, "0x"))
		if err != nil || len(body) < 9 {
			readErr = true
			break
		}
		key := strconv.FormatUint(n, 10)
		if k, ok := m.state.Decisions.Kinds[key]; ok && k != body[0] {
			findings = append(findings, foreignFinding{n, fmt.Sprintf("foreign decision: number %d on HlAdmin is kind %d, r/hlgov dispatched kind %d", n, k, body[0])})
		} else if delivered, ok := m.state.Decisions.Bodies[key]; ok && delivered != hex.EncodeToString(body) {
			findings = append(findings, foreignFinding{n, fmt.Sprintf("foreign decision: number %d on HlAdmin carries a body that is not r/hlgov's (kind %d)", n, body[0])})
		} else if !ok {
			if d := pending[n]; d != nil && (d.Kind != body[0] || !bytes.Equal(d.Params, body[9:])) {
				findings = append(findings, foreignFinding{n, fmt.Sprintf("foreign decision: number %d pending on HlAdmin (kind %d) is not what r/hlgov dispatched (kind %d)", n, d.Kind, body[0])})
			} else if why, unread := m.state.Decisions.Unread[key]; unread {
				m.Alarm.AlarmEvery("decision:unread:"+key, fmt.Sprintf("decision %d's delivery could not be read (%s): it is judged by its kind and, while pending, its parameters only", n, why), 24*time.Hour)
			}
		}
		checked = n
	}
	if readErr {
		m.blind(fmt.Sprintf("a decision of r/hlgov could not be read: HlAdmin's numbers after %d are not judged this poll", checked))
	}
	m.applyForeign(ctx, findings, checked, readErr, m.foreignStanding(ctx, o, snap))
	var texts []string
	for _, f := range findings {
		texts = append(texts, f.text)
	}
	m.mu.Lock()
	m.status.Decisions = map[string]any{"admin": o.admins.Live.Hex(), "adminLast": st.LastSeq, "govLast": gov.Last, "checked": m.state.DecisionsChecked, "pending": len(o.pending), "foreign": strings.Join(texts, "; "), "foreignStanding": len(m.state.Foreign)}
	m.status.Admin = map[string]any{"address": o.admins.Live.Hex(), "setupDone": st.SetupDone, "lastSeq": st.LastSeq, "anyFlag": st.AnyFlag(), "hold": st.Hold.Set(), "keys": addrStrings(st.Keys[:]), "flags": flagStrings(st.Flags[:]), "clockFactor": st.ClockFactor}
	m.mu.Unlock()
	if st.LastSeq > gov.Last {
		m.Alarm.Alarm("decision:counter", fmt.Sprintf("HlAdmin's last number %d is past r/hlgov's %d: a foreign decision took a number, or GovDAO's was lost", st.LastSeq, gov.Last))
	} else {
		m.Alarm.Clear("decision:counter")
	}
}

// foreignFinding is one foreign decision found: HlAdmin's number and what is foreign about it.
type foreignFinding struct {
	n    uint64
	text string
}

// applyForeign applies the decision rule's findings with the cut's patience: a foreign finding flags the gate and
// HlAdmin once it has stood two minutes (the alarm decision:foreign:pending before that), and is kept in the state by
// its number (E4-GT6: foreignStanding judges it again every poll); the numbers checked advance past a finding only when
// it is flagged, and past a clean poll's numbers at once. standing are the findings flagged before that still pend: the
// flags are set again for them whenever found cleared, and the alarm ends only when none stands.
func (m *Monitor) applyForeign(ctx context.Context, findings []foreignFinding, checked uint64, readErr bool, standing []string) {
	const still = " (still pending on HlAdmin: flagged until GovDAO cancels it)"
	if len(findings) == 0 {
		m.state.DecisionsChecked = checked
		if !readErr {
			m.Alarm.Clear("decision:foreign:pending")
			delete(m.seen, "decision:foreign")
		}
		if len(standing) > 0 {
			m.flagEth(ctx, "decision:foreign", strings.Join(standing, "; ")+still, true)
		} else {
			m.Alarm.Clear("decision:foreign")
		}
		return
	}
	var texts []string
	for _, f := range findings {
		texts = append(texts, f.text)
	}
	text := strings.Join(texts, "; ")
	if m.persisted("decision:foreign", monCutFail) {
		m.Alarm.Clear("decision:foreign:pending")
		delete(m.seen, "decision:foreign")
		for _, f := range findings {
			m.state.Foreign[strconv.FormatUint(f.n, 10)] = f.text
		}
		if len(standing) > 0 {
			text += "; " + strings.Join(standing, "; ") + still
		}
		m.flagEth(ctx, "decision:foreign", text, true)
		m.state.DecisionsChecked = checked
		return
	}
	m.Alarm.Alarm("decision:foreign:pending", "flagged once it has stood "+monCutFail.String()+": "+text)
	if len(standing) > 0 {
		m.flagEth(ctx, "decision:foreign", strings.Join(standing, "; ")+still, true)
	}
}

// foreignStanding judges every foreign decision flagged at an earlier poll (E4-GT6) and answers the findings that stand:
// one still pending on the live HlAdmin at the finalized block stands (an unpause leaves a foreign decision to mature);
// one no longer pending there (cancelled, spent, executed; or recorded on a retired HlAdmin, where nothing runs) is
// cured and dropped. A pending list that does not read keeps every finding standing.
func (m *Monitor) foreignStanding(ctx context.Context, o *ethObjects, snap *ethSnapshot) []string {
	if len(m.state.Foreign) == 0 {
		return nil
	}
	keys := sortedSymbols(m.state.Foreign)
	numbers := make([]uint64, 0, len(keys))
	for _, k := range keys {
		n, _ := strconv.ParseUint(k, 10, 64)
		numbers = append(numbers, n)
	}
	ds, err := ethx.ReadDecisions(ctx, m.pair, o.admins.Live, numbers, snap.Pin)
	var standing []string
	for i, k := range keys {
		if err == nil && ds[i] == nil {
			log.Printf("monitor: the foreign decision #%s is no longer pending on HlAdmin %s (cancelled, spent or executed): its finding ends", k, o.admins.Live.Hex())
			delete(m.state.Foreign, k)
			continue
		}
		standing = append(standing, m.state.Foreign[k])
	}
	if err != nil {
		m.ethErr("the foreign decisions still pending", err)
		m.blind("whether the foreign decisions flagged still pend on HlAdmin: " + ethReadText(err))
	}
	return standing
}

// mirrorSends sets the watch key's `sends` flag on every gno route while gno.land → Ethereum deliveries are paused and
// clears its own when they resume (§5.9: never credits or payouts for that).
func (m *Monitor) mirrorSends(ctx context.Context, mb *gnoside.Mailbox, paused bool, why string) {
	if paused {
		m.Alarm.Alarm("deliveries:paused", "gno.land → Ethereum deliveries are paused ("+why+"): sends flagged on the gno routes")
		for _, path := range m.liveRoutes() {
			m.setGnoFlag(ctx, mb, path, "sends", why)
		}
		m.state.SendsSet = true
		return
	}
	m.Alarm.Clear("deliveries:paused")
	if !m.state.SendsSet {
		return
	}
	for _, path := range m.liveRoutes() {
		if rf, ok := mb.Routes[path]; ok {
			for _, f := range rf.StandingFlags() {
				if f.Seat == m.seat && f.Fn == "sends" {
					m.clearGnoFlag(ctx, path, "sends")
				}
			}
		}
	}
	m.state.SendsSet = false
}

// liveRoutes are the realms a route flag goes on: every configured route and, while a migration is open, the token's
// successor too.
func (m *Monitor) liveRoutes() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range m.Cfg.Routes {
		paths := []string{r.Gno}
		if mig := m.migs[r.Symbol]; mig != nil {
			paths = append(paths, mig.Successor)
		}
		for _, p := range paths {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// capRatios alarms mint:cap-ratio:<token> while a token's day cap on the gate exceeds a fifth of its lock on gno.land
// (§5.4 bounds a forged day's mint before the flags by a tenth of the lock; the cap is an absolute number moved by kind
// 12 alone), quiet until the lock first reached the expected first-week lock, five caps, or 14 days after the launch;
// and limits:mismatch while a route's live per-message maximum exceeds the gate's (a send between the two is taken on
// gno.land and parked for good on Ethereum, OverMax, until a kind 12).
func (m *Monitor) capRatios(g *ethx.GateState, routes map[string]*gnoside.Route, mb *gnoside.Mailbox) {
	for _, r := range m.Cfg.Routes {
		rj := routes[r.Symbol]
		i, ok := TokenIndex(r.Symbol)
		if rj == nil || !ok || r.Token == "" || m.migErrs[r.Symbol] != nil {
			continue
		}
		lock := rj.Lock
		routeMax := rj.Maximum
		if mig := m.migs[r.Symbol]; mig != nil {
			lock, _, _, _ = mig.Sum()
			if mig.Succ != nil {
				routeMax = mig.Succ.Maximum
			}
		}
		l := g.Limits[i]
		quietEnds := int64(0)
		if mb != nil && mb.LaunchedAt > 0 {
			quietEnds = mb.LaunchedAt + int64(mb.Clocks.DeadlineDuration()/time.Second)
		}
		fiveCaps := l.DayCap > 0 && lock >= 0 && uint64(lock) >= 5*l.DayCap
		reached := m.state.CapReached[r.Symbol] || r.ExpectedLock <= 0 || lock >= r.ExpectedLock || fiveCaps || (quietEnds > 0 && m.now().Unix() >= quietEnds)
		m.state.CapReached[r.Symbol] = reached
		key := "mint:cap-ratio:" + r.Symbol
		if reached && !(lock > 0 && l.DayCap <= uint64(lock)/5) {
			m.Alarm.AlarmEvery(key, fmt.Sprintf("%s's day cap on the gate, %d, is above a fifth of its lock on gno.land, %d: lower it by kind 12, or wait for the lock to grow (§5.4)", r.Symbol, l.DayCap, lock), 24*time.Hour)
		} else {
			m.Alarm.Clear(key)
		}
		mmKey := "limits:mismatch:" + r.Symbol
		if routeMax > 0 && uint64(routeMax) > l.MaxPerMessage {
			m.Alarm.AlarmEvery(mmKey, fmt.Sprintf("%s's per-message maximum on gno.land, %d, is above the gate's %d (base units): a send between the two is taken on gno.land and parked for good on Ethereum (OverMax, until a kind 12) — raise the gate's by kind 12 first (§5.4)", r.Symbol, routeMax, l.MaxPerMessage), 24*time.Hour)
		} else {
			m.Alarm.Clear(mmKey)
		}
	}
}

// flagEthGate is a flag on the gate alone (an invariant break, the sources rule).
func (m *Monitor) flagEthGate(ctx context.Context, key, text string) {
	m.flagEth(ctx, key, text, false)
}

// flagEth sets the watch key's flag on the gate — and on HlAdmin too for the header and decision rules — where it does
// not stand already (§5.5: a flag is a transaction per object, never re-asserted on a timer; it is set again only when
// found cleared, or when an unpause recorded at a final block would clear it). Without the key it alarms; while the
// own node is the odd one out, the header and decision rules alarm only.
func (m *Monitor) flagEth(ctx context.Context, key, text string, adminToo bool) {
	if m.state.OddNode && adminToo {
		m.Alarm.Alarm(key, "alarm only while the own node is the odd one out: "+text)
		return
	}
	m.Alarm.Flag(key, text)
	if m.ReadOnly {
		return
	}
	what := "the gate"
	if adminToo {
		what = "the gate and HlAdmin"
	}
	if m.ethWatch == nil {
		m.Alarm.Alarm("flag:eth", "would flag "+what+" but this machine holds no Ethereum watch key (eth.watchKey): "+text)
		return
	}
	snap := m.lastSnap()
	if snap == nil {
		return
	}
	o := m.objects(ctx, snap)
	if o.gate != nil {
		m.ensureEthFlag(ctx, Address(m.Cfg.Eth.Gate), o.gate.Keys, o.gate.Flags, 0, o, snap, text)
	} else {
		m.Alarm.Alarm("flag:eth", "the gate could not be read ("+ethReadText(o.gateErr)+"): its flag is not set this poll: "+text)
	}
	if adminToo {
		if o.admins != nil {
			m.ensureEthFlag(ctx, o.admins.Live, o.admins.State.Keys, o.admins.State.Flags, 1, o, snap, text)
		} else {
			m.Alarm.Alarm("flag:eth", "HlAdmin could not be read ("+ethReadText(o.adminErr)+"): its flag is not set this poll: "+text)
		}
	}
}

// flagNeed says whether the watch key's flag on an object must be set now (pure): its slot among the object's keys,
// the flag as read at the final block, the block of a setFlag this key sent that the final block has not reached yet
// (0 for none), and the delivery blocks of the unpauses HlAdmin has recorded for this object. It is needed when it is
// not set, or when a recorded unpause would clear it (an unpause clears the flags set at a block before its
// delivery); never otherwise — no timer.
func flagNeed(slot int, flag ethx.Flag, sentBlock uint64, unpauses []uint64) (bool, string) {
	if slot < 0 {
		return false, "the watch key holds no slot"
	}
	block := flag.BlockNumber
	if sentBlock > block {
		block = sentBlock // our setFlag, landed above the final block read: standing, pending finality
	}
	if block == 0 {
		return true, "found cleared"
	}
	for _, d := range unpauses {
		if block < d {
			return true, fmt.Sprintf("an unpause recorded at block %d would clear the flag set at block %d", d, block)
		}
	}
	return false, ""
}

// ensureEthFlag sets the watch key's flag on one object when flagNeed says so, and records the transaction.
func (m *Monitor) ensureEthFlag(ctx context.Context, obj common.Address, keys [6]common.Address, flags [6]ethx.Flag, object byte, o *ethObjects, snap *ethSnapshot, why string) {
	me := m.ethWatch.Address()
	slot := -1
	for i, k := range keys {
		if k == me {
			slot = i
		}
	}
	name := "the gate"
	if object == 1 {
		name = "HlAdmin"
	}
	if slot < 0 {
		m.Alarm.Alarm("flag:eth:"+strings.ToLower(obj.Hex()), "would flag "+name+" but the Ethereum watch key "+me.Hex()+" holds no slot there: "+why)
		return
	}
	var unpauses []uint64
	for _, d := range o.pending {
		if d.Kind == kindUnpause && len(d.Params) >= 1 && d.Params[0] == object && d.DeliveredBlock <= snap.Block.Number {
			unpauses = append(unpauses, d.DeliveredBlock)
		}
	}
	sent := m.state.EthFlags[strings.ToLower(obj.Hex())]
	sentBlock := uint64(0)
	if sent.Block > snap.Block.Number {
		sentBlock = sent.Block
	}
	need, reason := flagNeed(slot, flags[slot], sentBlock, unpauses)
	if !need {
		return
	}
	data, err := m.flagCall(object)
	if err != nil {
		m.Alarm.Alarm("flag:eth", err.Error())
		return
	}
	rc, err := m.ethWatch.Send(ctx, ethx.Call{To: obj, Data: data, Label: "flag " + name, MaxBaseFee: m.Cfg.EmergencyMaxBaseFee()})
	if err != nil {
		m.Alarm.Alarm("flag:eth", "setting the flag on "+name+" failed: "+ethx.Redact(err.Error()))
		return
	}
	m.Alarm.Clear("flag:eth")
	block := uint64(0)
	if rc.BlockNumber != nil {
		block = rc.BlockNumber.Uint64()
	}
	m.state.EthFlags[strings.ToLower(obj.Hex())] = ethFlag{Block: block, Tx: rc.TxHash.Hex(), At: m.now().Unix(), Why: why}
	log.Printf("monitor: FLAG set on %s (slot %d) by the watch key %s at block %d (%s): %s", name, slot+1, me.Hex(), block, reason, why)
}

func (m *Monitor) flagCall(object byte) ([]byte, error) {
	if object == 1 {
		return ethx.Admin.Pack("setFlag")
	}
	return ethx.Gate.Pack("setFlag")
}

// lastSnap is this poll's snapshot (nil before one was taken).
func (m *Monitor) lastSnap() *ethSnapshot {
	if m.last == nil {
		return nil
	}
	return m.last.Snap
}

// monitorView is one poll's readings (hlwatch builds its page from them).
type monitorView struct {
	At           time.Time
	Snap         *ethSnapshot
	Head         gno.Head
	Mailbox      *gnoside.Mailbox
	Routes       map[string]*gnoside.Route
	Verdict      bool
	Identities   map[string]Identity
	IdentityText map[string]string
	Objects      *ethObjects
	objects      *ethObjects
	// Blind are the rules this poll could not run, with why; Behind the scans not yet at the finalized block.
	Blind, Behind []string
}

func addrStrings(as []common.Address) []string {
	out := make([]string, len(as))
	for i, a := range as {
		if a != (common.Address{}) {
			out[i] = a.Hex()
		}
	}
	return out
}

func flagStrings(fs []ethx.Flag) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		if f.Set() {
			out[i] = fmt.Sprintf("block %d at %s", f.BlockNumber, time.Unix(int64(f.Time), 0).UTC().Format(time.RFC3339))
		}
	}
	return out
}
