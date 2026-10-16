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

	"github.com/gnolang/bridges/ethereum/bridge/ethx"
)

// Alarmer is where every alarm goes (the Solana agent's, carried): the log, always, and a webhook as one JSON POST —
// `{"source","level","key","text","at"}` — when one is configured. An alarm repeats on the webhook once an hour
// while its condition stands and once more when it clears; the log sees every raise. Every text is redacted.
type Alarmer struct {
	URL    string
	Source string // "signer", "relayer", "monitor", "hlwatch"
	Repeat time.Duration
	HTTP   *http.Client

	mu       sync.Mutex
	standing map[string]time.Time // key → last posted
	text     map[string]string
	flags    map[string]bool      // the standing keys raised as flags (Flag): what hlwatch's green never stands over
	flagged  map[string]time.Time // key → last posted at flag level (an escalation is posted once a repeat: E6-GT4)
}

// Alarm raises an alarm under a key ("client:lag", "runway:relayer-eth", …).
func (a *Alarmer) Alarm(key, text string) { a.raise("alarm", key, text, 0) }

// AlarmEvery raises an alarm that repeats on the webhook at its own cadence while it stands (G35: a standing
// condition of months — mint:cap-ratio under the launch week's lock — posted hourly drowned the new keys).
func (a *Alarmer) AlarmEvery(key, text string, every time.Duration) {
	a.raise("alarm", key, text, every)
}

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
	delete(a.flags, key)
	delete(a.flagged, key)
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

// StandingFlags are the standing keys raised as flags (Flag: a flag the machine set, or would set), sorted.
func (a *Alarmer) StandingFlags() []string {
	var keys []string
	if a == nil {
		return keys
	}
	a.mu.Lock()
	for k := range a.flags {
		keys = append(keys, k)
	}
	a.mu.Unlock()
	sort.Strings(keys)
	return keys
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
	text = ethx.Redact(text) // no provider URL's key reaches the log, the status or the webhook (G16)
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
	if a.flags == nil {
		a.flags = map[string]bool{}
	}
	if a.flagged == nil {
		a.flagged = map[string]time.Time{}
	}
	last, was := a.standing[key]
	a.text[key] = text
	// a standing alarm raised again at flag level is posted at once, not at its next repeat: the escalation is the news
	// (a finality stall past the hour, a pending reading unjudged for one) — once a repeat: a key raised at alarm and at
	// flag level by turns (the sources rule over a flaky third source: own node odd, then unresolved) posted every other
	// poll (the Go trust audit's E6-GT4, item 2)
	posted, flaggedBefore := a.flagged[key]
	rising := was && level == "flag" && !a.flags[key] && (!flaggedBefore || time.Since(posted) >= repeat)
	if level == "flag" {
		a.flags[key] = true
	} else {
		delete(a.flags, key) // the key raised again as an alarm: its level is the last raise's
	}
	due := !was || rising || time.Since(last) >= repeat
	if due {
		a.standing[key] = time.Now()
		if level == "flag" {
			a.flagged[key] = time.Now()
		}
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
