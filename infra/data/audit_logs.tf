# Admin Activity logs are always on. Data-read logs give each secret read a principal. Data-write logs give each grant
# task the identity that created it.
resource "google_project_iam_audit_config" "secretmanager" {
  project = var.project_id
  service = "secretmanager.googleapis.com"

  audit_log_config {
    log_type = "DATA_READ"
  }
}

resource "google_project_iam_audit_config" "cloudtasks" {
  project = var.project_id
  service = "cloudtasks.googleapis.com"

  audit_log_config {
    log_type = "DATA_WRITE"
  }
}
