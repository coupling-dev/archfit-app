#!/usr/bin/env bash
# Traffic rollback from operations.md "Roll back": moves all traffic of a service back to a named earlier revision.
# Take the names from the "traffic before" of the bad release's record.
#
# It accepts only a revision that an earlier release created: the name has the release pattern
# <service>-r<run>-<attempt>, the revision belongs to the service, runs an image of this repository by digest, and is
# ready. A revision that Terraform or a person created never gets traffic. The public service moves first, the worker
# second: the reverse order of a release. Then the post-release checks run.
#
# For an image rollback, run release.sh with the digest of the last good image instead.
#
# Usage: rollback.sh
# Environment: PUBLIC_REVISION and WORKER_REVISION. Set one or both.
set -euo pipefail

here=$(dirname "${BASH_SOURCE[0]}")
# shellcheck source=lib.sh
source "$here/lib.sh"

targets=()
[[ -z ${PUBLIC_REVISION:-} ]] || targets+=("$PUBLIC_SERVICE=$PUBLIC_REVISION")
[[ -z ${WORKER_REVISION:-} ]] || targets+=("$WORKER_SERVICE=$WORKER_REVISION")
((${#targets[@]} > 0)) || die "set PUBLIC_REVISION, WORKER_REVISION, or both"

require_release_revision() {
	local service=$1 revision=$2 json
	[[ $revision =~ ^$service-r[0-9]+-[0-9]+$ ]] ||
		die "$revision is not a release revision of $service (want $service-r<run>-<attempt>)"
	json=$(revision_json "$revision") || die "cannot read revision $revision"
	jq -e --arg service "$service" --arg repo "$IMAGE_REPO" \
		'.metadata.labels["serving.knative.dev/service"] == $service and
		 (.spec.containers[0].image | startswith($repo + "@") and (ltrimstr($repo + "@") | test("^sha256:[0-9a-f]{64}$")))' \
		<<<"$json" >/dev/null ||
		die "revision $revision does not belong to $service or does not run an image of $IMAGE_REPO by digest"
	revision_is_ready <<<"$json" || die "revision $revision is not ready"
}

# Check every target before any traffic moves.
for target in "${targets[@]}"; do
	require_release_revision "${target%%=*}" "${target#*=}"
done

summary "### Traffic rollback" ""
for target in "${targets[@]}"; do
	promote "${target%%=*}" "${target#*=}"
	summary "- \`${target%%=*}\` → \`${target#*=}\`"
done
summary ""

"$here/check.sh"
