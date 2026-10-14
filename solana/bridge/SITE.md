# M5 — the site: bridge.gno.world (2026-10-14; branch bridge-m5 from bridge 5147b834)

Jae, 2026-10-14: "I think we should make the site, and have bridge.gno.world point to it, to work for other chains in
the future but just Solana for now." This is that site: static pages and a small Go backend that reads both
chains for the page, builds one unsigned transaction, and holds no key. Nothing is deployed; the deploy script is
written and reviewed, not run (the DNS record is Jae's hand, §How to deploy).

## What it is

**`bridge/site/`** — the pages, plain HTML, CSS and ES modules, no build step:

- `/` Home: the plain-English story of `PLAN.md` §1 in four short paragraphs — what it does, how it is checked,
  who can stop it, the honest limits — no jargon.
- `/bridge` Bridge: a chain picker (Solana, the one chain listed), a direction toggle, token, amount, recipient,
  one line of figures from the status (the fee, the minimum, the maximum per transfer, the day cap and what is
  available now, a pause if one stands), the wallet button, Send; then a tracker that polls the transfer every five
  seconds until it lands. A track box takes any reference by hand.
  - gno.land → Solana through Adena (`window.adena`): one `DoContract` with the exact envelope the realm requires —
    GNOT sends `amount + fee` in ugnot with the `TransferRemote` call (hlgnot.gno reads `unsafe.OriginSend()` and
    refuses anything else); BUBBLE sends two messages in one transaction, `bubble.Approve(route, amount + fee)`
    then `hlbubble.TransferRemote`. The gas is the backend's figure (`gasWanted` 60M, the fee at the chain's price
    from `auth/gasprice`, 0.001 ugnot/gas at rest). The transaction hash Adena answers is what is tracked.
  - Solana → gno.land: the page asks the backend for the burn (`POST /api/solana/burn-tx`), gets the serialized
    transaction, and the wallet signs it as the fee payer and sends it through its own RPC — the Wallet Standard
    first (Phantom, Backpack and the rest register themselves; a transaction is bytes), the `window.solana`
    provider as the fallback (it is handed an object whose `serialize()` returns the bytes). The signature is
    tracked.
- `/status` Status: per token the lock on gno.land against the supply on Solana with owed, voided, lost and the
  gap, and the identity line in the monitors' words; the caps and fees on both sides; the flags on both sides and
  any hold or quarantine; the client's trusted height against gno.land's head; who governs (the realms' own
  governor line); the signer rule and the seats; the pending decisions of every realm and of the admin program with
  when each matures and what defers it. Read every ten seconds.
- `/help` Help: the fees, the minimums, the delays, what "paid in installments" means, what to do when a transfer
  is late (nothing: the relayers retry hourly), links to the realm pages. The launch figures in the text are
  replaced by the live ones.
- `wallets.js` is the whole wallet layer, written here rather than fetched: Adena, the Wallet Standard discovery
  (the `wallet-standard:app-ready` event), the legacy provider, base58 and base64. No SDK, no CDN.

**`bridge/cmd/hlsite`** — the backend, one binary:

- serves the pages with the policy `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:;
  connect-src 'self'; …; frame-ancestors 'none'` and the other security headers on every response, so the page can
  neither call an RPC nor load anything from a CDN; the wallet extensions inject themselves and are not subject to
  it. An inline script is refused by that policy, so every page's script is a file (the browser suite caught one).
- the read-only JSON API, every chain read proxied by the backend:
  - `GET /api/chains` — the chains, their tokens (symbol, program, mint, realm, the realm's address for BUBBLE's
    `Approve`), the gno.land realms, the gas to send with.
  - `GET /api/<chain>/status` — the composition above: the far side's snapshot first (the cut's first step, §5.9),
    then gno.land at its head; cached ten seconds.
  - `GET /api/<chain>/transfer/<ref>` — a transfer's state by message id (a route's `send/<id>` is a transfer out,
    its `claim/<id>` a transfer in), by the wallet's gno.land transaction hash (the node's `tx` call, the
    `SentTransferRemote` event's `id`) or by a Solana signature (the mailbox's dispatch record in that transaction,
    read as the agent's scanner reads it: the `DISPATCH` tag, the slot, the message). The steps: dispatched on
    gno.land → delivered on Solana (the processed-message record, and the newest signature on it as the link);
    burned on Solana → credited on gno.land (the claim, the height from `delivered/<id>`) → paid (in full, or so far
    in installments; swept and voided are named). A burn the signers have not confirmed yet is known by its
    signature alone, and the page says so.
  - `POST /api/<chain>/burn-tx` `{token, sender, recipient, amount}` — the transaction `hlsol send` builds, exactly:
    a 400,000-CU budget, then the token program's `transfer_remote` toward gno.land, the sender paying. The
    throwaway keypair that names the dispatch record is made per request, signs that one transaction and is
    forgotten; the wallet's signature slot is left empty for the wallet. It can move nothing. A bridge realm's
    address as the recipient is refused (§5.2, K21: the realm would sweep it) — the configured realms and every
    route the mailbox registers or dropped, case-insensitively.
