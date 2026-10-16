package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/gnolang/bridges/solana/bridge/hl"
	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// The test sends (local runs and drills): a burn of synthetic toward gno.land through a token's transferRemote, a
// balance, a key. A burn's recipient is a gno address, carried as Hyperlane carries one (twelve zero bytes, then
// the twenty); a bridge realm's own address is refused when the caller names the realms (-refuse-realms), since
// the realm would sweep the credit (the Solana tools' K21).

// burnRecipient reads a gno recipient and refuses a named realm's package address.
func burnRecipient(to string, refuse []string) ([32]byte, error) {
	k, err := hl.GnoAddressToBytes32(to)
	if err != nil {
		return k, fmt.Errorf("-to %q is not a gno address: %w", to, err)
	}
	for _, p := range refuse {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if hl.GnoPackageAddress(p) == to {
			return k, fmt.Errorf("hleth send: %s is the address of %s, a bridge realm: the realm would sweep the credit (K21)", to, p)
		}
	}
	return k, nil
}

// sendBurn is `hleth send`: the token's quote for gno.land's domain paid as the call's value (the ProtocolFee and
// the MerkleTreeHook: 0 at launch), then transferRemote; it prints the transaction and the message's id and nonce
// from the mailbox's events.
func sendBurn(ctx context.Context, w io.Writer, cn *conn, tx *txer, token common.Address, gnoDomain uint32, to [32]byte, amount *big.Int) error {
	hy := ethx.HypERC20
	q, err := hy.Pack("quoteGasPayment", gnoDomain)
	if err != nil {
		return err
	}
	out, err := cn.c.CallAt(ctx, token, q, ethx.Latest)
	if err != nil {
		return fmt.Errorf("the token's quote: %w", err)
	}
	vals, err := hy.Unpack("quoteGasPayment", out)
	if err != nil || len(vals) != 1 {
		return fmt.Errorf("the token's quote: %v", err)
	}
	value := vals[0].(*big.Int)
	data, err := hy.Pack("transferRemote", gnoDomain, to, amount)
	if err != nil {
		return err
	}
	rc, err := tx.call(ctx, fmt.Sprintf("transferRemote %s to 0x%x", amount, to), token, data, value)
	if err != nil {
		return err
	}
	id, nonce := dispatchOf(rc)
	fmt.Fprintf(w, "sent: %s base units toward gno.land's domain %d for 0x%x, paying %s wei: tx %s, block %v; message %s, nonce %d\n", amount, gnoDomain, to, value, rc.TxHash.Hex(), rc.BlockNumber, id, nonce)
	return nil
}

// dispatchOf finds the mailbox's Dispatch in a receipt: the message's id and nonce ("" and 0 when none).
func dispatchOf(rc *types.Receipt) (string, uint32) {
	ev := ethx.Mailbox.Event("Dispatch")
	for _, l := range rc.Logs {
		if len(l.Topics) != 4 || l.Topics[0] != ev {
			continue
		}
		vals, err := ethx.Mailbox.ABI.Events["Dispatch"].Inputs.NonIndexed().Unpack(l.Data)
		if err != nil || len(vals) != 1 {
			continue
		}
		raw := vals[0].([]byte)
		m, err := hl.ParseMessage(raw)
		if err != nil {
			continue
		}
		id := hl.ID(raw)
		return "0x" + hex.EncodeToString(id[:]), m.Nonce
	}
	return "", 0
}

// balanceOf is a token's balance of an owner at the latest block.
func balanceOf(ctx context.Context, cn *conn, token, owner common.Address) (*big.Int, error) {
	data, err := ethx.HypERC20.Pack("balanceOf", owner)
	if err != nil {
		return nil, err
	}
	out, err := cn.c.CallAt(ctx, token, data, ethx.Latest)
	if err != nil {
		return nil, err
	}
	vals, err := ethx.HypERC20.Unpack("balanceOf", out)
	if err != nil || len(vals) != 1 {
		return nil, fmt.Errorf("balanceOf: %v", err)
	}
	return vals[0].(*big.Int), nil
}

// keygen writes a key file: a fresh key, or — for LOCAL runs only — keccak256 of a seed, so a run's addresses are
// stable. It refuses to overwrite a file and answers the key's address.
func keygen(path, seed string) (common.Address, error) {
	if seed == "" {
		return ethx.NewKeyFile(path)
	}
	if _, err := os.Stat(path); err == nil {
		return common.Address{}, fmt.Errorf("%s exists: refusing to overwrite a key", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return common.Address{}, err
	}
	secret := crypto.Keccak256([]byte(seed))
	k, err := crypto.ToECDSA(secret)
	if err != nil {
		return common.Address{}, fmt.Errorf("the seed's hash is no secp256k1 secret: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return common.Address{}, err
	}
	if _, err := f.WriteString(hex.EncodeToString(secret) + "\n"); err != nil {
		f.Close()
		return common.Address{}, err
	}
	if err := f.Close(); err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(k.PublicKey), nil
}
