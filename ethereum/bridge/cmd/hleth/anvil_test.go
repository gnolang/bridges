package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// The setup, the audit and the resumption on a real chain: anvil at Ethereum mainnet's fork (osaka), the builds of
// HLETH_CONTRACTS_OUT and HLETH_HYPERLANE_DIR (skipped where they or anvil are missing: the devbox has them).

const anvilKey0 = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

// anvil's default accounts 1–6: the seats, the watch slots, the beneficiary.
var anvilAccounts = []string{"0x70997970C51812dc3A010C7d01b50e0d17dc79C8", "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC",
	"0x90F79bf6EB2c4f870365E785982E1f101E93b906", "0x15d34AAf54267DB7D7c367839AAf71A00a2C6A65",
	"0x9965507D1a55bcC2695C58ba16FB37d819B0A4dc", "0x976EA74026E726554dB657fA54763abd0C3a0aa9"}

func startAnvil(t *testing.T) (string, *rpc.Client) {
	t.Helper()
	bin, err := exec.LookPath("anvil")
	if err != nil {
		bin = "/root/.foundry/bin/anvil"
		if _, err := os.Stat(bin); err != nil {
			t.Skip("no anvil")
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command(bin, "--port", strconv.Itoa(port), "--hardfork", "osaka", "--silent")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	c, err := rpc.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		var id hexutil.Big
		if err := c.CallContext(context.Background(), &id, "eth_chainId"); err == nil {
			break
		}
		if i > 100 {
			t.Fatal("anvil did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return url, c
}

// trustedFile writes an offline trusted header as -trusted-file takes it (fold F4): the node's /block block.header in
// amino's JSON (bytes in base64, int64 as strings) beside its set, four validators in tm2's order. The header is chain
// "dev"'s at height 100 and of the time given; an announcing one's NextValidatorsHash is another set's.
func trustedFile(t *testing.T, dir string, at time.Time, announcing bool) string {
	t.Helper()
	set := &gnolight.ValidatorSet{}
	for i := 0; i < 4; i++ {
		set.Validators = append(set.Validators, gnolight.Validator{PubKey: sha256.Sum256([]byte(fmt.Sprintf("validator %d", i))), Power: 10})
	}
	set.Sort()
	sh, _ := set.Hash()
	next := sh
	if announcing {
		next = sha256.Sum256([]byte("another set"))
	}
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	h := func(s string) string { x := sha256.Sum256([]byte(s)); return b64(x[:]) }
	header, _ := json.Marshal(map[string]any{"version": "v1.0.0-rc.0", "chain_id": "dev", "height": "100", "time": at.UTC().Format(time.RFC3339Nano),
		"num_txs": "0", "total_txs": "12", "app_version": "", "last_block_id": map[string]any{"hash": h("block 99"), "parts": map[string]any{"total": "1", "hash": h("parts 99")}},
		"last_commit_hash": h("commit 99"), "data_hash": "", "validators_hash": b64(sh[:]), "next_validators_hash": b64(next[:]),
		"consensus_hash": h("consensus"), "app_hash": h("app 99"), "last_results_hash": "", "proposer_address": "g1proposer"})
	j := trustedFileJSON{Header: header}
	for _, v := range set.Validators {
		j.Validators = append(j.Validators, ValidatorJSON{PubKey: fmt.Sprintf("0x%x", v.PubKey), Power: v.Power})
	}
	b, _ := json.Marshal(j)
	f, err := os.CreateTemp(dir, "trusted-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func deployerKey(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "deployer.key")
	if err := os.WriteFile(p, []byte(anvilKey0+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const testNS = "gno.land/r/g1leu8d2vsplhehcfkjg50mwgdpxdkt8tztu95wr/eth/"

func testSetupConfig(t *testing.T, url, dir string) setupConfig {
	arts := [2]string{os.Getenv("HLETH_CONTRACTS_OUT"), os.Getenv("HLETH_HYPERLANE_DIR")}
	var seats [4]common.Address
	var watch [2]common.Address
	for i := range seats {
		seats[i] = common.HexToAddress(anvilAccounts[i])
	}
	for i := range watch {
		watch[i] = common.HexToAddress(anvilAccounts[4+i])
	}
	return setupConfig{RPC: url, DeployerKey: deployerKey(t, dir), ContractsOut: arts[0], HyperlaneDir: arts[1], Out: filepath.Join(dir, "deployment.json"), ConfigOut: filepath.Join(dir, "bridge.eth.json"),
		GnoDomain: 1198419796, MailboxPkgPath: testNS + "hlmailbox", Hlgov: testNS + "hlgov", GnotRoute: testNS + "hlgnot", BubbleRoute: testNS + "hlbubble",
		Seats: seats, Watch: watch, Beneficiary: seats[0], MaxPerMessage: [2]uint64{defaultMaxPerMessage, defaultMaxPerMessage}, DayCap: [2]uint64{defaultDayCap, defaultDayCap},
		TrustedFile: trustedFile(t, dir, time.Now(), false), Name0: "GNOT", Symbol0: "GNOT", Name1: "BUBBLE", Symbol1: "BUBBLE",
		Send: ethx.SendConfig{Poll: 50 * time.Millisecond}}
}

func TestAnvilSetupAndAudit(t *testing.T) {
	arts := artifactsOrSkip(t)
	url, raw := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)

	var out bytes.Buffer
	sent, err := runSetup(ctx, cfg, &out)
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out.String())
	}
	if sent < 25 || !strings.Contains(out.String(), fmt.Sprintf("hleth setup: DONE (sent %d transactions)", sent)) {
		t.Fatalf("setup sent %d:\n%s", sent, out.String())
	}
	// resumable: a second run reads the chain and sends nothing
	out.Reset()
	if sent, err = runSetup(ctx, cfg, &out); err != nil || sent != 0 || !strings.Contains(out.String(), "hleth setup: DONE (sent nothing: every step was done already)") {
		t.Fatalf("the second setup sent %d (%v):\n%s", sent, err, out.String())
	}
	// a resumed setup with other launch values is refused before anything is sent
	other := cfg
	other.Hlgov = testNS + "hlgov2"
	out.Reset()
	if _, err := runSetup(ctx, other, &out); err == nil || !strings.Contains(err.Error(), "the launch values given differ") {
		t.Fatalf("a resume with another r/hlgov: %v", err)
	}

	acfg := auditConfig{RPC: url, Deployment: cfg.Out, ContractsOut: cfg.ContractsOut, HyperlaneDir: cfg.HyperlaneDir, At: "latest"}
	audit := func() ([]string, string) {
		t.Helper()
		var b bytes.Buffer
		fails, err := runAudit(ctx, acfg, &b)
		if err != nil {
			t.Fatalf("audit: %v\n%s", err, b.String())
		}
		return fails, b.String()
	}
	// with no gno source, no path flag and no launch value of the member's nothing fails, yet the verdict is no PASS: the
	// trust root, the bindings, the seats and watch keys, the beneficiary and both tokens' limits rest on the deployer's
	// record alone (E3-CF2, E3-GS4, E4-TL1)
	fails, text := audit()
	if len(fails) != 0 || strings.Contains(text, "hleth audit: PASS") || !strings.Contains(text, "hleth audit: UNVERIFIED (9)") ||
		!strings.Contains(text, "UNVERIFIED the client's trust root — the header (height 100") || !strings.Contains(text, "UNVERIFIED r/hlgov's path (-hlgov): ") ||
		!strings.Contains(text, "UNVERIFIED the guardians and watch keys at launch (") || !strings.Contains(text, "UNVERIFIED the protocol fee's beneficiary at launch (") ||
		!strings.Contains(text, "UNVERIFIED token 1's launch limits (25000000000 a message, 100000000000 a day): the deployment's record, bounded only by §5.4's ceilings") {
		t.Fatalf("the audit of a fresh setup with no gno source and no path flag: %d failures\n%s", len(fails), text)
	}
	if !strings.Contains(text, `  rpc> curl -s "$RPC" -H 'content-type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"eth_getStorageAt"`) {
		t.Fatalf("the audit prints no rpc line:\n%s", text)
	}
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "  rpc>") && strings.Contains(l, "127.0.0.1") {
			t.Fatalf("an rpc line names the provider (it must say $RPC): %s", l)
		}
	}
	// the nonce plan (fold F4): the ProxyAdmin at the deployer's nonce 0, its hand-over at 1, the thirteen creations
	// of steps 2–6 at 2–14 (the verifier at 6), HlAdmin at 15 — the address the ProxyAdmin went to — and HlAdminExec
	// HlAdmin's own CREATE; the verifier held three ways; HlAdmin's and HlAdminExec's immutables each beside its value
	dep, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	deployer := common.HexToAddress(dep.Deployer)
	for f, i := range map[string]uint64{"proxyAdmin": 0, "mailboxImpl": 2, "mailbox": 3, "merkleTreeHook": 4, "protocolFee": 5, "ed25519Verifier": 6, "clientImpl": 7, "client": 8,
		"token0Impl": 9, "token0": 10, "token1Impl": 11, "token1": 12, "gateImpl": 13, "gate": 14, "hlAdmin": 15} {
		if want := crypto.CreateAddress(deployer, i); dep.Addr(f) != want {
			t.Fatalf("%s at %s, not the deployer's nonce %d (%s)", f, dep.Addr(f).Hex(), i, want.Hex())
		}
	}
	if dep.FirstNonce != 0 || dep.AdminNonce != 15 || common.HexToAddress(dep.AdminAt) != dep.Addr("hlAdmin") {
		t.Fatalf("the plan: first nonce %d, HlAdmin's %d at %s (HlAdmin is %s)", dep.FirstNonce, dep.AdminNonce, dep.AdminAt, dep.Addr("hlAdmin").Hex())
	}
	cn, err := dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st := dep.stepOf("proxyAdminHandOver"); st == nil || st.Tx == "" {
		t.Fatalf("no hand-over recorded: %+v", dep.Steps)
	} else if tx, err := cn.c.Transaction(ctx, common.HexToHash(st.Tx)); err != nil || tx.Nonce != 1 || tx.To == nil || *tx.To != dep.Addr("proxyAdmin") {
		t.Fatalf("the hand-over %s: %+v (%v)", st.Tx, tx, err)
	}
	if want := crypto.CreateAddress(dep.Addr("hlAdmin"), 1); dep.Addr("hlAdminExec") != want || len(contractFields) != 16 {
		t.Fatalf("HlAdminExec at %s, not HlAdmin's CREATE at its nonce 1 (%s); %d contracts", dep.Addr("hlAdminExec").Hex(), want.Hex(), len(contractFields))
	}
	if dep.Launch.TrustedHeight != 100 || dep.Launch.TrustedTime == 0 || dep.Launch.GnoChainID != "dev" || len(dep.Launch.TrustedLeaves) != 16 {
		t.Fatalf("the launch's trusted header: %+v", dep.Launch)
	}
	// the first block is the ProxyAdmin's creation's; eth.deployBlock is finishSetup's (E2-RT6: every machine's scan
	// starts there), and the machines' config part written by -config-out names it
	if st := dep.stepOf("proxyAdmin"); st == nil || dep.FirstBlock != st.Block {
		t.Fatalf("the first block %d is not the ProxyAdmin's creation's: %+v", dep.FirstBlock, st)
	}
	if st := dep.stepOf("finishSetup"); st == nil || dep.DeployBlock != st.Block || dep.DeployBlock <= dep.FirstBlock {
		t.Fatalf("eth.deployBlock %d is not finishSetup's block: %+v", dep.DeployBlock, st)
	}
	var mc struct {
		Eth struct {
			DeployBlock uint64 `json:"deployBlock"`
			Admin       string `json:"admin"`
			Mailbox     string `json:"mailbox"`
		} `json:"eth"`
		Routes []struct {
			Symbol, Token string
		} `json:"routes"`
	}
	if b, err := os.ReadFile(cfg.ConfigOut); err != nil || json.Unmarshal(b, &mc) != nil {
		t.Fatalf("the machines' config part %s: %v", cfg.ConfigOut, err)
	}
	if mc.Eth.DeployBlock != dep.DeployBlock || mc.Eth.Admin != dep.HlAdmin || mc.Eth.Mailbox != dep.Mailbox || len(mc.Routes) != 2 || mc.Routes[1].Token != dep.Token1 {
		t.Fatalf("the machines' config part: %+v", mc)
	}
	verifier := dep.Addr("ed25519Verifier")
	for _, want := range []string{
		"ok   the client's ED25519() is " + verifier.Hex(),
		"ok   HlAdmin's ed25519() is " + verifier.Hex(),
		"ok   the verifier's EXTCODEHASH " + crypto.Keccak256Hash(arts["Ed25519Verifier"].Runtime).Hex() + " is this tree's build",
		"ok   HlAdmin's ed25519CodeHash() " + crypto.Keccak256Hash(arts["Ed25519Verifier"].Runtime).Hex() + " is the verifier's EXTCODEHASH",
		"ok   HlAdmin's localDomain() is 31337",
		"ok   ed25519Verifier (" + verifier.Hex() + ") code is the build of Ed25519Verifier byte for byte",
		"ok   hlAdmin's masked immutable HlAdmin.ed25519CodeHash = " + crypto.Keccak256Hash(arts["Ed25519Verifier"].Runtime).Hex(),
		"ok   hlAdmin's masked immutable HlAdmin.localDomain = 31337",
		"ok   hlAdminExec's masked immutable HlAdminExec.dayCap1 = 100000000000",
		"ok   hlAdminExec's masked immutable HlAdminExec.launch = 1",
		"ok   clientImpl's masked immutable GnoLightClient.ED25519 = " + verifier.Hex(),
		// the history (fold F4)
		"ok   hlAdmin is " + dep.Addr("hlAdmin").Hex() + ", the deployer's CREATE at nonce 15",
		"ok   HlAdmin is " + dep.Addr("hlAdmin").Hex() + ", the plan's address (the deployer's nonce 15 = its first 0 + 15)",
		"ok   the ProxyAdmin's ownership moved 2 time(s) since its creation",
		"ok   the ProxyAdmin's first owner is the deployer",
		"ok   the ProxyAdmin went from the deployer to HlAdmin " + dep.Addr("hlAdmin").Hex(),
		"ok   the hand-over is the deployer's transaction at nonce 1",
		"ok   token0 was upgraded 1 time(s): must be once",
		"ok   gate's admin changed 1 time(s)",
		"ok   the client's first record is header 100 ",
		"ok   HlAdmin's setup was done once (1 SetupDone logs)",
		fmt.Sprintf("ok   HlAdmin's SetupDone is at block %d, the record's eth.deployBlock %d", dep.DeployBlock, dep.DeployBlock),
		"ok   the mailbox logged no Dispatch and no Process before the setup was done",
		// the creation transactions (E2-RT1): each input the build's creation code and the setup's arguments
		"ok   proxyAdmin's creation input is the build of ProxyAdmin and the constructor's arguments the setup used, byte for byte",
		"ok   mailbox's creation input is the build of TransparentUpgradeableProxy and the constructor's arguments the setup used, byte for byte",
		"ok   client's creation input is the build of TransparentUpgradeableProxy and the constructor's arguments the setup used, byte for byte",
		"ok   clientImpl's creation input is the build of GnoLightClient and the constructor's arguments the setup used, byte for byte",
		"ok   hlAdmin's creation input is the build of HlAdmin and the constructor's arguments the setup used, byte for byte",
		"ok   hlAdmin's creation " + dep.stepOf("hlAdmin").Tx + " is the deployer's top-level creation at its planned nonce 15",
		"ok   hlAdmin's creation receipt created " + dep.Addr("hlAdmin").Hex(),
		"ok   hlAdminExec " + dep.Addr("hlAdminExec").Hex() + " is created by HlAdmin's constructor",
		// every proxy initialized in its construction
		"ok   mailbox refuses initialize from a stranger",
		"ok   token0 refuses initialize from a stranger",
		"ok   token1 refuses initialize from a stranger",
		"ok   client refuses initialize from a stranger",
		"ok   gate refuses initialize from a stranger",
		"ok   clientImpl refuses initialize from a stranger",
		// fold F5's three immutables, setupDone proved by finishSetup itself, the launch line, the record explained
		"ok   hlAdmin's masked immutable HlAdmin._execCodeHash = ", "ok   hlAdmin's masked immutable HlAdmin._startSequence = ", "ok   hlAdmin's masked immutable HlAdmin.mailboxPkgPathHash = ",
		"ok   HlAdmin's SetupDone is finishSetup's: logged by a top-level call of finishSetup() on HlAdmin",
		"ok   finishSetup passes again on block " + fmt.Sprint(dep.DeployBlock) + "'s state with setupDone stored false",
		"ok   HlAdmin's launchNonce() is 0, the number of messages the mailbox dispatched before SetupDone (0)",
		"ok   HlAdmin's lastSeq() ", "ok   HlAdmin's pending() 0 is at most the 0 decision(s)", "ok   HlAdmin's 0 cancelled range(s)", "ok   HlAdmin's standing flags (none)", "ok   HlAdmin's hold history (0)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the audit lacks %q:\n%s", want, text)
		}
	}
	// a verifier whose code is not this build's (one byte changed: anvil sets the code): the audit fails on its code,
	// its hash against the build and against HlAdmin's ed25519CodeHash() — and finishSetup, run again on that state
	// (SetupDone's block is the latest here), refuses it check 28 (fold F7: the audit takes no setupDone as proof)
	var vcode hexutil.Bytes
	if err := raw.Call(&vcode, "eth_getCode", verifier, "latest"); err != nil {
		t.Fatal(err)
	}
	bad := append(hexutil.Bytes{}, vcode...)
	bad[len(bad)/2] ^= 0xff
	if err := raw.Call(nil, "anvil_setCode", verifier, bad); err != nil {
		t.Fatal(err)
	}
	if fails, text = audit(); len(fails) != 4 || !strings.Contains(text, "FAIL the verifier's EXTCODEHASH") || !strings.Contains(text, "FAIL HlAdmin's ed25519CodeHash()") || !strings.Contains(text, "FAIL ed25519Verifier ("+verifier.Hex()+") code against the build of Ed25519Verifier: 1 bytes differ") ||
		!strings.Contains(text, "FAIL finishSetup refuses again on block "+fmt.Sprint(dep.DeployBlock)+"'s state with setupDone false: reverts: HlAdmin.SetupIncomplete 28") {
		t.Fatalf("a verifier that is not this build: %d failures\n%s", len(fails), text)
	}
	if err := raw.Call(nil, "anvil_setCode", verifier, vcode); err != nil {
		t.Fatal(err)
	}
	// the audit against another r/hlgov path fails on every place that binds it
	acfg.Hlgov = testNS + "hlgov2"
	if fails, text = audit(); len(fails) < 2 || !strings.Contains(text, "FAIL HlAdmin's hlgov() is") || !strings.Contains(text, "HlAdmin.hlgov = 0x") {
		t.Fatalf("an audit against another r/hlgov: %d failures\n%s", len(fails), text)
	}
	acfg.Hlgov = ""

	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	// with a gno node: the trust root against the node's block and validators at its height, the trusted hash against
	// the node's block, HlAdmin's counter against r/hlgov's, the seats against r/hlgov's — equal passes, a gap fails,
	// an absent realm is said so
	fg := honestGno(t, cfg, d)
	acfg.GnoRPC = fg.serve(t).URL
	if fails, text = audit(); len(fails) != 0 || !strings.Contains(text, "ok   the line's last number 0 is r/hlgov's counter 0") || !strings.Contains(text, "ok   the client's trusted hash at height 100 is gno.land's block hash") ||
		!strings.Contains(text, "ok   the trust root's hash "+d.Launch.TrustedHash+" is gno.land's block 100 hash") || !strings.Contains(text, "ok   seat 6 is "+common.HexToAddress(anvilAccounts[5]).Hex()) {
		t.Fatalf("the audit with a gno node: %d failures\n%s", len(fails), text)
	}
	fg.last = 3
	if fails, text = audit(); len(fails) != 1 || !strings.Contains(fails[0], "the line's last number 0 is r/hlgov's counter 3") {
		t.Fatalf("a counter gap: %d failures\n%s", len(fails), text)
	}
	fg.last, fg.absent = 0, true
	if fails, text = audit(); len(fails) != 0 || !strings.Contains(text, "is not on the chain yet") {
		t.Fatalf("r/hlgov absent: %d failures\n%s", len(fails), text)
	}
	acfg.GnoRPC = ""
	// the setup refuses a start sequence that is not r/hlgov's counter, before anything is sent
	fg.absent, fg.last = false, 2
	fresh := testSetupConfig(t, url, t.TempDir())
	fresh.GnoRPC = srvURL(t, fg)
	nonce, _ := nonceOf(t, raw, common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"))
	out.Reset()
	if _, err := runSetup(ctx, fresh, &out); err == nil || !strings.Contains(err.Error(), "-start-sequence 0 is not r/hlgov's counter (last 2") {
		t.Fatalf("a start sequence below r/hlgov's counter: %v\n%s", err, out.String())
	}
	if n, _ := nonceOf(t, raw, common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")); n != nonce {
		t.Fatalf("the refused setup sent %d transactions", n-nonce)
	}
	// a token's ISM changed under HlAdmin's authority (anvil impersonates it): the audit fails on it
	admin := d.Addr("hlAdmin")
	if err := raw.Call(nil, "anvil_impersonateAccount", admin); err != nil {
		t.Fatal(err)
	}
	if err := raw.Call(nil, "anvil_setBalance", admin, "0x56BC75E2D63100000"); err != nil {
		t.Fatal(err)
	}
	// (Hyperlane's setter takes a contract or zero: another contract of the deployment stands in for a rogue ISM)
	rogue := d.Addr("merkleTreeHook")
	data, _ := ethx.HypERC20.Pack("setInterchainSecurityModule", rogue)
	var h common.Hash
	if err := raw.Call(&h, "eth_sendTransaction", map[string]any{"from": admin, "to": d.Addr("token0"), "data": hexutil.Bytes(data)}); err != nil {
		t.Fatal(err)
	}
	waitReceipt(t, raw, h)
	if fails, text = audit(); len(fails) != 1 || !strings.Contains(fails[0], "token 0's ISM is "+rogue.Hex()) {
		t.Fatalf("a token's ISM moved off the gate: %d failures\n%s", len(fails), text)
	}
	// the ISM put back; then a token's implementation code with a stand-in mailbox in its immutable (anvil sets
	// the code): the masked value fails, named, with both values
	data, _ = ethx.HypERC20.Pack("setInterchainSecurityModule", d.Addr("gate"))
	if err := raw.Call(&h, "eth_sendTransaction", map[string]any{"from": admin, "to": d.Addr("token0"), "data": hexutil.Bytes(data)}); err != nil {
		t.Fatal(err)
	}
	waitReceipt(t, raw, h)
	var code hexutil.Bytes
	if err := raw.Call(&code, "eth_getCode", d.Addr("token1Impl"), "latest"); err != nil {
		t.Fatal(err)
	}
	stand := common.HexToAddress("0x000000000000000000000000000000000000bEEF")
	for _, im := range arts["HypERC20"].Immutables {
		if im.Name != "MailboxClient.mailbox" {
			continue
		}
		for _, r := range im.Refs {
			copy(code[r.Start:r.Start+32], common.LeftPadBytes(stand.Bytes(), 32))
		}
	}
	orig := append(hexutil.Bytes{}, code...)
	if err := raw.Call(&orig, "eth_getCode", d.Addr("token1Impl"), "latest"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Call(nil, "anvil_setCode", d.Addr("token1Impl"), code); err != nil {
		t.Fatal(err)
	}
	fails, text = audit()
	want := fmt.Sprintf("token1Impl's masked immutable MailboxClient.mailbox = 0x000000000000000000000000000000000000bEEF, but it must be %s (our mailbox)", d.Addr("mailbox").Hex())
	found := false
	for _, f := range fails {
		found = found || f == want
	}
	if !found {
		t.Fatalf("a token over a stand-in mailbox: %d failures, want %q\n%s", len(fails), want, text)
	}
	if err := raw.Call(nil, "anvil_setCode", d.Addr("token1Impl"), orig); err != nil {
		t.Fatal(err)
	}
	if fails, text = audit(); len(fails) != 0 {
		t.Fatalf("the code put back: %d failures\n%s", len(fails), text)
	}

	// the history (fold F4): what no state shows. Under HlAdmin's authority (anvil impersonates it) the ProxyAdmin is
	// handed to a stranger and back, then token 0 is "upgraded" to its own implementation again: every owner, slot and
	// implementation reads as it did, and the audit fails on the logs alone
	send := func(from, to common.Address, data []byte) {
		t.Helper()
		var h common.Hash
		if err := raw.Call(&h, "eth_sendTransaction", map[string]any{"from": from, "to": to, "data": hexutil.Bytes(data)}); err != nil {
			t.Fatal(err)
		}
		waitReceipt(t, raw, h)
	}
	stranger := common.HexToAddress("0x14dC79964da2C08b23698B3D3cc7Ca32193d9955") // anvil's account 7: no seat, no watch slot
	data, _ = ethx.ProxyAdmin.Pack("transferOwnership", stranger)
	send(admin, d.Addr("proxyAdmin"), data)
	data, _ = ethx.ProxyAdmin.Pack("transferOwnership", admin)
	send(stranger, d.Addr("proxyAdmin"), data)
	if fails, text = audit(); len(fails) != 1 || !strings.Contains(fails[0], "the ProxyAdmin's ownership moved 4 time(s) since its creation (must be 2") || !strings.Contains(text, "ok   the ProxyAdmin's owner is "+admin.Hex()) {
		t.Fatalf("the ProxyAdmin away and back: %d failures\n%s", len(fails), text)
	}
	data, _ = ethx.ProxyAdmin.Pack("upgrade", d.Addr("token0"), d.Addr("token0Impl"))
	send(admin, d.Addr("proxyAdmin"), data)
	if fails, text = audit(); len(fails) != 2 || !strings.Contains(fails[1], "token0 was upgraded 2 time(s): must be once, to "+d.Addr("token0Impl").Hex()) || !strings.Contains(text, "ok   token0's implementation slot holds "+d.Addr("token0Impl").Hex()) {
		t.Fatalf("a proxy upgraded to its own implementation again: %d failures\n%s", len(fails), text)
	}
}

// The setup holds the nonce plan and the trusted header (fold F4): a header that is old or announces a change is
// refused before the client's proxy is sent, whatever its source; a transaction outside the plan (the deployer's own,
// here a transfer) stops the setup before it sends anything more — HlAdmin could no longer land at the address the
// ProxyAdmin went to, and the ProxyAdmin is that address's from the deployer's second transaction.
func TestAnvilSetupHoldsThePlanAndTheTrustedHeader(t *testing.T) {
	arts := artifactsOrSkip(t)
	url, raw := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cfg := testSetupConfig(t, url, dir)
	me := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	nonce := func() uint64 {
		n, err := nonceOf(t, raw, me)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	var out bytes.Buffer
	cfg.TrustedFile = trustedFile(t, dir, time.Now().Add(-2*time.Hour), false)
	if _, err := runSetup(ctx, cfg, &out); err == nil || !strings.Contains(err.Error(), "the trusted header 100 (from "+cfg.TrustedFile+") is of ") || !strings.Contains(err.Error(), "read a newer header") {
		t.Fatalf("a header two hours old: %v\n%s", err, out.String())
	}
	if n := nonce(); n != 8 {
		t.Fatalf("the setup stopped at the client's proxy with the deployer's nonce %d (the plan's 8)", n)
	}
	d, err := LoadDeployment(cfg.Out, false)
	if err != nil {
		t.Fatal(err)
	}
	at := crypto.CreateAddress(me, 15)
	if d.AdminAt != at.Hex() || d.Addr("clientImpl") == (common.Address{}) || d.Addr("client") != (common.Address{}) {
		t.Fatalf("the record after the refusal: %+v", d)
	}
	cn, err := dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := read[common.Address](ctx, cn, ethx.Latest, d.Addr("proxyAdmin"), arts["ProxyAdmin"], "owner"); err != nil || owner != at {
		t.Fatalf("the ProxyAdmin's owner is %s (%v), not HlAdmin's planned address %s, before any proxy", owner.Hex(), err, at.Hex())
	}
	out.Reset()
	cfg.TrustedFile = trustedFile(t, dir, time.Now(), true)
	if _, err := runSetup(ctx, cfg, &out); err == nil || !strings.Contains(err.Error(), "announces a set change") {
		t.Fatalf("an announcing header: %v\n%s", err, out.String())
	}
	if n := nonce(); n != 8 {
		t.Fatalf("the announcing header's refusal moved the nonce to %d", n)
	}
	// a transaction outside the plan: nothing more is sent, the error names the plan
	var h common.Hash
	if err := raw.Call(&h, "eth_sendTransaction", map[string]any{"from": me, "to": me, "value": "0x1"}); err != nil {
		t.Fatal(err)
	}
	waitReceipt(t, raw, h)
	out.Reset()
	cfg.TrustedFile = trustedFile(t, dir, time.Now(), false)
	if _, err := runSetup(ctx, cfg, &out); !errors.Is(err, errPlan) || !strings.Contains(err.Error(), "the light client's proxy goes at the deployer's nonce 8, and its nonce is 9") {
		t.Fatalf("a nonce off the plan: %v\n%s", err, out.String())
	}
	if n := nonce(); n != 9 {
		t.Fatalf("the refused setup sent %d transactions", n-9)
	}
}

func srvURL(t *testing.T, f *fakeGno) string { return f.serve(t).URL }

// honestGno is gno.land as an honest deployment's record has it: the trusted file's header at the trusted height,
// the record's validators, r/hlgov up with its counter at the start, HlAdmin its decision recipient, the record's
// seats its Ethereum seats.
func honestGno(t *testing.T, cfg setupConfig, d *Deployment) *fakeGno {
	t.Helper()
	fg := &fakeGno{hashes: map[int64][]byte{}, headers: map[int64]json.RawMessage{d.Launch.TrustedHeight: headerOf(t, cfg.TrustedFile)}, hlgov: cfg.Hlgov,
		recipient: d.Addr("hlAdmin").Hex(), decisions: map[uint64]string{}, renders: map[string]string{}}
	for _, v := range d.Launch.Validators {
		pk, _ := hl.ParseKey32(v.PubKey)
		fg.validators = append(fg.validators, pk)
	}
	for _, k := range append(d.Launch.Seats[:], d.Launch.Watch[:]...) {
		fg.seats = append(fg.seats, common.HexToAddress(k).Hex())
	}
	return fg
}

// headerOf is a trusted file's header (/block's block.header, amino's JSON).
func headerOf(t *testing.T, path string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var j trustedFileJSON
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatal(err)
	}
	return j.Header
}

func nonceOf(t *testing.T, c *rpc.Client, a common.Address) (uint64, error) {
	t.Helper()
	var n hexutil.Uint64
	err := c.Call(&n, "eth_getTransactionCount", a, "latest")
	return uint64(n), err
}

func waitReceipt(t *testing.T, c *rpc.Client, h common.Hash) {
	t.Helper()
	for i := 0; i < 100; i++ {
		var rc map[string]any
		if err := c.Call(&rc, "eth_getTransactionReceipt", h); err == nil && rc != nil {
			if rc["status"] != "0x1" {
				t.Fatalf("tx %s reverted", h.Hex())
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("tx %s not mined", h.Hex())
}

// A creation broadcast and not seen mined when the run stopped is resumed by the next: adopted once mined, sent
// again when the pool dropped it — never sent twice at two addresses.
func TestAnvilResumesAPendingCreation(t *testing.T) {
	arts := artifactsOrSkip(t)
	url, raw := startAnvil(t)
	dir := t.TempDir()
	ctx := context.Background()
	cn, err := dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ethx.LoadKey(deployerKey(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	say := func(f string, a ...any) { fmt.Fprintf(&log, f+"\n", a...) }
	tx, err := newTxer(cn, key, ethx.SendConfig{Poll: 50 * time.Millisecond, Wait: 400 * time.Millisecond, ReplaceAfter: time.Hour}, say)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "deployment.json")
	d := &Deployment{}
	save := func() error { return d.Save(path) }
	if err := raw.Call(nil, "evm_setAutomine", false); err != nil {
		t.Fatal(err)
	}
	// 1. still pending when the run stops: the record names the nonce and the address it lands at
	_, _, err = tx.create(ctx, d, save, "proxyAdmin", "the ProxyAdmin", arts["ProxyAdmin"].Creation, 0)
	if !errors.Is(err, ethx.ErrStillPending) || d.Pending == nil || d.Pending.Nonce != 0 {
		t.Fatalf("a creation unmined: %v, pending %+v", err, d.Pending)
	}
	predicted := d.Pending.Address
	if d2, err := LoadDeployment(path, false); err != nil || d2.Pending == nil || d2.Pending.Address != predicted {
		t.Fatalf("the record on disk: %+v (%v)", d2, err)
	}
	if err := raw.Call(nil, "evm_mine"); err != nil {
		t.Fatal(err)
	}
	if err := tx.resumeCreate(ctx, d, save); err != nil {
		t.Fatalf("resume: %v\n%s", err, log.String())
	}
	if d.Pending != nil || d.ProxyAdmin != predicted || d.FirstBlock == 0 {
		t.Fatalf("adopted: %+v", d)
	}
	// 2. dropped from the pool before it was mined: the next run sends it again at its nonce, at the same address
	_, _, err = tx.create(ctx, d, save, "mailboxImpl", "the Mailbox's implementation", mustCreation(t, arts["Mailbox"], Values{"localdomain": uint32(31337)}), 1)
	if !errors.Is(err, ethx.ErrStillPending) || d.Pending == nil {
		t.Fatalf("a second creation unmined: %v", err)
	}
	predicted = d.Pending.Address
	for _, h := range d.Pending.Hashes {
		if err := raw.Call(nil, "anvil_dropTransaction", h); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Call(nil, "evm_setAutomine", true); err != nil {
		t.Fatal(err)
	}
	if err := tx.resumeCreate(ctx, d, save); err != nil {
		t.Fatalf("resume after a drop: %v\n%s", err, log.String())
	}
	if d.Pending != nil || d.MailboxImpl != predicted {
		t.Fatalf("sent again and adopted: %+v\n%s", d, log.String())
	}
	var code hexutil.Bytes
	if err := raw.Call(&code, "eth_getCode", common.HexToAddress(predicted), "latest"); err != nil || len(code) == 0 {
		t.Fatalf("no code at %s: %v", predicted, err)
	}
	if n, err := cn.c.NonceAt(ctx, tx.from, ethx.Latest); err != nil || n != 2 {
		t.Fatalf("the deployer's nonce is %d (%v): a creation was sent twice", n, err)
	}
}

func mustCreation(t *testing.T, a *Artifact, v Values) []byte {
	t.Helper()
	b, err := a.CreationCode(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
