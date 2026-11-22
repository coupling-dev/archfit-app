mock_provider "google" {}

override_data {
  target = data.terraform_remote_state.data
  values = {
    outputs = {
      project_id         = "coupling-dev-510710"
      project_number     = "335297348272"
      region             = "europe-west1"
      firestore_database = "archfit-823"
      service_accounts = {
        public  = "archfit-823-public@coupling-dev-510710.iam.gserviceaccount.com"
        worker  = "archfit-823-worker@coupling-dev-510710.iam.gserviceaccount.com"
        invoker = "archfit-823-invoker@coupling-dev-510710.iam.gserviceaccount.com"
      }
      secret_ids = {
        "github_private_key"    = "archfit-823-github-private-key"
        "github_webhook_secret" = "archfit-823-github-webhook-secret"
        "github_oauth_secret"   = "archfit-823-github-oauth-secret"
        "cookie_key"            = "archfit-823-cookie-key"
        "operator_token"        = "archfit-823-operator-token"
      }
      image_repository = "europe-west1-docker.pkg.dev/coupling-dev-510710/archfit-app-823/archfit-app"
    }
  }
}

variables {
  data_state_bucket      = "candidate-state"
  github_app_id          = "123"
  github_oauth_client_id = "candidate"
  operators              = [123]
  approved_accounts      = ["candidate"]
  image_digests          = { public = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", worker = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }
  secret_versions        = { github_private_key = "1", github_webhook_secret = "1", github_oauth_secret = "1", cookie_key = "1", operator_token = "1" }
}

run "both_roles_use_candidate_database" {
  command = plan
  assert {
    condition     = alltrue([for service in google_cloud_run_v2_service.this : contains([for env in service.template[0].containers[0].env : "${env.name}=${env.value}"], "ARCHFIT_FIRESTORE_DATABASE=archfit-823")]) && length(google_cloud_run_v2_service.this) == 2
    error_message = "Both services must use the candidate database."
  }
}
