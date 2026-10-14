//! Hyperlane on Solana, as much as the bridge programs touch: the ISM and recipient interface discriminators (the
//! first 8 bytes of sha256 of the method's name), the simulated questions' return data (`SimulationReturnData`
//! with its trailing 0xff), the message, and the mailbox's and the token program's PDAs, instructions and account
//! layouts — from rust/sealevel at the mainnet builds' commit (bridge/sol/sol.go is the Go twin).

use borsh::{BorshDeserialize, BorshSerialize};
use solana_program::{
    instruction::{AccountMeta, Instruction},
    keccak,
    program_error::ProgramError,
    pubkey::Pubkey,
};
use solana_system_interface::program as system_program;

// ---- the ISM interface -------------------------------------------------------------------------------------------

/// `hyperlane-interchain-security-module:type`
pub const ISM_TYPE: [u8; 8] = [105, 97, 97, 88, 63, 124, 106, 18];
/// `hyperlane-interchain-security-module:verify`
pub const ISM_VERIFY: [u8; 8] = [243, 53, 214, 0, 208, 18, 231, 67];
/// `hyperlane-interchain-security-module:verify-account-metas`
pub const ISM_VERIFY_ACCOUNT_METAS: [u8; 8] = [200, 65, 157, 12, 89, 255, 131, 216];
/// `hyperlane-message-recipient:interchain-security-module`
pub const RECIPIENT_ISM: [u8; 8] = [45, 18, 245, 87, 234, 46, 246, 15];
/// `hyperlane-message-recipient:interchain-security-module-account-metas`
pub const RECIPIENT_ISM_ACCOUNT_METAS: [u8; 8] = [190, 214, 218, 129, 67, 97, 4, 76];
/// `hyperlane-message-recipient:handle`
pub const RECIPIENT_HANDLE: [u8; 8] = [33, 210, 5, 66, 196, 212, 239, 142];
/// `hyperlane-message-recipient:handle-account-metas`
pub const RECIPIENT_HANDLE_ACCOUNT_METAS: [u8; 8] = [194, 141, 30, 82, 241, 41, 169, 52];

/// The PDA a relayer passes to an ISM's `VerifyAccountMetas` (seeds only; the account need not exist).
pub const VERIFY_ACCOUNT_METAS_SEEDS: &[&[u8]] = &[b"hyperlane_ism", b"-", b"verify", b"-", b"account_metas"];
pub const RECIPIENT_ISM_ACCOUNT_METAS_SEEDS: &[&[u8]] = &[b"hyperlane_message_recipient", b"-", b"interchain_security_module", b"-", b"account_metas"];
pub const HANDLE_ACCOUNT_METAS_SEEDS: &[&[u8]] = &[b"hyperlane_message_recipient", b"-", b"handle", b"-", b"account_metas"];

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct VerifyInstruction {
    pub metadata: Vec<u8>,
    pub message: Vec<u8>,
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct HandleInstruction {
    pub origin: u32,
    pub sender: [u8; 32],
    pub message: Vec<u8>,
}

#[derive(BorshSerialize, BorshDeserialize, Clone, Debug, PartialEq, Eq)]
pub struct SerializableAccountMeta {
    pub pubkey: Pubkey,
    pub is_signer: bool,
    pub is_writable: bool,
}

impl From<AccountMeta> for SerializableAccountMeta {
    fn from(m: AccountMeta) -> Self {
        Self { pubkey: m.pubkey, is_signer: m.is_signer, is_writable: m.is_writable }
    }
}

/// Return data with a trailing non-zero byte, since a simulation's return data loses trailing zeros.
#[derive(BorshSerialize, BorshDeserialize, Clone, Debug)]
pub struct SimulationReturnData<T: BorshSerialize + BorshDeserialize> {
    pub return_data: T,
    pub trailing_byte: u8,
}

impl<T: BorshSerialize + BorshDeserialize> SimulationReturnData<T> {
    pub fn new(return_data: T) -> Self {
        Self { return_data, trailing_byte: u8::MAX }
    }
}

/// What an instruction's data is, by its first eight bytes.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum InterfaceCall {
    IsmType,
    IsmVerify(VerifyInstruction),
    IsmVerifyAccountMetas(VerifyInstruction),
    RecipientIsm,
    RecipientIsmAccountMetas,
    Handle(HandleInstruction),
    HandleAccountMetas(HandleInstruction),
}

