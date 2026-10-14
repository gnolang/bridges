#!/bin/sh
# The bridge's launch (bridge/PLAN.md §5.3, §5.8, §6.6, §11.16): the Solana side deployed, set up and audited before
# anything goes up on gno.land (AN10), then the nine packages under the governor's own namespace, then the
# governor's Launch, then the machines' configs. In order and resumable — every step reads the chain first and
# skips what is done:
#   (a) preflight, read-only: the tools and the values (stage.sh stages and checks them); the machines' Solana
#       provider layout (§5.6, `hlbridge providers`); the governor's key against GOV_ADDR and its GNOT against the
#       uploads' need (each upload simulated with gnokey's `-simulate only` where its imports are on the chain
#       already, else the rehearsal's measured figures + 50%); the deployer's SOL against the deploy's need (each
#       program's rent at SOL_RPC's own rate, the rehearsal's measured state rent + 50%, the floats); the gno node's
#       head fresh (the client's trusted header is taken from it); the BUBBLE token on the chain; every bridge
#       package path absent and the six program ids unused (PART=gno: deployed and audited by the Solana half) — on
#       `resume`, present packages must be byte-identical to the stage;
#   (b) `hlsol setup`: the six deployments and the setup step at FACTOR, the realms' paths under GOV_ADDR compiled
#       into the programs, the admin program's sequence from 0 — skipped once the Solana side passes its audit;
#   (c) `hlsol audit` must PASS (the deployed ELFs bind GOV_ADDR's paths, hlkey pkg's addresses), or nothing more;
#   (d) stage.sh with the program ids: the nine packages and the manifest of their files;
#   (e) addpkg in dependency order from the governor's key, each simulated first (the gas, the storage deposit),
#       each confirmed by vm/qfile file by file against the stage, the GNOT each cost recorded;
#   (f) the governor's Launch — hlmailbox.Govern(cross(cur), hlmailbox.NewLaunchRequest(cross(cur))) by MsgRun
#       from his key (M1k) — skipped once the mailbox renders live (LAUNCH=no stops before it, so the deployment's
#       checklist can be published first, §6.6);
#   (g) the machines' configs: a bridge.json per role (three signers, the updater, the primary and the backup
#       relayer, two monitors) and the site's site.json (bridge/SITE.md);
#   (h) the summary: the program ids, the realm paths, the governor, what it cost, the next steps.
#
#   bridge/launch/launch.sh LAUNCH_ENV [resume]
#
# LAUNCH_ENV is bridge/launch/launch.env.example filled in. Everything lands under $OUT: the tools (bin/), the
# programs' staged sources and builds (programs/, so/), the program ids and the deployer's pubkey (ids.env), the
# staged packages and their manifest (stage/), the logs (logs/), the costs (costs/), the configs (configs/) — and no
# key. Needs go, gnokey, python3, rsync, the Solana CLI 3.0.14 with cargo-build-sbf in $SOLBIN (cargo in $PATH or
# ~/.cargo/bin). bridge/launch/rehearse.sh runs it end to end on a local validator and gnodev.
#
# Keys never move (bridge/RESUME.md §7), so the two keys may sit on two machines: PART=solana runs (a)–(c) where the
# deployer's keypair and the six program keypairs are, and needs no governor; PART=gno runs (a) and (c)–(h) where the
# governor's keybase is, after $OUT was copied there, and needs neither keypair (the ids and the deployer's pubkey
# come from $OUT/ids.env, the programs' files for the audit from $OUT/so); PART=all (the default) runs everything on
# one machine.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
T0=$(date +%s)
say() { printf '\n== %s (%ss)\n' "$*" "$(($(date +%s) - T0))"; }
die() { printf 'launch: FAIL: %s\n' "$*" >&2; exit 1; }
[ $# -ge 1 ] && [ $# -le 2 ] || { echo "usage: launch.sh LAUNCH_ENV [resume]" >&2; exit 2; }
ENVFILE=$1
case ${2:-} in "") RESUME="" ;; resume) RESUME=1 ;; *) echo "usage: launch.sh LAUNCH_ENV [resume]" >&2; exit 2 ;; esac
case $ENVFILE in /*) ;; *) ENVFILE=$PWD/$ENVFILE ;; esac
[ -f "$ENVFILE" ] || die "no launch env at $ENVFILE"
set -a
# shellcheck disable=SC1090
. "$ENVFILE"
set +a
PART=${PART:-all}
case $PART in
all) KEYVARS="DEPLOYER PROGRAM_KEYS SOLBIN HYPERLANE_SO GOV_HOME GOV_KEY" ;;
solana) KEYVARS="DEPLOYER PROGRAM_KEYS SOLBIN HYPERLANE_SO" ;; # no governor here: (a)–(c)
gno) KEYVARS="GOV_HOME GOV_KEY" ;;                             # no keypair here: (a), (c)–(h)
*) die "PART is all, solana or gno, not $PART" ;;
esac
for v in OUT GNO_RPC GNO_CHAIN_ID GNO_DOMAIN SOL_RPC SOL_DOMAIN GOV_ADDR BUBBLE_TOKEN \
  GUARDIAN1_GNO GUARDIAN2_GNO GUARDIAN3_GNO GUARDIAN4_GNO WATCH5_GNO WATCH6_GNO \
  GUARDIAN1_SOL GUARDIAN2_SOL GUARDIAN3_SOL GUARDIAN4_SOL WATCH5_SOL WATCH6_SOL \
  SIGNER1_PUB SIGNER2_PUB SIGNER3_PUB SIGNER1_URL SIGNER2_URL SIGNER3_URL SIGNER_THRESHOLD TREASURER_GNO TREASURER_SOL \
  EXPECTED_LOCK_GNOT EXPECTED_LOCK_BUBBLE FACTOR PROVIDERS NODE_RPC GNO_SOURCES \
  MACHINE_GNOKEY_HOME RELAYER_GNO_KEY RELAYER_GNO_ADDR RELAYER_SOL_KEY UPDATER_SOL_PUB UPDATER_SOL_KEY \
  WATCH5_GNO_KEY WATCH6_GNO_KEY WATCH5_SOL_KEY WATCH6_SOL_KEY SITE_SOL_RPC $KEYVARS; do
  eval "x=\${$v:-}"
  [ -n "$x" ] || die "$v is empty in $ENVFILE"
  case $x in FILL*) die "$v is not filled in $ENVFILE" ;; esac
done
ATA_FUND=${ATA_FUND:-250000000}
AUTHORITY_FUND=${AUTHORITY_FUND:-10000000}
GNO_GAS_PRICE_FLOOR=${GNO_GAS_PRICE_FLOOR:-0.001}
GNOT_NAME=${GNOT_NAME:-Gno}; GNOT_SYMBOL=${GNOT_SYMBOL:-GNOT}; GNOT_URI=${GNOT_URI:-}
BUBBLE_NAME=${BUBBLE_NAME:-Bubble}; BUBBLE_SYMBOL=${BUBBLE_SYMBOL:-BUBBLE}; BUBBLE_URI=${BUBBLE_URI:-}
LAUNCH=${LAUNCH:-yes}
[ "$FACTOR" = 1 ] || die "FACTOR $FACTOR: the launch builds the programs at factor 1, as its realms are (§5.9; p/decisions compiles 1)"
case $LAUNCH in yes | no) ;; *) die "LAUNCH is yes or no, not $LAUNCH" ;; esac
export PATH="${SOLBIN:+$SOLBIN:}$HOME/.cargo/bin:$PATH"
export CARGO_TARGET_DIR=${CARGO_TARGET_DIR:-$OUT/target}
NSP=gno.land/p/$GOV_ADDR
NSR=gno.land/r/$GOV_ADDR
GOVERNOR=${GOVERNOR:-$GOV_ADDR}   # the realms' governor: the namespace key, or another (stage.sh compiles it)
INERT_MAX_DEPOSIT=${INERT_MAX_DEPOSIT:-100000000}   # a parked package's storage cap, ugnot: its deposit is charged at the enable
BIN=$OUT/bin
SO=$OUT/so
PROGS=$OUT/programs
STAGE=$OUT/stage
mkdir -p "$OUT/logs" "$OUT/run" "$OUT/costs" "$OUT/configs" "$BIN" "$SO"
chmod 700 "$OUT"

# What bridge/launch/rehearse.sh measured (2026-10-14, gnodev at gnoland-1's prices — 100 ugnot a byte stored, 1
# ugnot per 1,000 gas), each 10% over and rounded up: what each upload stored (bytes) and burned (gas), the Launch
# the same; the six programs' ELF sizes (ours as built at factor 1; the preflight reads the files when they are
# there); and the setup's lamports beyond the programs' rent and the floats — the state accounts, the metadata,
# the fees — at the local validator's rent of 6,960 lamports a byte, above mainnet's 5,080, so a ceiling there.
# The preflight takes 150% of these where it cannot simulate or read the chain.
MEASURED_GNO="p/hyperlane/v0:36000:30000000 p/decisions/v0:11000:30000000 p/hlroute/v0:26000:160000000 r/hlmailbox:167000:170000000 r/hlverifier:10000:40000000 r/hlfees:20000:40000000 r/hlgnot:64000:80000000 r/hlbubble:65000:80000000 r/hlgov:69000:110000000 launch:4000:30000000"
MEASURED_ELF="mailbox:211024 gnot:357744 bubble:357744 client:270152 pausable:164448 admin:317624"
MEASURED_STATE_LAMPORTS=217000000
PACKAGES="p/hyperlane/v0 p/decisions/v0 p/hlroute/v0 r/hlmailbox r/hlverifier r/hlfees r/hlgnot r/hlbubble r/hlgov"

cat >"$OUT/run/launch.py" <<'EOF'
import base64, calendar, hashlib, json, os, re, sys, time, urllib.parse, urllib.request
from fractions import Fraction

B58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

def die(msg):
    print("launch: FAIL: " + msg, file=sys.stderr)
    sys.exit(1)

def get(rpc, path):
    j = json.load(urllib.request.urlopen(rpc.rstrip("/") + path, timeout=60))
    if j.get("error"):
        raise RuntimeError(json.dumps(j["error"])[:300])
    return j["result"]

# GET, never POST: a node answers the same, and gnodev's lazy loader (which reads POSTed bodies) is left alone
def abci(rpc, path, data=""):
    r = get(rpc, "/abci_query?path=" + urllib.parse.quote('"%s"' % path) + "&data=0x" + data.encode().hex())["response"]["ResponseBase"]
    if r.get("Error"):
        return None, (r.get("Log") or "") + json.dumps(r.get("Error"))
    return base64.b64decode(r.get("Data") or ""), ""

def absent(log):
    return "is not available" in log or "InvalidPackage" in log or "package not found" in log

def unquote(b):
    s = b.decode().strip()
    return json.loads(s) if s.startswith('"') else s

def cmd_status(rpc):
    r = get(rpc, "/status")
    si = r["sync_info"]
    t = si["latest_block_time"]
    m = re.match(r"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(\.\d+)?Z$", t)
    secs = calendar.timegm(time.strptime(m.group(1), "%Y-%m-%dT%H:%M:%S")) if m else 0
    print(r["node_info"]["network"], si["latest_block_height"], secs, "true" if si.get("catching_up") else "false")

def cmd_exists(rpc, pkg):
    b, log = abci(rpc, "vm/qfile", pkg)
    if b is not None:
        sys.exit(0)
    sys.exit(3 if absent(log) else 1)

def cmd_same(rpc, pkg, d, creator):
    b, log = abci(rpc, "vm/qfile", pkg)
    if b is None:
        print("%s is not on the chain" % pkg)
        sys.exit(3 if absent(log) else 1)
    served = sorted(b.decode().split())
    staged = sorted(f for f in os.listdir(d) if f.endswith(".gno") or f == "gnomod.toml")
    bad = []
    if served != staged:
        bad.append("files served %s, staged %s" % (served, staged))
    for f in staged:
        if f not in served:
            continue
        got, log = abci(rpc, "vm/qfile", pkg + "/" + f)
        want = open(os.path.join(d, f), "rb").read()
        if got is None:
            bad.append("%s: %s" % (f, log[:120]))
        elif f == "gnomod.toml":
            # the keeper appends its [addpkg] lines (creator, height, max_deposit): the staged text is their head
            if not got.startswith(want.rstrip(b"\n")):
                bad.append("gnomod.toml served %r, staged %r" % (got[:200], want))
            elif creator and ('creator = "%s"' % creator).encode() not in got:
                bad.append("gnomod.toml: not uploaded by %s: %r" % (creator, got[-200:]))
        elif hashlib.sha256(got).hexdigest() != hashlib.sha256(want).hexdigest():
            bad.append("%s: served sha256 %s, staged %s" % (f, hashlib.sha256(got).hexdigest(), hashlib.sha256(want).hexdigest()))
    if bad:
        print("%s differs from the stage: %s" % (pkg, "; ".join(bad)))
        sys.exit(4)
    print("%s: %d files served, byte for byte the stage's (the gnomod.toml's head; uploaded by %s)" % (pkg, len(served), creator))

def cmd_render(rpc, arg):
    b, log = abci(rpc, "vm/qrender", arg)
    if b is None:
        die("vm/qrender %s: %s" % (arg, log[:300]))
    sys.stdout.write(b.decode())

def cmd_qeval(rpc, expr):
    b, log = abci(rpc, "vm/qeval", expr)
    if b is None:
        die("vm/qeval %s: %s" % (expr, log[:300]))
    print(b.decode().strip())

def cmd_balance(rpc, addr):
    b, log = abci(rpc, "bank/balances/" + addr)
    if b is None:
        die("bank/balances/%s: %s" % (addr, log[:300]))
    m = re.search(r"(\d+)ugnot", unquote(b) or "")
    print(m.group(1) if m else 0)

def cmd_param(rpc, key):
    b, log = abci(rpc, "params/" + key)
    print("" if b is None else (unquote(b) or ""))

def cmd_meta(rpc, pkg):
    # a path's status under the inert code policy (vm/qpkgmeta_json): "inert CREATOR MAXDEP_UGNOT" for a parked
    # package, "live" for one enabled, nothing (exit 3) for none
    b, log = abci(rpc, "vm/qpkgmeta_json", pkg)
    if b is None:
        sys.exit(3)
    j = json.loads(b.decode() or "{}")
    if j.get("status") == "inert":
        m = re.search(r"(\d+)ugnot", j.get("max_deposit") or "")
        print("inert", j.get("creator", ""), m.group(1) if m else 0)
    elif j.get("status"):
        print(j["status"])
    else:
        sys.exit(3)

def cmd_price(rpc, floor):
    # the price a call pays per gas: the chain's (auth/gasprice), never under the floor (§5.7: 0.001 ugnot a gas)
    p = Fraction(floor)
    b, log = abci(rpc, "auth/gasprice")
    if b:
        j = json.loads(b.decode())
        gas, price = int(j.get("gas") or 0), j.get("price") or ""
        m = re.match(r"(\d+)ugnot$", price)
        if gas and m and Fraction(int(m.group(1)), gas) > p:
            p = Fraction(int(m.group(1)), gas)
    print(p.numerator, p.denominator)

def cmd_fee(gas, num, den):
    print(-(-int(gas) * int(num) // int(den)))

def solrpc(rpc, method, params):
    req = urllib.request.Request(rpc, json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode(), {"Content-Type": "application/json"})
    j = json.load(urllib.request.urlopen(req, timeout=60))
    if j.get("error"):
        raise RuntimeError("%s: %s" % (method, json.dumps(j["error"])[:300]))
    return j["result"]

def b58(b):
    n = int.from_bytes(b, "big")
    s = ""
    while n:
        n, r = divmod(n, 58)
        s = B58[r] + s
    return "1" * (len(b) - len(b.lstrip(b"\0"))) + s

def account(rpc, key):
    return solrpc(rpc, "getAccountInfo", [key, {"encoding": "base64", "commitment": "confirmed"}])["value"]

def cmd_solexists(rpc, key):
    sys.exit(0 if account(rpc, key) is not None else 3)

def cmd_solbal(rpc, key):
    print(solrpc(rpc, "getBalance", [key, {"commitment": "confirmed"}])["value"])

def cmd_solrent(rpc, size):
    print(solrpc(rpc, "getMinimumBalanceForRentExemption", [int(size)]))

def cmd_progrent(rpc, key):
    # an upgradeable program: its account (36 bytes: the state tag, then the programdata address) and its programdata
    a = account(rpc, key)
    if a is None:
        die("no program at %s" % key)
    pd = b58(base64.b64decode(a["data"][0])[4:36])
    b = account(rpc, pd)
    print(a["lamports"] + (b["lamports"] if b else 0))

def cmd_tx(path):
    out = open(path).read()
    def one(pat, default=None):
        m = re.search(pat, out, re.M)
        return m.group(1) if m else default
    ok = "1" if re.search(r"^OK!$", out, re.M) else "0"
    used = one(r"^GAS USED:\s+(\d+)", "0")
    wanted = one(r"^GAS WANTED:\s*(\d+)", "0")
    nbytes = one(r"^STORAGE DELTA:\s+(-?\d+) bytes", "0")
    fee = one(r"^STORAGE FEE:\s+(\d+)ugnot", "0")
    height = one(r"^HEIGHT:\s+(\d+)", "0")
    txhash = one(r"^TX HASH:\s+(\S+)", "-")
    print(ok, used, wanted, nbytes, fee, height, txhash)

def cmd_imports(d):
    out = set()
    for f in sorted(os.listdir(d)):
        if not f.endswith(".gno"):
            continue
        src = open(os.path.join(d, f)).read()
        out.update(re.findall(r'(?m)^import\s+(?:\w+\s+)?"([^"]+)"', src))
        for blk in re.findall(r"(?ms)^import\s*\((.*?)^\)", src):
            out.update(re.findall(r'(?m)^\s*(?:\w+\s+)?"([^"]+)"', blk))
    print("\n".join(sorted(p for p in out if "." in p.split("/", 1)[0])))

def providers():
    roles = {}
    for entry in os.environ["PROVIDERS"].split():
        role, _, urls = entry.partition("=")
        us = [u for u in urls.split(",") if u]
        if role not in ("signer1", "signer2", "signer3", "monitor1", "monitor2"):
            die("PROVIDERS: %r is not signer1, signer2, signer3, monitor1 or monitor2" % role)
        if role in roles:
            die("PROVIDERS: %s twice" % role)
        if len(us) != 2 or any(not re.match(r"https?://[^\s,]+$", u) for u in us):
            die("PROVIDERS: %s takes two provider URLs, http(s)://…, comma separated" % role)
        if us[0] == us[1]:
            die("PROVIDERS: %s names the same provider twice: two providers that must agree cannot be one (§5.6)" % role)
        roles[role] = us
    for r in ("signer1", "signer2", "signer3", "monitor1", "monitor2"):
        if r not in roles:
            die("PROVIDERS: no entry for %s" % r)
    # the monitors' rule (§5.6): each its own two providers, no pair a signer's
    spairs = {tuple(sorted(roles["signer%d" % i])) for i in (1, 2, 3)}
    for m in ("monitor1", "monitor2"):
        if tuple(sorted(roles[m])) in spairs:
            die("PROVIDERS: %s's pair is a signer's: a monitor reads Solana through two providers of its own, no pair shared with a signer (§5.6)" % m)
    return roles

def redact(u):
    m = re.match(r"(https?://[^/?#]+)", u)
    return (m.group(1) if m else u) + ("/…" if len(u) > len(m.group(1) if m else u) else "")

def cmd_layout():
    roles = providers()
    for r in sorted(roles):
        print("launch: %s reads Solana through %s and %s" % (r, redact(roles[r][0]), redact(roles[r][1])))

def cmd_configs(outdir):
    E = os.environ
    roles = providers()
    nsr = "gno.land/r/%s/" % E["GOV_ADDR"]
    signers = [{"url": E["SIGNER%d_URL" % i], "key": E["SIGNER%d_PUB" % i].lower().replace("0x", "")} for i in (1, 2, 3)]
    def base(rpcs):
        return {
            "gno": {"rpc": E["NODE_RPC"], "sources": E["GNO_SOURCES"].split(), "chainId": E["GNO_CHAIN_ID"],
                    "mailbox": nsr + "hlmailbox", "fees": nsr + "hlfees", "gov": nsr + "hlgov", "dao": "gno.land/r/gov/dao",
                    "gnokey": E.get("MACHINE_GNOKEY") or "gnokey"},
            "solana": {"rpcs": list(rpcs), "mailbox": E["ID_MAILBOX"], "client": E["ID_CLIENT"], "pausable": E["ID_PAUSABLE"],
                       "admin": E["ID_ADMIN"], "domain": int(E["SOL_DOMAIN"])},
            "routes": [{"symbol": "GNOT", "gno": nsr + "hlgnot", "token": E["ID_GNOT"], "expectedLock": int(E["EXPECTED_LOCK_GNOT"])},
                       {"symbol": "BUBBLE", "gno": nsr + "hlbubble", "token": E["ID_BUBBLE"], "expectedLock": int(E["EXPECTED_LOCK_BUBBLE"])}],
            "signers": signers,
            "webhook": E.get("ALARM_WEBHOOK", ""),
            "allowHTTP": False,
        }
    out = {}
    for i in (1, 2, 3):
        out["signer-%d.json" % i] = base(roles["signer%d" % i])
    c = base(roles["monitor1"])  # the updater runs on monitor 1's machine (§5.5)
    c["solana"].update({"updater": E["UPDATER_SOL_PUB"], "updaterKey": E["UPDATER_SOL_KEY"]})
    out["updater.json"] = c
    for role, m in (("primary", "monitor1"), ("backup", "monitor2")):  # primary on monitor 1, backup on monitor 2
        c = base(roles[m])
        c["gno"].update({"home": E["MACHINE_GNOKEY_HOME"], "key": E["RELAYER_GNO_KEY"], "address": E["RELAYER_GNO_ADDR"]})
        c["solana"]["payer"] = E["RELAYER_SOL_KEY"]
        out["relayer-%s.json" % role] = c
    for n, seat in ((1, 5), (2, 6)):
        c = base(roles["monitor%d" % n])
        c["gno"].update({"home": E["MACHINE_GNOKEY_HOME"], "address": E["RELAYER_GNO_ADDR"], "watchKey": E["WATCH%d_GNO_KEY" % seat], "watchAddress": E["WATCH%d_GNO" % seat]})
        c["solana"].update({"payer": E["RELAYER_SOL_KEY"], "updater": E["UPDATER_SOL_PUB"], "watchKey": E["WATCH%d_SOL_KEY" % seat]})
        out["monitor-%d.json" % n] = c
    out["site.json"] = {
        "gno": {"rpc": E.get("SITE_GNO_RPC") or E["GNO_RPC"], "chainId": E["GNO_CHAIN_ID"], "mailbox": nsr + "hlmailbox", "fees": nsr + "hlfees",
                "gov": nsr + "hlgov", "bubble": E["BUBBLE_TOKEN"], "realmLink": "https://gno.land/%s", "txLink": "", "gasWanted": 60000000},
        "chains": [{"id": "solana", "name": "Solana", "rpc": E["SITE_SOL_RPC"], "domain": int(E["SOL_DOMAIN"]),
                    "explorer": E.get("SITE_EXPLORER") or "https://explorer.solana.com", "cluster": E.get("SITE_CLUSTER", ""),
                    "tokens": [{"symbol": "GNOT", "program": E["ID_GNOT"], "realm": nsr + "hlgnot"},
                               {"symbol": "BUBBLE", "program": E["ID_BUBBLE"], "realm": nsr + "hlbubble"}],
                    "programs": {"mailbox": E["ID_MAILBOX"], "pausable": E["ID_PAUSABLE"], "admin": E["ID_ADMIN"], "client": E["ID_CLIENT"]}}],
    }
    for name, cfg in out.items():
        p = os.path.join(outdir, name)
        fd = os.open(p, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as w:
            json.dump(cfg, w, indent=2)
            w.write("\n")
        os.chmod(p, 0o600)
    print("launch: wrote %s" % ", ".join(sorted(out)))

cmds = {"status": cmd_status, "exists": cmd_exists, "same": cmd_same, "render": cmd_render, "qeval": cmd_qeval,
        "balance": cmd_balance, "param": cmd_param, "meta": cmd_meta, "price": cmd_price, "fee": cmd_fee, "tx": cmd_tx,
        "imports": cmd_imports, "layout": cmd_layout, "configs": cmd_configs, "solexists": cmd_solexists, "solbal": cmd_solbal,
        "solrent": cmd_solrent, "progrent": cmd_progrent}
cmds[sys.argv[1]](*sys.argv[2:])
EOF
lp() { python3 "$OUT/run/launch.py" "$@"; }
gq() { cmd=$1; shift; lp "$cmd" "$GNO_RPC" "$@"; }
pkgpath() { echo "gno.land/${1%%/*}/$GOV_ADDR/${1#*/}"; }
measured() { echo "$MEASURED_GNO" | tr ' ' '\n' | sed -n "s|^$1:||p"; }
gnotx() { # gnotx OUTFILE addpkg FLAGS...: a MsgAddPackage from the governor's key; gnokey's output kept in OUTFILE
  o=$1; shift
  rc=0
  echo | gnokey maketx "$@" -broadcast -chainid "$GNO_CHAIN_ID" -remote "$GNO_RPC" -insecure-password-stdin -home "$GOV_HOME" "$GOV_KEY" >"$o" 2>&1 || rc=$?
  return $rc
}
gnorun() { # gnorun OUTFILE SCRIPT FLAGS...: a MsgRun of SCRIPT from the governor's key (the key, then the file, last)
  o=$1; script=$2; shift 2
  rc=0
  echo | gnokey maketx run "$@" -broadcast -chainid "$GNO_CHAIN_ID" -remote "$GNO_RPC" -insecure-password-stdin -home "$GOV_HOME" "$GOV_KEY" "$script" >"$o" 2>&1 || rc=$?
  return $rc
}
solbal() { lp solbal "$SOL_RPC" "$1"; }
solrent() { lp solrent "$SOL_RPC" "$1"; }
gnot() { python3 -c 'import sys; print("%.6f" % (int(sys.argv[1]) / 1e6))' "$1"; }
sol() { python3 -c 'import sys; print("%.4f" % (int(sys.argv[1]) / 1e9))' "$1"; }
audit_pass() { # audit_pass LOG: hlsol audit of the deployment by its ids (no keypair needed), PASS or not (a holder's audit: finalized)
  "$BIN/hlsol" audit -rpc "$SOL_RPC" -mailbox "$ID_MAILBOX" -gnot "$ID_GNOT" -bubble "$ID_BUBBLE" -client "$ID_CLIENT" -pausable "$ID_PAUSABLE" -admin "$ID_ADMIN" \
    -so "$SO" -gno-rpc "$GNO_RPC" -mailbox-pkgpath "$NSR/hlmailbox" -hlgov "$NSR/hlgov" -deployer "$DEPLOYER_PUB" \
    -expected-lock-gnot "$EXPECTED_LOCK_GNOT" -expected-lock-bubble "$EXPECTED_LOCK_BUBBLE" >"$1" 2>&1 && grep -q '^hlsol audit: PASS' "$1"
}

