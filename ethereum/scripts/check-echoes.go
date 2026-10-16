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
	"go/printer"
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

// taintedType is whether a parameter of this type carries bytes its caller chose: a string, an address, any value, an
// interface; bytes, a fixed array of them included (a MsgRun hands any value: [32]byte is 32 bytes of its choosing),
// slices, maps and pointers of these; and a type this tree declares (its package's or another realm's of the walk)
// whose underlying type is one — a named string type, a struct with such a field (E6-RL2: a named string type and a
// [32]byte were no caller's bytes to rule 3). A pure package's type (hlroute.Range, …) is read as carrying none.
func (k *checker) taintedType(f *file, pkg string, e ast.Expr, depth int) bool {
	if depth > 8 {
		return true // a type that refers to itself: read as tainted
	}
	switch t := e.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string", "address", "any":
			return true
		}
		if numeric[t.Name] || t.Name == "realm" || t.Name == "error" {
			return false
		}
		if te, ok := k.types[pkg][t.Name]; ok {
			return k.taintedType(f, pkg, te, depth+1)
		}
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			p := x.Name
			if c, ok := f.importRef(x); ok && depth == 0 {
				p = c
			}
			if te, ok := k.types[p][t.Sel.Name]; ok {
				return k.taintedType(f, p, te, depth+1)
			}
		}
	case *ast.ArrayType:
		if id, ok := t.Elt.(*ast.Ident); ok && (id.Name == "byte" || id.Name == "uint8") {
			return true // bytes: a fixed array of them too
		}
		return k.taintedType(f, pkg, t.Elt, depth+1)
	case *ast.Ellipsis:
		return k.taintedType(f, pkg, t.Elt, depth+1)
	case *ast.StarExpr:
		return k.taintedType(f, pkg, t.X, depth+1)
	case *ast.MapType:
		return k.taintedType(f, pkg, t.Key, depth+1) || k.taintedType(f, pkg, t.Value, depth+1)
	case *ast.InterfaceType:
		return true
	case *ast.StructType:
		for _, fl := range t.Fields.List {
			if k.taintedType(f, pkg, fl.Type, depth+1) {
				return true
			}
		}
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

// deployer is the namespace of the deployment's own packages: its pure ones, gno.land/p/<deployer>/<name>/vN, and this
// tree's realms, gno.land/r/<deployer>/eth/<name>.
const deployer = "g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr"

func isVersion(s string) bool { return len(s) > 1 && s[0] == 'v' && s[1] >= '0' && s[1] <= '9' }

// canonicalImport is the name the checker's tables know an import path by: a standard library package's last element
// (a path whose first element has no dot: encoding/hex is hex), the deployment's own packages by their name, and any
// other path by itself, so that no table entry matches it whatever it is imported as (the round-6 realms audit's
// E6-RL2: the tables matched an import's spelling, so `ch "chain"` made ch.Emit no sink).
func canonicalImport(p string) string {
	if !strings.Contains(strings.SplitN(p, "/", 2)[0], ".") {
		return path.Base(p)
	}
	if rest, ok := strings.CutPrefix(p, "gno.land/p/"+deployer+"/"); ok {
		if el := strings.Split(rest, "/"); len(el) == 2 && isVersion(el[1]) {
			return el[0]
		}
	}
	if rest, ok := strings.CutPrefix(p, "gno.land/r/"+deployer+"/eth/"); ok && !strings.Contains(rest, "/") {
		return rest
	}
	return p
}

// qual is an expression's name as the tables know it: a name, or pkg.Name where pkg is an import of the file (an
// identifier no object of the file declares: go/parser leaves imports unresolved) by its canonical name; "" otherwise.
func (f *file) qual(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && id.Obj == nil {
			if c, ok := f.alias[id.Name]; ok {
				return c + "." + x.Sel.Name
			}
		}
	}
	return ""
}

// call is a call's name as the tables know it (qual).
func (f *file) call(c *ast.CallExpr) string { return f.qual(c.Fun) }

// importRef is whether id names an import of the file (and the import's canonical name).
func (f *file) importRef(id *ast.Ident) (string, bool) {
	if id.Obj != nil {
		return "", false
	}
	c, ok := f.alias[id.Name]
	return c, ok
}

type finding struct {
	pos  token.Position
	what string
}

type file struct {
	name, pkg string
	f         *ast.File
	imports   map[string]bool         // the names this file's imports go by
	alias     map[string]string       // an import's name here → the name the checker's tables know its package by (canonicalImport)
	okLines   map[int]map[string]bool // line → the names its echo-ok reason answers for
	okASCII   map[int]bool            // line → its echo-ok reason says its operands are ASCII (rule 4)
	states    map[string]bool         // the package's variables holding a p/hlroute State (st = hlroute.New(…)), shared by its files
}

type checker struct {
	fset     *token.FileSet
	local    map[string]map[string]bool     // package → its plain functions
	funcs    map[string]*ast.FuncDecl       // "pkg.Func" → its declaration (a plain function's)
	consts   map[string]map[string]string   // package → its constants (the text a string constant holds; a number's digits)
	types    map[string]map[string]ast.Expr // package → its declared types' type expressions (taintedType)
	methods  map[string]map[string]bool     // package → the types declared there that have a method (a T{} of one is no constant)
	trusted  map[string]map[string]bool     // package → its sanitizers and guards whose source is the pinned one (pinned)
	returns  map[string]bool                // "pkg.Func" → it can return its tainted parameters' bytes
	retClass map[string]partClass           // "pkg.Func" → what its string results can hold (rule 4)
	errClass map[string]partClass           // "pkg.Func" → what the errors it returns can say (rule 4)
	pages    map[string]bool                // "pkg.Func" → a page function: Render, and every function a page writes
	states   map[string]map[string]bool     // package → its State variables (a package-level var serves every file)
	files    []*file
	findings []finding
	sinks    int
	handoffs int // calls of p/hlroute's echoing parsers and of a State's methods
	marked   int // operands an echo-ok line answers for
}

// class is what a message part can hold (rule 4): whether every byte is ASCII, and the most bytes (unbounded: no bound).
type class struct {
	ascii bool
	bound int
}

// join is the class of two parts written one after the other.
func (c class) join(d class) class {
	b := unbounded
	if c.bound != unbounded && d.bound != unbounded {
		b = c.bound + d.bound
	}
	return class{c.ascii && d.ascii, b}
}