/// Decodes a Hyperlane interface call, or None when the data is not one (then it is the program's own).
pub fn decode_interface(data: &[u8]) -> Result<Option<InterfaceCall>, ProgramError> {
    if data.len() < 8 {
        return Ok(None);
    }
    let (d, rest) = data.split_at(8);
    let d: [u8; 8] = d.try_into().unwrap();
    let verify = |rest: &[u8]| VerifyInstruction::try_from_slice(rest).map_err(|_| ProgramError::InvalidInstructionData);
    let handle = |rest: &[u8]| HandleInstruction::try_from_slice(rest).map_err(|_| ProgramError::InvalidInstructionData);
    Ok(Some(match d {
        ISM_TYPE => InterfaceCall::IsmType,
        ISM_VERIFY => InterfaceCall::IsmVerify(verify(rest)?),
        ISM_VERIFY_ACCOUNT_METAS => InterfaceCall::IsmVerifyAccountMetas(verify(rest)?),
        RECIPIENT_ISM => InterfaceCall::RecipientIsm,
        RECIPIENT_ISM_ACCOUNT_METAS => InterfaceCall::RecipientIsmAccountMetas,
        RECIPIENT_HANDLE => InterfaceCall::Handle(handle(rest)?),
        RECIPIENT_HANDLE_ACCOUNT_METAS => InterfaceCall::HandleAccountMetas(handle(rest)?),
        _ => return Ok(None),
    }))
}

pub fn encode_interface(discriminator: [u8; 8], body: &impl BorshSerialize) -> Vec<u8> {
    let mut v = discriminator.to_vec();
    body.serialize(&mut v).expect("borsh");
    v
}

pub fn return_data<T: BorshSerialize + BorshDeserialize>(value: T) -> Vec<u8> {
    borsh::to_vec(&SimulationReturnData::new(value)).expect("borsh")
}

// ---- the message ------------------------------------------------------------------------------------------------

pub const MESSAGE_VERSION: u8 = 3;
pub const MESSAGE_HEADER_LEN: usize = 77;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Message<'a> {
    pub version: u8,
    pub nonce: u32,
    pub origin: u32,
    pub sender: [u8; 32],
    pub destination: u32,
    pub recipient: [u8; 32],
    pub body: &'a [u8],
}

impl<'a> Message<'a> {
    pub fn parse(raw: &'a [u8]) -> Result<Message<'a>, ProgramError> {
        if raw.len() < MESSAGE_HEADER_LEN {
            return Err(ProgramError::InvalidInstructionData);
        }
        let u32_at = |i: usize| u32::from_be_bytes(raw[i..i + 4].try_into().unwrap());
        let arr = |i: usize| -> [u8; 32] { raw[i..i + 32].try_into().unwrap() };
        Ok(Message {
            version: raw[0],
            nonce: u32_at(1),
            origin: u32_at(5),
            sender: arr(9),
            destination: u32_at(41),
            recipient: arr(45),
            body: &raw[MESSAGE_HEADER_LEN..],
        })
    }

    /// The message id: keccak256 of the raw bytes.
    pub fn id(raw: &[u8]) -> [u8; 32] {
        keccak::hashv(&[raw]).to_bytes()
    }
}

/// The raw bytes of a message (the tests build messages with it).
pub fn encode_message(nonce: u32, origin: u32, sender: &[u8; 32], destination: u32, recipient: &[u8; 32], body: &[u8]) -> Vec<u8> {
    let mut v = Vec::with_capacity(MESSAGE_HEADER_LEN + body.len());
    v.push(MESSAGE_VERSION);
    v.extend_from_slice(&nonce.to_be_bytes());
    v.extend_from_slice(&origin.to_be_bytes());
    v.extend_from_slice(sender);
    v.extend_from_slice(&destination.to_be_bytes());
    v.extend_from_slice(recipient);
    v.extend_from_slice(body);
    v
}

// ---- the mailbox ------------------------------------------------------------------------------------------------

pub fn mailbox_inbox(mailbox: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[b"hyperlane", b"-", b"inbox"], mailbox).0
}

pub fn mailbox_outbox(mailbox: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[b"hyperlane", b"-", b"outbox"], mailbox).0
}

/// The PDA the mailbox signs a recipient's `Handle` with.
pub fn mailbox_process_authority(mailbox: &Pubkey, recipient: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[b"hyperlane", b"-", b"process_authority", b"-", recipient.as_ref()], mailbox).0
}

