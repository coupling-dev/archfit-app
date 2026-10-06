# Secret Manager holds the secrets without values. The owner adds every version by hand, so no value enters Terraform
# state. Each service account can read only the secrets its role mounts. The public role never gets the operator token.
# The worker still needs the OAuth secret and the cookie key: the App reads them in every role until the worker stops
# building the UI. Only the public service calls the LLM, so only it gets the Anthropic key.
locals {
  secret_readers = {
    "archfit-github-private-key"    = ["public", "worker"]
    "archfit-github-webhook-secret" = ["public", "worker"]
    "archfit-github-oauth-secret"   = ["public", "worker"]
    "archfit-cookie-key"            = ["public", "worker"]
    "archfit-operator-token"        = ["worker"]
    "archfit-anthropic-key"         = ["public"]
  }

  secret_access = merge([
    for secret, roles in local.secret_readers : {
      for role in roles : "${secret}/${role}" => { secret = secret, role = role }
    }
  ]...)
}

resource "google_secret_manager_secret" "this" {
  for_each = local.secret_readers

  secret_id = each.key

  # Secrets stay in the one region, like the tenant state.
  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }

  deletion_policy = "PREVENT"

  depends_on = [google_project_service.this]
}

resource "google_secret_manager_secret_iam_member" "accessor" {
  for_each = local.secret_access

  secret_id = google_secret_manager_secret.this[each.value.secret].id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.runtime[each.value.role].member
}
