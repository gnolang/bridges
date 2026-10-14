//! The results leaf: block H's per-transaction `ABCIResult{Error, Data, Events}` (tm2 results.go), amino-encoded
//! bare — `[0A <Any error>] [12 <data>] (1A <Any event>)*` — and hashed as a leaf of `LastResultsHash` in header
//! H+1. An event is an Any: `0A <type url> 12 <value>`; gno's `chain.Event` (`/tm.Event`, gnovm stdlibs/chain)
//! is `{1: type, 2: attrs {1: key, 2: value}…, 3: pkg_path}`.
//!
//! The `Dispatch` event (§13): type `Dispatch`, pkg_path the mailbox realm's, attributes in this order: `id`
//! (32 bytes, lowercase hex), `nonce` (decimal), `origin`, `destination` (decimal), `sender`, `recipient` (32
//! bytes hex), `hash` (sha256 of the message bytes, hex). `Reemit` emits the same.

use crate::{amino::*, Error};

pub const EVENT_TYPE_URL: &str = "/tm.Event";
pub const DISPATCH_TYPE: &str = "Dispatch";

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Attribute {
    pub key: String,
    pub value: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Event {
    pub typ: String,
    pub attributes: Vec<Attribute>,
    pub pkg_path: String,
}

/// A parsed ABCIResult: whether an error is present, the data, and every event as (type URL, value bytes).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ParsedResult {
    pub error: bool,
    pub data: Vec<u8>,
    pub events: Vec<(String, Vec<u8>)>,
}

/// The seven attributes of a `Dispatch` event, decoded.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Dispatch {
    pub id: [u8; 32],
    pub nonce: u32,
    pub origin: u32,
    pub destination: u32,
    pub sender: [u8; 32],
    pub recipient: [u8; 32],
    pub hash: [u8; 32],
}

/// Walks a bare struct's fields: returns (field number, typ3, the value slice for length-prefixed fields).
fn fields<'a>(buf: &'a [u8]) -> Result<Vec<(u32, u8, &'a [u8])>, Error> {
    let mut pos = 0;
    let mut out = Vec::new();
    while pos < buf.len() {
        let (field, typ3) = read_key(buf, &mut pos)?;
        let start = pos;
        if typ3 == TYP3_BYTELEN {
            let v = read_bytes(buf, &mut pos)?;
            out.push((field, typ3, v));
        } else {
            skip_value(buf, &mut pos, typ3)?;
            out.push((field, typ3, &buf[start..pos]));
        }
    }
    Ok(out)
}

/// An Any's type URL and value.
pub fn parse_any(buf: &[u8]) -> Result<(String, Vec<u8>), Error> {
    let mut url = None;
    let mut value = Vec::new();
    for (field, typ3, v) in fields(buf)? {
        match (field, typ3) {
            (1, TYP3_BYTELEN) => url = Some(String::from_utf8(v.to_vec()).map_err(|_| Error::Encoding)?),
            (2, TYP3_BYTELEN) => value = v.to_vec(),
            _ => return Err(Error::Encoding),
        }
    }
    Ok((url.ok_or(Error::Encoding)?, value))
}

pub fn parse_result(leaf: &[u8]) -> Result<ParsedResult, Error> {
    let mut r = ParsedResult::default();
    for (field, typ3, v) in fields(leaf)? {
        match (field, typ3) {
            (1, TYP3_BYTELEN) => r.error = true,
            (2, TYP3_BYTELEN) => r.data = v.to_vec(),
            (3, TYP3_BYTELEN) => {
                let (url, value) = parse_any(v)?;
                r.events.push((url, value));
            }
            _ => return Err(Error::BadLeaf),
        }
    }
    Ok(r)
}