- rate-limited per client (two requests a second, a burst of thirty; the client is the first `X-Forwarded-For`
  address nginx sets), the key kept in memory and never written; nothing personal is logged — no address, no
  amount, no client (the test greps the log for all three).
- chain-agnostic: `chain.go`'s `Other` interface is what the site asks of the far side (a snapshot, a delivery, a
  burn by signature, the burn builder, the links); `solana.go` is the one implementation, over bridge/sol's
  parsers and bridge/agent's readers; a second chain implements the interface and is listed in the config.
- the config (`site.example.json`): the gno node and its realms, and per chain the provider, the domain, the
  explorer, the tokens and the four programs; the mint is derived from the program and checked when given; a
  chain of another kind is refused with "not implemented".

**The identity on the status page, honestly.** The monitors judge §5.9's identity over an ordered cut with the
exact amounts in flight, which take the dispatch index they keep (§5.9, AP1). The site keeps no index, so it shows
`lock = supply + owed + voided − lost` with what the monitors' tight rule gives without the in-flight burns: a right
side above the lock is BROKEN (never falsely: an uncounted burn in flight only makes the right side short), a short
right side is "holds; N in flight". The monitors' verdict is the one that flags; the site shows.

## How to run it

```
make -C bridge site-run                                        # the canned deployment on 127.0.0.1:8095
go run ./cmd/hlsite -config site/site.json -site site          # a deployment (from bridge/)
make -C bridge site-qa                                         # the gate: vet, the Go tests, the browser suite
```

The mock (`-mock`, `fixtures.go`) is a gno node and a Solana provider from canned data in the realms' and the
programs' exact shapes — the mailbox's and the routes' `json` as the realms build them and `bridge/local/e2e.sh`
reads them (M3-NOTES.md, M3l), the pausable ISM's flags and limits, the admin program's state and a pending kind 7,
the client's head, in the layouts bridge/sol parses (the encoders in `fixtures.go` are the parsers' inverse), a
known send out and a known burn in — so the readers run the code they run against a chain. With progress on
(the `-mock` server, the browser suite) the send delivers on the third look and the burn is paid on the fourth, so
a tracker shows every step.

**The tests.** Go (`go test ./cmd/hlsite/`): the config's checks and defaults; the Solana snapshot (supplies, limits
with the bucket's level, both objects' seats and flags, a hold's deferral, the client, the pending decision's
maturity); the burn builder (the decoded transaction: the wallet first and unsigned, the throwaway key second and
verified, the budget, `transfer_remote`'s bytes and accounts equal to `TransferRemoteIx`'s, the dispatch record;
the refusals); transfer resolution in both directions by every reference and the unknown ones; the status (the
identity holds, BROKEN on a supply above the lock, "in flight" on a short right side, a gno.land flag and a paused
function); the API (chains, status, transfer, the builder's refusals, 404s, 405, the headers, the limiter and its
refill, nothing personal in the log, the cache and the 503); the policy, and the pages loading nothing from outside
and carrying no inline script. Browser (`site/e2e/run.js`, Playwright 1.56 on the devbox): the real backend in
`-mock` mode serving the real pages, the wallets mocked in the page — both flows end to end with the wallets'
exact requests asserted (GNOT's `15000000ugnot` envelope and arguments, BUBBLE's `Approve` of amount + fee to the
route's address then `TransferRemote` with no coins, the gas; the Solana wallet handed the serialized bytes, the
Wallet Standard wallet the bytes and the chain), the refusals before any wallet is asked (the minimum, a bad
recipient, a realm address, a burn under the fee), the trackers to delivered and to paid, the track box by hash
and by junk, the status page (both identities, no flag, the governor, two pending decisions, the numbers), the help
page, the policy on the response, no page error, no horizontal overflow — at desktop (1280) and phone (390) width,
a screenshot of each page in `site/e2e/out/`.

