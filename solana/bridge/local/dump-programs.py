#!/usr/bin/env python3
# Dump upgradeable Solana programs' ELFs over RPC — `solana program dump`, without the CLI and cut to the ELF: the
# programdata account minus its 45-byte header, ending where the ELF's section header table ends (programdata is
# often allocated past the ELF). The local bridge runs Hyperlane's mainnet builds at our own program ids.
#
#   dump-programs.py OUTDIR [RPC]
import base64, hashlib, json, os, struct, sys, urllib.request

ALPH = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'

def b58e(b):
    n = int.from_bytes(b, 'big'); s = ''
    while n:
        n, r = divmod(n, 58); s = ALPH[r] + s
    return '1' * (len(b) - len(b.lstrip(b'\0'))) + s

# Hyperlane's mainnet programs (hyperlane-registry, rust/sealevel/environments/mainnet3): the mailbox, the newest
# multisig-ism-message-id (the default ISM, deployed at slot 448230589), the newest synthetic token build (the
# ALEO route, slot 397549645), and the SPL noop the mailbox expects at its own address.
PROGRAMS = {
    'mailbox': 'E588QtVUvresuXq2KoNEwAmoifCzYGpRBdHByN9KQMbi',
    'ism': 'CaLiNiZpuWz9ddBqthAdX22QMRELEiw2pm1UQnsRnC91',
    'token': '9EJC4KsqrxZzfp2iXp3gm8pKXTqzQo8AWUFQ22vK7Lvr',
    'noop': 'noopb9bkMVfRPU8AsbpTUg8AQkHtKwMYZiFUjNRtMmV',
}

def rpc(url, method, params):
    req = urllib.request.Request(url, json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode(),
                                 {"Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req, timeout=60))

def main():
    out = sys.argv[1]
    url = sys.argv[2] if len(sys.argv) > 2 else 'https://api.mainnet-beta.solana.com'
    os.makedirs(out, exist_ok=True)
    for name, prog in PROGRAMS.items():
        v = rpc(url, 'getAccountInfo', [prog, {"encoding": "base64"}])['result']['value']
        pd = b58e(base64.b64decode(v['data'][0])[4:36])
        d = base64.b64decode(rpc(url, 'getAccountInfo', [pd, {"encoding": "base64"}])['result']['value']['data'][0])
        elf = d[45:]
        assert elf[:4] == b'\x7fELF', name
        shoff, = struct.unpack_from('<Q', elf, 0x28)
        shentsize, shnum = struct.unpack_from('<HH', elf, 0x3A)
        elf = elf[:shoff + shentsize * shnum]
        open(os.path.join(out, name + '.so'), 'wb').write(elf)
        print(name, prog, len(elf), 'bytes, sha256', hashlib.sha256(elf).hexdigest())

main()
