package migrations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/migrations"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

const (
	lineageDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lineageDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	lineageDigestC = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	lineageDigestD = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func TestPostgreSQLMigrationsDownHandlesMultiGenerationLifecycleLineage(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 21, 0, 0, 0, time.UTC)
	seedMultiGenerationLifecycleLineage(t, database, now)

	if err := migrations.ApplyDownAll(ctx, database.Pool, database.MigrationPath); err != nil {
		t.Fatalf("ApplyDownAll() with multi-generation lifecycle lineage error = %v", err)
	}
	var assemblySchemaExists bool
	if err := database.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='assembly')`).Scan(&assemblySchemaExists); err != nil {
		t.Fatalf("query assembly schema after down: %v", err)
	}
	if assemblySchemaExists {
		t.Fatal("assembly schema still exists after ApplyDownAll")
	}

	if err := migrations.ApplyUp(ctx, database.Pool, database.MigrationPath); err != nil {
		t.Fatalf("ApplyUp() after lineage down error = %v", err)
	}
	var migrationCount int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM platform_meta.schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count reapplied migrations: %v", err)
	}
	if migrationCount != 15 {
		t.Fatalf("reapplied migration count = %d, want 15", migrationCount)
	}
	assertMigrationState(t, database)
}

func seedMultiGenerationLifecycleLineage(t *testing.T, database testpostgres.Database, now time.Time) {
	t.Helper()
	ctx := context.Background()
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO assembly.product_blueprints(blueprint_id,revision,document_version,schema_version,document,content_sha256,created_by,created_at) VALUES('bp.lineage-down',1,'1.0.0','1.0.0','{}',$1,'admin.test',$2)`, []any{lineageDigestA, now}},
		{`INSERT INTO assembly.assembly_plans(plan_id,blueprint_id,blueprint_revision,version,environment,schema_version,document,blueprint_sha256,catalog_revision,catalog_snapshot_sha256,plan_sha256,executable,created_by,created_at,updated_at) VALUES('plan.lineage-down','bp.lineage-down',1,1,'test','1.0.0','{}',$1,'catalog.test',$1,$1,TRUE,'admin.test',$2,$2)`, []any{lineageDigestA, now}},
		{`INSERT INTO assembly.assembly_runs(run_id,root_run_id,attempt_number,plan_id,plan_version,version,plan_sha256,schema_version,document,document_sha256,idempotency_key_digest,output_target_ref,status,diagnostic_ids,recovery,created_by,created_at,updated_at) VALUES('run.lineage-down','run.lineage-down',1,'plan.lineage-down',1,1,$1,'1.0.0','{}',$1,$2,'workspace.default','planned','[]','{}','admin.test',$3,$3)`, []any{lineageDigestA, lineageDigestB, now}},
		{`INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES('assembly.lineage-root','product.lineage-down','run.lineage-down','1.0.0','{}',$1,$1,$2)`, []any{lineageDigestA, now}},
		{`INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES('lock.lineage-root','product.lineage-down','run.lineage-down','assembly.lineage-root','1.0.0',$1,$2,$2,$3)`, []any{`{"catalog_checksum":"` + lineageDigestA + `","target_snapshot_checksum":"` + lineageDigestA + `"}`, lineageDigestB, now}},
		{`INSERT INTO assembly.lifecycle_heads(root_assembly_id,product_id,current_manifest_id,current_lock_id,version,updated_at) VALUES('assembly.lineage-root','product.lineage-down','assembly.lineage-root','lock.lineage-root',1,$1)`, []any{now}},
		{`INSERT INTO assembly.lifecycle_plans(lifecycle_plan_id,assembly_id,product_id,operation,version,schema_version,document,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,source_catalog_checksum,source_target_snapshot_checksum,target_snapshot_checksum,blocking_conflict_count,executable,confirmation_checksum,plan_checksum,created_by,created_at) VALUES('lifecycle.lineage-first','assembly.lineage-root','product.lineage-down','upgrade',1,'1.0.0','{}','assembly.lineage-root',$1,'lock.lineage-root',$2,$1,$1,$1,0,TRUE,$1,$3,'admin.test',$4)`, []any{lineageDigestA, lineageDigestB, lineageDigestC, now.Add(time.Minute)}},
	}
	for _, item := range queries {
		if _, err := database.Pool.Exec(ctx, item.query, item.args...); err != nil {
			t.Fatal(err)
		}
	}

	sourceState := map[string]any{"manifest_id": "assembly.lineage-root", "manifest_checksum": lineageDigestA, "lock_id": "lock.lineage-root", "lock_checksum": lineageDigestB, "catalog_checksum": lineageDigestA, "target_snapshot_checksum": lineageDigestA}
	targetState := map[string]any{"manifest_id": "assembly.lineage-successor", "manifest_checksum": lineageDigestB, "lock_id": "lock.lineage-successor", "lock_checksum": lineageDigestC, "catalog_checksum": lineageDigestA, "target_snapshot_checksum": lineageDigestB}
	sourceRaw := lineageJSON(t, sourceState)
	targetRaw := lineageJSON(t, targetState)
	createdAt := now.Add(2 * time.Minute)
	completedAt := now.Add(3 * time.Minute)

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_operations(operation_id,root_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,version,status,current_step,schema_version,document,source_state,target_state,recovery,diagnostic_ids,report_ids,operation_checksum,idempotency_key_digest,created_by,created_at,updated_at) VALUES('operation.lineage-first','operation.lineage-first','lifecycle.lineage-first','assembly.lineage-root','product.lineage-down','upgrade',1,'executing','step.execute','1.0.0','{}',$1,NULL,'{}','[]','[]',$2,$3,'admin.test',$4,$4)`, sourceRaw, lineageDigestA, lineageDigestD, createdAt); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `INSERT INTO assembly.lifecycle_artifact_transitions(operation_id,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,rollback_journal,created_at) VALUES('operation.lineage-first','assembly.lineage-root',$1,'lock.lineage-root',$2,'{}',$3)`, lineageDigestA, lineageDigestB, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_operations SET version=2,status='completed',current_step=NULL,target_state=$2,operation_checksum=$3,updated_at=$4,completed_at=$4 WHERE operation_id=$1`, "operation.lineage-first", targetRaw, lineageDigestB, completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,lifecycle_operation_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES('assembly.lineage-successor','product.lineage-down',NULL,'operation.lineage-first','1.0.0','{}',$1,$2,$3)`, lineageDigestC, lineageDigestB, completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,lifecycle_operation_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES('lock.lineage-successor','product.lineage-down',NULL,'operation.lineage-first','assembly.lineage-successor','1.0.0',$1,$2,$3,$4)`, `{"catalog_checksum":"`+lineageDigestA+`","target_snapshot_checksum":"`+lineageDigestB+`"}`, lineageDigestD, lineageDigestC, completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_artifact_transitions SET target_manifest_id='assembly.lineage-successor',target_manifest_checksum=$2,target_manifest_document='{}',target_lock_id='lock.lineage-successor',target_lock_checksum=$3,target_lock_document='{}',rollback_journal='{"state":"committed"}',completed_at=$4 WHERE operation_id=$1`, "operation.lineage-first", lineageDigestB, lineageDigestC, completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_heads SET current_manifest_id='assembly.lineage-successor',current_lock_id='lock.lineage-successor',version=2,updated_by_operation_id='operation.lineage-first',updated_at=$2 WHERE root_assembly_id=$1`, "assembly.lineage-root", completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `INSERT INTO assembly.lifecycle_plans(lifecycle_plan_id,assembly_id,product_id,operation,version,schema_version,document,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,source_catalog_checksum,source_target_snapshot_checksum,target_snapshot_checksum,blocking_conflict_count,executable,confirmation_checksum,plan_checksum,created_by,created_at) VALUES('lifecycle.lineage-second','assembly.lineage-root','product.lineage-down','eject',1,'1.0.0','{}','assembly.lineage-successor',$1,'lock.lineage-successor',$2,$3,$1,$1,0,TRUE,$3,$4,'admin.test',$5)`, lineageDigestB, lineageDigestC, lineageDigestA, lineageDigestD, completedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	secondSourceState := targetState
	secondTargetState := map[string]any{"manifest_id": "assembly.lineage-successor-2", "manifest_checksum": lineageDigestC, "lock_id": "lock.lineage-successor-2", "lock_checksum": lineageDigestD, "catalog_checksum": lineageDigestA, "target_snapshot_checksum": lineageDigestC}
	secondSourceRaw := lineageJSON(t, secondSourceState)
	secondTargetRaw := lineageJSON(t, secondTargetState)
	secondCreatedAt := completedAt.Add(2 * time.Minute)
	secondCompletedAt := completedAt.Add(3 * time.Minute)
	tx, err = database.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_operations(operation_id,root_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,version,status,current_step,schema_version,document,source_state,target_state,recovery,diagnostic_ids,report_ids,operation_checksum,idempotency_key_digest,created_by,created_at,updated_at) VALUES('operation.lineage-second','operation.lineage-second','lifecycle.lineage-second','assembly.lineage-root','product.lineage-down','eject',1,'executing','step.execute','1.0.0','{}',$1,NULL,'{}','[]','[]',$2,$3,'admin.test',$4,$4)`, secondSourceRaw, lineageDigestB, lineageDigestC, secondCreatedAt); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `INSERT INTO assembly.lifecycle_artifact_transitions(operation_id,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,rollback_journal,created_at) VALUES('operation.lineage-second','assembly.lineage-successor',$1,'lock.lineage-successor',$2,'{}',$3)`, lineageDigestB, lineageDigestC, secondCreatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_operations SET version=2,status='completed',current_step=NULL,target_state=$2,operation_checksum=$3,updated_at=$4,completed_at=$4 WHERE operation_id=$1`, "operation.lineage-second", secondTargetRaw, lineageDigestC, secondCompletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,lifecycle_operation_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES('assembly.lineage-successor-2','product.lineage-down',NULL,'operation.lineage-second','1.0.0','{}',$1,$2,$3)`, lineageDigestD, lineageDigestC, secondCompletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,lifecycle_operation_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES('lock.lineage-successor-2','product.lineage-down',NULL,'operation.lineage-second','assembly.lineage-successor-2','1.0.0',$1,$2,$3,$4)`, `{"catalog_checksum":"`+lineageDigestA+`","target_snapshot_checksum":"`+lineageDigestC+`"}`, lineageDigestA, lineageDigestD, secondCompletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_artifact_transitions SET target_manifest_id='assembly.lineage-successor-2',target_manifest_checksum=$2,target_manifest_document='{}',target_lock_id='lock.lineage-successor-2',target_lock_checksum=$3,target_lock_document='{}',rollback_journal='{"state":"committed"}',completed_at=$4 WHERE operation_id=$1`, "operation.lineage-second", lineageDigestC, lineageDigestD, secondCompletedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_heads SET current_manifest_id='assembly.lineage-successor-2',current_lock_id='lock.lineage-successor-2',version=3,updated_by_operation_id='operation.lineage-second',updated_at=$2 WHERE root_assembly_id=$1`, "assembly.lineage-root", secondCompletedAt); err != nil {
		t.Fatal(err)
	}

	var headManifestID, headLockID string
	var headVersion int64
	if err = database.Pool.QueryRow(ctx, `SELECT current_manifest_id,current_lock_id,version FROM assembly.lifecycle_heads WHERE root_assembly_id='assembly.lineage-root'`).Scan(&headManifestID, &headLockID, &headVersion); err != nil {
		t.Fatal(err)
	}
	if headManifestID != "assembly.lineage-successor-2" || headLockID != "lock.lineage-successor-2" || headVersion != 3 {
		t.Fatalf("seeded lifecycle head = %s/%s/v%d, want second successor/v3", headManifestID, headLockID, headVersion)
	}
}

func lineageJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
