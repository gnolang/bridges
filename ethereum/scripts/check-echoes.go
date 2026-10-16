// check-echoes walks the Ethereum deployment's realms (eth/realm/*/*.gno, tests left out) for a byte a caller chose —
// or any text it cannot vouch for — reaching a panic, an error, an event or a page as it came (the round-4 audits'
// E4-GS1 and E4-RL6; made default-deny by the round-5 realms audit's E5-RL3).
//
//	go run eth/scripts/check-echoes.go [realm dir]        (default eth/realm; eth/scripts/gnotest.sh runs it)
//
// WHY. A failed transaction's error carries its panic text into the block's results; a byte that is not UTF-8 there —
// or a rune gno's byte-offset cut splits (BoundedSprintException, at byte 1,021 on gno v1.5.1) — comes back from the
// public RPC's JSON as U+FFFD, the results root rebuilt from it is not the header's, and nothing in that block can be
// proven to Ethereum: one failed call a block, from any key, stops gno.land → Ethereum (E4-GS1, reproduced). An event
// value is in the results too; a page is what the machines parse.
//
// THE SINKS. A panic's argument; an errors.New's or fmt.Errorf's; every chain.Emit value; and what a page writes —
// the results of a Render function and of every function of this tree whose result a page writes (its page
// functions), and every strings.Builder write in them.
//
// RULE 1, DEFAULT-DENY. Every operand of a sink — the leaves of its + concatenations, through conversions — is a
// recognized safe form, or it is a finding:
//   - a literal; a constant of this tree, or of the pure packages (pureConsts), its text counted as a literal's;
//   - a sanitizer's call (ASCII by construction whatever its arguments: echo — strconv.QuoteToASCII of at most 64
//     bytes —, the number and bool formatters, hex, the EIP-55 and canonical address spellings, a length);
//   - on a page: a page function's call (checked as a page itself); its own builder's String(); a page of the route's
//     State or decision Ledger, or of p/decisions (pagers), that no caller's string reaches (it writes the realm's own
//     records: numbers, hex, canonical addresses, its own words);
//   - or one named, with why it is safe, by an echo-ok reason on the line (or the line before):
//     `// echo-ok(route): a registered route's path`, naming the operand's root identifier.
//
// Everything else is a finding: a builder, a field, a map value, a method's or a closure's result, a stored value, a
// variable that holds a message — whatever the checker cannot prove ASCII and the author has not answered for.
// strconv.Quote is no sanitizer: it keeps printable runes outside ASCII as they are and bounds nothing.
//
// RULE 2, NO SINK AS A VALUE. errors.New, fmt.Errorf and chain.Emit taken as a function value hide their calls.
//
// RULE 3, HAND-OFFS. A caller's string — the taint below — reaches p/hlroute's parsers that echo as it came (ParseRange,
// ParseIDs, ParseFns, ParseSeats, CleanID) and every method of a State (st = hlroute.New(…): its Draft… methods run
// the parsers, and ClaimOf, SendOf, SweepOf, Send and Credit panic through mustID with the raw id) only after
// mustBeASCII in the same function, or on a line whose echo-ok names it: p/hlroute is shared with the live Solana
// deployment and immutable, and its text around an echo is ASCII, so an ASCII argument keeps its refusals valid UTF-8.
//
// RULE 4, THE CUT. A panic, errors.New or fmt.Errorf whose literal and constant text holds a byte outside ASCII is
// held under the cut: that text and each operand's bound (an echo 273 bytes, a number 20, …; an operand of unknown
// length — a reason's, a hex of anything — is unbounded) must add up to at most maxMessage, so no cut reaches a rune;
// one whose text is all ASCII has only ASCII operands (the safe forms, and the reasons', which say so), so a cut
// anywhere splits nothing. And at most two echoes a message.
//
// THE TAINT (rules 3 and the pagers). Every parameter of type string, address, any, []byte, []string or ...string of
// every function is a caller's (an unexported helper's too: it may be handed one). An expression is tainted when it
// holds a tainted name outside a sanitizer: a value's field, element or slice when the value is; a method's result
// when its receiver is; a package function's result when an argument is — for a function of this tree, only when it
// can return its parameters' bytes (a summary computed to a fixed point). A variable assigned a tainted expression is
// tainted, and a range over one. Flow-insensitive, shadowing ignored: it over-approximates.
//
// A selftest runs first: fixtures that must and must not fire, and the round-5 realms audit's realm of thirteen
// constructs that slipped past the taint-only checker, which must give exactly thirteen findings; a census floor fails
// a walk that read too few sinks.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// unbounded is an operand's length when nothing bounds it.
const unbounded = -1

// sanitizers are the calls whose result is ASCII whatever their arguments, with the most bytes each writes (rule 4):
// numbers, hex, EIP-55 addresses, the canonical bech32 spelling, lengths, and echo itself (strconv.QuoteToASCII of at
// most 64 bytes, "... (N bytes)" after: 4×64+2+15).
var sanitizers = map[string]int{
	"echo": 273, "len": 20, "itoa": 20, "u32": 10, "u64s": 20, "hx": unbounded, "ea": 42, "keysHex": unbounded,
	"strconv.Itoa": 20, "strconv.FormatInt": 20, "strconv.FormatUint": 20, "strconv.FormatBool": 5,
	"strconv.QuoteToASCII": unbounded, "hex.EncodeToString": unbounded,
	"EthAddress": 42, "hlmailbox.EthAddress": 42, "CanonicalAddress": 64, "hlmailbox.CanonicalAddress": 64,
	"chain.PackageAddress": 64,
}

