#!/bin/sh
# Ship bridge.gno.world to the box (bridge/SITE.md): hlsite as a systemd service on the loopback, the pages
# beside it, the nginx vhost in front, TLS by certbot once the DNS record points here.
#
#   bridge/site/deploy.sh root@kourt.xyz
#
# Run from devbox (the standing rule: builds, gates and deploys run on a droplet, never the Mac), from a clean
# checkout of main. THE GATES: the tree is committed, the Go suite and the browser suite pass (make -C bridge
# site-qa), and bridge/site/site.json exists with no FILL left in it — the config is Jae's: the program ids
# from `hlsol setup`, the provider URL. What is NOT here: the DNS record for bridge.gno.world (Jae's hand, before
# the first run: certbot needs it to answer from this box) and any key — hlsite holds none.
set -eu

HOST=${1:?usage: [PREVIEW=1] bridge/site/deploy.sh user@host}
# PREVIEW=1 ships the canned deployment as a public preview (a banner, example figures, sending refused) before
# the bridge's programs exist: no site.json is needed or installed; a systemd drop-in runs hlsite -preview.
PREVIEW=${PREVIEW:-}
cd "$(dirname "$0")/../.."   # the repo root
BRIDGE=bridge
CONF=$BRIDGE/site/site.json

if [ -n "$(git status --porcelain --untracked-files=no 2>/dev/null)" ]; then echo "deploy: uncommitted changes — commit, then deploy" >&2; exit 1; fi
if [ -z "$PREVIEW" ]; then
  [ -f "$CONF" ] || { echo "deploy: $CONF is missing — copy site.example.json, fill the program ids and the provider (never committed: the provider URL may carry a key)" >&2; exit 1; }
  if grep -q 'FILL' "$CONF"; then echo "deploy: $CONF still has FILL placeholders" >&2; exit 1; fi
fi

echo "deploy: the gates (go vet, go test, the browser suite)"
make -C "$BRIDGE" site-qa

echo "deploy: building hlsite for the box"
OUT=$(mktemp -d)
trap 'rm -rf "$OUT"' EXIT
(cd "$BRIDGE" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags=-s -o "$OUT/hlsite" ./cmd/hlsite)
# the config is checked by the binary we ship, against the pages we ship, before anything leaves this machine
if [ -n "$PREVIEW" ]; then ARGS="-preview"; else ARGS="-config site/site.json"; fi
(cd "$BRIDGE" && go build -o "$OUT/hlsite-check" ./cmd/hlsite)
(cd "$BRIDGE" && "$OUT/hlsite-check" $ARGS -site site -listen 127.0.0.1:0 >/dev/null 2>"$OUT/check.log" &
 sleep 2; pkill -f "$OUT/hlsite-check" 2>/dev/null || { cat "$OUT/check.log" >&2; echo "deploy: hlsite refused to start ($ARGS)" >&2; exit 1; })

echo "deploy: uploading"
ssh "$HOST" 'mkdir -p /root/.deploy-hlsite'
rsync -az --delete --exclude e2e --exclude site.json --exclude deploy.sh --exclude '*.service' --exclude nginx.conf "$BRIDGE/site/" "$HOST:/root/.deploy-hlsite/site/"
rsync -az "$OUT/hlsite" "$BRIDGE/site/nginx.conf" "$BRIDGE/site/hlsite.service" "$HOST:/root/.deploy-hlsite/"
[ -n "$PREVIEW" ] || rsync -az "$CONF" "$HOST:/root/.deploy-hlsite/"

echo "deploy: installing on the box"
ssh "$HOST" "PREVIEW='$PREVIEW'; "'set -eu
install -d -m 755 /usr/local/share/hlsite
rsync -a --delete /root/.deploy-hlsite/site/ /usr/local/share/hlsite/site/
install -m 755 /root/.deploy-hlsite/hlsite /usr/local/bin/hlsite.new && mv /usr/local/bin/hlsite.new /usr/local/bin/hlsite
install -d -m 700 /etc/hlsite
install -m 644 /root/.deploy-hlsite/hlsite.service /etc/systemd/system/hlsite.service
if [ -n "$PREVIEW" ]; then
  install -d -m 755 /etc/systemd/system/hlsite.service.d
  printf "%s\n" "[Service]" "LoadCredential=" "ExecStart=" "ExecStart=/usr/local/bin/hlsite -preview -site /usr/local/share/hlsite/site -listen 127.0.0.1:8195" > /etc/systemd/system/hlsite.service.d/preview.conf
else
  install -m 600 /root/.deploy-hlsite/site.json /etc/hlsite/site.json
  rm -f /etc/systemd/system/hlsite.service.d/preview.conf
fi
# certbot rewrites the vhost once TLS is on, so only the first deploy installs it; the enabled one is a symlink
[ -e /etc/nginx/sites-available/bridge.gno.world ] || install -m 644 /root/.deploy-hlsite/nginx.conf /etc/nginx/sites-available/bridge.gno.world
ln -sf /etc/nginx/sites-available/bridge.gno.world /etc/nginx/sites-enabled/bridge.gno.world
nginx -t
systemctl daemon-reload
systemctl enable -q hlsite
systemctl restart hlsite
sleep 1
curl -sf http://127.0.0.1:8195/api/chains | grep -q "\"chains\"" || { journalctl -u hlsite -n 20 --no-pager; echo "hlsite did not answer" >&2; exit 1; }
systemctl reload nginx
echo "hlsite up: $(curl -s http://127.0.0.1:8195/api/chains | head -c 120)"
# TLS: only once the name resolves to this box and no certificate stands yet (the DNS record is Jae'"'"'s hand)
ME=$(curl -s -4 https://ifconfig.me || true)
if [ -n "$ME" ] && [ "$(dig +short bridge.gno.world | tail -1)" = "$ME" ]; then
  [ -e /etc/letsencrypt/live/bridge.gno.world ] || certbot --nginx -d bridge.gno.world --non-interactive --agree-tos --register-unsafely-without-email --redirect
  echo "TLS: $(ls /etc/letsencrypt/live/bridge.gno.world 2>/dev/null | head -1 || echo none)"
else
  echo "TLS: skipped — bridge.gno.world does not resolve to this box yet (DNS is Jae'"'"'s hand); run certbot --nginx -d bridge.gno.world once it does"
fi'
echo "deploy: done"