/// The per-ISM process authority: the PDA the newer mailbox source's `inbox_process` signs an ISM's `Verify`
/// with when the ISM names it among its `VerifyAccountMetas` (seeds `process_authority ‖ ism`: "so that ISMs
/// that require the Mailbox to co-sign (e.g. RateLimited) receive a proper signer account"). The mainnet build
/// this deployment uses signs with no such PDA (the suite showed it), so the pausable ISM binds its `Verify` to
/// the delivery through the Instructions sysvar instead; the derivation stays for a mailbox that has it.
pub fn mailbox_ism_process_authority(mailbox: &Pubkey, ism: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[b"process_authority", ism.as_ref()], mailbox).0
}

// ---- the token message ------------------------------------------------------------------------------------------

/// Hyperlane's token message (the body of a warp-route message): `recipient 32 ‖ amount u256 BE ‖ metadata`.
pub struct TokenMessage<'a> {
    pub recipient: [u8; 32],
    /// The amount, which must fit 64 bits (the top 24 bytes of the u256 zero).
    pub amount: u64,
    pub metadata: &'a [u8],
}

impl<'a> TokenMessage<'a> {
    /// None for a body shorter than 64 bytes or an amount past 64 bits.
    pub fn parse(body: &'a [u8]) -> Option<TokenMessage<'a>> {
        if body.len() < 64 || body[32..56].iter().any(|b| *b != 0) {
            return None;
        }
        let mut recipient = [0u8; 32];
        recipient.copy_from_slice(&body[..32]);
        Some(TokenMessage { recipient, amount: u64::from_be_bytes(body[56..64].try_into().unwrap()), metadata: &body[64..] })
    }
}

/// The bytes of a token message (the tests and the tools).
pub fn encode_token_message(recipient: &[u8; 32], amount: u64, metadata: &[u8]) -> Vec<u8> {
    let mut v = Vec::with_capacity(64 + metadata.len());
    v.extend_from_slice(recipient);
    v.extend_from_slice(&[0u8; 24]);
    v.extend_from_slice(&amount.to_be_bytes());
    v.extend_from_slice(metadata);
    v
}

pub fn mailbox_processed_message(mailbox: &Pubkey, id: &[u8; 32]) -> Pubkey {
    Pubkey::find_program_address(&[b"hyperlane", b"-", b"processed_message", b"-", id], mailbox).0
}

/// The mailbox's `Outbox` account (an `AccountData`: an initialized byte, then borsh): where the owner, the maximum
/// protocol fee and the fee config live.
pub struct Outbox {
    pub local_domain: u32,
    pub owner: Option<Pubkey>,
    pub max_protocol_fee: u64,
    pub fee: u64,
    pub beneficiary: Pubkey,
}

impl Outbox {
    pub fn parse(data: &[u8]) -> Result<Outbox, ProgramError> {
        use crate::pda::{read32, read_option_pubkey, read_u64};
        if data.first() != Some(&1) {
            return Err(ProgramError::UninitializedAccount);
        }
        let local_domain = u32::from_le_bytes(data.get(1..5).ok_or(ProgramError::InvalidAccountData)?.try_into().unwrap());
        let owner = read_option_pubkey(data, 6)?;
        // initialized 1 + local_domain 4 + bump 1 + the owner's Option tag (1 byte for None, 33 for Some), then
        // the tree: 32 × 32 branch + u64 count = 1032; the offset follows the tag, never an assumed owner
        let owner_len = if owner.is_some() { 33 } else { 1 };
        let at = 6 + owner_len + 1032;
        Ok(Outbox {
            local_domain,
            owner,
            max_protocol_fee: read_u64(data, at)?,
            fee: read_u64(data, at + 8)?,
            beneficiary: Pubkey::new_from_array(read32(data, at + 16)?),
        })
    }
}

pub struct Inbox {
    pub local_domain: u32,
    pub default_ism: Pubkey,
}

impl Inbox {
    pub fn parse(data: &[u8]) -> Result<Inbox, ProgramError> {
        if data.first() != Some(&1) {
            return Err(ProgramError::UninitializedAccount);
        }
        let local_domain = u32::from_le_bytes(data.get(1..5).ok_or(ProgramError::InvalidAccountData)?.try_into().unwrap());
        Ok(Inbox { local_domain, default_ism: Pubkey::new_from_array(crate::pda::read32(data, 6)?) })
    }
}

/// Mailbox instructions are borsh without a discriminator (instruction.rs): 2 InboxSetDefaultIsm, 9
/// TransferOwnership, 11 SetProtocolFeeConfig.
pub fn mailbox_set_default_ism(mailbox: &Pubkey, owner: &Pubkey, ism: &Pubkey) -> Instruction {
    let mut data = vec![2u8];
    data.extend_from_slice(ism.as_ref());
    Instruction {
        program_id: *mailbox,
        accounts: vec![AccountMeta::new(mailbox_inbox(mailbox), false), AccountMeta::new_readonly(mailbox_outbox(mailbox), false), AccountMeta::new(*owner, true)],
        data,
    }
}

pub fn mailbox_transfer_ownership(mailbox: &Pubkey, owner: &Pubkey, new_owner: Option<Pubkey>) -> Instruction {
    let mut data = vec![9u8];
    new_owner.serialize(&mut data).unwrap();
    Instruction { program_id: *mailbox, accounts: vec![AccountMeta::new(mailbox_outbox(mailbox), false), AccountMeta::new_readonly(*owner, true)], data }
}

pub fn mailbox_set_protocol_fee(mailbox: &Pubkey, owner: &Pubkey, fee: u64, beneficiary: &Pubkey) -> Instruction {
    let mut data = vec![11u8];
    data.extend_from_slice(&fee.to_le_bytes());
    data.extend_from_slice(beneficiary.as_ref());
    Instruction { program_id: *mailbox, accounts: vec![AccountMeta::new(mailbox_outbox(mailbox), false), AccountMeta::new_readonly(*owner, true)], data }
}

// ---- the token program (a warp route) ----------------------------------------------------------------------------

/// Token instructions carry the program discriminator `[1; 8]` then the borsh enum: 3 EnrollRemoteRouters, 5
/// SetInterchainSecurityModule, 7 TransferOwnership.
pub const PROGRAM_INSTRUCTION_DISCRIMINATOR: [u8; 8] = [1; 8];

/// The route's own account (also the PDA a handle-account-metas query names).
pub fn token_pda(token_program: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(HANDLE_ACCOUNT_METAS_SEEDS, token_program).0
}

/// The owner in a `HyperlaneToken` account: after the initialized byte, bump 1, mailbox 32, process authority 32,
/// dispatch bump 1, decimals 1, remote decimals 1.
pub const TOKEN_OWNER_OFFSET: usize = 1 + 1 + 32 + 32 + 1 + 1 + 1;

pub fn token_owner(data: &[u8]) -> Result<Option<Pubkey>, ProgramError> {
    if data.first() != Some(&1) {
        return Err(ProgramError::UninitializedAccount);
    }
    crate::pda::read_option_pubkey(data, TOKEN_OWNER_OFFSET)
}

/// The ISM pointer follows the owner.
pub fn token_ism(data: &[u8]) -> Result<Option<Pubkey>, ProgramError> {
    let owner_len = match data.get(TOKEN_OWNER_OFFSET) {
        Some(0) => 1,
        Some(1) => 33,
        _ => return Err(ProgramError::InvalidAccountData),
    };
    crate::pda::read_option_pubkey(data, TOKEN_OWNER_OFFSET + owner_len)
}

pub fn token_set_ism(token_program: &Pubkey, owner: &Pubkey, ism: Option<Pubkey>) -> Instruction {
    let mut data = PROGRAM_INSTRUCTION_DISCRIMINATOR.to_vec();
    data.push(5);
    ism.serialize(&mut data).unwrap();
    Instruction { program_id: *token_program, accounts: vec![AccountMeta::new(token_pda(token_program), false), AccountMeta::new_readonly(*owner, true)], data }
}

pub fn token_enroll_remote_router(token_program: &Pubkey, owner: &Pubkey, domain: u32, router: Option<[u8; 32]>) -> Instruction {
    let mut data = PROGRAM_INSTRUCTION_DISCRIMINATOR.to_vec();
    data.push(3);
    data.extend_from_slice(&1u32.to_le_bytes());
    data.extend_from_slice(&domain.to_le_bytes());
    router.serialize(&mut data).unwrap();
    Instruction {
        program_id: *token_program,
        accounts: vec![AccountMeta::new_readonly(system_program::ID, false), AccountMeta::new(token_pda(token_program), false), AccountMeta::new(*owner, true)],
        data,
    }
}

pub fn token_transfer_ownership(token_program: &Pubkey, owner: &Pubkey, new_owner: Option<Pubkey>) -> Instruction {
    let mut data = PROGRAM_INSTRUCTION_DISCRIMINATOR.to_vec();
    data.push(7);
    new_owner.serialize(&mut data).unwrap();
    Instruction { program_id: *token_program, accounts: vec![AccountMeta::new(token_pda(token_program), false), AccountMeta::new_readonly(*owner, true)], data }
}

#[cfg(test)]
mod tests {
    use super::*;
    use solana_program::hash::hashv;

