// Package sol speaks to the bridge's Solana programs: Hyperlane's mailbox and synthetic token (a warp route, from
// rust/sealevel at the mainnet builds' commit) with the Token-2022 calls that set up a synthetic token's mint, and
// our three — the light-client ISM, the pausable ISM and the admin program (bridge/programs, whose sol-util is the
// source of every byte here). Instruction layouts are borsh; the accounts our ISMs and the admin program need are
// derived the way the programs derive them, and a token's Handle accounts are asked of the token program by
// simulation, as Hyperlane's relayer does (get_process_payload).
package sol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
)

var (
	SystemProgram = solana.SystemProgramID
	Token2022     = solana.MustPublicKeyFromBase58("TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb")
	ATAProgram    = solana.MustPublicKeyFromBase58("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL")
	NoopProgram   = solana.MustPublicKeyFromBase58("noopb9bkMVfRPU8AsbpTUg8AQkHtKwMYZiFUjNRtMmV")
	ComputeBudget = solana.MustPublicKeyFromBase58("ComputeBudget111111111111111111111111111111")
)

// programDiscriminator prefixes every instruction of the ISM and token programs (account_utils
// PROGRAM_INSTRUCTION_DISCRIMINATOR); the mailbox's instructions carry none.
var programDiscriminator = []byte{1, 1, 1, 1, 1, 1, 1, 1}

// Interface discriminators: the first 8 bytes of sha256 of the interface method's name.
var (
	discISMAccountMetas    = disc("hyperlane-message-recipient:interchain-security-module-account-metas")
	discHandleAccountMetas = disc("hyperlane-message-recipient:handle-account-metas")
	discVerifyAccountMetas = disc("hyperlane-interchain-security-module:verify-account-metas")
)

func disc(name string) []byte {
	h := sha256.Sum256([]byte(name))
	return h[:8]
}

// PDA is the program address for seeds; it panics only if no bump works, which never happens in practice.
func PDA(program solana.PublicKey, seeds ...[]byte) solana.PublicKey {
	k, _, err := solana.FindProgramAddress(seeds, program)
	if err != nil {
		panic(err)
	}
	return k
}

func b(s string) []byte { return []byte(s) }

// ------------------------------------------------------------------ borsh --

type enc struct{ bytes.Buffer }

func (e *enc) u8(v uint8)   { e.WriteByte(v) }
func (e *enc) u32(v uint32) { _ = binary.Write(e, binary.LittleEndian, v) }
func (e *enc) u64(v uint64) { _ = binary.Write(e, binary.LittleEndian, v) }
func (e *enc) key(k solana.PublicKey) {
	e.Write(k[:])
}
func (e *enc) vec(v []byte) { e.u32(uint32(len(v))); e.Write(v) }
func (e *enc) str(s string) { e.vec([]byte(s)) }
func (e *enc) optKey(k *solana.PublicKey) {
	if k == nil {
		e.u8(0)
		return
	}
	e.u8(1)
	e.key(*k)
}

// u256 is hyperlane_core's U256 in borsh: four u64 limbs, least significant first.
func (e *enc) u256(v uint64) { e.u64(v); e.u64(0); e.u64(0); e.u64(0) }

func ix(program solana.PublicKey, data []byte, accounts ...*solana.AccountMeta) solana.Instruction {
	return solana.NewInstruction(program, accounts, data)
}

func w(k solana.PublicKey) *solana.AccountMeta  { return solana.NewAccountMeta(k, true, false) }
func r(k solana.PublicKey) *solana.AccountMeta  { return solana.NewAccountMeta(k, false, false) }
func ws(k solana.PublicKey) *solana.AccountMeta { return solana.NewAccountMeta(k, true, true) }
func rs(k solana.PublicKey) *solana.AccountMeta { return solana.NewAccountMeta(k, false, true) }

// ---------------------------------------------------------------- mailbox --

// Mailbox is a Hyperlane mailbox program.
type Mailbox struct{ Program solana.PublicKey }

func (m Mailbox) Inbox() solana.PublicKey { return PDA(m.Program, b("hyperlane"), b("-"), b("inbox")) }
func (m Mailbox) Outbox() solana.PublicKey {
	return PDA(m.Program, b("hyperlane"), b("-"), b("outbox"))
}
func (m Mailbox) ProcessAuthority(recipient solana.PublicKey) solana.PublicKey {
	return PDA(m.Program, b("hyperlane"), b("-"), b("process_authority"), b("-"), recipient[:])
}
func (m Mailbox) ProcessedMessage(id [32]byte) solana.PublicKey {
	return PDA(m.Program, b("hyperlane"), b("-"), b("processed_message"), b("-"), id[:])
}
func (m Mailbox) DispatchedMessage(unique solana.PublicKey) solana.PublicKey {
	return PDA(m.Program, b("hyperlane"), b("-"), b("dispatched_message"), b("-"), unique[:])
}