say "build: the tools from $(cd "$REPO" && git rev-parse --short HEAD 2>/dev/null || echo "an unversioned tree")"
(cd "$REPO/bridge" && nice -n 10 go build -o "$BIN/" ./cmd/...)
echo "hlwatch $("$BIN/hlwatch" version)"

say "(a) preflight: the values, the keys, the layout, both chains — read-only (PART=$PART)"
if [ "$PART" != solana ]; then
  GOV_LISTED=$(gnokey list -home "$GOV_HOME" 2>/dev/null | sed -n "s/^.* $GOV_KEY (local) - addr: \(g1[a-z0-9]*\).*/\1/p")
  [ "$GOV_LISTED" = "$GOV_ADDR" ] || die "the key $GOV_KEY in $GOV_HOME is ${GOV_LISTED:-absent}, not GOV_ADDR $GOV_ADDR"
  echo "the governor: $GOV_ADDR, the key $GOV_KEY in $GOV_HOME"
fi
if [ "$PART" != gno ]; then
  # the keypairs are here: the ids and the deployer's pubkey from them, written to $OUT/ids.env for the gno half
  [ -r "$DEPLOYER" ] || die "no deployer keypair at $DEPLOYER"
  DEPLOYER_PUB=$("$SOLBIN/solana-keygen" pubkey "$DEPLOYER")
  for p in mailbox gnot bubble client pausable admin; do
    [ -r "$PROGRAM_KEYS/$p.json" ] || die "no program keypair $PROGRAM_KEYS/$p.json (six fresh ones: solana-keygen new, never hlsol keygen -seed)"
    id=$("$SOLBIN/solana-keygen" pubkey "$PROGRAM_KEYS/$p.json")
    seeded=$("$BIN/hlsol" keygen -seed "bridge e2e $p" -o "$OUT/run/seeded.json")
    rm -f "$OUT/run/seeded.json"
    [ "$id" != "$seeded" ] || die "$PROGRAM_KEYS/$p.json is the e2e's seeded key ($id): anyone can derive it — the launch takes fresh random keypairs"
    eval "ID_$(echo "$p" | tr a-z A-Z)=$id"
  done
  printf 'ID_MAILBOX=%s\nID_GNOT=%s\nID_BUBBLE=%s\nID_CLIENT=%s\nID_PAUSABLE=%s\nID_ADMIN=%s\nDEPLOYER_PUB=%s\n' \
    "$ID_MAILBOX" "$ID_GNOT" "$ID_BUBBLE" "$ID_CLIENT" "$ID_PAUSABLE" "$ID_ADMIN" "$DEPLOYER_PUB" >"$OUT/ids.env"
