#!/bin/sh
# Refresh docs/gno-specs/ from Jae's gno fork (default ~/src/jaekwon/gno; GNO_FORK overrides) and show the diff.
set -e
G="${GNO_FORK:-$HOME/src/jaekwon/gno}"
R="$(cd "$(dirname "$0")/.." && pwd)"
[ -d "$G/docs/resources" ] || { echo "no gno fork at $G (set GNO_FORK)"; exit 1; }
cp "$G/docs/resources/gno-interrealm-v2.md"      "$R/docs/gno-specs/gno-interrealm-v2.md"
cp "$G/gnovm/adr/interrealm_v2.md"               "$R/docs/gno-specs/interrealm_v2.adr.md"
cp "$G/docs/resources/gno-security-guide.md"     "$R/docs/gno-specs/gno-security-guide.md"
cp "$G/docs/resources/gno-ai-contract-review.md" "$R/docs/gno-specs/gno-ai-contract-review.md"
sha=$(git -C "$G" log --format=%h -1 2>/dev/null || echo unknown)
sed -i.bak -E "s/commit [0-9a-f]+, copied [0-9-]+/commit $sha, copied $(date +%F)/" "$R/docs/gno-specs/README.md" && rm -f "$R/docs/gno-specs/README.md.bak"
git -C "$R" --no-pager diff --stat -- docs/gno-specs
