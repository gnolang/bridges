package agent

import (
	"io"
	"regexp"
	"strings"
)

// A provider's URL carries its key (G16: `https://…/?api-key=K` is the common form, `https://…/v2/K` another),
// and solana-go's and net/http's errors print the URL whole — so every alarm, note, status and log line could
// hand the key to the webhook's readers, the status endpoints' and the logs'. Redact cuts every http(s) URL in a
// text down to its scheme and host: the userinfo, the path, the query string and the fragment go.

var urlRE = regexp.MustCompile(`https?://[^\s"'<>()\[\]]+`)

// Redact strips every http(s) URL in a text to scheme://host, with "/…" where more followed (G16).
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
