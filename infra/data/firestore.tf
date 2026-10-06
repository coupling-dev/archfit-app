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

# The driver stores each key in the kv collection with two fields. No query reads value, so it gets no single-field
# index, which makes writes cheaper. tenant keeps its automatic index: the uninstall and the sweep query it.
resource "google_firestore_field" "kv_value" {
  database   = google_firestore_database.default.name
  collection = "kv"
  field      = "value"

  index_config {}
}