// InitIx initializes a mailbox: its domain, its default ISM, and a protocol fee (0 for ours).
func (m Mailbox) InitIx(payer solana.PublicKey, localDomain uint32, defaultISM solana.PublicKey, maxFee, fee uint64, beneficiary solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(0) // Init
	e.u32(localDomain)
	e.key(defaultISM)
	e.u64(maxFee)
	e.u64(fee)
	e.key(beneficiary)
	return ix(m.Program, e.Bytes(), w(SystemProgram), ws(payer), w(m.Inbox()), w(m.Outbox()))
}

// processData is InboxProcess's instruction data.
func processData(metadata, message []byte) []byte {
	var e enc
	e.u8(1) // InboxProcess
	e.vec(metadata)
	e.vec(message)
	return e.Bytes()
}

// ------------------------------------------------------- synthetic token --

// Synthetic is a hyperlane-sealevel-token program: a warp route whose token is minted here and burned to leave.
type Synthetic struct{ Program solana.PublicKey }

// Token is the route's own account (it is also the PDA a handle-account-metas query is sent to).
func (t Synthetic) Token() solana.PublicKey {
	return PDA(t.Program, b("hyperlane_message_recipient"), b("-"), b("handle"), b("-"), b("account_metas"))
}
func (t Synthetic) Mint() solana.PublicKey {
	return PDA(t.Program, b("hyperlane_token"), b("-"), b("mint"))
}
func (t Synthetic) ATAPayer() solana.PublicKey {
	return PDA(t.Program, b("hyperlane_token"), b("-"), b("ata_payer"))
}
func (t Synthetic) DispatchAuthority() solana.PublicKey {
	return PDA(t.Program, b("hyperlane_dispatcher"), b("-"), b("dispatch_authority"))
}

func (t Synthetic) data(variant uint8) *enc {
	var e enc
	e.Write(programDiscriminator)
	e.u8(variant)
	return &e
}

// InitIx initializes the route: its mailbox, its ISM (nil: the mailbox's default), no gas paymaster, and the
// decimals here and in messages. It creates the mint account (sized for a metadata pointer) and the ATA payer;
// the mint itself is set up by the Token-2022 instructions that must follow (MintSetupIxs).
func (t Synthetic) InitIx(payer, mailbox solana.PublicKey, ism *solana.PublicKey, decimals, remoteDecimals uint8) solana.Instruction {
	e := t.data(0)
	e.key(mailbox)
	e.optKey(ism)
	e.u8(0) // interchain_gas_paymaster: None
	e.u8(decimals)
	e.u8(remoteDecimals)
	return ix(t.Program, e.Bytes(), r(SystemProgram), w(t.Token()), w(t.DispatchAuthority()), ws(payer), w(t.Mint()), w(t.ATAPayer()))
}

// EnrollRemoteRouterIx names the route on another domain (32 bytes; a gno realm's address padded).
func (t Synthetic) EnrollRemoteRouterIx(owner solana.PublicKey, domain uint32, router [32]byte) solana.Instruction {
	e := t.data(2)
	e.u32(domain)
	e.u8(1)
	e.Write(router[:])
	return ix(t.Program, e.Bytes(), r(SystemProgram), w(t.Token()), ws(owner))
}

func (t Synthetic) SetISMIx(owner solana.PublicKey, ism *solana.PublicKey) solana.Instruction {
	e := t.data(5)
	e.optKey(ism)
	return ix(t.Program, e.Bytes(), w(t.Token()), ws(owner))
}

// ClearIGPIx sets the gas paymaster to none: our relayer delivers without being paid on chain.
func (t Synthetic) ClearIGPIx(owner solana.PublicKey) solana.Instruction {
	e := t.data(6)
	e.u8(0)
	return ix(t.Program, e.Bytes(), w(t.Token()), ws(owner))
}

func (t Synthetic) TransferOwnershipIx(owner solana.PublicKey, to *solana.PublicKey) solana.Instruction {
	e := t.data(7)
	e.optKey(to)
	return ix(t.Program, e.Bytes(), w(t.Token()), ws(owner))
}

