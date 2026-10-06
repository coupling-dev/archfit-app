output "base_url" {
  description = "ARCHFIT_BASE_URL and the OIDC audience. It has no trailing slash."
  value       = local.base_url
}

output "service_urls" {
  description = "run.app URL of each service."
  value       = { for k, s in google_cloud_run_v2_service.this : k => s.uri }
}

output "queues" {
  description = "Full queue names."
  value       = { for k, q in google_cloud_tasks_queue.this : k => q.id }
}

output "sweep_job" {
  description = "Scheduler job name."
  value       = google_cloud_scheduler_job.sweep.name
}

output "dns_records" {
  description = "Records the owner adds at the registrar for the domain mapping."
  value = [
    for r in try(google_cloud_run_domain_mapping.public.status[0].resource_records, []) : {
      name   = r.name
      type   = r.type
      rrdata = r.rrdata
    }
  ]
}
