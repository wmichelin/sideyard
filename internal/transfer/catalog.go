// Package transfer defines Sideyard's explicit, versioned logical data contract.
// It excludes engine migration ledgers and SQL Server computed columns.
package transfer

type Kind string

const (
	UUID      Kind = "uuid"
	Text      Kind = "text"
	TextArray Kind = "text_array"
	UUIDArray Kind = "uuid_array"
	JSON      Kind = "json"
	Integer   Kind = "integer"
	Instant   Kind = "instant"
	Date      Kind = "date"
	Decimal   Kind = "decimal_14_3"
	Boolean   Kind = "boolean"
	Bytes     Kind = "bytes"
)

type Column struct {
	Name     string `json:"name"`
	Kind     Kind   `json:"kind"`
	Nullable bool   `json:"nullable"`
}

type Table struct {
	Name        string   `json:"name"`
	Key         []string `json:"key"`
	Columns     []Column `json:"columns"`
	ShopColumn  string   `json:"shop_column,omitempty"`
	ShopViaUser string   `json:"shop_via_user,omitempty"`
}

const FormatVersion = 1
const LogicalSchema = "sideyard-2026-09-v31"
const ProductionV29LogicalSchema = "sideyard-2026-09-production-v29"

type Profile struct {
	LogicalSchema       string
	Catalog             []Table
	PostgresMigrations  int
	SQLServerMigrations int
}

