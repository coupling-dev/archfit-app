#!/usr/bin/env bats

setup() {
	ROOT=$(cd "$BATS_TEST_DIRNAME/../../.." && pwd)
	mkdir -p "$BATS_TEST_TMPDIR/internal/wire/manifest" "$BATS_TEST_TMPDIR/internal/wire/envelope" "$BATS_TEST_TMPDIR/bin"
	cp "$ROOT/internal/wire/envelope/envelope.v1.schema.json" "$BATS_TEST_TMPDIR/internal/wire/envelope/"
	cd "$BATS_TEST_TMPDIR" || return
	export GITHUB_OUTPUT="$BATS_TEST_TMPDIR/output" GITHUB_STEP_SUMMARY="$BATS_TEST_TMPDIR/summary"
	export RUNNER_TEMP="$BATS_TEST_TMPDIR"
	REF1=cadf35c0d4885a49a3a1482b876f2a456d20de06
	REF2=e0a1ef9473d68b426e9c56b2da5986bcee2fc769
	for job in action-pin action-conformance; do
		awk -v job="$job:" '
			/^  [a-z]/ { active = ($1 == job) }
			active && /^        run: \|/ { script = 1; next }
			active && script { sub(/^          /, ""); print }
		' "$ROOT/.github/workflows/ci.yaml" >"$job.sh"
	done
}

@test "Action pin job validates and deduplicates every transition ref" {
	jq -n --arg a "$REF1" --arg b "$REF2" '{entries:[{action_ref:$a},{action_ref:$b},{action_ref:$a}]}' >internal/wire/manifest/manifest.json
	run bash action-pin.sh
	[ "$status" -eq 0 ]
	[ "$(cat "$GITHUB_OUTPUT")" = "refs=[\"$REF1\",\"$REF2\"]" ]
}

@test "Action pin job fails closed for empty invalid or placeholder refs" {
	local manifest
	for manifest in '{"entries":[]}' '{"entries":[{}]}' '{"entries":[{"action_ref":123}]}' \
		'{"entries":[{"action_ref":"main"}]}' '{"entries":[{"action_ref":"cadf35c0d4885a49a3a1482b876f2a456d20de06\n"}]}' '{"entries":[{"action_ref":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}' \
		'{"entries":[{"action_ref":"e0a1ef9473d68b426e9c56b2da5986bcee2fc769\ninjected=true"}]}'; do
		printf '%s\n' "$manifest" >internal/wire/manifest/manifest.json
		run bash action-pin.sh
		[ "$status" -ne 0 ]
		[ ! -e "$GITHUB_OUTPUT" ]
	done
}

mock_git() {
	cat >bin/git <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
checkout=$2
case $3 in
	init) ;;
	fetch)
		ref=${!#}
		printf '%s\n' "$ref" >>"$RUNNER_TEMP/fetched"
		printf '%s' "$ref" >"$checkout/ref"
		if [[ $ref == "${FAIL_REF:-}" ]]; then exit 1; fi
		;;
	rev-parse) cat "$checkout/ref" ;;
	show)
		if [[ $(cat "$checkout/ref") == "${MISMATCH_REF:-}" ]]; then
			printf 'different schema\n'
		else
			cat internal/wire/envelope/envelope.v1.schema.json
		fi
		;;
	*) exit 2 ;;
esac
MOCK
	chmod +x bin/git
	export PATH="$BATS_TEST_TMPDIR/bin:$PATH"
	export ACTION_REFS="[\"$REF1\",\"$REF2\"]"
}

@test "Action conformance checks both commits in the static job" {
	mock_git
	run bash action-conformance.sh
	[ "$status" -eq 0 ]
	[ "$(cat fetched)" = "$(printf '%s\n%s' "$REF1" "$REF2")" ]
	[ "$(grep -c 'identical at' "$GITHUB_STEP_SUMMARY")" -eq 2 ]
}

@test "Action conformance fails on a second-ref mismatch or fetch failure" {
	mock_git
	export MISMATCH_REF="$REF2"
	run bash action-conformance.sh
	[ "$status" -ne 0 ]
	[[ $output == *"vendors a different envelope schema"* ]]
	unset MISMATCH_REF
	export FAIL_REF="$REF2"
	run bash action-conformance.sh
	[ "$status" -ne 0 ]
}

@test "Action conformance rejects empty invalid and placeholder lists before fetching" {
	mock_git
	local refs
	for refs in '[]' 'null' '["main"]' '["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]'; do
		export ACTION_REFS="$refs"
		run bash action-conformance.sh
		[ "$status" -ne 0 ]
		[ ! -e fetched ]
	done
}
