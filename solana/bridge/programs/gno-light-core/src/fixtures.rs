//! The loader of `../fixtures/*.json`: gnoland-1's own RPC answers (`block`, `commit`, `validators`,
//! `block_results` at one height, written by bridge/tools/gnoheaders.py), decoded from amino JSON into the core's
//! types, with the signatures and the per-transaction result leaves re-encoded from the JSON so the tests can
//! check them against the chain's own hashes.

use crate::{
    amino::*,
    commit::{Commit, Precommit},
    header::{BlockId, Header, PartSetHeader, Time},
    results::{encode_event, encode_result, Attribute, Event},
    valset::{Validator, ValidatorSet},
};
use base64::Engine;
use serde_json::Value;
use std::path::Path;

/// One precommit of a commit as the chain serves it, with its signature.
#[derive(Clone, Debug)]
pub struct Vote {
    pub typ: u8,
    pub height: i64,
    pub round: i64,
    pub block_id: BlockId,
    pub time: Time,
    pub signature: [u8; 64],
    pub validator_index: usize,
    pub validator_address: String,
}

#[derive(Clone, Debug)]
pub struct Fixture {
    pub height: i64,
    pub header: Header,
    /// `block_meta.block_id`: the hash the chain computed for this header, and its part set.
    pub block_hash: [u8; 32],
    pub parts: PartSetHeader,
    pub validators: ValidatorSet,
    pub commit_block_id: BlockId,
    pub votes: Vec<Option<Vote>>,
    /// The transactions' result leaves (amino bytes), when the block had any.
    pub results: Option<Vec<Vec<u8>>>,
    pub raw: Value,
}

pub fn b64(v: &Value) -> Vec<u8> {
    match v {
        Value::String(s) => base64::engine::general_purpose::STANDARD.decode(s).expect("base64"),
        _ => Vec::new(),
    }
}

pub fn int(v: &Value) -> i64 {
    match v {
        Value::String(s) => s.parse().expect("integer string"),
        Value::Number(n) => n.as_i64().expect("integer"),
        Value::Null => 0,
        _ => panic!("not an integer: {v}"),
    }
}

pub fn string(v: &Value) -> String {
    match v {
        Value::String(s) => s.clone(),
        Value::Null => String::new(),
        _ => panic!("not a string: {v}"),
    }
}

/// Days since 1970-01-01 of a civil date (Howard Hinnant's days_from_civil).
fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = if y >= 0 { y } else { y - 399 } / 400;
    let yoe = y - era * 400;
    let mp = (m + 9) % 12;
    let doy = (153 * mp + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146097 + doe - 719468
}

/// `2026-10-10T11:42:29.714683325Z` → seconds and nanoseconds.
pub fn parse_rfc3339(s: &str) -> Time {
    let b = s.as_bytes();
    assert!(b.len() >= 20 && b[4] == b'-' && b[7] == b'-' && b[10] == b'T' && b[13] == b':' && b[16] == b':', "time {s}");
    let num = |a: usize, e: usize| -> i64 { s[a..e].parse().expect("digits") };
    let (y, mo, d, h, mi, sec) = (num(0, 4), num(5, 7), num(8, 10), num(11, 13), num(14, 16), num(17, 19));
    let mut nanos: i64 = 0;
    let mut i = 19;
    if b[i] == b'.' {
        i += 1;
        let start = i;
        while i < b.len() && b[i].is_ascii_digit() {
            i += 1;
        }
        let frac = &s[start..i];
        let mut n: i64 = frac.parse().expect("fraction");
        for _ in frac.len()..9 {
            n *= 10;
        }
        nanos = n;
    }
    assert_eq!(&s[i..], "Z", "only UTC times: {s}");
    let secs = days_from_civil(y, mo, d) * 86400 + h * 3600 + mi * 60 + sec;
    Time { secs, nanos: nanos as i32 }
}