else
  # the gno half: no keypair on this machine; the ids and the deployer's pubkey come with $OUT from the Solana half
  [ -f "$OUT/ids.env" ] || die "no $OUT/ids.env: run PART=solana where the deployer's keypair is, then copy $OUT here"
  # shellcheck disable=SC1091
  . "$OUT/ids.env"
fi
export ID_MAILBOX ID_GNOT ID_BUBBLE ID_CLIENT ID_PAUSABLE ID_ADMIN
[ "$(printf '%s\n' "$ID_MAILBOX" "$ID_GNOT" "$ID_BUBBLE" "$ID_CLIENT" "$ID_PAUSABLE" "$ID_ADMIN" | sort -u | wc -l | tr -d ' ')" = 6 ] || die "the six program ids are not six keys"
echo "programs: mailbox $ID_MAILBOX, GNOT $ID_GNOT, BUBBLE $ID_BUBBLE, client $ID_CLIENT, pausable ISM $ID_PAUSABLE, admin program $ID_ADMIN; the deployer $DEPLOYER_PUB"
sh "$HERE/stage.sh" "$ENVFILE" "$STAGE" >"$OUT/logs/stage-preflight.log" 2>&1 || { tail -5 "$OUT/logs/stage-preflight.log"; die "stage.sh refused the values (see $OUT/logs/stage-preflight.log)"; }
grep '^stage: import\|^stage: the domains\|^stage: the verifier' "$OUT/logs/stage-preflight.log"
DIGEST=$(sed -n 's/^# digest (sha256 of the lines above): //p' "$STAGE/manifest.txt")
echo "staged: the manifest's digest $DIGEST"
lp layout
lp configs "$OUT/configs"
"$BIN/hlbridge" providers "$OUT/configs/signer-1.json" "$OUT/configs/signer-2.json" "$OUT/configs/signer-3.json" >"$OUT/logs/providers.log" 2>&1 ||
  die "the signers' provider layout is refused (§5.6: two providers each, no pair shared by two signers, no provider in more than two): $(cat "$OUT/logs/providers.log")"
