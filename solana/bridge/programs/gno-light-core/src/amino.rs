//! The slice of amino (tm2's wire encoding, proto3-compatible) the light client needs, mirrored from
//! tm2/pkg/amino/binary_encode.go (gno v1.2.0):
//!
//! - a field key is `uvarint(field << 3 | typ3)`: typ3 0 varint, 1 eight bytes (`fixed64`), 2 length-prefixed;
//! - a struct's zero-valued non-struct fields are omitted; a nested struct whose encoding is empty is omitted;
//! - `int64` and Go `int` are zigzag varints (`EncodeVarint`) unless tagged `fixed64`; `uint32`, `uint64` and a
//!   byte are plain uvarints; a bool is the varint 1 (false is omitted as a zero value);
//! - strings and byte slices are length-prefixed; a `[32]byte` array likewise;
//! - a registered interface value is a `google.protobuf.Any`: field 1 the type URL, field 2 the value, omitted when
//!   empty;
//! - a list of length-prefixed elements is unpacked: each element repeats the parent's field key;
//! - `time.Time` is `{1: uvarint(seconds), 2: uvarint(nanos)}`, each omitted when zero;
//! - a top-level value that is not a struct is wrapped as field 1 of an implicit struct (`amino.Marshal`), and
//!   `amino.MarshalSized` prefixes the whole with its uvarint length.

pub const TYP3_VARINT: u8 = 0;
pub const TYP3_8BYTE: u8 = 1;
pub const TYP3_BYTELEN: u8 = 2;
pub const TYP3_4BYTE: u8 = 5;

pub fn put_uvarint(out: &mut Vec<u8>, mut v: u64) {
    loop {
        let b = (v & 0x7f) as u8;
        v >>= 7;
        if v == 0 {
            out.push(b);
            return;
        }
        out.push(b | 0x80);
    }
}

/// Go's `binary.PutVarint`: zigzag, then uvarint.
pub fn put_zigzag(out: &mut Vec<u8>, v: i64) {
    put_uvarint(out, ((v << 1) ^ (v >> 63)) as u64)
}

pub fn put_key(out: &mut Vec<u8>, field: u32, typ3: u8) {
    put_uvarint(out, ((field as u64) << 3) | typ3 as u64)
}

/// A length-prefixed field (a string, bytes, a nested struct's bytes, an Any), omitted when empty.
pub fn put_bytes_field(out: &mut Vec<u8>, field: u32, b: &[u8]) {
    if b.is_empty() {
        return;
    }
    put_key(out, field, TYP3_BYTELEN);
    put_uvarint(out, b.len() as u64);
    out.extend_from_slice(b);
}

/// A plain uvarint field (uint32, uint64, a byte), omitted when zero.
pub fn put_uvarint_field(out: &mut Vec<u8>, field: u32, v: u64) {
    if v == 0 {
        return;
    }
    put_key(out, field, TYP3_VARINT);
    put_uvarint(out, v);
}

/// A zigzag varint field (int64, int), omitted when zero.
pub fn put_zigzag_field(out: &mut Vec<u8>, field: u32, v: i64) {
    if v == 0 {
        return;
    }
    put_key(out, field, TYP3_VARINT);
    put_zigzag(out, v);
}

/// An eight-byte little-endian field (`binary:"fixed64"`), omitted when zero.
pub fn put_fixed64_field(out: &mut Vec<u8>, field: u32, v: u64) {
    if v == 0 {
        return;
    }
    put_key(out, field, TYP3_8BYTE);
    out.extend_from_slice(&v.to_le_bytes());
}

/// `time.Time`'s own fields: seconds then nanoseconds, each a uvarint, each omitted when zero.
pub fn time_bare(secs: i64, nanos: i32) -> Vec<u8> {
    let mut out = Vec::with_capacity(12);
    put_uvarint_field(&mut out, 1, secs as u64);
    put_uvarint_field(&mut out, 2, nanos as u64);
    out
}

/// `amino.MarshalSized`: the uvarint length, then the bytes.
pub fn sized(body: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(body.len() + 3);
    put_uvarint(&mut out, body.len() as u64);
    out.extend_from_slice(body);
    out
}

