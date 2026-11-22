terraform {
  required_version = ">= 1.8"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.5.0"
    }
  }
  backend "gcs" {
    prefix = "candidate-823/data"
  }
}

provider "google" {
  project = "coupling-dev-510710"
  region  = "europe-west1"
}
