package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/product"
)

type Repository struct {
	pool      *pgxpool.Pool
	cursorKey []byte
}

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }
func NewWithCursorKey(pool *pgxpool.Pool, key []byte) *Repository {
	return &Repository{pool: pool, cursorKey: append([]byte(nil), key...)}
}

func (r *Repository) CreateBlueprint(ctx context.Context, record core.CreateBlueprintRecord) (core.Blueprint, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Blueprint{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.Blueprint](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	b := record.Blueprint
	_, err = tx.Exec(ctx, `INSERT INTO assembly.product_blueprints(blueprint_id,revision,product_id,document_version,schema_version,document,content_sha256,created_by,created_at) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9)`, b.BlueprintID, b.Revision, b.ProductID, b.DocumentVersion, b.SchemaVersion, string(b.Document), b.ContentSHA256, b.CreatedBy, b.CreatedAt)
	if err != nil {
		return core.Blueprint{}, mapWriteError(err)
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return core.Blueprint{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, b.BlueprintID, b); err != nil {
		return core.Blueprint{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Blueprint{}, err
	}
	return b, nil
}

func (r *Repository) GetBlueprint(ctx context.Context, productID, blueprintID string, revision int64) (core.Blueprint, error) {
	query := blueprintSelect + ` WHERE b.blueprint_id=$1 AND ($2='' OR b.product_id=$2)`
	args := []any{blueprintID, productID}
	if revision > 0 {
		query += ` AND b.revision=$3`
		args = append(args, revision)
	} else {
		query += ` ORDER BY b.revision DESC LIMIT 1`
	}
	return scanBlueprint(r.pool.QueryRow(ctx, query, args...))
}

func (r *Repository) CreatePlan(ctx context.Context, record core.CreatePlanRecord) (core.Plan, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Plan{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.Plan](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	p := record.Plan
	_, err = tx.Exec(ctx, `INSERT INTO assembly.assembly_plans(plan_id,blueprint_id,blueprint_revision,product_id,version,environment,schema_version,document,blueprint_sha256,catalog_revision,catalog_snapshot_sha256,plan_sha256,executable,created_by,created_at,updated_at) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15)`, p.PlanID, p.BlueprintID, p.BlueprintRevision, p.ProductID, p.Version, p.Environment, p.SchemaVersion, string(p.Document), p.BlueprintSHA256, p.CatalogRevision, p.CatalogSnapshotSHA256, p.PlanSHA256, p.Executable, p.CreatedBy, p.CreatedAt)
	if err != nil {
		return core.Plan{}, mapWriteError(err)
	}
	for _, item := range p.Capabilities {
		if _, err := tx.Exec(ctx, `INSERT INTO assembly.plan_capabilities(plan_id,product_id,capability_id,enabled,policy,source_package_id,source_package_version) VALUES($1,NULLIF($2,''),$3,$4,$5,$6,$7)`, p.PlanID, p.ProductID, item.CapabilityID, item.Enabled, item.Policy, item.SourcePackageID, item.SourcePackageVersion); err != nil {
			return core.Plan{}, mapWriteError(err)
		}
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return core.Plan{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, p.PlanID, p); err != nil {
		return core.Plan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Plan{}, err
	}
	return p, nil
}

func (r *Repository) GetPlan(ctx context.Context, productID, planID string) (core.Plan, error) {
	p, err := scanPlan(r.pool.QueryRow(ctx, planSelect+` WHERE p.plan_id=$1 AND ($2='' OR p.product_id=$2)`, planID, productID))
	if err != nil {
		return core.Plan{}, err
	}
	capabilities, err := loadCapabilities(ctx, r.pool, p.PlanID)
	if err != nil {
		return core.Plan{}, err
	}
	p.Capabilities = capabilities
	return p, nil
}

func (r *Repository) ConfirmPlan(ctx context.Context, record core.ConfirmPlanRecord) (core.Plan, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Plan{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.Plan](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	p, err := scanPlan(tx.QueryRow(ctx, planSelect+` WHERE p.plan_id=$1 AND ($2='' OR p.product_id=$2) FOR UPDATE`, record.PlanID, record.ProductID))
	if err != nil {
		return core.Plan{}, err
	}
	if p.Version != record.ExpectedVersion {
		return core.Plan{}, core.ErrVersionConflict
	}
	if p.ConfirmedAt == nil {
		p.Version++
		p.ConfirmedAt = &record.ConfirmedAt
		p.ConfirmedBy = record.ConfirmedBy
		p.UpdatedAt = record.ConfirmedAt
		p.AuditID = record.Event.Payload.AuditID
		if _, err := tx.Exec(ctx, `UPDATE assembly.assembly_plans SET version=$3,confirmed_at=$4,confirmed_by=$5,updated_at=$4 WHERE plan_id=$1 AND version=$2`, p.PlanID, record.ExpectedVersion, p.Version, record.ConfirmedAt, record.ConfirmedBy); err != nil {
			return core.Plan{}, err
		}
		if err := insertOutbox(ctx, tx, record.Event); err != nil {
			return core.Plan{}, err
		}
	}
	p.Capabilities, err = loadCapabilities(ctx, tx, p.PlanID)
	if err != nil {
		return core.Plan{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, p.PlanID, p); err != nil {
		return core.Plan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Plan{}, err
	}
	return p, nil
}

func (r *Repository) StartRun(ctx context.Context, record core.StartRunRecord) (core.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Run{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.Run](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	run := record.Run
	if run.IdempotencyKeyDigest == "" || run.IdempotencyKeyDigest != record.Idempotency.KeyDigest {
		return core.Run{}, core.ErrIdempotencyConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO assembly.assembly_runs(run_id,root_run_id,retry_of_run_id,attempt_number,plan_id,plan_version,product_id,version,plan_sha256,schema_version,document,document_sha256,idempotency_key_digest,output_target_ref,status,current_step_id,diagnostic_ids,recovery,created_by,created_at,updated_at,completed_at) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15,NULLIF($16,''),$17,$18,$19,$20,$21,$22)`, run.RunID, run.RootRunID, run.RetryOfRunID, run.AttemptNumber, run.PlanID, run.PlanVersion, run.ProductID, run.Version, run.PlanSHA256, run.SchemaVersion, string(run.Document), run.DocumentSHA256, record.Idempotency.KeyDigest, run.OutputTargetRef, run.Status, run.CurrentStepID, jsonStringArrayValue(run.DiagnosticIDs), jsonValue(run.Recovery), run.CreatedBy, run.CreatedAt, run.UpdatedAt, run.CompletedAt)
	if err != nil {
		return core.Run{}, mapWriteError(err)
	}
	if err := replaceRunSteps(ctx, tx, run); err != nil {
		return core.Run{}, err
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return core.Run{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO assembly.assembly_run_dispatches(run_id,state,attempt_count,available_at,created_at,updated_at) VALUES($1,'pending',0,$2,$2,$2)`, run.RunID, run.CreatedAt); err != nil {
		return core.Run{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, run.RunID, run); err != nil {
		return core.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Run{}, err
	}
	return run, nil
}

func (r *Repository) RetryRun(ctx context.Context, record core.RetryRunRecord) (core.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Run{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.Run](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	parent, err := scanRun(tx.QueryRow(ctx, runSelect+` WHERE r.run_id=$1 FOR UPDATE`, record.ParentRun.RunID))
	if err != nil {
		return core.Run{}, err
	}
	if parent.Version != record.ExpectedVersion {
		return core.Run{}, core.ErrVersionConflict
	}
	if parent.Status != core.RunStatusFailed || !parent.Recovery.Retryable || parent.Recovery.RollbackRequired {
		return core.Run{}, core.ErrConflict
	}
	run := record.Run
	if run.RootRunID != parent.RootRunID || run.RetryOfRunID != parent.RunID || run.AttemptNumber != parent.AttemptNumber+1 {
		return core.Run{}, core.ErrConflict
	}
	if run.IdempotencyKeyDigest == "" || run.IdempotencyKeyDigest != record.Idempotency.KeyDigest {
		return core.Run{}, core.ErrIdempotencyConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO assembly.assembly_runs(run_id,root_run_id,retry_of_run_id,attempt_number,plan_id,plan_version,product_id,version,plan_sha256,schema_version,document,document_sha256,idempotency_key_digest,output_target_ref,status,current_step_id,diagnostic_ids,recovery,created_by,created_at,updated_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15,NULLIF($16,''),$17,$18,$19,$20,$21,$22)`, run.RunID, run.RootRunID, run.RetryOfRunID, run.AttemptNumber, run.PlanID, run.PlanVersion, run.ProductID, run.Version, run.PlanSHA256, run.SchemaVersion, string(run.Document), run.DocumentSHA256, run.IdempotencyKeyDigest, run.OutputTargetRef, run.Status, run.CurrentStepID, jsonStringArrayValue(run.DiagnosticIDs), jsonValue(run.Recovery), run.CreatedBy, run.CreatedAt, run.UpdatedAt, run.CompletedAt)
	if err != nil {
		return core.Run{}, mapWriteError(err)
	}
	if err := upsertRunSteps(ctx, tx, run); err != nil {
		return core.Run{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO assembly.assembly_run_dispatches(run_id,state,attempt_count,available_at,created_at,updated_at) VALUES($1,'pending',0,$2,$2,$2)`, run.RunID, run.CreatedAt); err != nil {
		return core.Run{}, err
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return core.Run{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, run.RunID, run); err != nil {
		return core.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Run{}, err
	}
	return run, nil
}

func (r *Repository) BindProduct(ctx context.Context, record core.BindProductRecord) (core.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Run{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.Run](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	run, err := scanRun(tx.QueryRow(ctx, runSelect+` WHERE r.run_id=$1 FOR UPDATE`, record.RunID))
	if err != nil {
		return core.Run{}, err
	}
	if run.ProductID != "" && run.ProductID != record.ProductID {
		return core.Run{}, core.ErrConflict
	}
	if run.Version != record.ExpectedVersion {
		return core.Run{}, core.ErrVersionConflict
	}
	if run.ProductID == "" {
		if _, err := tx.Exec(ctx, `UPDATE assembly.product_blueprints b SET product_id=$2 FROM assembly.assembly_plans p WHERE p.plan_id=$1 AND b.blueprint_id=p.blueprint_id AND b.revision=p.blueprint_revision AND b.product_id IS NULL`, run.PlanID, record.ProductID); err != nil {
			return core.Run{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE assembly.assembly_plans SET product_id=$2 WHERE plan_id=$1 AND product_id IS NULL`, run.PlanID, record.ProductID); err != nil {
			return core.Run{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE assembly.plan_capabilities SET product_id=$2 WHERE plan_id=$1 AND product_id IS NULL`, run.PlanID, record.ProductID); err != nil {
			return core.Run{}, err
		}
		run.ProductID = record.ProductID
		run.Version++
		run.UpdatedAt = record.BoundAt
		run.AuditID = record.Event.Payload.AuditID
		if _, err := tx.Exec(ctx, `UPDATE assembly.assembly_runs SET product_id=$2,version=$3,updated_at=$4 WHERE run_id=$1 AND product_id IS NULL`, run.RunID, run.ProductID, run.Version, run.UpdatedAt); err != nil {
			return core.Run{}, err
		}
		if err := insertOutbox(ctx, tx, record.Event); err != nil {
			return core.Run{}, err
		}
	}
	run.Steps, err = loadRunSteps(ctx, tx, run.RunID)
	if err != nil {
		return core.Run{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, run.RunID, run); err != nil {
		return core.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Run{}, err
	}
	return run, nil
}

func (r *Repository) GetRun(ctx context.Context, productID, runID string) (core.Run, error) {
	run, err := scanRun(r.pool.QueryRow(ctx, runSelect+` WHERE r.run_id=$1 AND ($2='' OR r.product_id=$2)`, runID, productID))
	if err != nil {
		return core.Run{}, err
	}
	run.Steps, err = loadRunSteps(ctx, r.pool, run.RunID)
	if err == nil {
		run.Diagnostics, err = loadRunDiagnostics(ctx, r.pool, run.RunID)
	}
	if err == nil {
		run.Reports, err = loadRunReports(ctx, r.pool, run.RunID)
	}
	return run, err
}

func (r *Repository) ListRuns(ctx context.Context, filter core.RunListFilter) (core.RunPage, error) {
	var cursorTime *time.Time
	var cursorRun string
	if filter.Cursor != "" {
		decoded, err := r.decodeRunCursor(filter.Cursor, filter.Status, filter.ProductID)
		if err != nil {
			return core.RunPage{}, core.ErrInvalidCommand
		}
		cursorTime, cursorRun = &decoded.CreatedAt, decoded.RunID
	}
	rows, err := r.pool.Query(ctx, `SELECT r.run_id,COALESCE(r.product_id,''),r.plan_id,r.version,r.root_run_id,COALESCE(r.retry_of_run_id,''),r.attempt_number,r.status,COALESCE(r.current_step_id,''),(SELECT count(*) FROM assembly.assembly_run_diagnostics d WHERE d.run_id=r.run_id),(SELECT count(*) FROM assembly.assembly_run_reports p WHERE p.run_id=r.run_id),r.created_at,r.updated_at,r.completed_at FROM assembly.assembly_runs r WHERE ($1='' OR r.status=$1) AND ($2='' OR r.product_id=$2) AND ($3::timestamptz IS NULL OR (r.created_at,r.run_id)<($3,$4)) ORDER BY r.created_at DESC,r.run_id DESC LIMIT $5`, filter.Status, filter.ProductID, cursorTime, cursorRun, filter.PageSize+1)
	if err != nil {
		return core.RunPage{}, err
	}
	items := make([]core.RunSummary, 0, filter.PageSize+1)
	for rows.Next() {
		var item core.RunSummary
		if err := rows.Scan(&item.RunID, &item.ProductID, &item.PlanID, &item.Version, &item.RootRunID, &item.RetryOfRunID, &item.AttemptNumber, &item.Status, &item.CurrentStepID, &item.DiagnosticCount, &item.ReportCount, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt); err != nil {
			rows.Close()
			return core.RunPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return core.RunPage{}, err
	}
	rows.Close()
	page := core.RunPage{Items: items}
	if len(items) > filter.PageSize {
		last := items[filter.PageSize-1]
		page.Items = items[:filter.PageSize]
		page.NextCursor = r.encodeRunCursor(runCursor{CreatedAt: last.CreatedAt, RunID: last.RunID, Status: filter.Status, ProductID: filter.ProductID})
	}
	return page, nil
}

func (r *Repository) UpdateRun(ctx context.Context, record core.UpdateRunRecord) (core.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Run{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.Run](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	var version int64
	var productID string
	err = tx.QueryRow(ctx, `SELECT version,COALESCE(product_id,'') FROM assembly.assembly_runs WHERE run_id=$1 AND ($2='' OR product_id=$2) FOR UPDATE`, record.Run.RunID, record.Run.ProductID).Scan(&version, &productID)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Run{}, core.ErrNotFound
	}
	if err != nil {
		return core.Run{}, err
	}
	if version != record.ExpectedVersion {
		return core.Run{}, core.ErrVersionConflict
	}
	run := record.Run
	run.ProductID = productID
	run.Version = version + 1
	_, err = tx.Exec(ctx, `UPDATE assembly.assembly_runs SET version=$2,schema_version=$3,document=$4,document_sha256=$5,status=$6,current_step_id=NULLIF($7,''),diagnostic_ids=$8,recovery=$9,updated_at=$10,completed_at=$11 WHERE run_id=$1 AND version=$12`, run.RunID, run.Version, run.SchemaVersion, string(run.Document), run.DocumentSHA256, run.Status, run.CurrentStepID, jsonStringArrayValue(run.DiagnosticIDs), jsonValue(run.Recovery), run.UpdatedAt, run.CompletedAt, record.ExpectedVersion)
	if err != nil {
		return core.Run{}, err
	}
	if err := replaceRunSteps(ctx, tx, run); err != nil {
		return core.Run{}, err
	}
	if err := insertRunDiagnostics(ctx, tx, run, record.Diagnostics); err != nil {
		return core.Run{}, err
	}
	if err := insertRunReports(ctx, tx, run.RunID, record.Reports); err != nil {
		return core.Run{}, err
	}
	run.Diagnostics = append([]core.RunDiagnostic(nil), record.Diagnostics...)
	run.Reports = append([]core.RunReport(nil), record.Reports...)
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return core.Run{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, run.RunID, run); err != nil {
		return core.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Run{}, err
	}
	return run, nil
}

func (r *Repository) CompleteRun(ctx context.Context, record core.CompleteRunRecord) (core.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Run{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.Run](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	var version int64
	var productID string
	err = tx.QueryRow(ctx, `SELECT version,COALESCE(product_id,'') FROM assembly.assembly_runs WHERE run_id=$1 AND product_id=$2 FOR UPDATE`, record.Run.RunID, record.Run.ProductID).Scan(&version, &productID)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Run{}, core.ErrNotFound
	}
	if err != nil {
		return core.Run{}, err
	}
	if version != record.ExpectedVersion {
		return core.Run{}, core.ErrVersionConflict
	}
	m, l := record.Manifest, record.Lock
	if _, err := tx.Exec(ctx, `INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, m.AssemblyID, m.ProductID, m.RunID, m.SchemaVersion, string(m.Document), m.DocumentSHA256, m.ManifestSHA256, m.CreatedAt); err != nil {
		return core.Run{}, mapWriteError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, l.LockID, l.ProductID, l.RunID, l.AssemblyID, l.SchemaVersion, string(l.Document), l.DocumentSHA256, l.LockSHA256, l.CreatedAt); err != nil {
		return core.Run{}, mapWriteError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO assembly.lifecycle_heads(root_assembly_id,product_id,current_manifest_id,current_lock_id,version,updated_at) VALUES($1,$2,$1,$3,1,GREATEST($4::timestamptz,$5::timestamptz))`, m.AssemblyID, m.ProductID, l.LockID, m.CreatedAt, l.CreatedAt); err != nil {
		return core.Run{}, mapWriteError(err)
	}
	run := record.Run
	run.Version = version + 1
	run.ManifestID = m.AssemblyID
	run.LockID = l.LockID
	_, err = tx.Exec(ctx, `UPDATE assembly.assembly_runs SET version=$2,schema_version=$3,document=$4,document_sha256=$5,status='completed',current_step_id=NULLIF($6,''),diagnostic_ids=$7,recovery=$8,manifest_id=$9,lock_id=$10,updated_at=$11,completed_at=$12 WHERE run_id=$1 AND version=$13`, run.RunID, run.Version, run.SchemaVersion, string(run.Document), run.DocumentSHA256, run.CurrentStepID, jsonStringArrayValue(run.DiagnosticIDs), jsonValue(run.Recovery), run.ManifestID, run.LockID, run.UpdatedAt, run.CompletedAt, record.ExpectedVersion)
	if err != nil {
		return core.Run{}, err
	}
	if err := replaceRunSteps(ctx, tx, run); err != nil {
		return core.Run{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO assembly.assembly_run_reports(run_id,report_id,report_type,status,summary,checksum,created_at) VALUES($1,$2,'assembly_validation','passed','Assembly validation and artifact publication completed',$3,$4) ON CONFLICT DO NOTHING`, run.RunID, "report.validation", m.ManifestSHA256, run.UpdatedAt); err != nil {
		return core.Run{}, err
	}
	run.Reports = []core.RunReport{{ReportID: "report.validation", ReportType: "assembly_validation", Status: "passed", Summary: "Assembly validation and artifact publication completed", Checksum: m.ManifestSHA256, CreatedAt: run.UpdatedAt}}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return core.Run{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, run.RunID, run); err != nil {
		return core.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Run{}, err
	}
	return run, nil
}

func (r *Repository) GetManifest(ctx context.Context, productID, assemblyID string) (core.Manifest, error) {
	var m core.Manifest
	err := r.pool.QueryRow(ctx, `SELECT assembly_id,product_id,COALESCE(run_id,''),COALESCE(lifecycle_operation_id,''),schema_version,document,document_sha256,manifest_sha256,created_at FROM assembly.assembly_manifests WHERE assembly_id=$2 AND ($1='' OR product_id=$1)`, productID, assemblyID).Scan(&m.AssemblyID, &m.ProductID, &m.RunID, &m.LifecycleOperationID, &m.SchemaVersion, &m.Document, &m.DocumentSHA256, &m.ManifestSHA256, &m.CreatedAt)
	return m, mapNotFound(err)
}
func (r *Repository) GetLock(ctx context.Context, productID, lockID string) (core.GeneratedProjectLock, error) {
	var l core.GeneratedProjectLock
	err := r.pool.QueryRow(ctx, `SELECT lock_id,product_id,COALESCE(run_id,''),COALESCE(lifecycle_operation_id,''),assembly_id,schema_version,document,document_sha256,lock_sha256,created_at FROM assembly.generated_project_locks WHERE lock_id=$2 AND ($1='' OR product_id=$1)`, productID, lockID).Scan(&l.LockID, &l.ProductID, &l.RunID, &l.LifecycleOperationID, &l.AssemblyID, &l.SchemaVersion, &l.Document, &l.DocumentSHA256, &l.LockSHA256, &l.CreatedAt)
	return l, mapNotFound(err)
}

func (r *Repository) ClaimDispatch(ctx context.Context, workerID string, now time.Time, lease time.Duration) (core.Dispatch, error) {
	if workerID == "" || lease <= 0 {
		return core.Dispatch{}, core.ErrInvalidCommand
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Dispatch{}, err
	}
	defer rollback(tx, ctx)
	var item core.Dispatch
	err = tx.QueryRow(ctx, `SELECT d.run_id,r.root_run_id,r.created_by,d.attempt_count FROM assembly.assembly_run_dispatches d JOIN assembly.assembly_runs r ON r.run_id=d.run_id WHERE (d.state='pending' AND d.available_at<=$1) OR (d.state='leased' AND d.lease_until<=$1) ORDER BY d.available_at,d.created_at,d.run_id LIMIT 1 FOR UPDATE OF d SKIP LOCKED`, now).Scan(&item.RunID, &item.RootRunID, &item.CreatedBy, &item.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Dispatch{}, core.ErrNotFound
	}
	if err != nil {
		return core.Dispatch{}, err
	}
	item.AttemptCount++
	if _, err = tx.Exec(ctx, `UPDATE assembly.assembly_run_dispatches SET state='leased',attempt_count=attempt_count+1,lease_owner=$2,lease_until=$3,updated_at=$1 WHERE run_id=$4`, now, workerID, now.Add(lease), item.RunID); err != nil {
		return core.Dispatch{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return core.Dispatch{}, err
	}
	return item, nil
}

func (r *Repository) RenewDispatch(ctx context.Context, runID, workerID string, now time.Time, lease time.Duration) error {
	tag, err := r.pool.Exec(ctx, `UPDATE assembly.assembly_run_dispatches SET lease_until=$4,updated_at=$3 WHERE run_id=$1 AND state='leased' AND lease_owner=$2 AND lease_until>$3`, runID, workerID, now, now.Add(lease))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return core.ErrConflict
	}
	return nil
}

func (r *Repository) CompleteDispatch(ctx context.Context, runID, workerID string, now time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE assembly.assembly_run_dispatches SET state='completed',lease_owner=NULL,lease_until=NULL,last_error_code=NULL,updated_at=$3 WHERE run_id=$1 AND state='leased' AND lease_owner=$2`, runID, workerID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return core.ErrConflict
	}
	return nil
}

func (r *Repository) RequeueDispatch(ctx context.Context, runID, workerID, errorCode string, now, availableAt time.Time, dead bool) error {
	state := "pending"
	if dead {
		state = "dead"
	}
	tag, err := r.pool.Exec(ctx, `UPDATE assembly.assembly_run_dispatches SET state=$3,available_at=$4,lease_owner=NULL,lease_until=NULL,last_error_code=$5,updated_at=$6 WHERE run_id=$1 AND state='leased' AND lease_owner=$2`, runID, workerID, state, availableAt, errorCode, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return core.ErrConflict
	}
	return nil
}

func (r *Repository) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]core.ClaimedOutboxEvent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx, ctx)
	rows, err := tx.Query(ctx, `SELECT event_id,aggregate_id,event_type,payload,occurred_at,attempt_count FROM assembly.outbox_events WHERE published_at IS NULL AND dead=FALSE AND next_attempt_at<=$1 ORDER BY occurred_at,event_id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]core.ClaimedOutboxEvent, 0)
	for rows.Next() {
		var item core.ClaimedOutboxEvent
		var raw []byte
		if err := rows.Scan(&item.EventID, &item.AggregateID, &item.EventType, &raw, &item.OccurredAt, &item.AttemptCount); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Payload); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].AttemptCount++
		if _, err := tx.Exec(ctx, `UPDATE assembly.outbox_events SET attempt_count=attempt_count+1,next_attempt_at=$2 WHERE event_id=$1`, items[i].EventID, now.Add(30*time.Second)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}
func (r *Repository) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE assembly.outbox_events SET published_at=COALESCE(published_at,$2),last_error=NULL WHERE event_id=$1`, eventID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return core.ErrNotFound
	}
	return nil
}
func (r *Repository) MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE assembly.outbox_events SET next_attempt_at=$2,last_error=$3,dead=$4 WHERE event_id=$1 AND published_at IS NULL`, eventID, next, summary, dead)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return core.ErrNotFound
	}
	return nil
}

const blueprintSelect = `SELECT b.blueprint_id,COALESCE(b.product_id,''),b.revision,b.document_version,b.schema_version,b.document,b.content_sha256,b.created_by,b.created_at,COALESCE((SELECT e.payload->>'audit_id' FROM assembly.outbox_events e WHERE e.aggregate_id=b.blueprint_id AND e.event_type='assembly.blueprint_created.v1' ORDER BY e.occurred_at LIMIT 1),'') FROM assembly.product_blueprints b`
const planSelect = `SELECT p.plan_id,COALESCE(p.product_id,''),p.blueprint_id,p.blueprint_revision,p.version,p.environment,p.schema_version,p.document,p.blueprint_sha256,p.catalog_revision,p.catalog_snapshot_sha256,p.plan_sha256,p.executable,p.confirmed_at,COALESCE(p.confirmed_by,''),p.created_by,p.created_at,p.updated_at,COALESCE((SELECT e.payload->>'audit_id' FROM assembly.outbox_events e WHERE e.aggregate_id=p.plan_id ORDER BY e.occurred_at DESC LIMIT 1),'') FROM assembly.assembly_plans p`
const runSelect = `SELECT r.run_id,COALESCE(r.product_id,''),r.root_run_id,COALESCE(r.retry_of_run_id,''),r.attempt_number,r.plan_id,r.plan_version,r.version,r.plan_sha256,r.schema_version,r.document,r.document_sha256,r.idempotency_key_digest,r.output_target_ref,r.status,COALESCE(r.current_step_id,''),r.diagnostic_ids,r.recovery,COALESCE(r.manifest_id,''),COALESCE(r.lock_id,''),r.created_by,r.created_at,r.updated_at,r.completed_at,COALESCE((SELECT e.payload->>'audit_id' FROM assembly.outbox_events e WHERE e.aggregate_id=r.run_id ORDER BY e.occurred_at DESC LIMIT 1),'') FROM assembly.assembly_runs r`

type scanner interface{ Scan(...any) error }

func scanBlueprint(row scanner) (core.Blueprint, error) {
	var b core.Blueprint
	err := row.Scan(&b.BlueprintID, &b.ProductID, &b.Revision, &b.DocumentVersion, &b.SchemaVersion, &b.Document, &b.ContentSHA256, &b.CreatedBy, &b.CreatedAt, &b.AuditID)
	return b, mapNotFound(err)
}
func scanPlan(row scanner) (core.Plan, error) {
	var p core.Plan
	err := row.Scan(&p.PlanID, &p.ProductID, &p.BlueprintID, &p.BlueprintRevision, &p.Version, &p.Environment, &p.SchemaVersion, &p.Document, &p.BlueprintSHA256, &p.CatalogRevision, &p.CatalogSnapshotSHA256, &p.PlanSHA256, &p.Executable, &p.ConfirmedAt, &p.ConfirmedBy, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt, &p.AuditID)
	return p, mapNotFound(err)
}
func scanRun(row scanner) (core.Run, error) {
	var r core.Run
	var diagnostics, recovery []byte
	err := row.Scan(&r.RunID, &r.ProductID, &r.RootRunID, &r.RetryOfRunID, &r.AttemptNumber, &r.PlanID, &r.PlanVersion, &r.Version, &r.PlanSHA256, &r.SchemaVersion, &r.Document, &r.DocumentSHA256, &r.IdempotencyKeyDigest, &r.OutputTargetRef, &r.Status, &r.CurrentStepID, &diagnostics, &recovery, &r.ManifestID, &r.LockID, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt, &r.AuditID)
	if err != nil {
		return core.Run{}, mapNotFound(err)
	}
	if err := json.Unmarshal(diagnostics, &r.DiagnosticIDs); err != nil {
		return core.Run{}, err
	}
	if err := json.Unmarshal(recovery, &r.Recovery); err != nil {
		return core.Run{}, err
	}
	return r, nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadCapabilities(ctx context.Context, q queryer, planID string) ([]product.CapabilityItem, error) {
	rows, err := q.Query(ctx, `SELECT capability_id,enabled,policy,source_package_id,source_package_version FROM assembly.plan_capabilities WHERE plan_id=$1 ORDER BY capability_id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]product.CapabilityItem, 0)
	for rows.Next() {
		var item product.CapabilityItem
		if err := rows.Scan(&item.CapabilityID, &item.Enabled, &item.Policy, &item.SourcePackageID, &item.SourcePackageVersion); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func loadRunSteps(ctx context.Context, q queryer, runID string) ([]core.RunStep, error) {
	rows, err := q.Query(ctx, `SELECT step_id,kind,status,attempt,compensation_status,started_at,finished_at,diagnostic_ids FROM assembly.assembly_run_steps WHERE run_id=$1 ORDER BY ordinal`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := make([]core.RunStep, 0)
	for rows.Next() {
		var step core.RunStep
		var diagnostics []byte
		if err := rows.Scan(&step.StepID, &step.Kind, &step.Status, &step.Attempt, &step.CompensationStatus, &step.StartedAt, &step.FinishedAt, &diagnostics); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(diagnostics, &step.DiagnosticIDs); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}
func replaceRunSteps(ctx context.Context, tx pgx.Tx, run core.Run) error {
	return upsertRunSteps(ctx, tx, run)
}

func upsertRunSteps(ctx context.Context, tx pgx.Tx, run core.Run) error {
	for i, step := range run.Steps {
		if _, err := tx.Exec(ctx, `INSERT INTO assembly.assembly_run_steps(run_id,step_id,ordinal,kind,status,attempt,compensation_status,started_at,finished_at,diagnostic_ids) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(run_id,step_id) DO UPDATE SET status=EXCLUDED.status,attempt=EXCLUDED.attempt,compensation_status=EXCLUDED.compensation_status,started_at=EXCLUDED.started_at,finished_at=EXCLUDED.finished_at,diagnostic_ids=EXCLUDED.diagnostic_ids`, run.RunID, step.StepID, i, step.Kind, step.Status, step.Attempt, step.CompensationStatus, step.StartedAt, step.FinishedAt, jsonStringArrayValue(step.DiagnosticIDs)); err != nil {
			return err
		}
	}
	return nil
}

func loadRunDiagnostics(ctx context.Context, q queryer, runID string) ([]core.RunDiagnostic, error) {
	rows, err := q.Query(ctx, `SELECT diagnostic_id,code,severity,category,message,blocking,retryable,remediation,related_paths,created_at FROM assembly.assembly_run_diagnostics WHERE run_id=$1 ORDER BY created_at,diagnostic_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]core.RunDiagnostic, 0)
	for rows.Next() {
		var item core.RunDiagnostic
		var remediation, paths []byte
		if err := rows.Scan(&item.DiagnosticID, &item.Code, &item.Severity, &item.Category, &item.Message, &item.Blocking, &item.Retryable, &remediation, &paths, &item.CreatedAt); err != nil {
			return nil, err
		}
		if json.Unmarshal(remediation, &item.Remediation) != nil || json.Unmarshal(paths, &item.RelatedPaths) != nil {
			return nil, errors.New("invalid stored assembly diagnostic")
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadRunReports(ctx context.Context, q queryer, runID string) ([]core.RunReport, error) {
	rows, err := q.Query(ctx, `SELECT report_id,report_type,status,summary,COALESCE(checksum,''),created_at FROM assembly.assembly_run_reports WHERE run_id=$1 ORDER BY created_at,report_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]core.RunReport, 0)
	for rows.Next() {
		var item core.RunReport
		if err := rows.Scan(&item.ReportID, &item.ReportType, &item.Status, &item.Summary, &item.Checksum, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func insertRunDiagnostics(ctx context.Context, tx pgx.Tx, run core.Run, values []core.RunDiagnostic) error {
	for _, value := range values {
		if _, err := tx.Exec(ctx, `INSERT INTO assembly.assembly_run_diagnostics(run_id,diagnostic_id,code,severity,category,message,blocking,retryable,remediation,related_paths,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT DO NOTHING`, run.RunID, value.DiagnosticID, value.Code, value.Severity, value.Category, value.Message, value.Blocking, value.Retryable, jsonStringArrayValue(value.Remediation), jsonStringArrayValue(value.RelatedPaths), value.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func insertRunReports(ctx context.Context, tx pgx.Tx, runID string, values []core.RunReport) error {
	for _, value := range values {
		if _, err := tx.Exec(ctx, `INSERT INTO assembly.assembly_run_reports(run_id,report_id,report_type,status,summary,checksum,created_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7) ON CONFLICT DO NOTHING`, runID, value.ReportID, value.ReportType, value.Status, value.Summary, value.Checksum, value.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

type runCursor struct {
	CreatedAt time.Time      `json:"created_at"`
	RunID     string         `json:"run_id"`
	Status    core.RunStatus `json:"status,omitempty"`
	ProductID string         `json:"product_id,omitempty"`
	Signature string         `json:"signature"`
}

func (r *Repository) cursorSignature(value runCursor) string {
	value.Signature = ""
	raw, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, r.cursorKey)
	_, _ = mac.Write([]byte("assembly-run-cursor-v1:"))
	_, _ = mac.Write(raw)
	return fmt.Sprintf("%x", mac.Sum(nil))
}
func (r *Repository) encodeRunCursor(value runCursor) string {
	value.Signature = r.cursorSignature(value)
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (r *Repository) decodeRunCursor(value string, status core.RunStatus, productID string) (runCursor, error) {
	if len(r.cursorKey) < 32 || len(value) > 1024 {
		return runCursor{}, errors.New("cursor unavailable")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return runCursor{}, err
	}
	var result runCursor
	if json.Unmarshal(raw, &result) != nil || result.RunID == "" || result.CreatedAt.IsZero() || result.Status != status || result.ProductID != productID {
		return runCursor{}, errors.New("invalid cursor")
	}
	expected := r.cursorSignature(result)
	if len(result.Signature) != len(expected) || !hmac.Equal([]byte(result.Signature), []byte(expected)) {
		return runCursor{}, errors.New("invalid cursor signature")
	}
	return result, nil
}

func reserveIdempotency[T any](ctx context.Context, tx pgx.Tx, key core.Idempotency) (T, bool, error) {
	var zero T
	var requestDigest, state string
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT request_digest,state,response_json FROM assembly.idempotency_records WHERE operation=$1 AND actor_id=$2 AND scope_id=$3 AND key_digest=$4 FOR UPDATE`, key.Operation, key.ActorID, key.ScopeID, key.KeyDigest).Scan(&requestDigest, &state, &raw)
	if err == nil {
		if requestDigest != key.RequestDigest {
			return zero, false, core.ErrIdempotencyConflict
		}
		if state != "completed" || len(raw) == 0 {
			return zero, false, core.ErrOperationInProgress
		}
		if err := json.Unmarshal(raw, &zero); err != nil {
			return zero, false, err
		}
		return zero, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, false, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO assembly.idempotency_records(operation,actor_id,scope_id,key_digest,request_digest,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'pending',$6,$6) ON CONFLICT DO NOTHING`, key.Operation, key.ActorID, key.ScopeID, key.KeyDigest, key.RequestDigest, key.Now)
	if err != nil {
		return zero, false, err
	}
	if tag.RowsAffected() == 1 {
		return zero, true, nil
	}
	return zero, false, core.ErrOperationInProgress
}
func completeIdempotency(ctx context.Context, tx pgx.Tx, key core.Idempotency, resourceID string, response any) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE assembly.idempotency_records SET resource_id=$5,state='completed',response_json=$6,updated_at=$7 WHERE operation=$1 AND actor_id=$2 AND scope_id=$3 AND key_digest=$4`, key.Operation, key.ActorID, key.ScopeID, key.KeyDigest, resourceID, raw, key.Now)
	return err
}
func insertOutbox(ctx context.Context, tx pgx.Tx, event core.OutboxEvent) error {
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO assembly.outbox_events(event_id,aggregate_id,event_type,payload,occurred_at,next_attempt_at) VALUES($1,$2,$3,$4,$5,$5)`, event.EventID, event.AggregateID, event.EventType, raw, event.OccurredAt)
	return err
}
func jsonValue(value any) []byte { raw, _ := json.Marshal(value); return raw }
func jsonStringArrayValue(value []string) []byte {
	if value == nil {
		return []byte("[]")
	}
	return jsonValue(value)
}
func rollback(tx pgx.Tx, ctx context.Context) { _ = tx.Rollback(ctx) }
func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotFound
	}
	return err
}
func mapWriteError(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return fmt.Errorf("%w: %s", core.ErrConflict, databaseError.ConstraintName)
	}
	return err
}
