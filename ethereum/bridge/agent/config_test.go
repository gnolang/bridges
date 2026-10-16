package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A deployment's config: the gno side, the contracts' addresses (in their EIP-55 case only: one case carries no
// checksum, §5.2, round 3's E3-CF6), the routes, the signers, the bounds; a config that names the contracts needs
// Ethereum's domain and chain id.
func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "bridge.json")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := `{"gno":{"rpc":"http://127.0.0.1:26657","chainId":"dev","mailbox":"gno.land/r/x/eth/hlmailbox","gov":"gno.land/r/x/eth/hlgov"},
		"eth":{"providers":[{"name":"anvil","url":"http://127.0.0.1:8545","logRange":500}],"chainId":31337,"domain":31337,
			"mailbox":"0x00000000000000000000000000000000000000B1","client":"0x00000000000000000000000000000000000000C1",
			"gate":"0x00000000000000000000000000000000000000D1","admin":"0x00000000000000000000000000000000000000e1",
			"prices":{"GNOT":0.0000282},"emergencyMaxBaseFeeGwei":200},
		"routes":[{"symbol":"GNOT","gno":"gno.land/r/x/eth/hlgnot","token":"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"}],
		"signers":[{"url":"https://signer.example","key":"` + strings.Repeat("ab", 32) + `"}]}`
	c, err := LoadConfig(write(good))
	if err != nil {
		t.Fatal(err)
	}
	if c.Gno.Gnokey != "gnokey" || c.Gno.DAO != "gno.land/r/gov/dao" || !c.HasContracts() || c.Eth.Providers[0].LogRange != 500 {
		t.Fatalf("%+v", c)
	}
	if c.MaxLeaf() != 8192 || c.ReemitBatch() != 4 || c.Heartbeat().Hours() != 6 || c.Eth.UpdateGas != 0 || c.EmergencyMaxBaseFee().String() != "200000000000" {
		t.Fatal("the defaults")
	}
	bad := map[string]string{
		"a bad checksum":          strings.Replace(good, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", "0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", 1),
		"a token in one case":     strings.Replace(good, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", 1),
		"a mailbox in one case":   strings.Replace(good, "0x00000000000000000000000000000000000000B1", "0x00000000000000000000000000000000000000b1", 1),
		"no domain":               strings.Replace(good, `"domain":31337,`, "", 1),
		"a price of no route":     strings.Replace(good, `"prices":{"GNOT":0.0000282}`, `"prices":{"ETH":1}`, 1),
		"an emergency cap below":  strings.Replace(good, `"emergencyMaxBaseFeeGwei":200`, `"emergencyMaxBaseFeeGwei":2,"maxBaseFeeGwei":5`, 1),
		"a plain-http signer":     strings.Replace(good, "https://signer.example", "http://signer.example", 1),
		"a provider with no name": strings.Replace(good, `"name":"anvil",`, "", 1),
	}
	for name, s := range bad {
		if _, err := LoadConfig(write(s)); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}
