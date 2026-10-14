/// Every way the core refuses. The programs map these onto their own error codes (`Error as u32`).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u32)]
pub enum Error {
    /// A header field is out of its range (a negative height, a hash of the wrong length, a chain id too long).
    BadHeader = 1,
    /// The commit's shape is wrong: precommits beyond the set's length, a BlockID that is not the header's, a part
    /// set Total of zero.
    BadCommit = 2,
    /// The validator set is empty, bigger than 255, unsorted, has a duplicate address or an impossible power.
    BadValidatorSet = 3,
    /// A SimpleProof's index, total or aunts do not fit together.
    BadProof = 4,
    /// The results leaf does not parse as an ABCIResult.
    BadLeaf = 5,
    /// The leaf carries an error, or no Dispatch event of the mailbox realm.
    NoDispatch = 6,
    /// A Dispatch event's attributes are not the seven of §13 in order, or do not decode.
    BadDispatch = 7,
    /// Arithmetic left i64.
    Overflow = 8,
    /// Amino bytes do not decode.
    Encoding = 9,
}

impl core::fmt::Display for Error {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        write!(f, "{:?}", self)
    }
}

impl std::error::Error for Error {}
