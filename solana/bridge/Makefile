# The bridge's site (bridge/SITE.md). Everything else on this branch is driven by local/e2e.sh and `go test`.
#
#   make site-qa     the whole gate: vet, the Go tests, the browser suite (Playwright; runs on the devbox)
#   make site-go     the Go half alone (fine on the Mac under nice)
#   make site-e2e    the browser half alone
#   make site-run    the mock deployment on 127.0.0.1:8095, for a look

site-qa: site-go site-e2e

site-go:
	go vet ./cmd/hlsite/
	go test ./cmd/hlsite/

site-e2e:
	node site/e2e/run.js

site-run:
	go run ./cmd/hlsite -mock -site site -listen 127.0.0.1:8095

.PHONY: site-qa site-go site-e2e site-run
