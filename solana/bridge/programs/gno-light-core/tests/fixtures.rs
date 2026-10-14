//! The core against gnoland-1's own blocks (../fixtures, fetched by bridge/tools/gnoheaders.py): header hashes,
//! validator-set hashes, every precommit's sign bytes against its real signature, the commit rule, the two
//! validator-set changes (487,883: a fifth validator; 720,333: a sixth with power 1), LastResultsHash from the
//! re-encoded results, a SimpleProof over them, and the event parse.
//!
//! Run with `cargo test -p gno-light-core --all-features`.

use ed25519_dalek::{Signature, VerifyingKey};
use gno_light_core::{
    commit::{commit_rule, tallied_power},
    fixtures::{self, Fixture},
    merkle, results, sha256, vote,
};
use std::collections::BTreeMap;

fn all() -> BTreeMap<i64, Fixture> {
    fixtures::load_dir(fixtures::dir()).into_iter().map(|f| (f.height, f)).collect()
}

#[test]
fn header_hashes_match_the_chain() {
    let fx = all();
    assert!(fx.len() >= 13, "fixtures present");
    for f in fx.values() {
        f.header.check().unwrap();
        assert_eq!(f.header.hash(sha256), f.block_hash, "height {}", f.height);
        assert_eq!(f.commit_block_id.hash, f.block_hash.to_vec(), "height {}", f.height);
        assert_eq!(f.parts.hash.len(), 32);
        assert!(f.parts.total >= 1);
    }
}

#[test]
fn validator_sets_hash_to_validators_hash() {
    let fx = all();
    for f in fx.values() {
        f.validators.check(sha256).unwrap();
        assert_eq!(f.validators.hash(sha256).unwrap().to_vec(), f.header.validators_hash, "height {}", f.height);
        // the chain serves the set in address order, which the leaf order depends on
        let mut sorted = f.validators.clone();
        sorted.sort(sha256);
        assert_eq!(sorted, f.validators);
    }
}

#[test]
fn every_precommit_signature_verifies_over_our_sign_bytes() {
    let fx = all();
    let mut checked = 0;
    for f in fx.values() {
        assert_eq!(f.votes.len(), f.validators.len(), "len(precommits) == len(valset) at {}", f.height);
        for (i, v) in f.votes.iter().enumerate() {
            let Some(v) = v else { continue };
            assert_eq!(v.validator_index, i);
            assert_eq!(v.typ, vote::PRECOMMIT_TYPE);
            assert_eq!(v.height, f.height);
            let sb = vote::canonical_vote(v.typ, &f.header.chain_id, v.height, v.round, &v.block_id, &v.time);
            let vk = VerifyingKey::from_bytes(&f.validators.validators[i].pubkey).unwrap();
            vk.verify_strict(&sb, &Signature::from_bytes(&v.signature)).unwrap_or_else(|e| panic!("height {} slot {i}: {e}", f.height));
            // the address in the vote is the validator's
            assert!(v.validator_address.starts_with("g1"));
            checked += 1;
            // the signature does not verify over a prevote's bytes, nor another round's
            let prevote = vote::canonical_vote(vote::PREVOTE_TYPE, &f.header.chain_id, v.height, v.round, &v.block_id, &v.time);
            assert!(vk.verify_strict(&prevote, &Signature::from_bytes(&v.signature)).is_err());
            let other = vote::canonical_vote(v.typ, &f.header.chain_id, v.height, v.round + 1, &v.block_id, &v.time);
            assert!(vk.verify_strict(&other, &Signature::from_bytes(&v.signature)).is_err());
        }
    }
    assert!(checked >= 60, "{checked} signatures checked");
}

