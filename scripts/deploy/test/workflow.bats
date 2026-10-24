#!/usr/bin/env bats

setup() {
	ROOT=$(cd "$BATS_TEST_DIRNAME/../../.." && pwd)
}

@test "new images need full CI while incident rollback needs only deployment safety" {
	local validation release
	validation=$(sed -n '/^  validate:/,/^  deploy-check:/p' "$ROOT/.github/workflows/deploy.yaml")
	release=$(sed -n '/^  release:/,/^    steps:/p' "$ROOT/.github/workflows/deploy.yaml")
	[[ $validation == *"uses: ./.github/workflows/ci.yaml"* ]]
	[[ $validation == *"inputs.mode == 'build' || inputs.mode == 'release'"* ]]
	[[ $validation != *"inputs.mode == 'image'"* ]]
	[[ $validation != *"inputs.mode == 'traffic'"* ]]
	[[ $release == *"needs: [validate, deploy-check]"* ]]
	[[ $release == *"always() && !cancelled()"* ]]
	[[ $release == *"needs.deploy-check.result == 'success'"* ]]
	[[ $release == *"needs.validate.result == 'success'"* ]]
	[[ $release == *"inputs.mode == 'image' || inputs.mode == 'traffic'"* ]]
}

@test "CI can be called by deployment and rejects release manifest placeholders" {
	grep -q '^  workflow_call:' "$ROOT/.github/workflows/ci.yaml"
	grep -q 'ARCHFIT_RELEASE: "1"' "$ROOT/.github/workflows/ci.yaml"
}

@test "validation has no cloud permissions and release checks its exact checkout" {
	local validation
	validation=$(sed -n '/^  validate:/,/^  deploy-check:/p' "$ROOT/.github/workflows/deploy.yaml")
	[[ $validation == *"contents: read"* ]]
	[[ $validation != *"id-token:"* ]]
	[[ $validation != *"contents: write"* ]]
	local safety
	safety=$(sed -n '/^  deploy-check:/,/^  release:/p' "$ROOT/.github/workflows/deploy.yaml")
	[[ $safety == *"run: bash scripts/deploy/test.sh"* ]]
	[[ $safety != *"setup-go@"* ]]
	[[ $safety != *"make deploy-check"* ]]
	[ "$(grep -Fc "run: test \"\$(git rev-parse HEAD)\" = \"\$GITHUB_SHA\"" "$ROOT/.github/workflows/deploy.yaml")" -eq 2 ]
}