// pureConsts are the pure packages' constants a sink may take (the walk reads eth/realm, not realm/p): words, kinds and
// paths, ASCII all.
var pureConsts = map[string]string{
	"decisions.DAOPath": "gno.land/r/gov/dao",
}

// pagers are the pure packages' page writers: a page may take their result when no caller's string reaches them (a
// State's and a Ledger's methods, matched by receiver below, and these).
var pagers = map[string]bool{
	"decisions.GovernorLine": true, "decisions.GovernorJSON": true, "decisions.Clocks": true, "decisions.ClocksJSON": true,
}

// okReason is an exemption: `// echo-ok(name, …): <reason>` on the line of a sink operand (or the line before it)
// answers for the operands it names there — by their root identifier (err of err.Error()) or their dotted name
// (hlmailbox.Predecessor, st.MigratedTo) — and for no other.
var okReason = regexp.MustCompile(`echo-ok\(([A-Za-z0-9_., ]+)\):\s*\S`)

// hlrouteParsers are p/hlroute's functions that echo what they refuse as it came (rule 3).
var hlrouteParsers = map[string]bool{"hlroute.ParseRange": true, "hlroute.ParseIDs": true, "hlroute.ParseFns": true, "hlroute.ParseSeats": true, "hlroute.CleanID": true}

const (
	maxEchoes  = 2
	maxMessage = 1000 // under gno's cut at byte 1,021 (BoundedSprintException's 1,024 less its "...")
)

// sinks are where a message goes.
var sinks = map[string]bool{"panic": true, "errors.New": true, "chain.Emit": true, "fmt.Errorf": true}

// cut are the sinks rule 4 bounds: the ones that become a failed transaction's error.
var cut = map[string]bool{"panic": true, "errors.New": true, "fmt.Errorf": true}

// taintedType is whether a parameter of this type carries bytes its caller chose.
func taintedType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "string" || t.Name == "address" || t.Name == "any"
	case *ast.ArrayType:
		if t.Len != nil {
			return false // [32]byte and the like: fixed bytes
		}
		if id, ok := t.Elt.(*ast.Ident); ok {
			return id.Name == "byte" || id.Name == "string" || id.Name == "address"
		}
	case *ast.Ellipsis:
		return taintedType(t.Elt)
	}
	return false
}

func callName(c *ast.CallExpr) string { return exprName(c.Fun) }

// exprName is pkg.Name or Name for an identifier or a selector of one, "" otherwise.
func exprName(e ast.Expr) string {
	switch f := e.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}

// dotted is an operand's dotted name: st.MigratedTo, hlmailbox.Predecessor of hlmailbox.Predecessor(p), "" when it
// has none.
func dotted(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		if d := dotted(x.X); d != "" {
			return d + "." + x.Sel.Name
		}
	case *ast.CallExpr:
		return dotted(x.Fun)
	}
	return ""
}

// rootIdent is the identifier an operand hangs from (x of x.f.g(…)[i], *x, string(x)), or nil.
func rootIdent(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.SelectorExpr:
			e = x.X
		case *ast.CallExpr:
			e = x.Fun
		case *ast.IndexExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.TypeAssertExpr:
			e = x.X
		case *ast.UnaryExpr:
			e = x.X
		default:
			return nil
		}
	}
}

type finding struct {
	pos  token.Position
	what string
}

type file struct {
	name, pkg string
	f         *ast.File
	imports   map[string]bool         // the names this file's imports go by
	okLines   map[int]map[string]bool // line → the names its echo-ok reason answers for
	states    map[string]bool         // the package's variables holding a p/hlroute State (st = hlroute.New(…)), shared by its files
}

type checker struct {
	fset     *token.FileSet
	local    map[string]map[string]bool   // package → its plain functions
	consts   map[string]map[string]string // package → its constants (the text a string constant holds; a number's digits)
	returns  map[string]bool              // "pkg.Func" → it can return its tainted parameters' bytes
	pages    map[string]bool              // "pkg.Func" → a page function: Render, and every function a page writes
	states   map[string]map[string]bool   // package → its State variables (a package-level var serves every file)
	files    []*file
	findings []finding
	sinks    int
	handoffs int // calls of p/hlroute's echoing parsers and of a State's methods
	marked   int // operands an echo-ok line answers for
}

// scope is one function's analysis.
type scope struct {
	k        *checker
	f        *file
	fd       *ast.FuncDecl
	taint    map[string]bool
	builders map[string]bool // its strings.Builder variables
}

