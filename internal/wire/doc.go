// Package wire groups the versioned wire contracts shared with the archfit engine, archfit-action and the files
// customers keep on their protected ref. It exports nothing: consumers import the child packages envelope, report,
// settings, policy and manifest.
//
// Placement rule: a wire package holds transport types, schemas, strict decoding and digests. It holds no domain
// decision and does no I/O; nothing here is persisted. Children never import each other or any other internal
// package; shared meaning travels as plain strings and the consumer does the mapping.
//
// Versioning rule: every contract carries a version identifier, and a wrong or unknown one is a decode error.
// Decoders reject unknown fields, so a producer may send a new optional field only after an App release allows it.
// The report decoder reads each engine release it lists with that release's own key set, and the manifest may
// approve several releases, so an engine upgrade needs no flag day.
// A breaking change adds a new version constant and a new decode path beside the old one, which stays until no
// approved producer emits the old version. A version bump never changes a consumer's domain types.
//
// Two digest formats exist and are never compared with each other: engine-style bare lowercase hex (policy.Digest,
// the report's config_hash, the envelope's baseline_digest and labels_digest) and the prefixed "sha256:<hex>" form
// of the envelope's payload_digest and image_digest.
//
// The shared conformance fixtures live in testdata/; module.md defines their layout.
package wire
