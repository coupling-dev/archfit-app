variable "project_id" {
  description = "Google Cloud project ID."
  type        = string
  default     = "coupling-dev-510710"
}

variable "region" {
  description = "The one region for every regional resource. The Firestore location cannot change."
  type        = string
  default     = "europe-west1"
}

variable "owner_email" {
  description = "Owner account. It receives the guardrail alert and the essential-contact notices."
  type        = string
}

variable "essential_contact_id" {
  description = "Numeric ID of the existing essential contact, from `gcloud essential-contacts list`."
  type        = string
}

variable "import_existing" {
  description = "Import the APIs and guardrails that exist before Terraform. Tests turn it off: the test framework cannot import."
  type        = bool
  default     = true
}

variable "github_repository" {
  description = "The repository that may deploy, as owner/name."
  type        = string
  default     = "coupling-dev/archfit-app"
}

variable "github_repository_id" {
  description = "Numeric GitHub ID of the repository. It does not change on a rename or transfer."
  type        = string
  default     = "1373981201"
}

variable "deploy_workflow" {
  description = "The only workflow file that may deploy, under .github/workflows/."
  type        = string
  default     = "deploy.yaml"
}