var Catalog = []Table{
	{Name: "interest_signups", Key: []string{"id"}, Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "name", Kind: Text, Nullable: false},
		{Name: "email", Kind: Text, Nullable: false},
		{Name: "business", Kind: Text, Nullable: false},
		{Name: "trade", Kind: Text, Nullable: false},
		{Name: "message", Kind: Text, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
	}},
	{Name: "interest_notifications", Key: []string{"signup_id"}, Columns: []Column{
		{Name: "signup_id", Kind: UUID, Nullable: false},
		{Name: "attempts", Kind: Integer, Nullable: false},
		{Name: "next_attempt_at", Kind: Instant, Nullable: false},
		{Name: "sent_at", Kind: Instant, Nullable: true},
		{Name: "provider_message_id", Kind: Text, Nullable: false},
	}},
	{Name: "shops", Key: []string{"id"}, ShopColumn: "id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "name", Kind: Text, Nullable: false},
		{Name: "slug", Kind: Text, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "kind", Kind: Text, Nullable: false},
		{Name: "expires_at", Kind: Instant, Nullable: true},
		{Name: "demo_mutations_used", Kind: Integer, Nullable: false},
		{Name: "demo_import_rows_used", Kind: Integer, Nullable: false},
		{Name: "demo_import_occurrences_used", Kind: Integer, Nullable: false},
	}},
	{Name: "users", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "email", Kind: Text, Nullable: false},
		{Name: "password_hash", Kind: Text, Nullable: false},
		{Name: "role", Kind: Text, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
	}},
	{Name: "sessions", Key: []string{"id"}, ShopViaUser: "user_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "user_id", Kind: UUID, Nullable: false},
		{Name: "token_hash", Kind: Text, Nullable: false},
		{Name: "expires_at", Kind: Instant, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
	}},
	{Name: "owner_provision_events", Key: []string{"id"}, ShopViaUser: "user_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "user_id", Kind: UUID, Nullable: false},
		{Name: "action", Kind: Text, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
	}},
	{Name: "pilot_activation_events", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "event_type", Kind: Text, Nullable: false},
		{Name: "occurred_at", Kind: Instant, Nullable: false},
	}},
	{Name: "customers", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "external_id", Kind: Text, Nullable: true},
		{Name: "name", Kind: Text, Nullable: false},
		{Name: "email", Kind: Text, Nullable: true},
		{Name: "phone", Kind: Text, Nullable: true},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "updated_at", Kind: Instant, Nullable: false},
	}},
	{Name: "properties", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "customer_id", Kind: UUID, Nullable: false},
		{Name: "external_id", Kind: Text, Nullable: true},
		{Name: "label", Kind: Text, Nullable: true},
		{Name: "address_line1", Kind: Text, Nullable: false},
		{Name: "address_line2", Kind: Text, Nullable: true},
		{Name: "city", Kind: Text, Nullable: false},
		{Name: "state", Kind: Text, Nullable: false},
		{Name: "postal_code", Kind: Text, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "updated_at", Kind: Instant, Nullable: false},
		{Name: "address_revision", Kind: Integer, Nullable: false},
	}},
	{Name: "equipment", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "property_id", Kind: UUID, Nullable: false},
		{Name: "external_id", Kind: Text, Nullable: true},
		{Name: "kind", Kind: Text, Nullable: false},
		{Name: "make", Kind: Text, Nullable: true},
		{Name: "model", Kind: Text, Nullable: true},
		{Name: "serial_number", Kind: Text, Nullable: true},
		{Name: "installed_on", Kind: Date, Nullable: true},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "location", Kind: Text, Nullable: false},
		{Name: "warranty_until", Kind: Date, Nullable: true},
		{Name: "internal_notes", Kind: Text, Nullable: false},
		{Name: "revision", Kind: Integer, Nullable: false},
	}},
	{Name: "customer_vehicles", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "customer_id", Kind: UUID, Nullable: false},
		{Name: "name", Kind: Text, Nullable: false},
		{Name: "make", Kind: Text, Nullable: false},
		{Name: "model", Kind: Text, Nullable: false},
		{Name: "model_year", Kind: Integer, Nullable: true},
		{Name: "identifier", Kind: Text, Nullable: false},
		{Name: "internal_notes", Kind: Text, Nullable: false},
		{Name: "revision", Kind: Integer, Nullable: false},
	}},
	{Name: "property_details", Key: []string{"property_id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "property_id", Kind: UUID, Nullable: false},
		{Name: "access_notes", Kind: Text, Nullable: false},
		{Name: "revision", Kind: Integer, Nullable: false},
	}},
	{Name: "property_measurements", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "property_id", Kind: UUID, Nullable: false},
		{Name: "kind", Kind: Text, Nullable: false},
		{Name: "zone", Kind: Text, Nullable: false},
		{Name: "value", Kind: Decimal, Nullable: false},
		{Name: "unit", Kind: Text, Nullable: false},
		{Name: "source", Kind: Text, Nullable: false},
		{Name: "measured_on", Kind: Date, Nullable: false},
		{Name: "estimated", Kind: Boolean, Nullable: false},
		{Name: "recorded_by", Kind: Text, Nullable: false},
		{Name: "recorded_at", Kind: Instant, Nullable: false},
	}},
	{Name: "property_notes", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "property_id", Kind: UUID, Nullable: false},
		{Name: "submission_key", Kind: Text, Nullable: false},
		{Name: "body", Kind: Text, Nullable: false},
		{Name: "occurred_on", Kind: Date, Nullable: false},
		{Name: "recorded_by", Kind: Text, Nullable: false},
		{Name: "recorded_at", Kind: Instant, Nullable: false},
	}},
	{Name: "property_photos", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "property_id", Kind: UUID, Nullable: false},
		{Name: "submission_key", Kind: Text, Nullable: false},
		{Name: "content_hash", Kind: Text, Nullable: false},
		{Name: "caption", Kind: Text, Nullable: false},
		{Name: "category", Kind: Text, Nullable: false},
		{Name: "content_type", Kind: Text, Nullable: false},
		{Name: "image_data", Kind: Bytes, Nullable: true},
		{Name: "recorded_by", Kind: Text, Nullable: false},
		{Name: "recorded_at", Kind: Instant, Nullable: false},
		{Name: "deleted_at", Kind: Instant, Nullable: true},
		{Name: "object_key", Kind: Text, Nullable: true},
		{Name: "object_version", Kind: Text, Nullable: true},
		{Name: "object_pending", Kind: Boolean, Nullable: false},
	}},
	{Name: "property_photo_objects", Key: []string{"object_key"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "object_key", Kind: Text, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "photo_id", Kind: UUID, Nullable: false},
		{Name: "content_hash", Kind: Text, Nullable: false},
		{Name: "content_type", Kind: Text, Nullable: false},
		{Name: "byte_length", Kind: Integer, Nullable: false},
		{Name: "version_id", Kind: Text, Nullable: true},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "retired_at", Kind: Instant, Nullable: true},
		{Name: "purged_at", Kind: Instant, Nullable: true},
	}},
	{Name: "asset_notes", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "vehicle_id", Kind: UUID, Nullable: true},
		{Name: "equipment_id", Kind: UUID, Nullable: true},
		{Name: "submission_key", Kind: Text, Nullable: false},
		{Name: "body", Kind: Text, Nullable: false},
		{Name: "occurred_on", Kind: Date, Nullable: false},
		{Name: "occurred_at", Kind: Instant, Nullable: true},
		{Name: "event_timezone", Kind: Text, Nullable: false},
		{Name: "recorded_by", Kind: Text, Nullable: false},
		{Name: "recorded_at", Kind: Instant, Nullable: false},
	}},
	{Name: "jobs", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "customer_id", Kind: UUID, Nullable: false},
		{Name: "property_id", Kind: UUID, Nullable: true},
		{Name: "submission_key", Kind: Text, Nullable: false},
		{Name: "creation_fingerprint", Kind: Text, Nullable: false},
		{Name: "kind", Kind: Text, Nullable: false},
		{Name: "title", Kind: Text, Nullable: false},
		{Name: "details", Kind: Text, Nullable: false},
		{Name: "scheduled_on", Kind: Date, Nullable: true},
		{Name: "status", Kind: Text, Nullable: false},
		{Name: "completed_at", Kind: Instant, Nullable: true},
		{Name: "revision", Kind: Integer, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "updated_at", Kind: Instant, Nullable: false},
	}},
	{Name: "job_events", Key: []string{"shop_id", "job_id", "revision"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "job_id", Kind: UUID, Nullable: false},
		{Name: "revision", Kind: Integer, Nullable: false},
		{Name: "action", Kind: Text, Nullable: false},
		{Name: "snapshot", Kind: JSON, Nullable: false},
		{Name: "recorded_by", Kind: Text, Nullable: false},
		{Name: "recorded_at", Kind: Instant, Nullable: false},
	}},
	{Name: "business_settings", Key: []string{"shop_id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "primary_trade", Kind: Text, Nullable: false},
		{Name: "trades", Kind: TextArray, Nullable: false},
		{Name: "revision", Kind: Integer, Nullable: false},
	}},
	{Name: "business_services", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "preset_key", Kind: Text, Nullable: false},
		{Name: "preset_version", Kind: Integer, Nullable: false},
		{Name: "name", Kind: Text, Nullable: false},
		{Name: "description", Kind: Text, Nullable: false},
		{Name: "enabled", Kind: Boolean, Nullable: false},
		{Name: "revision", Kind: Integer, Nullable: false},
	}},
	{Name: "plan_templates", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "name", Kind: Text, Nullable: false},
		{Name: "description", Kind: Text, Nullable: true},
		{Name: "price_cents", Kind: Integer, Nullable: false},
		{Name: "billing_interval", Kind: Text, Nullable: false},
		{Name: "visits_per_year", Kind: Integer, Nullable: false},
		{Name: "visit_windows", Kind: TextArray, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "term_months", Kind: Integer, Nullable: false},
	}},
	{Name: "agreements", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "customer_id", Kind: UUID, Nullable: false},
		{Name: "property_id", Kind: UUID, Nullable: true},
		{Name: "plan_template_id", Kind: UUID, Nullable: true},
		{Name: "external_id", Kind: Text, Nullable: true},
		{Name: "status", Kind: Text, Nullable: false},
		{Name: "anniversary_date", Kind: Date, Nullable: false},
		{Name: "visits_entitled", Kind: Integer, Nullable: false},
		{Name: "visits_used", Kind: Integer, Nullable: false},
		{Name: "price_cents", Kind: Integer, Nullable: false},
		{Name: "notes", Kind: Text, Nullable: true},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "updated_at", Kind: Instant, Nullable: false},
		{Name: "plan_snapshot_name", Kind: Text, Nullable: false},
		{Name: "plan_snapshot_description", Kind: Text, Nullable: false},
		{Name: "plan_snapshot_billing_interval", Kind: Text, Nullable: false},
		{Name: "plan_snapshot_term_months", Kind: Integer, Nullable: false},
		{Name: "plan_snapshot_visits_per_year", Kind: Integer, Nullable: false},
		{Name: "plan_snapshot_visit_windows", Kind: TextArray, Nullable: false},
	}},
	{Name: "visit_dues", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "agreement_id", Kind: UUID, Nullable: false},
		{Name: "season", Kind: Text, Nullable: false},
		{Name: "due_year", Kind: Integer, Nullable: false},
		{Name: "status", Kind: Text, Nullable: false},
		{Name: "scheduled_at", Kind: Instant, Nullable: true},
		{Name: "completed_at", Kind: Instant, Nullable: true},
		{Name: "notes", Kind: Text, Nullable: true},
		{Name: "created_at", Kind: Instant, Nullable: false},
	}},
	{Name: "service_schedules", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "agreement_id", Kind: UUID, Nullable: false},
		{Name: "external_id", Kind: Text, Nullable: true},
		{Name: "service_name", Kind: Text, Nullable: false},
		{Name: "cadence_unit", Kind: Text, Nullable: false},
		{Name: "cadence_interval", Kind: Integer, Nullable: false},
		{Name: "starts_on", Kind: Date, Nullable: false},
		{Name: "ends_on", Kind: Date, Nullable: true},
		{Name: "generated_through", Kind: Date, Nullable: true},
		{Name: "active", Kind: Boolean, Nullable: false},
		{Name: "paused_at", Kind: Instant, Nullable: true},
		{Name: "counts_toward_entitlement", Kind: Boolean, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "updated_at", Kind: Instant, Nullable: false},
	}},
	{Name: "service_occurrences", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "schedule_id", Kind: UUID, Nullable: false},
		{Name: "due_on", Kind: Date, Nullable: false},
		{Name: "status", Kind: Text, Nullable: false},
		{Name: "completed_at", Kind: Instant, Nullable: true},
		{Name: "notes", Kind: Text, Nullable: true},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "updated_at", Kind: Instant, Nullable: false},
		{Name: "scheduled_on", Kind: Date, Nullable: true},
		{Name: "schedule_revision", Kind: Integer, Nullable: false},
	}},
	{Name: "visit_notes", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "occurrence_id", Kind: UUID, Nullable: true},
		{Name: "seasonal_visit_id", Kind: UUID, Nullable: true},
		{Name: "submission_key", Kind: Text, Nullable: false},
		{Name: "body", Kind: Text, Nullable: false},
		{Name: "occurred_on", Kind: Date, Nullable: false},
		{Name: "occurred_at", Kind: Instant, Nullable: true},
		{Name: "event_timezone", Kind: Text, Nullable: false},
		{Name: "recorded_by", Kind: Text, Nullable: false},
		{Name: "recorded_at", Kind: Instant, Nullable: false},
	}},
	{Name: "renewal_links", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "agreement_id", Kind: UUID, Nullable: false},
		{Name: "token_hash", Kind: Text, Nullable: false},
		{Name: "expires_at", Kind: Instant, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
	}},
	{Name: "renewal_tasks", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "agreement_id", Kind: UUID, Nullable: false},
		{Name: "milestone_days", Kind: Integer, Nullable: false},
		{Name: "status", Kind: Text, Nullable: false},
		{Name: "notes", Kind: Text, Nullable: true},
		{Name: "updated_at", Kind: Instant, Nullable: false},
		{Name: "cycle_start_date", Kind: Date, Nullable: false},
		{Name: "due_at", Kind: Date, Nullable: false},
	}},
	{Name: "renewal_digest_outbox", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "digest_date", Kind: Date, Nullable: false},
		{Name: "task_ids", Kind: UUIDArray, Nullable: false},
		{Name: "status", Kind: Text, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
		{Name: "sent_at", Kind: Instant, Nullable: true},
	}},
	{Name: "demo_import_previews", Key: []string{"token_hash"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "token_hash", Kind: Text, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "csv_data", Kind: Bytes, Nullable: false},
		{Name: "row_count", Kind: Integer, Nullable: false},
		{Name: "projected_occurrences", Kind: Integer, Nullable: false},
		{Name: "projection_date", Kind: Date, Nullable: false},
		{Name: "expires_at", Kind: Instant, Nullable: false},
		{Name: "created_at", Kind: Instant, Nullable: false},
	}},
	{Name: "import_rows", Key: []string{"id"}, ShopColumn: "shop_id", Columns: []Column{
		{Name: "id", Kind: UUID, Nullable: false},
		{Name: "shop_id", Kind: UUID, Nullable: false},
		{Name: "source", Kind: Text, Nullable: false},
		{Name: "external_id", Kind: Text, Nullable: false},
		{Name: "entity_type", Kind: Text, Nullable: false},
		{Name: "entity_id", Kind: UUID, Nullable: false},
		{Name: "content_hash", Kind: Text, Nullable: true},
		{Name: "imported_at", Kind: Instant, Nullable: false},
	}},
}

