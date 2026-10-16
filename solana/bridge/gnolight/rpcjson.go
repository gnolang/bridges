package gnolight

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// What a gno.land node serves over RPC (amino JSON: bytes in base64, int64 as strings, time as RFC 3339), read
// into the core's types the way the programs' fixture loader does (gno-light-core/src/fixtures.rs), so a header
// fetched live hashes to what the chain's block_meta says and a results leaf re-encoded from block_results hashes
// to the LastResultsHash in the next header.

// jsonBlock is /block's result.
type jsonBlock struct {
	BlockMeta struct {
		BlockID json.RawMessage `json:"block_id"`
		Header  json.RawMessage `json:"header"`
	} `json:"block_meta"`
	Block struct {
		Header     json.RawMessage        `json:"header"`
		Data       struct{ Txs []string } `json:"data"`
		LastCommit json.RawMessage        `json:"last_commit"`
	} `json:"block"`
}

type jsonBlockID struct {
	Hash  string `json:"hash"`
	Parts struct {
		Total json.Number `json:"total"`
		Hash  string      `json:"hash"`
	} `json:"parts"`
}

type jsonHeader struct {
	Version            string       `json:"version"`
	ChainID            string       `json:"chain_id"`
	Height             json.Number  `json:"height"`
	Time               string       `json:"time"`
	NumTxs             json.Number  `json:"num_txs"`
	TotalTxs           json.Number  `json:"total_txs"`
	AppVersion         string       `json:"app_version"`
	LastBlockID        *jsonBlockID `json:"last_block_id"`
	LastCommitHash     string       `json:"last_commit_hash"`
	DataHash           string       `json:"data_hash"`
	ValidatorsHash     string       `json:"validators_hash"`
	NextValidatorsHash string       `json:"next_validators_hash"`
	ConsensusHash      string       `json:"consensus_hash"`
	AppHash            string       `json:"app_hash"`
	LastResultsHash    string       `json:"last_results_hash"`
	ProposerAddress    string       `json:"proposer_address"`
}

type jsonVote struct {
	Type             json.Number  `json:"type"`
	Height           json.Number  `json:"height"`
	Round            json.Number  `json:"round"`
	BlockID          *jsonBlockID `json:"block_id"`
	Timestamp        string       `json:"timestamp"`
	ValidatorAddress string       `json:"validator_address"`
	ValidatorIndex   json.Number  `json:"validator_index"`
	Signature        string       `json:"signature"`
}

type jsonCommit struct {
	BlockID    jsonBlockID `json:"block_id"`
	Precommits []*jsonVote `json:"precommits"`
}

