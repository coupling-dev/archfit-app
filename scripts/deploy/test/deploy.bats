#!/usr/bin/env bats
# shellcheck disable=SC2030,SC2031 # each test runs in its own subshell on purpose
# Behavior tests of the release scripts. Fake gcloud, docker and curl in test/bin record every call; no test talks to
# Google Cloud.

bats_require_minimum_version 1.5.0

setup() {
	ROOT=$(cd "$BATS_TEST_DIRNAME/../../.." && pwd)
	DEPLOY=$ROOT/scripts/deploy
	export PATH="$BATS_TEST_DIRNAME/bin:$PATH"
	export FAKE_STATE=$BATS_TEST_TMPDIR FAKE_LOG=$BATS_TEST_TMPDIR/calls.log
	FAKE_DIGEST=sha256:$(printf 'b%.0s' {1..64})
	export FAKE_DIGEST
	export GITHUB_RUN_ID=4242 GITHUB_RUN_ATTEMPT=1 GITHUB_SHA=0123456789abcdef0123456789abcdef01234567
	export GITHUB_STEP_SUMMARY=$BATS_TEST_TMPDIR/summary.md
	export RELEASE_DIR=$BATS_TEST_TMPDIR/release READY_ATTEMPTS=3 READY_DELAY=0
	unset GITHUB_ACTIONS
	: >"$FAKE_LOG"

	IMAGE=europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app/archfit-app@$FAKE_DIGEST
	WORKER_REV=archfit-worker-r4242-1
	PUBLIC_REV=archfit-public-r4242-1
}

# calls prints the logged calls that start with the given words, in order.
calls() {
	grep -E "^$*" "$FAKE_LOG" || true
}

release() {
	run "$DEPLOY/release.sh" "$FAKE_DIGEST"
}

assert_nothing_promoted() {
	[ -z "$(calls 'gcloud run services update-traffic [^ ]+ --to-revisions')" ]
}

assert_canary_untagged() {
	[ "$(calls 'gcloud run services update-traffic archfit-public --remove-tags=canary ' | wc -l)" -eq 1 ]
}

# --- release ---

@test "release deploys the worker, then the public canary, both by digest and without traffic" {
	release
	[ "$status" -eq 0 ]
	run calls 'gcloud run deploy'
	[ "${#lines[@]}" -eq 2 ]
	[[ ${lines[0]} == "gcloud run deploy archfit-worker --image=$IMAGE --revision-suffix=r4242-1 --no-traffic --region="* ]]
	[[ ${lines[1]} == "gcloud run deploy archfit-public --image=$IMAGE --revision-suffix=r4242-1 --no-traffic --tag=canary --region="* ]]
}

@test "release promotes the worker, then the public service, to the exact revisions it deployed" {
	release
	[ "$status" -eq 0 ]
	run calls 'gcloud run services update-traffic'
	[ "${#lines[@]}" -eq 2 ]
	[[ ${lines[0]} == "gcloud run services update-traffic archfit-worker --to-revisions=$WORKER_REV=100 "* ]]
	[[ ${lines[1]} == "gcloud run services update-traffic archfit-public --to-revisions=$PUBLIC_REV=100 "* ]]
	run grep -E -- '--to-latest|LATEST|--remove-tags' "$FAKE_LOG"
	[ "$status" -eq 1 ]
}

@test "release checks the canary through its tag URL before any promotion" {
	release
	[ "$status" -eq 0 ]
	run grep -n -E '^(curl|gcloud run services update-traffic)' "$FAKE_LOG"
	[[ ${lines[0]} == *"curl "*"https://canary---archfit-public-abc.a.run.app/ready" ]]
	[[ ${lines[1]} == *"update-traffic archfit-worker"* ]]
}

@test "release retries the readiness check and promotes once the canary is ready" {
	export FAKE_READY_CODES="503 503 200"
	release
	[ "$status" -eq 0 ]
	[ "$(calls curl | wc -l)" -eq 3 ]
	[ "$(calls 'gcloud run services update-traffic' | wc -l)" -eq 2 ]
}

