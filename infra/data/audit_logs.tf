# Admin Activity logs are always on. Data-read logs give each secret read a principal. Cloud Tasks data-write logs
# record RunTask and DeleteTask with their principal. CreateTask writes no audit log at all, so a grant task has no
# recorded creator; only the project owner can add tasks to the operator queue.
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
