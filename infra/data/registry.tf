resource "google_artifact_registry_repository" "app" {
  location      = var.region
  repository_id = "archfit-app"
  format        = "DOCKER"
  description   = "archfit-app images, deployed by digest"

  # Keep the last images for rollbacks. A keep policy wins over a delete policy.
  cleanup_policies {
    id     = "keep-recent"
    action = "KEEP"
    most_recent_versions {
      keep_count = 10
    }
  }

  cleanup_policies {
    id     = "delete-old"
    action = "DELETE"
    condition {
      older_than = "2592000s" # 30 days
    }
  }

  depends_on = [google_project_service.this]
}
