terraform {
  # for_each in import blocks needs OpenTofu or Terraform 1.7.
  required_version = ">= 1.7"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.5.0"
    }
  }

  backend "gcs" {
    bucket = "coupling-dev-510710-tfstate"
    prefix = "data"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
  # The owner applies with user credentials. Some APIs (Cloud Quotas, Essential Contacts) then need a quota project.
  user_project_override = true
  billing_project       = var.project_id
}

data "google_project" "this" {}
