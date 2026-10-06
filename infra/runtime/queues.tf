# Each queue sets its own target route and identity (queue-level uriOverride, enforced). A task creator names only the
# queue, so the public service cannot aim a task at the grants route.
locals {
  state = var.paused ? "PAUSED" : "RUNNING"
  queue_names = {
    webhooks = "archfit-webhooks"
    operator = "archfit-operator"
  }

  queues = {
    # GitHub webhook work. Transient errors retry for about 2 hours. The sweep repairs the rest.
    webhooks = {
      path                      = var.worker_paths.webhooks
      max_dispatches_per_second = 5
      max_concurrent_dispatches = 10
      max_attempts              = -1
      max_retry_duration        = "7200s"
    }
    # Grants. Only the project owner can add tasks here. Cloud Tasks writes no audit log for CreateTask.
    operator = {
      path                      = var.worker_paths.grants
      max_dispatches_per_second = 1
      max_concurrent_dispatches = 1
      max_attempts              = 5
      max_retry_duration        = "3600s"
    }
  }
}

resource "google_cloud_tasks_queue" "this" {
  for_each = local.queues

  name          = local.queue_names[each.key]
  location      = var.region
  desired_state = local.state

  rate_limits {
    max_dispatches_per_second = each.value.max_dispatches_per_second
    max_concurrent_dispatches = each.value.max_concurrent_dispatches
  }

  retry_config {
    max_attempts       = each.value.max_attempts
    max_retry_duration = each.value.max_retry_duration
    min_backoff        = "10s"
    max_backoff        = "600s"
  }

  http_target {
    http_method = "POST"

    uri_override {
      scheme                    = "HTTPS"
      host                      = trimprefix(local.worker_url, "https://")
      uri_override_enforce_mode = "ALWAYS"
      path_override {
        path = each.value.path
      }
    }

    oidc_token {
      service_account_email = local.data.service_accounts.invoker
      audience              = local.worker_url
    }
  }
}

# The public service can add tasks to the webhook queue and to no other queue.
resource "google_cloud_tasks_queue_iam_member" "public_enqueuer" {
  name     = google_cloud_tasks_queue.this["webhooks"].name
  location = var.region
  role     = "roles/cloudtasks.enqueuer"
  member   = "serviceAccount:${local.data.service_accounts.public}"
}

# The Scheduler starts the sweep each hour.
resource "google_cloud_scheduler_job" "sweep" {
  name             = "archfit-sweep"
  region           = var.region
  schedule         = "0 * * * *"
  time_zone        = "Etc/UTC"
  paused           = var.paused
  attempt_deadline = "600s"

  http_target {
    http_method = "POST"
    uri         = "${local.worker_url}${var.worker_paths.sweep}"

    oidc_token {
      service_account_email = local.data.service_accounts.invoker
      audience              = local.worker_url
    }
  }
}
