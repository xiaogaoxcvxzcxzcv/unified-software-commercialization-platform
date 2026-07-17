package migrations_test

import (
	"context"
	"testing"

	"platform.local/capability-platform/backend/internal/platform/migrations"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestPostgreSQLMigrationsUpRepeatDownAndReapply(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()

	if err := migrations.ApplyUp(ctx, database.Pool, database.MigrationPath); err != nil {
		t.Fatalf("repeat ApplyUp() error = %v", err)
	}
	assertMigrationState(t, database)

	if err := migrations.ApplyDownAll(ctx, database.Pool, database.MigrationPath); err != nil {
		t.Fatalf("ApplyDownAll() error = %v", err)
	}
	for _, schema := range []string{"platform_meta", "identity", "access_control", "audit", "product", "product_application", "tenant", "assembly", "notification", "hosted_interaction"} {
		var exists bool
		if err := database.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, schema).Scan(&exists); err != nil {
			t.Fatalf("query schema %q after down: %v", schema, err)
		}
		if exists {
			t.Fatalf("schema %q still exists after ApplyDownAll", schema)
		}
	}

	if err := migrations.ApplyUp(ctx, database.Pool, database.MigrationPath); err != nil {
		t.Fatalf("reapply ApplyUp() error = %v", err)
	}
	assertMigrationState(t, database)
}

func assertMigrationState(t *testing.T, database testpostgres.Database) {
	t.Helper()
	ctx := context.Background()
	loaded, err := migrations.Load(database.MigrationPath)
	if err != nil {
		t.Fatalf("load repository migrations: %v", err)
	}
	wantHistory := len(loaded)
	var history int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM platform_meta.schema_migrations`).Scan(&history); err != nil {
		t.Fatalf("count migration history: %v", err)
	}
	if history != wantHistory {
		t.Fatalf("migration history count = %d, want %d", history, wantHistory)
	}
	for _, relation := range []string{
		"platform_meta.installation",
		"identity.admin_sessions",
		"identity.admin_session_tokens",
		"identity.admin_auth_clients",
		"identity.admin_auth_client_credentials",
		"identity.outbox_events",
		"identity.external_auth_flows",
		"identity.external_identity_proofs",
		"identity.registration_verification_challenges",
		"identity.hosted_auth_proofs",
		"identity.hosted_grant_redemptions",
		"access_control.admin_scope_bindings",
		"access_control.scope_binding_idempotency_records",
		"access_control.outbox_events",
		"audit.events",
		"product.products",
		"product.product_clients",
		"product.product_client_credentials",
		"product.client_sessions",
		"product.product_capability_sets",
		"product_application.product_applications",
		"product_application.application_client_bindings",
		"product_application.redirect_policy_versions",
		"notification.security_deliveries",
		"notification.security_delivery_attempts",
		"notification.outbox_events",
		"hosted_interaction.interactions",
		"hosted_interaction.browser_sessions",
		"hosted_interaction.completion_grants",
		"hosted_interaction.idempotency_records",
		"hosted_interaction.outbox_events",
		"tenant.product_tenants",
		"tenant.distribution_bindings",
		"assembly.product_blueprints",
		"assembly.assembly_plans",
		"assembly.plan_capabilities",
		"assembly.assembly_runs",
		"assembly.assembly_run_steps",
		"assembly.assembly_run_dispatches",
		"assembly.assembly_run_diagnostics",
		"assembly.assembly_run_reports",
		"assembly.lifecycle_plans",
		"assembly.lifecycle_operations",
		"assembly.lifecycle_heads",
		"assembly.lifecycle_dispatches",
		"assembly.lifecycle_artifact_transitions",
		"assembly.lifecycle_diagnostics",
		"assembly.lifecycle_reports",
		"assembly.assembly_manifests",
		"assembly.generated_project_locks",
		"assembly.idempotency_records",
		"assembly.outbox_events",
	} {
		var found *string
		if err := database.Pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, relation).Scan(&found); err != nil {
			t.Fatalf("query relation %q: %v", relation, err)
		}
		if found == nil || *found != relation {
			t.Fatalf("relation %q is missing", relation)
		}
	}
	var triggerCount int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgname='audit_events_no_update' AND NOT tgisinternal`).Scan(&triggerCount); err != nil {
		t.Fatalf("query audit trigger: %v", err)
	}
	if triggerCount != 1 {
		t.Fatalf("audit append-only trigger count = %d, want 1", triggerCount)
	}

	for _, trigger := range []string{"products_identity_immutable", "product_applications_identity_immutable", "product_tenants_identity_immutable", "external_auth_flow_one_way", "external_identity_proof_one_way", "registration_verification_one_way", "security_delivery_attempt_immutable", "hosted_interaction_one_way", "hosted_browser_session_one_way", "hosted_idempotency_immutable", "hosted_grant_one_way", "hosted_outbox_one_way", "identity_hosted_auth_proof_one_way", "identity_hosted_grant_redemption_immutable", "assembly_blueprints_document_immutable", "assembly_plans_contract_immutable", "assembly_runs_contract_immutable", "assembly_runs_retry_chain_valid", "assembly_run_steps_contract_immutable", "assembly_runs_delete_immutable", "assembly_run_steps_delete_immutable", "assembly_run_diagnostics_immutable", "assembly_run_reports_immutable", "assembly_manifests_lifecycle_source_valid", "generated_project_locks_lifecycle_source_valid", "lifecycle_plans_immutable", "lifecycle_operations_insert_valid", "lifecycle_operations_contract_immutable", "lifecycle_operations_delete_immutable", "lifecycle_artifact_transitions_contract_immutable", "lifecycle_artifact_transitions_delete_immutable", "lifecycle_diagnostics_immutable", "lifecycle_reports_immutable", "lifecycle_heads_contract_immutable", "lifecycle_heads_delete_immutable"} {
		if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgname=$1 AND NOT tgisinternal`, trigger).Scan(&triggerCount); err != nil {
			t.Fatalf("query trigger %q: %v", trigger, err)
		}
		if triggerCount != 1 {
			t.Fatalf("trigger %q count = %d, want 1", trigger, triggerCount)
		}
	}
	for permission, risk := range map[string]string{
		"assembly.blueprint.manage":  "normal",
		"assembly.plan":              "normal",
		"assembly.execute":           "high",
		"assembly.lifecycle.execute": "high",
		"assembly.lifecycle.plan":    "normal",
		"assembly.read":              "normal",
	} {
		var actualRisk string
		if err := database.Pool.QueryRow(ctx, `SELECT risk_level FROM access_control.admin_permissions WHERE permission_code=$1`, permission).Scan(&actualRisk); err != nil {
			t.Fatalf("query permission %q: %v", permission, err)
		}
		if actualRisk != risk {
			t.Fatalf("permission %q risk = %q, want %q", permission, actualRisk, risk)
		}
	}
}
