package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
)

// deployment.json: what `hleth setup` deployed and with which launch values, written after every step (so a
// stopped setup resumes from it and from the chain), read by `hleth audit`, `hleth decision` and the local run,
// which builds the machines' bridge.json from it. It holds no secret: addresses, numbers and transaction hashes.

// Deployment is one Ethereum deployment of the bridge.
type Deployment struct {
	ChainID     uint64 `json:"chainId"`
	LocalDomain uint32 `json:"localDomain"` // Ethereum's Hyperlane domain: the mailbox's
	GnoDomain   uint32 `json:"gnoDomain"`   // gno.land's: the client's origin, the routers' domain
	Deployer    string `json:"deployer"`
	// FirstBlock is the block of the deployment's first transaction (the ProxyAdmin's creation): where the audit's
	// reading of the setup's history starts.
	FirstBlock uint64 `json:"firstBlock"`
	// DeployBlock is the block of HlAdmin.finishSetup (PLAN.md §5.6, §5.8 step 10): where every scan of the signers,
	// the monitors, the inbound relayer and hlwatch starts. The scans' start only: the launch line — HlAdmin's
	// launchNonce(), the mailbox's nonce finishSetup recorded — draws what is no burn of this deployment (a message
	// below it, the deployer's while it owned the mailbox and the tokens, can sit in this very block); `hleth audit`
	// lists every message below it. 0 until finishSetup is mined.
	DeployBlock uint64 `json:"deployBlock"`
	// The nonce plan (M2-NOTES.md, Fold F4): FirstNonce creates the ProxyAdmin, FirstNonce + 1 hands it to AdminAt,
	// steps 2–6's thirteen creations follow, and AdminNonce (FirstNonce + 15) creates HlAdmin at AdminAt =
	// CREATE(deployer, AdminNonce). The plan is fixed before the ProxyAdmin is sent.
	FirstNonce uint64 `json:"firstNonce"`
	AdminNonce uint64 `json:"adminNonce"`
	AdminAt    string `json:"adminAt"`
	// Factor is the clock factor the contracts were built with (read from the client after its creation).
	Factor uint64 `json:"factor"`

	// The sixteen deployments of §5.8 (M2-NOTES.md §1 G: eleven codes, five proxies): fifteen created from the
	// deployer key, and the HlAdminExec HlAdmin's constructor creates.
	ProxyAdmin     string `json:"proxyAdmin"`
	MailboxImpl    string `json:"mailboxImpl"`
	Mailbox        string `json:"mailbox"`
	MerkleTreeHook string `json:"merkleTreeHook"`
	ProtocolFee    string `json:"protocolFee"`
	ClientImpl     string `json:"clientImpl"`
	Client         string `json:"client"`
	Token0Impl     string `json:"token0Impl"`
	Token0         string `json:"token0"`
	Token1Impl     string `json:"token1Impl"`
	Token1         string `json:"token1"`
	GateImpl       string `json:"gateImpl"`
	Gate           string `json:"gate"`
	HlAdmin        string `json:"hlAdmin"`
	HlAdminExec    string `json:"hlAdminExec"`
	// Ed25519Verifier is the signature verifier the client's implementation calls (lane E-M2a's Ed25519Verifier, its
	// address an immutable of the client and of HlAdmin, its EXTCODEHASH HlAdmin's ed25519CodeHash): deployed at step
	// 4 before the client.
	Ed25519Verifier string `json:"ed25519Verifier,omitempty"`

	Launch Launch `json:"launch"`
	// Steps are the steps done, in order, each with the transaction that did it (none for a step found done).
	Steps []Step `json:"steps"`
	// Pending is a contract creation broadcast and not yet seen mined: the next run adopts it or sends it again.
	Pending *PendingCreate `json:"pending,omitempty"`
}

// Launch are the values the setup was given, as it used them.
type Launch struct {
	GnoChainID    string          `json:"gnoChainId"`
	TrustedHeight int64           `json:"trustedHeight"`
	TrustedHash   string          `json:"trustedHash"`
	TrustedTime   int64           `json:"trustedTime"` // the trusted header's time (unix seconds), within the hour of step 4's block
	Validators    []ValidatorJSON `json:"validators"`
	// TrustedLeaves are the trusted header's sixteen field leaves as the client's initialize took them (hex): what its
	// proxy's creation input carries, which the audit rebuilds byte for byte (E2-RT1), and which hash to TrustedHash.
	TrustedLeaves      []string  `json:"trustedLeaves,omitempty"`
	MailboxPkgPath     string    `json:"mailboxPkgPath"`
	MailboxPkgPathHash string    `json:"mailboxPkgPathHash"` // keccak256 of the path: what the client compiles
	Hlgov              string    `json:"hlgov"`              // r/hlgov's pkgpath
	Hlgov32            string    `json:"hlgov32"`            // its address, 32 bytes left-padded
	GnotRoute          string    `json:"gnotRoute"`
	BubbleRoute        string    `json:"bubbleRoute"`
	Route0             string    `json:"route0"`          // r/hlgnot's address, 32 bytes
	Route1             string    `json:"route1"`          // r/hlbubble's
	Ed25519CodeHash    string    `json:"ed25519CodeHash"` // this build's Ed25519Verifier: the EXTCODEHASH HlAdmin pins
	Seats              [4]string `json:"seats"`
	Watch              [2]string `json:"watch"`
	MaxPerMessage      [2]uint64 `json:"maxPerMessage"`
	DayCap             [2]uint64 `json:"dayCap"`
	ExpectedLock       [2]uint64 `json:"expectedLock"` // the locks the limits were compiled from (E3-CF4; none on a local chain): the deployer's word, which the audit prints beside its own -expected-lock-*
	Beneficiary        string    `json:"beneficiary"`
	MaxProtocolFee     string    `json:"maxProtocolFee"` // wei, decimal
	Name0              string    `json:"name0"`
	Symbol0            string    `json:"symbol0"`
	Name1              string    `json:"name1"`
	Symbol1            string    `json:"symbol1"`
	StartSequence      uint64    `json:"startSequence"`
}