// localFunc is the summary key of a call to a function of this tree, or "".
func (s *scope) localFunc(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		if s.k.local[s.f.pkg][f.Name] {
			return s.f.pkg + "." + f.Name
		}
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok && s.f.imports[x.Name] && s.k.local[x.Name][f.Sel.Name] {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}

// blame lists the tainted names that make e tainted (nil when it is clean).
func (s *scope) blame(e ast.Expr) []*ast.Ident {
	switch x := e.(type) {
	case nil:
		return nil
	case *ast.Ident:
		if s.taint[x.Name] {
			return []*ast.Ident{x}
		}
		return nil
	case *ast.BasicLit, *ast.FuncLit:
		return nil
	case *ast.ParenExpr:
		return s.blame(x.X)
	case *ast.StarExpr:
		return s.blame(x.X)
	case *ast.UnaryExpr:
		if x.Op == token.NOT {
			return nil // a bool
		}
		return s.blame(x.X)
	case *ast.BinaryExpr:
		switch x.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ, token.LAND, token.LOR:
			return nil // a bool carries no bytes
		}
		return append(s.blame(x.X), s.blame(x.Y)...)
	case *ast.SelectorExpr:
		return s.blame(x.X)
	case *ast.IndexExpr:
		return s.blame(x.X)
	case *ast.SliceExpr:
		return s.blame(x.X)
	case *ast.TypeAssertExpr:
		return s.blame(x.X)
	case *ast.KeyValueExpr:
		return s.blame(x.Value)
	case *ast.CompositeLit:
		var out []*ast.Ident
		for _, el := range x.Elts {
			out = append(out, s.blame(el)...)
		}
		return out
	case *ast.CallExpr:
		if _, ok := sanitizers[callName(x)]; ok {
			return nil
		}
		if id, ok := x.Fun.(*ast.Ident); ok && numeric[id.Name] {
			return nil // a conversion to a number carries no bytes
		}
		args := func() []*ast.Ident {
			var out []*ast.Ident
			for _, a := range x.Args {
				out = append(out, s.blame(a)...)
			}
			return out
		}
		if key := s.localFunc(x); key != "" {
			if !s.k.returns[key] {
				return nil // it returns nothing of its parameters' bytes
			}
			return args()
		}
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && s.f.imports[id.Name] {
				return args() // another package's function: tainted by its arguments
			}
			return append(s.blame(sel.X), args()...) // a method: tainted by its receiver or an argument
		}
		return args() // a conversion, a builtin, a closure
	}
	return nil
}

func (s *scope) params(fd *ast.FuncDecl) {
	if fd.Type.Params == nil {
		return
	}
	for _, f := range fd.Type.Params.List {
		if taintedType(f.Type) {
			for _, n := range f.Names {
				s.taint[n.Name] = true
			}
		}
	}
}

// propagate taints, to a fixed point, every variable assigned a tainted expression anywhere in the body (closures
// included), and the variables of a range over one.
func (s *scope) propagate(body *ast.BlockStmt) {
	for changed := true; changed; {
		changed = false
		mark := func(lhs, rhs []ast.Expr) {
			dirty := false
			for _, r := range rhs {
				if len(s.blame(r)) > 0 {
					dirty = true
				}
			}
			if !dirty {
				return
			}
			for _, l := range lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name != "_" && !s.taint[id.Name] {
					s.taint[id.Name] = true
					changed = true
				}
			}
		}
		ast.Inspect(body, func(n ast.Node) bool {
			switch st := n.(type) {
			case *ast.AssignStmt:
				mark(st.Lhs, st.Rhs)
			case *ast.ValueSpec:
				var lhs []ast.Expr
				for _, nm := range st.Names {
					lhs = append(lhs, nm)
				}
				mark(lhs, st.Values)
			case *ast.RangeStmt:
				mark([]ast.Expr{st.Key, st.Value}, []ast.Expr{st.X})
			}
			return true
		})
	}
}

// isBuilderType is strings.Builder (or a pointer to one).
func isBuilderType(e ast.Expr) bool {
	if st, ok := e.(*ast.StarExpr); ok {
		e = st.X
	}
	return exprName(e) == "strings.Builder"
}

// findBuilders are the function's strings.Builder variables: declared so, or made by a composite literal of the type.
func (s *scope) findBuilders(body *ast.BlockStmt) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.ValueSpec:
			if st.Type != nil && isBuilderType(st.Type) {
				for _, nm := range st.Names {
					s.builders[nm.Name] = true
				}
			}
		case *ast.AssignStmt:
			for i, r := range st.Rhs {
				if u, ok := r.(*ast.UnaryExpr); ok {
					r = u.X
				}
				if cl, ok := r.(*ast.CompositeLit); ok && isBuilderType(cl.Type) && i < len(st.Lhs) {
					if id, ok := st.Lhs[i].(*ast.Ident); ok {
						s.builders[id.Name] = true
					}
				}
			}
		}
		return true
	})
}

// numeric are the types a value of carries no caller's bytes.
var numeric = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "byte": true, "rune": true, "bool": true, "float64": true,
}

// numbersOnly is whether every result of a function is a number or a bool.
func numbersOnly(fd *ast.FuncDecl) bool {
	if fd.Type.Results == nil {
		return true
	}
	for _, r := range fd.Type.Results.List {
		if id, ok := r.Type.(*ast.Ident); !ok || !numeric[id.Name] {
			return false
		}
	}
	return true
}