@test "release promotes nothing when the canary never becomes ready" {
	export FAKE_READY_CODES="503"
	release
	[ "$status" -ne 0 ]
	[[ $output == *"canary $PUBLIC_REV is not ready; nothing promoted"* ]]
	[ "$(calls curl | wc -l)" -eq 3 ]
	assert_nothing_promoted
	assert_canary_untagged
	[ "$(jq -r '.status + "|" + .step' "$RELEASE_DIR/release.json")" = "failed|canary readiness" ]
}

@test "release promotes nothing when a deployed revision runs another image" {
	export FAKE_FOREIGN_IMAGE_REVISION=$PUBLIC_REV
	release
	[ "$status" -ne 0 ]
	[[ $output == *"revision $PUBLIC_REV is not the archfit-public revision of this release"* ]]
	assert_nothing_promoted
	assert_canary_untagged
	[ -z "$(calls curl)" ]
}

@test "release promotes nothing when a revision belongs to another service" {
	export FAKE_FOREIGN_REVISION=$WORKER_REV
	release
	[ "$status" -ne 0 ]
	assert_nothing_promoted
}

@test "release promotes nothing when a deployed revision is not ready" {
	export FAKE_NOT_READY_REVISION=$WORKER_REV
	release
	[ "$status" -ne 0 ]
	[[ $output == *"revision $WORKER_REV is not ready"* ]]
	assert_nothing_promoted
}

@test "release promotes nothing when the canary tag points at a revision someone else created" {
	export FAKE_CANARY_REVISION=archfit-public-00007-xyz
	release
	[ "$status" -ne 0 ]
	[[ $output == *"tag canary does not point at $PUBLIC_REV"* ]]
	assert_nothing_promoted
	[ -z "$(calls curl)" ]
}

@test "release stops before any deploy when the digest is not in Artifact Registry" {
	export FAKE_DIGEST_MISSING=1
	release
	[ "$status" -ne 0 ]
	[[ $output == *"is not in Artifact Registry"* ]]
	[ -z "$(calls 'gcloud run deploy')" ]
}

@test "release refuses an image reference that is not a digest" {
	for ref in "" latest "sha256:abc" "$FAKE_DIGEST:latest" "europe-west1-docker.pkg.dev/x@$FAKE_DIGEST"; do
		run "$DEPLOY/release.sh" "$ref"
		[ "$status" -ne 0 ]
		[[ $output == *"not an image digest"* ]]
	done
	[ ! -s "$FAKE_LOG" ]
}

@test "release records the image, the revisions and the traffic before it" {
	release
	[ "$status" -eq 0 ]
	run jq -c '{status, step, run_commit, digest, image, worker, public: (.public | del(.canary_url))}' "$RELEASE_DIR/release.json"
	[ "$output" = "{\"status\":\"released\",\"step\":\"finished\",\"run_commit\":\"$GITHUB_SHA\",\"digest\":\"$FAKE_DIGEST\",\"image\":\"$IMAGE\",\"worker\":{\"revision\":\"$WORKER_REV\",\"traffic_before\":[\"archfit-worker-r100-1=100\"]},\"public\":{\"revision\":\"$PUBLIC_REV\",\"traffic_before\":[\"archfit-public-r100-1=100\"]}}" ]
	grep -q "| Image | \`$IMAGE\` |" "$GITHUB_STEP_SUMMARY"
	grep -q "| Public traffic before | \`archfit-public-r100-1=100\` |" "$GITHUB_STEP_SUMMARY"
}

@test "release fails after promotion when the post-release check fails" {
	export FAKE_WORKER_INGRESS=all
	release
	[ "$status" -ne 0 ]
	[ "$(calls 'gcloud run services update-traffic' | wc -l)" -eq 2 ]
	[ -z "$(calls 'gcloud run services update-traffic archfit-public --remove-tags')" ]
	[ "$(jq -r '.status + "|" + .step' "$RELEASE_DIR/release.json")" = "failed|post-release checks" ]
}

# --- post-release checks ---

@test "check passes when the worker exposure and the queue routing match the design" {
	run "$DEPLOY/check.sh"
	[ "$status" -eq 0 ]
	grep -q 'Post-release checks: passed' "$GITHUB_STEP_SUMMARY"
}

