# Architecture Decision Records

This directory captures the load-bearing engineering decisions made in GoDB — the ones that, if you didn't know the reasoning, you would be tempted to undo and regret it. Each ADR records the context that forced a choice, the choice itself, the alternatives considered, and the consequences accepted.

ADRs are short, dated, immutable documents. They are not the same as the [PRD](../prd.md) (which describes what the product is) or the [book](../book/) (which teaches the surrounding concepts). They sit between those: "here is the choice we made, and here is why someone reading the code six months later shouldn't undo it."

## Index

| #    | Title                                                                                        | Status   |
|------|----------------------------------------------------------------------------------------------|----------|
| 0001 | [Single `.godb` file with fixed 4 KB pages](0001-single-file-fixed-pages.md)                 | Accepted |
| 0002 | [Big-endian for all on-disk multi-byte integers](0002-big-endian-on-disk.md)                 | Accepted |
| 0003 | [`internal/` for implementation, `pkg/godb` for stable API](0003-internal-vs-pkg-layout.md)  | Accepted |
| 0004 | [No SQLite file or SQL dialect compatibility](0004-no-sqlite-compatibility.md)               | Accepted |
| 0005 | [Bottom-up build order: storage before SQL](0005-bottom-up-build-order.md)                   | Accepted |
| 0006 | [No buffer pool in v0.1 — direct pager I/O](0006-no-buffer-pool-in-v0-1.md)                  | Accepted |
| 0007 | [Explicit byte values for `record.Kind` (no `iota`)](0007-explicit-kind-byte-values.md)      | Accepted |
| 0008 | [`NULL` and empty `TEXT` have distinct on-disk encodings](0008-null-and-empty-text-distinct.md) | Accepted |
| 0009 | [LEB128 uvarint via `encoding/binary` (not SQLite varint)](0009-leb128-uvarint.md)           | Accepted |
| 0010 | [Slotted page with sorted cell directory + payloads from page end](0010-slotted-page-layout.md) | Accepted |
| 0011 | [Primary-key column stays in the row payload](0011-pk-column-stays-in-row.md)                | Accepted |
| 0012 | [Append-only page allocation in v0.1 (no freelist)](0012-append-only-page-allocation.md)     | Accepted |
| 0013 | [`PageHeader.RightSibling` carries dual semantics](0013-rightsibling-dual-semantics.md)       | Accepted |
| 0014 | [Catalog object encoding uses a custom binary format](0014-catalog-row-encoding.md)           | Accepted |
| 0015 | [SQL grammar is deliberately small; parser is hand-written recursive descent](0015-sql-grammar-scope.md) | Accepted |
| 0016 | [`Rows` is materialized in v0.1; streaming arrives in v0.2](0016-rows-materialization.md) | Accepted |
| 0017 | [Transactions are not supported in GoDB v0.1](0017-no-transactions-in-v0-1.md) | Accepted |
| 0018 | [`btree.UpdateCellSameSize` — same-size in-place cell update](0018-btree-update-cell-same-size.md) | Accepted |

## How to add an ADR

1. Copy [`template.md`](template.md) to a new file named `NNNN-kebab-case-title.md` using the next sequence number.
2. Fill in every section. The "Alternatives considered" section is required — if no alternatives existed, the decision isn't worth an ADR.
3. Add a row to the index table above.
4. Reference the new ADR from any code comment, book chapter, or other ADR that explains the same decision.

## When is something an ADR vs a code comment?

A code comment explains *what this line does* or *why it can't be done another way locally*. An ADR explains a decision that crosses files, packages, or milestones — anything where someone reading the code in one place would otherwise have no idea why a global constraint exists.

Rule of thumb: if the decision shows up in three places and "just changing it" would require coordinated edits across them, it deserves an ADR.
