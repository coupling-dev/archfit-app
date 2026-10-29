mock_provider "google" {
  mock_data "google_project" {
    defaults = {
      number = "335297348272"
    }
  }

  mock_resource "google_iam_workload_identity_pool" {
    defaults = {
      name = "projects/335297348272/locations/global/workloadIdentityPools/github"
    }
  }
}

# Each account gets its own identity, so a grant to the wrong account fails a test.
override_resource {
  target = google_service_account.runtime["public"]
  values = {
    email  = "archfit-public@coupling-dev-510710.iam.gserviceaccount.com"
    member = "serviceAccount:archfit-public@coupling-dev-510710.iam.gserviceaccount.com"
    name   = "projects/coupling-dev-510710/serviceAccounts/archfit-public@coupling-dev-510710.iam.gserviceaccount.com"
  }
}

override_resource {
  target = google_service_account.runtime["worker"]
  values = {
    email  = "archfit-worker@coupling-dev-510710.iam.gserviceaccount.com"
    member = "serviceAccount:archfit-worker@coupling-dev-510710.iam.gserviceaccount.com"
    name   = "projects/coupling-dev-510710/serviceAccounts/archfit-worker@coupling-dev-510710.iam.gserviceaccount.com"
  }
}

override_resource {
  target = google_service_account.runtime["invoker"]
  values = {
    email  = "archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com"
    member = "serviceAccount:archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com"
    name   = "projects/coupling-dev-510710/serviceAccounts/archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com"
  }
}

override_resource {
  target = google_service_account.deploy
  values = {
    email  = "archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com"
    member = "serviceAccount:archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com"
    name   = "projects/coupling-dev-510710/serviceAccounts/archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com"
  }
}

variables {
  import_existing      = false
  owner_email          = "owner@example.com"
  essential_contact_id = "123"
}

run "workload_identity_admits_one_repository_branch_and_workflow" {
  command = plan

  assert {
    condition = google_iam_workload_identity_pool_provider.github.attribute_condition == join(" && ", [
      "assertion.repository_id == \"1373981201\"",
      "assertion.ref == \"refs/heads/main\"",
      "assertion.workflow_ref == \"coupling-dev/archfit-app/.github/workflows/deploy.yaml@refs/heads/main\"",
    ])
    error_message = "The provider must admit only the repository ID, main and the deploy workflow."
  }

  assert {
    condition     = google_iam_workload_identity_pool_provider.github.oidc[0].issuer_uri == "https://token.actions.githubusercontent.com"
    error_message = "The provider must trust only the GitHub Actions issuer."
  }

  assert {
    condition     = endswith(google_service_account_iam_member.deploy_workload_identity.member, "/attribute.repository_id/1373981201")
    error_message = "Only the repository's principal set may impersonate the deploy account."
  }
}

run "deploy_identity_has_only_its_three_grants" {
  command = plan

  assert {
    condition     = google_project_iam_member.deploy_run_developer.role == "roles/run.developer" && google_project_iam_member.deploy_run_developer.member == "serviceAccount:archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com"
    error_message = "The deploy account's only project role is run.developer."
  }

  assert {
    condition     = google_artifact_registry_repository_iam_member.deploy_writer.role == "roles/artifactregistry.writer" && google_artifact_registry_repository_iam_member.deploy_writer.member == "serviceAccount:archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com"
    error_message = "The deploy account writes images only to the App repository."
  }

  assert {
    condition = toset([for m in google_service_account_iam_member.deploy_acts_as : "${m.service_account_id}|${m.role}|${m.member}"]) == toset([
      "projects/coupling-dev-510710/serviceAccounts/archfit-public@coupling-dev-510710.iam.gserviceaccount.com|roles/iam.serviceAccountUser|serviceAccount:archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com",
      "projects/coupling-dev-510710/serviceAccounts/archfit-worker@coupling-dev-510710.iam.gserviceaccount.com|roles/iam.serviceAccountUser|serviceAccount:archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com",
    ])
    error_message = "The deploy account may act only as the two accounts that run revisions, never as the invoker."
  }

  assert {
    condition     = google_service_account_iam_member.deploy_workload_identity.service_account_id == "projects/coupling-dev-510710/serviceAccounts/archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com"
    error_message = "Workload Identity may impersonate only the deploy account."
  }
}

run "each_secret_is_readable_only_by_its_roles" {
  command = plan

  assert {
    condition = toset([
      for k, m in google_secret_manager_secret_iam_member.accessor :
      "${split("/", k)[0]}|${m.role}|${trimsuffix(trimprefix(m.member, "serviceAccount:archfit-"), "@coupling-dev-510710.iam.gserviceaccount.com")}"
      if m.secret_id == google_secret_manager_secret.this[split("/", k)[0]].id
      ]) == toset([
      "archfit-github-private-key|roles/secretmanager.secretAccessor|public",
      "archfit-github-private-key|roles/secretmanager.secretAccessor|worker",
      "archfit-github-webhook-secret|roles/secretmanager.secretAccessor|public",
      "archfit-github-webhook-secret|roles/secretmanager.secretAccessor|worker",
      "archfit-github-oauth-secret|roles/secretmanager.secretAccessor|public",
      "archfit-cookie-key|roles/secretmanager.secretAccessor|public",
      "archfit-operator-token|roles/secretmanager.secretAccessor|worker",
      "archfit-anthropic-key|roles/secretmanager.secretAccessor|public",
    ])
    error_message = "Each role gets only its secrets: the worker cannot read OAuth, cookie or LLM secrets; public cannot read the operator token."
  }

  assert {
    condition     = alltrue([for s in google_secret_manager_secret.this : s.deletion_policy == "PREVENT"])
    error_message = "Secrets are data and must not be destroyed by Terraform."
  }
}