// summarize says whether a function can return its tainted parameters' bytes.
func (k *checker) summarize(f *file, fd *ast.FuncDecl) bool {
	if fd.Body == nil || numbersOnly(fd) {
		return false
	}
	s := &scope{k: k, f: f, fd: fd, taint: map[string]bool{}}
	s.params(fd)
	if len(s.taint) == 0 {
		return false
	}
	s.propagate(fd.Body)
	ret := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false // a closure's return is its own
		}
		if r, ok := n.(*ast.ReturnStmt); ok {
			for _, e := range r.Results {
				if len(s.blame(e)) > 0 {
					ret = true
				}
			}
		}
		return true
	})
	if fd.Type.Results != nil {
		for _, fl := range fd.Type.Results.List {
			for _, n := range fl.Names {
				if s.taint[n.Name] {
					ret = true
				}
			}
		}
	}
	return ret
}

// leaves splits a sink's argument at its + concatenations, parentheses and string conversions.
func leaves(e ast.Expr) []ast.Expr {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return leaves(x.X)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return append(leaves(x.X), leaves(x.Y)...)
		}
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "string" && len(x.Args) == 1 {
			return leaves(x.Args[0])
		}
	}
	return []ast.Expr{e}
}

// constText is the text of a constant operand (a literal's value, a constant of this tree or a pure package's), and
// whether it is one.
func (s *scope) constText(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING || x.Kind == token.CHAR {
			if v, err := strconv.Unquote(x.Value); err == nil {
				return v, true
			}
		}
		return x.Value, true // a number
	case *ast.Ident:
		if v, ok := s.k.consts[s.f.pkg][x.Name]; ok {
			return v, true
		}
		switch x.Name {
		case "true", "false", "nil":
			return x.Name, true
		}
	case *ast.CompositeLit:
		if len(x.Elts) == 0 {
			return "{}", true // T{}: a value with nothing in it (a sentinel)
		}
	case *ast.SelectorExpr:
		if p, ok := x.X.(*ast.Ident); ok && s.f.imports[p.Name] {
			if v, ok := s.k.consts[p.Name][x.Sel.Name]; ok {
				return v, true
			}
			if v, ok := pureConsts[p.Name+"."+x.Sel.Name]; ok {
				return v, true
			}
		}
	}
	return "", false
}

// safeCall says whether a call leaf is a recognized safe form, and its bound.
func (s *scope) safeCall(c *ast.CallExpr, page bool) (bool, int) {
	name := callName(c)
	if b, ok := sanitizers[name]; ok {
		return true, b
	}
	// a State's Amount writes an amount's digits and the route's symbol (p/hlroute)
	if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Amount" {
		if id, ok := sel.X.(*ast.Ident); ok && s.f.states[id.Name] {
			return true, 40
		}
	}
	if !page {
		return false, 0
	}
	clean := true
	for _, a := range c.Args {
		if len(s.blame(a)) > 0 {
			clean = false
		}
	}
	// a page function, checked as a page itself: whatever it is handed when it returns none of its parameters' bytes,
	// and no caller's string when it can (ethWords)
	if key := s.localFunc(c); key != "" && s.k.pages[key] && (clean || !s.k.returns[key]) {
		return true, unbounded
	}
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false, 0
	}
	// its own builder's text, whose writes are sinks here
	if id, ok := sel.X.(*ast.Ident); ok && s.builders[id.Name] && sel.Sel.Name == "String" {
		return true, unbounded
	}
	if !clean {
		return false, 0
	}
	if pagers[name] {
		return true, unbounded
	}
	// a page of the route's State or decision Ledger: st.X(…), st.Ledger.X(…), ledger.X(…)
	if r := rootIdent(sel.X); r != nil && (s.f.states[r.Name] || r.Name == "ledger") {
		return true, unbounded
	}
	return false, 0
}

// checkOperands holds a sink's operands to rule 1, and returns the message's constant text and bound (rule 4).
func (s *scope) checkOperands(args []ast.Expr, what string, start, end int, page bool) (text string, bound int, echoes int) {
	answered := func(name string) bool {
		for l := start - 1; l <= end; l++ {
			if s.f.okLines[l][name] {
				return true
			}
		}
		return false
	}
	for _, a := range args {
		for _, l := range leaves(a) {
			if t, ok := s.constText(l); ok {
				text += t
				if bound != unbounded {
					bound += len(t)
				}
				continue
			}
			if c, ok := l.(*ast.CallExpr); ok {
				if callName(c) == "echo" {
					echoes++
				}
				if safe, b := s.safeCall(c, page); safe {
					if b == unbounded || bound == unbounded {
						bound = unbounded
					} else {
						bound += b
					}
					continue
				}
			}
			r := rootIdent(l)
			if (r != nil && answered(r.Name)) || (dotted(l) != "" && answered(dotted(l))) {
				s.k.marked++
				bound = unbounded
				continue
			}
			name := "an operand"
			if r != nil {
				name = r.Name
			}
			s.k.findings = append(s.k.findings, finding{s.k.fset.Position(l.Pos()), fmt.Sprintf("%s in %s takes %s, no recognized safe form (a literal, a constant, a sanitizer, a page's own): wrap it in echo(), or say why it is safe on the line (// echo-ok(%s): <reason>)%s", what, s.fd.Name.Name, name, name, sinkAt(start))})
		}
	}
	return text, bound, echoes
}

