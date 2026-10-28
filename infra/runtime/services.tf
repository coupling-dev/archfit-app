# Two Cloud Run services run the same image. A role setting selects the routes.
# CI owns the image and the traffic of both services. Terraform owns every other setting and ignores those fields.
locals {
  base_url = "https://${var.domain}"

  # Each service mounts only the secrets its role needs, as files. The App reads the file named by the *_FILE variable.
  secret_env = {
    "archfit-github-private-key"    = "ARCHFIT_GITHUB_PRIVATE_KEY_FILE"
    "archfit-github-webhook-secret" = "ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE"
    "archfit-github-oauth-secret"   = "ARCHFIT_GITHUB_OAUTH_SECRET_FILE"
    "archfit-cookie-key"            = "ARCHFIT_COOKIE_KEY_FILE"
    "archfit-operator-token"        = "ARCHFIT_OPERATOR_TOKEN_FILE"
    "archfit-anthropic-key"         = "ARCHFIT_ANTHROPIC_KEY_FILE"
  }

  # The data stack grants each secret to its roles. A service mounts exactly the secrets its account can read.
  role_secrets = {
    for role in keys(local.service_access) : role => [
      for secret, roles in local.data.secret_readers : secret if contains(roles, role)
    ]
  }

  # The Anthropic key is optional: without a version it is not mounted, and managed AI stays off. Every other secret
  # must have a pinned version, or the plan fails.
  mounts = {
    for role, secrets in local.role_secrets : role => {
      for s in secrets : s => var.secret_versions[s] if s != "archfit-anthropic-key" || contains(keys(var.secret_versions), s)
    }
  }

  # The deterministic run.app URL of the worker. It is known before the service exists, so the public service, the
  # queues and the Scheduler can name it without a cycle. It is the audience of every invoker ID token, and the App
  # compares it byte for byte.
  worker_url = "https://archfit-worker-${local.data.project_number}.${var.region}.run.app"

  # The settings from operations.md "Roles and routing" and "Firestore".
  common_env = merge({
    ARCHFIT_BASE_URL               = local.base_url
    ARCHFIT_GITHUB_APP_ID          = var.github_app_id
    ARCHFIT_GITHUB_OAUTH_CLIENT_ID = var.github_oauth_client_id
    ARCHFIT_STORE                  = "firestore"
    ARCHFIT_FIRESTORE_PROJECT      = var.project_id
    GOOGLE_CLOUD_PROJECT           = var.project_id
    ARCHFIT_WORKER_URL             = local.worker_url
    ARCHFIT_INVOKER                = local.data.service_accounts.invoker
    }, var.webhook_secret_previous_version == null ? {} : {
    ARCHFIT_GITHUB_WEBHOOK_SECRET_PREVIOUS_FILE = "/secrets/archfit-github-webhook-secret/previous"
  })

  role_env = {
    public = merge(local.common_env, {
      ARCHFIT_ROLE        = "public"
      ARCHFIT_QUEUE       = "cloudtasks"
      ARCHFIT_TASKS_QUEUE = "projects/${var.project_id}/locations/${var.region}/queues/${local.queue_names.webhooks}"
    })
    worker = merge(local.common_env, {
      ARCHFIT_ROLE = "worker"
    })
  }

  # The public timeout sits above the longest route deadline, the 300 s managed assist route (handler.go); uploads
  # and control routes stop at 55 s on their own. The worker timeout covers the sweep.
  # The public service is the only component that the internet reaches. Its invoker check is off instead of an
  # allUsers binding. The worker is internal, and its invoker check is on.
  service_access = {
    public = { ingress = "INGRESS_TRAFFIC_ALL", invoker_iam_disabled = true, timeout = "330s" }
    worker = { ingress = "INGRESS_TRAFFIC_INTERNAL_ONLY", invoker_iam_disabled = false, timeout = "600s" }
  }
}

resource "google_cloud_run_v2_service" "this" {
  for_each = local.service_access

  name                 = "archfit-${each.key}"
  location             = var.region
  ingress              = each.value.ingress
  invoker_iam_disabled = each.value.invoker_iam_disabled
  # Cloud Run accepts an ID token for this audience even if the platform changes which URLs it lists.
  custom_audiences = each.key == "worker" ? [local.worker_url] : []
  # Keep deletion protection on: replacing a service requires a reviewed bootstrap image and an owner decision.

  template {
    service_account = local.data.service_accounts[each.key]
    timeout         = each.value.timeout

    # One instance per service. The region's Cloud Run quota is 4 vCPU and each instance takes 1 vCPU. A release brings
    # up a new revision of each service while the live one still serves: with one instance per revision that is at most
    # 4 vCPU (2 live + 2 new). Two per service filled the quota with the live revisions alone, so traffic could not move
    # to a new revision ("Quota exceeded for total allowable CPU", deploy run 40731326038).
    scaling {
      min_instance_count = 0
      max_instance_count = 1
    }

    containers {
      image = "${local.data.image_repository}@${var.initial_image_digest}"

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        cpu_idle          = true
        startup_cpu_boost = true
      }

      dynamic "env" {
        for_each = local.role_env[each.key]
        content {
          name  = env.key
          value = env.value
        }
      }

      dynamic "env" {
        for_each = local.mounts[each.key]
        content {
          name  = local.secret_env[env.key]
          value = "/secrets/${env.key}/value"
        }
      }

      dynamic "volume_mounts" {
        for_each = local.mounts[each.key]
        content {
          name       = volume_mounts.key
          mount_path = "/secrets/${volume_mounts.key}"
        }
      }
    }

    dynamic "volumes" {
      for_each = local.mounts[each.key]
      content {
        name = volumes.key
        secret {
          secret = local.data.secret_ids[volumes.key]
          items {
            path    = "value"
            version = volumes.value
          }
          dynamic "items" {
            for_each = volumes.key == "archfit-github-webhook-secret" && var.webhook_secret_previous_version != null ? [var.webhook_secret_previous_version] : []
            content {
              path    = "previous"
              version = items.value
            }
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [
      # CI deploys by digest and moves traffic. gcloud also writes the client fields. The revision name is not
      # ignored: a kept name with a changed template is rejected by Cloud Run, so Terraform lets it generate one.
      template[0].containers[0].image,
      traffic,
      client,
      client_version,
    ]
  }
}

# Only the invoker identity of the queues and the Scheduler can call the worker.
resource "google_cloud_run_v2_service_iam_member" "worker_invoker" {
  name     = google_cloud_run_v2_service.this["worker"].name
  location = google_cloud_run_v2_service.this["worker"].location
  role     = "roles/run.invoker"
  member   = "serviceAccount:${local.data.service_accounts.invoker}"
}
