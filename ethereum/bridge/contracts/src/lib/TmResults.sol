// SPDX-License-Identifier: LicenseRef-GNO-Network-GPL
pragma solidity 0.8.33;

import {TmAmino} from "./TmAmino.sol";

/// The results leaf: a transaction's `ABCIResult{Error, Data, Events}` (tm2 results.go), amino-encoded bare —
/// `[0A <Any error>] [12 <data>] (1A <Any event>)*` — a leaf of `LastResultsHash` in the next header. An event is an
/// Any `{0A <type url> 12 <value>}`; gno's `chain.Event` (`/tm.Event`) is `{1: type, 2: attrs {1: key, 2: value}…,
/// 3: pkg_path}`. Ported from bridge/programs/gno-light-core/src/results.rs `dispatch_events`.
///
/// The `Dispatch` event (the Solana plan §13): type `Dispatch`, pkg_path the mailbox realm's, attributes in this
/// order: `id` (32 bytes, lowercase hex), `nonce` (decimal), `origin`, `destination` (decimal), `sender`,
/// `recipient` (32 bytes, lowercase hex), `hash` (sha256 of the message bytes, lowercase hex).
///
/// A leaf proves a message when all of this holds, exactly as the Rust reader decides: every field of the leaf is
/// 1, 2 or 3 and length-prefixed (else `BadLeaf`), every Any and every `/tm.Event` decodes (else `Encoding`), no
/// error is present (else `NoDispatch`: a failed transaction's leaf proves nothing), every `Dispatch` event of the
/// mailbox's pkg_path carries the seven attributes in order with values in their exact forms (else `BadDispatch`),
/// there is at least one (else `NoDispatch`), and one of them is the message's. One reading differs from the Rust,
/// and only to accept more: strings are compared as bytes and never checked as UTF-8 (the Go twin `gnolight` reads
/// them so); the Dispatch values the match reads are ASCII by their own rules.
library TmResults {
    error BadLeaf();
    error NoDispatch();
    error BadDispatch();

    bytes32 internal constant EVENT_TYPE_URL_HASH = keccak256("/tm.Event");
    bytes32 internal constant DISPATCH_TYPE_HASH = keccak256("Dispatch");

    /// What a Dispatch must carry to be a message's: the message's id (keccak256), hash (sha256) and header fields.
    struct Message {
        bytes32 id;
        bytes32 hash;
        uint32 nonce;
        uint32 origin;
        uint32 destination;
        bytes32 sender;
        bytes32 recipient;
    }

    /// One decoded Dispatch event.
    struct Dispatch {
        bytes32 id;
        uint32 nonce;
        uint32 origin;
        uint32 destination;
        bytes32 sender;
        bytes32 recipient;
        bytes32 hash;
    }

    /// Reads the leaf and answers whether one of the mailbox's Dispatch events is `m`'s; reverts on every refusal
    /// of the Rust reader (above). `pkgPathHash` is keccak256 of the mailbox realm's path; `m.id` is never zero.
    function proves(bytes memory leaf, bytes32 pkgPathHash, Message memory m) internal pure returns (bool found) {
        uint256 count;
        (found, count) = _walk(leaf, pkgPathHash, m);
        if (count == 0) {
            revert NoDispatch();
        }
    }

    /// Every Dispatch event of the mailbox in the leaf, decoded, in order (the tests and the tools); reverts where
    /// `proves` would.
    function dispatches(bytes memory leaf, bytes32 pkgPathHash) internal pure returns (Dispatch[] memory out) {
        Message memory none;
        (, uint256 count) = _walk(leaf, pkgPathHash, none);
        if (count == 0) {
            revert NoDispatch();
        }
        out = new Dispatch[](count);
        uint256 k;
        uint256 pos = 0;
        while (pos < leaf.length) {
            (uint256 field,, uint256 start, uint256 stop) = TmAmino.readField(leaf, pos, leaf.length);
            pos = stop;
            if (field == 3) {
                (bool ours, Dispatch memory d) = _ours(leaf, start, stop, pkgPathHash);
                if (ours) {
                    out[k++] = d;
                }
            }
        }
    }

    /// A field-3 Any at [s, e): whether it is a Dispatch event of the mailbox, and the event decoded.
    function _ours(bytes memory leaf, uint256 s, uint256 e, bytes32 pkgPathHash)
        private
        pure
        returns (bool ours, Dispatch memory d)
    {
        (bool isEvent, uint256 vs, uint256 ve) = _anyEvent(leaf, s, e);
        if (isEvent) {
            uint256 attrs;
            (ours, attrs) = _event(leaf, vs, ve, pkgPathHash);
            if (ours) {
                d = _dispatch(leaf, vs, ve, attrs);
            }
        }
    }

    /// The single pass: every check of the Rust reader; the number of the mailbox's Dispatch events and whether one
    /// is `m`'s (never, when `m.id` is zero).
    function _walk(bytes memory leaf, bytes32 pkgPathHash, Message memory m)
        private
        pure
        returns (bool found, uint256 count)
    {
        bool failed;
        uint256 pos = 0;
        while (pos < leaf.length) {
            (uint256 field, uint256 typ3, uint256 start, uint256 stop) = TmAmino.readField(leaf, pos, leaf.length);
            pos = stop;
            if (typ3 != TmAmino.TYP3_BYTELEN || field > 3) {
                revert BadLeaf();
            }
            if (field == 1) {
                failed = true;
            } else if (field == 3) {
                (bool ours, bool matches) = _eventField(leaf, start, stop, pkgPathHash, m);
                if (ours) {
                    count++;
                    found = found || matches;
                }
            }
        }
        if (failed) {
            revert NoDispatch();
        }
    }

    /// A field-3 Any at [s, e): whether it is a Dispatch event of the mailbox, and whether it is `m`'s.
    function _eventField(bytes memory leaf, uint256 s, uint256 e, bytes32 pkgPathHash, Message memory m)
        private
        pure
        returns (bool ours, bool matches)
    {
        Dispatch memory d;
        (ours, d) = _ours(leaf, s, e, pkgPathHash);
        matches = ours && m.id != bytes32(0) && _is(d, m);
    }

    function _is(Dispatch memory d, Message memory m) private pure returns (bool) {
        return d.id == m.id && d.hash == m.hash && d.nonce == m.nonce && d.origin == m.origin
            && d.destination == m.destination && d.sender == m.sender && d.recipient == m.recipient;
    }

    /// An Any at [s, e): whether its type URL is `/tm.Event`, and where its value lies. The URL must be present;
    /// fields 1 and 2 only, length-prefixed; the last of each wins (results.rs `parse_any`).
    function _anyEvent(bytes memory leaf, uint256 s, uint256 e)
        private
        pure
        returns (bool isEvent, uint256 vs, uint256 ve)
    {
        bool seenUrl;
        uint256 us;
        uint256 ue;
        uint256 pos = s;
        while (pos < e) {
            (uint256 field, uint256 typ3, uint256 start, uint256 stop) = TmAmino.readField(leaf, pos, e);
            pos = stop;
            if (typ3 != TmAmino.TYP3_BYTELEN) {
                revert TmAmino.Encoding();
            }
            if (field == 1) {
                (seenUrl, us, ue) = (true, start, stop);
            } else if (field == 2) {
                (vs, ve) = (start, stop);
            } else {
                revert TmAmino.Encoding();
            }
        }
        if (!seenUrl) {
            revert TmAmino.Encoding();
        }
        isEvent = TmAmino.hashRange(leaf, us, ue - us) == EVENT_TYPE_URL_HASH;
    }

    /// A `chain.Event`'s value at [s, e), checked whole (results.rs `parse_event`: fields 1–3 length-prefixed, every
    /// attribute's fields 1–2 length-prefixed, the last type and pkg_path win): whether it is a Dispatch of the
    /// mailbox, and how many attributes it carries.
    function _event(bytes memory leaf, uint256 s, uint256 e, bytes32 pkgPathHash)
        private
        pure
        returns (bool ours, uint256 attrs)
    {
        uint256 ts;
        uint256 te;
        uint256 ps;
        uint256 pe;
        uint256 pos = s;
        while (pos < e) {
            (uint256 field, uint256 typ3, uint256 start, uint256 stop) = TmAmino.readField(leaf, pos, e);
            pos = stop;
            if (typ3 != TmAmino.TYP3_BYTELEN) {
                revert TmAmino.Encoding();
            }
            if (field == 1) {
                (ts, te) = (start, stop);
            } else if (field == 2) {
                _attribute(leaf, start, stop);
                attrs++;
            } else if (field == 3) {
                (ps, pe) = (start, stop);
            } else {
                revert TmAmino.Encoding();
            }
        }
        ours = TmAmino.hashRange(leaf, ts, te - ts) == DISPATCH_TYPE_HASH
            && TmAmino.hashRange(leaf, ps, pe - ps) == pkgPathHash;
    }

    /// An attribute at [s, e): where its key and value lie (empty when absent; the last of each wins).
    function _attribute(bytes memory leaf, uint256 s, uint256 e)
        private
        pure
        returns (uint256 ks, uint256 ke, uint256 vs, uint256 ve)
    {
        uint256 pos = s;
        while (pos < e) {
            (uint256 field, uint256 typ3, uint256 start, uint256 stop) = TmAmino.readField(leaf, pos, e);
            pos = stop;
            if (typ3 != TmAmino.TYP3_BYTELEN) {
                revert TmAmino.Encoding();
            }
            if (field == 1) {
                (ks, ke) = (start, stop);
            } else if (field == 2) {
                (vs, ve) = (start, stop);
            } else {
                revert TmAmino.Encoding();
            }
        }
    }

    /// The seven attributes of a Dispatch event of the mailbox (its value at [s, e), already checked by `_event`),
    /// in order and in their exact forms (results.rs `Dispatch::from_event`).
    function _dispatch(bytes memory leaf, uint256 s, uint256 e, uint256 attrs)
        private
        pure
        returns (Dispatch memory d)
    {
        if (attrs != 7) {
            revert BadDispatch();
        }
        uint256 j;
        uint256 pos = s;
        while (pos < e) {
            (uint256 field,, uint256 start, uint256 stop) = TmAmino.readField(leaf, pos, e);
            pos = stop;
            if (field == 2) {
                _attr(leaf, start, stop, j, d);
                j++;
            }
        }
    }

    /// The j-th attribute of a Dispatch, read into `d`.
    function _attr(bytes memory leaf, uint256 s, uint256 e, uint256 j, Dispatch memory d) private pure {
        (uint256 ks, uint256 ke, uint256 vs, uint256 ve) = _attribute(leaf, s, e);
        bytes32 k = TmAmino.hashRange(leaf, ks, ke - ks);
        if (j == 0) {
            _key(k, "id");
            d.id = hex32(leaf, vs, ve);
        } else if (j == 1) {
            _key(k, "nonce");
            d.nonce = decimal32(leaf, vs, ve);
        } else if (j == 2) {
            _key(k, "origin");
            d.origin = decimal32(leaf, vs, ve);
        } else if (j == 3) {
            _key(k, "destination");
            d.destination = decimal32(leaf, vs, ve);
        } else if (j == 4) {
            _key(k, "sender");
            d.sender = hex32(leaf, vs, ve);
        } else if (j == 5) {
            _key(k, "recipient");
            d.recipient = hex32(leaf, vs, ve);
        } else {
            _key(k, "hash");
            d.hash = hex32(leaf, vs, ve);
        }
    }

    function _key(bytes32 got, bytes memory want) private pure {
        if (got != keccak256(want)) {
            revert BadDispatch();
        }
    }

    /// Exactly 64 lowercase hex digits at [s, e) (results.rs `hex32`), read in place.
    function hex32(bytes memory leaf, uint256 s, uint256 e) internal pure returns (bytes32 out) {
        if (e - s != 64) {
            revert BadDispatch();
        }
        bool bad;
        assembly ("memory-safe") {
            let p := add(add(leaf, 32), s)
            let v := 0
            for { let i := 0 } lt(i, 64) { i := add(i, 1) } {
                let c := byte(0, mload(add(p, i)))
                // '0'..'9' → 0..9; below '0' the subtraction wraps past 9 and falls to the letters' test
                let n := sub(c, 0x30)
                if gt(n, 9) {
                    // 'a'..'f' → 10..15 (0x57 = 'a' − 10); anything else is refused
                    n := sub(c, 0x57)
                    if or(lt(c, 0x61), gt(c, 0x66)) { bad := 1 }
                }
                v := or(shl(4, v), and(n, 0xf))
            }
            out := v
        }
        if (bad) {
            revert BadDispatch();
        }
    }

    /// A decimal u32 at [s, e): 1–10 digits, no leading zero but "0" itself, at most 2^32 − 1 (results.rs
    /// `decimal_u32`).
    function decimal32(bytes memory leaf, uint256 s, uint256 e) internal pure returns (uint32) {
        uint256 len = e - s;
        if (len == 0 || len > 10 || (len > 1 && TmAmino.byteAt(leaf, s) == 0x30)) {
            revert BadDispatch();
        }
        uint256 v;
        bool bad;
        assembly ("memory-safe") {
            let p := add(add(leaf, 32), s)
            for { let i := 0 } lt(i, len) { i := add(i, 1) } {
                let n := sub(byte(0, mload(add(p, i))), 0x30)
                if gt(n, 9) { bad := 1 }
                v := add(mul(v, 10), n)
            }
        }
        // ten digits stay below 2^34: no wrap before the bound is checked
        if (bad || v > type(uint32).max) {
            revert BadDispatch();
        }
        return uint32(v);
    }
}
