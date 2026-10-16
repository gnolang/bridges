package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gnolang/bridges/ethereum/bridge/gnoside"
)

// The dust folded out of the state (the Go trust audit's E6-GT3). A burn below the fee of the realm that would credit it
// is dust the relayers leave (E5-GS5): it stays uncredited — in flight, as the identity must count it — until someone
// pays its Process, maybe never, and a stranger's 1-unit burns cost ≈$0.03 each. Kept among the index's burns, every one
// was a record (≈700 bytes: its message, its transaction) rewritten with the state every round, for good: 50,000 of
// them 35 MB a round, 610 ms a quiet round (measured). So a burn that has been dust for foldAfter — signed already, on a
// signer — is folded: taken out of the index's burns into a side file written once (an append-only file of lines beside
// the state: a fold, an unfold), and the identity counts each token's folded sum in flight. A folded burn's mark is not
// read when hlmailbox's count moves (the pile's reads nobody pays for); it is read every markRefresh, and before a token
// with folded dust is judged BROKEN with a delivery unplaced — what a delivered folded burn reads as: its amount in the
// route's owed (the sweep queue) and still in the folded sum. Found delivered, or above its realm's fee again (a fee
// lowered), it is unfolded: back among the index's burns, judged as any.
//
// The file and the state agree by a count: the state keeps how many of the file's lines it holds (burnIndex.DustLines),
// written after the lines are synced; a line past it is a round's whose state was never saved, dropped when the file is
// read. The file is rewritten whole (its live folds only) right after a state is saved and only then, so a crash between
// the two leaves a file of exactly the folds that state holds.

// foldAfter is how long a burn is dust before it is folded out of the state.
const foldAfter = time.Hour

// realmFee answers, by token, the fee of the realm that would credit its burn: the line's live successor's while a
// migration is open, else the route's (-1 when neither was read: no burn is dust by it).
func realmFee(routes map[string]*gnoside.Route, lines map[string]*gnoside.Migration) func(string) int64 {
	return func(sym string) int64 {
		if mig := lines[sym]; mig != nil && mig.Succ != nil {
			return mig.Succ.Fee
		}
		if rj := routes[sym]; rj != nil {
			return rj.Fee
		}
		return -1
	}
}

// dustFile is the folded dust: the records by id (in memory), and the file they are kept in ("" keeps them in memory
// only: a state with no file).
type dustFile struct {
	path    string
	recs    map[string]*burnRec
	sums    map[string]*foldStat // by token, kept as burns fold and unfold (a round reads no record)
	lines   int                  // the file's lines (folds and unfolds) since it was last rewritten whole
	pending [][]byte             // lines not yet written
}

// foldStat is a token's folded dust: its sum in flight, how many, and an amount none of them is above.
type foldStat struct{ sum, n, max int64 }

func (d *dustFile) add(rec *burnRec) {
	st := d.sums[rec.Symbol]
	if st == nil {
		st = &foldStat{}
		d.sums[rec.Symbol] = st
	}
	st.sum, st.n, st.max = satAdd(st.sum, rec.Amount), st.n+1, max(st.max, rec.Amount)
}

func (d *dustFile) remove(rec *burnRec) {
	if st := d.sums[rec.Symbol]; st != nil {
		if st.sum, st.n = satSub(st.sum, rec.Amount), st.n-1; st.n <= 0 {
			delete(d.sums, rec.Symbol)
		}
	}
}

// dustLine is one line of the file: a fold (the burn's record) or an unfold (its id).
type dustLine struct {
	ID     string   `json:"id"`
	Fold   *burnRec `json:"fold,omitempty"`
	Unfold bool     `json:"unfold,omitempty"`
}

// dustPath is the side file beside a state file ("" for none).
func dustPath(state string) string {
	if state == "" {
		return ""
	}
	return state + ".dust.jsonl"
}

