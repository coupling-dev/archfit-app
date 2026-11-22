locals {
  project = "coupling-dev-510710"
  region  = "europe-west1"
  secret_readers = {
    github_private_key    = ["public", "worker"]
    github_webhook_secret = ["public", "worker"]
    github_oauth_secret   = ["public"]
    cookie_key            = ["public"]
    operator_token        = ["worker"]
  }
  secret_ids = {
    github_private_key    = "archfit-823-github-private-key"
    github_webhook_secret = "archfit-823-github-webhook-secret"
    github_oauth_secret   = "archfit-823-github-oauth-secret"
    cookie_key            = "archfit-823-cookie-key"
    operator_token        = "archfit-823-operator-token"
  }
  secret_access = merge([for secret, roles in local.secret_readers : {
    for role in roles : "${secret}/${role}" => { secret = secret, role = role }
  }]...)
}

resource "google_firestore_database" "candidate" {
  project                     = local.project
  name                        = "archfit-823"
  location_id                 = local.region
  type                        = "FIRESTORE_NATIVE"
  app_engine_integration_mode = "DISABLED"
  delete_protection_state     = "DELETE_PROTECTION_ENABLED"
  deletion_policy             = "PREVENT"
}
resource "google_service_account" "runtime" {
  for_each   = toset(["public", "worker", "invoker"])
  project    = local.project
  account_id = "archfit-823-${each.key}"
}
resource "google_project_iam_member" "state_user" {
  for_each = toset(["public", "worker"])
  project  = local.project
  role     = "roles/datastore.user"
  member   = google_service_account.runtime[each.key].member
  condition {
    title      = "candidate_database_only"
    expression = "resource.name == \"projects/coupling-dev-510710/databases/archfit-823\""
  }
}
resource "google_service_account_iam_member" "public_acts_as_invoker" {
  service_account_id = google_service_account.runtime["invoker"].name
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.runtime["public"].member
}
resource "google_secret_manager_secret" "this" {
  for_each        = local.secret_ids
  project         = local.project
  secret_id       = each.value
  deletion_policy = "PREVENT"
  replication {
    user_managed {
      replicas {
        location = local.region
      }
    }
  }
}
resource "google_secret_manager_secret_iam_member" "accessor" {
  for_each  = local.secret_access
  secret_id = google_secret_manager_secret.this[each.value.secret].id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.runtime[each.value.role].member
}
resource "google_artifact_registry_repository" "app" {
  project       = local.project
  location      = local.region
  repository_id = "archfit-app-823"
  format        = "DOCKER"
}
