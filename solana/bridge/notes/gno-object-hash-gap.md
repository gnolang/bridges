# gno v1.2.0: a dirty parent saved before its dirty child stores a zero child hash, so the AppHash does not commit to that subtree

Found during the bridge plan's review (2026-10-10), confirmed by an independent reproduction. Drafted as an issue for
gnolang/gno; Jae decides whether and where to file it. The patch and the filetests are beside this note.

## Summary

`saveUnsavedObjects` (gnovm/pkg/gnolang/realm.go:931-963) saves `.created` objects child-first but saves `.updated`
objects in mark order with a plain `saveObject`. `MarkDirty` zeroes an object's hash (ownership.go:335-341), and
`toRefValue` (realm.go:2057-2110) writes `RefValue{ObjectID, Hash: oo.GetHash()}` unconditionally, so a parent
serialized while a non-escaped child is still dirty stores a zero hash for the link; amino `omitempty` drops it, and
the parent is never re-saved in that transaction. Only escaped objects' hashes reach the `main` (consensus) store;
everything below a zero link is bound to the AppHash by nothing. `master` (fetched 2026-10-10) has the same loop
(realm.go:955-958) and the same debug-only assertion (realm.go:2100, `non-escaped object should not have zero hash`);
no CI job runs `make test.debugAssert`.

## Reproduction (gnovm/tests/files; `go test -run TestFiles/zz_parentfirst1 ./pkg/gnolang`)

```go
// PKGPATH: gno.land/r/demo/parentfirst
package parentfirst
type Child struct{ X int }
type Parent struct { Counter int; Kid Child }
var p Parent
func main() {
	p.Counter++ // the Parent StructValue is marked dirty first  (rlm.updated[0])
	p.Kid.X = 1 // the Child StructValue is marked dirty second (rlm.updated[1])
}
```

The `Realm:` golden shows the parent `:4` saved before its child `:5` with the link's `"Hash"` line removed, then `:5`
saved with its new hash. Swapping the two statements gives a correct hash. Under `-tags debugAssert` the file,
`map31b.gno`, `zrealm17.gno` and 29 upstream fixtures in all panic at realm.go:2100.

Three orderings put a parent before its non-escaped child in `rlm.updated`: (a) the parent is written before the child
during execution (`DidUpdate` → `MarkDirty(po)`); (b) `processNewEscapedMarks` marks the old owner dirty before
`markDirtyAncestors` runs (`map31b.gno`); (c) `markDirtyAncestors` reaches a common ancestor from the first dirty
subtree and appends the second subtree's owners after it, so modifying any two package-level objects in one call
leaves the escaped package block with a zero link to the second one's heap item.

## Live evidence (gnoland-1)

`gno.land/r/gnoland/blog`: a breadth-first walk of 476 object links found 4 with a zero hash (`41→42`, `15→53`,
`54→56`, `56→57`); `…eb:15` (a BPTree struct, ModTime 58) links its root `:53` (ModTime 59, not escaped, no entry in
`main`) without a hash, and `:15`'s reconstructed bytes hash to the value its own parent records. The `main` store
holds `…eb:2` only.

## Does it heal?

The child gets its hash in the same transaction (it is saved, later). The parent is re-saved only in a later
transaction where the child is dirty and ordered before it. A parent-only modification does not repair it: the object
cache is evicted every transaction (keeper.go:401), the parent reloads with lazy `RefValue` children, and
`copyValueWithRefs` passes a `RefValue` through verbatim (realm.go:1776), copying the zero link forward.

## Scope

- Consensus: no fork; every node computes the same incomplete AppHash.
- What the AppHash no longer commits to: divergence, corruption or tampering of the `base` DB inside such subtrees is
  undetectable by AppHash comparison; export/import cannot be validated there; any merkle proof of object state
  (`.store/main/key?prove` → object bytes → child links) breaks at a zero link. tm2 has no state sync today; a future
  snapshot sync trusting the AppHash inherits the gap. Nothing in production verifies links at load: the check
  (values.go:3358, values_fill.go:77) is `debugAssert`-only and skips zero hashes.
- A separate pre-existing stale-link case (`zrealm_hiv_real_rejected.gno`, a cross-realm closure capture) also trips
  `hash chain broken` under `debugAssert` and survives the fix; not analysed further.

## Fix (gno-zero-hash-fix.patch, tested)

In `saveUnsavedObjects`, save `.updated` objects child-first as `.created` already are: for each dirty object,
recursively save its still-dirty, non-escaped children that are in THIS realm's `updated` set, then the object
(`saveDirtyChildrenFirst`, mirroring `saveUnsavedObjectRecursively`). The set restriction matters: an unrestricted
descent panics in `zrealm_crossrealm34.gno` ("cannot save object with no finalized ID") because a child marked dirty
by a borrowing realm can own that realm's not-yet-finalized objects. Whole `TestFiles` under `debugAssert`: 32
zero-hash panics → 1, the survivor being the cross-realm fixture whose link is transient and repaired in the same
transaction (its golden saves `u[A:3]` twice, the second time with the hash); the golden update changes only the eight
`Realm:` goldens that gain hashes. Also recommended: make the load-time chain check run in production and not skip
zero hashes for non-escaped children.
