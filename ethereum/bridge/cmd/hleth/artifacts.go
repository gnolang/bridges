package main

import (
	"fmt"
	"path/filepath"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/ethereum/bridge/repro"
)

// The code the setup deploys and the audit compares (PLAN.md §5.8, §5.9): our four contracts — GnoLightClient,
// PausableGate, HlAdmin and Ed25519Verifier — and HlAdminExec, which HlAdmin's constructor creates, from a forge `out`
// directory of eth/bridge/contracts (built with
// `forge build --force --ast`, so every artifact carries its source's AST and the immutables can be named), and
// Hyperlane 12.2.0's and OpenZeppelin 4.9.3's six from the release's own compile (local/hyperlane-artifacts.sh:
// one JSON per contract in forge's shape, with the immutables' names beside their references). The deploy uses the
// creation code; the audit compares the runtime code with every immutable masked and reads each masked value.

// The artifact, its reading and the packing by name are the repro package's (shared with hlwatch); these names keep
// the tool's own words for them.
type (
	Ref       = repro.Ref
	Immutable = repro.Immutable
	Artifact  = repro.Artifact
	Values    = repro.Values
)

func norm(s string) string { return repro.Norm(s) }

// hyperlaneNames are the six contracts the release's compile gives (local/hyperlane-artifacts.sh).
var hyperlaneNames = []string{"Mailbox", "HypERC20", "MerkleTreeHook", "ProtocolFee", "ProxyAdmin", "TransparentUpgradeableProxy"}

// ourNames are our contracts in a forge out directory (HlAdminExec is created by HlAdmin's constructor; the client
// calls Ed25519Verifier at an address its implementation holds, and HlAdmin pins that address and its code hash).
var ourNames = []string{"GnoLightClient", "PausableGate", "HlAdmin", "HlAdminExec", "Ed25519Verifier"}

// Artifacts are both sets, by contract name.
type Artifacts map[string]*Artifact

// LoadArtifacts reads our contracts from a forge out directory and Hyperlane's from the release compile's.
func LoadArtifacts(contractsOut, hyperlaneDir string) (Artifacts, error) {
	out := Artifacts{}
	for _, n := range hyperlaneNames {
		a, err := repro.ReadArtifact(filepath.Join(hyperlaneDir, n+".json"), n, nil)
		if err != nil {
			return nil, fmt.Errorf("the Hyperlane release's %s (-hyperlane %s, written by local/hyperlane-artifacts.sh): %w", n, hyperlaneDir, err)
		}
		out[n] = a
	}
	names, err := repro.ForgeImmutableNames(contractsOut)
	if err != nil {
		return nil, err
	}
	for _, n := range ourNames {
		a, err := repro.ReadArtifact(filepath.Join(contractsOut, n+".sol", n+".json"), n, names)
		if err != nil {
			return nil, fmt.Errorf("our %s (-contracts %s, a forge out directory built with --ast): %w", n, contractsOut, err)
		}
		out[n] = a
	}
	if len(clientVerifierParams(out["GnoLightClient"])) == 0 {
		return nil, fmt.Errorf("the GnoLightClient of -contracts %s takes no Ed25519 verifier's address: not this tree's client (M2-NOTES.md §1 A)", contractsOut)
	}
	return out, nil
}

// Ed25519CodeHash is the EXTCODEHASH this build's Ed25519Verifier has once deployed (its runtime code fills no
// immutable): what HlAdmin's ed25519CodeHash must be, and what finishSetup's check 28 holds the verifier's code to.
func (a Artifacts) Ed25519CodeHash() common.Hash {
	return crypto.Keccak256Hash(a["Ed25519Verifier"].Runtime)
}

// clientVerifierParams are the client constructor's address parameters: the Ed25519 verifier it calls (E-M2a's
// Ed25519Verifier, an immutable of the implementation). None for a client with the library inline.
func clientVerifierParams(a *Artifact) []string {
	if a == nil {
		return nil
	}
	var out []string
	for _, in := range a.ABI.Constructor.Inputs {
		if in.Type.T == abi.AddressTy {
			out = append(out, in.Name)
		}
	}
	return out
}

// ---------------------------------------------------------------------------------------- reading views --

// unpackOne decodes a view's single answer into T.
func unpackOne[T any](a *Artifact, method string, out []byte) (T, error) {
	var zero T
	vals, err := a.ABI.Unpack(method, out)
	if err != nil {
		return zero, fmt.Errorf("%s.%s: %w", a.Name, method, err)
	}
	if len(vals) != 1 {
		return zero, fmt.Errorf("%s.%s: %d values, 1 expected", a.Name, method, len(vals))
	}
	return convertTo[T](vals[0])
}

// convertTo copies a decoded value into T (by position for a tuple); a mismatch is an error, never a panic.
func convertTo[T any](in any) (out T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("decoding %T into %T: %v", in, out, r)
		}
	}()
	return *abi.ConvertType(in, new(T)).(*T), nil
}
