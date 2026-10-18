variable "project_id" {
  description = "Google Cloud project ID."
  type        = string
  default     = "coupling-dev-510710"
}

variable "region" {
  description = "The one region for every regional resource."
  type        = string
  default     = "europe-west1"
}

variable "domain" {
  description = "Public domain. https://<domain> is ARCHFIT_BASE_URL and the OIDC audience."
  type        = string
  default     = "app.coupling.dev"
}

variable "initial_image_digest" {
  description = "Digest of the real App image built before the first runtime apply. CI owns later image and traffic changes."
  type        = string

  validation {
    condition     = can(regex("^sha256:[0-9a-f]{64}$", var.initial_image_digest))
    error_message = "Build the App image first and provide its sha256 digest, not a tag or placeholder image."
  }
}

variable "webhook_secret_previous_version" {
  description = "Pinned previous version of archfit-github-webhook-secret during rotation. Null mounts only the current version."
  type        = string
  default     = null

  validation {
    condition     = var.webhook_secret_previous_version == null || can(regex("^[1-9][0-9]*$", var.webhook_secret_previous_version))
    error_message = "Use a numeric Secret Manager version for the previous webhook secret, never latest."
  }
}

# The registered App, coupling-dev/archfit-review (runbook step 5). These IDs are not secret.
variable "github_app_id" {
  description = "GitHub App ID."
  type        = string
  default     = "5348120"
}

variable "github_oauth_client_id" {
  description = "GitHub App OAuth client ID."
  type        = string
  default     = "Iv23lix1DdWQpCdiyYA7"
}

variable "secret_versions" {
  description = "Pinned Secret Manager version of each mounted secret. A new value reaches the App only through a new revision. Add archfit-anthropic-key to turn on managed AI."
  type        = map(string)
  default = {
    "archfit-github-private-key"    = "1"
    "archfit-github-webhook-secret" = "1"
    "archfit-github-oauth-secret"   = "1"
    "archfit-cookie-key"            = "1"
    "archfit-operator-token"        = "1"
  }
}

variable "paused" {
  description = "Pause the queues and the Scheduler. They started paused until the first real image served."
  type        = bool
  default     = false
}

variable "worker_paths" {
  description = "Worker routes that each queue and the Scheduler call. Each queue enforces its own route."
  type = object({
    webhooks = string
    grants   = string
    sweep    = string
  })
  default = {
    webhooks = "/internal/webhooks"
    grants   = "/internal/grants"
    sweep    = "/internal/sweep"
  }
}
