//! tm2's simple merkle tree (RFC 6962 hashing: leaf `sha256(0x00‖x)`, inner `sha256(0x01‖l‖r)`, the split at the
//! largest power of two below n) and its `SimpleProof`, mirrored from tm2/pkg/crypto/merkle.

use crate::Error;

/// sha256 over the concatenation of the parts: the host passes a software hash, a program the `hashv` syscall.
pub type Hasher = fn(&[&[u8]]) -> [u8; 32];

pub fn leaf_hash(h: Hasher, leaf: &[u8]) -> [u8; 32] {
    h(&[&[0u8], leaf])
}

pub fn inner_hash(h: Hasher, left: &[u8; 32], right: &[u8; 32]) -> [u8; 32] {
    h(&[&[1u8], left, right])
}

/// The largest power of two strictly below n (n ≥ 2).
pub fn split_point(n: usize) -> usize {
    let mut k = 1;
    while k * 2 < n {
        k *= 2;
    }
    k
}

/// `SimpleHashFromByteSlices`: None for no items.
pub fn simple_hash(h: Hasher, items: &[&[u8]]) -> Option<[u8; 32]> {
    match items.len() {
        0 => None,
        1 => Some(leaf_hash(h, items[0])),
        n => {
            let k = split_point(n);
            let l = simple_hash(h, &items[..k])?;
            let r = simple_hash(h, &items[k..])?;
            Some(inner_hash(h, &l, &r))
        }
    }
}

/// tm2's `SimpleProof`: the leaf's position, its hash, and the sibling hashes from the leaf's sibling up to a child
/// of the root.
#[derive(Clone, Debug, PartialEq, Eq, borsh::BorshSerialize, borsh::BorshDeserialize)]
pub struct SimpleProof {
    pub total: u32,
    pub index: u32,
    pub leaf_hash: [u8; 32],
    pub aunts: Vec<[u8; 32]>,
}

/// The sibling directions on the path from the root down to `index`: true where the path goes left.
fn path(index: u32, total: u32) -> Result<Vec<bool>, Error> {
    if total == 0 || index >= total {
        return Err(Error::BadProof);
    }
    let (mut i, mut t) = (index as usize, total as usize);
    let mut dirs = Vec::new();
    while t > 1 {
        let k = split_point(t);
        if i < k {
            dirs.push(true);
            t = k;
        } else {
            dirs.push(false);
            i -= k;
            t -= k;
        }
    }
    Ok(dirs)
}

/// `computeHashFromAunts`: the root implied by the proof's leaf hash and aunts. The aunts run from the leaf's
/// sibling upward; the path is walked top-down and applied bottom-up, which is tm2's recursion without the stack.
pub fn root_from_proof(h: Hasher, proof: &SimpleProof) -> Result<[u8; 32], Error> {
    let dirs = path(proof.index, proof.total)?;
    if dirs.len() != proof.aunts.len() {
        return Err(Error::BadProof);
    }
    let mut hash = proof.leaf_hash;
    for (left, aunt) in dirs.iter().rev().zip(proof.aunts.iter()) {
        hash = if *left { inner_hash(h, &hash, aunt) } else { inner_hash(h, aunt, &hash) };
    }
    Ok(hash)
}

/// `SimpleProof.Verify`: the leaf's bytes hash to the proof's leaf hash and the proof's root is `root`.
pub fn verify_proof(h: Hasher, proof: &SimpleProof, root: &[u8; 32], leaf: &[u8]) -> Result<(), Error> {
    if leaf_hash(h, leaf) != proof.leaf_hash {
        return Err(Error::BadProof);
    }
    if root_from_proof(h, proof)? != *root {
        return Err(Error::BadProof);
    }
    Ok(())
}

/// The proof for one item of a list, as `SimpleProofsFromByteSlices` makes it (the tools and the tests).
pub fn simple_proof(h: Hasher, items: &[&[u8]], index: usize) -> Option<SimpleProof> {
    if index >= items.len() {
        return None;
    }
    let dirs = path(index as u32, items.len() as u32).ok()?;
    // Walk down again, collecting the sibling subtree hash at each level (top-down), then reverse.
    let mut aunts_top_down = Vec::with_capacity(dirs.len());
    let (mut lo, mut hi) = (0usize, items.len());
    for left in &dirs {
        let k = split_point(hi - lo);
        if *left {
            aunts_top_down.push(simple_hash(h, &items[lo + k..hi])?);
            hi = lo + k;
        } else {
            aunts_top_down.push(simple_hash(h, &items[lo..lo + k])?);
            lo += k;
        }
    }
    aunts_top_down.reverse();
    Some(SimpleProof {
        total: items.len() as u32,
        index: index as u32,
        leaf_hash: leaf_hash(h, items[index]),
        aunts: aunts_top_down,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sha256;

    #[test]
    fn split_points() {
        assert_eq!(split_point(2), 1);
        assert_eq!(split_point(3), 2);
        assert_eq!(split_point(4), 2);
        assert_eq!(split_point(5), 4);
        assert_eq!(split_point(16), 8);
        assert_eq!(split_point(17), 16);
    }

    #[test]
    fn proofs_round_trip() {
        for n in 1..=20usize {
            let items: Vec<Vec<u8>> = (0..n).map(|i| vec![i as u8; i + 1]).collect();
            let refs: Vec<&[u8]> = items.iter().map(|v| v.as_slice()).collect();
            let root = simple_hash(sha256, &refs).unwrap();
            for i in 0..n {
                let p = simple_proof(sha256, &refs, i).unwrap();
                verify_proof(sha256, &p, &root, &items[i]).unwrap();
                let mut bad = p.clone();
                bad.index = (i as u32 + 1) % n as u32;
                if n > 1 {
                    assert!(verify_proof(sha256, &bad, &root, &items[i]).is_err());
                }
                let mut bad = p.clone();
                bad.aunts.push([0u8; 32]);
                assert!(verify_proof(sha256, &bad, &root, &items[i]).is_err());
            }
        }
    }
}