var CurrentProfile = Profile{LogicalSchema: LogicalSchema, Catalog: Catalog, PostgresMigrations: 31, SQLServerMigrations: 30}

var ProductionV29Profile = Profile{
	LogicalSchema:      ProductionV29LogicalSchema,
	Catalog:            productionV29Catalog(Catalog),
	PostgresMigrations: 29, SQLServerMigrations: 30,
}

func productionV29Catalog(catalog []Table) []Table {
	result := withoutTables(catalog, "jobs", "job_events", "property_photo_objects")
	for index := range result {
		if result[index].Name == "property_photos" {
			// PostgreSQL migration 031 added Azure object handoff fields.
			// SQL defaults those target-only columns to NULL/false for v29 rows.
			result[index].Columns = append([]Column(nil), result[index].Columns[:12]...)
		}
	}
	return result
}

func withoutTables(catalog []Table, names ...string) []Table {
	excluded := make(map[string]struct{}, len(names))
	for _, name := range names {
		excluded[name] = struct{}{}
	}
	result := make([]Table, 0, len(catalog)-len(excluded))
	for _, table := range catalog {
		if _, skip := excluded[table.Name]; !skip {
			result = append(result, table)
		}
	}
	return result
}

func profileBySchema(schema string) (Profile, bool) {
	switch schema {
	case LogicalSchema:
		return CurrentProfile, true
	case ProductionV29LogicalSchema:
		return ProductionV29Profile, true
	default:
		return Profile{}, false
	}
}