// sinkAt is, with CHECK_ECHOES_SINKS=1, the line a finding's sink starts at (for tooling that adds reasons above it).
func sinkAt(line int) string {
	if os.Getenv("CHECK_ECHOES_SINKS") != "1" {
		return ""
	}
	return " [sink " + strconv.Itoa(line) + "]"
}

func nonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func (k *checker) check(f *file, fd *ast.FuncDecl) {
	if fd.Body == nil {
		return
	}
	s := &scope{k: k, f: f, fd: fd, taint: map[string]bool{}, builders: map[string]bool{}}
	s.params(fd)
	s.propagate(fd.Body)
	s.findBuilders(fd.Body)
	page := k.pages[f.pkg+"."+fd.Name.Name]
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			name := callName(x)
			// a page function's builder writes
			if page {
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && s.builders[id.Name] && strings.HasPrefix(sel.Sel.Name, "Write") {
						k.sinks++
						s.checkOperands(x.Args, "a page write", k.fset.Position(x.Pos()).Line, k.fset.Position(x.End()).Line, true)
					}
				}
			}
			if !sinks[name] {
				return true
			}
			k.sinks++
			args := x.Args
			if name == "chain.Emit" && len(args) > 0 {
				args = args[1:] // the event's type is its first argument; the rest are keys and values
			}
			start, end := k.fset.Position(x.Pos()).Line, k.fset.Position(x.End()).Line
			text, bound, echoes := s.checkOperands(args, name+"(...)", start, end, false)
			if cut[name] {
				if echoes > maxEchoes {
					k.findings = append(k.findings, finding{k.fset.Position(x.Pos()), fmt.Sprintf("%s(...) in %s holds %d echoes: at most %d", name, fd.Name.Name, echoes, maxEchoes)})
				}
				if nonASCII(text) && (bound == unbounded || bound > maxMessage) {
					b := "unbounded"
					if bound != unbounded {
						b = strconv.Itoa(bound) + " bytes"
					}
					k.findings = append(k.findings, finding{k.fset.Position(x.Pos()), fmt.Sprintf("%s(...) in %s holds text outside ASCII and can reach gno's cut at byte 1,021 (%s): write its text in ASCII, or bound every operand under %d bytes", name, fd.Name.Name, b, maxMessage)})
				}
			}
		}
		return true
	})
	// a page function's returns (a closure's return is its own: an Iterate callback's bool)
	if page {
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.ReturnStmt:
				k.sinks++
				s.checkOperands(x.Results, "a page", k.fset.Position(x.Pos()).Line, k.fset.Position(x.End()).Line, true)
			}
			return true
		})
	}
	// rule 2: a sink taken as a value, not called
	called := map[ast.Expr]bool{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			called[c.Fun] = true
		}
		return true
	})
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		if name := exprName(e); name != "" && name != "panic" && sinks[name] && !called[e] {
			if _, isSel := e.(*ast.SelectorExpr); isSel {
				k.findings = append(k.findings, finding{k.fset.Position(e.Pos()), fmt.Sprintf("%s in %s is taken as a value: its calls are sinks the checker cannot see; call it directly", name, fd.Name.Name)})
				return false
			}
		}
		return true
	})
	// rule 3: a caller's string reaches p/hlroute's echoing parsers and a State's methods only after mustBeASCII (in
	// the same function), or on a line whose echo-ok names it
	guarded := map[string]bool{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && callName(c) == "mustBeASCII" && len(c.Args) == 2 {
			a := c.Args[1]
			if cv, ok := a.(*ast.CallExpr); ok && callName(cv) == "string" && len(cv.Args) == 1 {
				a = cv.Args[0]
			}
			if id, ok := a.(*ast.Ident); ok {
				guarded[id.Name] = true
			}
		}
		return true
	})
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		target := ""
		if hlrouteParsers[callName(c)] && f.imports["hlroute"] {
			target = callName(c)
		} else if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && f.states[x.Name] {
				target = x.Name + "." + sel.Sel.Name
			}
		}
		if target == "" {
			return true
		}
		k.handoffs++
		line := k.fset.Position(c.Pos()).Line
		for _, a := range c.Args {
			for _, id := range s.blame(a) {
				if guarded[id.Name] || f.okLines[line][id.Name] || f.okLines[line-1][id.Name] {
					continue
				}
				k.findings = append(k.findings, finding{k.fset.Position(id.Pos()), fmt.Sprintf("%s hands %s a caller's string, %s, before mustBeASCII: p/hlroute echoes what it refuses as it came (or say on the line why it cannot: // echo-ok(%s): <reason>)", fd.Name.Name, target, id.Name, id.Name)})
			}
		}
		return true
	})
}