// TransferRemoteIx burns amount from sender's token account and dispatches a token message to recipient on
// destination. unique is a fresh keypair's public key, signing too: it names the dispatched-message account.
func (t Synthetic) TransferRemoteIx(mailbox Mailbox, sender, unique solana.PublicKey, destination uint32, recipient [32]byte, amount uint64) solana.Instruction {
	e := t.data(1)
	e.u32(destination)
	e.Write(recipient[:])
	e.u256(amount)
	return ix(t.Program, e.Bytes(),
		r(SystemProgram),
		r(NoopProgram),
		r(t.Token()),
		r(mailbox.Program),
		w(mailbox.Outbox()),
		r(t.DispatchAuthority()),
		ws(sender),
		ws(unique),
		w(mailbox.DispatchedMessage(unique)),
		r(Token2022),
		w(t.Mint()),
		w(ATA(sender, t.Mint())),
	)
}

// ATA is a wallet's associated Token-2022 account for mint.
func ATA(owner, mint solana.PublicKey) solana.PublicKey {
	return PDA(ATAProgram, owner[:], Token2022[:], mint[:])
}

// --------------------------------------------------------------- Token-2022 --

// MintSetupIxs turns the route's bare mint account into a Token-2022 mint with a metadata pointer to itself
// and the given decimals, minted by payer for now: the order of Hyperlane's client (warp_route.rs), which runs
// `spl-token create-token --enable-metadata` and initialize_mint2 against the account Init made. The metadata
// itself and the hand-off of the mint authority to the mint account follow (MetadataIxs, MintAuthorityToSelfIx).
func MintSetupIxs(mint, payer solana.PublicKey, decimals uint8) []solana.Instruction {
	// MetadataPointerExtension (39), Initialize (0): authority, then the metadata's address (the mint itself)
	var p enc
	p.u8(39)
	p.u8(0)
	p.key(payer)
	p.key(mint)
	// InitializeMint2 (20): decimals, mint authority, no freeze authority
	var m enc
	m.u8(20)
	m.u8(decimals)
	m.key(payer)
	m.u8(0)
	return []solana.Instruction{
		ix(Token2022, p.Bytes(), w(mint)),
		ix(Token2022, m.Bytes(), w(mint)),
	}
}

// MetadataIxs writes the token's name, symbol and uri into the mint (the spl-token-metadata-interface's
// Initialize), first topping the mint up with the lamports its larger size needs.
func MetadataIxs(mint, payer solana.PublicKey, name, symbol, uri string, extraLamports uint64) []solana.Instruction {
	var e enc
	e.Write(disc("spl_token_metadata_interface:initialize_account"))
	e.str(name)
	e.str(symbol)
	e.str(uri)
	topUp := solana.NewInstruction(SystemProgram, solana.AccountMetaSlice{ws(payer), w(mint)}, transferData(extraLamports))
	return []solana.Instruction{
		topUp,
		ix(Token2022, e.Bytes(), w(mint), r(payer), r(mint), ws(payer)),
	}
}

// MintAuthorityToSelfIx makes the mint account its own mint authority, which the route signs for as a PDA.
func MintAuthorityToSelfIx(mint, current solana.PublicKey) solana.Instruction {
	var e enc
	e.u8(6) // SetAuthority
	e.u8(0) // MintTokens
	e.u8(1)
	e.key(mint)
	return ix(Token2022, e.Bytes(), w(mint), ws(current))
}

func transferData(lamports uint64) []byte {
	var e enc
	e.u32(2) // system Transfer
	e.u64(lamports)
	return e.Bytes()
}

// TransferIx moves lamports (funding the route's ATA payer, for one).
func TransferIx(from, to solana.PublicKey, lamports uint64) solana.Instruction {
	return solana.NewInstruction(SystemProgram, solana.AccountMetaSlice{ws(from), w(to)}, transferData(lamports))
}

// ComputeLimitIx raises a transaction's compute budget.
func ComputeLimitIx(units uint32) solana.Instruction {
	var e enc
	e.u8(2)
	e.u32(units)
	return ix(ComputeBudget, e.Bytes())
}

// ------------------------------------------------------------------ client --

// Client sends and simulates transactions for one payer.
type Client struct {
	RPC   *rpc.Client
	Payer solana.PrivateKey
}

func NewClient(url string, payer solana.PrivateKey) *Client {
	return &Client{RPC: NewRPC(url), Payer: payer}
}

