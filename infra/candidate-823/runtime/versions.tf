terraform {
  required_version = ">= 1.8"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.5.0"
    }
  }
  backend "gcs" {
    prefix = "candidate-823/runtime"
  }
}

provider "google" {
  project = local.project
  region  = local.region
}

data "terraform_remote_state" "data" {
  backend = "gcs"
  config = {
    bucket = var.data_state_bucket
    prefix = "candidate-823/data"
  }
}
