# Cloud Run domain mapping (Preview, no SLA). The owner must have verified coupling.dev in Search Console.
# DNS is at the registrar: the owner adds the records from the dns_records output.
resource "google_cloud_run_domain_mapping" "public" {
  name     = local.domain
  location = local.region

  metadata {
    namespace = local.project
  }

  spec {
    route_name = google_cloud_run_v2_service.this["public"].name
  }
}
