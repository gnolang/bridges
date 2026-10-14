//! The upgradeable loader (BPFLoaderUpgradeab1e11111111111111111111111): its account layouts (bincode: a u32
//! enum tag, then the fields) and the instructions the admin program sends it, encoded by hand so the programs
//! carry no serde (solana-loader-v3-interface 6.1 is the reference; the tests use it directly).
//!
//! Program account: tag 2, then the programdata address (36 bytes). ProgramData: tag 3, slot u64, `Option<Pubkey>`
//! upgrade authority (45 bytes), then the ELF. Buffer: tag 1, `Option<Pubkey>` authority (37 bytes), then the ELF.

use solana_program::{
    instruction::{AccountMeta, Instruction},
    program_error::ProgramError,
    pubkey,
    pubkey::Pubkey,
    sysvar,
};
use solana_system_interface::program as system_program;

pub const LOADER_ID: Pubkey = pubkey!("BPFLoaderUpgradeab1e11111111111111111111111");
/// The least an `ExtendProgram` may ask for (SIMD-0431), and the most a CPI may grow an account by in one
/// instruction. A kind 8's growth is one extension of the whole excess, at or above it, sent top-level by the
/// executor (the loader takes one extension of a program a block); the suite sends it in steps of this size to
/// show that rule.
pub const EXTEND_STEP: u32 = 10_240;
pub const PROGRAM_LEN: usize = 36;
pub const PROGRAMDATA_HEADER: usize = 45;
pub const BUFFER_HEADER: usize = 37;

pub fn programdata_address(program: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(&[program.as_ref()], &LOADER_ID).0
}

fn option_pubkey(data: &[u8], at: usize) -> Result<Option<Pubkey>, ProgramError> {
    crate::pda::read_option_pubkey(data, at)
}

/// A program account's programdata address.
pub fn parse_program(data: &[u8]) -> Result<Pubkey, ProgramError> {
    if data.len() < PROGRAM_LEN || data[..4] != [2, 0, 0, 0] {
        return Err(ProgramError::InvalidAccountData);
    }
    Ok(Pubkey::new_from_array(crate::pda::read32(data, 4)?))
}

/// A programdata account's (slot, upgrade authority).
pub fn parse_programdata(data: &[u8]) -> Result<(u64, Option<Pubkey>), ProgramError> {
    if data.len() < PROGRAMDATA_HEADER || data[..4] != [3, 0, 0, 0] {
        return Err(ProgramError::InvalidAccountData);
    }
    Ok((crate::pda::read_u64(data, 4)?, option_pubkey(data, 12)?))
}

/// A buffer account's authority; the ELF follows the header.
pub fn parse_buffer(data: &[u8]) -> Result<Option<Pubkey>, ProgramError> {
    if data.len() < BUFFER_HEADER || data[..4] != [1, 0, 0, 0] {
        return Err(ProgramError::InvalidAccountData);
    }
    option_pubkey(data, 4)
}

/// `SetAuthority` (tag 4) on a programdata or a buffer: the current authority signs; the new one need not.
pub fn set_authority(target: &Pubkey, current: &Pubkey, new: Option<&Pubkey>) -> Instruction {
    let mut accounts = vec![AccountMeta::new(*target, false), AccountMeta::new_readonly(*current, true)];
    if let Some(n) = new {
        accounts.push(AccountMeta::new_readonly(*n, false));
    }
    Instruction { program_id: LOADER_ID, accounts, data: 4u32.to_le_bytes().to_vec() }
}

/// `SetAuthorityChecked` (tag 7): both the current and the new authority sign.
pub fn set_authority_checked(target: &Pubkey, current: &Pubkey, new: &Pubkey) -> Instruction {
    Instruction {
        program_id: LOADER_ID,
        accounts: vec![AccountMeta::new(*target, false), AccountMeta::new_readonly(*current, true), AccountMeta::new_readonly(*new, true)],
        data: 7u32.to_le_bytes().to_vec(),
    }
}

/// `Upgrade` (tag 3): programdata, program, buffer, spill, rent, clock, authority.
pub fn upgrade(program: &Pubkey, buffer: &Pubkey, authority: &Pubkey, spill: &Pubkey) -> Instruction {
    Instruction {
        program_id: LOADER_ID,
        accounts: vec![
            AccountMeta::new(programdata_address(program), false),
            AccountMeta::new(*program, false),
            AccountMeta::new(*buffer, false),
            AccountMeta::new(*spill, false),
            AccountMeta::new_readonly(sysvar::rent::ID, false),
            AccountMeta::new_readonly(sysvar::clock::ID, false),
            AccountMeta::new_readonly(*authority, true),
        ],
        data: 3u32.to_le_bytes().to_vec(),
    }
}

/// `ExtendProgram` (tag 6, u32 additional bytes): programdata (w), program (w), the system program, the payer
/// (signer, w) — permissionless, no authority signs, the payer funds the added rent. This is the form mainnet's
/// loader has (agave v4.3.0 handles tags 0–7): `ExtendProgramChecked` (tag 9) was behind
/// `enable_extend_program_checked`, abandoned with SIMD-0164, and the feature account is absent on mainnet,
/// testnet and devnet, though program-test still carries it (the audit's F1; the suite deactivates it).
pub fn extend_program(program: &Pubkey, payer: &Pubkey, additional_bytes: u32) -> Instruction {
    let mut data = 6u32.to_le_bytes().to_vec();
    data.extend_from_slice(&additional_bytes.to_le_bytes());
    Instruction {
        program_id: LOADER_ID,
        accounts: vec![
            AccountMeta::new(programdata_address(program), false),
            AccountMeta::new(*program, false),
            AccountMeta::new_readonly(system_program::ID, false),
            AccountMeta::new(*payer, true),
        ],
        data,
    }
}

/// `Close` (tag 5) of a buffer: the buffer, the recipient of its lamports, the authority.
pub fn close_buffer(buffer: &Pubkey, recipient: &Pubkey, authority: &Pubkey) -> Instruction {
    Instruction {
        program_id: LOADER_ID,
        accounts: vec![AccountMeta::new(*buffer, false), AccountMeta::new(*recipient, false), AccountMeta::new_readonly(*authority, true)],
        data: 5u32.to_le_bytes().to_vec(),
    }
}

/// What each of our programs' `init` demands (§5.8): `program` is this program's account under the loader, its
/// programdata names `authority` as the upgrade authority, and `authority` signed.
pub fn require_upgrade_authority(program_id: &Pubkey, program: &solana_program::account_info::AccountInfo, programdata: &solana_program::account_info::AccountInfo, authority: &solana_program::account_info::AccountInfo) -> Result<(), ProgramError> {
    if program.key != program_id || program.owner != &LOADER_ID {
        return Err(ProgramError::IncorrectProgramId);
    }
    let pd_key = parse_program(&program.try_borrow_data()?)?;
    if programdata.key != &pd_key || programdata.owner != &LOADER_ID {
        return Err(ProgramError::InvalidAccountData);
    }
    let (_, upgrade_authority) = parse_programdata(&programdata.try_borrow_data()?)?;
    if upgrade_authority != Some(*authority.key) || !authority.is_signer {
        return Err(ProgramError::MissingRequiredSignature);
    }
    Ok(())
}

/// The ELF sha256 the plan's kind 8 names: over the buffer's bytes after the loader's header.
pub fn buffer_elf(data: &[u8]) -> Result<&[u8], ProgramError> {
    parse_buffer(data)?;
    Ok(&data[BUFFER_HEADER..])
}
