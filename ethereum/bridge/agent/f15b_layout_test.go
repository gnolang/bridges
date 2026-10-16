package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E4-GT3 (go-trust-poc/zz_audit4_layout_test.go): LayoutDiff compared a type by its name with the AST number stripped,
// so a struct whose members a new build reorders — Hyperlane's Mailbox `deliveries`, every delivered mark under it — read
// "append-only", and the kind 8 "immutable"; so did a fixed array that shrank. The types are compared through the
// layouts' "types": a struct's members, a mapping's value, an array's length and element, every size.
func TestF15bLayoutComparesTheTypes(t *testing.T) {
	delivery := func(ast, members, size string) string {
		return `"t_struct(Delivery)` + ast + `_storage":{"label":"struct Mailbox.Delivery","numberOfBytes":"` + size + `","members":[` + members + `]},` +
			`"t_mapping(t_bytes32,t_struct(Delivery)` + ast + `_storage)":{"label":"mapping(bytes32 => struct Mailbox.Delivery)","numberOfBytes":"32"},"t_uint8":{"numberOfBytes":"1"},"t_uint32":{"numberOfBytes":"4"}`
	}
	mailbox := func(ast, members, size string) string {
		return `{"storage":[{"label":"_initialized","slot":"0","offset":0,"type":"t_uint8"},{"label":"nonce","slot":"101","offset":0,"type":"t_uint32"},{"label":"deliveries","slot":"102","offset":0,"type":"t_mapping(t_bytes32,t_struct(Delivery)` + ast + `_storage)"}],"types":{` + delivery(ast, members, size) + `}}`
	}
	const kept = `{"label":"processor","slot":"0","offset":0,"type":"t_address"},{"label":"blockNumber","slot":"0","offset":20,"type":"t_uint48"}`
	old := mailbox("8100", kept, "32")
	array := func(n, bytes string) string {
		return `{"storage":[{"label":"x","slot":"0","offset":0,"type":"t_uint256"},{"label":"last","slot":"1","offset":0,"type":"t_array(t_uint256)` + n + `_storage"}],"types":{"t_uint256":{"numberOfBytes":"32"},"t_array(t_uint256)` + n + `_storage":{"base":"t_uint256","numberOfBytes":"` + bytes + `"}}}`
	}
	cases := []struct{ name, old, new, want string }{
		{"the same struct, another build", old, mailbox("9200", kept, "32"), LayoutAppendOnly},
		{"the struct's members reordered", old, mailbox("9200", `{"label":"blockNumber","slot":"0","offset":0,"type":"t_uint48"},{"label":"processor","slot":"0","offset":6,"type":"t_address"}`, "32"), "LAYOUT DIFFERS: deliveries"},
		{"a member's type changed", old, mailbox("9200", `{"label":"processor","slot":"0","offset":0,"type":"t_address"},{"label":"blockNumber","slot":"0","offset":20,"type":"t_uint64"}`, "32"), "LAYOUT DIFFERS"},
		{"a member appended in the struct's unused bytes", old, mailbox("9200", kept+`,{"label":"flag","slot":"0","offset":26,"type":"t_bool"}`, "32"), LayoutAppendOnly},
		{"the struct grown past its slot", old, mailbox("9200", kept+`,{"label":"more","slot":"1","offset":0,"type":"t_uint256"}`, "64"), "LAYOUT DIFFERS"},
		{"the struct's members not given", old, `{"storage":[{"label":"_initialized","slot":"0","offset":0,"type":"t_uint8"},{"label":"nonce","slot":"101","offset":0,"type":"t_uint32"},{"label":"deliveries","slot":"102","offset":0,"type":"t_mapping(t_bytes32,t_struct(Delivery)9200_storage)"}]}`, "LAYOUT DIFFERS"},
		{"the last array shrunk", array("4", "128"), array("2", "64"), "LAYOUT DIFFERS: last"},
		{"the last array grown", array("4", "128"), array("6", "192"), LayoutAppendOnly},
		{"a mapping's value type changed", `{"storage":[{"label":"m","slot":"0","offset":0,"type":"t_mapping(t_address,t_uint256)"}],"types":{"t_mapping(t_address,t_uint256)":{"numberOfBytes":"32"}}}`, `{"storage":[{"label":"m","slot":"0","offset":0,"type":"t_mapping(t_address,t_uint128)"}],"types":{"t_mapping(t_address,t_uint128)":{"numberOfBytes":"32"}}}`, "LAYOUT DIFFERS"},
	}
	for _, c := range cases {
		got := LayoutDiff([]byte(c.old), []byte(c.new))
		t.Logf("%s: %s", c.name, got)
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %s (want %q)", c.name, got, c.want)
		}
	}
}

// The project's own layouts, as forge writes them (F15B_LAYOUTS: a directory of `forge inspect <C> storageLayout --json`
// files): each kept by itself — an honest rebuild reads append-only under the types' comparison.
func TestF15bRealLayoutsKeepThemselves(t *testing.T) {
	dir := os.Getenv("F15B_LAYOUTS")
	if dir == "" {
		t.Skip("F15B_LAYOUTS unset: forge's layouts of the contracts, on the devbox")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) == 0 {
		t.Fatalf("no layouts in %s", dir)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		got := LayoutDiff(b, b)
		t.Logf("%s: %s", filepath.Base(f), got)
		if !strings.HasPrefix(got, LayoutAppendOnly) {
			t.Errorf("%s does not keep itself: %s", filepath.Base(f), got)
		}
	}
}