pub fn block_id_from_json(v: &Value) -> BlockId {
    if v.is_null() {
        return BlockId::default();
    }
    BlockId {
        hash: b64(&v["hash"]),
        parts: PartSetHeader { total: int(&v["parts"]["total"]) as u32, hash: b64(&v["parts"]["hash"]) },
    }
}

pub fn header_from_json(v: &Value) -> Header {
    Header {
        version: string(&v["version"]),
        chain_id: string(&v["chain_id"]),
        height: int(&v["height"]),
        time: parse_rfc3339(&string(&v["time"])),
        num_txs: int(&v["num_txs"]),
        total_txs: int(&v["total_txs"]),
        app_version: string(&v["app_version"]),
        last_block_id: block_id_from_json(&v["last_block_id"]),
        last_commit_hash: b64(&v["last_commit_hash"]),
        data_hash: b64(&v["data_hash"]),
        validators_hash: b64(&v["validators_hash"]),
        next_validators_hash: b64(&v["next_validators_hash"]),
        consensus_hash: b64(&v["consensus_hash"]),
        app_hash: b64(&v["app_hash"]),
        last_results_hash: b64(&v["last_results_hash"]),
        proposer_address: string(&v["proposer_address"]),
    }
}

pub fn validators_from_json(v: &Value) -> ValidatorSet {
    let vals = v["validators"].as_array().expect("validators");
    ValidatorSet {
        validators: vals
            .iter()
            .map(|x| {
                assert_eq!(x["pub_key"]["@type"], "/tm.PubKeyEd25519");
                let pk = b64(&x["pub_key"]["value"]);
                let mut pubkey = [0u8; 32];
                pubkey.copy_from_slice(&pk);
                Validator { pubkey, power: int(&x["voting_power"]) }
            })
            .collect(),
    }
}

pub fn vote_from_json(v: &Value) -> Option<Vote> {
    if v.is_null() {
        return None;
    }
    let sig = b64(&v["signature"]);
    let mut signature = [0u8; 64];
    signature.copy_from_slice(&sig);
    Some(Vote {
        typ: int(&v["type"]) as u8,
        height: int(&v["height"]),
        round: int(&v["round"]),
        block_id: block_id_from_json(&v["block_id"]),
        time: parse_rfc3339(&string(&v["timestamp"])),
        signature,
        validator_index: int(&v["validator_index"]) as usize,
        validator_address: string(&v["validator_address"]),
    })
}

/// A `std.Coin`/`std.Coins` or an `Address` field: amino writes their string form.
fn string_field(out: &mut Vec<u8>, field: u32, v: &Value) {
    put_bytes_field(out, field, string(v).as_bytes());
}

/// The amino Any of an event as the chain serves it in JSON. The types gnoland-1 emits today; another type is an
/// error so a test skips that block knowingly rather than hashing the wrong bytes.
pub fn event_any_from_json(ev: &Value) -> Result<Vec<u8>, String> {
    let t = string(&ev["@type"]);
    let mut body = Vec::new();
    match t.as_str() {
        "/tm.Event" => {
            let attrs = ev["attrs"].as_array().cloned().unwrap_or_default();
            let e = Event {
                typ: string(&ev["type"]),
                attributes: attrs.iter().map(|a| Attribute { key: string(&a["key"]), value: string(&a["value"]) }).collect(),
                pkg_path: string(&ev["pkg_path"]),
            };
            body = encode_event(&e);
        }
        "/tm.StorageDepositEvent" => {
            put_zigzag_field(&mut body, 1, int(&ev["bytes_delta"]));
            string_field(&mut body, 2, &ev["fee_delta"]);
            string_field(&mut body, 3, &ev["pkg_path"]);
        }
        "/tm.StorageUnlockEvent" => {
            put_zigzag_field(&mut body, 1, int(&ev["bytes_delta"]));
            string_field(&mut body, 2, &ev["fee_refund"]);
            string_field(&mut body, 3, &ev["pkg_path"]);
            if ev["refund_withheld"].as_bool().unwrap_or(false) {
                put_uvarint_field(&mut body, 4, 1);
            }
        }
        "/bank.TransferEvent" => {
            string_field(&mut body, 1, &ev["from"]);
            string_field(&mut body, 2, &ev["to"]);
            string_field(&mut body, 3, &ev["coins"]);
        }
        other => return Err(format!("event type {other} has no schema here")),
    }
    Ok(any(&t, &body))
}

