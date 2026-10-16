#!/usr/bin/env python3
"""Fetch gno.land headers, commits, validator sets and block results into JSON fixtures for the light client's tests.

    gnoheaders.py OUTDIR HEIGHT... [--rpc URL] [--results | --no-results] [--pace SECONDS]

For every height H it writes OUTDIR/H.json holding the chain's own RPC answers, untouched (amino JSON, base64
bytes), so the Rust tests parse exactly what the chain serves:

    {"height": H, "block": <block?height=H>, "commit": <commit?height=H>, "validators": <validators?height=H>,
     "block_results": <block_results?height=H> or null}

`head` as a height means the chain's latest block. Block results are fetched for every height whose block has
transactions, for all with --results, for none with --no-results (a header H+1 fetched only for the LastResultsHash
it commits to block H's results under). The commit for height H is the one signed by the validators of H (the one
block H+1 carries as LastCommit); the validators at H are the set that signed H.

--pace waits that many seconds before every request: rpc.gno.land bans an IP for ten minutes on a burst (HTTP 429,
"Per-IP rate limit exceeded"), so a fetch of more than a few heights from the public node runs with --pace 2 or more.
"""
import json, os, sys, time, urllib.request

RPC = 'https://rpc.gno.land'
PACE = 0.0


def rpc(url, q):
    if PACE:
        time.sleep(PACE)
    req = urllib.request.Request(url + '/' + q, headers={'User-Agent': 'gnoheaders/1'})
    r = json.load(urllib.request.urlopen(req, timeout=90))
    if 'error' in r:
        raise SystemExit('rpc %s: %s' % (q, r['error']))
    return r['result']


def main(argv):
    global PACE
    url, results_all, results_none, out, heights = RPC, False, False, None, []
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == '--rpc':
            url = argv[i + 1]
            i += 2
            continue
        if a == '--pace':
            PACE = float(argv[i + 1])
            i += 2
            continue
        if a == '--results':
            results_all = True
            i += 1
            continue
        if a == '--no-results':
            results_none = True
            i += 1
            continue
        if out is None:
            out = a
        else:
            heights.append(a)
        i += 1
    if out is None or not heights:
        raise SystemExit(__doc__)
    os.makedirs(out, exist_ok=True)
    for h in heights:
        if h == 'head':
            h = rpc(url, 'status')['sync_info']['latest_block_height']
        h = int(h)
        block = rpc(url, 'block?height=%d' % h)
        commit = rpc(url, 'commit?height=%d' % h)
        validators = rpc(url, 'validators?height=%d' % h)
        results = None
        if not results_none and (results_all or block['block']['header']['num_txs'] != '0'):
            results = rpc(url, 'block_results?height=%d' % h)
        fx = {'height': h, 'block': block, 'commit': commit, 'validators': validators, 'block_results': results}
        path = os.path.join(out, '%d.json' % h)
        with open(path, 'w') as f:
            json.dump(fx, f, indent=1, sort_keys=True)
            f.write('\n')
        hdr = block['block']['header']
        print('%s: height %s, %s validators, %s txs, validators_hash %s next %s' % (
            path, hdr['height'], len(validators['validators']), hdr['num_txs'], hdr['validators_hash'][:12],
            hdr['next_validators_hash'][:12]))


if __name__ == '__main__':
    main(sys.argv[1:])
