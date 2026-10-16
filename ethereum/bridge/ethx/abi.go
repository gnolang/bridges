package ethx

import (
	"bytes"
	"embed"
	"fmt"
	"sort"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// The ABIs the bridge's Go side speaks (abi/*.json, written by abi/gen.sh on the devbox):
//
//   - our three contracts, GnoLightClient, PausableGate and HlAdmin: the shared interfaces of
//     contracts/src/interfaces (IGnoLightClient.abi.json …; forge inspect, solc 0.8.33) — the interface is the
//     contract the contracts, the Go machines and the tests share — and the implementations' (GnoLightClient.abi.json
//     …, with HlAdmin's delegate HlAdminExec, and the Guardians library's), which win where present, since they add
//     the custom errors a revert is named by (OverDayCap, NotMature, RetiredAdmin, NoCodeYet …). Written by
//     abi/gen.sh from this tree's contracts (fold F3, 2026-10-14: the merged tree, F2's NoCodeYet in);
//   - Hyperlane's Mailbox, HypERC20, MerkleTreeHook and ProtocolFee, and OpenZeppelin 4.9.3's ProxyAdmin and
//     TransparentUpgradeableProxy: compiled by solc 0.8.33+commit.64118f21 from @hyperlane-xyz/core@12.2.0's own
//     compiler input (the npm tarball's dist/buildArtifact.json), output "abi" — the release's real compile.

//go:embed abi/*.json
var abiFS embed.FS

// Contract is one contract's ABI by name.
type Contract struct {
	Name string
	ABI  abi.ABI
}

func loadABI(names ...string) *Contract {
	if c := loadOptional(names...); c != nil {
		return c
	}
	panic(fmt.Sprintf("ethx: no ABI among %v", names))
}

// loadOptional is the first of the named ABIs that is embedded, nil when none is.
func loadOptional(names ...string) *Contract {
	for _, n := range names {
		b, err := abiFS.ReadFile("abi/" + n + ".abi.json")
		if err != nil {
			continue
		}
		a, err := abi.JSON(bytes.NewReader(b))
		if err != nil {
			panic(fmt.Sprintf("ethx: abi/%s.abi.json: %v", n, err))
		}
		return &Contract{Name: names[0], ABI: a}
	}
	return nil
}

var (
	// LightClient, Gate and Admin: the implementation's ABI when it is here, else the interface's.
	LightClient = loadABI("GnoLightClient", "IGnoLightClient")
	Gate        = loadABI("PausableGate", "IPausableGate")
	Admin       = loadABI("HlAdmin", "IHlAdmin")

	Mailbox        = loadABI("Mailbox")
	HypERC20       = loadABI("HypERC20")
	MerkleTreeHook = loadABI("MerkleTreeHook")
	ProtocolFee    = loadABI("ProtocolFee")
	ProxyAdmin     = loadABI("ProxyAdmin")
	Proxy          = loadABI("TransparentUpgradeableProxy")

	// AdminExec is HlAdmin's delegate (lane E-M2b's HlAdminExec, which HlAdmin.execute delegatecalls): its errors
	// come back through HlAdmin's (the file-level SetupIncomplete, NotDelegated, AuthorityLost, NoCodeYet among
	// them); nil until its ABI is embedded.
	AdminExec = loadOptional("HlAdminExec")

	// Guardians is the seats' library HlAdmin and the gate share (src/lib/Guardians.sol): its ABI names every error of
	// the seats, some of which the code raises by selector (CurrentKey, TooManyGuardians, TooManyWatch), so no
	// contract's own ABI lists them.
	Guardians = loadOptional("Guardians")
)

// extraContracts are ABIs a revert may be named by beyond the embedded ones (the tests' custom errors).
var extraContracts []*Contract

func allContracts() []*Contract {
	out := []*Contract{LightClient, Gate, Admin, Mailbox, HypERC20, MerkleTreeHook, ProtocolFee, ProxyAdmin, Proxy}
	for _, c := range []*Contract{AdminExec, Guardians} {
		if c != nil {
			out = append(out, c)
		}
	}
	return append(out, extraContracts...)
}

// Pack is a method's calldata; a name or arguments the ABI does not take is a programming error, answered as an
// error so a caller never sends half-built calldata.
func (c *Contract) Pack(method string, args ...any) ([]byte, error) {
	b, err := c.ABI.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", c.Name, method, err)
	}
	return b, nil
}

// Unpack decodes a method's return values.
func (c *Contract) Unpack(method string, data []byte) ([]any, error) {
	out, err := c.ABI.Unpack(method, data)
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", c.Name, method, err)
	}
	return out, nil
}

// Event is an event's topic 0 by name; a name the ABI lacks panics (a programming error).
func (c *Contract) Event(name string) common.Hash {
	e, ok := c.ABI.Events[name]
	if !ok {
		panic(fmt.Sprintf("ethx: %s has no event %s", c.Name, name))
	}
	return e.ID
}

// Selector is a method's 4-byte selector.
func (c *Contract) Selector(method string) [4]byte {
	m, ok := c.ABI.Methods[method]
	if !ok {
		panic(fmt.Sprintf("ethx: %s has no method %s", c.Name, method))
	}
	var s [4]byte
	copy(s[:], m.ID)
	return s
}

// ErrorNames are the custom errors the ABI names, sorted (what a revert of the contract can be called).
func (c *Contract) ErrorNames() []string {
	var out []string
	for n := range c.ABI.Errors {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
