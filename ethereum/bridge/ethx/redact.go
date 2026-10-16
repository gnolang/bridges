package ethx

import (
	"io"
	"regexp"
	"strings"
)

// A provider's URL carries its key (`https://…/v2/KEY`, `https://…/?apikey=KEY`), and go-ethereum's and net/http's
// errors print the URL whole — so every alarm, note, status and log line could hand the key to the webhook's
// readers, the status endpoints' and the logs'. Redact cuts every http(s) and ws(s) URL in a text down to its
// scheme and host: the userinfo, the path, the query string and the fragment go (the Solana bridge's rule, G16,
// with the websocket schemes an Ethereum provider also serves).

var urlRE = regexp.MustCompile(`(?:https?|wss?)://[^\s"'<>()\[\]]+`)

// Redact strips every http(s) or ws(s) URL in a text to scheme://host, with "/…" where more followed.
func Redact(text string) string {
	if !strings.Contains(text, "://") {
		return text
	}
	return urlRE.ReplaceAllStringFunc(text, func(u string) string {
		// trailing prose punctuation is not the URL's
		tail := ""
		for len(u) > 0 && strings.ContainsRune(":,;.", rune(u[len(u)-1])) {
			tail = string(u[len(u)-1]) + tail
			u = u[:len(u)-1]
		}
		scheme, rest, _ := strings.Cut(u, "://")
		host := rest
		more := ""
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			host, more = rest[:i], "/…"
		}
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		return scheme + "://" + host + more + tail
	})
}

// RedactWriter is a log sink that redacts every line it is given (the machines' and the tools' log output).
type RedactWriter struct{ W io.Writer }

func (r RedactWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(r.W, Redact(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
