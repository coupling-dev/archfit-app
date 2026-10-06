locals {
  # The APIs that were enabled on the project before Terraform: the runtime set from operations.md, the platform APIs
  # they need, and the defaults that stayed after the guardrail cleanup. Terraform imports them and enables nothing new.
  services = toset([
    "artifactregistry.googleapis.com",
    "billingbudgets.googleapis.com",
    "cloudbilling.googleapis.com",
    "cloudquotas.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "cloudscheduler.googleapis.com",
    "cloudtasks.googleapis.com",
    "cloudtrace.googleapis.com",
    "datastore.googleapis.com",
    "essentialcontacts.googleapis.com",
    "firebaserules.googleapis.com",
    "firestore.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
    "run.googleapis.com",
    "secretmanager.googleapis.com",
    "servicemanagement.googleapis.com",
    "serviceusage.googleapis.com",
    "storage-api.googleapis.com",
    "storage-component.googleapis.com",
    "storage.googleapis.com",
    "sts.googleapis.com",
    "telemetry.googleapis.com",
  ])
}

import {
  for_each = var.import_existing ? local.services : toset([])
  to       = google_project_service.this[each.key]
  id       = "${var.project_id}/${each.key}"
}

resource "google_project_service" "this" {
  for_each = local.services

  service = each.key
  # Removing a service from the list only stops managing it. Disabling an API is a guardrail change made by hand.
  disable_on_destroy         = false
  disable_dependent_services = false
}
