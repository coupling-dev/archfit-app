# The free quota covers only the default database. Its location cannot change after creation.
resource "google_firestore_database" "default" {
  name                        = "(default)"
  location_id                 = var.region
  type                        = "FIRESTORE_NATIVE"
  app_engine_integration_mode = "DISABLED"
  delete_protection_state     = "DELETE_PROTECTION_ENABLED"
  deletion_policy             = "PREVENT"

  depends_on = [google_project_service.this]
}

# A restore creates a new database. It never overwrites the live one.
resource "google_firestore_backup_schedule" "daily" {
  database  = google_firestore_database.default.name
  retention = "604800s" # 7 days

  daily_recurrence {}
}
