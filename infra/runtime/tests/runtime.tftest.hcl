mock_provider "google" {}

# Each service gets its own URL, so a queue or job aimed at the wrong service fails a test.
override_resource {
  target = google_cloud_run_v2_service.this["public"]
  values = {
    uri = "https://archfit-public-335297348272.europe-west1.run.app"
  }
}

override_resource {
  target = google_cloud_run_v2_service.this["worker"]
  values = {
    uri = "https://archfit-worker-335297348272.europe-west1.run.app"
  }
}

override_data {
  target = data.terraform_remote_state.data
  values = {
    outputs = {
      service_accounts = {
        public  = "archfit-public@coupling-dev-510710.iam.gserviceaccount.com"
        worker  = "archfit-worker@coupling-dev-510710.iam.gserviceaccount.com"
        invoker = "archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com"
      }
      secret_ids = {
        "archfit-github-private-key"    = "archfit-github-private-key"
        "archfit-github-webhook-secret" = "archfit-github-webhook-secret"
        "archfit-github-oauth-secret"   = "archfit-github-oauth-secret"
        "archfit-cookie-key"            = "archfit-cookie-key"
        "archfit-operator-token"        = "archfit-operator-token"
        "archfit-anthropic-key"         = "archfit-anthropic-key"
      }
      secret_readers = {
        "archfit-github-private-key"    = ["public", "worker"]
        "archfit-github-webhook-secret" = ["public", "worker"]
        "archfit-github-oauth-secret"   = ["public"]
        "archfit-cookie-key"            = ["public"]
        "archfit-operator-token"        = ["worker"]
        "archfit-anthropic-key"         = ["public"]
      }
      deploy_service_account = "archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com"
      notification_channel   = "projects/coupling-dev-510710/notificationChannels/1"
      project_number         = "335297348272"
      image_repository       = "europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app/archfit-app"
    }
  }
}

variables {
  github_app_id          = "123456"
  github_oauth_client_id = "Iv1.test"
  initial_image_digest   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}

