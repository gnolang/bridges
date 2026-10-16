package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gnolang/bridges/solana/bridge/agent"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// Round 3 of the Ethereum port's audit, the site's part (E3-CF6): an Ethereum address a person types is taken in its
// EIP-55 case only; a burn may name neither deployment's hlverifier; the page knows all sixteen of the Ethereum
// deployment's contracts. These tests read the config and the API as JSON, so they run unchanged on the code before.

// ethChainJSON is the mock's Ethereum chain beside Solana's, as a config's JSON object to edit.
func ethChainJSON(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	both := mockConfig()
	both.Chains = append(both.Chains, mockEthChain())
	b, _ := json.Marshal(both)
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c, c["chains"].([]any)[1].(map[string]any)
}

func loadMap(t *testing.T, c map[string]any) error {
	t.Helper()
	b, _ := json.Marshal(c)
	_, err := loadText(t, string(b))
	return err
}

// TestEthConfigTakesOnlyTheChecksum: the config's Ethereum addresses are typed by the operator, so they are taken in
// their EIP-55 case only (§5.2: "the site and the tools accept only EIP-55 checksummed input"): one case carries no
// checksum and a typo in it would pass unseen — the refusal names no checksummed form to paste back. An address whose
// EIP-55 form is itself one case is taken as typed.
func TestEthConfigTakesOnlyTheChecksum(t *testing.T) {
	for _, field := range []string{"mailbox", "pausable", "admin", "client", "proxyAdmin", "merkleTreeHook", "protocolFee"} {
		for _, one := range []func(string) string{strings.ToLower, func(s string) string { return "0x" + strings.ToUpper(s[2:]) }} {
			c, e := ethChainJSON(t)
			p := e["programs"].(map[string]any)
			cs := p[field].(string)
			p[field] = one(cs)
			err := loadMap(t, c)
			if err == nil || !strings.Contains(err.Error(), "one case") || strings.Contains(err.Error(), cs) {
				t.Errorf("programs.%s %s: %v (want a one-case refusal that names no checksummed form)", field, p[field], err)
			}
		}
	}
	c, e := ethChainJSON(t)
	tok := e["tokens"].([]any)[0].(map[string]any)
	tok["program"] = strings.ToLower(tok["program"].(string))
	if err := loadMap(t, c); err == nil || !strings.Contains(err.Error(), "one case") {
		t.Errorf("a token's contract in lower case: %v", err)
	}
	for _, own := range []string{"0x1234567890123456789012345678901234567890", "0x123456789012345678901234567890123456789a", "0x00000000000000000000000000000000000000AB"} {
		a, err := parseEthAddress(own)
		if err != nil || a.String() != own {
			t.Errorf("%s, its own EIP-55 form: %s %v", own, a, err)
		}
	}
	for _, typo := range []string{"0x00000000000000000000000000000000000000ab", "0x123456789012345678901234567890123456789A"} {
		if _, err := parseEthAddress(typo); err == nil {
			t.Errorf("%s (not its EIP-55 form) taken", typo)
		}
	}
}