run "only_the_public_account_acts_as_the_invoker" {
  command = plan

  assert {
    condition = (
      google_service_account_iam_member.public_acts_as_invoker.service_account_id == "projects/coupling-dev-510710/serviceAccounts/archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com"
      && google_service_account_iam_member.public_acts_as_invoker.member == "serviceAccount:archfit-public@coupling-dev-510710.iam.gserviceaccount.com"
      && google_service_account_iam_member.public_acts_as_invoker.role == "roles/iam.serviceAccountUser"
    )
    error_message = "The public account names the invoker in its tasks, so it alone may act as the invoker."
  }
}

run "state_lives_in_the_one_region_and_cannot_be_destroyed" {
  command = plan

  assert {
    condition     = google_firestore_database.default.name == "(default)" && google_firestore_database.default.location_id == "europe-west1"
    error_message = "Only the default database is free, and its location is permanent."
  }

  assert {
    condition     = google_firestore_database.default.type == "FIRESTORE_NATIVE"
    error_message = "The Store driver needs Native mode."
  }

  assert {
    condition     = google_firestore_database.default.delete_protection_state == "DELETE_PROTECTION_ENABLED" && google_firestore_database.default.deletion_policy == "PREVENT"
    error_message = "The database must be protected in Google Cloud and in Terraform."
  }

  assert {
    condition     = google_firestore_field.kv_value.collection == "kv" && google_firestore_field.kv_value.field == "value" && length(google_firestore_field.kv_value.index_config[0].indexes) == 0
    error_message = "The value field of kv is exempt from single-field indexing."
  }

  assert {
    condition     = google_firestore_backup_schedule.daily.retention == "604800s"
    error_message = "Backups keep 7 days."
  }
}

run "runtime_accounts_get_state_access_and_nothing_else_at_project_level" {
  command = plan

  assert {
    condition     = toset([for m in google_project_iam_member.state_user : m.member]) == toset(["serviceAccount:archfit-public@coupling-dev-510710.iam.gserviceaccount.com", "serviceAccount:archfit-worker@coupling-dev-510710.iam.gserviceaccount.com"])
    error_message = "Only the public and worker accounts use Firestore; the invoker has no project role."
  }

  assert {
    condition     = alltrue([for m in google_project_iam_member.state_user : m.role == "roles/datastore.user"])
    error_message = "The runtime accounts get datastore.user only."
  }
}

run "apis_are_imported_and_never_disabled" {
  command = plan

  assert {
    condition     = alltrue([for s in google_project_service.this : s.disable_on_destroy == false])
    error_message = "Removing a service from the list must not disable the API."
  }

  assert {
    condition     = !contains(keys(google_project_service.this), "compute.googleapis.com") && !contains(keys(google_project_service.this), "pubsub.googleapis.com")
    error_message = "Terraform must not enable an API outside the documented list."
  }
}

run "guardrail_quotas_allow_only_the_one_region" {
  command = plan

  assert {
    condition = toset([for k, q in google_cloud_quotas_quota_preference.run : k if length(q.dimensions) > 0]) == toset([
      "run-cpu-europe-west1",
      "run-mem-europe-west1",
      "run-services-europe-west1",
    ])
    error_message = "Only the three europe-west1 preferences carry a dimension; the others apply to every region."
  }

  assert {
    condition = alltrue([
      for k, q in google_cloud_quotas_quota_preference.run :
      length(q.dimensions) == 0 || q.dimensions == tomap({ region = "europe-west1" })
    ])
    error_message = "A regional preference names only europe-west1."
  }

  assert {
    condition = alltrue([
      for k, q in google_cloud_quotas_quota_preference.run :
      endswith(k, "-europe-west1") || k == "run-max-regions" || q.quota_config[0].preferred_value == "0"
    ])
    error_message = "Every other region, GPU, job and worker-pool quota must be 0."
  }

  assert {
    condition     = google_cloud_quotas_quota_preference.run["run-max-regions"].quota_config[0].preferred_value == "1"
    error_message = "One region per project."
  }

  assert {
    condition     = google_cloud_quotas_quota_preference.run["run-cpu-europe-west1"].quota_config[0].preferred_value == "8000"
    error_message = "8 vCPU in europe-west1: 2 services x 2 instances x (live + new revision) x 1 vCPU must fit during a release."
  }

  assert {
    condition     = google_cloud_quotas_quota_preference.run["run-mem-europe-west1"].quota_config[0].preferred_value == "4294967296"
    error_message = "4 GiB in europe-west1: 8 instances x 512 MiB."
  }

  assert {
    condition     = length(google_cloud_quotas_quota_preference.run) == 13
    error_message = "All 13 existing quota preferences are managed."
  }
}