func (c *Client) tx(ctx context.Context, ixs []solana.Instruction) (*solana.Transaction, error) {
	bh, err := c.RPC.GetLatestBlockhash(ctx, rpc.CommitmentConfirmed)
	if err != nil {
		return nil, err
	}
	return solana.NewTransaction(ixs, bh.Value.Blockhash, solana.TransactionPayer(c.Payer.PublicKey()))
}

// Send signs (payer first, then signers), sends, and waits until the transaction is confirmed or failed, or ctx
// ends (G21: a shutdown never waits the minute out). A failure comes back with the program logs.
func (c *Client) Send(ctx context.Context, ixs []solana.Instruction, signers ...solana.PrivateKey) (solana.Signature, error) {
	tx, err := c.tx(ctx, ixs)
	if err != nil {
		return solana.Signature{}, err
	}
	keys := append([]solana.PrivateKey{c.Payer}, signers...)
	if _, err := tx.Sign(func(k solana.PublicKey) *solana.PrivateKey {
		for i := range keys {
			if keys[i].PublicKey().Equals(k) {
				return &keys[i]
			}
		}
		return nil
	}); err != nil {
		return solana.Signature{}, err
	}
	sig, err := c.RPC.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{PreflightCommitment: rpc.CommitmentConfirmed})
	if err != nil {
		return sig, fmt.Errorf("send: %w%s", err, rpcLogs(err))
	}
	for i := 0; i < 120; i++ {
		st, err := c.RPC.GetSignatureStatuses(ctx, true, sig)
		if err == nil && len(st.Value) == 1 && st.Value[0] != nil {
			s := st.Value[0]
			if s.Err != nil {
				return sig, fmt.Errorf("transaction %s failed: %v", sig, s.Err)
			}
			if s.ConfirmationStatus == rpc.ConfirmationStatusConfirmed || s.ConfirmationStatus == rpc.ConfirmationStatusFinalized {
				return sig, nil
			}
		}
		select {
		case <-ctx.Done():
			return sig, fmt.Errorf("transaction %s: sent, its confirmation not awaited: %w", sig, ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	return sig, fmt.Errorf("transaction %s: not confirmed in a minute", sig)
}

// rpcLogs pulls the simulation logs out of a preflight failure.
func rpcLogs(err error) string {
	var re *jsonrpc.RPCError
	if errors.As(err, &re) && re.Data != nil {
		return fmt.Sprintf("\n%v", re.Data)
	}
	return ""
}

// Simulate runs instructions without sending them and returns the result (logs, return data).
func (c *Client) Simulate(ctx context.Context, ixs []solana.Instruction) (*rpc.SimulateTransactionResult, error) {
	tx, err := c.tx(ctx, ixs)
	if err != nil {
		return nil, err
	}
	out, err := c.RPC.SimulateTransactionWithOpts(ctx, tx, &rpc.SimulateTransactionOpts{
		SigVerify:              false,
		ReplaceRecentBlockhash: true,
		Commitment:             rpc.CommitmentConfirmed,
	})
	if err != nil {
		return nil, err
	}
	if out.Value.Err != nil {
		return out.Value, fmt.Errorf("simulation failed: %v\n%s", out.Value.Err, strings.Join(out.Value.Logs, "\n"))
	}
	return out.Value, nil
}

// returnData is a simulation's return data, decoded.
func returnData(res *rpc.SimulateTransactionResult) ([]byte, error) {
	if res.ReturnData == nil {
		return nil, errors.New("no return data")
	}
	return res.ReturnData.Data.Content, nil
}

// accountMetas reads a SimulationReturnData<Vec<SerializableAccountMeta>>: a borsh vector of (pubkey, is_signer,
// is_writable), then a 0xff byte that keeps the RPC from trimming trailing zeros. A signer the program asks for
// is refused, and so is the payer under any flags (§5.7: a dynamic meta equal to the payer is rejected, since the
// compiled transaction folds it into the signer slot): nothing a program names may spend from the payer.
func accountMetas(data []byte, payer solana.PublicKey) (solana.AccountMetaSlice, error) {
	if len(data) < 4 {
		return nil, errors.New("account metas: short return data")
	}
	n := int(binary.LittleEndian.Uint32(data))
	if len(data) < 4+34*n {
		return nil, errors.New("account metas: truncated")
	}
	var out solana.AccountMetaSlice
	for i := 0; i < n; i++ {
		o := 4 + 34*i
		k := solana.PublicKeyFromBytes(data[o : o+32])
		signer, writable := data[o+32] == 1, data[o+33] == 1
		if signer {
			return nil, fmt.Errorf("account metas: %s asked to sign", k)
		}
		if k.Equals(payer) {
			return nil, fmt.Errorf("account metas: the program named the payer %s", k)
		}
		out = append(out, solana.NewAccountMeta(k, writable, false))
	}
	return out, nil
}

func (c *Client) metas(ctx context.Context, program solana.PublicKey, pdaSeeds [][]byte, data []byte) (solana.AccountMetaSlice, error) {
	pda := PDA(program, pdaSeeds...)
	res, err := c.Simulate(ctx, []solana.Instruction{ix(program, data, w(pda))})
	if err != nil {
		return nil, err
	}
	rd, err := returnData(res)
	if err != nil {
		return nil, err
	}
	return accountMetas(rd, c.Payer.PublicKey())
}

// RecipientISMAccounts are the accounts a recipient's InterchainSecurityModule question needs (none for ours).
func (c *Client) RecipientISMAccounts(ctx context.Context, recipient solana.PublicKey) (solana.AccountMetaSlice, error) {
	return c.metas(ctx, recipient,
		[][]byte{b("hyperlane_message_recipient"), b("-"), b("interchain_security_module"), b("-"), b("account_metas")},
		discISMAccountMetas)
}

// RecipientISM is the ISM the mailbox will use for a recipient (its own, or the mailbox's default), asked of
// the mailbox's InboxGetRecipientIsm.
func (c *Client) RecipientISM(ctx context.Context, m Mailbox, recipient solana.PublicKey, ismAccounts solana.AccountMetaSlice) (solana.PublicKey, error) {
	var g enc
	g.u8(3)
	g.key(recipient)
	q := solana.AccountMetaSlice{r(m.Inbox()), r(recipient)}
	q = append(q, ismAccounts...)
	res, err := c.Simulate(ctx, []solana.Instruction{ix(m.Program, g.Bytes(), q...)})
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("the recipient's ISM: %w", err)
	}
	rd, err := returnData(res)
	if err != nil || len(rd) < 32 {
		return solana.PublicKey{}, fmt.Errorf("the recipient's ISM: %v", err)
	}
	return solana.PublicKeyFromBytes(rd[:32]), nil
}

// HandleAccounts asks a recipient program (a token route) which accounts its Handle needs, by simulation, with
// the payer rule applied; the admin program's are derived instead (Admin.HandleAccounts).
func (c *Client) HandleAccounts(ctx context.Context, recipient solana.PublicKey, origin uint32, sender [32]byte, body []byte) (solana.AccountMetaSlice, error) {
	var h enc
	h.Write(discHandleAccountMetas)
	h.u32(origin)
	h.Write(sender[:])
	h.vec(body)
	return c.metas(ctx, recipient, [][]byte{b("hyperlane_message_recipient"), b("-"), b("handle"), b("-"), b("account_metas")}, h.Bytes())
}

// ProcessIx assembles a mailbox delivery (InboxProcess) from the accounts each party needs, as the mailbox reads
// them: the payer, the system program, the inbox, the recipient's process authority, the processed-message record,
// the recipient's ISM-question accounts, the noop program, the ISM and its Verify accounts, the recipient and its
// Handle accounts.
func ProcessIx(m Mailbox, payer solana.PublicKey, metadata, message []byte, id [32]byte, recipient, ism solana.PublicKey, recipientISMAccounts, verifyAccounts, handleAccounts solana.AccountMetaSlice) solana.Instruction {
	accts := solana.AccountMetaSlice{
		ws(payer),
		r(SystemProgram),
		w(m.Inbox()),
		r(m.ProcessAuthority(recipient)),
		w(m.ProcessedMessage(id)),
	}
	accts = append(accts, recipientISMAccounts...)
	accts = append(accts, r(NoopProgram), r(ism))
	accts = append(accts, verifyAccounts...)
	accts = append(accts, r(recipient))
	accts = append(accts, handleAccounts...)
	return ix(m.Program, processData(metadata, message), accts...)
}

// Delivered says whether the mailbox has processed a message id, as of a finalized block.
func (c *Client) Delivered(ctx context.Context, m Mailbox, id [32]byte) (bool, error) {
	_, err := c.RPC.GetAccountInfoWithOpts(ctx, m.ProcessedMessage(id), &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentFinalized})
	if errors.Is(err, rpc.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
