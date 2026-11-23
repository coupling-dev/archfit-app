variable "data_state_bucket" {
  description = "Owner-supplied bucket containing candidate/data state."
  type        = string
  nullable    = false
  validation {
    condition     = length(trimspace(var.data_state_bucket)) > 0
    error_message = "Supply the candidate data state bucket."
  }
}

variable "github_app_id" {
  type = string
}

variable "github_oauth_client_id" {
  type = string
}

variable "operators" {
  type = list(number)
}

variable "approved_accounts" {
  type = list(string)
}

variable "image_digests" {
  type = map(string)
}

variable "secret_versions" {
  type = map(string)
}

variable "paused" {
  description = "Keep queues and scheduler paused until separately approved activation."
  type        = bool
  default     = true
}