    #[test]
    fn discriminators() {
        let d = |s: &str| hashv(&[s.as_bytes()]).to_bytes()[..8].to_vec();
        assert_eq!(d("hyperlane-interchain-security-module:type"), ISM_TYPE);
        assert_eq!(d("hyperlane-interchain-security-module:verify"), ISM_VERIFY);
        assert_eq!(d("hyperlane-interchain-security-module:verify-account-metas"), ISM_VERIFY_ACCOUNT_METAS);
        assert_eq!(d("hyperlane-message-recipient:interchain-security-module"), RECIPIENT_ISM);
        assert_eq!(d("hyperlane-message-recipient:interchain-security-module-account-metas"), RECIPIENT_ISM_ACCOUNT_METAS);
        assert_eq!(d("hyperlane-message-recipient:handle"), RECIPIENT_HANDLE);
        assert_eq!(d("hyperlane-message-recipient:handle-account-metas"), RECIPIENT_HANDLE_ACCOUNT_METAS);
    }

    #[test]
    fn message_round_trip() {
        let raw = encode_message(7, 1198419788, &[1; 32], 1399811149, &[2; 32], b"body");
        let m = Message::parse(&raw).unwrap();
        assert_eq!((m.version, m.nonce, m.origin, m.destination), (3, 7, 1198419788, 1399811149));
        assert_eq!(m.body, b"body");
        assert_eq!(Message::id(&raw), keccak::hashv(&[&raw]).to_bytes());
    }