// or is the class of a value that is one or the other.
func (c class) or(d class) class {
	b := c.bound
	if b != unbounded && (d.bound == unbounded || d.bound > b) {
		b = d.bound
	}
	return class{c.ascii && d.ascii, b}
}

// unknownClass is what a part the checker cannot read may hold: anything.
var unknownClass = class{false, unbounded}

// partClass is what the tree's own source says a value can hold, and whether it says it all (complete is false when a
// part comes from outside the source the checker reads: a parameter, another package's result).
type partClass struct {
	c        class
	complete bool
}

// scope is one function's analysis — a FuncDecl's, or a package-level declaration's (its values, closures included).
type scope struct {
	k           *checker
	f           *file
	name        string        // the function's name, or "var <names>" for a package-level declaration
	ftype       *ast.FuncType // nil for a package-level declaration
	body        ast.Node
	taint       map[string]bool
	builders    map[string]bool      // its strings.Builder variables
	builderObjs map[*ast.Object]bool // and their declarations' objects (builderUses)
}

// localFunc is the summary key of a call to a function of this tree, or "".
func (s *scope) localFunc(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		if (f.Obj == nil || f.Obj.Kind == ast.Fun) && s.k.local[s.f.pkg][f.Name] {
			return s.f.pkg + "." + f.Name
		}
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			if pc, ok := s.f.importRef(x); ok && s.k.local[pc][f.Sel.Name] {
				return pc + "." + f.Sel.Name
			}
		}
	}
	return ""
}

// sanitizer is whether a call is a sanitizer's, and its bound: a standard library one by its canonical name, the
// tree's own (echo, itoa, hlmailbox.EthAddress, …) only where its source is the pinned one (pinned: the round-6
// realms audit's E6-RL2 — a sanitizer was trusted by its name, so an echo that returns its argument passed).
func (s *scope) sanitizer(c *ast.CallExpr) (int, bool) {
	name := s.f.call(c)
	b, ok := sanitizers[name]
	if !ok {
		return 0, false
	}
	pkg, fn := s.f.pkg, name
	if i := strings.Index(name, "."); i >= 0 {
		pkg, fn = name[:i], name[i+1:]
		if s.k.local[pkg] == nil {
			return b, true // a standard library function's (canonicalImport)
		}
	} else if id, ok := c.Fun.(*ast.Ident); ok && id.Obj != nil && id.Obj.Kind != ast.Fun {
		return 0, false // a local or a parameter of that name
	}
	return b, s.k.trusted[pkg][fn]
}

// guard is whether a call is the trusted mustBeASCII of rule 3 (pinned, as the sanitizers are).
func (s *scope) guard(c *ast.CallExpr) bool {
	id, ok := c.Fun.(*ast.Ident)
	return ok && id.Name == "mustBeASCII" && (id.Obj == nil || id.Obj.Kind == ast.Fun) && s.k.trusted[s.f.pkg]["mustBeASCII"] && len(c.Args) == 2
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
		if _, ok := s.sanitizer(x); ok {
			return nil
		}
		if id, ok := x.Fun.(*ast.Ident); ok && numeric[id.Name] && id.Obj == nil {
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
			if id, ok := sel.X.(*ast.Ident); ok {
				if _, isImport := s.f.importRef(id); isImport {
					return args() // another package's function: tainted by its arguments
				}
			}
			return append(s.blame(sel.X), args()...) // a method: tainted by its receiver or an argument
		}
		return append(s.blame(x.Fun), args()...) // a conversion, a builtin, a closure (a tainted func value too)
	}
	return nil
}

// params taints the function's parameters a caller's bytes can fill, and those of every closure in its body that is
// held as a value — a variable's, a package-level one's — since whoever calls it may hand it anything (E6-RL2); a
// closure handed straight to a call (an avl tree's Iterate callback, eachFlag's) takes what that call hands it, the
// realm's own records, read as any read of the realm's state is.
func (s *scope) params() {
	lists := []*ast.FieldList{}
	if s.ftype != nil {
		lists = append(lists, s.ftype.Params)
	}
	if s.body != nil {
		handed := map[*ast.FuncLit]bool{}
		ast.Inspect(s.body, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				for _, a := range c.Args {
					if fl, ok := a.(*ast.FuncLit); ok {
						handed[fl] = true
					}
				}
			}
			return true
		})
		ast.Inspect(s.body, func(n ast.Node) bool {
			if fl, ok := n.(*ast.FuncLit); ok && !handed[fl] {
				lists = append(lists, fl.Type.Params)
			}
			return true
		})
	}
	for _, l := range lists {
		if l == nil {
			continue
		}
		for _, f := range l.List {
			if s.k.taintedType(s.f, s.f.pkg, f.Type, 0) {
				for _, n := range f.Names {
					s.taint[n.Name] = true
				}
			}
		}
	}
}

// propagate taints, to a fixed point, every variable assigned a tainted expression anywhere in the body (closures
// included), and the variables of a range over one.
func (s *scope) propagate(body ast.Node) {
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
func (f *file) isBuilderType(e ast.Expr) bool {
	if st, ok := e.(*ast.StarExpr); ok {
		e = st.X
	}
	return f.qual(e) == "strings.Builder"
}

// findBuilders are the function's strings.Builder variables: declared so, or made by a composite literal of the type
// (a parameter of the type is no builder of the function's: builderUses refuses handing one over).
func (s *scope) findBuilders(body ast.Node) {
	s.builderObjs = map[*ast.Object]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.ValueSpec:
			if st.Type != nil && s.f.isBuilderType(st.Type) {
				for _, nm := range st.Names {
					s.builders[nm.Name] = true
					if nm.Obj != nil {
						s.builderObjs[nm.Obj] = true
					}
				}
			}
		case *ast.AssignStmt:
			for i, r := range st.Rhs {
				if u, ok := r.(*ast.UnaryExpr); ok {
					r = u.X
				}
				if cl, ok := r.(*ast.CompositeLit); ok && s.f.isBuilderType(cl.Type) && i < len(st.Lhs) {
					if id, ok := st.Lhs[i].(*ast.Ident); ok {
						s.builders[id.Name] = true
						if id.Obj != nil {
							s.builderObjs[id.Obj] = true
						}
					}
				}
			}
		}
		return true
	})
}

