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

  secret_versions = merge(
    var.secret_versions,
    var.anthropic_key_version == null ? {} : { "archfit-anthropic-key" = var.anthropic_key_version },
  )

  # The data stack grants each secret to its roles. A service mounts exactly the secrets its account can read.
  role_secrets = {
    for role in keys(local.service_access) : role => [
      for secret, roles in local.data.secret_readers : secret if contains(roles, role)
    ]
  }

  # The Anthropic key is optional. Every other secret must have a pinned version, or the plan fails.
  mounts = {
    for role, secrets in local.role_secrets : role => {
      for s in secrets : s => local.secret_versions[s] if s != "archfit-anthropic-key" || var.anthropic_key_version != null
    }
  }

  common_env = {
    ARCHFIT_BASE_URL      = local.base_url
    ARCHFIT_GITHUB_APP_ID = var.github_app_id
    ARCHFIT_STORE         = "firestore"
  }

  role_env = {
    public = merge(local.common_env, {
      ARCHFIT_ROLE                   = "public"
      ARCHFIT_GITHUB_OAUTH_CLIENT_ID = var.github_oauth_client_id
      # Built from names: the queue depends on the worker URL, and both services are one resource.
      ARCHFIT_TASKS_QUEUE = "projects/${var.project_id}/locations/${var.region}/queues/${local.queue_names.webhooks}"
    })
    worker = merge(local.common_env, {
      ARCHFIT_ROLE = "worker"
    })
  }

  # The public service is the only component that the internet reaches. Its invoker check is off instead of an
  # allUsers binding. The worker is internal, and its invoker check is on.
  service_access = {
    public = { ingress = "INGRESS_TRAFFIC_ALL", invoker_iam_disabled = true, timeout = "60s" }
    worker = { ingress = "INGRESS_TRAFFIC_INTERNAL_ONLY", invoker_iam_disabled = false, timeout = "600s" }
  }
}

resource "google_cloud_run_v2_service" "this" {
  for_each = local.service_access

  name                 = "archfit-${each.key}"
  location             = var.region
  ingress              = each.value.ingress
  invoker_iam_disabled = each.value.invoker_iam_disabled
  # Services hold no state. A replacement keeps the data, so a runtime change may delete a service.
  deletion_protection = false

  template {
    service_account = local.data.service_accounts[each.key]
    timeout         = each.value.timeout

    # Two instances per service cap cost and throughput. With 1 vCPU each, the four instances fill the 4 vCPU quota.
    scaling {
      min_instance_count = 0
      max_instance_count = 2
    }

    containers {
      image = var.placeholder_image

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
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [
      # CI deploys by digest and moves traffic. gcloud also writes the client fields and the revision name.
      template[0].containers[0].image,
      traffic,
      client,
      client_version,
      template[0].revision,
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