// constValue is a constant's text when the walk can read it: a string literal, a concatenation of them, a number's
// digits; "" with false otherwise.
func (k *checker) constValue(pkg string, e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			v, err := strconv.Unquote(x.Value)
			return v, err == nil
		}
		return x.Value, true
	case *ast.ParenExpr:
		return k.constValue(pkg, x.X)
	case *ast.BinaryExpr:
		l, ok1 := k.constValue(pkg, x.X)
		r, ok2 := k.constValue(pkg, x.Y)
		if !ok1 || !ok2 {
			return "", false
		}
		if x.Op == token.ADD {
			return l + r, true
		}
		return "0", true // arithmetic: digits
	case *ast.Ident:
		v, ok := k.consts[pkg][x.Name]
		return v, ok
	case *ast.CallExpr:
		if len(x.Args) == 1 {
			return k.constValue(pkg, x.Args[0]) // a typed constant: address("g1…"), int64(1)
		}
	}
	return "", false
}

// scan parses every file, computes the summaries and the page functions to a fixed point, then checks every function.
func (k *checker) scan(sources map[string]string) error {
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		af, err := parser.ParseFile(k.fset, name, sources[name], parser.ParseComments)
		if err != nil {
			return err
		}
		if k.states[af.Name.Name] == nil {
			k.states[af.Name.Name] = map[string]bool{}
		}
		f := &file{name: name, pkg: af.Name.Name, f: af, imports: map[string]bool{}, okLines: map[int]map[string]bool{}, states: k.states[af.Name.Name]}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			n := path.Base(p)
			if strings.HasPrefix(n, "v") && len(n) > 1 && n[1] >= '0' && n[1] <= '9' {
				n = path.Base(path.Dir(p)) // gno.land/p/…/hlroute/v0 goes by hlroute
			}
			if im.Name != nil {
				n = im.Name.Name
			}
			f.imports[n] = true
		}
		if k.local[f.pkg] == nil {
			k.local[f.pkg] = map[string]bool{}
			k.consts[f.pkg] = map[string]string{}
		}
		for _, d := range af.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				k.local[f.pkg][fd.Name.Name] = true
			}
		}
		for _, cg := range af.Comments {
			for _, cm := range cg.List {
				m := okReason.FindStringSubmatch(cm.Text)
				if m == nil {
					continue
				}
				line := k.fset.Position(cm.Pos()).Line
				if f.okLines[line] == nil {
					f.okLines[line] = map[string]bool{}
				}
				for _, n := range strings.Split(m[1], ",") {
					f.okLines[line][strings.TrimSpace(n)] = true
				}
			}
		}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, v := range vs.Values {
					if c, ok := v.(*ast.CallExpr); ok && callName(c) == "hlroute.New" && i < len(vs.Names) {
						f.states[vs.Names[i].Name] = true
					}
				}
			}
		}
		k.files = append(k.files, f)
	}
	// the constants, to a fixed point (one may be built from another)
	for changed := true; changed; {
		changed = false
		for _, f := range k.files {
			for _, d := range f.f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, nm := range vs.Names {
						if _, done := k.consts[f.pkg][nm.Name]; done {
							continue
						}
						v := "0" // an iota or an untyped number follows the spec before: digits
						if i < len(vs.Values) {
							var ok bool
							if v, ok = k.constValue(f.pkg, vs.Values[i]); !ok {
								continue
							}
						}
						k.consts[f.pkg][nm.Name] = v
						changed = true
					}
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, f := range k.files {
			for _, d := range f.f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv != nil {
					continue
				}
				key := f.pkg + "." + fd.Name.Name
				if !k.returns[key] && k.summarize(f, fd) {
					k.returns[key] = true
					changed = true
				}
			}
		}
	}
	// the page functions: Render, and every function of the tree a page's return or builder write calls
	for _, f := range k.files {
		for _, d := range f.f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "Render" {
				k.pages[f.pkg+".Render"] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, f := range k.files {
			for _, d := range f.f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv != nil || fd.Body == nil || !k.pages[f.pkg+"."+fd.Name.Name] {
					continue
				}
				s := &scope{k: k, f: f, fd: fd, taint: map[string]bool{}, builders: map[string]bool{}}
				s.findBuilders(fd.Body)
				mark := func(e ast.Expr) {
					for _, l := range leaves(e) {
						if c, ok := l.(*ast.CallExpr); ok {
							if _, ok := sanitizers[callName(c)]; ok {
								continue // a sanitizer is safe by its name, not a page
							}
							if key := s.localFunc(c); key != "" && !k.pages[key] {
								k.pages[key] = true
								changed = true
							}
						}
					}
				}
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.ReturnStmt:
						for _, r := range x.Results {
							mark(r)
						}
					case *ast.CallExpr:
						if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
							if id, ok := sel.X.(*ast.Ident); ok && s.builders[id.Name] && strings.HasPrefix(sel.Sel.Name, "Write") {
								for _, a := range x.Args {
									mark(a)
								}
							}
						}
					}
					return true
				})
			}
		}
	}
	for _, f := range k.files {
		for _, d := range f.f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				k.check(f, fd)
			}
		}
	}
	return nil
}

