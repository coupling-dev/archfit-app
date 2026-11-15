# Gate Markdown

**Path**: internal/gate/markdown/
**Parent**: internal/gate/ (Gate)
**Submodules**: none (leaf)

## Purpose

Render untrusted policy names, reasons and engine records as literal, bounded text in App-authored issues.

## Responsibilities and Contract

- `CodeSpan(s string) string`: replace line breaks and use a backtick delimiter longer than any run in the text,
  padded when the text begins or ends with a backtick.
- `Fenced(s string) string`: place text in a code block with a fence its content cannot close.
- `OneLine(s string) string`: replace CR, LF and CRLF with spaces.
- `Clip(s string, n int) string`: keep at most `n` bytes at a rune boundary; append an ellipsis when truncated and
  the budget has room for it.

Callers own the body budgets, including generated fences and spans. `gate/drift` and `gate/expiry` use these helpers;
this package has no I/O or domain state. Existing feedback and request formatting remain unchanged.

## Tests

`TestCodeSpan`, `TestFencedCannotBeClosedByItsContent` and `TestClip` cover backticks, mentions and links as literal
text, line breaks, UTF-8 and small byte budgets. Drift and expiry tests cover full issue-body budgets.