echo "the signers' layout: $(cat "$OUT/logs/providers.log"); the monitors' pairs are their own"

ST=$(gq status) || die "$GNO_RPC does not answer /status"
set -- $ST
[ "$1" = "$GNO_CHAIN_ID" ] || die "$GNO_RPC serves the chain $1, not GNO_CHAIN_ID $GNO_CHAIN_ID"
AGE=$(($(date +%s) - $3))
[ "$4" = false ] || die "$GNO_RPC is catching up: its head is no head to take the client's trusted header from"
[ "$AGE" -lt 600 ] || die "$GNO_RPC's head $2 is $AGE s old: the client's trusted header comes from the head and must be under an hour old when the client starts (§6.1); a node this far behind is no node to launch from"
echo "gno.land: $GNO_CHAIN_ID at height $2, $AGE s old"
gq exists "$BUBBLE_TOKEN" || die "the BUBBLE token realm $BUBBLE_TOKEN is not on $GNO_RPC"
echo "the BUBBLE token realm $BUBBLE_TOKEN is on the chain (live, never redeployed)"
# the keeper's two gates on an upload (gno.land/pkg/sdk/vm: checkNamespacePermission, checkCLASignature), read now
# rather than learnt from a failed addpkg (or after the Solana side's spend): the governor's own address
# namespace, and the CLA when it is enforced
for gate in "sysnames_pkgpath:IsAuthorizedAddressForNamespace(\"$GOV_ADDR\", \"$GOV_ADDR\")" "syscla_pkgpath:HasValidSignature(\"$GOV_ADDR\")"; do
  realm=$(gq param "vm:p:${gate%%:*}")
  if [ -z "$realm" ] || ! gq exists "$realm"; then echo "the keeper's ${gate%%:*} gate: ${realm:-none} is not enforced here"; continue; fi
  verdict=$(gq qeval "$realm.${gate#*:}")
  [ "$verdict" = "(true bool)" ] || die "$realm.${gate#*:} is $verdict: the governor cannot upload (sign the CLA, or check the namespace) before the launch"
  echo "the keeper's ${gate%%:*} gate: $realm.${gate#*:} is true"