func b64(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

func num(n json.Number) int64 {
	if n == "" {
		return 0
	}
	v, _ := strconv.ParseInt(string(n), 10, 64)
	return v
}

// ParseTime reads an RFC 3339 time with nanoseconds into seconds and nanoseconds.
func ParseTime(s string) (Time, error) {
	if s == "" {
		return Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return Time{}, err
	}
	return Time{Secs: t.Unix(), Nanos: int32(t.Nanosecond())}, nil
}

func blockIDFromJSON(j *jsonBlockID) (BlockID, error) {
	if j == nil {
		return BlockID{}, nil
	}
	h, err := b64(j.Hash)
	if err != nil {
		return BlockID{}, err
	}
	ph, err := b64(j.Parts.Hash)
	if err != nil {
		return BlockID{}, err
	}
	return BlockID{Hash: h, Parts: PartSetHeader{Total: uint32(num(j.Parts.Total)), Hash: ph}}, nil
}

// HeaderFromJSON reads a header as /block serves it.
func HeaderFromJSON(raw []byte) (*Header, error) {
	var j jsonHeader
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, err
	}
	t, err := ParseTime(j.Time)
	if err != nil {
		return nil, err
	}
	lb, err := blockIDFromJSON(j.LastBlockID)
	if err != nil {
		return nil, err
	}
	h := &Header{Version: j.Version, ChainID: j.ChainID, Height: num(j.Height), Time: t, NumTxs: num(j.NumTxs), TotalTxs: num(j.TotalTxs),
		AppVersion: j.AppVersion, LastBlockID: lb, ProposerAddress: j.ProposerAddress}
	for _, f := range []struct {
		dst *[]byte
		src string
	}{{&h.LastCommitHash, j.LastCommitHash}, {&h.DataHash, j.DataHash}, {&h.ValidatorsHash, j.ValidatorsHash}, {&h.NextValidatorsHash, j.NextValidatorsHash},
		{&h.ConsensusHash, j.ConsensusHash}, {&h.AppHash, j.AppHash}, {&h.LastResultsHash, j.LastResultsHash}} {
		if *f.dst, err = b64(f.src); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// Block is /block's result decoded: the header and the hash the chain computed for it.
type Block struct {
	Header     *Header
	Hash       [32]byte
	Parts      PartSetHeader
	NumTxs     int64
	LastCommit *Commit // the commit of the block before, as this block carries it
}

// BlockFromJSON reads /block's result.
func BlockFromJSON(raw []byte) (*Block, error) {
	var j jsonBlock
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, err
	}
	h, err := HeaderFromJSON(j.Block.Header)
	if err != nil {
		return nil, err
	}
	var id jsonBlockID
	if err := json.Unmarshal(j.BlockMeta.BlockID, &id); err != nil {
		return nil, err
	}
	bid, err := blockIDFromJSON(&id)
	if err != nil {
		return nil, err
	}
	if len(bid.Hash) != 32 {
		return nil, errors.New("gnolight: block_meta carries no 32-byte hash")
	}
	b := &Block{Header: h, Hash: arr32(bid.Hash), Parts: bid.Parts, NumTxs: h.NumTxs}
	if len(j.Block.LastCommit) > 0 && string(j.Block.LastCommit) != "null" {
		var c jsonCommit
		if err := json.Unmarshal(j.Block.LastCommit, &c); err == nil {
			if lc, err := commitFromJSON(&c); err == nil {
				b.LastCommit = lc
			}
		}
	}
	return b, nil
}

// Vote is one precommit of a commit as the chain serves it, with its signature.
type Vote struct {
	Type             byte
	Height           int64
	Round            int64
	BlockID          BlockID
	Time             Time
	Signature        []byte
	ValidatorIndex   int
	ValidatorAddress string
}

// CommitFromJSON reads /commit's result: the commit's BlockID and one slot per validator (nil where the chain
// serves a nil precommit), each present slot's vote with its signature.
func CommitFromJSON(raw []byte) (*Commit, []*Vote, error) {
	var j struct {
		SignedHeader struct {
			Commit jsonCommit `json:"commit"`
		} `json:"signed_header"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, nil, err
	}
	c, err := commitFromJSON(&j.SignedHeader.Commit)
	if err != nil {
		return nil, nil, err
	}
	votes, err := votesFromJSON(&j.SignedHeader.Commit)
	if err != nil {
		return nil, nil, err
	}
	return c, votes, nil
}

func votesFromJSON(j *jsonCommit) ([]*Vote, error) {
	out := make([]*Vote, len(j.Precommits))
	for i, v := range j.Precommits {
		if v == nil {
			continue
		}
		sig, err := b64(v.Signature)
		if err != nil {
			return nil, err
		}
		bid, err := blockIDFromJSON(v.BlockID)
		if err != nil {
			return nil, err
		}
		t, err := ParseTime(v.Timestamp)
		if err != nil {
			return nil, err
		}
		out[i] = &Vote{Type: byte(num(v.Type)), Height: num(v.Height), Round: num(v.Round), BlockID: bid, Time: t, Signature: sig,
			ValidatorIndex: int(num(v.ValidatorIndex)), ValidatorAddress: v.ValidatorAddress}
	}
	return out, nil
}

// commitFromJSON is the commit as the client stages it: a slot carries a precommit only when it is a precommit
// for the commit's BlockID.
func commitFromJSON(j *jsonCommit) (*Commit, error) {
	bid, err := blockIDFromJSON(&j.BlockID)
	if err != nil {
		return nil, err
	}
	votes, err := votesFromJSON(j)
	if err != nil {
		return nil, err
	}
	c := &Commit{BlockID: bid, Precommits: make([]*Precommit, len(votes)), Signatures: make([][]byte, len(votes))}
	for i, v := range votes {
		if v == nil || v.Type != PrecommitType || string(v.BlockID.Hash) != string(bid.Hash) || v.BlockID.Parts.Total != bid.Parts.Total || string(v.BlockID.Parts.Hash) != string(bid.Parts.Hash) {
			continue
		}
		c.Precommits[i] = &Precommit{Round: v.Round, Time: v.Time}
		c.Signatures[i] = v.Signature
	}
	return c, nil
}

// ValidatorsFromJSON reads /validators' result.
func ValidatorsFromJSON(raw []byte) (*ValidatorSet, error) {
	var j struct {
		Validators []struct {
			PubKey struct {
				Type  string `json:"@type"`
				Value string `json:"value"`
			} `json:"pub_key"`
			VotingPower json.Number `json:"voting_power"`
		} `json:"validators"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, err
	}
	s := &ValidatorSet{}
	for _, v := range j.Validators {
		if v.PubKey.Type != PubKeyTypeURL {
			return nil, fmt.Errorf("gnolight: a validator with a %s key (ed25519 only)", v.PubKey.Type)
		}
		pk, err := b64(v.PubKey.Value)
		if err != nil || len(pk) != 32 {
			return nil, errors.New("gnolight: a validator key that is not 32 bytes")
		}
		s.Validators = append(s.Validators, Validator{PubKey: arr32(pk), Power: num(v.VotingPower)})
	}
	return s, nil
}

// ------------------------------------------------------------ block results --

// TxResult is one transaction's result as block_results serves it: the leaf re-encoded, or why it cannot be,
// and its events decoded for the readers that watch them. A failed transaction's leaf is rebuilt with its error
// (errortypes.go); a string the node's JSON cannot carry exactly — bytes that are not UTF-8, which it serves as
// U+FFFD — rebuilds to other bytes than the chain hashed, which only the root's check against the header shows.
type TxResult struct {
	Leaf   []byte
	Err    error    // the leaf cannot be rebuilt: a type outside the set this client knows (a *SchemaError), or bad JSON
	Events []*Event // the chain.Event entries (other event types are not events a reader watches)
}

// BlockResultsFromJSON reads block_results' result into one TxResult per transaction.
func BlockResultsFromJSON(raw []byte) ([]*TxResult, error) {
	var j struct {
		Results *struct {
			DeliverTx []json.RawMessage `json:"deliver_tx"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, err
	}
	if j.Results == nil {
		return nil, nil
	}
	out := make([]*TxResult, len(j.Results.DeliverTx))
	for i, tx := range j.Results.DeliverTx {
		out[i] = resultLeafFromJSON(tx)
	}
	return out, nil
}

func resultLeafFromJSON(tx json.RawMessage) *TxResult {
	var j struct {
		ResponseBase struct {
			Error  json.RawMessage   `json:"Error"`
			Data   string            `json:"Data"`
			Events []json.RawMessage `json:"Events"`
		} `json:"ResponseBase"`
	}
	r := &TxResult{}
	if err := json.Unmarshal(tx, &j); err != nil {
		r.Err = err
		return r
	}
	var errAny []byte
	if len(j.ResponseBase.Error) > 0 && string(j.ResponseBase.Error) != "null" {
		a, err := errorAnyFromJSON(j.ResponseBase.Error)
		if err != nil {
			r.Err = err
		}
		errAny = a
	}
	data, err := b64(j.ResponseBase.Data)
	if err != nil {
		r.Err = err
		return r
	}
	var anys [][]byte
	for _, ev := range j.ResponseBase.Events {
		a, e, err := eventAnyFromJSON(ev)
		if err != nil {
			if r.Err == nil {
				r.Err = err
			}
			continue
		}
		anys = append(anys, a)
		if e != nil {
			r.Events = append(r.Events, e)
		}
	}
	if r.Err == nil {
		r.Leaf = EncodeResult(errAny, data, anys)
	}
	return r
}

// eventAnyFromJSON is the amino Any of an event as the chain serves it in JSON: the types gno.land emits today
// (chain.Event, the two storage events, the bank transfer); another type is an error so a caller skips that block
// knowingly rather than hashing the wrong bytes. A std.Coin, std.Coins or Address field is written in its string
// form, as amino does.
func eventAnyFromJSON(raw json.RawMessage) ([]byte, *Event, error) {
	var head struct {
		Type string `json:"@type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, nil, err
	}
	var body []byte
	switch head.Type {
	case EventTypeURL:
		var j struct {
			Type  string `json:"type"`
			Attrs []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"attrs"`
			PkgPath string `json:"pkg_path"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, nil, err
		}
		e := &Event{Type: j.Type, PkgPath: j.PkgPath}
		for _, a := range j.Attrs {
			e.Attributes = append(e.Attributes, Attribute{a.Key, a.Value})
		}
		return Any(head.Type, EncodeEvent(e)), e, nil
	case "/tm.StorageDepositEvent":
		var j struct {
			BytesDelta json.Number     `json:"bytes_delta"`
			FeeDelta   json.RawMessage `json:"fee_delta"`
			PkgPath    string          `json:"pkg_path"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, nil, err
		}
		body = putZigzagField(body, 1, num(j.BytesDelta))
		body = putBytesField(body, 2, []byte(coinString(j.FeeDelta)))
		body = putBytesField(body, 3, []byte(j.PkgPath))
	case "/tm.StorageUnlockEvent":
		var j struct {
			BytesDelta     json.Number     `json:"bytes_delta"`
			FeeRefund      json.RawMessage `json:"fee_refund"`
			PkgPath        string          `json:"pkg_path"`
			RefundWithheld bool            `json:"refund_withheld"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, nil, err
		}
		body = putZigzagField(body, 1, num(j.BytesDelta))
		body = putBytesField(body, 2, []byte(coinString(j.FeeRefund)))
		body = putBytesField(body, 3, []byte(j.PkgPath))
		if j.RefundWithheld {
			body = putUvarintField(body, 4, 1)
		}
	case "/bank.TransferEvent":
		var j struct {
			From  string          `json:"from"`
			To    string          `json:"to"`
			Coins json.RawMessage `json:"coins"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, nil, err
		}
		body = putBytesField(body, 1, []byte(j.From))
		body = putBytesField(body, 2, []byte(j.To))
		body = putBytesField(body, 3, []byte(coinString(j.Coins)))
	default:
		return nil, nil, &SchemaError{Kind: "event", TypeURL: head.Type}
	}
	return Any(head.Type, body), nil, nil
}

// coinString is a std.Coin or std.Coins as amino writes it: the string the node already serves, or, when a
// client serves the object form, "<amount><denom>" joined by commas.
func coinString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var one struct {
		Denom  string      `json:"denom"`
		Amount json.Number `json:"amount"`
	}
	if json.Unmarshal(raw, &one) == nil && one.Denom != "" {
		return string(one.Amount) + one.Denom
	}
	var many []struct {
		Denom  string      `json:"denom"`
		Amount json.Number `json:"amount"`
	}
	if json.Unmarshal(raw, &many) == nil {
		out := ""
		for i, c := range many {
			if i > 0 {
				out += ","
			}
			out += string(c.Amount) + c.Denom
		}
		return out
	}
	return ""
}

// ResultLeaves are the leaves of a block's results, or an error naming the first leaf that cannot be rebuilt
// (its position and why): a proof needs every leaf's hash.
func ResultLeaves(results []*TxResult) ([][]byte, error) {
	out := make([][]byte, len(results))
	for i, r := range results {
		if r.Err != nil {
			return nil, fmt.Errorf("transaction %d of the block: %w", i, r.Err)
		}
		out[i] = r.Leaf
	}
	return out, nil
}
