# Rules for every agent working in this repository

These hold for every change, review and audit here.

## gno realms: check against the specifications, every time

Before changing, reviewing or auditing any gno code under `solana/realm/`, read these and check the code against
them rule by rule:

- `solana/docs/gno-specs/gno-interrealm-v2.md`: the Interrealm Specification v2. Realm context and storage context,
  real and unreal objects, storage as authority, `/p/` immutability, the three borrow rules, `cur` as a capability
  token, realm values ephemeral, boundaries and finalization, the conversion guards, panics across boundaries and
  `revive`, method values, the public-API checklist (§11), MsgCall, MsgRun and MsgAddPackage (§12).
- `solana/docs/gno-specs/interrealm_v2.adr.md`: its ADR.
- `solana/docs/gno-specs/gno-security-guide.md`: the security guide for realm authors. The threat model, the four
  structural defenses, the safety hypothesis, the anti-patterns (§5), the verification checklist (§8).
- `solana/docs/gno-specs/gno-ai-contract-review.md`: the review method to follow.

Then run `python3 solana/scripts/check-interrealm.py`. A secondary `realm` parameter is never the caller until
`IsCurrent()` proves it, and `unsafe.PreviousRealm()` beside a `cur realm` parameter is flagged. A review or audit
report names the sections it checked. The copies are dated in `solana/docs/gno-specs/README.md`;
`solana/scripts/sync-gno-specs.sh` refreshes them from a gno checkout (`GNO_FORK`).

## The plan wins

`solana/bridge/PLAN.md` is the design. Where code and plan differ, the plan wins until a reviewed change to the plan
says otherwise; every finding and its answer goes into `REVIEWS.md` or `AUDIT.md`.

## Commits

No agent trailers on commits (no Co-Authored-By, no "Generated with"). The human is the author of record.