func newChecker() *checker {
	return &checker{fset: token.NewFileSet(), local: map[string]map[string]bool{}, consts: map[string]map[string]string{}, returns: map[string]bool{}, pages: map[string]bool{}, states: map[string]map[string]bool{}}
}

// The fixtures run first: a rule that stopped reading what it must, or started reading what it must not, fails
// before the scan it would have made meaningless (check-interrealm.py's discipline).
// mustFireFiles are fixtures of two files: a State declared in one is a State in the other.
var mustFireFiles = []map[string]string{
	{"a.gno": "package p\nimport \"gno.land/p/x/hlroute/v0\"\nvar st = hlroute.New(hlroute.Launch{})\n",
		"b.gno": "package p\nfunc F(cur realm, id string) { st.ClaimOf(id) }\n"},
}

var mustFire = []string{
	`func F(cur realm, s string) { panic("x: " + s) }`,
	`func F(cur realm, s string) { p := strings.TrimSpace(s); panic("x: " + p) }`,
	`func F(s string) error { return errors.New("bad " + s) }`,
	`func F(cur realm, a address) { chain.Emit("E", "a", string(a)) }`,
	`func F(cur realm, s string) { for _, p := range strings.Split(s, ",") { panic("x " + p[:2]) } }`,
	`func F(cur realm, s string) { _, err := hlroute.CleanID(s); if err != nil { panic(err.Error()) } }`,
	`func F(cur realm, s string) { cb := func() { panic("late " + s) }; cb() }`,
	`func F(cur realm, s string) { if x := strings.ToLower(s); x != "" { panic(x) } }`,
	`func F(cur realm, s string) { panic("n " + strconv.Itoa(len(s)) + " of " + s) }`,
	`func H(s string) string { return strings.TrimSpace(s) }
func F(cur realm, s string) { panic("x " + H(s)) }`,
	`func G(s string) error { return errors.New("bad " + echo(s)) }
func F(cur realm, s string) { if err := G(s); err != nil { panic(err.Error()) } }`,
	`func F(cur realm, s, t string) { panic("x: " + s + t) // echo-ok(t): only t is answered for
}`,
	`func F(cur realm, s string) { hlroute.ParseIDs(s) }`,
	`var st = hlroute.New(hlroute.Launch{})
func F(cur realm, ids string) { st.DraftRestore(ids) }`,
	`var st = hlroute.New(hlroute.Launch{})
func F(cur realm, id string) { st.ClaimOf(id) }`,
	`func F(cur realm, a, b, c string) { panic(echo(a) + echo(b) + echo(c)) }`,
	`func F(cur realm, n int) { panic("— " + strconv.QuoteToASCII(strings.Repeat("x", n)) + " —") }`,
	`func F(cur realm, s string) { v := tree.Get(s); panic("stored " + v.(string)) }`,
	`func F(cur realm, s string) { panic("q " + strconv.Quote(s)) }`,
	`func Render(path string) string { return "# " + path }`,
	`func Render(path string) string { var b strings.Builder; b.WriteString(path); return b.String() }`,
	`func Render(path string) string { return page(path) }
func page(p string) string { return "x" + p }`,
}

var mustNotFire = []string{
	`func F(cur realm, s string) { panic("x: " + echo(s)) }`,
	`func F(cur realm, s string) { panic("x: " + strconv.Itoa(len(s))) }`,
	`func F(cur realm, n int) { panic("fixed " + strconv.Itoa(n)) }`,
	`func F(cur realm, s string) { panic("x: " + s) // echo-ok(s): a test of the reason
}`,
	`var st = hlroute.New(hlroute.Launch{})
func F(cur realm, ids string) { mustBeASCII("ids", ids); st.DraftRestore(ids) }`,
	`func F(cur realm, to address) { mustBeASCII("to", string(to)); hlroute.CleanID(string(to)) }`,
	`func F(cur realm, k [32]byte) { panic("k " + hx(k[:])) }`,
	`const word = "sends"
func F(cur realm) { panic("the " + word + " are paused — " + strconv.Itoa(3)) }`,
	`func F(cur realm) { chain.Emit("E", "kind", decisions.DAOPath, "n", strconv.FormatBool(true)) }`,
	`var st = hlroute.New(hlroute.Launch{})
func Render(path string) string {
	if path == "json" { return "{" + strconv.QuoteToASCII(string(chain.PackageAddress("x"))) + "," + st.JSON(1, 2) + "}" }
	var b strings.Builder
	b.WriteString("# page\n" + st.Figures(1) + itoa(2))
	return b.String() + page()
}
func page() string { return "- " + strconv.Itoa(1) }`,
}