/// A `chain.Event`'s value bytes.
pub fn parse_event(value: &[u8]) -> Result<Event, Error> {
    let mut e = Event::default();
    for (field, typ3, v) in fields(value)? {
        match (field, typ3) {
            (1, TYP3_BYTELEN) => e.typ = String::from_utf8(v.to_vec()).map_err(|_| Error::Encoding)?,
            (2, TYP3_BYTELEN) => {
                let mut a = Attribute::default();
                for (f, t, av) in fields(v)? {
                    match (f, t) {
                        (1, TYP3_BYTELEN) => a.key = String::from_utf8(av.to_vec()).map_err(|_| Error::Encoding)?,
                        (2, TYP3_BYTELEN) => a.value = String::from_utf8(av.to_vec()).map_err(|_| Error::Encoding)?,
                        _ => return Err(Error::Encoding),
                    }
                }
                e.attributes.push(a);
            }
            (3, TYP3_BYTELEN) => e.pkg_path = String::from_utf8(v.to_vec()).map_err(|_| Error::Encoding)?,
            _ => return Err(Error::Encoding),
        }
    }
    Ok(e)
}

fn hex32(s: &str) -> Result<[u8; 32], Error> {
    let b = s.as_bytes();
    if b.len() != 64 {
        return Err(Error::BadDispatch);
    }
    let mut out = [0u8; 32];
    for i in 0..32 {
        let hi = nibble(b[2 * i])?;
        let lo = nibble(b[2 * i + 1])?;
        out[i] = hi << 4 | lo;
    }
    Ok(out)
}

fn nibble(c: u8) -> Result<u8, Error> {
    match c {
        b'0'..=b'9' => Ok(c - b'0'),
        b'a'..=b'f' => Ok(c - b'a' + 10),
        _ => Err(Error::BadDispatch),
    }
}

fn decimal_u32(s: &str) -> Result<u32, Error> {
    if s.is_empty() || s.len() > 10 || (s.len() > 1 && s.starts_with('0')) {
        return Err(Error::BadDispatch);
    }
    let mut v: u64 = 0;
    for c in s.bytes() {
        if !c.is_ascii_digit() {
            return Err(Error::BadDispatch);
        }
        v = v * 10 + (c - b'0') as u64;
    }
    u32::try_from(v).map_err(|_| Error::BadDispatch)
}

impl Dispatch {
    /// Reads a `Dispatch` event's attributes, which must be exactly the seven of §13 in order.
    pub fn from_event(e: &Event) -> Result<Dispatch, Error> {
        if e.typ != DISPATCH_TYPE || e.attributes.len() != 7 {
            return Err(Error::BadDispatch);
        }
        let keys = ["id", "nonce", "origin", "destination", "sender", "recipient", "hash"];
        for (a, k) in e.attributes.iter().zip(keys.iter()) {
            if a.key != *k {
                return Err(Error::BadDispatch);
            }
        }
        let a = &e.attributes;
        Ok(Dispatch {
            id: hex32(&a[0].value)?,
            nonce: decimal_u32(&a[1].value)?,
            origin: decimal_u32(&a[2].value)?,
            destination: decimal_u32(&a[3].value)?,
            sender: hex32(&a[4].value)?,
            recipient: hex32(&a[5].value)?,
            hash: hex32(&a[6].value)?,
        })
    }
}

/// Every `Dispatch` event of the mailbox realm in a results leaf, in order. Refuses a leaf that carries an error
/// (a failed transaction's result), and a leaf with no such event.
pub fn dispatch_events(leaf: &[u8], mailbox_pkg_path: &str) -> Result<Vec<Dispatch>, Error> {
    let r = parse_result(leaf)?;
    if r.error {
        return Err(Error::NoDispatch);
    }
    let mut out = Vec::new();
    for (url, value) in &r.events {
        if url != EVENT_TYPE_URL {
            continue;
        }
        let e = parse_event(value)?;
        if e.typ == DISPATCH_TYPE && e.pkg_path == mailbox_pkg_path {
            out.push(Dispatch::from_event(&e)?);
        }
    }
    if out.is_empty() {
        return Err(Error::NoDispatch);
    }
    Ok(out)
}

// ------------------------------------------------------------------ encoding --

