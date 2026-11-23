locals {
  data       = data.terraform_remote_state.data.outputs
  project    = "coupling-dev-510710"
  region     = "europe-west1"
  domain     = "candidate.coupling.dev"
  base_url   = "https://${local.domain}"
  worker_url = "https://archfit-candidate-worker-${local.data.project_number}.${local.region}.run.app"
  secret_env = {
    github_private_key    = "ARCHFIT_GITHUB_PRIVATE_KEY_FILE"
    github_webhook_secret = "ARCHFIT_GITHUB_WEBHOOK_SECRET_FILE"
    github_oauth_secret   = "ARCHFIT_GITHUB_OAUTH_SECRET_FILE"
    cookie_key            = "ARCHFIT_COOKIE_KEY_FILE"
    operator_token        = "ARCHFIT_OPERATOR_TOKEN_FILE"
  }
  role_secrets = {
    public = toset(["github_private_key", "github_webhook_secret", "github_oauth_secret", "cookie_key"])
    worker = toset(["github_private_key", "github_webhook_secret", "operator_token"])
  }
  common_env = {
    ARCHFIT_BASE_URL               = local.base_url
    ARCHFIT_GITHUB_APP_ID          = var.github_app_id
    ARCHFIT_GITHUB_OAUTH_CLIENT_ID = var.github_oauth_client_id
    ARCHFIT_STORE                  = "firestore"
    ARCHFIT_FIRESTORE_PROJECT      = local.project
    ARCHFIT_FIRESTORE_DATABASE     = "archfit-candidate"
    GOOGLE_CLOUD_PROJECT           = local.project
    ARCHFIT_WORKER_URL             = local.worker_url
    ARCHFIT_INVOKER                = local.data.service_accounts.invoker
    ARCHFIT_OPERATORS              = join(",", var.operators)
    ARCHFIT_APPROVED_ACCOUNTS      = join(",", var.approved_accounts)
  }
}

resource "google_cloud_run_v2_service" "this" {
  for_each             = local.role_secrets
  name                 = "archfit-candidate-${each.key}"
  location             = local.region
  ingress              = each.key == "worker" ? "INGRESS_TRAFFIC_INTERNAL_ONLY" : "INGRESS_TRAFFIC_ALL"
  invoker_iam_disabled = each.key == "public"
  deletion_protection  = true
  custom_audiences     = each.key == "worker" ? [local.worker_url] : []

  lifecycle {
    precondition {
      condition     = local.data.project_id == local.project && local.data.region == local.region && local.data.project_number == "335297348272" && local.data.firestore_database == "archfit-candidate" && local.data.image_repository == "europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app-candidate/archfit-app" && alltrue([for role in ["public", "worker", "invoker"] : local.data.service_accounts[role] == "archfit-candidate-${role}@${local.project}.iam.gserviceaccount.com"])
      error_message = "Data outputs must identify the approved candidate resources."
    }
    precondition {
      condition     = alltrue([for key, id in { github_private_key = "archfit-candidate-github-private-key", github_webhook_secret = "archfit-candidate-github-webhook-secret", github_oauth_secret = "archfit-candidate-github-oauth-secret", cookie_key = "archfit-candidate-cookie-key", operator_token = "archfit-candidate-operator-token" } : local.data.secret_ids[key] == id])
      error_message = "Secret IDs must identify candidate secrets."
    }
    precondition {
      condition     = alltrue([for role in ["public", "worker"] : can(regex("^sha256:[0-9a-f]{64}$", var.image_digests[role]))]) && var.image_digests.public == var.image_digests.worker && alltrue([for key in keys(local.secret_env) : can(regex("^[1-9][0-9]*$", var.secret_versions[key]))])
      error_message = "Supply image digests and numeric secret versions."
    }
    precondition {
      condition     = can(regex("^[1-9][0-9]*$", var.github_app_id)) && var.github_app_id != "5348120" && length(trimspace(var.github_oauth_client_id)) > 0 && var.github_oauth_client_id != "Iv23lix1DdWQpCdiyYA7" && length(var.operators) > 0 && alltrue([for id in var.operators : id > 0 && floor(id) == id]) && length(var.approved_accounts) > 0 && alltrue([for account in var.approved_accounts : length(trimspace(account)) > 0 && !strcontains(account, ",")])
      error_message = "Supply candidate App, OAuth, operator and admission values."
    }
  }

  template {
    service_account = local.data.service_accounts[each.key]
    timeout         = each.key == "worker" ? "600s" : "330s"
    scaling {
      min_instance_count = 0
      max_instance_count = 2
    }
    containers {
      image = "${local.data.image_repository}@${var.image_digests[each.key]}"
      resources {
        limits = { cpu = "1", memory = "512Mi" }
      }
      dynamic "env" {
        for_each = merge(local.common_env, { ARCHFIT_ROLE = each.key }, each.key == "public" ? {
          ARCHFIT_QUEUE       = "cloudtasks"
          ARCHFIT_TASKS_QUEUE = "projects/${local.project}/locations/${local.region}/queues/${local.queue_names.webhooks}"
        } : {})
        content {
          name  = env.key
          value = env.value
        }
      }
      dynamic "env" {
        for_each = each.value
        content {
          name  = local.secret_env[env.value]
          value = "/secrets/${env.value}/value"
        }
      }
      dynamic "volume_mounts" {
        for_each = each.value
        content {
          name       = replace(volume_mounts.value, "_", "-")
          mount_path = "/secrets/${volume_mounts.value}"
        }
      }
    }
    dynamic "volumes" {
      for_each = each.value
      content {
        name = replace(volumes.value, "_", "-")
        secret {
          secret = local.data.secret_ids[volumes.value]
          items {
            path    = "value"
            version = var.secret_versions[volumes.value]
          }
        }
      }
    }
  }
}

resource "google_cloud_run_v2_service_iam_member" "worker_invoker" {
  name     = google_cloud_run_v2_service.this["worker"].name
  location = local.region
  role     = "roles/run.invoker"
  member   = "serviceAccount:${local.data.service_accounts.invoker}"
}