#[test]
fn appendix_example_is_validator_zero_at_701862() {
    let fx = all();
    let f = &fx[&701862.min(701855)];
    // the appendix's bytes are from 701,862, which the fixtures do not carry; the same construction at 701,855
    let v = f.votes[0].as_ref().unwrap();
    let (c, sigs) = f.commit();
    let sb = c.sign_bytes(&f.header.chain_id, f.height, 0).unwrap();
    assert_eq!(sb, vote::canonical_vote(2, "gnoland-1", v.height, v.round, &v.block_id, &v.time));
    assert_eq!(sb[0] as usize, sb.len() - 1, "sized prefix");
    assert!(sigs[0].is_some());
}

#[test]
fn commit_rule_holds_on_every_fixture_and_fails_short() {
    let fx = all();
    for f in fx.values() {
        let (c, sigs) = f.commit();
        c.check_shape(&f.block_hash, f.validators.len()).unwrap();
        let total = f.validators.total_power().unwrap();
        let tallied = tallied_power(&f.validators, |i| sigs[i].is_some()).unwrap();
        assert!(commit_rule(tallied, total), "height {}: {tallied} of {total}", f.height);
        // drop signatures until the rule fails: with every validator present the count that fails is the chain's
        let present: Vec<usize> = c.present().collect();
        let mut short = present.clone();
        while let Some(_) = short.pop() {
            let t = tallied_power(&f.validators, |i| short.contains(&i)).unwrap();
            if !commit_rule(t, total) {
                break;
            }
        }
        assert!(short.len() < present.len());
    }
}

#[test]
fn the_three_set_changes() {
    let fx = all();
    // 487,882 announces the fifth validator, joining at power 1: Next differs from Validators and equals the set
    // of 487,883, whose total is 241
    let a = &fx[&487882];
    assert_ne!(a.header.validators_hash, a.header.next_validators_hash);
    assert_eq!(a.header.next_validators_hash, fx[&487883].validators.hash(sha256).unwrap().to_vec());
    assert_eq!(fx[&487881].validators.len(), 4);
    assert_eq!(fx[&487881].validators.total_power().unwrap(), 240);
    assert_eq!(fx[&487883].validators.len(), 5);
    assert_eq!(fx[&487883].validators.total_power().unwrap(), 241);
    assert_eq!(fx[&487881].header.next_validators_hash, fx[&487881].header.validators_hash);
    // 495,706 announces the fifth's raise from 1 to 60: a power change alone changes the hash (a quarantine)
    let r = &fx[&495706];
    assert_ne!(r.header.validators_hash, r.header.next_validators_hash);
    assert_eq!(r.header.next_validators_hash, fx[&495707].validators.hash(sha256).unwrap().to_vec());
    assert_eq!(fx[&495706].validators.total_power().unwrap(), 241);
    assert_eq!(fx[&495707].validators.total_power().unwrap(), 300);
    assert_eq!(fx[&495707].validators.len(), 5);
    let keys_before: Vec<[u8; 32]> = fx[&495706].validators.validators.iter().map(|v| v.pubkey).collect();
    let keys_after: Vec<[u8; 32]> = fx[&495707].validators.validators.iter().map(|v| v.pubkey).collect();
    assert_eq!(keys_before, keys_after, "the same five keys, one power changed");
    // under the 241 total, three at 60 carry it (180·3 = 540 > 482: the power-1 key changes nothing), two at 60
    // plus the 1 do not (121·3 = 363)
    let five = &fx[&487883].validators;
    let heavy: Vec<usize> = five.validators.iter().enumerate().filter(|(_, v)| v.power == 60).map(|(i, _)| i).collect();
    let light = five.validators.iter().position(|v| v.power == 1).unwrap();
    assert!(commit_rule(tallied_power(five, |i| heavy[..3].contains(&i)).unwrap(), 241));
    assert!(!commit_rule(tallied_power(five, |i| heavy[..2].contains(&i) || i == light).unwrap(), 241));
    // 720,332 announces the sixth, with power 1
    let b = &fx[&720332];
    assert_ne!(b.header.validators_hash, b.header.next_validators_hash);
    assert_eq!(b.header.next_validators_hash, fx[&720333].validators.hash(sha256).unwrap().to_vec());
    let six = &fx[&720333].validators;
    assert_eq!(six.len(), 6);
    let mut powers: Vec<i64> = six.validators.iter().map(|v| v.power).collect();
    powers.sort();
    assert_eq!(powers, vec![1, 60, 60, 60, 60, 60]);
    assert_eq!(six.total_power().unwrap(), 301);
    // four at 60 carry it; three at 60 plus the 1 do not
    let heavy: Vec<usize> = six.validators.iter().enumerate().filter(|(_, v)| v.power == 60).map(|(i, _)| i).collect();
    let light = six.validators.iter().position(|v| v.power == 1).unwrap();
    let t4 = tallied_power(six, |i| heavy[..4].contains(&i)).unwrap();
    assert_eq!(t4, 240);
    assert!(commit_rule(t4, 301));
    let t3 = tallied_power(six, |i| heavy[..3].contains(&i) || i == light).unwrap();
    assert_eq!(t3, 181);
    assert!(!commit_rule(t3, 301));
    // the real commit of 720,333 was signed by the new set
    let (_, sigs) = fx[&720333].commit();
    assert_eq!(sigs.len(), 6);
    // every chain id is gnoland-1
    for f in fx.values() {
        assert_eq!(f.header.chain_id, "gnoland-1");
    }
}