/// A `chain.Event`'s value bytes (the bare struct).
pub fn encode_event(e: &Event) -> Vec<u8> {
    let mut out = Vec::with_capacity(64);
    put_bytes_field(&mut out, 1, e.typ.as_bytes());
    for a in &e.attributes {
        let mut kv = Vec::with_capacity(a.key.len() + a.value.len() + 4);
        put_bytes_field(&mut kv, 1, a.key.as_bytes());
        put_bytes_field(&mut kv, 2, a.value.as_bytes());
        // an attribute whose key and value are both empty still writes an empty struct? amino omits it: an empty
        // nested value rolls back to nothing (writeFieldIfNotEmpty), so follow suit
        put_bytes_field(&mut out, 2, &kv);
    }
    put_bytes_field(&mut out, 3, e.pkg_path.as_bytes());
    out
}

/// A results leaf from its parts: an optional error Any, the data, and events as Any bytes.
pub fn encode_result(error_any: Option<&[u8]>, data: &[u8], events: &[Vec<u8>]) -> Vec<u8> {
    let mut out = Vec::new();
    if let Some(e) = error_any {
        put_bytes_field(&mut out, 1, e);
    }
    put_bytes_field(&mut out, 2, data);
    for ev in events {
        put_bytes_field(&mut out, 3, ev);
    }
    out
}

/// The hex string of 32 bytes, as the realm writes it (lowercase).
pub fn hex_lower(b: &[u8]) -> String {
    const H: &[u8; 16] = b"0123456789abcdef";
    let mut s = String::with_capacity(b.len() * 2);
    for x in b {
        s.push(H[(x >> 4) as usize] as char);
        s.push(H[(x & 15) as usize] as char);
    }
    s
}

impl Dispatch {
    /// The event the realm emits for this dispatch.
    pub fn to_event(&self, mailbox_pkg_path: &str) -> Event {
        let attr = |k: &str, v: String| Attribute { key: k.into(), value: v };
        Event {
            typ: DISPATCH_TYPE.into(),
            attributes: vec![
                attr("id", hex_lower(&self.id)),
                attr("nonce", self.nonce.to_string()),
                attr("origin", self.origin.to_string()),
                attr("destination", self.destination.to_string()),
                attr("sender", hex_lower(&self.sender)),
                attr("recipient", hex_lower(&self.recipient)),
                attr("hash", hex_lower(&self.hash)),
            ],
            pkg_path: mailbox_pkg_path.into(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn dispatch_round_trip() {
        let d = Dispatch { id: [0xab; 32], nonce: 7, origin: 1198419788, destination: 1399811149, sender: [2; 32], recipient: [3; 32], hash: [0xcd; 32] };
        let ev = d.to_event("gno.land/r/g1abc/hlmailbox");
        let leaf = encode_result(None, b"", &[any(EVENT_TYPE_URL, &encode_event(&ev))]);
        let got = dispatch_events(&leaf, "gno.land/r/g1abc/hlmailbox").unwrap();
        assert_eq!(got, vec![d]);
        assert_eq!(dispatch_events(&leaf, "gno.land/r/other").unwrap_err(), Error::NoDispatch);
        // a failed transaction's leaf is refused whatever it carries
        let failed = encode_result(Some(&any("/std.UnauthorizedError", b"")), b"", &[any(EVENT_TYPE_URL, &encode_event(&ev))]);
        assert_eq!(dispatch_events(&failed, "gno.land/r/g1abc/hlmailbox").unwrap_err(), Error::NoDispatch);
        // uppercase hex is not the realm's
        let mut bad = ev.clone();
        bad.attributes[0].value = bad.attributes[0].value.to_uppercase();
        let leaf = encode_result(None, b"", &[any(EVENT_TYPE_URL, &encode_event(&bad))]);
        assert_eq!(dispatch_events(&leaf, "gno.land/r/g1abc/hlmailbox").unwrap_err(), Error::BadDispatch);
    }

    #[test]
    fn decimals() {
        assert_eq!(decimal_u32("0").unwrap(), 0);
        assert_eq!(decimal_u32("4294967295").unwrap(), u32::MAX);
        assert!(decimal_u32("4294967296").is_err());
        assert!(decimal_u32("01").is_err());
        assert!(decimal_u32("").is_err());
        assert!(decimal_u32("1a").is_err());
    }
}
