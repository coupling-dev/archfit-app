terraform {
  # Import blocks with for_each need 1.7; the tests' mock providers need OpenTofu 1.8.
  required_version = ">= 1.8"

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