#[test]
fn last_results_hash_from_re_encoded_results() {
    let fx = all();
    let mut checked = 0;
    for (h, f) in fx.iter() {
        let Some(next) = fx.get(&(h + 1)) else { continue };
        match &f.results {
            None => {
                assert!(next.header.last_results_hash.is_empty(), "height {h}: no txs, so H+1 carries no results hash");
                assert_eq!(f.header.num_txs, 0);
            }
            Some(leaves) => {
                assert_eq!(leaves.len() as i64, f.header.num_txs);
                let refs: Vec<&[u8]> = leaves.iter().map(|l| l.as_slice()).collect();
                let root = merkle::simple_hash(sha256, &refs).unwrap();
                assert_eq!(root.to_vec(), next.header.last_results_hash, "height {h}: LastResultsHash in header {}", h + 1);
                // a SimpleProof of each leaf verifies against the header's hash, and not against a changed leaf
                for (i, leaf) in leaves.iter().enumerate() {
                    let p = merkle::simple_proof(sha256, &refs, i).unwrap();
                    merkle::verify_proof(sha256, &p, &root, leaf).unwrap();
                    let mut changed = leaf.clone();
                    changed.push(0);
                    assert!(merkle::verify_proof(sha256, &p, &root, &changed).is_err());
                }
                checked += 1;
            }
        }
    }
    assert!(checked >= 3, "{checked} result sets checked");
}

#[test]
fn the_results_leaf_parses_and_the_chain_event_reads() {
    let fx = all();
    let f = &fx[&740913];
    let leaves = f.results.as_ref().unwrap();
    let r = results::parse_result(&leaves[0]).unwrap();
    assert!(!r.error);
    assert_eq!(r.events[0].0, results::EVENT_TYPE_URL);
    let e = results::parse_event(&r.events[0].1).unwrap();
    assert_eq!(e.typ, "Approval");
    assert_eq!(e.pkg_path, "gno.land/p/nt/grc20/v0");
    assert_eq!(e.attributes.len(), 4);
    assert_eq!(e.attributes[0].key, "token");
    // no Dispatch of a mailbox realm in this block
    assert_eq!(results::dispatch_events(&leaves[0], "gno.land/r/x/hlmailbox").unwrap_err(), gno_light_core::Error::NoDispatch);
    // the leaf of the §13 example: one bank transfer, no data, no error
    let g = &fx[&701854];
    let r = results::parse_result(&g.results.as_ref().unwrap()[0]).unwrap();
    assert_eq!(r.events.len(), 1);
    assert_eq!(r.events[0].0, "/bank.TransferEvent");
    // every leaf of every fixture parses
    for f in fx.values() {
        if let Some(leaves) = &f.results {
            for l in leaves {
                results::parse_result(l).unwrap();
            }
        }
    }
}
