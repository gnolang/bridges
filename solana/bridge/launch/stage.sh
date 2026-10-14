#!/bin/sh
# Stage the bridge's nine packages for the governor's namespace (bridge/PLAN.md §5.3: every launch value compiled
# in; §6.6: the realms' paths and the hash of each file published with the deployment; §11.16: the governor's own
# key uploads them, so they deploy under gno.land/{p,r}/<GOV_ADDR>/…).
#
#   bridge/launch/stage.sh LAUNCH_ENV STAGE_DIR
#
# The six program ids come from the environment when the caller exports them (ID_MAILBOX, ID_GNOT, ID_BUBBLE,
# ID_CLIENT, ID_PAUSABLE, ID_ADMIN — launch.sh does), else from the keypairs in $PROGRAM_KEYS. From the repo's realm sources (the non-test .gno files and gnomod.toml; the repo is never written) into
# STAGE_DIR/<pkgpath>/: p/hyperlane/v0, p/decisions/v0, p/hlroute/v0, r/hlmailbox, r/hlverifier, r/hlfees,
# r/hlgnot, r/hlbubble, r/hlgov — never hlgnot2, the reference successor, which the launch does not deploy. Then:
#   1. the paths: every quoted path of those nine packages, and the realms' `namespace` constant, move from the
#      repo's namespace (the live BUBBLE token's) to GOV_ADDR's, and nothing else does — the imports of the token
#      realm stay at its live path, and the routes' `bubblePath` (`namespace + "bubble"`: the token realm's
#      address, one of the six a transfer may not name, §5.2) is pinned to BUBBLE_TOKEN;
#   2. the launch values (§5.3), each a swap that must match exactly once or the script fails naming it: the
#      signers and their rule, the six seats on gno.land and on Solana, each route's router (its token program as
#      hex), r/hlgov's recipient, pausable ISM and client, the treasurer, the governor in the five staged realms
#      that compile it — and none of bridge/local/e2e.sh's test-only swaps (no cap floor, no maximum, no clock
#      factor: mainnet keeps the compiled values);
#   3. the checks: the domains the realms derive from GNO_CHAIN_ID against GNO_DOMAIN and SOL_DOMAIN; p/decisions'
#      compiled clock factor 1 (§5.9) and r/hlgov's start sequence 0 (a chain's first r/hlgov, the admin program's
#      -start-sequence 0); no `private` gnomod.toml (§5.3); no reference left to the repo's namespace but a live
#      path; every import resolving to a staged package or to one GNO_RPC serves (vm/qfile);
#   4. the manifest: the sha256 of every staged file by its path on chain (STAGE_DIR/manifest.txt), which a member
#      compares with the served files — a served gnomod.toml carries the keeper's [addpkg] lines too.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
die() { printf 'stage: FAIL: %s\n' "$*" >&2; exit 1; }
[ $# -eq 2 ] || { echo "usage: stage.sh LAUNCH_ENV STAGE_DIR" >&2; exit 2; }
ENVFILE=$1
STAGE=$2
case $ENVFILE in /*) ;; *) ENVFILE=$PWD/$ENVFILE ;; esac
[ -f "$ENVFILE" ] || die "no launch env at $ENVFILE"
# shellcheck disable=SC1090
. "$ENVFILE"
for v in GNO_RPC GNO_CHAIN_ID GNO_DOMAIN SOL_DOMAIN GOV_ADDR BUBBLE_TOKEN \
  GUARDIAN1_GNO GUARDIAN2_GNO GUARDIAN3_GNO GUARDIAN4_GNO WATCH5_GNO WATCH6_GNO \
  GUARDIAN1_SOL GUARDIAN2_SOL GUARDIAN3_SOL GUARDIAN4_SOL WATCH5_SOL WATCH6_SOL \
  SIGNER1_PUB SIGNER2_PUB SIGNER3_PUB SIGNER_THRESHOLD TREASURER_GNO; do
  eval "x=\${$v:-}"
  [ -n "$x" ] || die "$v is empty in $ENVFILE"
  case $x in FILL*) die "$v is not filled in $ENVFILE" ;; esac
done

PY=$(mktemp -d "${TMPDIR:-/tmp}/hlstage.XXXXXX")
trap 'rm -rf "$PY"' EXIT INT TERM
cat >"$PY/stage.py" <<'EOF'
import hashlib, os, re, sys, urllib.parse, urllib.request, json, base64

B58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
BECH32 = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
PACKAGES = ["p/hyperlane/v0", "p/decisions/v0", "p/hlroute/v0", "r/hlmailbox", "r/hlverifier", "r/hlfees", "r/hlgnot", "r/hlbubble", "r/hlgov"]

def die(msg):
    print("stage: FAIL: " + msg, file=sys.stderr)
    sys.exit(1)

def read(path):
    return open(path, encoding="utf-8").read()

def b58hex(s):
    n = 0
    for c in s:
        if c not in B58:
            die("%r is not a base58 key" % s)
        n = n * 58 + B58.index(c)
    b = n.to_bytes((n.bit_length() + 7) // 8, "big") if n else b""
    b = b"\0" * (len(s) - len(s.lstrip("1"))) + b
    if len(b) != 32:
        die("%r is not a 32-byte key" % s)
    return b.hex()

def bech32_ok(addr):
    if addr != addr.lower() or not addr.startswith("g1") or len(addr) != 40:
        return False
    data = addr[2:]
    if any(c not in BECH32 for c in data):
        return False
    gen = [0x3B6A57B2, 0x26508E6D, 0x1EA119FA, 0x3D4233DD, 0x2A1462B3]
    chk = 1
    for v in [ord("g") >> 5, 0, ord("g") & 31] + [BECH32.index(c) for c in data]:
        top = chk >> 25
        chk = (chk & 0x1FFFFFF) << 5 ^ v
        for i in range(5):
            if (top >> i) & 1:
                chk ^= gen[i]
    return chk == 1

def pkgdirs(stage, ns):
    return [(p, os.path.join(stage, "gno.land", p.split("/")[0], ns, *p.split("/")[1:])) for p in PACKAGES]

def files(d):
    return sorted(f for f in os.listdir(d) if f.endswith(".gno") or f == "gnomod.toml")

def qfile(rpc, data):
    url = rpc.rstrip("/") + "/abci_query?path=" + urllib.parse.quote('"vm/qfile"') + "&data=0x" + data.encode().hex()
    j = json.load(urllib.request.urlopen(url, timeout=30))
    if j.get("error"):
        raise RuntimeError(json.dumps(j["error"]))
    r = j["result"]["response"]["ResponseBase"]
    if r.get("Error"):
        if "is not available" in (r.get("Log") or "") or "InvalidPackage" in json.dumps(r.get("Error")):
            return None
        raise RuntimeError((r.get("Log") or "")[:300])
    return base64.b64decode(r["Data"] or "")

def cmd_copy(src, dst):
    os.makedirs(dst, exist_ok=True)
    n = 0
    for f in sorted(os.listdir(src)):
        if f == "gnomod.toml" or (f.endswith(".gno") and not f.endswith("_test.gno") and not f.endswith("_filetest.gno")):
            open(os.path.join(dst, f), "w", encoding="utf-8").write(read(os.path.join(src, f)))
            n += 1
    mod = read(os.path.join(dst, "gnomod.toml"))
    if re.search(r"(?m)^\s*private\s*=", mod):
        die("%s/gnomod.toml sets `private`: a private package is re-uploadable by its namespace's owner (§5.3)" % src)
    print("stage: %s: %d files" % (dst, n))

def cmd_rewrite(stage, old, new):
    pairs = [('"gno.land/%s/%s/%s"' % (p.split("/", 1)[0], old, p.split("/", 1)[1]), '"gno.land/%s/%s/%s"' % (p.split("/", 1)[0], new, p.split("/", 1)[1])) for p in PACKAGES]
    pairs.append(('"gno.land/r/%s/"' % old, '"gno.land/r/%s/"' % new))  # the realms' namespace constant
    total = 0
    for pkg, d in pkgdirs(stage, new):
        for f in files(d):
            path = os.path.join(d, f)
            src = read(path)
            n = 0
            for a, b in pairs:
                n += src.count(a)
                src = src.replace(a, b)
            open(path, "w", encoding="utf-8").write(src)
            total += n
            if n:
                print("stage: %s/%s: %d paths rewritten" % (pkg, f, n))
    print("stage: %d quoted paths moved from gno.land/{p,r}/%s/ to gno.land/{p,r}/%s/" % (total, old, new))

def cmd_swap(path, name, pat, to):
    src = read(path)
    out, n = re.subn(pat, lambda m: to, src, flags=re.M)
    if n != 1:
        die("the swap %s matched %d times in %s, not once (pattern %s)" % (name, n, path, pat))
    open(path, "w", encoding="utf-8").write(out)
    print("stage: %s: %s" % (name, to.strip()))

def cmd_value(path, pat):
    m = re.findall(pat, read(path), flags=re.M)
    if len(m) != 1:
        die("%s matched %d times in %s, not once" % (pat, len(m), path))
    print(m[0])

def cmd_b58hex(key):
    print(b58hex(key))

def cmd_gnoaddr(name, addr):
    if not bech32_ok(addr):
        die("%s %r is not a g1… address (bech32, its checksum included)" % (name, addr))

def imports_of(src):
    out = []
    for m in re.finditer(r'(?m)^import\s+(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"([^"]+)"', src):
        out.append(m.group(1))
    for m in re.finditer(r"(?ms)^import\s*\((.*?)^\)", src):
        for line in m.group(1).splitlines():
            line = line.split("//", 1)[0].strip()
            mm = re.match(r'(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"([^"]+)"$', line)
            if mm:
                out.append(mm.group(1))
    return out

def cmd_check(stage, old, new, bubble, treasurer, rpc):
    staged = {"gno.land/%s/%s/%s" % (p.split("/", 1)[0], new, p.split("/", 1)[1]): p for p in PACKAGES}
    # every staged package's module line is its own path, and each realm's pkgPath constant agrees with it
    for pkg, d in pkgdirs(stage, new):
        want = "gno.land/%s/%s/%s" % (pkg.split("/", 1)[0], new, pkg.split("/", 1)[1])
        mod = re.findall(r'(?m)^module\s*=\s*"([^"]+)"', read(os.path.join(d, "gnomod.toml")))
        if mod != [want]:
            die("%s's gnomod.toml names module %s, not %s" % (pkg, mod, want))
        for f in files(d):
            src = read(os.path.join(d, f))
            ns = re.findall(r'(?m)^\s*namespace\s*=\s*"([^"]+)"', src)
            for m in re.findall(r'(?m)^\s*(?:const\s+)?pkgPath\s*=\s*namespace\s*\+\s*"([^"]+)"', src):
                if len(ns) != 1 or ns[0] + m != want:
                    die("%s/%s: pkgPath %s+%s is not its module %s" % (pkg, f, ns, m, want))
            for m in re.findall(r'(?m)^\s*(?:const\s+)?pkgPath\s*=\s*"([^"]+)"', src):
                if m != want:
                    die("%s/%s: pkgPath %s is not its module %s" % (pkg, f, m, want))
    # no reference to the repo's namespace is left but a live path (quoted, not one of the nine) or the treasurer
    kept = {}
    for pkg, d in pkgdirs(stage, new):
        for f in files(d):
            for i, line in enumerate(read(os.path.join(d, f)).splitlines(), 1):
                if old not in line:
                    continue
                rest = line
                for m in re.finditer(r'"(gno\.land/[pr]/%s/[^"]+)"' % old, line):
                    p = m.group(1)
                    if p.replace("/%s/" % old, "/%s/" % new, 1) in staged:
                        die("%s/%s:%d still names the bridge package %s at the repo's namespace" % (pkg, f, i, p))
                    kept.setdefault(p, set()).add(pkg)
                    rest = rest.replace(m.group(0), "")
                if old in rest:
                    if treasurer == old and re.match(r'\s*launchTreasurer\s*=\s*address\("%s"\)\s*$' % old, line):
                        continue
                    die("%s/%s:%d keeps the repo's namespace %s outside a live path: %s" % (pkg, f, i, old, line.strip()))
    # every import: a staged package, a standard library, or a package GNO_RPC serves
    users = {}
    for pkg, d in pkgdirs(stage, new):
        for f in files(d):
            if f.endswith(".gno"):
                for imp in imports_of(read(os.path.join(d, f))):
                    users.setdefault(imp, set()).add(pkg)
    for p in kept:
        users.setdefault(p, set())
    missing = []
    for imp in sorted(users):
        by = ", ".join(sorted(users[imp])) or "a constant"
        if "." not in imp.split("/", 1)[0]:
            continue  # a standard library: the chain's own
        if imp in staged:
            print("stage: import %s (by %s): staged" % (imp, by))
            continue
        try:
            got = qfile(rpc, imp)
        except Exception as e:
            die("vm/qfile %s on %s: %s" % (imp, rpc, e))
        if got is None:
            missing.append(imp)
            print("stage: import %s (by %s): MISSING on %s" % (imp, by, rpc))
        else:
            what = " — the live BUBBLE token realm" if imp == bubble else (" — kept at the repo's namespace (a live path)" if old in imp else "")
            print("stage: import %s (by %s): on the chain (vm/qfile: %d files)%s" % (imp, by, len(got.decode().split()), what))
    if missing:
        die("%d import(s) resolve neither to a staged package nor to a package on %s: %s" % (len(missing), rpc, ", ".join(missing)))
    if bubble not in kept:
        die("no staged file names the BUBBLE token realm %s" % bubble)

def cmd_manifest(stage, new, out):
    lines = []
    for pkg, d in pkgdirs(stage, new):
        for f in files(d):
            p = os.path.join(d, f)
            lines.append("%s  %s" % (hashlib.sha256(open(p, "rb").read()).hexdigest(), os.path.relpath(p, stage)))
    body = "\n".join(lines) + "\n"
    digest = hashlib.sha256(body.encode()).hexdigest()
    with open(out, "w") as w:
        w.write("# the bridge's staged files, sha256 by path on chain (bridge/launch/stage.sh); a served gnomod.toml carries the keeper's [addpkg] lines too\n")
        w.write(body)
        w.write("# digest (sha256 of the lines above): %s\n" % digest)
    print(open(out).read(), end="")

cmds = {"copy": cmd_copy, "rewrite": cmd_rewrite, "swap": cmd_swap, "value": cmd_value, "b58hex": cmd_b58hex,
        "gnoaddr": cmd_gnoaddr, "check": cmd_check, "manifest": cmd_manifest}
cmds[sys.argv[1]](*sys.argv[2:])
EOF
py() { python3 "$PY/stage.py" "$@"; }

# the repo's namespace: the live BUBBLE token's, where the realm sources sit (the e2e's and the dev chain's)
OLD=$(sed -n 's|^module = "gno.land/r/\(g1[a-z0-9]*\)/hlmailbox"$|\1|p' "$REPO/realm/hlmailbox/gnomod.toml")
[ -n "$OLD" ] || die "no namespace in realm/hlmailbox/gnomod.toml"
py gnoaddr GOV_ADDR "$GOV_ADDR"
# the governor compiled into the six realms: the namespace key by default, or another key (gnoland-1 lets only its
# run_submitters send MsgRun, and every governor decision is one: Jae 2026-10-14, his cold key governs)
GOVERNOR=${GOVERNOR:-$GOV_ADDR}
py gnoaddr GOVERNOR "$GOVERNOR"
[ "$GOV_ADDR" != "$OLD" ] || die "GOV_ADDR is $OLD, the BUBBLE token's namespace: the governor is a key of its own, and the bridge deploys under its namespace (§11.16)"
[ "$BUBBLE_TOKEN" = "gno.land/r/$OLD/bubble" ] || die "BUBBLE_TOKEN is $BUBBLE_TOKEN, but the realms import gno.land/r/$OLD/bubble (the live token, never redeployed)"
NSP=gno.land/p/$GOV_ADDR
NSR=gno.land/r/$GOV_ADDR

# the values, checked before anything is written
for v in GUARDIAN1_GNO GUARDIAN2_GNO GUARDIAN3_GNO GUARDIAN4_GNO WATCH5_GNO WATCH6_GNO TREASURER_GNO; do eval "py gnoaddr $v \"\$$v\""; done
SEATS_GNO="$GUARDIAN1_GNO $GUARDIAN2_GNO $GUARDIAN3_GNO $GUARDIAN4_GNO $WATCH5_GNO $WATCH6_GNO"
SEATS_SOL="$GUARDIAN1_SOL $GUARDIAN2_SOL $GUARDIAN3_SOL $GUARDIAN4_SOL $WATCH5_SOL $WATCH6_SOL"
[ "$(printf '%s\n' $SEATS_GNO | sort -u | wc -l | tr -d ' ')" = 6 ] || die "the six gno seats are not six keys: no key in two seats or in a seat and a watch slot (§5.8)"
[ "$(printf '%s\n' $SEATS_SOL | sort -u | wc -l | tr -d ' ')" = 6 ] || die "the six Solana seats are not six keys: no key in two seats or in a seat and a watch slot (§5.8)"
SIGNERS=""
for v in SIGNER1_PUB SIGNER2_PUB SIGNER3_PUB; do
  eval "k=\$$v"
  k=$(printf '%s' "$k" | tr 'A-F' 'a-f' | sed 's/^0x//')
  printf '%s' "$k" | grep -qE '^[0-9a-f]{64}$' || die "$v is not an ed25519 key in hex (hlkey show)"
  SIGNERS="${SIGNERS:+$SIGNERS,}$k"
done
[ "$(printf '%s\n' "$SIGNERS" | tr ',' '\n' | sort -u | wc -l | tr -d ' ')" = 3 ] || die "the three signers' keys are not three keys"
case $SIGNER_THRESHOLD in 2 | 3) ;; *) die "SIGNER_THRESHOLD $SIGNER_THRESHOLD: three signers take a rule of 2 or 3 (§5.6: 2 of 3)" ;; esac
hexof() { py b58hex "$1"; }
# (every value a swap takes is computed into a variable first: a failing $(…) inside an argument would not stop the
# script, an assignment does)
SEATS_HEX=""
for k in $SEATS_SOL; do h=$(hexof "$k"); SEATS_HEX="$SEATS_HEX $h"; done
pid() { # pid NAME: a program's id — as the caller exports it (launch.sh's ID_GNOT …), else from its keypair
  eval "v=\${ID_$(echo "$1" | tr a-z A-Z):-}"
  if [ -n "$v" ]; then echo "$v"; return 0; fi
  [ -n "${PROGRAM_KEYS:-}" ] && [ -n "${SOLBIN:-}" ] || die "no id for the $1 program: export ID_$(echo "$1" | tr a-z A-Z), or set PROGRAM_KEYS and SOLBIN"
  "$SOLBIN/solana-keygen" pubkey "$PROGRAM_KEYS/$1.json" 2>/dev/null || die "no program keypair $PROGRAM_KEYS/$1.json"
}
GNOT_ID=$(pid gnot); BUBBLE_ID=$(pid bubble); CLIENT_ID=$(pid client); PAUSABLE_ID=$(pid pausable); ADMIN_ID=$(pid admin)
GNOT_HEX=$(hexof "$GNOT_ID"); BUBBLE_HEX=$(hexof "$BUBBLE_ID"); CLIENT_HEX=$(hexof "$CLIENT_ID"); PAUSABLE_HEX=$(hexof "$PAUSABLE_ID"); ADMIN_HEX=$(hexof "$ADMIN_ID")

echo "stage: the repo's namespace $OLD (the live BUBBLE token's) -> the namespace key's $GOV_ADDR; into $STAGE"
rm -rf "$STAGE"
mkdir -p "$STAGE"
for p in hyperlane decisions hlroute; do py copy "$REPO/realm/p/$p/v0" "$STAGE/$NSP/$p/v0"; done
for r in hlmailbox hlverifier hlfees hlgnot hlbubble hlgov; do py copy "$REPO/realm/$r" "$STAGE/$NSR/$r"; done

# 1. the paths, then the token realm's address pinned in the two routes (it was namespace + "bubble")
py rewrite "$STAGE" "$OLD" "$GOV_ADDR"
py swap "$STAGE/$NSR/hlgnot/hlgnot.gno" "hlgnot bubblePath" 'bubblePath   = namespace \+ "bubble"$' "bubblePath   = \"$BUBBLE_TOKEN\" // the live token realm, never redeployed"
py swap "$STAGE/$NSR/hlbubble/hlbubble.gno" "hlbubble bubblePath" 'bubblePath  = namespace \+ "bubble"$' "bubblePath  = \"$BUBBLE_TOKEN\" // the live token realm, never redeployed"

# 2. the launch values
M=$STAGE/$NSR/hlmailbox
py swap "$M/mailbox.gno" launchSigners '^	launchSigners   = "[a-f0-9,]*"$' "	launchSigners   = \"$SIGNERS\""
py swap "$M/mailbox.gno" launchThreshold '^	launchThreshold = [0-9]+$' "	launchThreshold = $SIGNER_THRESHOLD"
i=1
for v in GUARDIAN1_GNO GUARDIAN2_GNO GUARDIAN3_GNO GUARDIAN4_GNO WATCH5_GNO WATCH6_GNO; do
  eval "a=\$$v"
  py swap "$M/flags.gno" "launchSeat$i" "^	launchSeat$i = address\\(\"[a-z0-9]*\"\\)( //.*)?\$" "	launchSeat$i = address(\"$a\")"
  i=$((i + 1))
done
py swap "$STAGE/$NSR/hlgnot/hlgnot.gno" "hlgnot launchRouter" '^	launchRouter   = "[A-Za-z0-9]*"( //.*)?$' "	launchRouter   = \"$GNOT_HEX\" // the GNOT token program $GNOT_ID"
py swap "$STAGE/$NSR/hlbubble/hlbubble.gno" "hlbubble launchRouter" '^	launchRouter   = "[A-Za-z0-9]*"( //.*)?$' "	launchRouter   = \"$BUBBLE_HEX\" // the BUBBLE token program $BUBBLE_ID"
G=$STAGE/$NSR/hlgov/hlgov.gno
py swap "$G" launchRecipient '^	launchRecipient   = "[a-f0-9]*"( //.*)?$' "	launchRecipient   = \"$ADMIN_HEX\" // the admin program $ADMIN_ID"
py swap "$G" launchPausableISM '^	launchPausableISM = "[a-f0-9]*"( //.*)?$' "	launchPausableISM = \"$PAUSABLE_HEX\" // the pausable ISM $PAUSABLE_ID"
py swap "$G" launchClient '^	launchClient      = "[a-f0-9]*"( //.*)?$' "	launchClient      = \"$CLIENT_HEX\" // the light client $CLIENT_ID"
i=1
for k in $SEATS_SOL; do
  h=$(echo "$SEATS_HEX" | cut -d' ' -f$((i + 1)))
  py swap "$G" "launchSolSeat$i" "^	launchSolSeat$i    = \"[a-f0-9]*\"( //.*)?\$" "	launchSolSeat$i    = \"$h\" // $k"
  i=$((i + 1))
done
py swap "$STAGE/$NSR/hlfees/hlfees.gno" launchTreasurer '^	launchTreasurer = address\("[a-z0-9]*"\)$' "	launchTreasurer = address(\"$TREASURER_GNO\")"
for f in hlmailbox/mailbox.gno hlgnot/hlgnot.gno hlbubble/hlbubble.gno hlfees/hlfees.gno hlgov/hlgov.gno; do
  py swap "$STAGE/$NSR/$f" "launchGovernor in $f" '^const launchGovernor = address\("[a-z0-9]*"\)$' "const launchGovernor = address(\"$GOVERNOR\")"
done

# 3. the checks: the domains (derived from the chain id, compiled nowhere else), the clock factor, the start sequence
val() { py value "$1" "$2"; }
MAINNET_ID=$(val "$M/mailbox.gno" '^	mainnetChainID = "([^"]+)"$')
if [ "$GNO_CHAIN_ID" = "$MAINNET_ID" ]; then
  WANT_GNO=$(val "$M/mailbox.gno" '^	DomainMainnet  = uint32\(([0-9]+)\)$'); WANT_SOL=$(val "$M/mailbox.gno" '^	SolanaMainnet = uint32\(([0-9]+)\)$')
else
  WANT_GNO=$(val "$M/mailbox.gno" '^	DomainTest     = uint32\(([0-9]+)\)$'); WANT_SOL=$(val "$M/mailbox.gno" '^	SolanaLocal   = uint32\(([0-9]+)\)$')
fi
[ "$GNO_DOMAIN" = "$WANT_GNO" ] || die "GNO_DOMAIN $GNO_DOMAIN: on $GNO_CHAIN_ID the realms take $WANT_GNO as gno.land's domain (hlmailbox's domainOf)"
[ "$SOL_DOMAIN" = "$WANT_SOL" ] || die "SOL_DOMAIN $SOL_DOMAIN: on $GNO_CHAIN_ID the realms take $WANT_SOL as Solana's (hlmailbox's solanaOf)"
echo "stage: the domains the realms derive on $GNO_CHAIN_ID: gno.land $WANT_GNO, Solana $WANT_SOL (GNO_DOMAIN and SOL_DOMAIN agree)"
FACTOR_REALMS=$(val "$STAGE/$NSP/decisions/v0/decisions.gno" '^	Factor = int64\(([0-9]+)\)$')
[ "$FACTOR_REALMS" = 1 ] || die "p/decisions compiles a clock factor of $FACTOR_REALMS: the launch's realms take 1 (§5.9)"
[ "$(val "$G" '^	launchSequence = uint64\(([0-9]+)\)$')" = 0 ] || die "r/hlgov's launchSequence is not 0: a chain's first r/hlgov starts at 0, as the admin program's -start-sequence 0 does"
for f in hlmailbox/mailbox.gno hlgnot/hlgnot.gno hlbubble/hlbubble.gno hlfees/hlfees.gno hlgov/hlgov.gno; do
  [ "$(val "$STAGE/$NSR/$f" '^	namespace = "([^"]+)"$')" = "$NSR/" ] || die "$f's namespace is not $NSR/"
  [ "$(val "$STAGE/$NSR/$f" '^const launchGovernor = address\("([a-z0-9]+)"\)$')" = "$GOVERNOR" ] || die "$f's governor is not $GOVERNOR"
done
py check "$STAGE" "$OLD" "$GOV_ADDR" "$BUBBLE_TOKEN" "$TREASURER_GNO" "$GNO_RPC"

# the launch values as compiled, for the deployment's checklist (§6.6)
for r in hlgnot hlbubble; do
  f=$STAGE/$NSR/$r/$r.gno
  fee=$(val "$f" '^	launchFee      = (.+)$'); min=$(val "$f" '^	launchMinimum  = (.+)$'); floor=$(val "$f" '^	launchFeeFloor = (.+)$')
  capfloor=$(val "$f" '^	launchCapFloor = (.+)$'); max=$(val "$f" '^	launchMaximum  = (.+)$')
  echo "stage: $r compiles fee $fee, minimum $min, fee floor $floor, cap floor $capfloor, per-message maximum $max (as the source has them)"
done
verifier=$(val "$M/mailbox.gno" '^	launchVerifier  = (.+)$')
echo "stage: the verifier $verifier ($NSR/hlverifier) with $SIGNER_THRESHOLD of the 3 signers; clock factor 1; r/hlgov starts at 0; the governor $GOVERNOR (the namespace key $GOV_ADDR)"

# 4. the manifest
py manifest "$STAGE" "$GOV_ADDR" "$STAGE/manifest.txt"