run "services_start_with_the_real_digest_pinned_app" {
  command = plan

  assert {
    condition = alltrue([
      for s in google_cloud_run_v2_service.this :
      s.template[0].containers[0].image == "europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app/archfit-app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    ])
    error_message = "New services run the real digest-pinned App, never a success-returning placeholder."
  }
}

run "initial_image_rejects_a_mutable_tag" {
  command = plan
  variables {
    initial_image_digest = "latest"
  }
  expect_failures = [var.initial_image_digest]
}

run "webhook_rotation_mounts_both_versions_on_each_role" {
  command = plan
  variables {
    webhook_secret_previous_version = "1"
    secret_versions = {
      "archfit-github-private-key"    = "1"
      "archfit-github-webhook-secret" = "2"
      "archfit-github-oauth-secret"   = "1"
      "archfit-cookie-key"            = "1"
      "archfit-operator-token"        = "1"
    }
  }

  assert {
    condition = alltrue([
      for s in google_cloud_run_v2_service.this :
      contains([for e in s.template[0].containers[0].env : "${e.name}=${e.value}"], "ARCHFIT_GITHUB_WEBHOOK_SECRET_PREVIOUS_FILE=/secrets/archfit-github-webhook-secret/previous")
    ])
    error_message = "Both signature verifiers receive the previous webhook secret during rotation."
  }

  assert {
    condition = alltrue([
      for s in google_cloud_run_v2_service.this :
      toset([for item in [for v in s.template[0].volumes : v if v.name == "archfit-github-webhook-secret"][0].secret[0].items : "${item.path}=${item.version}"]) == toset(["value=2", "previous=1"])
    ])
    error_message = "Rotation mounts two pinned versions of the same secret without adding another secret or IAM grant."
  }
}

run "previous_webhook_secret_is_absent_outside_rotation" {
  command = plan

  assert {
    condition = alltrue([
      for s in google_cloud_run_v2_service.this :
      !contains([for e in s.template[0].containers[0].env : e.name], "ARCHFIT_GITHUB_WEBHOOK_SECRET_PREVIOUS_FILE")
    ])
    error_message = "The previous secret file is configured only during rotation."
  }
}

run "previous_webhook_version_rejects_latest" {
  command = plan
  variables {
    webhook_secret_previous_version = "latest"
  }
  expect_failures = [var.webhook_secret_previous_version]
}

run "worker_is_internal_and_checks_the_invoker" {
  command = plan

  assert {
    condition     = google_cloud_run_v2_service.this["worker"].ingress == "INGRESS_TRAFFIC_INTERNAL_ONLY"
    error_message = "Only same-project Cloud Tasks and Cloud Scheduler may reach the worker."
  }

  assert {
    condition     = google_cloud_run_v2_service.this["worker"].invoker_iam_disabled == false
    error_message = "The worker's invoker check must be on."
  }

  assert {
    condition     = google_cloud_run_v2_service_iam_member.worker_invoker.member == "serviceAccount:archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com"
    error_message = "Only the invoker identity may call the worker."
  }

  assert {
    condition     = google_cloud_run_v2_service.this["public"].ingress == "INGRESS_TRAFFIC_ALL" && google_cloud_run_v2_service.this["public"].invoker_iam_disabled == true
    error_message = "The public service is open without an allUsers binding."
  }
}

run "each_service_runs_as_its_own_account_with_at_most_two_instances" {
  command = plan

  assert {
    condition     = alltrue([for s in google_cloud_run_v2_service.this : s.deletion_protection != false])
    error_message = "Deleting a service must require an explicit owner decision."
  }

  assert {
    condition = alltrue([
      for k, s in google_cloud_run_v2_service.this :
      s.template[0].service_account == "archfit-${k}@coupling-dev-510710.iam.gserviceaccount.com"
    ])
    error_message = "Each service must run as its own service account."
  }

  assert {
    condition     = tonumber(trimsuffix(google_cloud_run_v2_service.this["public"].template[0].timeout, "s")) > 300
    error_message = "The public timeout must exceed the 300 s deadline of the managed assist route."
  }

  assert {
    condition     = alltrue([for s in google_cloud_run_v2_service.this : s.template[0].scaling[0].max_instance_count == 2])
    error_message = "Two instances per service cap cost."
  }
}

run "secrets_are_pinned_files_mounted_per_role" {
  command = plan

  assert {
    condition = toset([for v in google_cloud_run_v2_service.this["public"].template[0].volumes : v.name]) == toset([
      "archfit-github-private-key",
      "archfit-github-webhook-secret",
      "archfit-github-oauth-secret",
      "archfit-cookie-key",
    ])
    error_message = "The public service mounts no operator token, and no Anthropic key until a version is set."
  }

  assert {
    condition = toset([for v in google_cloud_run_v2_service.this["worker"].template[0].volumes : v.name]) == toset([
      "archfit-github-private-key",
      "archfit-github-webhook-secret",
      "archfit-operator-token",
    ])
    error_message = "The worker mounts only its App key, webhook secret and operator token."
  }

  assert {
    condition = alltrue([
      for name in ["ARCHFIT_GITHUB_OAUTH_SECRET_FILE", "ARCHFIT_COOKIE_KEY_FILE", "ARCHFIT_ANTHROPIC_KEY_FILE"] :
      !contains([for e in google_cloud_run_v2_service.this["worker"].template[0].containers[0].env : e.name], name)
    ])
    error_message = "The worker has no file references to OAuth, cookie or LLM secrets."
  }

  assert {
    condition = alltrue(flatten([
      for s in google_cloud_run_v2_service.this : [for v in s.template[0].volumes : v.secret[0].items[0].version == "1"]
    ]))
    error_message = "Each revision pins a secret version; it never reads latest."
  }

  assert {
    condition = alltrue([
      for e in google_cloud_run_v2_service.this["worker"].template[0].containers[0].env :
      e.value == "/secrets/archfit-operator-token/value" if e.name == "ARCHFIT_OPERATOR_TOKEN_FILE"
    ]) && contains([for e in google_cloud_run_v2_service.this["worker"].template[0].containers[0].env : e.name], "ARCHFIT_OPERATOR_TOKEN_FILE")
    error_message = "A *_FILE variable names the mounted file."
  }

  assert {
    condition     = !contains([for e in google_cloud_run_v2_service.this["public"].template[0].containers[0].env : e.name], "ARCHFIT_OPERATOR_TOKEN_FILE")
    error_message = "The public role starts without the operator token."
  }
}

run "anthropic_key_mounts_only_on_the_public_service_when_set" {
  command = plan

  variables {
    secret_versions = {
      "archfit-github-private-key"    = "1"
      "archfit-github-webhook-secret" = "1"
      "archfit-github-oauth-secret"   = "1"
      "archfit-cookie-key"            = "1"
      "archfit-operator-token"        = "1"
      "archfit-anthropic-key"         = "3"
    }
  }

  assert {
    condition = anytrue([
      for v in google_cloud_run_v2_service.this["public"].template[0].volumes :
      v.name == "archfit-anthropic-key" && v.secret[0].items[0].version == "3"
    ])
    error_message = "The public service mounts the pinned Anthropic key version."
  }

  assert {
    condition     = !contains([for v in google_cloud_run_v2_service.this["worker"].template[0].volumes : v.name], "archfit-anthropic-key")
    error_message = "The worker never gets the Anthropic key."
  }
}

run "base_url_is_the_domain_without_a_trailing_slash" {
  command = plan

  assert {
    condition = alltrue([
      for s in google_cloud_run_v2_service.this :
      contains([for e in s.template[0].containers[0].env : "${e.name}=${e.value}"], "ARCHFIT_BASE_URL=https://app.coupling.dev")
    ])
    error_message = "ARCHFIT_BASE_URL is the OIDC audience and must be exact."
  }

  assert {
    condition     = google_cloud_run_domain_mapping.public.name == "app.coupling.dev" && google_cloud_run_domain_mapping.public.spec[0].route_name == "archfit-public"
    error_message = "The domain maps to the public service."
  }
}

run "queues_enforce_their_own_route_and_identity" {
  command = plan

  assert {
    condition = alltrue([
      for q in google_cloud_tasks_queue.this :
      q.http_target[0].uri_override[0].uri_override_enforce_mode == "ALWAYS"
      && q.http_target[0].uri_override[0].host == "archfit-worker-335297348272.europe-west1.run.app"
      && q.http_target[0].oidc_token[0].service_account_email == "archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com"
      && q.http_target[0].oidc_token[0].audience == "https://archfit-worker-335297348272.europe-west1.run.app"
    ])
    error_message = "A task creator must not choose the target URL or identity."
  }

  assert {
    condition = (
      google_cloud_scheduler_job.sweep.http_target[0].uri == "https://archfit-worker-335297348272.europe-west1.run.app/internal/sweep"
      && google_cloud_scheduler_job.sweep.http_target[0].oidc_token[0].service_account_email == "archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com"
      && google_cloud_scheduler_job.sweep.http_target[0].oidc_token[0].audience == "https://archfit-worker-335297348272.europe-west1.run.app"
    )
    error_message = "The Scheduler calls the worker's sweep route as the invoker."
  }

  assert {
    condition     = google_cloud_tasks_queue.this["operator"].http_target[0].uri_override[0].path_override[0].path == "/internal/grants"
    error_message = "Only the operator queue reaches the grants route."
  }

  assert {
    condition     = google_cloud_tasks_queue.this["webhooks"].http_target[0].uri_override[0].path_override[0].path != "/internal/grants"
    error_message = "The webhook queue must never reach the grants route."
  }

  assert {
    condition     = google_cloud_tasks_queue_iam_member.public_enqueuer.name == "archfit-webhooks" && google_cloud_tasks_queue_iam_member.public_enqueuer.role == "roles/cloudtasks.enqueuer"
    error_message = "The public service can add tasks only to the webhook queue."
  }

  assert {
    condition = toset([for m in google_cloud_tasks_queue_iam_member.deploy_viewer : "${m.name}|${m.role}|${m.member}"]) == toset([
      "archfit-webhooks|roles/cloudtasks.viewer|serviceAccount:archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com",
      "archfit-operator|roles/cloudtasks.viewer|serviceAccount:archfit-deploy@coupling-dev-510710.iam.gserviceaccount.com",
    ])
    error_message = "The deploy account can only read the two queues, for the post-release routing check."
  }
}

run "processing_starts_paused" {
  command = plan

  assert {
    condition     = alltrue([for q in google_cloud_tasks_queue.this : q.desired_state == "PAUSED"]) && google_cloud_scheduler_job.sweep.paused
    error_message = "Queues and the Scheduler stay paused until the first real image serves."
  }

  assert {
    condition     = google_cloud_scheduler_job.sweep.schedule == "0 * * * *"
    error_message = "The sweep runs each hour."
  }

  assert {
    condition     = !google_monitoring_alert_policy.missing_sweep.enabled && !google_monitoring_alert_policy.sweep_job_failures.enabled
    error_message = "A paused Scheduler must not raise the sweep alerts."
  }
}

run "resumed_processing_runs_queues_and_watches_the_sweep" {
  command = plan

  variables {
    paused = false
  }

  assert {
    condition     = alltrue([for q in google_cloud_tasks_queue.this : q.desired_state == "RUNNING"]) && !google_cloud_scheduler_job.sweep.paused
    error_message = "Resuming runs the queues and the Scheduler."
  }

  assert {
    condition     = google_monitoring_alert_policy.missing_sweep.enabled && google_monitoring_alert_policy.sweep_job_failures.enabled
    error_message = "The sweep alerts are on while the Scheduler runs."
  }
}

run "roles_get_the_settings_from_operations_md" {
  command = plan

  assert {
    condition = alltrue([
      for k, s in google_cloud_run_v2_service.this : alltrue([
        for want in [
          "ARCHFIT_ROLE=${k}",
          "ARCHFIT_STORE=firestore",
          "ARCHFIT_FIRESTORE_PROJECT=coupling-dev-510710",
          "GOOGLE_CLOUD_PROJECT=coupling-dev-510710",
          "ARCHFIT_WORKER_URL=https://archfit-worker-335297348272.europe-west1.run.app",
          "ARCHFIT_INVOKER=archfit-invoker@coupling-dev-510710.iam.gserviceaccount.com",
          "ARCHFIT_GITHUB_APP_ID=123456",
          "ARCHFIT_GITHUB_OAUTH_CLIENT_ID=Iv1.test",
        ] : contains([for e in s.template[0].containers[0].env : "${e.name}=${e.value}"], want)
      ])
    ])
    error_message = "Each service names its role, the store, and the worker audience and invoker that the App checks."
  }

  assert {
    condition = alltrue([
      for want in ["ARCHFIT_QUEUE=cloudtasks", "ARCHFIT_TASKS_QUEUE=projects/coupling-dev-510710/locations/europe-west1/queues/archfit-webhooks"] :
      contains([for e in google_cloud_run_v2_service.this["public"].template[0].containers[0].env : "${e.name}=${e.value}"], want)
    ])
    error_message = "The public service enqueues to the webhook queue."
  }

  assert {
    condition     = !contains([for e in google_cloud_run_v2_service.this["worker"].template[0].containers[0].env : e.name], "ARCHFIT_QUEUE")
    error_message = "The worker never enqueues."
  }

  assert {
    condition     = google_cloud_run_v2_service.this["worker"].custom_audiences == tolist(["https://archfit-worker-335297348272.europe-west1.run.app"])
    error_message = "The worker accepts the invoker token for the audience the App checks."
  }
}
