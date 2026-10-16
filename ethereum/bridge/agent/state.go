package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State files (the Solana agent's rule, carried): written atomically — the bytes to a temporary file, fsynced,
// renamed over the file, the directory fsynced — so a power loss leaves the old file or the new one; a missing file
// is an empty state, a damaged one an error naming the corruption, so a human deletes or restores it knowingly.

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

var errCorrupt = errors.New("corrupt")

func readJSON(path string, v any) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return fmt.Errorf("%s is %w: zero length (a write that never finished); restore it or delete it knowingly", path, errCorrupt)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s is %w: %v; restore it or delete it knowingly", path, errCorrupt, err)
	}
	return nil
}

// errNotYet is a wait, never a failure.
var errNotYet = errors.New("not yet")

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func when(t int64) string { return time.Unix(t, 0).UTC().Format(time.RFC3339) }

func copyMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func keysOf[V any](m map[string]V) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func short(s string) string {
	s = strings.TrimPrefix(s, "0x")
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