done
PRESENT=""
for pkg in $PACKAGES; do
  path=$(pkgpath "$pkg")
  rc=0; gq exists "$path" || rc=$?
  case $rc in
  0)
    [ -n "$RESUME" ] || die "$path is on the chain already: a launch that stopped is resumed with \`launch.sh LAUNCH_ENV resume\`, which checks it against the stage"
    gq same "$path" "$STAGE/$path" "$GOV_ADDR" || die "$path is on the chain with other files than the stage: a public package is never replaced; the launch cannot go on under this governor's namespace"
    PRESENT="$PRESENT $pkg" ;;
  3) ;;
  *) die "vm/qfile $path on $GNO_RPC failed" ;;
  esac
done
echo "the bridge's nine package paths: ${PRESENT:+on the chain and matching the stage:$PRESENT; }the rest absent"
PR=$(gq price "$GNO_GAS_PRICE_FLOOR") || die "auth/gasprice on $GNO_RPC"
set -- $PR
PRICE_NUM=$1; PRICE_DEN=$2
STORAGE_PRICE=$(gq param vm:p:storage_price | sed -n 's/^\([0-9]*\)ugnot$/\1/p')
[ -n "$STORAGE_PRICE" ] || STORAGE_PRICE=100
echo "gas: $PRICE_NUM ugnot per $PRICE_DEN gas (the chain's, never under $GNO_GAS_PRICE_FLOOR); storage: $STORAGE_PRICE ugnot a byte"

# the GNOT the uploads and the Launch need: simulated where the imports are up, measured + 50% elsewhere (the
# governor's machine: a simulation is signed by its key)
NEED=0
GNOLIST="$PACKAGES launch"
[ "$PART" != solana ] || GNOLIST=""
for pkg in $GNOLIST; do
  case " $PRESENT " in *" $pkg "*) continue ;; esac
  how="" ; cost=""
  if [ "$pkg" != launch ]; then
    path=$(pkgpath "$pkg")
    up=yes
    for imp in $(lp imports "$STAGE/$path"); do gq exists "$imp" || { up=""; break; }; done
    if [ -n "$up" ]; then
      gnotx "$OUT/run/sim.txt" addpkg -pkgpath "$path" -pkgdir "$STAGE/$path" -gas-fee 3000000ugnot -gas-wanted 2000000000 -max-deposit 1000000000ugnot -simulate only ||
        die "simulating addpkg $path: $(tail -5 "$OUT/run/sim.txt")"
      set -- $(lp tx "$OUT/run/sim.txt")
      cost=$(($5 + $(lp fee $(($2 * 3 / 2 + 1000000)) "$PRICE_NUM" "$PRICE_DEN")))
      how="simulated: $2 gas, $4 bytes stored"
    fi
  else
    live=$(gq render "$NSR/hlmailbox:json" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("live"))' 2>/dev/null || echo "")
    [ "$live" != True ] || continue
  fi
  if [ -z "$cost" ]; then
    m=$(measured "$pkg")
    b=${m%%:*}; g=${m#*:}
    cost=$(( (b * STORAGE_PRICE + $(lp fee $((g * 3 / 2)) "$PRICE_NUM" "$PRICE_DEN")) * 3 / 2 ))
    how="the rehearsal's $b bytes and $g gas, + 50%"
  fi
  echo "  $pkg: $(gnot "$cost") GNOT ($how)"
  NEED=$((NEED + cost))
done
GOV_BAL=$(gq balance "$GOV_ADDR")
if [ "$PART" = solana ]; then
  echo "the governor $GOV_ADDR holds $(gnot "$GOV_BAL") GNOT (its need is read on its own machine, PART=gno: about 70 GNOT before anything is up)"
else
  echo "the governor needs $(gnot "$NEED") GNOT for what is left to upload and launch; it holds $(gnot "$GOV_BAL")"
  [ "$GOV_BAL" -ge "$NEED" ] || die "the governor $GOV_ADDR holds $(gnot "$GOV_BAL") GNOT, $(gnot $((NEED - GOV_BAL))) short of $(gnot "$NEED")"
fi

# the SOL the deploy needs: each undeployed program's rent at SOL_RPC's rate (programdata at the ELF's size and
# the program account; the loader drains a deploy's buffer to the payer before it pays for the programdata, so the
# buffer is the same lamports, never more), the state accounts and the fees, the floats
SOLVED=""
if audit_pass "$OUT/logs/audit-preflight.log"; then SOLVED=1; fi
if [ "$PART" = gno ] && [ -z "$SOLVED" ]; then
  grep -E '^FAIL|^hlsol audit' "$OUT/logs/audit-preflight.log" | head -5
  die "the Solana side does not pass its audit from here: run PART=solana where the deployer's keypair is first, then copy its $OUT here (the audit reads $OUT/so)"
fi
SOLNEED=0; UNDEPLOYED=""
for p in mailbox gnot bubble client pausable admin; do
  eval "id=\$ID_$(echo "$p" | tr a-z A-Z)"
  rc=0; lp solexists "$SOL_RPC" "$id" || rc=$?
  [ "$rc" = 0 ] || [ "$rc" = 3 ] || die "getAccountInfo $id on $SOL_RPC failed"
  if [ "$rc" = 0 ]; then
    # the gno half finds them deployed by the Solana half: that is its order, not a resume
    [ -n "$RESUME" ] || [ "$PART" = gno ] || die "the program id $id ($p) is in use on $SOL_RPC: a launch that stopped is resumed with \`launch.sh LAUNCH_ENV resume\`; fresh keys are never in use"
  elif [ -z "$SOLVED" ]; then
    UNDEPLOYED="$UNDEPLOYED $p"
  fi
done
if [ -z "$SOLVED" ]; then
  for p in $UNDEPLOYED; do
    case $p in
    mailbox) f=$HYPERLANE_SO/mailbox.so ;;
    gnot | bubble) f=$HYPERLANE_SO/token.so ;;
    client) f=$SO/gno_light_client_ism.so ;;
    pausable) f=$SO/pausable_ism.so ;;
    admin) f=$SO/admin_program.so ;;
    esac
    if [ -f "$f" ]; then size=$(wc -c <"$f" | tr -d ' '); else size=$(echo "$MEASURED_ELF" | tr ' ' '\n' | sed -n "s|^$p:||p"); size=$((size + size / 100)); fi
    [ -n "$size" ] || die "no size for the $p program"
    r=$(($(solrent $((size + 45))) + $(solrent 36)))
    echo "  $p: $(sol "$r") SOL of rent ($size bytes)"
    SOLNEED=$((SOLNEED + r))
  done
  st=$((MEASURED_STATE_LAMPORTS * 3 / 2 + 2 * ATA_FUND + AUTHORITY_FUND))
  echo "  the state accounts and fees (the rehearsal's $(sol "$MEASURED_STATE_LAMPORTS") SOL + 50%) and the floats (two ATA payers at $(sol "$ATA_FUND"), the authority PDA at $(sol "$AUTHORITY_FUND")): $(sol "$st") SOL"
  SOLNEED=$((SOLNEED + st))
