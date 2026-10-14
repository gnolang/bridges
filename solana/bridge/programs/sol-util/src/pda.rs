//! Program accounts: creating a PDA (robust to a pre-funded address), growing one, closing one.

use solana_program::{
    account_info::AccountInfo,
    entrypoint::ProgramResult,
    program::{invoke, invoke_signed},
    program_error::ProgramError,
    pubkey::Pubkey,
    rent::Rent,
    sysvar::Sysvar,
};
use solana_system_interface::{instruction as system_instruction, program as system_program};

/// Creates `new` as a rent-exempt account of `space` bytes owned by `owner`, the PDA signing with `seeds` (bump
/// included), `payer` paying. If someone pre-funded the address the lamports are kept and topped up.
pub fn create_pda_account<'a>(
    payer: &AccountInfo<'a>,
    new: &AccountInfo<'a>,
    system_program_info: &AccountInfo<'a>,
    owner: &Pubkey,
    space: usize,
    seeds: &[&[u8]],
) -> ProgramResult {
    if system_program_info.key != &system_program::ID {
        return Err(ProgramError::IncorrectProgramId);
    }
    if !new.data_is_empty() || new.owner != &system_program::ID {
        return Err(ProgramError::AccountAlreadyInitialized);
    }
    let rent = Rent::get()?;
    let required = rent.minimum_balance(space);
    let have = new.lamports();
    if have == 0 {
        invoke_signed(
            &system_instruction::create_account(payer.key, new.key, required, space as u64, owner),
            &[payer.clone(), new.clone(), system_program_info.clone()],
            &[seeds],
        )
    } else {
        if required > have {
            invoke(&system_instruction::transfer(payer.key, new.key, required - have), &[payer.clone(), new.clone(), system_program_info.clone()])?;
        }
        invoke_signed(&system_instruction::allocate(new.key, space as u64), &[new.clone(), system_program_info.clone()], &[seeds])?;
        invoke_signed(&system_instruction::assign(new.key, owner), &[new.clone(), system_program_info.clone()], &[seeds])
    }
}

/// Grows (or shrinks) a program-owned account to `new_len`, `payer` topping up the rent; the data past the old
/// length is zeroed by the runtime.
pub fn resize_account<'a>(target: &AccountInfo<'a>, payer: &AccountInfo<'a>, system_program_info: &AccountInfo<'a>, new_len: usize) -> ProgramResult {
    let rent = Rent::get()?;
    let required = rent.minimum_balance(new_len);
    let have = target.lamports();
    if required > have {
        invoke(&system_instruction::transfer(payer.key, target.key, required - have), &[payer.clone(), target.clone(), system_program_info.clone()])?;
    }
    target.resize(new_len)
}

/// Closes a program-owned account: its lamports go to `dest`, its data is emptied and it returns to the system
/// program, so nothing can revive it within the transaction.
pub fn close_account(target: &AccountInfo, dest: &AccountInfo) -> ProgramResult {
    let lamports = target.lamports();
    **target.try_borrow_mut_lamports()? = 0;
    **dest.try_borrow_mut_lamports()? = dest.lamports().checked_add(lamports).ok_or(ProgramError::ArithmeticOverflow)?;
    target.resize(0)?;
    target.assign(&system_program::ID);
    Ok(())
}

/// A readable 32-byte slice of account data at an offset.
pub fn read32(data: &[u8], at: usize) -> Result<[u8; 32], ProgramError> {
    let s = data.get(at..at + 32).ok_or(ProgramError::InvalidAccountData)?;
    let mut a = [0u8; 32];
    a.copy_from_slice(s);
    Ok(a)
}

pub fn read_u64(data: &[u8], at: usize) -> Result<u64, ProgramError> {
    let s = data.get(at..at + 8).ok_or(ProgramError::InvalidAccountData)?;
    let mut a = [0u8; 8];
    a.copy_from_slice(s);
    Ok(u64::from_le_bytes(a))
}

pub fn read_i64(data: &[u8], at: usize) -> Result<i64, ProgramError> {
    Ok(read_u64(data, at)? as i64)
}

/// A borsh `Option<Pubkey>` at an offset.
pub fn read_option_pubkey(data: &[u8], at: usize) -> Result<Option<Pubkey>, ProgramError> {
    match data.get(at).ok_or(ProgramError::InvalidAccountData)? {
        0 => Ok(None),
        1 => Ok(Some(Pubkey::new_from_array(read32(data, at + 1)?))),
        _ => Err(ProgramError::InvalidAccountData),
    }
}
