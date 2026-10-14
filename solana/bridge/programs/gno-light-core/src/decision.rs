//! A decision body's prefix (§13): `kind u8 ‖ sequence number u64 BE`, on which the admin program records before it
//! parses anything else.

/// A decision body's 9-byte prefix: kind, then the sequence number big-endian.
pub fn decision_prefix(body: &[u8]) -> Option<(u8, u64)> {
    if body.len() < 9 {
        return None;
    }
    let mut n = [0u8; 8];
    n.copy_from_slice(&body[1..9]);
    Some((body[0], u64::from_be_bytes(n)))
}
