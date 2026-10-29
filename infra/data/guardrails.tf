# The project guardrails from operations.md. They were applied with gcloud before Terraform existed, so each one has
# an import block. An import block is a no-op once the resource is in state.
#
# The billing budget lives on the owner's billing account, not in the project. Terraform does not manage it.

locals {
  # One element when the import blocks below run, none when they are off.
  imports = var.import_existing ? toset(["existing"]) : toset([])

  # Cloud Run quotas: 8 vCPU, 4 GiB and 4 services in the one region, 0 everywhere else, one region per project, and
  # no GPUs, jobs or worker pools. A preference without a region applies to every region that has no own preference.
  # CPU: a release brings up a new revision of each service beside the live one, so the worst case is 2 services x
  # 2 instances (max_instance_count in infra/runtime) x 2 revisions (live + new) x 1 vCPU = 8 vCPU. Memory: 8 instances x
  # 512 MiB = 4 GiB, so the memory quota stays 4 GiB. Raise these before raising the instance count.
  run_quota_preferences = {
    "run-cpu-europe-west1"      = { quota_id = "CpuAllocPerProjectRegion", value = "8000", region = var.region }
    "run-cpu-all-regions"       = { quota_id = "CpuAllocPerProjectRegion", value = "0", region = null }
    "run-mem-europe-west1"      = { quota_id = "MemAllocPerProjectRegion", value = "4294967296", region = var.region }
    "run-mem-all-regions"       = { quota_id = "MemAllocPerProjectRegion", value = "0", region = null }
    "run-services-europe-west1" = { quota_id = "ServicesPerProject", value = "4", region = var.region }
    "run-services-all-regions"  = { quota_id = "ServicesPerProject", value = "0", region = null }
    "run-gpu-l4"                = { quota_id = "NvidiaL4GpuAllocPerProjectRegion", value = "0", region = null }
    "run-gpu-l4-nozr"           = { quota_id = "NvidiaL4GpuAllocNoZonalRedundancyPerProjectRegion", value = "0", region = null }
    "run-gpu-rtx6000"           = { quota_id = "NvidiaRtxPro6000GpuAllocPerProjectRegion", value = "0", region = null }
    "run-gpu-rtx6000-nozr"      = { quota_id = "NvidiaRtxPro6000GpuAllocNoZonalRedundancyPerProjectRegion", value = "0", region = null }
    "run-jobs-all-regions"      = { quota_id = "JobsPerProject", value = "0", region = null }
    "run-workerpools-all"       = { quota_id = "WorkerPoolsPerProject", value = "0", region = null }
    "run-max-regions"           = { quota_id = "MaxRegionsPerProject", value = "1", region = null }
  }

  guardrail_log_filter = join(" ", [
    "logName=\"projects/${var.project_id}/logs/cloudaudit.googleapis.com%2Factivity\"",
    "AND NOT protoPayload.methodName=\"google.longrunning.Operations.GetOperation\"",
    "AND (protoPayload.methodName:\"SetIamPolicy\"",
    "OR protoPayload.serviceName=\"serviceusage.googleapis.com\"",
    "OR protoPayload.serviceName=\"cloudquotas.googleapis.com\"",
    "OR protoPayload.methodName=\"google.iam.admin.v1.CreateServiceAccountKey\"",
    "OR (protoPayload.serviceName=\"run.googleapis.com\"",
    "AND protoPayload.methodName:(\"CreateService\" OR \"ReplaceService\" OR \"CreateJob\")",
    "AND NOT resource.labels.location=\"${var.region}\"))",
  ])
}

import {
  for_each = var.import_existing ? local.run_quota_preferences : {}
  to       = google_cloud_quotas_quota_preference.run[each.key]
  id       = "projects/${var.project_id}/locations/global/quotaPreferences/${each.key}"
}

resource "google_cloud_quotas_quota_preference" "run" {
  for_each = local.run_quota_preferences

  parent     = "projects/${var.project_id}"
  name       = each.key
  service    = "run.googleapis.com"
  quota_id   = each.value.quota_id
  dimensions = each.value.region == null ? {} : { region = each.value.region }

  quota_config {
    preferred_value = each.value.value
  }

  lifecycle {
    # Request metadata, not the guardrail. gcloud set them, and a later change only edits the request record.
    ignore_changes = [justification, contact_email, ignore_safety_checks]
  }
}

import {
  for_each = local.imports
  to       = google_monitoring_notification_channel.owner_email
  id       = "projects/${var.project_id}/notificationChannels/5888030191618892716"
}

resource "google_monitoring_notification_channel" "owner_email" {
  display_name = "Owner email"
  type         = "email"
  labels = {
    email_address = var.owner_email
  }

  deletion_policy = "PREVENT"
}

import {
  for_each = local.imports
  to       = google_monitoring_alert_policy.guardrail
  id       = "projects/${var.project_id}/alertPolicies/9192556684800536581"
}

resource "google_monitoring_alert_policy" "guardrail" {
  display_name          = "Guardrail change (IAM, APIs, quotas, SA keys, Cloud Run region)"
  combiner              = "OR"
  severity              = "CRITICAL"
  notification_channels = [google_monitoring_notification_channel.owner_email.id]

  conditions {
    display_name = "Guardrail change in Admin Activity log"
    condition_matched_log {
      filter = local.guardrail_log_filter
    }
  }

  # Copied from the live policy, so the import does not drop the text of the alert email.
  documentation {
    content   = "Admin Activity audit log shows a change to a cost or security guardrail of project ${var.project_id}: IAM policy, enabled APIs, quota preferences, a service account key, or a Cloud Run service outside ${var.region}. If you did not make this change, follow the incident response in archfit-app docs/design/cloud/security.md."
    mime_type = "text/markdown"
  }

  alert_strategy {
    auto_close = "1800s"
    notification_rate_limit {
      period = "300s"
    }
  }

  deletion_policy = "PREVENT"
}

import {
  for_each = local.imports
  to       = google_essential_contacts_contact.owner
  id       = "projects/${var.project_id}/contacts/${var.essential_contact_id}"
}

resource "google_essential_contacts_contact" "owner" {
  parent                              = "projects/${var.project_id}"
  email                               = var.owner_email
  language_tag                        = "en"
  notification_category_subscriptions = ["SECURITY", "BILLING", "SUSPENSION", "TECHNICAL", "LEGAL"]

  deletion_policy = "PREVENT"
}
