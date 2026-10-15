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

if digest=$(image_digest "$image" 2>/dev/null) && [[ -n $digest ]]; then
	log "build: $image exists, reusing it"
else
	# Progress goes to stderr: stdout carries only the digest.
	gcloud auth configure-docker "${IMAGE_REPO%%/*}" --quiet >&2
	docker build --platform=linux/amd64 --label="org.opencontainers.image.revision=$commit" --tag="$image" . >&2
	docker push "$image" >&2
	digest=$(image_digest "$image")
fi

require_digest "$digest"
mkdir -p "$RELEASE_DIR"
jq -n --arg commit "$commit" --arg digest "$digest" --arg image "$IMAGE_REPO@$digest" \
	'{commit: $commit, digest: $digest, image: $image}' >"$RELEASE_DIR/build.json"
summary "### App image built" "" "Image: $IMAGE_REPO@$digest" \
	"Use initial_image_digest=$digest for the first runtime plan. This build step does not deploy Cloud Run."
printf '%s\n' "$digest"