/// One transaction's ABCIResult leaf from `block_results`.
pub fn result_leaf_from_json(tx: &Value) -> Result<Vec<u8>, String> {
    let base = &tx["ResponseBase"];
    if !base["Error"].is_null() {
        return Err("a failed transaction's error has no schema here".into());
    }
    let data = b64(&base["Data"]);
    let mut events = Vec::new();
    if let Some(evs) = base["Events"].as_array() {
        for ev in evs {
            events.push(event_any_from_json(ev)?);
        }
    }
    Ok(encode_result(None, &data, &events))
}

pub fn load(path: impl AsRef<Path>) -> Fixture {
    let raw: Value = serde_json::from_str(&std::fs::read_to_string(path.as_ref()).expect("fixture file")).expect("fixture json");
    let header = header_from_json(&raw["block"]["block"]["header"]);
    let meta_id = block_id_from_json(&raw["block"]["block_meta"]["block_id"]);
    let mut block_hash = [0u8; 32];
    block_hash.copy_from_slice(&meta_id.hash);
    let commit = &raw["commit"]["signed_header"]["commit"];
    let votes = commit["precommits"].as_array().expect("precommits").iter().map(vote_from_json).collect();
    let results = raw["block_results"].as_object().map(|br| {
        br["results"]["deliver_tx"]
            .as_array()
            .cloned()
            .unwrap_or_default()
            .iter()
            .map(|tx| result_leaf_from_json(tx).unwrap_or_else(|e| panic!("{e}")))
            .collect()
    });
    Fixture {
        height: int(&raw["height"]),
        header,
        block_hash,
        parts: meta_id.parts,
        validators: validators_from_json(&raw["validators"]),
        commit_block_id: block_id_from_json(&commit["block_id"]),
        votes,
        results,
        raw,
    }
}

/// Every fixture of a directory, by height.
pub fn load_dir(dir: impl AsRef<Path>) -> Vec<Fixture> {
    let mut out: Vec<Fixture> = std::fs::read_dir(dir)
        .expect("fixtures dir")
        .filter_map(|e| e.ok())
        .filter(|e| e.path().extension().map(|x| x == "json").unwrap_or(false))
        .map(|e| load(e.path()))
        .collect();
    out.sort_by_key(|f| f.height);
    out
}

/// The directory of the fixtures, from any crate of the workspace.
pub fn dir() -> std::path::PathBuf {
    let here = Path::new(env!("CARGO_MANIFEST_DIR"));
    here.join("..").join("fixtures")
}

impl Fixture {
    /// The commit as the client stages it (only the precommits for the commit's BlockID are present) and each slot's
    /// signature.
    pub fn commit(&self) -> (Commit, Vec<Option<[u8; 64]>>) {
        let mut precommits = Vec::with_capacity(self.votes.len());
        let mut sigs = Vec::with_capacity(self.votes.len());
        for v in &self.votes {
            match v {
                Some(v) if v.block_id == self.commit_block_id && v.typ == crate::vote::PRECOMMIT_TYPE => {
                    precommits.push(Some(Precommit { round: v.round, time: v.time }));
                    sigs.push(Some(v.signature));
                }
                _ => {
                    precommits.push(None);
                    sigs.push(None);
                }
            }
        }
        (Commit { block_id: self.commit_block_id.clone(), precommits }, sigs)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn times() {
        assert_eq!(parse_rfc3339("1970-01-01T00:00:00Z"), Time { secs: 0, nanos: 0 });
        assert_eq!(parse_rfc3339("2026-10-10T11:42:33.040788319Z"), Time { secs: 1791632553, nanos: 40788319 });
        assert_eq!(parse_rfc3339("2026-10-10T11:42:33.5Z"), Time { secs: 1791632553, nanos: 500_000_000 });
    }
}
