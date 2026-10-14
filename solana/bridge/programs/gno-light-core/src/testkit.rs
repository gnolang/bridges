//! Synthetic chains for the programs' tests: validator keys from seeds, headers with the hashes the client checks,
//! commits signed with ed25519-dalek, result leaves with a `Dispatch` event.

use crate::{
    commit::{Commit, Precommit},
    header::{BlockId, Header, PartSetHeader, Time},
    merkle::{self, SimpleProof},
    results::{self, Dispatch},
    sha256,
    valset::{Validator, ValidatorSet},
    vote,
};
use ed25519_dalek::{Signer, SigningKey};

pub struct TestValidator {
    pub key: SigningKey,
    pub power: i64,
}

impl TestValidator {
    pub fn pubkey(&self) -> [u8; 32] {
        self.key.verifying_key().to_bytes()
    }

    pub fn validator(&self) -> Validator {
        Validator { pubkey: self.pubkey(), power: self.power }
    }

    pub fn sign(&self, msg: &[u8]) -> [u8; 64] {
        self.key.sign(msg).to_bytes()
    }
}

/// Validators from a seed byte and powers, returned in set order (by address).
pub fn validators(seed: u8, powers: &[i64]) -> Vec<TestValidator> {
    let mut vals: Vec<TestValidator> = powers
        .iter()
        .enumerate()
        .map(|(i, p)| {
            let mut s = [0u8; 32];
            s[0] = seed;
            s[1] = i as u8;
            s[31] = 0x5a;
            TestValidator { key: SigningKey::from_bytes(&s), power: *p }
        })
        .collect();
    vals.sort_by_key(|v| v.validator().address(sha256));
    vals
}

pub fn set_of(vals: &[TestValidator]) -> ValidatorSet {
    ValidatorSet { validators: vals.iter().map(|v| v.validator()).collect() }
}

/// A header whose ValidatorsHash and NextValidatorsHash are the given sets' and whose other hashes are filled
/// plausibly; `last_results_hash` None means a block with no transactions before it.
pub fn header(
    chain_id: &str,
    height: i64,
    time_secs: i64,
    set: &ValidatorSet,
    next: &ValidatorSet,
    last_results_hash: Option<[u8; 32]>,
    last_block_hash: [u8; 32],
) -> Header {
    Header {
        version: "v1.0.0-rc.0".into(),
        chain_id: chain_id.into(),
        height,
        time: Time { secs: time_secs, nanos: 123_456_789 },
        num_txs: 0,
        total_txs: height,
        app_version: String::new(),
        last_block_id: BlockId { hash: last_block_hash.to_vec(), parts: PartSetHeader { total: 1, hash: sha256(&[&last_block_hash]).to_vec() } },
        last_commit_hash: sha256(&[b"commit", &height.to_le_bytes()]).to_vec(),
        data_hash: Vec::new(),
        validators_hash: set.hash(sha256).unwrap().to_vec(),
        next_validators_hash: next.hash(sha256).unwrap().to_vec(),
        consensus_hash: sha256(&[b"consensus"]).to_vec(),
        app_hash: sha256(&[b"app", &height.to_le_bytes()]).to_vec(),
        last_results_hash: last_results_hash.map(|h| h.to_vec()).unwrap_or_default(),
        proposer_address: "g1hqhetnnz0raw5hps6yxexl7q09a6f8w3anlptt".into(),
    }
}

/// The commit of a header signed by the validators at the given indexes (set order), round 0, and each slot's
/// signature.
pub fn sign(chain_id: &str, header: &Header, vals: &[TestValidator], signers: &[usize]) -> (Commit, Vec<Option<[u8; 64]>>) {
    sign_at_round(chain_id, header, vals, signers, 0)
}

/// As `sign`, at the given round: the commit's one round (tm2's `Commit.ValidateBasic`), in every present
/// precommit and its sign bytes.
pub fn sign_at_round(chain_id: &str, header: &Header, vals: &[TestValidator], signers: &[usize], round: i64) -> (Commit, Vec<Option<[u8; 64]>>) {
    let hash = header.hash(sha256);
    let block_id = BlockId { hash: hash.to_vec(), parts: PartSetHeader { total: 1, hash: sha256(&[b"parts", &hash]).to_vec() } };
    let mut precommits = vec![None; vals.len()];
    let mut sigs = vec![None; vals.len()];
    for (i, v) in vals.iter().enumerate() {
        if !signers.contains(&i) {
            continue;
        }
        let time = Time { secs: header.time.secs + 3, nanos: 1000 + i as i32 };
        let sb = vote::precommit_sign_bytes(chain_id, header.height, round, &block_id, &time);
        precommits[i] = Some(Precommit { round, time });
        sigs[i] = Some(v.sign(&sb));
    }
    (Commit { block_id, precommits }, sigs)
}

/// A block's result leaves: one transaction whose events are the given dispatches, plus `others` plain leaves.
pub fn dispatch_leaves(pkg_path: &str, dispatches: &[Dispatch], others: usize) -> Vec<Vec<u8>> {
    let events: Vec<Vec<u8>> = dispatches
        .iter()
        .map(|d| crate::amino::any(results::EVENT_TYPE_URL, &results::encode_event(&d.to_event(pkg_path))))
        .collect();
    let mut leaves = vec![results::encode_result(None, b"", &events)];
    for i in 0..others {
        leaves.push(results::encode_result(None, &[i as u8; 4], &[]));
    }
    leaves
}

/// The root over leaves and the proof of one.
pub fn results_proof(leaves: &[Vec<u8>], index: usize) -> ([u8; 32], SimpleProof) {
    let refs: Vec<&[u8]> = leaves.iter().map(|l| l.as_slice()).collect();
    (merkle::simple_hash(sha256, &refs).unwrap(), merkle::simple_proof(sha256, &refs, index).unwrap())
}
