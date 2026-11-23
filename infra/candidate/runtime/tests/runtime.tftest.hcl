mock_provider "google" {}

override_data {
  target = data.terraform_remote_state.data
  values = {
    outputs = {
      project_id         = "coupling-dev-510710"
      project_number     = "335297348272"
      region             = "europe-west1"
      firestore_database = "archfit-candidate"
      service_accounts = {
        public  = "archfit-candidate-public@coupling-dev-510710.iam.gserviceaccount.com"
        worker  = "archfit-candidate-worker@coupling-dev-510710.iam.gserviceaccount.com"
        invoker = "archfit-candidate-invoker@coupling-dev-510710.iam.gserviceaccount.com"
      }
      secret_ids = {
        "github_private_key"    = "archfit-candidate-github-private-key"
        "github_webhook_secret" = "archfit-candidate-github-webhook-secret"
        "github_oauth_secret"   = "archfit-candidate-github-oauth-secret"
        "cookie_key"            = "archfit-candidate-cookie-key"
        "operator_token"        = "archfit-candidate-operator-token"
      }
      image_repository = "europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app-candidate/archfit-app"
    }
  }
}

variables {
  data_state_bucket      = "candidate-state"
  github_app_id          = "123"
  github_oauth_client_id = "candidate"
  operators              = [123]
  approved_accounts      = ["candidate"]
  image_digests          = { public = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", worker = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }
  secret_versions        = { github_private_key = "1", github_webhook_secret = "1", github_oauth_secret = "1", cookie_key = "1", operator_token = "1" }
}

run "both_roles_use_candidate_database" {
  command = plan
  assert {
    condition     = alltrue([for service in google_cloud_run_v2_service.this : contains([for env in service.template[0].containers[0].env : "${env.name}=${env.value}"], "ARCHFIT_FIRESTORE_DATABASE=archfit-candidate")]) && length(google_cloud_run_v2_service.this) == 2
    error_message = "Both services must use the candidate database."
  }
}

run "reject_wrong_database" {
  command = plan
  override_data {
    target = data.terraform_remote_state.data
    values = {
      outputs = {
        project_id         = "coupling-dev-510710"
        project_number     = "335297348272"
        region             = "europe-west1"
        firestore_database = "(default)"
        service_accounts = {
          public  = "archfit-candidate-public@coupling-dev-510710.iam.gserviceaccount.com"
          worker  = "archfit-candidate-worker@coupling-dev-510710.iam.gserviceaccount.com"
          invoker = "archfit-candidate-invoker@coupling-dev-510710.iam.gserviceaccount.com"
        }
        secret_ids = {
          github_private_key    = "archfit-candidate-github-private-key"
          github_webhook_secret = "archfit-candidate-github-webhook-secret"
          github_oauth_secret   = "archfit-candidate-github-oauth-secret"
          cookie_key            = "archfit-candidate-cookie-key"
          operator_token        = "archfit-candidate-operator-token"
        }
        image_repository = "europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app-candidate/archfit-app"
      }
    }
  }
  expect_failures = [google_cloud_run_v2_service.this["public"], google_cloud_run_v2_service.this["worker"]]
}

run "candidate_host_only" {
  command = plan
  assert {
    condition     = alltrue([for service in google_cloud_run_v2_service.this : contains([for env in service.template[0].containers[0].env : "${env.name}=${env.value}"], "ARCHFIT_BASE_URL=https://candidate.coupling.dev")])
    error_message = "Both roles must use only the candidate host."
  }
}

run "reject_wrong_identity" {
  command = plan
  override_data {
    target = data.terraform_remote_state.data
    values = {
      outputs = {
        project_id         = "coupling-dev-510710"
        project_number     = "335297348272"
        region             = "europe-west1"
        firestore_database = "archfit-candidate"
        service_accounts = {
          public  = "archfit-candidate-public@coupling-dev-510710.iam.gserviceaccount.com"
          worker  = "archfit-candidate-worker@coupling-dev-510710.iam.gserviceaccount.com"
          invoker = "archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com"
        }
        secret_ids = {
          github_private_key    = "archfit-candidate-github-private-key"
          github_webhook_secret = "archfit-candidate-github-webhook-secret"
          github_oauth_secret   = "archfit-candidate-github-oauth-secret"
          cookie_key            = "archfit-candidate-cookie-key"
          operator_token        = "archfit-candidate-operator-token"
        }
        image_repository = "europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app-candidate/archfit-app"
      }
    }
  }
  expect_failures = [google_cloud_run_v2_service.this["public"], google_cloud_run_v2_service.this["worker"]]
}


run "private_worker_and_paused_dispatch" {
  command = plan
  assert {
    condition     = google_cloud_run_v2_service.this["worker"].ingress == "INGRESS_TRAFFIC_INTERNAL_ONLY" && !google_cloud_run_v2_service.this["worker"].invoker_iam_disabled && google_cloud_run_v2_service.this["worker"].custom_audiences == tolist(["https://archfit-candidate-worker-335297348272.europe-west1.run.app"])
    error_message = "Worker must be internal with candidate audience and IAM enabled."
  }
  assert {
    condition     = alltrue([for queue in google_cloud_tasks_queue.this : queue.desired_state == "PAUSED" && queue.http_target[0].uri_override[0].uri_override_enforce_mode == "ALWAYS" && queue.http_target[0].oidc_token[0].audience == "https://archfit-candidate-worker-335297348272.europe-west1.run.app"]) && google_cloud_scheduler_job.sweep.paused
    error_message = "Dispatch must start paused and use the enforced candidate worker target."
  }
  assert {
    condition     = google_cloud_run_v2_service_iam_member.worker_invoker.member == "serviceAccount:archfit-candidate-invoker@coupling-dev-510710.iam.gserviceaccount.com" && google_cloud_tasks_queue_iam_member.public_enqueuer.name == "archfit-candidate-webhooks"
    error_message = "Only candidate principals and webhook queue may be granted access."
  }
}

run "role_secret_isolation" {
  command = plan
  assert {
    condition     = toset([for v in google_cloud_run_v2_service.this["public"].template[0].volumes : v.secret[0].secret]) == toset(["archfit-candidate-github-private-key", "archfit-candidate-github-webhook-secret", "archfit-candidate-github-oauth-secret", "archfit-candidate-cookie-key"]) && toset([for v in google_cloud_run_v2_service.this["worker"].template[0].volumes : v.secret[0].secret]) == toset(["archfit-candidate-github-private-key", "archfit-candidate-github-webhook-secret", "archfit-candidate-operator-token"])
    error_message = "Secrets must be mounted only by the roles that need them."
  }
  assert {
    condition     = alltrue([for service in google_cloud_run_v2_service.this : service.deletion_protection])
    error_message = "Candidate service deletion must be separately reviewed."
  }
}

run "reject_production_app_id" {
  command = plan
  variables { github_app_id = "5348120" }
  expect_failures = [google_cloud_run_v2_service.this["public"], google_cloud_run_v2_service.this["worker"]]
}

run "reject_production_oauth_id" {
  command = plan
  variables { github_oauth_client_id = "Iv23lix1DdWQpCdiyYA7" } # gitleaks:allow -- public OAuth client ID, not a client secret
  expect_failures = [google_cloud_run_v2_service.this["public"], google_cloud_run_v2_service.this["worker"]]
}

run "reject_mutable_secret_version" {
  command = plan
  variables { secret_versions = { github_private_key = "latest", github_webhook_secret = "1", github_oauth_secret = "1", cookie_key = "1", operator_token = "1" } }
  expect_failures = [google_cloud_run_v2_service.this["public"], google_cloud_run_v2_service.this["worker"]]
}

run "reject_image_tag" {
  command = plan
  variables { image_digests = { public = "latest", worker = "latest" } }
  expect_failures = [google_cloud_run_v2_service.this["public"], google_cloud_run_v2_service.this["worker"]]
}

run "reject_missing_admission" {
  command = plan
  variables { approved_accounts = [] }
  expect_failures = [google_cloud_run_v2_service.this["public"], google_cloud_run_v2_service.this["worker"]]
}

run "reject_missing_operators" {
  command = plan
  variables { operators = [] }
  expect_failures = [google_cloud_run_v2_service.this["public"], google_cloud_run_v2_service.this["worker"]]
}

run "fixed_domain_and_worker_routes" {
  command = plan
  assert {
    condition     = google_cloud_run_domain_mapping.public.name == "candidate.coupling.dev" && google_cloud_run_domain_mapping.public.spec[0].route_name == "archfit-candidate-public"
    error_message = "Only the candidate domain may map to the candidate public service."
  }
  assert {
    condition = alltrue([
      for queue, path in { webhooks = "/internal/webhooks", operator = "/internal/grants" } :
      google_cloud_tasks_queue.this[queue].name == "archfit-candidate-${queue}" &&
      google_cloud_tasks_queue.this[queue].http_target[0].uri_override[0].host == "archfit-candidate-worker-335297348272.europe-west1.run.app" &&
      google_cloud_tasks_queue.this[queue].http_target[0].uri_override[0].path_override[0].path == path &&
      google_cloud_tasks_queue.this[queue].http_target[0].oidc_token[0].service_account_email == "archfit-candidate-invoker@coupling-dev-510710.iam.gserviceaccount.com"
    ])
    error_message = "Each named queue must use its fixed candidate worker route and invoker."
  }
  assert {
    condition     = google_cloud_scheduler_job.sweep.http_target[0].uri == "https://archfit-candidate-worker-335297348272.europe-west1.run.app/internal/sweep" && google_cloud_tasks_queue_iam_member.public_enqueuer.role == "roles/cloudtasks.enqueuer" && google_cloud_tasks_queue_iam_member.public_enqueuer.member == "serviceAccount:archfit-candidate-public@coupling-dev-510710.iam.gserviceaccount.com"
    error_message = "The sweep must reach only the candidate worker and enqueue permission must be scoped to the candidate public identity."
  }
}
