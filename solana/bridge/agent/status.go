package agent

import (
	"encoding/json"
	"net/http"
	"sync"
)

// statusBox is what a machine's status endpoint serves: a snapshot the loop publishes at the end of each round,
// encoded under the box's own lock, so a reader never walks the loop's live maps (G5: an encoder iterating a
// map the loop writes aborts the process; a flood of status requests was a crash loop).
type statusBox struct {
	mu sync.Mutex
	b  []byte
}

// publish encodes v now and keeps the bytes, every URL in them redacted (G16).
func (s *statusBox) publish(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"error": err.Error()})
	}
	b = []byte(Redact(string(b)))
	s.mu.Lock()
	s.b = b
	s.mu.Unlock()
}

// bytes is the last snapshot ("{}" before the first).
func (s *statusBox) bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.b == nil {
		return []byte("{}")
	}
	return append([]byte(nil), s.b...)
}

// get decodes the last snapshot (what tests read).
func (s *statusBox) get() map[string]any {
	var out map[string]any
	_ = json.Unmarshal(s.bytes(), &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// handler serves GET /v1/status from the snapshot.
func (s *statusBox) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(s.bytes())
	})
	return mux
}

// copyMap is a shallow copy of a map (the values are plain or immutable), for a snapshot built under a lock.
func copyMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