// attachDust reads the index's folded dust from its file: the first DustLines lines, replayed (the rest dropped: a round
// whose state was never saved); a folded id the burns also hold leaves the burns (the fold is the later word).
func (x *burnIndex) attachDust(path string) error {
	x.init()
	d := &dustFile{path: path, recs: map[string]*burnRec{}, sums: map[string]*foldStat{}}
	x.dust = d
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		x.DustLines = 0
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var offset int64
	for d.lines < x.DustLines {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break // a file shorter than the state's count: rewritten whole after that state was saved
		}
		if err != nil {
			return err
		}
		var l dustLine
		if err := json.Unmarshal(bytes.TrimSpace(line), &l); err != nil || l.ID == "" || (l.Fold == nil) == !l.Unfold {
			return fmt.Errorf("%s is %w at line %d: %v; restore it or delete it knowingly", path, errCorrupt, d.lines+1, err)
		}
		if l.Fold != nil {
			d.recs[l.ID] = l.Fold
		} else {
			delete(d.recs, l.ID)
		}
		d.lines++
		offset += int64(len(line))
	}
	if d.lines < x.DustLines {
		x.DustLines = d.lines
	}
	if st, err := f.Stat(); err == nil && st.Size() > offset {
		if err := os.Truncate(path, offset); err != nil { // the lines of a round whose state was never saved
			return err
		}
	}
	for id, rec := range d.recs {
		delete(x.Burns, id)
		d.add(rec)
	}
	return nil
}

// folded is the index's folded dust (an empty set before attachDust).
func (x *burnIndex) folded() map[string]*burnRec {
	if x.dust == nil {
		x.dust = &dustFile{recs: map[string]*burnRec{}, sums: map[string]*foldStat{}}
	}
	return x.dust.recs
}

// has says whether the index holds a burn by id, among its burns or folded.
func (x *burnIndex) has(id string) bool {
	if _, ok := x.Burns[id]; ok {
		return true
	}
	_, ok := x.folded()[id]
	return ok
}

// foldedSum is each token's folded dust: its sum in flight, how many, and an amount none of them is above.
func (x *burnIndex) foldedSum() map[string][3]int64 {
	x.folded()
	out := map[string][3]int64{}
	for sym, st := range x.dust.sums {
		out[sym] = [3]int64{st.sum, st.n, st.max}
	}
	return out
}

func (d *dustFile) note(l dustLine) {
	b, err := json.Marshal(l)
	if err != nil {
		panic(err) // a burnRec always encodes
	}
	d.pending = append(d.pending, append(b, '\n'))
}

// fold takes a burn out of the index's burns into the folded dust.
func (x *burnIndex) fold(id string) {
	rec := x.Burns[id]
	if rec == nil {
		return
	}
	delete(x.Burns, id)
	x.folded()[id] = rec
	x.dust.add(rec)
	x.dust.note(dustLine{ID: id, Fold: rec})
}

// unfold brings a folded burn back among the index's burns, credited at height (0: still uncredited — above its realm's
// fee again).
func (x *burnIndex) unfold(id string, height int64) {
	rec := x.folded()[id]
	if rec == nil {
		return
	}
	delete(x.dust.recs, id)
	x.dust.remove(rec)
	rec.Credited, rec.mark = height, markSeen{}
	x.Burns[id] = rec
	x.dust.note(dustLine{ID: id, Unfold: true})
}

