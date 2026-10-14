package main

import (
	"context"
	"time"
)

// Other is the far side of every route, what the site asks of a chain (bridge/SITE.md): Solana is the one
// implementation (solana.go); a second chain implements this and is listed in the config. The site never
// sends anything itself: it reads, and builds one unsigned transaction for a wallet to sign.
type Other interface {
	// Snapshot reads the chain's side at once: the supplies at a slot, the flags, the limits, the client, the
	// pending decisions. A section that cannot be read is named in Errors; the rest is served.
	Snapshot(ctx context.Context, now time.Time) *OtherSnapshot
	// DeliveryOf is the transaction that delivered one of gno.land's messages there, nil when none has.
	DeliveryOf(ctx context.Context, id [32]byte) (*TxRef, error)
	// Burn is every burn toward gno.land one transaction made, by its signature: none when it made no burn.
	Burn(ctx context.Context, sig string) ([]Burn, error)
	// BurnTx builds the unsigned transaction of a burn toward gno.land for a wallet to sign.
	BurnTx(ctx context.Context, t TokenConfig, sender, recipient string, amount uint64) (*BurnTx, error)
	// IsSignature says whether a tracking reference is one of this chain's transaction signatures.
	IsSignature(ref string) bool
	TxLink(sig string) string
	AccountLink(k string) string
}

// FlagJSON is one flag's moment: the chain's slot and unix time.
type FlagJSON struct {
	Slot uint64 `json:"slot"`
	Time int64  `json:"time"`
}

// GuardiansJSON is one object's seats, flags and hold (PLAN.md §5.8, §6.1).
type GuardiansJSON struct {
	Keys      []string    `json:"keys"`      // seats 1–4, then watch slots 5–6
	Flags     []*FlagJSON `json:"flags"`     // one per key; null when clear
	HoldSince *FlagJSON   `json:"holdSince"` // the standing hold's start, null when none
	Hold      bool        `json:"hold"`      // a hold stands by the program's rule now
}

// LimitJSON is a token's mint limits on the pausable ISM (§5.4): the per-message maximum, the day cap and the
// bucket's level now.
type LimitJSON struct {
	MaxPerMessage uint64 `json:"maxPerMessage"`
	DayCap        uint64 `json:"dayCap"`
	Used          uint64 `json:"used"`
}

// PendingJSON is one decision pending on the admin program and when it matures (§6.5).
type PendingJSON struct {
	Number    uint64 `json:"number"`
	Kind      byte   `json:"kind"`
	KindName  string `json:"kindName"`
	Text      string `json:"text"`
	Delivered int64  `json:"delivered"`
	MaturesAt int64  `json:"maturesAt"`
	Deferred  string `json:"deferred"` // what holds it past its clock, "" when nothing
}

// ClientJSON is the light client's state (§6.1).
type ClientJSON struct {
	TrustedHeight uint64 `json:"trustedHeight"`
	QuarantineEnd int64  `json:"quarantineEnd"`
	LastAccepted  int64  `json:"lastAccepted"`
	ClockFactor   int64  `json:"clockFactor"`
}

// OtherSnapshot is one reading of the far side.
type OtherSnapshot struct {
	Slot      uint64               `json:"slot"`
	At        int64                `json:"at"`
	Supplies  map[string]int64     `json:"supplies"` // by symbol, base units
	Pausable  *GuardiansJSON       `json:"pausable"`
	Admin     *GuardiansJSON       `json:"admin"`
	Limits    map[string]LimitJSON `json:"limits"` // by symbol
	Client    *ClientJSON          `json:"client"`
	LastSeq   uint64               `json:"lastSeq"`
	SetupDone bool                 `json:"setupDone"`
	Pending   []PendingJSON        `json:"pending"`
	Errors    []string             `json:"errors"`
}

// TxRef is a transaction on the far side.
type TxRef struct {
	Sig  string `json:"sig"`
	Slot uint64 `json:"slot"`
	Time int64  `json:"time"`
	Link string `json:"link"`
}

// Burn is one burn toward gno.land as the far side's mailbox recorded it.
type Burn struct {
	ID     string `json:"id"`    // the message id, hex
	Token  string `json:"token"` // the route's symbol ("" when the sender is no token of ours)
	Amount int64  `json:"amount"`
	To     string `json:"to"`           // the gno.land recipient, "" when the bytes are no gno address
	BadTo  bool   `json:"badRecipient"` // the recipient is no gno address: the realm sweeps it (§5.2)
	Nonce  uint32 `json:"nonce"`
	Slot   uint64 `json:"slot"`
	Time   int64  `json:"time"`
	Sig    string `json:"sig"`
}

// BurnTx is an unsigned burn transaction for a wallet.
type BurnTx struct {
	Tx                   string `json:"tx"`                   // the serialized transaction, base64, the wallet's signature slot empty
	Unique               string `json:"unique"`               // the throwaway key that names the dispatch record (signed already)
	MessageAccount       string `json:"messageAccount"`       // the dispatch record the burn will write
	Blockhash            string `json:"blockhash"`            //
	LastValidBlockHeight uint64 `json:"lastValidBlockHeight"` // the transaction lapses past it
}
