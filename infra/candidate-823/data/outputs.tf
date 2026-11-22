output "project_id" { value = local.project }
output "project_number" { value = "335297348272" }
output "region" { value = local.region }
output "firestore_database" { value = google_firestore_database.candidate.name }
output "service_accounts" { value = { for k in keys(google_service_account.runtime) : k => "archfit-823-${k}@${local.project}.iam.gserviceaccount.com" } }
output "secret_ids" { value = local.secret_ids }
output "image_repository" { value = "${local.region}-docker.pkg.dev/${local.project}/archfit-app-823/archfit-app" }
