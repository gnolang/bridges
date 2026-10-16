package ethx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// The events the machines read back from Ethereum's logs (committed by the receipts root, never truncated: the
// record the watch rebuilds, PLAN.md §6.2), decoded: the client's HeaderRecorded and StageProgress (the updater's
// resumption of a staged update, the last update's time and gas; the monitor's check of every stage against
// gno.land's own headers), HlAdmin's DecisionRecorded and DecisionExecuted.

// HeaderRecorded is the client's record of a header it accepted (Proven false) or proved.
type HeaderRecorded struct {
	Hash        [32]byte
	Height      int64
	Epoch       uint64
	Record      uint64
	RunningHash [32]byte
	Proven      bool
	Block       uint64
	BlockHash   common.Hash
	Tx          common.Hash
	Index       uint
}

// StageProgress is a stage's tally after a call that did not cross 2/3: since fold F4 it carries the header's height
// (the watch holds the height and the hash against gno.land without reading the call), and every one carries at least
// one new signature (a call that verifies none reverts NoNewSignature).
type StageProgress struct {
	Key        [32]byte
	Submitter  common.Address
	HeaderHash [32]byte
	Height     int64
	Tallied    int64
	Total      int64
	Block      uint64
	Tx         common.Hash
}

// DecisionRecorded is HlAdmin's record of a decision.
type DecisionRecorded struct {
	Number   uint64
	Kind     uint8
	MatureAt uint64
	Block    uint64
	Tx       common.Hash
}

// DecisionExecuted is HlAdmin's log of a decision it ran (a cancel too, at its recording): the monitors read the
// proxies' EIP-1967 slots after a kind 8 (E3-RT8, E3-AS7).
type DecisionExecuted struct {
	Number uint64
	Kind   uint8
	Block  uint64
	Index  uint
	Tx     common.Hash
}

func topicInt64(t common.Hash) int64 { return int64(binary.BigEndian.Uint64(t[24:])) }

func topicUint(t common.Hash) (uint64, bool) {
	for _, b := range t[:24] {
		if b != 0 {
			return 0, false
		}
	}
	return binary.BigEndian.Uint64(t[24:]), true
}

func checkLog(l types.Log, c *Contract, event string, topics int) error {
	if len(l.Topics) != topics || l.Topics[0] != c.Event(event) {
		return fmt.Errorf("not a %s.%s log", c.Name, event)
	}
	if l.Removed {
		return errors.New("a removed log")
	}
	return nil
}

// DecodeHeaderRecorded decodes a client's HeaderRecorded log.
func DecodeHeaderRecorded(l types.Log) (*HeaderRecorded, error) {
	if err := checkLog(l, LightClient, "HeaderRecorded", 3); err != nil {
		return nil, err
	}
	vals, err := LightClient.ABI.Events["HeaderRecorded"].Inputs.NonIndexed().Unpack(l.Data)
	if err != nil {
		return nil, err
	}
	if len(vals) != 4 {
		return nil, errors.New("HeaderRecorded: not four values")
	}
	return &HeaderRecorded{Hash: l.Topics[1], Height: topicInt64(l.Topics[2]), Epoch: vals[0].(uint64), Record: vals[1].(uint64), RunningHash: vals[2].([32]byte), Proven: vals[3].(bool),
		Block: l.BlockNumber, BlockHash: l.BlockHash, Tx: l.TxHash, Index: l.Index}, nil
}

// DecodeStageProgress decodes a client's StageProgress log.
func DecodeStageProgress(l types.Log) (*StageProgress, error) {
	if err := checkLog(l, LightClient, "StageProgress", 3); err != nil {
		return nil, err
	}
	vals, err := LightClient.ABI.Events["StageProgress"].Inputs.NonIndexed().Unpack(l.Data)
	if err != nil {
		return nil, err
	}
	if len(vals) != 4 {
		return nil, errors.New("StageProgress: not four values")
	}
	return &StageProgress{Key: l.Topics[1], Submitter: common.BytesToAddress(l.Topics[2][12:]), HeaderHash: vals[0].([32]byte), Height: vals[1].(int64), Tallied: vals[2].(int64),
		Total: vals[3].(int64), Block: l.BlockNumber, Tx: l.TxHash}, nil
}

// DecodeDecisionRecorded decodes HlAdmin's DecisionRecorded log.
func DecodeDecisionRecorded(l types.Log) (*DecisionRecorded, error) {
	if err := checkLog(l, Admin, "DecisionRecorded", 2); err != nil {
		return nil, err
	}
	n, ok := topicUint(l.Topics[1])
	if !ok {
		return nil, errors.New("DecisionRecorded: a number past 64 bits")
	}
	vals, err := Admin.ABI.Events["DecisionRecorded"].Inputs.NonIndexed().Unpack(l.Data)
	if err != nil {
		return nil, err
	}
	if len(vals) != 2 {
		return nil, errors.New("DecisionRecorded: not two values")
	}
	return &DecisionRecorded{Number: n, Kind: vals[0].(uint8), MatureAt: vals[1].(uint64), Block: l.BlockNumber, Tx: l.TxHash}, nil
}

// DecodeDecisionExecuted decodes HlAdmin's DecisionExecuted log.
func DecodeDecisionExecuted(l types.Log) (*DecisionExecuted, error) {
	if err := checkLog(l, Admin, "DecisionExecuted", 2); err != nil {
		return nil, err
	}
	n, ok := topicUint(l.Topics[1])
	if !ok {
		return nil, errors.New("DecisionExecuted: a number past 64 bits")
	}
	vals, err := Admin.ABI.Events["DecisionExecuted"].Inputs.NonIndexed().Unpack(l.Data)
	if err != nil {
		return nil, err
	}
	if len(vals) != 1 {
		return nil, errors.New("DecisionExecuted: not one value")
	}
	return &DecisionExecuted{Number: n, Kind: vals[0].(uint8), Block: l.BlockNumber, Index: l.Index, Tx: l.TxHash}, nil
}

// HashTopic is a bytes32 as a log topic; AddressTopic an address; Uint32Topic a uint32.
func HashTopic(b [32]byte) common.Hash { return common.Hash(b) }

func AddressTopic(a common.Address) common.Hash { return common.BytesToHash(a.Bytes()) }

func Uint32Topic(v uint32) common.Hash { return common.BigToHash(new(big.Int).SetUint64(uint64(v))) }
