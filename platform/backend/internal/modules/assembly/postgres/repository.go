package postgres

import (
	"context"
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

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

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
	_, err = tx.Exec(ctx, `INSERT INTO assembly.assembly_runs(run_id,plan_id,plan_version,product_id,version,plan_sha256,schema_version,document,document_sha256,idempotency_key_digest,output_target_ref,status,current_step_id,diagnostic_ids,recovery,created_by,created_at,updated_at,completed_at) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''),$14,$15,$16,$17,$18,$19)`, run.RunID, run.PlanID, run.PlanVersion, run.ProductID, run.Version, run.PlanSHA256, run.SchemaVersion, string(run.Document), run.DocumentSHA256, record.Idempotency.KeyDigest, run.OutputTargetRef, run.Status, run.CurrentStepID, jsonValue(run.DiagnosticIDs), jsonValue(run.Recovery), run.CreatedBy, run.CreatedAt, run.UpdatedAt, run.CompletedAt)
	if err != nil {
		return core.Run{}, mapWriteError(err)
	}
	if err := replaceRunSteps(ctx, tx, run); err != nil {
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
	return run, err
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
	_, err = tx.Exec(ctx, `UPDATE assembly.assembly_runs SET version=$2,schema_version=$3,document=$4,document_sha256=$5,status=$6,current_step_id=NULLIF($7,''),diagnostic_ids=$8,recovery=$9,updated_at=$10,completed_at=$11 WHERE run_id=$1 AND version=$12`, run.RunID, run.Version, run.SchemaVersion, string(run.Document), run.DocumentSHA256, run.Status, run.CurrentStepID, jsonValue(run.DiagnosticIDs), jsonValue(run.Recovery), run.UpdatedAt, run.CompletedAt, record.ExpectedVersion)
	if err != nil {
		return core.Run{}, err
	}
	if err := replaceRunSteps(ctx, tx, run); err != nil {
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
	run := record.Run
	run.Version = version + 1
	run.ManifestID = m.AssemblyID
	run.LockID = l.LockID
	_, err = tx.Exec(ctx, `UPDATE assembly.assembly_runs SET version=$2,schema_version=$3,document=$4,document_sha256=$5,status='completed',current_step_id=NULLIF($6,''),diagnostic_ids=$7,recovery=$8,manifest_id=$9,lock_id=$10,updated_at=$11,completed_at=$12 WHERE run_id=$1 AND version=$13`, run.RunID, run.Version, run.SchemaVersion, string(run.Document), run.DocumentSHA256, run.CurrentStepID, jsonValue(run.DiagnosticIDs), jsonValue(run.Recovery), run.ManifestID, run.LockID, run.UpdatedAt, run.CompletedAt, record.ExpectedVersion)
	if err != nil {
		return core.Run{}, err
	}
	if err := replaceRunSteps(ctx, tx, run); err != nil {
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

func (r *Repository) GetManifest(ctx context.Context, productID, assemblyID string) (core.Manifest, error) {
	var m core.Manifest
	err := r.pool.QueryRow(ctx, `SELECT assembly_id,product_id,run_id,schema_version,document,document_sha256,manifest_sha256,created_at FROM assembly.assembly_manifests WHERE assembly_id=$2 AND ($1='' OR product_id=$1)`, productID, assemblyID).Scan(&m.AssemblyID, &m.ProductID, &m.RunID, &m.SchemaVersion, &m.Document, &m.DocumentSHA256, &m.ManifestSHA256, &m.CreatedAt)
	return m, mapNotFound(err)
}
func (r *Repository) GetLock(ctx context.Context, productID, lockID string) (core.GeneratedProjectLock, error) {
	var l core.GeneratedProjectLock
	err := r.pool.QueryRow(ctx, `SELECT lock_id,product_id,run_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at FROM assembly.generated_project_locks WHERE lock_id=$2 AND ($1='' OR product_id=$1)`, productID, lockID).Scan(&l.LockID, &l.ProductID, &l.RunID, &l.AssemblyID, &l.SchemaVersion, &l.Document, &l.DocumentSHA256, &l.LockSHA256, &l.CreatedAt)
	return l, mapNotFound(err)
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
const runSelect = `SELECT r.run_id,COALESCE(r.product_id,''),r.plan_id,r.plan_version,r.version,r.plan_sha256,r.schema_version,r.document,r.document_sha256,r.idempotency_key_digest,r.output_target_ref,r.status,COALESCE(r.current_step_id,''),r.diagnostic_ids,r.recovery,COALESCE(r.manifest_id,''),COALESCE(r.lock_id,''),r.created_by,r.created_at,r.updated_at,r.completed_at,COALESCE((SELECT e.payload->>'audit_id' FROM assembly.outbox_events e WHERE e.aggregate_id=r.run_id ORDER BY e.occurred_at DESC LIMIT 1),'') FROM assembly.assembly_runs r`

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
	err := row.Scan(&r.RunID, &r.ProductID, &r.PlanID, &r.PlanVersion, &r.Version, &r.PlanSHA256, &r.SchemaVersion, &r.Document, &r.DocumentSHA256, &r.IdempotencyKeyDigest, &r.OutputTargetRef, &r.Status, &r.CurrentStepID, &diagnostics, &recovery, &r.ManifestID, &r.LockID, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt, &r.AuditID)
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
	if _, err := tx.Exec(ctx, `DELETE FROM assembly.assembly_run_steps WHERE run_id=$1`, run.RunID); err != nil {
		return err
	}
	for i, step := range run.Steps {
		if _, err := tx.Exec(ctx, `INSERT INTO assembly.assembly_run_steps(run_id,step_id,ordinal,kind,status,attempt,compensation_status,started_at,finished_at,diagnostic_ids) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, run.RunID, step.StepID, i, step.Kind, step.Status, step.Attempt, step.CompensationStatus, step.StartedAt, step.FinishedAt, jsonValue(step.DiagnosticIDs)); err != nil {
			return err
		}
	}
	return nil
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
func jsonValue(value any) []byte              { raw, _ := json.Marshal(value); return raw }
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