// blindRealm is the round-5 realms audit's fixture (E5-RL3): thirteen constructs that put a caller's bytes, or a message
// past the cut, into a panic or an error, each of which the taint-only checker passed. Each must be one finding.
const blindRealm = `package x

import (
	"errors"
	"strconv"
	"strings"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
)

var st = hlroute.New(hlroute.Launch{})

type T struct{ x string }

type W struct{}

func (W) wrap(s string) string { return "bad " + s }

var stored string

func B1(cur realm, s string) {
	var b strings.Builder
	b.WriteString(s)
	panic("b1: " + b.String())
}

func B2(cur realm, s string) {
	var t T
	t.x = s
	panic("b2: " + t.x)
}

func B3(cur realm, s string) {
	m := map[string]string{}
	m["k"] = s
	panic("b3: " + m["k"])
}

func B4(cur realm, s string) { panic(W{}.wrap(s)) }

func B5(cur realm, k [32]byte) { panic("b5: " + string(k[:])) }

func B6(cur realm, s string) {
	b := make([]byte, len(s))
	copy(b, s)
	panic("b6: " + string(b))
}

func B7(cur realm, s string) {
	p := new(string)
	*p = s
	panic("b7: " + *p)
}

func B8(cur realm, s string) {
	f := func() string { return s }
	panic("b8: " + f())
}

func B9(cur realm, s string) { panic("b9: " + strconv.Quote(s)) }

func B10(cur realm, id string) { st.ClaimOf(id) }

func B11set(cur realm, s string) { stored = s }
func B11(cur realm)              { panic("b11: " + stored) }

func B12(cur realm, s string) {
	msg := strings.Repeat("— ", 300) + echo(s) + strings.Repeat("— ", 300)
	panic(msg)
}

func B13(cur realm, s string) error {
	mk := errors.New
	return mk("b13: " + s)
}

func echo(s string) string { return strconv.QuoteToASCII(s) }
`

func selftest() error {
	const head = "package p\nimport (\"errors\"\n\"strings\"\n\"strconv\"\n\"chain\"\n\"gno.land/p/x/decisions/v0\"\n\"gno.land/p/x/hlroute/v0\")\n"
	for i, src := range mustFire {
		k := newChecker()
		if err := k.scan(map[string]string{"fire.gno": head + src + "\n"}); err != nil {
			return fmt.Errorf("fixture %d does not parse: %v", i, err)
		}
		if len(k.findings) == 0 {
			return fmt.Errorf("SELFTEST: no longer flags %q; the checker would stop noticing what it exists for", src)
		}
	}
	for i, src := range mustNotFire {
		k := newChecker()
		if err := k.scan(map[string]string{"quiet.gno": head + src + "\n"}); err != nil {
			return fmt.Errorf("fixture %d does not parse: %v", i, err)
		}
		if len(k.findings) != 0 {
			return fmt.Errorf("SELFTEST: flags %q, which is safe (%s); a checker that cries wolf gets switched off", src, k.findings[0].what)
		}
	}
	for i, files := range mustFireFiles {
		k := newChecker()
		if err := k.scan(files); err != nil {
			return fmt.Errorf("two-file fixture %d does not parse: %v", i, err)
		}
		if len(k.findings) == 0 {
			return fmt.Errorf("SELFTEST: no longer flags a State of another file handed a caller's string (%v)", files)
		}
	}
	k := newChecker()
	if err := k.scan(map[string]string{"blind.gno": blindRealm}); err != nil {
		return fmt.Errorf("the blind realm does not parse: %v", err)
	}
	if len(k.findings) != 13 {
		var w []string
		for _, f := range k.findings {
			w = append(w, fmt.Sprintf("%d: %s", f.pos.Line, f.what))
		}
		return fmt.Errorf("SELFTEST: the round-5 audit's realm of thirteen blind constructs gives %d findings, not 13:\n  %s", len(k.findings), strings.Join(w, "\n  "))
	}
	return nil
}

// censusFloor is the fewest sinks the tree holds: a checker that reads far fewer reads nothing.
const censusFloor = 600

func main() {
	dir := "eth/realm"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := selftest(); err != nil {
		fmt.Fprintln(os.Stderr, "check-echoes:", err)
		os.Exit(2)
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*", "*.gno"))
	sources := map[string]string{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.gno") || strings.HasSuffix(p, "_filetest.gno") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, "check-echoes:", err)
			os.Exit(2)
		}
		sources[p] = string(b)
	}
	k := newChecker()
	if err := k.scan(sources); err != nil {
		fmt.Fprintln(os.Stderr, "check-echoes:", err)
		os.Exit(2)
	}
	sort.Slice(k.findings, func(i, j int) bool {
		a, b := k.findings[i].pos, k.findings[j].pos
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		return a.Line < b.Line
	})
	for _, f := range k.findings {
		fmt.Printf("%s:%d: %s\n", f.pos.Filename, f.pos.Line, f.what)
	}
	pages := 0
	for range k.pages {
		pages++
	}
	fmt.Printf("check-echoes: %d files, %d sinks (panic, errors.New, chain.Emit, page writes in %d page functions), %d hand-offs to p/hlroute, %d operands answered by echo-ok reasons, %d findings\n", len(sources), k.sinks, pages, k.handoffs, k.marked, len(k.findings))
	if k.sinks < censusFloor {
		fmt.Fprintf(os.Stderr, "check-echoes: only %d sinks read in %s (the floor is %d): the walk is blind\n", k.sinks, dir, censusFloor)
		os.Exit(2)
	}
	if len(k.findings) > 0 {
		os.Exit(1)
	}
}