// builderUses holds the function's builders to their methods: a builder whose address is taken, that is handed to a
// call, stored or copied is a finding, since a write made through the other name would be a sink the checker cannot
// see (the round-6 realms audit's E6-RL2: a page's builder written through p := &b passed, its text then trusted as
// the builder's own).
func (s *scope) builderUses() {
	if len(s.builderObjs) == 0 {
		return
	}
	ok := map[*ast.Ident]bool{}
	ast.Inspect(s.body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, isSel := x.Fun.(*ast.SelectorExpr); isSel {
				if id, isID := sel.X.(*ast.Ident); isID {
					ok[id] = true // a method call on it
				}
			}
		case *ast.ValueSpec:
			for _, nm := range x.Names {
				ok[nm] = true
			}
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, l := range x.Lhs {
					if id, isID := l.(*ast.Ident); isID {
						ok[id] = true
					}
				}
			}
		}
		return true
	})
	ast.Inspect(s.body, func(n ast.Node) bool {
		if id, isID := n.(*ast.Ident); isID && id.Obj != nil && s.builderObjs[id.Obj] && !ok[id] {
			s.k.findings = append(s.k.findings, finding{s.k.fset.Position(id.Pos()), fmt.Sprintf("the strings.Builder %s in %s is used other than by calling its methods (its address taken, handed over, stored or copied): a write through another name would be a sink the checker cannot see", id.Name, s.name)})
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
	s := &scope{k: k, f: f, name: fd.Name.Name, ftype: fd.Type, body: fd.Body, taint: map[string]bool{}}
	s.params()
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
		if x.Obj != nil {
			// declared in this file: a constant only if a const declares it — a parameter, a local or a function of the
			// name is none, whatever the package's constant of that name holds (E6-RL2: a parameter or a local shadowing
			// a constant read as the constant)
			if x.Obj.Kind != ast.Con {
				return "", false
			}
			if vs, ok := x.Obj.Decl.(*ast.ValueSpec); ok {
				for i, nm := range vs.Names {
					if nm.Name == x.Name {
						if i < len(vs.Values) {
							return s.k.constValue(s.f.pkg, vs.Values[i])
						}
						return "0", true // an iota's digits
					}
				}
			}
			return "", false
		}
		if v, ok := s.k.consts[s.f.pkg][x.Name]; ok {
			return v, true
		}
		switch x.Name {
		case "true", "false", "nil":
			return x.Name, true
		}
	case *ast.CompositeLit:
		// T{}: a value with nothing in it (a sentinel) — of a type of this package with no method, since gno prints a
		// panic's value through its Error or String method (E6-RL2: panic(E{}) with an Error method passed)
		if len(x.Elts) == 0 {
			switch t := x.Type.(type) {
			case *ast.Ident:
				if _, declared := s.k.types[s.f.pkg][t.Name]; declared && !s.k.methods[s.f.pkg][t.Name] {
					return "{}", true
				}
			case *ast.StructType:
				return "{}", true // struct{}{}
			}
		}
	case *ast.SelectorExpr:
		if p, ok := x.X.(*ast.Ident); ok {
			if pc, ok := s.f.importRef(p); ok {
				if v, ok := s.k.consts[pc][x.Sel.Name]; ok {
					return v, true
				}
				if v, ok := pureConsts[pc+"."+x.Sel.Name]; ok {
					return v, true
				}
			}
		}
	}
	return "", false
}

// safeCall says whether a call leaf is a recognized safe form, and its bound.
func (s *scope) safeCall(c *ast.CallExpr, page bool) (bool, int) {
	name := s.f.call(c)
	if b, ok := s.sanitizer(c); ok {
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

// checkOperands holds a sink's operands to rule 1, and returns the message's class — whether every part is ASCII, and
// its bound (rule 4) — and its echoes.
func (s *scope) checkOperands(args []ast.Expr, what string, start, end int, page bool) (cl class, echoes int) {
	answered := func(name string) bool {
		for l := start - 1; l <= end; l++ {
			if s.f.okLines[l][name] {
				return true
			}
		}
		return false
	}
	cl = class{true, 0}
	for _, a := range args {
		for _, l := range leaves(a) {
			if t, ok := s.constText(l); ok {
				cl = cl.join(class{!nonASCII(t), len(t)})
				continue
			}
			if c, ok := l.(*ast.CallExpr); ok {
				if _, trusted := s.sanitizer(c); trusted && s.f.call(c) == "echo" {
					echoes++
				}
				if safe, b := s.safeCall(c, page); safe {
					cl = cl.join(class{true, b}) // a sanitizer's text, or a page's own (no page is a cut sink)
					continue
				}
			}
			r := rootIdent(l)
			if (r != nil && answered(r.Name)) || (dotted(l) != "" && answered(dotted(l))) {
				s.k.marked++
				cl = cl.join(s.reasonClass(l, start, end))
				continue
			}
			name := "an operand"
			if r != nil {
				name = r.Name
			}
			s.k.findings = append(s.k.findings, finding{s.k.fset.Position(l.Pos()), fmt.Sprintf("%s in %s takes %s, no recognized safe form (a literal, a constant, a sanitizer, a page's own): wrap it in echo(), or say why it is safe on the line (// echo-ok(%s): <reason>)%s", what, s.name, name, name, sinkAt(start))})
		}
	}
	return cl, echoes
}

// reasonClass is what an operand an echo-ok reason answers for may hold in a panic or an error (rule 4): what the
// tree's own source says, where the operand's value comes from it (valueClass), and for the rest ASCII of any length
// when the reason says "ASCII", anything when it does not (the round-6 realms audit's E6-RL2: rule 4 took every
// reason's operand for ASCII, and ParseEthAddress's refusal, with two em dashes, was not).
func (s *scope) reasonClass(l ast.Expr, start, end int) class {
	rest := unknownClass
	for ln := start - 1; ln <= end; ln++ {
		if s.f.okASCII[ln] {
			rest = class{true, unbounded}
		}
	}
	pc := s.valueClass(l, 0)
	if pc.complete {
		return pc.c
	}
	return pc.c.or(rest)
}

// valueClass is what an expression of the function can hold, read from the tree's source: a constant's text, a
// sanitizer's ASCII, a function of the tree's string result (retClass), the errors a variable was assigned (X.Error():
// errClass of the call, or the message of an errors.New or fmt.Errorf), a variable by every value assigned it.
func (s *scope) valueClass(e ast.Expr, depth int) partClass {
	pc := partClass{class{true, 0}, true}
	for _, l := range leaves(e) {
		lc := s.leafClass(l, depth)
		pc.c, pc.complete = pc.c.join(lc.c), pc.complete && lc.complete
	}
	return pc
}

func (s *scope) leafClass(l ast.Expr, depth int) partClass {
	if depth > 6 {
		return partClass{class{true, 0}, false}
	}
	if t, ok := s.constText(l); ok {
		return partClass{class{!nonASCII(t), len(t)}, true}
	}
	switch x := l.(type) {
	case *ast.ParenExpr:
		return s.valueClass(x.X, depth+1)
	case *ast.CallExpr:
		if b, ok := s.sanitizer(x); ok {
			return partClass{class{true, b}, true}
		}
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && len(x.Args) == 0 {
			if id, ok := sel.X.(*ast.Ident); ok {
				return s.varClass(id, depth+1, true)
			}
		}
		if key := s.localFunc(x); key != "" {
			if pc, ok := s.k.retClass[key]; ok {
				return pc
			}
		}
	case *ast.Ident:
		return s.varClass(x, depth+1, false)
	}
	return partClass{class{true, 0}, false}
}

// varClass is what a variable of the function can hold — its string value, or (err) the error it holds' text — by
// every value assigned it in the function; a parameter's is its caller's (incomplete).
func (s *scope) varClass(id *ast.Ident, depth int, err bool) partClass {
	pc := partClass{class{true, 0}, true}
	if id.Obj == nil || id.Obj.Kind != ast.Var {
		return partClass{class{true, 0}, false}
	}
	if _, isField := id.Obj.Decl.(*ast.Field); isField {
		return partClass{class{true, 0}, false} // a parameter: its caller's value
	}
	found := false
	add := func(rhs ast.Expr, multi bool) {
		found = true
		var lc partClass
		switch {
		case err:
			lc = s.errorClass(rhs, depth)
		case multi:
			lc = partClass{class{true, 0}, false}
			if c, ok := rhs.(*ast.CallExpr); ok {
				if key := s.localFunc(c); key != "" {
					if r, ok := s.k.retClass[key]; ok {
						lc = r
					}
				}
			}
		default:
			lc = s.valueClass(rhs, depth)
		}
		pc.c, pc.complete = pc.c.or(lc.c), pc.complete && lc.complete
	}
	ast.Inspect(s.body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.AssignStmt:
			for i, l := range st.Lhs {
				if li, ok := l.(*ast.Ident); ok && li.Obj == id.Obj {
					if len(st.Rhs) == len(st.Lhs) {
						add(st.Rhs[i], false)
					} else if len(st.Rhs) == 1 {
						add(st.Rhs[0], true)
					}
				}
			}
		case *ast.ValueSpec:
			for i, nm := range st.Names {
				if nm.Obj == id.Obj {
					if len(st.Values) == len(st.Names) {
						add(st.Values[i], false)
					} else if len(st.Values) == 1 {
						add(st.Values[0], true)
					}
				}
			}
		case *ast.RangeStmt:
			for _, l := range []ast.Expr{st.Key, st.Value} {
				if li, ok := l.(*ast.Ident); ok && li.Obj == id.Obj {
					found = true
					pc.complete = false
				}
			}
		}
		return true
	})
	if !found {
		pc.complete = false
	}
	return pc
}

// errorClass is what the error a call returns can say: an errors.New's or fmt.Errorf's message, or the errors a function
// of the tree makes (errClass); another package's, anything (incomplete).
func (s *scope) errorClass(e ast.Expr, depth int) partClass {
	c, ok := e.(*ast.CallExpr)
	if !ok {
		return partClass{class{true, 0}, false}
	}
	switch s.f.call(c) {
	case "errors.New", "fmt.Errorf":
		pc := partClass{class{true, 0}, true}
		for _, a := range c.Args {
			lc := s.valueClass(a, depth+1)
			pc.c, pc.complete = pc.c.join(lc.c), pc.complete && lc.complete
		}
		return pc
	}
	if key := s.localFunc(c); key != "" {
		if r, ok := s.k.errClass[key]; ok {
			return r
		}
	}
	return partClass{class{true, 0}, false}
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

// check holds one function's body — or a package-level declaration's values, ftype nil — to the rules.
func (k *checker) check(f *file, name string, ftype *ast.FuncType, body ast.Node, page bool) {
	s := &scope{k: k, f: f, name: name, ftype: ftype, body: body, taint: map[string]bool{}, builders: map[string]bool{}}
	s.params()
	s.propagate(body)
	s.findBuilders(body)
	s.builderUses()
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			sink := f.call(x)
			// a page function's builder writes
			if page {
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && s.builders[id.Name] && strings.HasPrefix(sel.Sel.Name, "Write") {
						k.sinks++
						s.checkOperands(x.Args, "a page write", k.fset.Position(x.Pos()).Line, k.fset.Position(x.End()).Line, true)
					}
				}
			}
			if !sinks[sink] {
				return true
			}
			if id, ok := x.Fun.(*ast.Ident); ok && id.Obj != nil {
				return true // a local of the name, no builtin
			}
			k.sinks++
			// every argument: chain.Emit's first, the event's type, as much as its keys and values (E6-RL2)
			start, end := k.fset.Position(x.Pos()).Line, k.fset.Position(x.End()).Line
			cl, echoes := s.checkOperands(x.Args, sink+"(...)", start, end, false)
			if cut[sink] {
				if echoes > maxEchoes {
					k.findings = append(k.findings, finding{k.fset.Position(x.Pos()), fmt.Sprintf("%s(...) in %s holds %d echoes: at most %d", sink, name, echoes, maxEchoes)})
				}
				if !cl.ascii && (cl.bound == unbounded || cl.bound > maxMessage) {
					b := "unbounded"
					if cl.bound != unbounded {
						b = strconv.Itoa(cl.bound) + " bytes"
					}
					k.findings = append(k.findings, finding{k.fset.Position(x.Pos()), fmt.Sprintf("%s(...) in %s can hold text outside ASCII (its own, or an operand's that the source or its reason does not hold to ASCII) and can reach gno's cut at byte 1,021 (%s): write it in ASCII, bound every operand under %d bytes, or say on the reason that its operand is ASCII", sink, name, b, maxMessage)})
				}
			}
		}
		return true
	})
	// a page function's returns (a closure's return is its own: an Iterate callback's bool)
	if page {
		ast.Inspect(body, func(n ast.Node) bool {
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
	// rule 2: a sink taken as a value, not called — in a function or a package-level declaration (E6-RL2)
	called := map[ast.Expr]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			called[c.Fun] = true
		}
		return true
	})
	ast.Inspect(body, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		if nm := f.qual(e); nm != "" && nm != "panic" && sinks[nm] && !called[e] {
			if _, isSel := e.(*ast.SelectorExpr); isSel {
				k.findings = append(k.findings, finding{k.fset.Position(e.Pos()), fmt.Sprintf("%s in %s is taken as a value: its calls are sinks the checker cannot see; call it directly", nm, name)})
				return false
			}
		}
		return true
	})
	// rule 3: a caller's string reaches p/hlroute's echoing parsers and a State's methods only after mustBeASCII — a
	// statement of the function's own body, not inside a branch or a closure, before the hand-off, the name assigned
	// nothing between the two (E6-RL2: the guard counted wherever it stood) — or on a line whose echo-ok names it
	guards := map[string][]token.Pos{}
	if blk, ok := body.(*ast.BlockStmt); ok {
		for _, st := range blk.List {
			es, ok := st.(*ast.ExprStmt)
			if !ok {
				continue
			}
			c, ok := es.X.(*ast.CallExpr)
			if !ok || !s.guard(c) {
				continue
			}
			a := c.Args[1]
			if cv, ok := a.(*ast.CallExpr); ok && f.call(cv) == "string" && len(cv.Args) == 1 {
				a = cv.Args[0]
			}
			if id, ok := a.(*ast.Ident); ok {
				guards[id.Name] = append(guards[id.Name], c.End())
			}
		}
	}
	// a name assigned a caller's bytes again after its guard (a clean value — a sanitizer's, CanonicalAddress's — keeps it)
	assigned := map[string][]token.Pos{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.AssignStmt:
			dirty := false
			for _, r := range st.Rhs {
				if len(s.blame(r)) > 0 {
					dirty = true
				}
			}
			for _, l := range st.Lhs {
				if id, ok := l.(*ast.Ident); ok && dirty {
					assigned[id.Name] = append(assigned[id.Name], st.Pos())
				}
			}
		case *ast.RangeStmt:
			for _, l := range []ast.Expr{st.Key, st.Value} {
				if id, ok := l.(*ast.Ident); ok {
					assigned[id.Name] = append(assigned[id.Name], st.Pos())
				}
			}
		}
		return true
	})
	guardedAt := func(nm string, at token.Pos) bool {
		for _, g := range guards[nm] {
			if g >= at {
				continue
			}
			clean := true
			for _, a := range assigned[nm] {
				if a > g && a < at {
					clean = false
				}
			}
			if clean {
				return true
			}
		}
		return false
	}
	ast.Inspect(body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		target := ""
		if nm := f.call(c); hlrouteParsers[nm] {
			target = nm
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
				if guardedAt(id.Name, c.Pos()) || f.okLines[line][id.Name] || f.okLines[line-1][id.Name] {
					continue
				}
				k.findings = append(k.findings, finding{k.fset.Position(id.Pos()), fmt.Sprintf("%s hands %s a caller's string, %s, before mustBeASCII: p/hlroute echoes what it refuses as it came (or say on the line why it cannot: // echo-ok(%s): <reason>)", name, target, id.Name, id.Name)})
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

// scan parses every file, computes the summaries and the page functions to a fixed point, then checks every function
// and every package-level declaration.
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
		f := &file{name: name, pkg: af.Name.Name, f: af, imports: map[string]bool{}, alias: map[string]string{}, okLines: map[int]map[string]bool{}, okASCII: map[int]bool{}, states: k.states[af.Name.Name]}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			n := path.Base(p)
			if isVersion(n) {
				n = path.Base(path.Dir(p)) // gno.land/p/…/hlroute/v0 goes by hlroute
			}
			if im.Name != nil {
				n = im.Name.Name
			}
			if n == "." {
				k.findings = append(k.findings, finding{k.fset.Position(im.Pos()), "a dot import of " + p + " hides its names from the checker: import it by name"})
				continue
			}
			f.imports[n] = true
			f.alias[n] = canonicalImport(p)
		}
		if k.local[f.pkg] == nil {
			k.local[f.pkg] = map[string]bool{}
			k.consts[f.pkg] = map[string]string{}
			k.types[f.pkg] = map[string]ast.Expr{}
			k.methods[f.pkg] = map[string]bool{}
			k.trusted[f.pkg] = map[string]bool{}
		}
		for _, d := range af.Decls {
			switch x := d.(type) {
			case *ast.FuncDecl:
				if x.Recv == nil {
					k.local[f.pkg][x.Name.Name] = true
					k.funcs[f.pkg+"."+x.Name.Name] = x
				} else if len(x.Recv.List) > 0 {
					t := x.Recv.List[0].Type
					if st, ok := t.(*ast.StarExpr); ok {
						t = st.X
					}
					if id, ok := t.(*ast.Ident); ok {
						k.methods[f.pkg][id.Name] = true
					}
				}
			case *ast.GenDecl:
				if x.Tok == token.TYPE {
					for _, sp := range x.Specs {
						ts := sp.(*ast.TypeSpec)
						k.types[f.pkg][ts.Name.Name] = ts.Type
					}
				}
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
				if i := strings.Index(cm.Text, "):"); i >= 0 && strings.Contains(cm.Text[i:], "ASCII") {
					f.okASCII[line] = true
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
					if c, ok := v.(*ast.CallExpr); ok && f.call(c) == "hlroute.New" && i < len(vs.Names) {
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
	k.pin()
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
	k.classes()
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
				s := &scope{k: k, f: f, name: fd.Name.Name, ftype: fd.Type, body: fd.Body, taint: map[string]bool{}, builders: map[string]bool{}}
				s.findBuilders(fd.Body)
				mark := func(e ast.Expr) {
					for _, l := range leaves(e) {
						if c, ok := l.(*ast.CallExpr); ok {
							if _, ok := s.sanitizer(c); ok {
								continue // a sanitizer is safe by its pinned source, not a page
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
			switch x := d.(type) {
			case *ast.FuncDecl:
				if x.Body != nil {
					k.check(f, x.Name.Name, x.Type, x.Body, x.Recv == nil && k.pages[f.pkg+"."+x.Name.Name])
				}
			case *ast.GenDecl:
				// a package-level declaration's values: a closure held in a var is a function like any other, and a
				// sink taken as a value there escapes rule 2 as much as in a function (E6-RL2)
				if x.Tok != token.VAR {
					continue
				}
				for _, sp := range x.Specs {
					vs := sp.(*ast.ValueSpec)
					if len(vs.Values) == 0 {
						continue
					}
					var nm []string
					for _, n := range vs.Names {
						nm = append(nm, n.Name)
					}
					k.check(f, "var "+strings.Join(nm, ", "), nil, vs, false)
				}
			}
		}
	}
	return nil
}

// pinnedSources are the tree's sanitizers and rule 3's guard as their source must read, comments aside, the realm's
// name in a refusal written <pkg>, whitespace runs one space (source); with what each relies on (pinnedNeeds). A
// function of the name whose source is another is no sanitizer: its calls are a function's like any other (E6-RL2:
// a sanitizer was trusted by its name, and echo.gno was compared across nothing). echo is trusted only where echoMax is
// 64 too, its bound (273 bytes) being QuoteToASCII's of 64 bytes and its length's note.
var pinnedSources = map[string][]string{
	"echo":             {`func echo(s string) string { if len(s) <= echoMax { return strconv.QuoteToASCII(s) } return strconv.QuoteToASCII(s[:echoMax]) + "... (" + strconv.Itoa(len(s)) + " bytes)" }`},
	"mustBeASCII":      {`func mustBeASCII(what, s string) { for i := 0; i < len(s); i++ { if s[i] >= 0x80 { panic("<pkg>: the " + what + " holds a byte outside ASCII, at offset " + strconv.Itoa(i) + ": refused before it is read") } } }`},
	"itoa":             {`func itoa(n int64) string { return strconv.FormatInt(n, 10) }`},
	"u32":              {`func u32(n uint32) string { return strconv.FormatUint(uint64(n), 10) }`},
	"u64s":             {`func u64s(n uint64) string { return strconv.FormatUint(n, 10) }`},
	"hx":               {`func hx(b []byte) string { return hex.EncodeToString(b) }`},
	"ea":               {`func ea(k [32]byte) string { return hlmailbox.EthAddress(k) }`},
	"keysHex":          {`func keysHex(ks []Key) string { s := make([]string, len(ks)) for i, k := range ks { s[i] = hx(k[:]) } return strings.Join(s, ",") }`},
	"EthAddress":       {`func EthAddress(b [32]byte) string { for _, c := range b[:12] { if c != 0 { return "0x" + hex.EncodeToString(b[:]) } } return "0x" + eip55(b[12:]) }`},
	"eip55":            {`func eip55(addr []byte) string { lower := hex.EncodeToString(addr) h := keccak256.Sum256([]byte(lower)) out := []byte(lower) for i := 0; i < len(out); i++ { c := out[i] if c < 'a' || c > 'f' { continue } nib := h[i/2] >> 4 if i%2 == 1 { nib = h[i/2] & 0x0f } if nib >= 8 { out[i] = c - 'a' + 'A' } } return string(out) }`},
	"CanonicalAddress": {`func CanonicalAddress(a address) address { b, err := hl.AddressToBytes32(a) if err != nil { return "" } c, err := hl.Bytes32ToAddress(b) if err != nil { return "" } return c }`},
}

// pinnedNeeds are what a pinned function relies on, trusted too: a local function, or pkg.Func of another realm.
var pinnedNeeds = map[string][]string{"EthAddress": {"eip55"}, "keysHex": {"hx"}, "ea": {"hlmailbox.EthAddress"}}

// source is a function's source as pinnedSources holds it.
func (k *checker) source(pkg string, fd *ast.FuncDecl) string {
	var b strings.Builder
	if err := printer.Fprint(&b, token.NewFileSet(), &ast.FuncDecl{Name: fd.Name, Type: fd.Type, Body: fd.Body}); err != nil {
		return ""
	}
	t := strings.ReplaceAll(b.String(), "\""+pkg+": ", "\"<pkg>: ")
	return strings.Join(strings.Fields(t), " ")
}

// pin trusts each package's sanitizers and guard whose source is pinned, and whose needs are trusted (to a fixed point).
func (k *checker) pin() {
	for pkg := range k.local {
		for name, srcs := range pinnedSources {
			fd := k.funcs[pkg+"."+name]
			if fd == nil || fd.Body == nil {
				continue
			}
			src := k.source(pkg, fd)
			for _, want := range srcs {
				if src == want && (name != "echo" || k.consts[pkg]["echoMax"] == "64") {
					k.trusted[pkg][name] = true
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for pkg, t := range k.trusted {
			for name := range t {
				for _, need := range pinnedNeeds[name] {
					np, nf := pkg, need
					if i := strings.Index(need, "."); i >= 0 {
						np, nf = need[:i], need[i+1:]
					}
					if !k.trusted[np][nf] {
						delete(t, name)
						changed = true
					}
				}
			}
		}
	}
}

// classes computes, to a fixed point, what each function of the tree's string results can hold (retClass) and what
// the errors it returns can say (errClass): its errors.New and fmt.Errorf messages and the errors it hands on from
// calls (rule 4). A function whose class keeps growing past the rounds is read as unbounded.
func (k *checker) classes() {
	type fn struct {
		f  *file
		fd *ast.FuncDecl
	}
	var all []fn
	for _, f := range k.files {
		for _, d := range f.f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Body != nil {
				all = append(all, fn{f, fd})
				key := f.pkg + "." + fd.Name.Name
				k.retClass[key] = partClass{class{true, 0}, true}
				k.errClass[key] = partClass{class{true, 0}, true}
			}
		}
	}
	for round := 0; round < 30; round++ {
		changed := false
		for _, x := range all {
			key := x.f.pkg + "." + x.fd.Name.Name
			s := &scope{k: k, f: x.f, name: x.fd.Name.Name, ftype: x.fd.Type, body: x.fd.Body, taint: map[string]bool{}}
			strs, errs := resultKinds(x.fd)
			ret, er := partClass{class{true, 0}, true}, partClass{class{true, 0}, true}
			or := func(a *partClass, b partClass) { a.c, a.complete = a.c.or(b.c), a.complete && b.complete }
			ast.Inspect(x.fd.Body, func(n ast.Node) bool {
				switch y := n.(type) {
				case *ast.FuncLit:
					return false // a closure's returns are its own
				case *ast.CallExpr:
					if nm := x.f.call(y); nm == "errors.New" || nm == "fmt.Errorf" {
						or(&er, s.errorClass(y, 0))
					}
				case *ast.ReturnStmt:
					if len(y.Results) == 1 && len(strs)+len(errs) > 1 {
						if c, ok := y.Results[0].(*ast.CallExpr); ok { // return f(…), handing on its results
							if kk := s.localFunc(c); kk != "" {
								or(&ret, k.retClass[kk])
								or(&er, k.errClass[kk])
							} else {
								or(&ret, partClass{class{true, 0}, false})
								or(&er, partClass{class{true, 0}, false})
							}
						}
						return true
					}
					for _, i := range strs {
						if i < len(y.Results) {
							or(&ret, s.valueClass(y.Results[i], 0))
						}
					}
					for _, i := range errs {
						if i < len(y.Results) {
							switch r := y.Results[i].(type) {
							case *ast.Ident:
								if r.Name != "nil" {
									or(&er, s.varClass(r, 0, true))
								}
							default:
								or(&er, s.errorClass(r, 0))
							}
						}
					}
				}
				return true
			})
			if ret != k.retClass[key] || er != k.errClass[key] {
				k.retClass[key], k.errClass[key] = ret, er
				changed = true
			}
		}
		if !changed {
			return
		}
	}
	for key := range k.retClass {
		k.retClass[key] = partClass{unknownClass, false}
		k.errClass[key] = partClass{unknownClass, false}
	}
}

// resultKinds are the positions of a function's string results (a string, an address) and of its error results.
func resultKinds(fd *ast.FuncDecl) (strs, errs []int) {
	if fd.Type.Results == nil {
		return nil, nil
	}
	i := 0
	for _, r := range fd.Type.Results.List {
		n := len(r.Names)
		if n == 0 {
			n = 1
		}
		for j := 0; j < n; j++ {
			if id, ok := r.Type.(*ast.Ident); ok {
				switch id.Name {
				case "string", "address":
					strs = append(strs, i)
				case "error":
					errs = append(errs, i)
				}
			}
			i++
		}
	}
	return strs, errs
}

func newChecker() *checker {
	return &checker{fset: token.NewFileSet(), local: map[string]map[string]bool{}, funcs: map[string]*ast.FuncDecl{}, consts: map[string]map[string]string{}, types: map[string]map[string]ast.Expr{}, methods: map[string]map[string]bool{}, trusted: map[string]map[string]bool{}, returns: map[string]bool{}, retClass: map[string]partClass{}, errClass: map[string]partClass{}, pages: map[string]bool{}, states: map[string]map[string]bool{}}
}

// The fixtures run first: a rule that stopped reading what it must, or started reading what it must not, fails
// before the scan it would have made meaningless (check-interrealm.py's discipline).
// mustFireFiles are fixtures of two files: a State declared in one is a State in the other.
var mustFireFiles = []map[string]string{
	{"a.gno": "package p\nimport \"gno.land/p/" + deployer + "/hlroute/v0\"\nvar st = hlroute.New(hlroute.Launch{})\n",
		"b.gno": "package p\nfunc F(cur realm, id string) { st.ClaimOf(id) }\n"},
	// a dot import hides its package's names (E6-RL2)
	{"a.gno": "package p\nimport . \"chain\"\nfunc F(cur realm, s string) { Emit(\"E\", \"k\", s) }\n"},
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
	// round 6 (E6-RL2): a reason's operand whose source the checker reads, outside ASCII and unbounded, past the cut
	`func P(s string) error { return errors.New("bad — " + strconv.QuoteToASCII(s)) }
func F(cur realm, s string) { if err := P(s); err != nil { panic("x " + err.Error()) } // echo-ok(err): P's refusal, ASCII
}`,
	// a reason that does not say its operand is ASCII, of a value the checker cannot read, in a panic
	`func F(cur realm) { p := cur.Previous().PkgPath(); panic("x " + p) // echo-ok(p): the caller's path
}`,
	// a builder handed to a helper, whose writes are then no page writes
	`func Render(path string) string { var b strings.Builder; put(&b, path); return b.String() }
func put(b *strings.Builder, s string) { b.WriteString(s) }`,
	// rule 3's guard inside a branch, and one the name is assigned after
	`var st = hlroute.New(hlroute.Launch{})
func F(cur realm, ids string, ok bool) { if ok { mustBeASCII("ids", ids) }; st.DraftRestore(ids) }`,
	`var st = hlroute.New(hlroute.Launch{})
func F(cur realm, ids, other string) { mustBeASCII("ids", ids); ids = other; st.DraftRestore(ids) }`,
	// a closure's parameter handed to p/hlroute
	`var st = hlroute.New(hlroute.Launch{})
func F(cur realm) { f := func(id string) { st.ClaimOf(id) }; f("x") }`,
	// a refusal outside ASCII, bounded where it is made, handed on by a long ASCII message past the cut (the twelfth)
	"const pad = \"" + strings.Repeat("x", 900) + "\"\n" + `func P(s string) error { return errors.New("bad — " + echo(s)) }
func F(cur realm, s string) { if err := P(s); err != nil { panic(pad + err.Error()) } // echo-ok(err): P's refusal
}`,
}

var mustNotFire = []string{
	`func F(cur realm, s string) { panic("x: " + echo(s)) }`,
	`func F(cur realm, s string) { panic("x: " + strconv.Itoa(len(s))) }`,
	`func F(cur realm, n int) { panic("fixed " + strconv.Itoa(n)) }`,
	`func F(cur realm, s string) { panic("x: " + s) // echo-ok(s): a test of the reason, which says its operand is ASCII
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
	// round 6 (E6-RL2): a reason's operand whose source the checker reads and finds ASCII, at any length
	`func P(s string) error { return errors.New("bad " + strconv.QuoteToASCII(s)) }
func F(cur realm, s string) { if err := P(s); err != nil { panic("x " + err.Error()) } // echo-ok(err): P's refusal
}`,
	// a reason that says its operand is ASCII, of a value the checker cannot read
	`func F(cur realm) { p := cur.Previous().PkgPath(); panic("x " + p) // echo-ok(p): the caller's pkgpath, ASCII by tm2's path rules
}`,
	// an operand outside ASCII the source bounds under the cut
	`func P(s string) error { return errors.New("bad — " + echo(s)) }
func F(cur realm, s string) { if err := P(s); err != nil { panic("x " + err.Error()) } // echo-ok(err): P's refusal
}`,
	// a sentinel of a type with no method, and the guard as a statement of the body before the hand-off
	`type sentinel struct{}
var st = hlroute.New(hlroute.Launch{})
func F(cur realm, ids string) { mustBeASCII("ids", ids); st.DraftRestore(ids); panic(sentinel{}) }`,
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

// blindRealm6 is the round-6 realms audit's fixture (E6-RL2): twelve more constructs that put a caller's bytes into a
// panic, an event or a page past the round-5 checker. Each must be one finding, in its own construct (the N<k> comment
// before it; the auditor's control C1 is left out: a plain echo is mustFire's first fixture).
const blindRealm6 = `package zzblind

import (
	ch "chain"
	"chain"
	"strings"

	"gno.land/p/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/hlroute/v0"
)

var st = hlroute.New(hlroute.Launch{})

var stored string

// N1: chain imported under another name: ch.Emit is no sink.
func N1(cur realm, s string) { ch.Emit("E", "k", s) }

// N2: an event's type, chain.Emit's first argument, is never checked.
func N2(cur realm, s string) { chain.Emit(s, "k", "v") }

// N3: a package-level closure's body is never walked.
var fail = func(s string) { panic("n3: " + s) }

func N3(cur realm, s string) { fail(s) }

// N4: a sink taken as a value at package level escapes rule 2.
var emit = chain.Emit

func N4(cur realm, s string) { emit("E", "k", s) }

// N5: a sanitizer is trusted by its name: this echo returns its argument.
func echo(s string) string { return s }

func N5(cur realm, s string) { panic("n5: " + echo(s)) }

// N6: a parameter named as a package constant reads as the constant's text.
const id = "fixed"

func N6(cur realm, id string) { panic("n6: " + id) }

// N7: a local named as a package constant.
const msg = "fixed"

func N7(cur realm, s string) {
	msg := s
	panic(msg)
}

// N8: a page's builder written through a pointer.
func Render(path string) string {
	var b strings.Builder
	p := &b
	p.WriteString(path)
	return b.String()
}

// N9: rule 3's guard is flow-insensitive: the hand-off panics with the raw ids before mustBeASCII runs.
func mustBeASCII(what, s string) {}

func N9(cur realm, ids string) {
	st.DraftRestore(ids)
	mustBeASCII("ids", ids)
}

// N11: an empty composite literal reads as a constant, but gno v1.5.1 renders a panic's composite value through its
// Error() method (BoundedSprintTV -> boundedUserSprint -> tv.Sprint(m)).
type E struct{}

func (E) Error() string { return stored }

func N11set(cur realm, s string) { stored = s }
func N11(cur realm)              { panic(E{}) }

// N12: a [32]byte parameter is no caller's string to rule 3, but MsgRun hands any bytes.
func N12(cur realm, k [32]byte) { st.SendOf(string(k[:])) }

// N13: a named string type is no caller's string to rule 3.
type Word string

func N13(cur realm, w Word) { st.ClaimOf(string(w)) }
`

func selftest() error {
	const head = "package p\nimport (\"errors\"\n\"strings\"\n\"strconv\"\n\"chain\"\n\"encoding/hex\"\n\"gno.land/p/" + deployer + "/decisions/v0\"\n\"gno.land/p/" + deployer + "/hlroute/v0\")\n" +
		// the pinned sanitizers and guard, as the realms write them (pinnedSources)
		"const echoMax = 64\n" +
		"func echo(s string) string {\n\tif len(s) <= echoMax {\n\t\treturn strconv.QuoteToASCII(s)\n\t}\n\treturn strconv.QuoteToASCII(s[:echoMax]) + \"... (\" + strconv.Itoa(len(s)) + \" bytes)\"\n}\n" +
		"func mustBeASCII(what, s string) {\n\tfor i := 0; i < len(s); i++ {\n\t\tif s[i] >= 0x80 {\n\t\t\tpanic(\"p: the \" + what + \" holds a byte outside ASCII, at offset \" + strconv.Itoa(i) + \": refused before it is read\") // echo-ok(what): every caller names its parameter with a constant, ASCII\n\t\t}\n\t}\n}\n" +
		"func hx(b []byte) string { return hex.EncodeToString(b) }\n" +
		"func itoa(n int64) string { return strconv.FormatInt(n, 10) }\n"
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
	// the round-6 audit's twelve: one finding each, in its own construct
	k = newChecker()
	if err := k.scan(map[string]string{"blind6.gno": blindRealm6}); err != nil {
		return fmt.Errorf("the round-6 blind realm does not parse: %v", err)
	}
	lines := strings.Split(blindRealm6, "\n")
	per := map[string]int{}
	var w []string
	for _, f := range k.findings {
		c := "?"
		for l := f.pos.Line - 1; l >= 0 && l < len(lines); l-- {
			if m := regexp.MustCompile(`^// (N[0-9]+):`).FindStringSubmatch(lines[l]); m != nil {
				c = m[1]
				break
			}
		}
		per[c]++
		w = append(w, fmt.Sprintf("%s line %d: %s", c, f.pos.Line, f.what))
	}
	want := []string{"N1", "N2", "N3", "N4", "N5", "N6", "N7", "N8", "N9", "N11", "N12", "N13"}
	bad := len(k.findings) != len(want)
	for _, c := range want {
		if per[c] != 1 {
			bad = true
		}
	}
	if bad {
		return fmt.Errorf("SELFTEST: the round-6 audit's realm of twelve blind constructs gives %d findings, not one in each of %v:\n  %s", len(k.findings), want, strings.Join(w, "\n  "))
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
