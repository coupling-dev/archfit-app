#!/usr/bin/env bash
# Post-release checks from security.md "Worker protection". The deploy identity can change ingress and the invoker
# check, so each release and rollback reads them back and fails if they differ from the design:
#
#  - The worker is not exposed: ingress is internal, the invoker check is on, and its IAM policy has exactly one
#    binding, roles/run.invoker for the invoker identity. Any other binding can grant run.routes.invoke.
#  - Each queue sends to its own worker route with the invoker identity (queue-level uriOverride, enforced).
#
# Usage: check.sh
set -euo pipefail

# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

failures=()
fail() {
	failures+=("$*")
}

check_worker() {
	local json policy ingress invoker_check_off bindings want
	json=$(service_json "$WORKER_SERVICE") || {
		fail "cannot read service $WORKER_SERVICE"
		return
	}
	ingress=$(jq -r '.metadata.annotations["run.googleapis.com/ingress"] // ""' <<<"$json")
	[[ $ingress == internal ]] || fail "$WORKER_SERVICE ingress is '$ingress', want 'internal'"
	invoker_check_off=$(jq -r '.metadata.annotations["run.googleapis.com/invoker-iam-disabled"] // "false"' <<<"$json")
	[[ $invoker_check_off == false ]] || fail "$WORKER_SERVICE has the invoker check off"

	policy=$(gcloud_run run services get-iam-policy "$WORKER_SERVICE" --format=json) || {
		fail "cannot read the IAM policy of $WORKER_SERVICE"
		return
	}
	bindings=$(jq -c '[.bindings[]? | {role, members: (.members | sort), condition}] | sort' <<<"$policy")
	want=$(jq -nc --arg member "serviceAccount:$INVOKER" '[{role: "roles/run.invoker", members: [$member], condition: null}]')
	[[ $bindings == "$want" ]] || fail "$WORKER_SERVICE IAM bindings are $bindings, want $want"
}

check_queue() {
	local queue=$1 path=$2 json got want
	json=$(gcloud tasks queues describe "$queue" --location="$REGION" --project="$PROJECT_ID" --format=json) || {
		fail "cannot read queue $queue"
		return
	}
	got=$(jq -c '.httpTarget | {
		method: .httpMethod, scheme: .uriOverride.scheme, host: .uriOverride.host, path: .uriOverride.pathOverride.path,
		enforce: .uriOverride.uriOverrideEnforceMode,
		identity: .oidcToken.serviceAccountEmail, audience: .oidcToken.audience}' <<<"$json")
	want=$(jq -nc --arg host "$WORKER_HOST" --arg path "$path" --arg identity "$INVOKER" '{
		method: "POST", scheme: "HTTPS", host: $host, path: $path, enforce: "ALWAYS",
		identity: $identity, audience: ("https://" + $host)}')
	[[ $got == "$want" ]] || fail "queue $queue routing is $got, want $want"
}

check_worker
for route in "${QUEUE_ROUTES[@]}"; do
	check_queue "${route%%=*}" "${route#*=}"
done

if ((${#failures[@]} > 0)); then
	summary "### Post-release checks: failed" ""
	for failure in "${failures[@]}"; do
		log "check: $failure"
		summary "- $failure"
	done
	die "post-release checks failed: the worker exposure or the queue routing differs from the design"
fi
log "check: worker exposure and queue routing match the design"
summary "### Post-release checks: passed" ""