// readFolded reads the delivered marks of every folded burn of the tokens asked (nil: all), at a reading whose last read
// of hlmailbox gave the delivered count d, and unfolds the ones delivered (their height the mark's). It answers how many
// it found delivered and the highest height among them (the backends rule holds it to a later head).
func (x *burnIndex) readFolded(ctx context.Context, g gnoside.Reader, mailbox string, H int64, d int, syms map[string]bool) (int, int64, error) {
	var ids []string
	for id, rec := range x.folded() {
		if syms == nil || syms[rec.Symbol] {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}
	sort.Strings(ids)
	marks, err := deliveredMarks(ctx, g, mailbox, ids, d, H)
	if err != nil {
		return 0, 0, err
	}
	n, top := 0, int64(0)
	for _, id := range ids {
		if at := marks[id]; at != 0 {
			x.unfold(id, at)
			n++
			top = max(top, at)
		}
	}
	return n, top, nil
}

// foldDust folds the burns that have been dust for foldAfter: uncredited, read unset at the reading whose hlmailbox count
// was d, at or below block F, below the fee of the realm that would credit them (fee answers it by token, -1 unknown),
// and — signed answers it, nil for a machine that signs nothing — signed. Folded burns at or above their realm's fee
// again (a fee lowered) are unfolded. It answers how many it folded.
func (x *burnIndex) foldDust(now time.Time, F uint64, d int, fee func(sym string) int64, signed func(id string) bool) int {
	n := 0
	for _, id := range x.ids() {
		rec := x.Burns[id]
		f := fee(rec.Symbol)
		switch {
		case rec.Credited != 0, !rec.mark.read, rec.mark.delivered != d, rec.Block > F, f <= 0, rec.Amount >= f:
			continue
		case now.Sub(time.Unix(rec.Seen, 0)) < foldAfter:
			continue
		case signed != nil && !signed(id):
			continue
		}
		x.fold(id)
		n++
	}
	for sym, st := range x.foldedSum() {
		if f := fee(sym); f >= 0 && st[2] >= f { // a fee lowered past one of them: a delivery like any, waited for again
			top := int64(0)
			for id, rec := range x.folded() {
				switch {
				case rec.Symbol != sym:
				case rec.Amount >= f:
					x.unfold(id, 0)
				default:
					top = max(top, rec.Amount)
				}
			}
			if st := x.dust.sums[sym]; st != nil {
				st.max = top
			}
		}
	}
	return n
}

// flushDust writes the folded dust's new lines to its file, synced, and answers the count the state is to keep. Call it
// before the state is saved; compactDust after.
func (x *burnIndex) flushDust() error {
	d := x.dust
	if d == nil || d.path == "" || len(d.pending) == 0 {
		if d != nil && d.path == "" {
			d.pending = nil
		}
		if d != nil {
			x.DustLines = d.lines
		}
		return nil
	}
	f, err := os.OpenFile(d.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	for _, l := range d.pending {
		if _, err := f.Write(l); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	d.lines += len(d.pending)
	d.pending = nil
	x.DustLines = d.lines
	return nil
}

// compactDust rewrites the file whole — its live folds only — once its unfolds outnumber them past a margin, and answers
// whether it did (the state is then saved again, its count the new file's). Call it only right after a state was saved:
// nothing changed since, so a crash before the second save leaves a file of exactly that state's folds.
func (x *burnIndex) compactDust() (bool, error) {
	d := x.dust
	if d == nil || d.path == "" || d.lines <= 2*len(d.recs)+256 {
		return false, nil
	}
	ids := make([]string, 0, len(d.recs))
	for id := range d.recs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	tmp := d.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return false, err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	for _, id := range ids {
		b, err := json.Marshal(dustLine{ID: id, Fold: d.recs[id]})
		if err != nil {
			f.Close()
			return false, err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			f.Close()
			return false, err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return false, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, d.path); err != nil {
		return false, err
	}
	if dir, err := os.Open(filepath.Dir(d.path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	d.lines = len(ids)
	x.DustLines = d.lines
	return true, nil
}

// saveWithDust saves a state whose index folds dust: the file's new lines synced, the state written, then the file
// rewritten whole when due and the state written again with its count.
func saveWithDust(x *burnIndex, write func() error) error {
	if err := x.flushDust(); err != nil {
		return fmt.Errorf("the folded dust's file: %w", err)
	}
	if err := write(); err != nil {
		return err
	}
	done, err := x.compactDust()
	if err != nil || !done {
		return err
	}
	return write()
}
