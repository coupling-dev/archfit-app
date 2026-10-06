# One service account per component, with only the roles it needs. No service-account keys exist.
locals {
  runtime_accounts = {
    public  = "archfit public service: intake, uploads and the UI"
    worker  = "archfit worker service: queued webhooks, the sweep and grants"
    invoker = "OIDC identity of the queues and the Scheduler; the only caller of the worker"
  }

  # Firestore has no per-collection IAM. Tenant isolation is a code property.
  state_users = ["public", "worker"]
}

resource "google_service_account" "runtime" {
  for_each = local.runtime_accounts

  account_id   = "archfit-${each.key}"
  display_name = "archfit ${each.key}"
  description  = each.value
}

resource "google_project_iam_member" "state_user" {
  for_each = toset(local.state_users)

  project = var.project_id
  role    = "roles/datastore.user"
  member  = google_service_account.runtime[each.key].member
}

# The deploy identity pushes images and deploys revisions. It gets no IAM, service-usage or quota rights.
resource "google_service_account" "deploy" {
  account_id   = "archfit-deploy"
  display_name = "archfit deploy"
  description  = "CI deploy identity through Workload Identity Federation"
}

resource "google_project_iam_member" "deploy_run_developer" {
  project = var.project_id
  role    = "roles/run.developer"
  member  = google_service_account.deploy.member
}

resource "google_artifact_registry_repository_iam_member" "deploy_writer" {
  location   = google_artifact_registry_repository.app.location
  repository = google_artifact_registry_repository.app.name
  role       = "roles/artifactregistry.writer"
  member     = google_service_account.deploy.member
}

# A deploy must run the revision as its service account, so the deploy identity can act as the two service accounts
# that run revisions, and as no other.
resource "google_service_account_iam_member" "deploy_acts_as" {
  for_each = toset(["public", "worker"])

  service_account_id = google_service_account.runtime[each.key].name
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.deploy.member
}

# A webhook task names the invoker in its own oidcToken, and Cloud Tasks requires the task creator to act as that
# account. The queue-level uriOverride still pins the route, so the token reaches only the webhook route.
resource "google_service_account_iam_member" "public_acts_as_invoker" {
  service_account_id = google_service_account.runtime["invoker"].name
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.runtime["public"].member
}
