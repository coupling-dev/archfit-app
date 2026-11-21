#!/bin/sh
# Reject old project homes, not personal logins.
# docs/archive, docs/plans and docs/reviews record history.
# This script contains the rule itself.
# Listed vendored paths come from the pinned engine/Action. The engine v4.0.0
# result corpus, the version 3 policy corpus and the tests that pin them name
# the old home until task 6.5 deletes them (internal/wire/result/module.md).
# Runtime references to approved v4 Action/engine move with manifest in 5.3,
# both repositories accepted during epic 6.
# Workflow parser tests retain the v4 Action contract.
set -eu
pattern='alexei-led/(archfit|homebrew-tap|tap/archfit)|ghcr\.io/alexei-led|archfit(-app)?/v[0-9]+\b'
if git grep -n -I -E "$pattern" -- \
  ':!docs/archive/**' ':!docs/plans/**' ':!docs/reviews/**' ':!scripts/check-names.sh' \
  ':!internal/control/onboarding/templates/archfit.yaml.tmpl' \
  ':!internal/wire/manifest/engine_manifest.go' \
  ':!internal/wire/workflow/workflow_test.go' \
  ':!web/src/components/Setup.svelte' \
  ':!internal/wire/result/vendor_test.go' \
  ':!internal/wire/policy3/source_test.go' \
  ':!internal/wire/policy3/testdata/v3/SCHEMA_SOURCE' \
  ':!internal/wire/result/testdata/v4.0.0/SCHEMA_SOURCE' \
  ':!internal/wire/policy3/testdata/v3/archfit.schema.json' \
  ':!internal/wire/result/testdata/v4.0.0/archfit.result.schema.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-bare-policy-digest.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-bare-policy-digest.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-baseline-digest.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-baseline-digest.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-event.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-event.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-kind.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-kind.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-missing-target-sha.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-missing-target-sha.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-payload-digest.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-payload-digest.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-push-with-pull-request.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-push-with-pull-request.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-schema-version.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-schema-version.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-target-ref.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-target-ref.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-unknown-key.expect.json' \
  ':!internal/wire/testdata/envelope-v2/invalid-unknown-key.json' \
  ':!internal/wire/testdata/envelope-v2/merge-group.expect.json' \
  ':!internal/wire/testdata/envelope-v2/merge-group.json' \
  ':!internal/wire/testdata/envelope-v2/pull-request.expect.json' \
  ':!internal/wire/testdata/envelope-v2/pull-request.json' \
  ':!internal/wire/testdata/envelope-v2/push.expect.json' \
  ':!internal/wire/testdata/envelope-v2/push.json' \
  ':!internal/wire/testdata/envelope-v2/rerun.expect.json' \
  ':!internal/wire/testdata/envelope-v2/rerun.json' \
  ':!internal/wire/testdata/envelope-v2/stand-in.payload' \
  ':!scripts/check-names.sh'; then
  echo 'check-names: replace old home with beta plan section 3 names' >&2
  exit 1
fi
