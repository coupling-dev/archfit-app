#!/usr/bin/env bash
# Releases one image to both services, as operations.md "Release" requires:
#
#  1. Deploy the worker, then the public service as a tagged canary. Both new revisions get no traffic.
#  2. Check that each new revision is the one this release created, with this image.
#  3. Check the canary with GET /ready. If it fails, no traffic moves.
#  4. Promote the worker, then the public service, each to its named revision. Never "latest".
#  5. Run the post-release checks (check.sh).
#
# It writes the release record to $RELEASE_DIR/release.json and to the job summary, also when it fails.
# An image rollback runs this script with the digest of an earlier image.
#
# Usage: release.sh <image digest>
# Environment: GITHUB_RUN_ID and GITHUB_RUN_ATTEMPT name the revisions. GITHUB_SHA is recorded.
set -euo pipefail

here=$(dirname "${BASH_SOURCE[0]}")
# shellcheck source=lib.sh
source "$here/lib.sh"

digest=${1:-}
require_digest "$digest"
image="$IMAGE_REPO@$digest"

run_id=${GITHUB_RUN_ID:-}
attempt=${GITHUB_RUN_ATTEMPT:-1}
[[ $run_id =~ ^[0-9]+$ && $attempt =~ ^[0-9]+$ ]] || die "GITHUB_RUN_ID and GITHUB_RUN_ATTEMPT must be numbers"
# The run names the revisions, so a revision name is unique and the release knows it before the deploy.
suffix="r$run_id-$attempt"
worker_revision="$WORKER_SERVICE-$suffix"
public_revision="$PUBLIC_SERVICE-$suffix"

status=failed
step=start
worker_before=""
public_before=""
canary_url=""

write_record() {
	mkdir -p "$RELEASE_DIR"
	jq -n \
		--arg status "$status" --arg step "$step" --arg commit "${GITHUB_SHA:-}" --arg image "$image" \
		--arg digest "$digest" --arg run "$run_id" --arg attempt "$attempt" \
		--arg worker "$worker_revision" --arg worker_before "$worker_before" \
		--arg public "$public_revision" --arg public_before "$public_before" --arg canary "$canary_url" \
		'def lines: split("\n") | map(select(. != ""));
		{status: $status, step: $step, commit: $commit, image: $image, digest: $digest, run: $run, attempt: $attempt,
		 worker: {revision: $worker, traffic_before: ($worker_before | lines)},
		 public: {revision: $public, traffic_before: ($public_before | lines), canary_url: $canary}}' \
		>"$RELEASE_DIR/release.json"
	summary \
		"### Release: $status" \
		"" \
		"| Field | Value |" \
		"| --- | --- |" \
		"| Image | \`$image\` |" \
		"| Worker revision | \`$worker_revision\` |" \
		"| Public revision | \`$public_revision\` |" \
		"| Worker traffic before | \`${worker_before//$'\n'/ }\` |" \
		"| Public traffic before | \`${public_before//$'\n'/ }\` |" \
		"| Stopped at | $step |" \
		""
}
trap write_record EXIT

deploy() {
	local service=$1
	shift
	log "deploy: $service <- $image (no traffic)"
	gcloud_run run deploy "$service" --image="$image" --revision-suffix="$suffix" --no-traffic "$@"
}

# require_ours stops the release unless the revision belongs to the service, runs this image, and is ready.
require_ours() {
	local service=$1 revision=$2 json
	json=$(revision_json "$revision") || die "cannot read revision $revision"
	jq -e --arg service "$service" --arg image "$image" \
		'.metadata.labels["serving.knative.dev/service"] == $service and .spec.containers[0].image == $image' \
		<<<"$json" >/dev/null ||
		die "revision $revision is not the $service revision of this release with $image; nothing promoted"
	revision_is_ready <<<"$json" || die "revision $revision is not ready; nothing promoted"
}

# check_ready calls GET /ready on the canary until it answers 200 or the attempts run out.
check_ready() {
	local url="$1/ready" attempt_no response code
	for ((attempt_no = 1; attempt_no <= READY_ATTEMPTS; attempt_no++)); do
		response=$(curl --silent --show-error --max-time 10 --write-out '\n%{http_code}' "$url" 2>&1) || true
		code=${response##*$'\n'}
		if [[ $code == 200 ]]; then
			log "ready: $url answered 200"
			return 0
		fi
		log "ready: attempt $attempt_no of $READY_ATTEMPTS: HTTP $code: ${response%$'\n'*}"
		if ((attempt_no < READY_ATTEMPTS)); then
			sleep "$READY_DELAY"
		fi
	done
	return 1
}

step="image lookup"
gcloud artifacts docker images describe "$image" --project="$PROJECT_ID" --format='value(image_summary.digest)' >/dev/null ||
	die "image $image is not in Artifact Registry"

step="traffic before"
worker_before=$(serving_traffic "$WORKER_SERVICE")
public_before=$(serving_traffic "$PUBLIC_SERVICE")

step="deploy worker"
deploy "$WORKER_SERVICE"
step="deploy public canary"
deploy "$PUBLIC_SERVICE" --tag="$CANARY_TAG"

step="verify revisions"
require_ours "$WORKER_SERVICE" "$worker_revision"
require_ours "$PUBLIC_SERVICE" "$public_revision"

step="canary readiness"
canary_url=$(service_json "$PUBLIC_SERVICE" | jq -r --arg tag "$CANARY_TAG" --arg revision "$public_revision" \
	'first(.status.traffic[]? | select(.tag == $tag and .revisionName == $revision) | .url) // ""')
[[ $canary_url == https://* ]] || die "tag $CANARY_TAG does not point at $public_revision; nothing promoted"
check_ready "$canary_url" || die "canary $public_revision is not ready; nothing promoted"

step="promote worker"
promote "$WORKER_SERVICE" "$worker_revision"
step="promote public"
promote "$PUBLIC_SERVICE" "$public_revision"

step="post-release checks"
"$here/check.sh"

step=finished
status=released