/// A `google.protobuf.Any`: the type URL as field 1, the value as field 2 (omitted when empty).
pub fn any(type_url: &str, value: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(type_url.len() + value.len() + 6);
    put_bytes_field(&mut out, 1, type_url.as_bytes());
    put_bytes_field(&mut out, 2, value);
    out
}

// ------------------------------------------------------------------ decoding --

use crate::Error;

pub fn read_uvarint(buf: &[u8], pos: &mut usize) -> Result<u64, Error> {
    let mut v: u64 = 0;
    let mut shift = 0u32;
    loop {
        let b = *buf.get(*pos).ok_or(Error::Encoding)?;
        *pos += 1;
        if shift >= 64 {
            return Err(Error::Encoding);
        }
        v |= ((b & 0x7f) as u64) << shift;
        if b & 0x80 == 0 {
            return Ok(v);
        }
        shift += 7;
    }
}

pub fn read_zigzag(buf: &[u8], pos: &mut usize) -> Result<i64, Error> {
    let u = read_uvarint(buf, pos)?;
    Ok(((u >> 1) as i64) ^ -((u & 1) as i64))
}

/// A field key: (field number, typ3).
pub fn read_key(buf: &[u8], pos: &mut usize) -> Result<(u32, u8), Error> {
    let k = read_uvarint(buf, pos)?;
    let field = (k >> 3) as u32;
    if field == 0 || k >> 3 > u32::MAX as u64 {
        return Err(Error::Encoding);
    }
    Ok((field, (k & 7) as u8))
}

/// A length-prefixed value.
pub fn read_bytes<'a>(buf: &'a [u8], pos: &mut usize) -> Result<&'a [u8], Error> {
    let n = read_uvarint(buf, pos)? as usize;
    let end = pos.checked_add(n).ok_or(Error::Encoding)?;
    let b = buf.get(*pos..end).ok_or(Error::Encoding)?;
    *pos = end;
    Ok(b)
}

/// Skips a value of the given typ3 (for fields the reader does not know).
pub fn skip_value(buf: &[u8], pos: &mut usize, typ3: u8) -> Result<(), Error> {
    match typ3 {
        TYP3_VARINT => {
            read_uvarint(buf, pos)?;
        }
        TYP3_8BYTE => *pos = pos.checked_add(8).filter(|e| *e <= buf.len()).ok_or(Error::Encoding)?,
        TYP3_BYTELEN => {
            read_bytes(buf, pos)?;
        }
        TYP3_4BYTE => *pos = pos.checked_add(4).filter(|e| *e <= buf.len()).ok_or(Error::Encoding)?,
        _ => return Err(Error::Encoding),
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn varints() {
        let mut o = vec![];
        put_uvarint(&mut o, 300);
        assert_eq!(o, [0xac, 0x02]);
        let mut o = vec![];
        put_zigzag(&mut o, 1);
        assert_eq!(o, [0x02]);
        let mut o = vec![];
        put_zigzag(&mut o, -1);
        assert_eq!(o, [0x01]);
        let mut o = vec![];
        put_zigzag(&mut o, 60);
        assert_eq!(o, [0x78]);
        let mut p = 0;
        assert_eq!(read_uvarint(&[0xac, 0x02], &mut p).unwrap(), 300);
        let mut p = 0;
        assert_eq!(read_zigzag(&[0x01], &mut p).unwrap(), -1);
        let mut p = 0;
        assert_eq!(read_zigzag(&[0x78], &mut p).unwrap(), 60);
    }

    #[test]
    fn keys_and_fields() {
        let mut o = vec![];
        put_key(&mut o, 6, TYP3_BYTELEN);
        assert_eq!(o, [0x32]);
        let mut o = vec![];
        put_fixed64_field(&mut o, 2, 0x0ab5a6);
        assert_eq!(o, [0x11, 0xa6, 0xb5, 0x0a, 0, 0, 0, 0, 0]);
        let mut o = vec![];
        put_bytes_field(&mut o, 1, b"");
        assert!(o.is_empty());
        assert_eq!(time_bare(0, 0), Vec::<u8>::new());
    }
}