fi
DEPLOYER_BAL=$(solbal "$DEPLOYER_PUB")
echo "the deployer $DEPLOYER_PUB needs $(sol "$SOLNEED") SOL${SOLVED:+ (the Solana side passes its audit already)}; it holds $(sol "$DEPLOYER_BAL")"
[ "$DEPLOYER_BAL" -ge "$SOLNEED" ] || die "the deployer holds $(sol "$DEPLOYER_BAL") SOL, $(sol $((SOLNEED - DEPLOYER_BAL))) short of $(sol "$SOLNEED")"
echo "preflight: PASS"

say "(b) the Solana side: hlsol setup (six deployments, the setup step; factor $FACTOR; the realms under $GOV_ADDR)"
if [ "$PART" != gno ]; then
  for f in mailbox.so token.so; do
    [ -f "$HYPERLANE_SO/$f" ] || python3 "$REPO/bridge/local/dump-programs.py" "$HYPERLANE_SO"
    cmp -s "$HYPERLANE_SO/$f" "$SO/$f" 2>/dev/null || cp "$HYPERLANE_SO/$f" "$SO/$f"
    echo "$f: sha256 $(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$SO/$f") (Hyperlane's mainnet build, byte for byte)"
  done
fi
if [ -n "$SOLVED" ] || audit_pass "$OUT/logs/audit-b.log"; then
  echo "the Solana side passes its audit: nothing to set up (skipped)"
else
  # the programs' sources, staged: hlsol setup writes the launch values into their config.rs, never the repo's
  rsync -a --delete --exclude target --exclude tests "$REPO/bridge/programs/" "$PROGS/"
  B0=$(solbal "$DEPLOYER_PUB")
  nice -n 10 "$BIN/hlsol" setup -rpc "$SOL_RPC" -deployer "$DEPLOYER" -keys "$PROGRAM_KEYS" -so "$SO" -programs "$PROGS" -factor "$FACTOR" -solana-bin "$SOLBIN" \
    -gno-rpc "$GNO_RPC" -gno-chain-id "$GNO_CHAIN_ID" -gno-domain "$GNO_DOMAIN" -local-domain "$SOL_DOMAIN" \
    -mailbox-pkgpath "$NSR/hlmailbox" -hlgov "$NSR/hlgov" -gnot-router "$NSR/hlgnot" -bubble-router "$NSR/hlbubble" \
    -guardians "$GUARDIAN1_SOL,$GUARDIAN2_SOL,$GUARDIAN3_SOL,$GUARDIAN4_SOL" -watch "$WATCH5_SOL,$WATCH6_SOL" -treasurer "$TREASURER_SOL" \
    -expected-lock-gnot "$EXPECTED_LOCK_GNOT" -expected-lock-bubble "$EXPECTED_LOCK_BUBBLE" -start-sequence 0 \
    -ata-fund "$ATA_FUND" -authority-fund "$AUTHORITY_FUND" \
    -deploy-send "${DEPLOY_SEND:-rpc}" -max-sign-attempts "${DEPLOY_MAX_SIGN_ATTEMPTS:-0}" -cu-price "${DEPLOY_CU_PRICE:-0}" \
    -gnot-name "$GNOT_NAME" -gnot-symbol "$GNOT_SYMBOL" -gnot-uri "$GNOT_URI" -bubble-name "$BUBBLE_NAME" -bubble-symbol "$BUBBLE_SYMBOL" -bubble-uri "$BUBBLE_URI" \
    >>"$OUT/logs/setup.log" 2>&1 || { tail -20 "$OUT/logs/setup.log"; die "hlsol setup (see $OUT/logs/setup.log; run again with resume: it repeats nothing done)"; }
  grep -q '^hlsol setup: DONE' "$OUT/logs/setup.log" || die "hlsol setup did not reach its done condition: $(tail -3 "$OUT/logs/setup.log")"
  grep -E '^setup: (start sequence|the pausable ISM|deploy|client init|admin program init|finish_setup)' "$OUT/logs/setup.log" | cut -c1-200
  B1=$(solbal "$DEPLOYER_PUB")
  PROGRENT=0
  for p in mailbox gnot bubble client pausable admin; do
    eval "id=\$ID_$(echo "$p" | tr a-z A-Z)"
    r=$(lp progrent "$SOL_RPC" "$id")
    PROGRENT=$((PROGRENT + r))
  done
  SPENT=$((B0 - B1)); FLOATS=$((2 * ATA_FUND + AUTHORITY_FUND))
  printf 'solana-setup\t%s\t%s\t%s\t%s\n' "$SPENT" "$PROGRENT" "$FLOATS" "$((SPENT - PROGRENT - FLOATS))" >"$OUT/costs/solana-setup.tsv"
  echo "the setup spent $(sol "$SPENT") SOL: $(sol "$PROGRENT") the six programs' rent, $(sol "$FLOATS") the floats, $(sol $((SPENT - PROGRENT - FLOATS))) the state accounts and the fees"
fi

say "(c) hlsol audit: the deployment against §5.8, before any realm goes up (AN10)"
n=0
until audit_pass "$OUT/logs/audit.log"; do
  n=$((n + 1)); [ "$n" -lt 30 ] || { grep -E '^FAIL|^hlsol audit' "$OUT/logs/audit.log"; die "hlsol audit does not pass: nothing goes up on gno.land (see $OUT/logs/audit.log)"; }
  sleep 3 # a holder's audit reads at finalized: the setup's last transactions get their 32 slots
done
GOVADDR_HLGOV=$("$BIN/hlkey" pkg "$NSR/hlgov" | cut -d' ' -f1)
for want in "ok   client ELF binds the mailbox pkgpath \"$NSR/hlmailbox\"" "ok   admin program ELF binds r/hlgov's address $GOVADDR_HLGOV" \
  "ok   pausable ISM ELF binds the mailbox $ID_MAILBOX" "ok   admin program ELF binds the BUBBLE token $ID_BUBBLE" "ok   the admin program's upgrade authority, its own = none"; do
  grep -qF -- "$want" "$OUT/logs/audit.log" || die "the audit lacks: $want"
done
grep -E '^hlsol audit|ELF binds|setup_done|clock factors|origin domains' "$OUT/logs/audit.log" | cut -c1-200
if [ "$PART" = solana ]; then
  say "PART=solana: the Solana side is deployed, set up and audited; the gno half runs where the governor's key is"
  for p in mailbox gnot bubble client pausable admin; do eval "echo \"  $p \$ID_$(echo "$p" | tr a-z A-Z)\""; done
  [ ! -f "$OUT/costs/solana-setup.tsv" ] || { set -- $(cat "$OUT/costs/solana-setup.tsv"); echo "the setup spent $(sol "$2") SOL ($(sol "$3") the programs' rent, $(sol "$4") the floats, $(sol "$5") the state and fees)"; }
  echo "next: copy $OUT to the governor's machine — no key is in it; the tools, the build and the programs' sources stay here:"
  echo "  rsync -a --exclude bin --exclude target --exclude programs $OUT/ <the governor's machine>:<its OUT>/"
  echo "then there, its launch env naming that OUT: PART=gno sh bridge/launch/launch.sh LAUNCH_ENV"
  say "launch: DONE (the Solana half)"
  exit 0
fi

say "(d) stage the nine packages with the program ids"
sh "$HERE/stage.sh" "$ENVFILE" "$STAGE" >"$OUT/logs/stage.log" 2>&1 || { tail -5 "$OUT/logs/stage.log"; die "stage.sh"; }
[ "$(sed -n 's/^# digest (sha256 of the lines above): //p' "$STAGE/manifest.txt")" = "$DIGEST" ] || die "the stage changed since the preflight"
cat "$STAGE/manifest.txt"

say "(e) the packages go up from the namespace key, in dependency order"
POLICY=$(gq param vm:p:code_submission_policy)
PARKED=""
[ "$POLICY" != inert ] || echo "the chain parks new code (vm code_submission_policy inert): each upload waits for a package approver ($(gq param vm:p:pkg_approvers)) to enable it, its storage deposit (capped at $(gnot "$INERT_MAX_DEPOSIT") GNOT a package) charged then"
for pkg in $PACKAGES; do
  path=$(pkgpath "$pkg")
  tag=$(echo "$pkg" | tr '/' '-')
  if gq exists "$path"; then
    gq same "$path" "$STAGE/$path" "$GOV_ADDR" || die "$path is on the chain with other files than the stage"
    echo "addpkg $path: on the chain already, its files the stage's (skipped)"
    continue
  fi
  if [ "$POLICY" = inert ]; then
    meta=$(gq meta "$path" || true)
    case $meta in
    inert\ *)
      set -- $meta
      [ "$2" = "$GOV_ADDR" ] || die "$path is parked by $2, not by the namespace key $GOV_ADDR"
      if [ "$3" -ge "$INERT_MAX_DEPOSIT" ]; then
        echo "addpkg $path: parked already (max deposit $(gnot "$3") GNOT), awaiting the approver"
        PARKED="$PARKED $path"
        continue
      fi
      echo "addpkg $path: parked with a max deposit of $(gnot "$3") GNOT, under the cap: replaced (its submitter may)" ;;
    esac
  fi
  gnotx "$OUT/run/sim.txt" addpkg -pkgpath "$path" -pkgdir "$STAGE/$path" -gas-fee 3000000ugnot -gas-wanted 2000000000 -max-deposit 1000000000ugnot -simulate only ||
    die "simulating addpkg $path: $(tail -5 "$OUT/run/sim.txt")"
  set -- $(lp tx "$OUT/run/sim.txt")
  WANT=$(($2 * 3 / 2 + 1000000)); [ "$WANT" -le 2000000000 ] || WANT=2000000000
  FEE=$(lp fee "$WANT" "$PRICE_NUM" "$PRICE_DEN"); MAXDEP=$(($5 * 3 / 2 + 1000000))
  [ "$POLICY" != inert ] || MAXDEP=$INERT_MAX_DEPOSIT   # nothing is stored at submit: the simulation shows no deposit
  BAL0=$(gq balance "$GOV_ADDR")
  [ "$BAL0" -ge $((FEE + MAXDEP)) ] || die "the governor holds $(gnot "$BAL0") GNOT; addpkg $path wants up to $(gnot $((FEE + MAXDEP)))"
  gnotx "$OUT/logs/addpkg-$tag.log" addpkg -pkgpath "$path" -pkgdir "$STAGE/$path" -gas-fee "${FEE}ugnot" -gas-wanted "$WANT" -max-deposit "${MAXDEP}ugnot" ||
    die "addpkg $path: $(tail -5 "$OUT/logs/addpkg-$tag.log")"
  set -- $(lp tx "$OUT/logs/addpkg-$tag.log")
  [ "$1" = 1 ] || die "addpkg $path: no OK!: $(tail -5 "$OUT/logs/addpkg-$tag.log")"
  BAL1=$(gq balance "$GOV_ADDR")
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$pkg" "$2" "$WANT" "$FEE" "$4" "$5" "$((BAL0 - BAL1))" >"$OUT/costs/addpkg-$tag.tsv"
  if [ "$POLICY" = inert ] && ! gq exists "$path"; then
    meta=$(gq meta "$path" || true)
    case $meta in inert\ "$GOV_ADDR"\ *) ;; *) die "addpkg $path: neither live nor parked by $GOV_ADDR after the upload: ${meta:-no status}" ;; esac
    echo "addpkg $path: height $6, parked (gas fee $(gnot "$FEE") GNOT), awaiting the approver"
    PARKED="$PARKED $path"
    continue
  fi
  gq same "$path" "$STAGE/$path" "$GOV_ADDR" >/dev/null || die "addpkg $path: the served files differ from the stage: $(gq same "$path" "$STAGE/$path" "$GOV_ADDR" || true)"
  echo "addpkg $path: height $6, $2 gas used of $WANT (fee $(gnot "$FEE") GNOT), $4 bytes stored (deposit $(gnot "$5") GNOT); the governor paid $(gnot $((BAL0 - BAL1))) GNOT; vm/qfile: its files the stage's"
