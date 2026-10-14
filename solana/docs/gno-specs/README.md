# gno specifications every realm change is checked against

Copies of Jae's gno fork (`~/src/jaekwon/gno`, commit 5d7d8f5f9, copied 2026-10-13), so that every agent and every
machine working on this repo has them:

- `gno-interrealm-v2.md` — the Interrealm Specification v2 (docs/resources/gno-interrealm-v2.md).
- `interrealm_v2.adr.md` — its ADR (gnovm/adr/interrealm_v2.md).
- `gno-security-guide.md` — the security guide for realm authors (docs/resources/gno-security-guide.md).
- `gno-ai-contract-review.md` — the review method (docs/resources/gno-ai-contract-review.md).

The rule that uses them is in AGENTS.md at the repo root. Refresh the copies with `scripts/sync-gno-specs.sh`
and commit the diff; the checker is `scripts/check-interrealm.py` (run from the repo root).
