package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Alarmer is where every alarm goes: the log, always, and a webhook as one JSON POST — `{"source","level",
// "key","text","at"}` — when one is configured (the box's chat hook is one consumer). An alarm repeats on the
// webhook once an hour while its condition stands and once more when it clears; the log sees every raise.
type Alarmer struct {
	URL    string
	Source string // "signer", "relayer", "monitor", "hlwatch"
	Repeat time.Duration
	HTTP   *http.Client

	mu       sync.Mutex
	standing map[string]time.Time // key → last posted
	text     map[string]string
}

// Alarm raises an alarm under a key ("identity:BUBBLE", "runway:relayer-sol", …).
func (a *Alarmer) Alarm(key, text string) { a.raise("alarm", key, text, 0) }

// AlarmEvery raises an alarm that repeats on the webhook at its own cadence while it stands (G35: a standing
// condition of months — mint:cap-ratio under the launch week's lock — posted hourly drowned the new keys).
func (a *Alarmer) AlarmEvery(key, text string, every time.Duration) { a.raise("alarm", key, text, every) }

// Flag reports a flag the machine set (or would set), the loudest level.
func (a *Alarmer) Flag(key, text string) { a.raise("flag", key, text, 0) }

// Clear ends a standing alarm; the webhook hears it once.
func (a *Alarmer) Clear(key string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	_, was := a.standing[key]
	delete(a.standing, key)
	delete(a.text, key)
	a.mu.Unlock()
	if was {
		log.Printf("%s: cleared %s", a.Source, key)
		a.post("clear", key, "cleared")
	}
}

// Standing lists the alarms that stand now, by key.
func (a *Alarmer) Standing() map[string]string {
	out := map[string]string{}
	if a == nil {
		return out
	}
	a.mu.Lock()
	for k, v := range a.text {
		out[k] = v
	}
	a.mu.Unlock()
	return out
}

// StandingKeys is the standing alarms' keys, sorted.
func (a *Alarmer) StandingKeys() []string {
	var keys []string
	for k := range a.Standing() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (a *Alarmer) raise(level, key, text string, every time.Duration) {
	text = Redact(text) // no provider URL's key reaches the log, the status or the webhook (G16)
	if a == nil {
		log.Printf("%s %s: %s", level, key, text)
		return
	}
	log.Printf("%s: %s %s: %s", a.Source, level, key, text)
	repeat := a.Repeat
	if repeat == 0 {
		repeat = time.Hour
	}
	if every > repeat {
		repeat = every // the key's own cadence, when longer than the Alarmer's
	}
	a.mu.Lock()
	if a.standing == nil {
		a.standing, a.text = map[string]time.Time{}, map[string]string{}
	}
	last, was := a.standing[key]
	a.text[key] = text
	due := !was || time.Since(last) >= repeat
	if due {
		a.standing[key] = time.Now()
	}
	a.mu.Unlock()
	if due {
		a.post(level, key, text)
	}
}

func (a *Alarmer) post(level, key, text string) {
	if a.URL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"source": a.Source, "level": level, "key": key, "text": text, "at": time.Now().UTC().Format(time.RFC3339)})
	c := a.HTTP
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", a.URL, bytes.NewReader(body))
		if err != nil {
			log.Printf("%s: webhook: %v", a.Source, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			log.Printf("%s: webhook: %v", a.Source, err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Printf("%s: webhook: %s", a.Source, resp.Status)
		}
	}()
}
