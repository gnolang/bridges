package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
	"github.com/gnolang/bridges/solana/bridge/hl"
)

// Each of the deployer's fifteen creations as the setup makes it — its build and its constructor's arguments — from
// the deployment's record alone (PLAN.md §5.8; the plan red team's E2-RT1). The setup sends exactly these bytes, and
// `hleth audit` holds every creation transaction's input to them byte for byte: a contract's runtime code says
// nothing of what its constructor ran — an initcode can write a decision record into HlAdmin's storage, a header
// entry into the client's, a balance into a token's, and return the build's runtime exactly — so the initcode itself
// is compared, and with it every argument the constructor took. HlAdminExec is created inside HlAdmin's constructor:
// HlAdmin's input covers it.

// creationOf is a field's build and constructor values: the launch values l (the record's, or the audit's reading of
// the bindings), the deployment's addresses and domains, this tree's builds.
func creationOf(field string, d *Deployment, l Launch, a Artifacts) (*Artifact, Values, error) {
	A := d.Addr
	me := common.HexToAddress(d.Deployer)
	pa := A("proxyAdmin")
	proxy := func(impl *Artifact, implField string, init Values) (*Artifact, Values, error) {
		data, err := impl.CallData("initialize", init)
		if err != nil {
			return nil, nil, err
		}
		return a["TransparentUpgradeableProxy"], Values{"logic": A(implField), "admin": pa, "data": data}, nil
	}
	switch field {
	case "proxyAdmin":
		return a["ProxyAdmin"], Values{}, nil
	case "mailboxImpl":
		return a["Mailbox"], Values{"localdomain": d.LocalDomain}, nil
	case "mailbox":
		return proxy(a["Mailbox"], "mailboxImpl", Values{"owner": me, "defaultism": pa, "defaulthook": pa, "requiredhook": pa})
	case "merkleTreeHook":
		return a["MerkleTreeHook"], Values{"mailbox": A("mailbox")}, nil
	case "protocolFee":
		maxFee, ok := new(big.Int).SetString(l.MaxProtocolFee, 10)
		if !ok {
			return nil, nil, fmt.Errorf("the record's maximum protocol fee %q is no number", l.MaxProtocolFee)
		}
		return a["ProtocolFee"], Values{"maxprotocolfee": maxFee, "protocolfee": new(big.Int), "beneficiary": common.HexToAddress(l.Beneficiary), "owner": me}, nil
	case "ed25519Verifier":
		return a["Ed25519Verifier"], Values{}, nil
	case "clientImpl":
		v := Values{"mailboxpkgpathhash": crypto.Keccak256Hash([]byte(l.MailboxPkgPath))}
		for _, p := range clientVerifierParams(a["GnoLightClient"]) {
			v[norm(p)] = A("ed25519Verifier")
		}
		return a["GnoLightClient"], v, nil
	case "client":
		init, err := clientInitValues(l, d.GnoDomain, me)
		if err != nil {
			return nil, nil, err
		}
		return proxy(a["GnoLightClient"], "clientImpl", init)
	case "token0Impl", "token1Impl":
		return a["HypERC20"], Values{"decimals": uint8(6), "scalenumerator": big.NewInt(1), "scaledenominator": big.NewInt(1), "mailbox": A("mailbox")}, nil
	case "token0", "token1":
		name, symbol := l.Name0, l.Symbol0
		if field == "token1" {
			name, symbol = l.Name1, l.Symbol1
		}
		return proxy(a["HypERC20"], field+"Impl", Values{"totalsupply": new(big.Int), "name": name, "symbol": symbol, "hook": common.Address{}, "interchainsecuritymodule": common.Address{}, "owner": me})
	case "gateImpl":
		return a["PausableGate"], Values{}, nil
	case "gate":
		return proxy(a["PausableGate"], "gateImpl", Values{"owner": me, "mailbox": A("mailbox"), "client": A("client"),
			"tokens": [2]common.Address{A("token0"), A("token1")}, "seats": launchSeats(l), "watch": launchWatch(l),
			"maxpermessage": l.MaxPerMessage, "daycap": l.DayCap})
	case "hlAdmin":
		return a["HlAdmin"], Values{"c": Values{
			"mailbox": A("mailbox"), "client": A("client"), "gate": A("gate"), "token0": A("token0"), "token1": A("token1"),
			"proxyadmin": pa, "merkletreehook": A("merkleTreeHook"), "protocolfee": A("protocolFee"),
			"ed25519": A("ed25519Verifier"), "ed25519codehash": a.Ed25519CodeHash(), "localdomain": d.LocalDomain,
			"origin": d.GnoDomain, "hlgov": realm32(l.Hlgov), "route0": realm32(l.GnotRoute), "route1": realm32(l.BubbleRoute),
			"mailboxpkgpathhash": crypto.Keccak256Hash([]byte(l.MailboxPkgPath)), "seats": launchSeats(l), "watch": launchWatch(l),
			"maxpermessage": l.MaxPerMessage, "daycap": l.DayCap,
			"startsequence": l.StartSequence, "predecessor": common.Address{},
		}}, nil
	}
	return nil, nil, fmt.Errorf("%s is no creation of the deployer's (HlAdminExec is HlAdmin's)", field)
}

// creationBytes is a field's whole creation input: the build's creation code and its constructor's arguments.
func creationBytes(field string, d *Deployment, l Launch, a Artifacts) ([]byte, *Artifact, error) {
	art, v, err := creationOf(field, d, l, a)
	if err != nil {
		return nil, nil, err
	}
	b, err := art.CreationCode(v)
	return b, art, err
}

func launchSeats(l Launch) [4]common.Address {
	var out [4]common.Address
	for i, s := range l.Seats {
		out[i] = common.HexToAddress(s)
	}
	return out
}

func launchWatch(l Launch) [2]common.Address {
	var out [2]common.Address
	for i, s := range l.Watch {
		out[i] = common.HexToAddress(s)
	}
	return out
}

// clientInitValues are the client's initializer as the record keeps its trusted header: the chain id, gno.land's
// domain, the header's sixteen leaves (which must hash to the trusted hash), its set in the order given, the deployer
// as admin (until step 10).
func clientInitValues(l Launch, gnoDomain uint32, admin common.Address) (Values, error) {
	if len(l.TrustedLeaves) != 16 {
		return nil, fmt.Errorf("the record holds %d of the trusted header's sixteen leaves (a record of an earlier build: its client's creation cannot be rebuilt)", len(l.TrustedLeaves))
	}
	leaves := make([][]byte, 16)
	for i, s := range l.TrustedLeaves {
		b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
		if err != nil {
			return nil, fmt.Errorf("the record's trusted leaf %d: %w", i, err)
		}
		leaves[i] = b
	}
	want, err := hl.ParseKey32(l.TrustedHash)
	if err != nil {
		return nil, fmt.Errorf("the record's trusted hash: %w", err)
	}
	if got, _ := gnolight.SimpleHash(leaves); got != want {
		return nil, errors.New("the record's trusted leaves hash to " + hex.EncodeToString(got[:]) + ", not its trusted hash " + l.TrustedHash)
	}
	var vals []Values
	for _, v := range l.Validators {
		pk, err := hl.ParseKey32(v.PubKey)
		if err != nil {
			return nil, fmt.Errorf("the record's validator key %q: %w", v.PubKey, err)
		}
		vals = append(vals, Values{"pubkey": pk, "power": v.Power})
	}
	return Values{"chainid": l.GnoChainID, "origindomain": gnoDomain, "headerleaves": leaves, "validators": vals, "admin": admin}, nil
}
