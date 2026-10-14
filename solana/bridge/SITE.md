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

# M7 — the machine: the bridge shown as it runs (2026-10-14; branch bridge-m7 from bridge d8888a6f)

Jae, 2026-10-14, the day the bridge went live on mainnet: "begin working on making bridge.gno.world visually
illustrative and dynamic w/ the bridging mechanics so users learn how it all works under the hood (educational
feedback of what's going on)." So the site now draws the bridge as a machine fed by the real chains, plays every
real event on it as it happens, walks a tracked transfer through its steps saying why each is safe, and explains the
parts on a page of its own. Deployed to bridge.gno.world the same day (§Deployed).

## The pages

- **Home** (`index.html`, `home.js`): the machine (`machine.js`) is the centrepiece. gno.land's blocks stream
  along the top at ≈3.4 s, Solana's slots along the bottom at ≈0.4 s (each from the last reading, run forward at
  the chain's pace); a teal pin rides the block the light client trusts, or the lag is written at the stream's left
  edge, so the client is seen following the head and jumping forward when a header lands. Between the chains, the
  parts, each a drawn object with a short label: gno.land's validators (a seal each, sized by power — the client's
  own set), the fee realm, one vault per token with its lock, the payout meter (a gauge refilling at the day cap a
  day, a mark at a recipient's quarter), the claims, the governor (Jae's key until the yield, GovDAO after) and the
  guardians with their flags (four seats, two watch keys; a flag raised on any object shows); hlmailbox and
  Solana's mailbox as two bars (down: sends; up: checks 2 of 3 / burns), the updater, the relayer (two doors:
  proves sends, vouched burns) and the three signers, each a lamp — vouching (serves the newest burn's signature),
  up, down — with its word under it; Solana's light client (its trusted height, the validators' power as one bar
  with the 2/3 mark), the pausable gate across the two mints (violet when a flag or the quarantine stands), the
  mints' supplies, the gate's day bucket, the admin program and what it waits on. Under the Solana stream the
  identity as two balances, lock = supply + owed + voided − lost with the numbers: level while it holds, tipped
  toward the lock while a transfer is in flight, red the other way if BROKEN.
  The paths are straight lanes a packet travels: a send goes down its token's lane (block → lock → hlmailbox →
  relayer → mailbox → gate → mint), a burn comes up the lane on the right (mint → outbox → the signers vouch →
  relayer → hlmailbox → claim → meter → out of the lock), a header goes down the left (block → validators → updater
  → light client), a decision from the governor through the relayer to the admin program. Every new event in the
  activity plays as it arrives (the newest also on load when it is under 90 s old); a caption strip says in one
  sentence what happened and why it is safe, and between events what stands ("gno.land is at block 793,795;
  Solana's light client trusts block 793,647, 148 behind — the updater brings a header every 10 minutes, or as
  soon as a transfer waits"). The recent events are listed (the newest header and the newest transfer steps); a
  click plays one. After a minute with nothing new, **replay the last real transfer** plays the newest completed
  transfer each way with its real steps and times, faster, under a REPLAY banner, then gives the picture back to
  the live view (events that came meanwhile play after it). The page reads the activity every ten seconds and at
  once when its tab comes back. The plain story follows the machine, unchanged.
- **Bridge** (`bridge.js`, `walk.js`): tracking a transfer (by message id, gno.land transaction hash or Solana
  signature) walks it through five steps, each with its real numbers and time, why it is safe, and a small picture:
  gno.land → Solana: locked in the realm → the block is final (> 2/3 of the power signed it; gno.land does not
  reorganize) → its header reaches Solana (the updater; anyone may carry headers, a forged one needs 2/3 of the
  keys) → the relayer proves the transfer (the results proof drawn as a tree: the leaf, the path, the siblings; a
  block of one result is its own root) → minted, through the gate. Solana → gno.land: burned (final) → 2 of 3
  signers vouch (the lamps light one by one) → the relayer delivers it (hlmailbox checks 2 of 3, marks the id done
  once) → a claim is credited (an IOU, counted as owed) → paid under the meter (the gauge and the time the meters
  still need, an estimate: the chain renders no recipient's meter). A compact machine sits under the tracker, runs
  live between transfers, plays each step as it becomes done, and plays a step again when it is chosen.
- **How it works** (`how.html`, `how.js`, `/how`, in every page's nav): eleven small scenes drawn with the same
  pieces (`draw.js`) — two chains, the lock, the validators' signatures, the header crossing, the proof as a tree,
  the gate and the mint, the burn, the signers vouching, the claim under the meter, the balance, who can stop it —
  one or two sentences each, each playing once in view and again on a tap.
- **Reduced motion**: nothing moves. The streams stand at the reading, an event lights its path instead of
  carrying a packet, the scenes light theirs; the figures, the lamps, the balances and the captions are the same.
- **Colour is the part's role** (`site.css`, both themes): teal a cryptographic check (validators, the light client,
  the proof, the verifier), orange the trusted signers, violet a brake or a limit (guardians, the gate closed, the
  meters, the governor's plate), grey a carrier (updater, relayer, mailboxes); the chains and the coins neutral; the
  lamps the reserved status colours with their words. The three role hues pass the colour-blind and contrast checks
  across every pair in both themes; text keeps the text colours. No hexagram anywhere: seals carry a four-point
  star.

## The backend: `GET /api/<chain>/activity`

`activity.go`, `solana_activity.go`, `signers.go`. One index per chain, of what the chains' own records say:

- **gno.land**: hlmailbox's newest messages (`msgs/<from>`, read only when its count moved), each a route's send
  (token, amount and recipient from the message; sender and time from the route's `send/<id>`) or r/hlgov's decision
  (its kind and number, its block's time); the burns' claims (`claim/<id>`: credited when and in which block, paid
  how far; a payout's time is this reader's observation — the chain keeps none — and marked not exact otherwise).
- **Solana**: the mailbox's outbox and inbox counters and the client's 64-byte head at once; a listing only when its
  counter moved (or a read left work): the outbox's newest transactions → the burns (each read as the tracker reads
  one: the dispatch record of that slot); the inbox's → the deliveries, read from each process instruction's own
  message bytes (a route's message a mint, the admin program's a decision); the client's → the headers accepted
  (an update's StageInit names the header, its UpdateFinish accepts it) and the proofs staged (the message's
  sha256, the header — or the client's ring entry, resolved by reading the client's whole account only then — and
  the tree's shape: leaves, index, siblings; the first staging kept). Each transaction is read once, four at a
  time; the first read takes the whole listing (20 for the mailbox, 60 for the client: about two hours of headers),
  every later read at most 15 new ones; every account batch at most ten (sol.MultiMax; the mock refuses more).
- **The signers' lamps**: the config's new `signers` (each signer's public signature endpoint, what the relayers
  read; optional — without it the lamps say nothing) asked for the newest burn's nonce: a signature is vouching, any
  answer up, none down; fifteen seconds' cache.
- **The answer**: the status (as /status serves it; while a read fails — rpc.gno.land's per-IP rate limit was met
  at 05:55 by the box's agents and hlsite together — its last good reading, `stale`, with the failure and the
  reading's time named), the lamps and the threshold, the counters, the events newest first with stable keys (the
  page plays each once), and `last`: the newest completed transfer each way with its walk, for the replay.
- **Never in the way**: the index answers at once from its last reading and refreshes in the background when older
  than ten seconds (one refresh at a time, on a copy that replaces the reading whole; a read's budget 15 s, a
  refresh's 90 s); the status and the lamps likewise; a fresh server warms all of them at start; before the very
  first reading a request waits at most four seconds and then says the reading is under way. A public provider's
  slow answer (15 s was measured for one getMultipleAccounts) delays the picture, never the page. The route is
  under the same per-client limiter and policy as the rest of /api.
- **The transfer's walk**: `/api/<chain>/transfer/<ref>` now also carries `walk` (the five steps' done and time),
  `height`, `nonce`, `trusted`, `proof`, `vouched`, `signers`, `threshold` and `meter` (the payout meter's level,
  cap, a recipient's quarter and the remainder, while a claim waits); a send older than the index's listing is
  found in hlmailbox's pages before it (five at most, each read once).

The mock (`-mock`) is now a staged world: a history (decision #3 enacted; 25 GNOT out with its header, proof and
mint; 500 BUBBLE back, vouched, credited, paid) and two transfers in flight that move only when stepped (`POST
/api/mock/step?flow=out|in|down|reset`, -mock alone): out — locked, the header of block 1291 accepted, the proof
staged against it from the client's ring, minted; in — burned, signer 1 vouches, signer 2, credited, half paid,
paid. The figures move with the stages so the identity holds at each (a send in flight is a gap until its mint, a
burn until its credit). The transactions are built with bridge/sol's own instruction builders, so the readers parse
what the programs would. Its limiter is a hundred a second (the suite is one client making a crowd's requests); a
deployment keeps two.

## The value locked over time: `GET /api/<chain>/history` (2026-10-15)

Jae: "is there a graph of how much value is locked through the bridge? on bridge.gno.world" — "do it, i want to see
the graph". Home (below the recent events) and Status (below the identity) draw each token's lock on gno.land from
the launch to now: a step line in SVG (`chart.js`, no library), its figure above it, a pointer or a touch reading the
level at a time, an open circle where a time is estimated. A token never sent gets a line saying so.

- **Recorded, not read back.** A node answers a realm's render only as of its newest block: `vm/qrender` ignores the
  height asked (gnoland-1 through rpc.gno.land and Onbloc, 2026-10-15), so the past cannot be queried. hlsite's
  sampler takes the status every minute and keeps a point when a lock moved, at the status's time; a change first
  seen more than five minutes after the previous reading (hlsite was down) is marked estimated.
- **Persisted.** `-state DIR` keeps `history-<chain>.json` there (written whole, renamed over the old); the unit's
  `StateDirectory=hlsite` gives it `/var/lib/hlsite`. Without `-state` (the mock, the tests) it lives in memory.
- **Rebuilt once.** The first reading rebuilds what came before, anchored at its locks and walked back through the
  activity's index: each send at its record's time (exact), each claim's paid part when this reader saw it paid,
  else at its credit time (estimated: the chain keeps no payout time), a sweep at its credit (estimated). The
  rebuild waits, up to ten readings, for an index that holds every dispatch hlmailbox made and every claim each
  route counts; the index keeps the newest transfers of each kind, so an older deployment's history starts at its
  first reading with the launch's level estimated. Under the mock (whose canned routes count claims its records do
  not hold) the history starts at once without the rebuild.
- **Served** as `{chain, from, rebuilt, sampled, now, tokens: [{symbol, lock, points: [[time, lock] or [time, lock,
  1] for an estimate]}]}`, a token's points only where its lock changed.
- **Tests**: `TestRebuildHistory` (mainnet's first day in small numbers: the launch at nothing, the sends, a payout
  seen and one estimated), `TestRebuildHistoryUnexplained`, `TestActivityComplete`, `TestSampleHistory` (a change,
  none, a gap, the file, a restart), `TestHistoryAPI`; three mutations (walking forward, no gap estimate, a point
  every reading) each fail them. The browser suite's `lockedCase`: both charts on Home and Status at both widths,
  each line ending at the machine's lock, a pointer reading a level, the burn in flight paid out dropping BUBBLE's
  line one step, every label inside its chart — 183 checks.

## Tests and verdicts (2026-10-14, the devbox: Go 1.27.1, node 22, Playwright 1.56.1)

- **Go** (`go test ./cmd/hlsite/`, also under `-race`): TestActivity (the history's events with their exact times,
  a mint's proof, a payout not exactly timed, the send through its header, its ring proof and its mint, the burn
  through its lamps, credit, half and full payout, a signer down, the replay's choice of completed transfers, the
  reset), TestActivityReadsOnlyWhatMoved (each transaction read once; a read where nothing moved costs the two
  counters, the client's head and the mailbox's render; nothing within the TTL), TestActivityAnswersAtOnce (five
  reads of a stale reading answer at once while one background refresh runs behind a 300 ms provider),
  TestActivityKeepsTheLastStatus (stale with the failure named; /status still 503), TestLamps, TestProcessMessage,
  TestWalks, TestFindDispatch; TestResolveTransfers walks both directions through every stage; the policy test reads
  every new file. `go vet` and `gofmt` clean.
- **Browser** (`site/e2e/run.js`, `make -C bridge site-qa`): **PASS, 151 checks at desktop and phone width** — the
  machine drawn from the mock's figures (the head, the trusted height, the locks, the supplies, three lamps up, six
  validators, the GNOT balance tipped by the 10 GNOT in flight), the header event playing as it arrives (its packet,
  its caption, the pin moving to 1,291) and an event row playing on a click, the walkthrough through every step both
  ways with the machine playing each (the proof's tree, leaf 2 of 3; the lamps lighting 1 then 2; the meter's time
  left; paid in installments, then in full), the replay of the last completed round trip in order with its real
  numbers (the page's clock ten minutes on), How it works (eleven scenes, played in view and on a tap), reduced
  motion (the same figures, the streams still, a lit path and no packet), the dark theme, no overflow and no text
  spilling its box (HTML), every label inside its part and its drawing (SVG, on screen), no page error — with every
  M5 check kept (the wallets' exact requests, the refusals, the status and help pages, the policy).

## Deployed (2026-10-14)

From a fresh checkout of a7811edd on the devbox (/root/src/bridge-m7-deploy: a clone of the bridge-site clone with
this branch fetched from a bundle; the live site.json beside it, untracked, with `signers` added: the three
`https://bridge.gno.world/signer/N` endpoints), `sh bridge/site/deploy.sh root@kourt.xyz`: the gates passed (vet,
go test, the 151 browser checks), the binary, the pages and the config installed, `nginx -t`, hlsite up, TLS in
place; again from d2a45391 (the replay's captions, from watching the first live replay) and from this commit (the
REPLAY banner off the guardians' flags), each time through the same gates. The first gate met a 429 from hlsite's
own limiter in the suite and stopped the deploy, as it should (a7811edd fixed the mock's limiter).

**The live check** (Playwright on the devbox against https://bridge.gno.world, 1280 and 390): the machine's
figures equal /api/solana/status (GNOT lock 130 = supply 130 — 10, 30 and 100 sent, 10 burned back — BUBBLE 0 = 0,
the client's trusted height, the identities, six validators), the head within the few blocks a reading is old, all
three lamps vouching (burn #0), no horizontal overflow on any page, no console error at either width; the two real
transfers walk through all five steps (100 GNOT: locked 06:41:52 in block 792,971, the header of 792,976 at
06:42:27, the proof under 792,972 — its block's only result — at 06:42:38, minted 06:42:50; 10 GNOT back: burn #0
at 05:42:03, credited in block 792,034 at 05:45:13, paid); the replay plays send, header, mint, burn, credit,
payout with those times, then a header that arrived meanwhile. The activity answers in 60–260 ms.

**For whoever deploys next**: the devbox's /root/src/bridge-site/bridge/site/site.json has no `signers`; deployed
from it, the lamps say nothing (everything else works). Add the line the deployed config carries:
`"signers": ["https://bridge.gno.world/signer/1", "https://bridge.gno.world/signer/2", "https://bridge.gno.world/signer/3"]`.

## Next

- A gno.land block time for the stream from the node's own recent blocks rather than the 3.43 s constant.
- The payout's real time: the route could render when a claim was paid (a realm change, for the realms' lane); the
  site then stops estimating it.
- The recipient's meter level rendered by the route, so the pay step's time left is exact, not an estimate.
- The quarantine and a hold drawn as events of their own (a flag raised, a hold's 21-day clock) when they happen.
- Per-validator signatures on the header packet (the commit's signers, from the update's precommits), so the
  picture shows which validators signed each header.
