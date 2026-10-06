output "project_number" {
  description = "Project number."
  value       = data.google_project.this.number
}

output "service_accounts" {
  description = "Runtime service account emails by component: public, worker, invoker."
  value       = { for k, sa in google_service_account.runtime : k => sa.email }
}

output "deploy_service_account" {
  description = "Deploy service account email. CI uses it as service_account in google-github-actions/auth."
  value       = google_service_account.deploy.email
}

output "workload_identity_provider" {
  description = "Full provider name. CI uses it as workload_identity_provider in google-github-actions/auth."
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "image_repository" {
  description = "Image path without a tag or digest."
  value       = "${google_artifact_registry_repository.app.location}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.app.repository_id}/archfit-app"
}

output "secret_ids" {
  description = "Secret IDs. The owner adds the values."
  value       = { for k, s in google_secret_manager_secret.this : k => s.secret_id }
}

output "secret_readers" {
  description = "Roles that may read each secret. The runtime stack mounts a secret only in these roles."
  value       = local.secret_readers
}

output "notification_channel" {
  description = "Owner email notification channel for the runtime alerts."
  value       = google_monitoring_notification_channel.owner_email.id
}

output "firestore_database" {
  description = "Firestore database name."
  value       = google_firestore_database.default.name
}
