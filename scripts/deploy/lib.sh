# shellcheck shell=bash disable=SC2034 # the settings are read by the scripts that source this file
# Shared settings and helpers of the release scripts. Sourced, never run.
#
# The values come from the Terraform in infra/ (variable defaults, names and outputs of the data and runtime stacks).
# They are not secret. scripts/deploy/test/deploy.bats checks each of them against the Terraform code.

PROJECT_ID=coupling-dev-510710
REGION=europe-west1
IMAGE_REPO=europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app/archfit-app
PUBLIC_SERVICE=archfit-public
WORKER_SERVICE=archfit-worker
CANARY_TAG=canary

# The routing that security.md "Worker protection" requires.
WORKER_HOST=archfit-worker-335297348272.europe-west1.run.app
INVOKER=archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com
QUEUE_ROUTES=(
	archfit-webhooks=/internal/webhooks
	archfit-operator=/internal/grants
)

# The canary readiness check. GET /ready keeps a failed result for 5 seconds.
READY_ATTEMPTS=${READY_ATTEMPTS:-12}
READY_DELAY=${READY_DELAY:-10}

# Every release record goes to this folder. The workflow uploads it as an artifact.
RELEASE_DIR=${RELEASE_DIR:-release}

log() {
	printf '%s\n' "$*" >&2
}

die() {
	if [[ -n ${GITHUB_ACTIONS:-} ]]; then
		printf '::error::%s\n' "$*" >&2
	else
		printf 'error: %s\n' "$*" >&2
	fi
	exit 1
}

# summary appends Markdown to the job summary when the workflow provides one.
summary() {
	if [[ -n ${GITHUB_STEP_SUMMARY:-} ]]; then
		printf '%s\n' "$@" >>"$GITHUB_STEP_SUMMARY"
	fi
}

require_digest() {
	[[ $1 =~ ^sha256:[0-9a-f]{64}$ ]] || die "not an image digest: '$1' (want sha256:<64 hex>)"
}

gcloud_run() {
	gcloud "$@" --region="$REGION" --project="$PROJECT_ID" --quiet
}

image_digest() {
	gcloud artifacts docker images describe "$1" --project="$PROJECT_ID" --format='value(image_summary.digest)'
}

service_json() {
	gcloud_run run services describe "$1" --format=json
}

revision_json() {
	gcloud_run run revisions describe "$1" --format=json
}

# revision_is_ready reads a revision JSON on stdin and succeeds when its Ready condition is True.
revision_is_ready() {
	jq -e '[.status.conditions[]? | select(.type == "Ready" and .status == "True")] | length == 1' >/dev/null
}

# serving_traffic prints the revisions with traffic of a service as "revision=percent", one per line.
serving_traffic() {
	service_json "$1" | jq -r '.status.traffic[]? | select((.percent // 0) > 0) | "\(.revisionName)=\(.percent)"'
}

# promote sends all traffic of a service to one named revision. It never uses "latest".
promote() {
	local service=$1 revision=$2
	log "promote: $service -> $revision"
	gcloud_run run services update-traffic "$service" --to-revisions="$revision=100"
}

# drop_canary removes the canary tag. Its URL bypasses traffic, so a revision without traffic must not keep it.
drop_canary() {
	log "untag: $PUBLIC_SERVICE $CANARY_TAG"
	gcloud_run run services update-traffic "$PUBLIC_SERVICE" --remove-tags="$CANARY_TAG"
}
