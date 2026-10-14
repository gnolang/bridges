package gno

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gnolang/bridges/solana/bridge/gnolight"
)

// What the light client's feeders read from a node: a block's header with the hash the chain computed for it,
// a height's commit (the precommits of the set that signed it, with their signatures), the validator set at a
// height, and a block's results (the leaves of LastResultsHash, re-encoded, and the events the updater watches).

// BlockAt is the block at a height, decoded: its header, its hash, its parts and (when the block before it had
// transactions) nothing more — the results come from BlockResults.
func (c *Client) BlockAt(ctx context.Context, height int64) (*gnolight.Block, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "block", map[string]any{"height": strconv.FormatInt(height, 10)}, &raw); err != nil {
		return nil, err
	}
	b, err := gnolight.BlockFromJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("gno block %d: %w", height, err)
	}
	if b.Header.Height != height {
		return nil, fmt.Errorf("gno block %d: the node served height %d", height, b.Header.Height)
	}
	return b, nil
}

// CommitAt is the commit for a height: the one signed by the validators of that height (what block H+1 carries
// as LastCommit), as the client stages it, and every vote with its signature.
func (c *Client) CommitAt(ctx context.Context, height int64) (*gnolight.Commit, []*gnolight.Vote, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "commit", map[string]any{"height": strconv.FormatInt(height, 10)}, &raw); err != nil {
		return nil, nil, err
	}
	cm, votes, err := gnolight.CommitFromJSON(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("gno commit %d: %w", height, err)
	}
	for _, v := range votes {
		if v != nil && v.Height != height {
			return nil, nil, fmt.Errorf("gno commit %d: a vote for height %d", height, v.Height)
		}
	}
	return cm, votes, nil
}

// ValidatorsAt is the validator set at a height (the set that signs that height), in the chain's order.
func (c *Client) ValidatorsAt(ctx context.Context, height int64) (*gnolight.ValidatorSet, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "validators", map[string]any{"height": strconv.FormatInt(height, 10)}, &raw); err != nil {
		return nil, err
	}
	s, err := gnolight.ValidatorsFromJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("gno validators %d: %w", height, err)
	}
	if err := s.Check(); err != nil {
		return nil, fmt.Errorf("gno validators %d: %w", height, err)
	}
	return s, nil
}

// BlockResults are a block's per-transaction results: each leaf re-encoded (or why it cannot be) and its events.
func (c *Client) BlockResults(ctx context.Context, height int64) ([]*gnolight.TxResult, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "block_results", map[string]any{"height": strconv.FormatInt(height, 10)}, &raw); err != nil {
		return nil, err
	}
	rs, err := gnolight.BlockResultsFromJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("gno block_results %d: %w", height, err)
	}
	return rs, nil
}

// ErrNoHeight is a height the node does not have yet (or pruned).
var ErrNoHeight = errors.New("gno: no block at that height")

// IsNoHeight says whether an RPC error is the node lacking the height (the head not reached, or pruned).
func IsNoHeight(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "must be less than or equal to the current blockchain height") || strings.Contains(s, "height") && (strings.Contains(s, "not available") || strings.Contains(s, "pruned") || strings.Contains(s, "lowest height"))
}

// ValsetEvent is whether a block's events carry r/sys/validators' ValidatorAdded or ValidatorRemoved (the
// updater's trigger for the announcing header, §6.1).
func ValsetEvent(results []*gnolight.TxResult) bool {
	for _, r := range results {
		for _, e := range r.Events {
			if (e.Type == "ValidatorAdded" || e.Type == "ValidatorRemoved") && strings.HasPrefix(e.PkgPath, "gno.land/r/sys/validators") {
				return true
			}
		}
	}
	return false
}
