# Documentation

These documents describe the product, its design, and how it was built. The
service is implemented and tested but not deployed; the root
[README](../README.md) lists what is still missing for a release.

## Documents

- [Product design](design/product.md) — first-version scope, user flows,
  governance, privacy, and commercial decisions.
- [Trust and reporting](design/trust-and-reporting.md) — trusted inputs,
  measurement identity, producer eligibility, and gate behavior.
- [First-version decisions](design/decisions.md) — closes the open design items;
  overrides the two documents above on how things are built.
- [Module design](../internal/module.md) — architecture overview and the root of
  the fractal design tree; every package folder under `internal/` holds its own
  `module.md` specification.
- [Tech stack](tech-stack.md) — what the design tree is implemented with;
  platform seams still open.
- [Implementation plan](plans/2026-09-21-implement-archfit-app.md) — bottom-up,
  TDD, one task per module; replaces the closed-beta task order.
- [Closed-beta plan](plans/closed-beta.md) — M0–M5 milestones and T01–T23
  implementation tasks.
- [Hosting research](research/20260918-hosting.md) — dated provider, cost, and
  validation research; not a deployment approval.

Product design owns scope. Trust and reporting owns technical invariants. The
plan sequences implementation but does not expand scope. Research is dated and
must be rechecked before execution.

The documents remain separate because they serve different readers and change at
different rates. Keep links current and use Git history for superseded text.
