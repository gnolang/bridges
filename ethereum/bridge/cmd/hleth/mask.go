package main

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"github.com/gnolang/bridges/ethereum/bridge/repro"
)

// The code check of the audit (PLAN.md §5.8, §5.9) is the repro package's (shared with hlwatch, which holds a kind 8's
// implementation and a kind 9's successor to the same rule before the vote): a deployed contract's runtime code
// against its build, byte for byte, except where the build leaves an immutable's 32 bytes to its constructor — those
// are masked out of the comparison and read instead, each beside the value it must have (the red team's ER1).

type (
	Reading   = repro.Reading
	Expect    = repro.Expect
	Judgement = repro.Judgement
)

func CompareCode(onchain []byte, a *Artifact) ([]Reading, string) {
	return repro.CompareCode(onchain, a)
}

func JudgeCode(contract string, at common.Address, onchain []byte, a *Artifact, want map[string]Expect) []Judgement {
	return repro.JudgeCode(contract, at, onchain, a, want)
}

func expectAddr(a common.Address, what string) Expect { return repro.ExpectAddr(a, what) }

func expectUint(n *big.Int, what string) Expect { return repro.ExpectUint(n, what) }

func expectU64(n uint64, what string) Expect { return repro.ExpectU64(n, what) }

func expectBytes32(b [32]byte, what string) Expect { return repro.ExpectBytes32(b, what) }

func isZero(b []byte) bool { return repro.IsZero(b) }

func failures(js []Judgement) int { return repro.Failures(js) }

func judgementText(js []Judgement) string { return repro.JudgementText(js) }

const (
	kindAddr    = repro.KindAddr
	kindUint    = repro.KindUint
	kindBytes32 = repro.KindBytes32
)
