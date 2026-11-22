mock_provider "google" {}

override_resource {
  target = google_service_account.runtime["public"]
  values = {
    email  = "archfit-823-public@coupling-dev-510710.iam.gserviceaccount.com"
    member = "serviceAccount:archfit-823-public@coupling-dev-510710.iam.gserviceaccount.com"
    name   = "projects/coupling-dev-510710/serviceAccounts/archfit-823-public@coupling-dev-510710.iam.gserviceaccount.com"
  }
}

override_resource {
  target = google_service_account.runtime["worker"]
  values = {
    email  = "archfit-823-worker@coupling-dev-510710.iam.gserviceaccount.com"
    member = "serviceAccount:archfit-823-worker@coupling-dev-510710.iam.gserviceaccount.com"
    name   = "projects/coupling-dev-510710/serviceAccounts/archfit-823-worker@coupling-dev-510710.iam.gserviceaccount.com"
  }
}

override_resource {
  target = google_service_account.runtime["invoker"]
  values = {
    email  = "archfit-823-invoker@coupling-dev-510710.iam.gserviceaccount.com"
    member = "serviceAccount:archfit-823-invoker@coupling-dev-510710.iam.gserviceaccount.com"
    name   = "projects/coupling-dev-510710/serviceAccounts/archfit-823-invoker@coupling-dev-510710.iam.gserviceaccount.com"
  }
}

override_resource {
  target = google_secret_manager_secret.this["github_private_key"]
  values = { id = "projects/coupling-dev-510710/secrets/archfit-823-github-private-key" }
}

override_resource {
  target = google_secret_manager_secret.this["github_webhook_secret"]
  values = { id = "projects/coupling-dev-510710/secrets/archfit-823-github-webhook-secret" }
}

override_resource {
  target = google_secret_manager_secret.this["github_oauth_secret"]
  values = { id = "projects/coupling-dev-510710/secrets/archfit-823-github-oauth-secret" }
}

override_resource {
  target = google_secret_manager_secret.this["cookie_key"]
  values = { id = "projects/coupling-dev-510710/secrets/archfit-823-cookie-key" }
}

override_resource {
  target = google_secret_manager_secret.this["operator_token"]
  values = { id = "projects/coupling-dev-510710/secrets/archfit-823-operator-token" }
}

run "candidate_database_is_isolated" {
  command = plan

  assert {
    condition     = google_firestore_database.candidate.name == "archfit-823" && google_firestore_database.candidate.type == "FIRESTORE_NATIVE" && google_firestore_database.candidate.deletion_policy == "PREVENT"
    error_message = "The candidate must use its protected named Native database."
  }
}

run "candidate_image_repository" {
  command = plan

  assert {
    condition     = output.image_repository == "europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app-823/archfit-app"
    error_message = "The image path must select the candidate repository and App image."
  }
}

run "candidate_access_and_outputs" {
  command = plan

  assert {
    condition     = output.project_id == "coupling-dev-510710" && output.project_number == "335297348272" && output.region == "europe-west1" && output.firestore_database == "archfit-823" && google_firestore_database.candidate.delete_protection_state == "DELETE_PROTECTION_ENABLED"
    error_message = "Candidate project, region, database and deletion protection must be fixed."
  }
  assert {
    condition     = toset(keys(google_service_account.runtime)) == toset(["public", "worker", "invoker"]) && alltrue([for k, v in google_service_account.runtime : v.account_id == "archfit-823-${k}" && v.project == "coupling-dev-510710"])
    error_message = "Only candidate service accounts may be created."
  }
  assert {
    condition     = toset(keys(google_project_iam_member.state_user)) == toset(["public", "worker"]) && alltrue([for k, v in google_project_iam_member.state_user : v.project == "coupling-dev-510710" && v.role == "roles/datastore.user" && v.member == "serviceAccount:archfit-823-${k}@coupling-dev-510710.iam.gserviceaccount.com" && v.condition[0].expression == "resource.name == \"projects/coupling-dev-510710/databases/archfit-823\""])
    error_message = "Only public and worker may access the named database."
  }
  assert {
    condition     = google_service_account_iam_member.public_acts_as_invoker.role == "roles/iam.serviceAccountUser" && google_service_account_iam_member.public_acts_as_invoker.member == "serviceAccount:archfit-823-public@coupling-dev-510710.iam.gserviceaccount.com" && google_service_account_iam_member.public_acts_as_invoker.service_account_id == "projects/coupling-dev-510710/serviceAccounts/archfit-823-invoker@coupling-dev-510710.iam.gserviceaccount.com"
    error_message = "Public may act only as the candidate invoker."
  }
  assert {
    condition = output.secret_ids == {
      github_private_key    = "archfit-823-github-private-key"
      github_webhook_secret = "archfit-823-github-webhook-secret"
      github_oauth_secret   = "archfit-823-github-oauth-secret"
      cookie_key            = "archfit-823-cookie-key"
      operator_token        = "archfit-823-operator-token"
    } && toset(keys(google_secret_manager_secret.this)) == toset(keys(output.secret_ids))
    error_message = "Only the five candidate secret containers are allowed."
  }
  assert {
    condition     = toset(keys(google_secret_manager_secret_iam_member.accessor)) == toset(["github_private_key/public", "github_private_key/worker", "github_webhook_secret/public", "github_webhook_secret/worker", "github_oauth_secret/public", "cookie_key/public", "operator_token/worker"]) && alltrue([for k, v in google_secret_manager_secret_iam_member.accessor : v.role == "roles/secretmanager.secretAccessor" && v.member == "serviceAccount:archfit-823-${split("/", k)[1]}@coupling-dev-510710.iam.gserviceaccount.com"])
    error_message = "Secret access must match the candidate role matrix."
  }
  assert {
    condition     = alltrue([for k, v in google_secret_manager_secret_iam_member.accessor : v.secret_id == "projects/coupling-dev-510710/secrets/${output.secret_ids[split("/", k)[0]]}"])
    error_message = "Every secret grant must target its matching candidate container."
  }
  assert {
    condition     = google_artifact_registry_repository.app.project == "coupling-dev-510710" && google_artifact_registry_repository.app.location == "europe-west1" && google_artifact_registry_repository.app.repository_id == "archfit-app-823" && google_artifact_registry_repository.app.format == "DOCKER"
    error_message = "The image repository must be candidate-only."
  }

}
