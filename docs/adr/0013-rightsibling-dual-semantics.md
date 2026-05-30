# ADR-0013: `PageHeader.RightSibling` carries dual semantics

- Status: Accepted
- Date: 2026-05-30
- Tags: storage, layout, btree

## Context

The 28-byte page header (spec §6.6, implemented in [`internal/btree/page.go`](../../internal/btree/page.go)) reserves an 8-byte field at offset 8 labelled `RightSibling`. In M3 (slotted pages) the field was reserved but unused; M5 (the multi-page B+tree) is the first milestone that needs to actually thread page-to-page pointers through it.

Two distinct kinds of B+tree pages now exist:

- **Leaves** (`PageTypeTableLeaf`) need a next-leaf-in-sorted-order pointer so that `Scan` can walk every cell across many pages without descending the tree each time. This is the classic B+tree leaf chain.
- **Internal pages** (`PageTypeTableInternal`) need a *rightmost child* pointer. An internal page holds N cells of the form `(left_child, separator)` plus one extra "rightmost child" reference for keys ≥ the largest separator. The rightmost child isn't a sibling of the internal page — it's a child.

Both are exactly one `PageID` (`uint64`, 8 bytes). The natural questions:

- Should we add a *second* 8-byte field to the page header dedicated to "rightmost child" for internal pages? (Permanent +8 bytes of header overhead for every page, including leaves that never use it.)
- Should we reuse the existing `RightSibling` slot with overloaded semantics based on page type? (No header change, no per-leaf overhead, but readers have to know which interpretation applies.)
- Should we use `RightSibling` only on leaves and stash the rightmost-child on internal pages somewhere else (e.g. the unused `Parent` field)? (Asymmetric and confusing.)

## Decision

`PageHeader.RightSibling` carries **two different semantics** depending on the page's type byte:

- On **leaf pages** (`PageTypeTableLeaf`, `PageTypeIndexLeaf`): the page id of the next leaf in sorted-key order. `0` means "this is the rightmost leaf in its tree" — there is no further sibling.
- On **internal pages** (`PageTypeTableInternal`, `PageTypeIndexInternal`): the page id of the rightmost child (the subtree containing keys ≥ all separators on this page). `0` is only valid on an internal page with zero cells, which is itself a transient/malformed state — `ValidateInternal` rejects an internal page with cells but no rightmost child.

The on-disk byte slot does not change. The interpretation is unambiguous because the page type byte at offset 0 always tells the reader which set of semantics applies.

In code, internal-page consumers go through the accessors `btree.RightmostChild` and `btree.SetRightmostChild` (in [`internal/btree/internal.go`](../../internal/btree/internal.go)). Leaf consumers go through `btree.RightSibling` and `btree.SetRightSibling` (in [`internal/btree/leaf.go`](../../internal/btree/leaf.go)). Both accessor pairs check the page type and refuse to read or write the field if the page is the wrong kind — so a logic error confusing the two surfaces as a typed error at the call site rather than a silent miswrite.

## Consequences

**Enables.** The 28-byte page header stays as-is — no format change required to ship M5. Per-leaf overhead stays unchanged. The two semantics are localized in two small accessor pairs, each enforced by `requireLeaf` / `requireInternal`. The leaf chain and the rightmost-child pointer never appear on the same page, so there's no ambiguity at runtime — the page type chooses which interpretation to use.

**Constrains.** A reader who only knows `RightSibling` as "next sibling" gets confused when they hit an internal page. The mitigation is twofold: (1) the field's doc comment in [`internal/btree/page.go`](../../internal/btree/page.go) calls out the dual semantic, and (2) the accessors are typed by page kind, so the wrong accessor on the wrong page returns `ErrNotLeaf` instead of giving back a misinterpreted value.

A future "let's make this two fields" PR exists and is undesirable. It would either (a) bump the page-header size from 28 to 36 bytes (an on-disk layout change, requiring a file-format major-version bump, breaking every existing `.godb`), or (b) carve the field's bits into "low 32 = leaf-sibling, high 32 = rightmost-child" or similar (cute but constrains page ids to 32-bit, which would cap database size at 16 TB for 4 KB pages — fine forever in practice but is a real ceiling). Neither alternative is worth the gain.

**Reversibility.** Effectively permanent within a file-format version. Reversing the decision requires a format bump.

## Alternatives considered

**Add an explicit 8-byte `RightmostChild` field to the page header.** Pros: each field has one meaning. Cons: 28 → 36 bytes per page header, which on a 4 KB page is small in absolute terms (~0.2%) but is unbounded in aggregate — every page in every database pays this cost forever, for a structure (the leaf chain) that doesn't use it on internal pages. Rejected: pure overhead with no readability win once readers learn the convention.

**Use `Parent` for the rightmost child on internal pages.** The `Parent` field exists in the header (debug-only per a comment from M3, currently unused). Reusing *it* for internal-page rightmost child would keep `RightSibling` single-meaning. Rejected: "Parent" is the wrong word for "rightmost child" by a lot, more so than reusing "RightSibling" for "rightmost child." If we ever want a real debug parent pointer later, we'd be sad to have spent the field.

**Encode the rightmost child inside the cell directory.** Some implementations represent rightmost-child as an extra "virtual cell" past the end of the directory. Pros: only one mental model (cells), no special field. Cons: complicates the slotted-page invariants the rest of the engine relies on (the cell directory + free-space accounting), and the rightmost child has no separator key by definition. Rejected: more local complexity for less global change.

## Related

- Code: [`internal/btree/page.go`](../../internal/btree/page.go) (`PageHeader` doc comment), [`internal/btree/leaf.go`](../../internal/btree/leaf.go) (`RightSibling` / `SetRightSibling`), [`internal/btree/internal.go`](../../internal/btree/internal.go) (`RightmostChild` / `SetRightmostChild`).
- Book: [Chapter 07 — The Multi-page B+tree (M5)](../book/07-milestone-5-multi-page-btree.md) walks through both uses with the descent + split algorithms.
- See also: [ADR-0010](0010-slotted-page-layout.md) (slotted page layout) — the page header layout this field lives in.
