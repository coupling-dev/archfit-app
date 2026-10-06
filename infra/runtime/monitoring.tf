# Alerts from operations.md. All go to the owner email channel of the data stack.
# The budget alerts live on the billing account and are not managed here.
locals {
  channels         = [local.data.notification_channel]
  public           = google_cloud_run_v2_service.this["public"]
  service_names    = [for s in google_cloud_run_v2_service.this : s.name]
  service_selector = join(" OR ", [for n in local.service_names : "\"${n}\""])
}

# The public health check. It calls the run.app URL, so it works before DNS, and /healthz does not depend on the host.
resource "google_monitoring_uptime_check_config" "public_health" {
  display_name = "archfit public /healthz"
  timeout      = "10s"
  period       = "300s"

  http_check {
    path         = "/healthz"
    use_ssl      = true
    validate_ssl = true
  }

  monitored_resource {
    type = "uptime_url"
    labels = {
      project_id = var.project_id
      host       = trimprefix(local.public.uri, "https://")
    }
  }
}

resource "google_monitoring_alert_policy" "public_health" {
  display_name          = "archfit public health check failing"
  combiner              = "OR"
  severity              = "CRITICAL"
  notification_channels = local.channels

  conditions {
    display_name = "/healthz fails"
    condition_threshold {
      filter          = "metric.type=\"monitoring.googleapis.com/uptime_check/check_passed\" AND metric.label.check_id=\"${google_monitoring_uptime_check_config.public_health.uptime_check_id}\" AND resource.type=\"uptime_url\""
      comparison      = "COMPARISON_GT"
      threshold_value = 1
      duration        = "600s"
      aggregations {
        alignment_period     = "1200s"
        per_series_aligner   = "ALIGN_NEXT_OLDER"
        cross_series_reducer = "REDUCE_COUNT_FALSE"
        group_by_fields      = ["resource.label.*"]
      }
    }
  }
}

# At beta traffic a count works better than a ratio: any 5xx response alerts.
resource "google_monitoring_alert_policy" "public_5xx" {
  display_name          = "archfit public 5xx responses"
  combiner              = "OR"
  severity              = "ERROR"
  notification_channels = local.channels

  conditions {
    display_name = "5xx responses in 5 minutes"
    condition_threshold {
      filter          = "resource.type=\"cloud_run_revision\" AND resource.label.service_name=\"${local.public.name}\" AND metric.type=\"run.googleapis.com/request_count\" AND metric.label.response_code_class=\"5xx\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"
      aggregations {
        alignment_period     = "300s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }
}

# Cloud Logging reads the severity only from its own key name. App lines match once the App writes that key.
# Platform error lines (crashes, 5xx request logs) match already.
resource "google_monitoring_alert_policy" "error_lines" {
  display_name          = "archfit error log line"
  combiner              = "OR"
  severity              = "ERROR"
  notification_channels = local.channels

  conditions {
    display_name = "Error line on either service"
    condition_matched_log {
      filter = "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=(${local.service_selector}) AND severity>=ERROR"
    }
  }

  alert_strategy {
    auto_close = "1800s"
    notification_rate_limit {
      period = "300s"
    }
  }
}

# One count for each scheduled sweep that finished: the request line of POST /internal/sweep with outcome "swept".
# A one-repository pass after a push webhook writes "sweep: pass finished" too, so that line is not used.
# Both key names match, before and after the App moves to the Cloud Logging key names.
resource "google_logging_metric" "sweep_passes" {
  name = "archfit/sweep_passes"
  filter = join(" AND ", [
    "resource.type=\"cloud_run_revision\"",
    "resource.labels.service_name=\"${google_cloud_run_v2_service.this["worker"].name}\"",
    "(jsonPayload.msg=\"request\" OR jsonPayload.message=\"request\")",
    "jsonPayload.route=\"POST ${var.worker_paths.sweep}\"",
    "jsonPayload.outcome=\"swept\"",
  ])

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
  }
}

# Target: one finished pass at least every 2 hours. Off while the Scheduler is paused.
# A metric-absence condition fires only after the metric had a point since the policy changed. A sweep that never
# succeeds is caught by sweep_job_failures below.
resource "google_monitoring_alert_policy" "missing_sweep" {
  display_name          = "archfit sweep missing"
  combiner              = "OR"
  severity              = "ERROR"
  enabled               = !var.paused
  notification_channels = local.channels

  conditions {
    display_name = "No finished sweep pass in 2 hours"
    condition_absent {
      filter   = "resource.type=\"cloud_run_revision\" AND metric.type=\"logging.googleapis.com/user/${google_logging_metric.sweep_passes.name}\""
      duration = "7200s"
      aggregations {
        alignment_period     = "600s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }
}

# Cloud Scheduler logs each attempt. A failed attempt (a 401, a 5xx, a deadline) is an ERROR line on the job.
resource "google_monitoring_alert_policy" "sweep_job_failures" {
  display_name          = "archfit sweep job fails"
  combiner              = "OR"
  severity              = "ERROR"
  enabled               = !var.paused
  notification_channels = local.channels

  conditions {
    display_name = "Failed Scheduler attempt"
    condition_matched_log {
      filter = "resource.type=\"cloud_scheduler_job\" AND resource.labels.job_id=\"${google_cloud_scheduler_job.sweep.name}\" AND severity>=ERROR"
    }
  }

  alert_strategy {
    auto_close = "1800s"
    notification_rate_limit {
      period = "3600s"
    }
  }
}

resource "google_monitoring_alert_policy" "webhook_queue_depth" {
  display_name          = "archfit webhook queue growing"
  combiner              = "OR"
  severity              = "WARNING"
  notification_channels = local.channels

  conditions {
    display_name = "More than 50 tasks wait for 15 minutes"
    condition_threshold {
      filter          = "resource.type=\"cloud_tasks_queue\" AND resource.label.queue_id=\"${google_cloud_tasks_queue.this["webhooks"].name}\" AND metric.type=\"cloudtasks.googleapis.com/queue/depth\""
      comparison      = "COMPARISON_GT"
      threshold_value = 50
      duration        = "900s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_MAX"
      }
    }
  }
}

# A failed dispatch includes 401 and 403 from the platform, which never reach the App.
resource "google_monitoring_alert_policy" "task_dispatch_failures" {
  display_name          = "archfit task dispatches fail"
  combiner              = "OR"
  severity              = "ERROR"
  notification_channels = local.channels

  conditions {
    display_name = "Task attempts without OK in 5 minutes"
    condition_threshold {
      filter          = "resource.type=\"cloud_tasks_queue\" AND metric.type=\"cloudtasks.googleapis.com/queue/task_attempt_count\" AND metric.label.response_code!=\"ok\" AND metric.label.response_code!=\"OK\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"
      aggregations {
        alignment_period     = "300s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
        group_by_fields      = ["resource.label.queue_id"]
      }
    }
  }
}