done

if [ -n "$PARKED" ]; then
  echo "parked, awaiting the chain's package approver:$PARKED"
  say "launch: WAITING — resume once the approver enabled them: launch.sh LAUNCH_ENV resume (each is then checked file by file against the stage)"
  exit 0
fi

say "(f) the governor's Launch: hlmailbox.Govern with the Launch request, by MsgRun from his key (M1k)"
mailbox_json() { gq render "$NSR/hlmailbox:json"; }
live() { mailbox_json | python3 -c 'import json,sys; print(json.load(sys.stdin)["live"])'; }
if [ "$(live)" = True ]; then
  echo "the mailbox renders live: launched already (skipped)"
elif [ "$GOVERNOR" != "$GOV_ADDR" ] && [ "$LAUNCH" = yes ]; then
  printf 'package main\n\nimport "%s/hlmailbox"\n\nfunc main(cur realm) {\n\thlmailbox.Govern(cross(cur), hlmailbox.NewLaunchRequest(cross(cur)))\n}\n' "$NSR" >"$OUT/run/launch.gno"
  echo "the Launch is the governor's own script, signed by $GOVERNOR (not this machine's key): from the machine holding it,"
  echo "  gnokey maketx run -gas-fee 400000ugnot -gas-wanted 400000000 -max-deposit 10000000ugnot -broadcast -chainid $GNO_CHAIN_ID -remote $GNO_RPC <its key> $OUT/run/launch.gno"
  echo "  (the script: $(tr '\n' ' ' <"$OUT/run/launch.gno"))"
  say "launch: WAITING — for the governor's Launch; then launch.sh LAUNCH_ENV resume writes the machines' configs"
  exit 0
