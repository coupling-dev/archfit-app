output "base_url" {
  value = local.base_url
}

output "worker_url" {
  value = local.worker_url
}

output "service_urls" {
  value = { for role, service in google_cloud_run_v2_service.this : role => service.uri }
}

output "queues" {
  value = { for role, queue in google_cloud_tasks_queue.this : role => queue.id }
}

output "sweep_job" {
  value = google_cloud_scheduler_job.sweep.id
}

output "dns_records" {
  value = try(google_cloud_run_domain_mapping.public.status[0].resource_records, [])
}