@test "check fails when the worker exposure or a queue route differs from the design" {
	local cases=(
		"FAKE_WORKER_INGRESS=all|archfit-worker ingress is 'all', want 'internal'"
		"FAKE_WORKER_INGRESS=internal-and-cloud-load-balancing|archfit-worker ingress is 'internal-and-cloud-load-balancing'"
		"FAKE_WORKER_INVOKER_IAM_DISABLED=true|archfit-worker has the invoker check off"
		"FAKE_WORKER_EXTRA_INVOKER=allUsers|archfit-worker IAM bindings are"
		"FAKE_WORKER_EXTRA_INVOKER=allAuthenticatedUsers|archfit-worker IAM bindings are"
		"FAKE_WORKER_EXTRA_INVOKER=serviceAccount:archfit-public@coupling-dev-510710.iam.gserviceaccount.com|archfit-worker IAM bindings are"
		"FAKE_WORKER_EXTRA_BINDING=roles/run.developer|serviceAccount:archfit-public@coupling-dev-510710.iam.gserviceaccount.com|archfit-worker IAM bindings are"
		"FAKE_WORKER_NO_BINDINGS=1|archfit-worker IAM bindings are []"
		"FAKE_QUEUE_PATH=/internal/grants|queue archfit-webhooks routing is"
		"FAKE_QUEUE_HOST=archfit-public-335297348272.europe-west1.run.app|queue archfit-webhooks routing is"
		"FAKE_QUEUE_IDENTITY=archfit-public@coupling-dev-510710.iam.gserviceaccount.com|queue archfit-webhooks routing is"
		"FAKE_QUEUE_AUDIENCE=https://app.coupling.dev|queue archfit-webhooks routing is"
		"FAKE_QUEUE_ENFORCE=IF_NOT_EXISTS|queue archfit-webhooks routing is"
		"FAKE_QUEUE_SCHEME=HTTP|queue archfit-webhooks routing is"
		"FAKE_QUEUE_METHOD=GET|queue archfit-webhooks routing is"
	)
	local case setting want
	for case in "${cases[@]}"; do
		setting=${case%|*}
		want=${case##*|}
		run env FAKE_QUEUE=archfit-webhooks "$setting" "$DEPLOY/check.sh"
		echo "case $setting: $output"
		[ "$status" -ne 0 ]
		[[ $output == *"$want"* ]]
		[[ $output == *"post-release checks failed"* ]]
	done
}

@test "check fails when the operator queue points at the webhook route" {
	run env FAKE_QUEUE=archfit-operator FAKE_QUEUE_PATH=/internal/webhooks "$DEPLOY/check.sh"
	[ "$status" -ne 0 ]
	[[ $output == *"queue archfit-operator routing is"* ]]
}

# --- traffic rollback ---

@test "rollback moves the public service, then the worker, to the named release revisions and checks" {
	run env PUBLIC_REVISION=archfit-public-r100-1 WORKER_REVISION=archfit-worker-r100-1 "$DEPLOY/rollback.sh"
	[ "$status" -eq 0 ]
	run calls 'gcloud run services update-traffic'
	[[ ${lines[0]} == "gcloud run services update-traffic archfit-public --to-revisions=archfit-public-r100-1=100 "* ]]
	[[ ${lines[1]} == "gcloud run services update-traffic archfit-worker --to-revisions=archfit-worker-r100-1=100 "* ]]
	[[ ${lines[2]} == "gcloud run services update-traffic archfit-public --remove-tags=canary "* ]]
	[ "${#lines[@]}" -eq 3 ]
	[ -n "$(calls 'gcloud tasks queues describe')" ]
}

@test "rollback moves all traffic and runs the checks when the canary tag cannot be removed, then fails" {
	run env FAKE_REMOVE_TAGS_FAILS=1 PUBLIC_REVISION=archfit-public-r100-1 WORKER_REVISION=archfit-worker-r100-1 "$DEPLOY/rollback.sh"
	[ "$status" -ne 0 ]
	[[ $output == *"the tag canary still names a bad revision; remove it by hand"* ]]
	[ "$(calls 'gcloud run services update-traffic [^ ]+ --to-revisions' | wc -l)" -eq 2 ]
	[ -n "$(calls 'gcloud tasks queues describe')" ]
}

@test "rollback moves one service when only its revision is named" {
	run env WORKER_REVISION=archfit-worker-r100-1 "$DEPLOY/rollback.sh"
	[ "$status" -eq 0 ]
	run calls 'gcloud run services update-traffic'
	[ "${#lines[@]}" -eq 1 ]
	[[ ${lines[0]} == *"archfit-worker --to-revisions=archfit-worker-r100-1=100"* ]]
}

@test "rollback moves no traffic when a named revision is not a release revision" {
	local cases=(
		"PUBLIC_REVISION=archfit-public-00007-xyz|is not a release revision of archfit-public"
		"PUBLIC_REVISION=archfit-worker-r100-1|is not a release revision of archfit-public"
		"FAKE_FOREIGN_REVISION=archfit-public-r100-1|does not belong to archfit-public"
		"FAKE_FOREIGN_IMAGE_REVISION=archfit-public-r100-1|does not run an image of"
		"FAKE_NOT_READY_REVISION=archfit-public-r100-1|is not ready"
	)
	local case setting want
	for case in "${cases[@]}"; do
		setting=${case%%|*}
		want=${case#*|}
		: >"$FAKE_LOG"
		run env PUBLIC_REVISION=archfit-public-r100-1 WORKER_REVISION=archfit-worker-r100-1 "$setting" "$DEPLOY/rollback.sh"
		echo "case $setting: $output"
		[ "$status" -ne 0 ]
		[[ $output == *"$want"* ]]
		assert_nothing_promoted
	done
}

@test "rollback needs at least one revision" {
	run "$DEPLOY/rollback.sh"
	[ "$status" -ne 0 ]
	[[ $output == *"set PUBLIC_REVISION, WORKER_REVISION, or both"* ]]
	[ ! -s "$FAKE_LOG" ]
}

@test "image rollback is a release of an earlier digest" {
	local old
	old=sha256:$(printf 'a%.0s' {1..64})
	run "$DEPLOY/release.sh" "$old"
	[ "$status" -eq 0 ]
	[ "$(calls "gcloud run deploy .* --image=[^ ]*@$old " | wc -l)" -eq 2 ]
}

# --- build ---

@test "build pushes the commit image once and prints its digest" {
	run --separate-stderr "$DEPLOY/build.sh" "$GITHUB_SHA"
	[ "$status" -eq 0 ]
	[ "$output" = "$FAKE_DIGEST" ]
	local tagged=europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app/archfit-app:$GITHUB_SHA
	[ -n "$(calls "docker build --platform=linux/amd64 .*--tag=$tagged \\.")" ]
	[ "$(calls "docker push $tagged$")" = "docker push $tagged" ]
}

@test "build records a bootstrap image without touching Cloud Run" {
	run --separate-stderr "$DEPLOY/build.sh" "$GITHUB_SHA"
	[ "$status" -eq 0 ]
	[ "$output" = "$FAKE_DIGEST" ]
	jq -e --arg commit "$GITHUB_SHA" --arg digest "$FAKE_DIGEST" --arg image "$IMAGE" \
		'.commit == $commit and .digest == $digest and .image == $image' "$RELEASE_DIR/build.json"
	grep -q "$FAKE_DIGEST" "$GITHUB_STEP_SUMMARY"
	[ -z "$(calls 'gcloud run')" ]
}

@test "build reuses the image of a commit that was already pushed" {
	touch "$FAKE_STATE/pushed"
	run --separate-stderr "$DEPLOY/build.sh" "$GITHUB_SHA"
	[ "$status" -eq 0 ]
	[ "$output" = "$FAKE_DIGEST" ]
	[ -z "$(calls docker)" ]
}

@test "build refuses a value that is not a full commit SHA" {
	for ref in "" main 0123456 "$GITHUB_SHA;id"; do
		run "$DEPLOY/build.sh" "$ref"
		[ "$status" -ne 0 ]
		[[ $output == *"not a commit SHA"* ]]
	done
	[ ! -s "$FAKE_LOG" ]
}

# --- settings match the Terraform ---

@test "the deploy workflow is the one the Workload Identity provider admits" {
	grep -q 'default     = "deploy.yaml"' "$ROOT/infra/data/variables.tf"
	[ -f "$ROOT/.github/workflows/deploy.yaml" ]
}

@test "the project, region, services and image repository match the Terraform" {
	# shellcheck source=../lib.sh
	source "$DEPLOY/lib.sh"
	local stack
	for stack in data runtime; do
		grep -A3 '^variable "project_id"' "$ROOT/infra/$stack/variables.tf" | grep -q "default     = \"$PROJECT_ID\""
		grep -A3 '^variable "region"' "$ROOT/infra/$stack/variables.tf" | grep -q "default     = \"$REGION\""
	done
	# shellcheck disable=SC2016 # literal Terraform expressions
	{
		grep -q 'name                 = "archfit-${each.key}"' "$ROOT/infra/runtime/services.tf"
		grep -q 'value       = "${google_artifact_registry_repository.app.location}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.app.repository_id}/archfit-app"' "$ROOT/infra/data/outputs.tf"
	}
	grep -A1 '^  service_access = {' "$ROOT/infra/runtime/services.tf" | grep -q '^    public = '
	grep -A2 '^  service_access = {' "$ROOT/infra/runtime/services.tf" | grep -q '^    worker = '
	[ "$PUBLIC_SERVICE" = archfit-public ] && [ "$WORKER_SERVICE" = archfit-worker ]
	grep -q 'repository_id = "archfit-app"' "$ROOT/infra/data/registry.tf"
	[ "$IMAGE_REPO" = "$REGION-docker.pkg.dev/$PROJECT_ID/archfit-app/archfit-app" ]
}

@test "the expected queue routes and identities match the Terraform" {
	# shellcheck source=../lib.sh
	source "$DEPLOY/lib.sh"
	grep -q 'webhooks = "/internal/webhooks"' "$ROOT/infra/runtime/variables.tf"
	grep -q 'grants   = "/internal/grants"' "$ROOT/infra/runtime/variables.tf"
	[[ " ${QUEUE_ROUTES[*]} " == *" archfit-webhooks=/internal/webhooks "* ]]
	[[ " ${QUEUE_ROUTES[*]} " == *" archfit-operator=/internal/grants "* ]]
	grep -q "webhooks = \"${QUEUE_ROUTES[0]%%=*}\"" "$ROOT/infra/runtime/queues.tf"
	grep -q "operator = \"${QUEUE_ROUTES[1]%%=*}\"" "$ROOT/infra/runtime/queues.tf"
	grep -q "https://archfit-worker-\${local.data.project_number}.\${var.region}.run.app" "$ROOT/infra/runtime/services.tf"
	[ "$WORKER_HOST" = "archfit-worker-335297348272.$REGION.run.app" ]
	grep -q "\`https://$WORKER_HOST\`" "$ROOT/infra/README.md"
	# shellcheck disable=SC2016 # a literal Terraform expression
	grep -q 'account_id   = "archfit-${each.key}"' "$ROOT/infra/data/identities.tf"
	grep -q '^    invoker = ' "$ROOT/infra/data/identities.tf"
	[ "$INVOKER" = "archfit-invoker@$PROJECT_ID.iam.gserviceaccount.com" ]
}

@test "the workflow names the project number of the worker host" {
	# shellcheck source=../lib.sh
	source "$DEPLOY/lib.sh"
	local number=${WORKER_HOST#archfit-worker-}
	number=${number%%.*}
	grep -q "workload_identity_provider: projects/$number/locations/global/workloadIdentityPools/github/providers/archfit-app" "$ROOT/.github/workflows/deploy.yaml"
	grep -q "service_account: archfit-deploy@$PROJECT_ID.iam.gserviceaccount.com" "$ROOT/.github/workflows/deploy.yaml"
}

@test "a push that skips the release job does not join the release concurrency group" {
	run grep -n '^concurrency:' "$ROOT/.github/workflows/deploy.yaml"
	[ "$status" -eq 1 ]
	grep -q '^    concurrency:' "$ROOT/.github/workflows/deploy.yaml"
}