elif [ "$LAUNCH" = no ]; then
  echo "LAUNCH=no: stopping before the Launch, so the deployment's checklist goes out first (§6.6); the realms refuse sends and credits until it"
else
  printf 'package main\n\nimport "%s/hlmailbox"\n\nfunc main(cur realm) {\n\thlmailbox.Govern(cross(cur), hlmailbox.NewLaunchRequest(cross(cur)))\n}\n' "$NSR" >"$OUT/run/launch.gno"
  gnorun "$OUT/run/sim.txt" "$OUT/run/launch.gno" -gas-fee 3000000ugnot -gas-wanted 2000000000 -max-deposit 1000000000ugnot -simulate only ||
    die "simulating the Launch: $(tail -5 "$OUT/run/sim.txt")"
  set -- $(lp tx "$OUT/run/sim.txt")
  WANT=$(($2 * 3 / 2 + 1000000)); FEE=$(lp fee "$WANT" "$PRICE_NUM" "$PRICE_DEN"); MAXDEP=$(($5 * 3 / 2 + 1000000))
  BAL0=$(gq balance "$GOV_ADDR")
  gnorun "$OUT/logs/launch.log" "$OUT/run/launch.gno" -gas-fee "${FEE}ugnot" -gas-wanted "$WANT" -max-deposit "${MAXDEP}ugnot" ||
    die "the Launch: $(tail -5 "$OUT/logs/launch.log")"
  set -- $(lp tx "$OUT/logs/launch.log")
  [ "$1" = 1 ] || die "the Launch: no OK!: $(tail -5 "$OUT/logs/launch.log")"
  BAL1=$(gq balance "$GOV_ADDR")
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' launch "$2" "$WANT" "$FEE" "$4" "$5" "$((BAL0 - BAL1))" >"$OUT/costs/launch.tsv"
  echo "the Launch: height $6, $2 gas used of $WANT (fee $(gnot "$FEE") GNOT), $4 bytes stored (deposit $(gnot "$5") GNOT); the governor paid $(gnot $((BAL0 - BAL1))) GNOT"
fi
mailbox_json | python3 -c '
import json, sys
j = json.load(sys.stdin); gov = sys.argv[1]; launch = sys.argv[2]
assert j["governor"] == gov and j["yielded"] == 0, ("the governor", j.get("governor"), j.get("yielded"))
assert j["live"] or launch == "no", "not live after the Launch"
print("hlmailbox: live %s, the governor %s, yielded %s, the verifier registered %s, signers %d of threshold %d" % (j["live"], j["governor"], j["yielded"], j["registered"], len(j["signers"]["keys"]), j["signers"]["threshold"]))' "$GOVERNOR" "$LAUNCH" ||
  die "the mailbox does not render the launch as it should: $(mailbox_json | cut -c1-300)"

say "(g) the machines' configs and the site's"
lp configs "$OUT/configs"
"$BIN/hlbridge" providers "$OUT/configs/signer-1.json" "$OUT/configs/signer-2.json" "$OUT/configs/signer-3.json" >"$OUT/logs/providers.log" 2>&1 ||
  die "the signers' provider layout is refused: $(cat "$OUT/logs/providers.log")"
echo "the signers' layout: $(cat "$OUT/logs/providers.log")"
"$BIN/hlsite" -config "$OUT/configs/site.json" -site "$REPO/bridge/site" -listen 127.0.0.1:0 >"$OUT/logs/hlsite-check.log" 2>&1 &
SITEPID=$!
sleep 2
kill -0 "$SITEPID" 2>/dev/null || die "hlsite refused site.json: $(cat "$OUT/logs/hlsite-check.log")"
kill "$SITEPID" 2>/dev/null || true; wait "$SITEPID" 2>/dev/null || true
echo "site.json: hlsite takes it ($(head -1 "$OUT/logs/hlsite-check.log"))"
ls -l "$OUT/configs" | sed 1d | awk '{print "  " $NF " (" $1 ")"}'

say "(h) the summary"
echo "the governor: $GOVERNOR (until his Yield on hlmailbox; GovDAO after, §11.16); the namespace key $GOV_ADDR"
echo "programs (Solana, factor $FACTOR, every authority the admin program's or none):"
for p in mailbox gnot bubble client pausable admin; do eval "echo \"  $p \$ID_$(echo "$p" | tr a-z A-Z)\""; done
echo "realms (gno.land $GNO_CHAIN_ID, the manifest's digest $DIGEST):"
for pkg in $PACKAGES; do path=$(pkgpath "$pkg"); echo "  $path  $("$BIN/hlkey" pkg "$path" | cut -d' ' -f1)"; done
echo "  the BUBBLE token realm, live: $BUBBLE_TOKEN"
echo "costs:"
if [ -f "$OUT/costs/solana-setup.tsv" ]; then
  set -- $(cat "$OUT/costs/solana-setup.tsv")
  echo "  SOL, the deployer's spend in the setup: $(sol "$2") ($(sol "$3") the six programs' rent, $(sol "$4") the floats, $(sol "$5") the state accounts and fees)"
fi
TOTAL=0
for pkg in $PACKAGES launch; do
  f=$OUT/costs/$(if [ "$pkg" = launch ]; then echo launch; else echo "addpkg-$(echo "$pkg" | tr '/' '-')"; fi).tsv
  [ -f "$f" ] || continue
  set -- $(cat "$f")
  echo "  GNOT, $1: $(gnot "$7") ($(gnot "$6") the storage deposit for $5 bytes, $(gnot "$4") the gas fee for $3 gas wanted, $2 used)"
  TOTAL=$((TOTAL + $7))
done
echo "  GNOT in all: $(gnot "$TOTAL")"
echo "files: the configs in $OUT/configs (0600: the provider URLs may carry keys), the manifest $STAGE/manifest.txt, the audit $OUT/logs/audit.log"
cat <<EOF
next:
  1. each machine takes its config: signer-N.json to signer N; monitor-1.json, updater.json and relayer-primary.json to
     monitor 1; monitor-2.json and relayer-backup.json to monitor 2 (the keys they name live there: §5.6).
  2. start them: hlbridge signer -config signer-N.json -key <its hlkey file> -store <dir> -listen 0.0.0.0:9100
     -tls-cert <cert> -tls-key <key> -status-listen 127.0.0.1:9140; hlbridge monitor -config monitor-N.json -state
     monitor.json -listen 127.0.0.1:9122; hlbridge updater -config updater.json -state updater.json -listen
     127.0.0.1:9123; hlbridge relayer -role primary|backup -config relayer-<role>.json -state relayer.json -listen
     127.0.0.1:9121.
  3. fund the floats (§5.5, §11.4): the relayers' SOL key, the two ATA payers to 1.25 SOL, the updater's 0.3 SOL,
     the relayers' gno gas key (500 GNOT), the guardians' and watch keys (20 GNOT, 0.05 SOL each).
  4. hlwatch status -config monitor-1.json -node <your own node>: green.
  5. the deployment's checklist (§6.6): these program ids and build hashes, the manifest, $OUT/logs/audit.log,
     the governor's key, the seats, the signers, the treasurer, the fees and caps, the lock the testing phase holds.
  6. the testing phase: a small lock of his own; decisions by Govern; the outsiders seated, the yield vote, his
     Yield (§11.16).
EOF
say "launch: DONE"