// TestEthContractsAllSixteen: the page refuses as a send's recipient every contract the Ethereum deployment created —
// the realm refuses all sixteen (eth/realm/hlmailbox ethdeploy.gno: the five proxies, the four plain contracts, the five
// launch implementations, the Ed25519Verifier and HlAdminExec), so /api/chains lists the sixteen, each at its own
// address; the config names each and refuses one missing or two at one address.
func TestEthContractsAllSixteen(t *testing.T) {
	s, _, _, _ := mockBoth(false, siteDir, fixedNow)
	rr := httptest.NewRecorder()
	s.handler().ServeHTTP(rr, httptest.NewRequest("GET", "/api/chains", nil))
	var out struct {
		Chains []struct {
			ID        string            `json:"id"`
			Contracts map[string]string `json:"contracts"`
		} `json:"chains"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || len(out.Chains) != 2 {
		t.Fatalf("/api/chains: %v %s", err, rr.Body.String())
	}
	cs := out.Chains[1].Contracts
	want := []string{"mailbox", "gate", "admin", "client", "proxyAdmin", "merkleTreeHook", "protocolFee", "GNOT", "BUBBLE",
		"mailboxImpl", "token0Impl", "token1Impl", "clientImpl", "gateImpl", "ed25519Verifier", "hlAdminExec"}
	seen := map[string]string{}
	for _, k := range want {
		a, ok := cs[k]
		if _, err := parseEthAddress(a); !ok || err != nil {
			t.Errorf("contract %s: %q %v", k, a, err)
			continue
		}
		if o, dup := seen[strings.ToLower(a)]; dup {
			t.Errorf("contracts %s and %s at one address %s", o, k, a)
		}
		seen[strings.ToLower(a)] = k
	}
	if len(cs) != len(want) {
		t.Errorf("%d contracts listed, want the deployment's %d: %v", len(cs), len(want), cs)
	}
	if out.Chains[0].Contracts != nil {
		t.Errorf("Solana's entry lists contracts: %v", out.Chains[0].Contracts)
	}
	for _, field := range []string{"mailboxImpl", "token0Impl", "token1Impl", "clientImpl", "gateImpl", "ed25519Verifier", "hlAdminExec"} {
		c, e := ethChainJSON(t)
		delete(e["programs"].(map[string]any), field)
		if err := loadMap(t, c); err == nil || !strings.Contains(err.Error(), "programs."+field) {
			t.Errorf("no programs.%s: %v", field, err)
		}
	}
	c, e := ethChainJSON(t)
	p := e["programs"].(map[string]any)
	p["gateImpl"] = p["clientImpl"]
	if err := loadMap(t, c); err == nil || !strings.Contains(err.Error(), "same address") {
		t.Errorf("two contracts at one address: %v", err)
	}
}

// TestBurnToAVerifierRefused: a burn may not name either deployment's hlverifier as its gno.land recipient — on the
// Ethereum deployment its routes sweep such a credit (§5.2: "one of this deployment's realms (its hlverifier
// included), one of the Solana deployment's six"), on the Solana one the route pays it into the verifier's account,
// where no code moves it — so both builders refuse it, as they refuse the other realms (K21): the launch verifier of
// each deployment (its mailbox's namespace's hlverifier) and the verifier each mailbox names as active now.
func TestBurnToAVerifierRefused(t *testing.T) {
	s, _, _, _ := mockBoth(false, siteDir, fixedNow)
	sol, eth := s.others["solana"], s.others["ethereum"]
	solTok, ethTok := *s.cfg.Chain("solana").Token("BUBBLE"), *s.cfg.Chain("ethereum").Token("BUBBLE")
	solSender, ethSender := mockPub("alice").String(), mockEthAddr("alice").String()
	for _, v := range []string{mockNS + "hlverifier", mockEthNS("hlverifier")} {
		to := hl.GnoPackageAddress(v)
		if _, err := sol.BurnTx(ctx, solTok, solSender, to, uint64(200*one)); err == nil || !strings.Contains(err.Error(), "bridge realm") || !strings.Contains(err.Error(), v) {
			t.Errorf("Solana's builder, a burn to %s: %v", v, err)
		}
		if _, err := eth.BurnTx(ctx, ethTok, ethSender, strings.ToUpper(to), uint64(200*one)); err == nil || !strings.Contains(err.Error(), "bridge realm") || !strings.Contains(err.Error(), v) {
			t.Errorf("Ethereum's builder, a burn to %s: %v", v, err)
		}
	}
	// a verifier the mailbox names as active later (a swap): refused once the mailbox is read naming it
	swapped := mockNS + "hlverifier2"
	s.rememberMailbox(s.cfg.Chain("solana"), agent.MailboxView{ActiveVerifier: swapped})
	if _, err := eth.BurnTx(ctx, ethTok, ethSender, hl.GnoPackageAddress(swapped), uint64(200*one)); err == nil || !strings.Contains(err.Error(), swapped) {
		t.Errorf("a burn to the active verifier %s: %v", swapped, err)
	}
	// a wallet is no realm: the same burn to the recipient builds
	if _, err := eth.BurnTx(ctx, ethTok, ethSender, mockRecipient, uint64(200*one)); err != nil {
		t.Errorf("a burn to a wallet: %v", err)
	}
}
