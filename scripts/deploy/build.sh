#!/usr/bin/env bash
# Builds and pushes the one image of a main commit, and prints its digest (sha256:...).
# A rerun for the same commit reuses the pushed image, so each commit has one image.
#
# Usage: build.sh <commit SHA>
set -euo pipefail

# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

commit=${1:-}
[[ $commit =~ ^[0-9a-f]{40}$ ]] || die "not a commit SHA: '$commit'"
image="$IMAGE_REPO:$commit"

image_digest() {
	gcloud artifacts docker images describe "$1" --project="$PROJECT_ID" --format='value(image_summary.digest)'
}

if digest=$(image_digest "$image" 2>/dev/null) && [[ -n $digest ]]; then
	log "build: $image exists, reusing it"
else
	gcloud auth configure-docker "${IMAGE_REPO%%/*}" --quiet
	docker build --platform=linux/amd64 --label="org.opencontainers.image.revision=$commit" --tag="$image" .
	docker push "$image"
	digest=$(image_digest "$image")
fi

require_digest "$digest"
printf '%s\n' "$digest"