    /// An `Outbox` account as Hyperlane writes it: initialized 1, local_domain u32, bump, owner Option<Pubkey>,
    /// the tree (32 × 32 branch, u64 count), max_protocol_fee, then the fee config (fee u64, beneficiary).
    fn outbox_bytes(owner: Option<Pubkey>, max_fee: u64, fee: u64, beneficiary: Pubkey) -> Vec<u8> {
        let mut d = vec![1u8];
        d.extend_from_slice(&1399811149u32.to_le_bytes());
        d.push(255);
        owner.serialize(&mut d).unwrap();
        d.extend_from_slice(&[0xaa; 1024]);
        d.extend_from_slice(&7u64.to_le_bytes());
        d.extend_from_slice(&max_fee.to_le_bytes());
        d.extend_from_slice(&fee.to_le_bytes());
        d.extend_from_slice(beneficiary.as_ref());
        d
    }

    #[test]
    fn outbox_parses_with_and_without_an_owner() {
        let b = Pubkey::new_from_array([9; 32]);
        let with = Outbox::parse(&outbox_bytes(Some(Pubkey::new_from_array([3; 32])), 50_000_000, 500, b)).unwrap();
        assert_eq!((with.local_domain, with.owner, with.max_protocol_fee, with.fee, with.beneficiary), (1399811149, Some(Pubkey::new_from_array([3; 32])), 50_000_000, 500, b));
        // an owner-less outbox writes one byte for the owner: the tree and the fees sit 32 bytes earlier
        let without = Outbox::parse(&outbox_bytes(None, 50_000_000, 500, b)).unwrap();
        assert_eq!((without.owner, without.max_protocol_fee, without.fee, without.beneficiary), (None, 50_000_000, 500, b));
        // a short account is a refusal, never a read past the end
        assert!(Outbox::parse(&outbox_bytes(None, 1, 2, b)[..1000]).is_err());
    }

    #[test]
    fn token_message_round_trip() {
        let raw = encode_token_message(&[4; 32], 25_000_000_000, b"meta");
        assert_eq!(raw.len(), 68);
        let t = TokenMessage::parse(&raw).unwrap();
        assert_eq!((t.recipient, t.amount, t.metadata), ([4; 32], 25_000_000_000, &b"meta"[..]));
        assert!(TokenMessage::parse(&raw[..63]).is_none(), "shorter than 64 bytes");
        let mut big = raw.clone();
        big[40] = 1; // a bit past 64 bits
        assert!(TokenMessage::parse(&big).is_none(), "an amount past 64 bits");
        let empty = encode_token_message(&[4; 32], 0, b"");
        assert_eq!(TokenMessage::parse(&empty).unwrap().amount, 0);
    }
}
