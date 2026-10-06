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

variable "placeholder_image" {
  description = "Image for a new service. It answers every request with success. CI replaces it; Terraform ignores the image after that."
  type        = string
  default     = "us-docker.pkg.dev/cloudrun/container/hello"
}

variable "github_app_id" {
  description = "GitHub App ID. Empty until the App is registered; the real image then refuses to start."
  type        = string
  default     = ""
}

variable "github_oauth_client_id" {
  description = "GitHub App OAuth client ID. Empty until the App is registered."
  type        = string
  default     = ""
}

variable "secret_versions" {
  description = "Pinned Secret Manager version of each mounted secret. A new value reaches the App only through a new revision."
  type        = map(string)
  default = {
    "archfit-github-private-key"    = "1"
    "archfit-github-webhook-secret" = "1"
    "archfit-github-oauth-secret"   = "1"
    "archfit-cookie-key"            = "1"
    "archfit-operator-token"        = "1"
  }
}

variable "anthropic_key_version" {
  description = "Pinned version of archfit-anthropic-key. Null mounts no key, and managed AI stays off."
  type        = string
  default     = null
}

variable "paused" {
  description = "Pause the queues and the Scheduler. They stay paused until the first real image serves."
  type        = bool
  default     = true
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
