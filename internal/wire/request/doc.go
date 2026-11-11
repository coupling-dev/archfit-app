// Package request is the request contract of Archfit 4 (design 10.3, 11.7): the one line a worker writes to ask an
// owner to change the policy, the issue the App opens for it, and the key that makes a second request for the same
// thing join the first. The App owns the contract; the engine's `archfit request` vendors the goldens in
// testdata/line/ and testdata/form/ and writes exactly those bytes.
//
// # The request line
//
//	comment-line = "/archfit request" SP reason [ SP "--expires" SP date ]
//	cli          = "archfit request" [ " -m " shell-quoted-reason ] [ " --expires " date ]
//	reason       = 1 to 500 characters of one line, trimmed
//	date         = YYYY-MM-DD, after the App's date, at most 365 days ahead
//
// The cli form is the command a worker types; the App never reads it. With an open pull request the CLI posts the
// canonical comment line as a comment: "/archfit request " reason, then " --expires " date when one is given.
//
// ParseComment reads a pull-request comment. The first line that starts with "/archfit request", at the start of
// the line and followed by white space or the end of the line, is the request; every other line is ignored, so a
// comment holds one request. A line ends at LF, CRLF or CR, as in Markdown, so a reason is always one line. The flag
// is the pair that ends the line: when the second-to-last word is "--expires", the last word must be a date, and a
// line that ends with a lone "--expires" has a bad date. Any earlier "--expires" stays in the reason. The reason is
// the rest, trimmed; its spacing inside is kept. Characters are Unicode code points.
//
// # The issue the CLI opens
//
// Before a pull request exists, `archfit request` opens an issue from the last `archfit can` answer:
//
//	issue-form = comment-line NL NL text-block
//	text-block = fence "text" NL record fence NL
//
// The record is the `can` answer as the engine prints it, and fence is a run of backticks longer than any run in
// the record, at least three. ParseIssue needs the request line as the first line that is not blank, so the body the
// App renders is never read as a request. It keeps the record of the first text block after the line verbatim and
// reads the answer from it: the head "from -> to: rule: what" and the "balance:" line. A `can` answer never names a
// copy or a cycle, so the key of an answer is complete. The App marks these numbers "as reported" until a pull
// request confirms them.
//
// # The key
//
// Errors with the same key are one request (M2 plan D3). The key is its parts joined by one space:
//
//	graph, coupling, layers, cycle, a custom rule:  rule from to   (for only, to is the library)
//	duplication:                                    the engine's key of the error
//	unowned:                                        "unowned" path
//
// The engine's key of a copy is the rule, the two modules sorted and the digest of the literal; it is compared,
// never parsed. A cycle is one error per dependency, keyed in its direction, as exceptions match it. An expired
// exception asks nothing and has no key.
//
// # Rejections
//
// A line that is a request but cannot be one gets one reply: no reason, a reason over 500 characters, a bad, past or
// too distant date. The App also replies when no error asks (NothingToAsk) and when the author may not ask
// (NoPermission, M2 plan D21). A reply never repeats the comment's text.
package request
