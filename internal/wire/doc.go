// Package wire groups the transport contracts shared with the Archfit 4 engine and Action.
// Consumers import its children: envelope, manifest, policy3, request, result, wording and workflow.
//
// Wire packages own strict decoding, schemas and digests, not I/O, domain decisions or storage.
// Children do not import one another. Versioned contracts reject unknown versions and fields;
// the request line is an unversioned command whose goldens change with the engine release.
// Digests use the prefixed sha256:<hex> form. Envelope 1, engine state reports, policy schema 2,
// App settings and manifest 1 are not accepted contracts.
//
// Envelope fixtures live in testdata/envelope-v2; other children own their fixtures.
package wire