**Verdicts, 2026-10-14, on the dev droplet (Go 1.27.1, node 22, Playwright 1.56.1):** `go vet` clean,
`gofmt` clean, `go test ./cmd/hlsite/` ok; the browser suite `PASS: 70 checks at desktop and phone width`.

## How to deploy it

`bridge/site/deploy.sh root@kourt.xyz`, from devbox, from a clean checkout of main — written and reviewed, NOT
run. Its gates: the tree committed, `make -C bridge site-qa` green, `bridge/site/site.json` present with no `FILL`
left (copied from `site.example.json`; never committed, since a provider URL may carry a key), and the shipped
binary accepting that config against the shipped pages before anything leaves the machine. It installs the binary
at `/usr/local/bin/hlsite`, the pages at `/usr/local/share/hlsite/site`, the config at `/etc/hlsite/site.json`
(0600, handed to the service as a systemd credential, readable by its dynamic user alone), the unit
`hlsite.service` (DynamicUser, ProtectSystem=strict, no key anywhere, 127.0.0.1:8195 on the box, whose 8095 is the meet gate's), and the nginx vhost
`bridge.gno.world` in sites-available — installed only when absent, since certbot rewrites it, and symlinked into
sites-enabled, never edited through the symlink — then `nginx -t`, the restart, a smoke read of `/api/chains`, the
reload. TLS: `certbot --nginx -d bridge.gno.world` runs only once the name resolves to the box and no certificate
stands; otherwise it prints what to run.

**Jae's hand, before the first run:** the DNS A record `bridge.gno.world` → the box, and `site.json` — the program
ids `hlsol setup` printed, the Solana provider URL, the explorer's cluster.

**The preview (2026-10-14).** Jae pointed `bridge.gno.world` at the box (138.68.5.120) before any program is
deployed, so the site first ships as a public preview: `PREVIEW=1 bridge/site/deploy.sh root@kourt.xyz` needs no
`site.json`, installs a drop-in (`/etc/systemd/system/hlsite.service.d/preview.conf`) that runs `hlsite -preview`
— the canned deployment with a banner on every page ("the bridge is not deployed yet; the figures are examples;
sending opens at launch"), the Status line marked PREVIEW, the wallet buttons disabled, `/api/mock` not served and
the burn builder answering 503 — and reads no chain. The first deploy without `PREVIEW` (with `site.json`) removes
the drop-in. `TestPreview` pins the four refusals and the pages' hooks.

## What is mocked until devnet

The live wiring is verified against the local e2e deployment's shapes only. Devnet must confirm:

1. **Adena under the policy.** `script-src 'self'` refuses a script the extension would inject by a
   `<script src="chrome-extension://…">` tag (Phantom and Backpack register in the main world and are unaffected).
   If Adena is not seen on the page, add its scheme to `script-src`, nothing else. Also `DoContract`'s answer shape
   (`data.hash`), two messages in one transaction for BUBBLE, and the `send` landing as the realm's `OriginSend`.
2. **The node's `tx` call** — the event JSON for `SentTransferRemote` (the parser is loose: any object with a
   `type` and a list of `{key, value}`), and the hash form the wallet returns (base64 is what is asked for; hex is
   converted).
3. **The Solana wallets** — the legacy provider taking an object whose `serialize()` returns the bytes; a
   partially signed legacy transaction (the throwaway key's signature in slot 1, the payer's slot empty) accepted
   and completed by Phantom and Backpack through both paths; the Wallet Standard's chain string for devnet
   (`solana:devnet`).
4. **The dispatch record** on a real mailbox — the 53-byte head before the message (`1`, `DISPATCH`, the nonce, the
   slot, a key), read with the agent's own reading; `getTransaction` at `maxSupportedTransactionVersion` 1.
5. **The delivery's signature** — `getSignaturesForAddress` on the processed-message record returning the
   delivering transaction (the record's creation).
6. **The reads shared with hlwatch** — `getTokenSupply` at finalized, the pausable ISM's, the admin program's and
   the client's layouts (bridge/sol's parsers, pinned on the programs' vectors).
7. **The gas** — 60M gas at the chain's price; a node's own minimum may ask more (the relayers' F14 rule); Adena's
   `gasFee` is in ugnot.
8. **The links** — gnoscan's transaction URL (`txLink`, "" until confirmed) and the explorer's `?cluster=devnet`.
9. **The limiter behind nginx** — `X-Forwarded-For` as the vhost sets it.
10. **The identity's reading** against the monitors' on a live bridge: a send in flight shows as a positive gap
    here and as exact in-flight there.