// ValidatorJSON is one validator of the trusted set: its ed25519 key in hex and its power.
type ValidatorJSON struct {
	PubKey string `json:"pubkey"`
	Power  int64  `json:"power"`
}

// Step is one step done.
type Step struct {
	Name  string `json:"name"`
	Tx    string `json:"tx,omitempty"`
	Block uint64 `json:"block,omitempty"`
}

// PendingCreate is a creation in flight, written before each version is broadcast: what the next run needs to find
// it mined (its nonce, the address CREATE gives it, every version's hash); the transaction itself — its fees, its
// bytes — is in the sender's state file (<out>.sender.json), which resumes its wait.
type PendingCreate struct {
	Field   string        `json:"field"` // the Deployment field it fills
	Label   string        `json:"label"`
	Nonce   uint64        `json:"nonce"`
	Address string        `json:"address"` // CREATE(deployer, nonce): where it lands
	Hashes  []common.Hash `json:"hashes"`  // every version signed for broadcast, the newest last
	FirstAt int64         `json:"firstAt"`
}

// LoadDeployment reads deployment.json; a missing file is an empty deployment when allowMissing.
func LoadDeployment(path string, allowMissing bool) (*Deployment, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return &Deployment{}, nil
	}
	if err != nil {
		return nil, err
	}
	var d Deployment
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("%s is corrupt: %v; restore it or delete it knowingly", path, err)
	}
	return &d, nil
}

// Save writes the deployment atomically (a temporary file, fsynced, renamed over the old one): a stop leaves the
// old file or the new one, never half of either.
func (d *Deployment) Save(path string) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil
}

// field is a pointer to a contract field by its JSON name ("mailbox", "token0Impl", …).
func (d *Deployment) field(name string) *string {
	switch name {
	case "proxyAdmin":
		return &d.ProxyAdmin
	case "mailboxImpl":
		return &d.MailboxImpl
	case "mailbox":
		return &d.Mailbox
	case "merkleTreeHook":
		return &d.MerkleTreeHook
	case "protocolFee":
		return &d.ProtocolFee
	case "clientImpl":
		return &d.ClientImpl
	case "client":
		return &d.Client
	case "token0Impl":
		return &d.Token0Impl
	case "token0":
		return &d.Token0
	case "token1Impl":
		return &d.Token1Impl
	case "token1":
		return &d.Token1
	case "gateImpl":
		return &d.GateImpl
	case "gate":
		return &d.Gate
	case "hlAdmin":
		return &d.HlAdmin
	case "hlAdminExec":
		return &d.HlAdminExec
	case "ed25519Verifier":
		return &d.Ed25519Verifier
	}
	return nil
}

// Addr is a recorded contract's address (zero when not recorded).
func (d *Deployment) Addr(name string) common.Address {
	p := d.field(name)
	if p == nil || *p == "" {
		return common.Address{}
	}
	return common.HexToAddress(*p)
}

// stepDone records a step done (once).
func (d *Deployment) stepDone(name, tx string, block uint64) {
	for i := range d.Steps {
		if d.Steps[i].Name == name {
			if tx != "" {
				d.Steps[i].Tx, d.Steps[i].Block = tx, block
			}
			return
		}
	}
	d.Steps = append(d.Steps, Step{Name: name, Tx: tx, Block: block})
}

// historyFrom is where the setup's history is read from: the deployment's first block (an earlier build's record
// kept it as deployBlock).
func (d *Deployment) historyFrom() uint64 {
	if d.FirstBlock != 0 {
		return d.FirstBlock
	}
	return d.DeployBlock
}

// stepOf is a recorded step, nil if none.
func (d *Deployment) stepOf(name string) *Step {
	for i := range d.Steps {
		if d.Steps[i].Name == name {
			return &d.Steps[i]
		}
	}
	return nil
}

// contractFields are the deployment's sixteen contracts in deploy order, by their JSON names (hlAdminExec is
// HlAdmin's creation).
var contractFields = []string{"proxyAdmin", "mailboxImpl", "mailbox", "merkleTreeHook", "protocolFee", "ed25519Verifier", "clientImpl", "client",
	"token0Impl", "token0", "token1Impl", "token1", "gateImpl", "gate", "hlAdmin", "hlAdminExec"}
