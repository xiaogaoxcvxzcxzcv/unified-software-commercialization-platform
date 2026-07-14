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
	"platform.local/capability-platform/backend/internal/modules/product"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) BeginProvisioning(ctx context.Context, record product.BeginProvisioningRecord) (result product.Product, err error) {
	if r.pool == nil {
		return result, errors.New("product repository is not configured")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, err
	}
	defer rollback(tx, ctx)
	if replay, ok, err := claimIdempotency(ctx, tx, record.Idempotency, record.Product.CreatedAt); err != nil {
		return result, err
	} else if ok {
		if err := json.Unmarshal(replay, &result); err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `INSERT INTO product.products
		(product_id, product_code, name, status, provisioning_state, context_version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,'pending',$5,$6,$6)`, record.Product.ProductID, record.Product.ProductCode, record.Product.Name, record.Product.Status, record.Product.ContextVersion, record.Product.CreatedAt)
	if err != nil {
		return result, mapWriteError(err)
	}
	for _, environment := range record.Environments {
		if _, err := tx.Exec(ctx, `INSERT INTO product.product_environments
			(product_id, environment, status, context_version, created_at, updated_at)
			VALUES ($1,$2,'active',1,$3,$3)`, record.Product.ProductID, environment, record.Product.CreatedAt); err != nil {
			return result, mapWriteError(err)
		}
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return result, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, record.Product, record.Product.UpdatedAt); err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return record.Product, nil
}

func (r *Repository) CompleteProvisioning(ctx context.Context, record product.CompleteProvisioningRecord) (result product.Product, err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, err
	}
	defer rollback(tx, ctx)
	if replay, ok, err := claimIdempotency(ctx, tx, record.Idempotency, record.Now); err != nil {
		return result, err
	} else if ok {
		if err := json.Unmarshal(replay, &result); err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	row := tx.QueryRow(ctx, `SELECT product_id, product_code, name, status, provisioning_state,
		COALESCE(official_tenant_id,''), context_version, created_at, updated_at
		FROM product.products WHERE product_id=$1 FOR UPDATE`, record.ProductID)
	current, err := scanProduct(row)
	if err != nil {
		return result, mapNotFound(err)
	}
	switch current.ProvisioningState {
	case "pending":
		row = tx.QueryRow(ctx, `UPDATE product.products SET provisioning_state='ready', official_tenant_id=$2,
			context_version=context_version+1, updated_at=$3 WHERE product_id=$1
			RETURNING product_id, product_code, name, status, provisioning_state,
			COALESCE(official_tenant_id,''), context_version, created_at, updated_at`, record.ProductID, record.OfficialTenantID, record.Now)
		result, err = scanProduct(row)
		if err != nil {
			return result, err
		}
	case "ready":
		if current.OfficialTenantID != record.OfficialTenantID {
			return result, product.ErrProvisioningState
		}
		result = current
	default:
		return result, product.ErrProvisioningState
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return result, err
	}
	result.AuditID = record.Event.Payload.AuditID
	if err := completeIdempotency(ctx, tx, record.Idempotency, result, record.Now); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (r *Repository) ListProducts(ctx context.Context, limit int) ([]product.Product, error) {
	rows, err := r.pool.Query(ctx, `SELECT product_id, product_code, name, status, provisioning_state,
		COALESCE(official_tenant_id,''), context_version, created_at, updated_at
		FROM product.products ORDER BY created_at, product_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]product.Product, 0)
	for rows.Next() {
		item, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) GetProduct(ctx context.Context, productID string) (product.Product, error) {
	item, err := scanProduct(r.pool.QueryRow(ctx, `SELECT product_id, product_code, name, status, provisioning_state,
		COALESCE(official_tenant_id,''), context_version, created_at, updated_at
		FROM product.products WHERE product_id=$1`, productID))
	if err != nil {
		return product.Product{}, mapNotFound(err)
	}
	return item, nil
}

func (r *Repository) RegisterClient(ctx context.Context, record product.RegisterClientRecord) (result product.ClientAuthentication, err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, err
	}
	defer rollback(tx, ctx)
	if replay, ok, err := claimIdempotency(ctx, tx, record.Idempotency, record.Client.CreatedAt); err != nil {
		return result, err
	} else if ok {
		if err := json.Unmarshal(replay, &result); err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	var productCode, productStatus, provisioningState, environmentStatus string
	var productVersion, environmentVersion int64
	err = tx.QueryRow(ctx, `SELECT p.product_code, p.status, p.provisioning_state, p.context_version,
		e.status, e.context_version FROM product.products p
		JOIN product.product_environments e ON e.product_id=p.product_id AND e.environment=$2
		WHERE p.product_id=$1 FOR SHARE`, record.Client.ProductID, record.Client.Environment).
		Scan(&productCode, &productStatus, &provisioningState, &productVersion, &environmentStatus, &environmentVersion)
	if err != nil {
		return result, mapNotFound(err)
	}
	if productStatus != "active" || environmentStatus != "active" || provisioningState == "failed" {
		return result, product.ErrProductUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO product.product_clients
		(client_id, product_id, environment, status, context_version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$6)`, record.Client.ClientID, record.Client.ProductID, record.Client.Environment, record.Client.Status, record.Client.ContextVersion, record.Client.CreatedAt)
	if err != nil {
		return result, mapWriteError(err)
	}
	if err := insertCredential(ctx, tx, record.Credential); err != nil {
		return result, err
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return result, err
	}
	result = product.ClientAuthentication{Client: record.Client, Credential: record.Credential, Context: product.ProductContext{ProductID: record.Client.ProductID, ProductCode: productCode, Environment: record.Client.Environment, ContextVersion: compositeContextVersion(productVersion, environmentVersion)}}
	if err := completeIdempotency(ctx, tx, record.Idempotency, result, record.Client.UpdatedAt); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (r *Repository) RotateClientCredential(ctx context.Context, record product.RotateCredentialRecord) (result product.ClientCredential, err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, err
	}
	defer rollback(tx, ctx)
	if replay, ok, err := claimIdempotency(ctx, tx, record.Idempotency, record.Now); err != nil {
		return result, err
	} else if ok {
		if err := json.Unmarshal(replay, &result); err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	var currentGeneration int
	err = tx.QueryRow(ctx, `SELECT c.generation FROM product.product_client_credentials c
		JOIN product.product_clients pc ON pc.client_id=c.client_id AND pc.product_id=c.product_id
		WHERE c.product_id=$1 AND c.client_id=$2
		ORDER BY c.generation DESC LIMIT 1 FOR UPDATE`, record.ProductID, record.ClientID).Scan(&currentGeneration)
	if err != nil {
		return result, mapNotFound(err)
	}
	if currentGeneration != record.ExpectedGeneration {
		return result, product.ErrCredentialVersionConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE product.product_client_credentials SET status='revoked', revoked_at=$3
		WHERE product_id=$1 AND client_id=$2 AND status='active'`, record.ProductID, record.ClientID, record.Now); err != nil {
		return result, err
	}
	if err := insertCredential(ctx, tx, record.Credential); err != nil {
		return result, err
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return result, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, record.Credential, record.Now); err != nil {
		return result, err
	}
	return record.Credential, tx.Commit(ctx)
}

func (r *Repository) RevokeClientCredential(ctx context.Context, record product.RevokeCredentialRecord) (result product.ClientCredential, err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, err
	}
	defer rollback(tx, ctx)
	if replay, ok, err := claimIdempotency(ctx, tx, record.Idempotency, record.Now); err != nil {
		return result, err
	} else if ok {
		if err := json.Unmarshal(replay, &result); err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	result, err = scanCredential(tx.QueryRow(ctx, credentialSelect+` WHERE product_id=$1 AND client_id=$2 AND credential_id=$3 FOR UPDATE`, record.ProductID, record.ClientID, record.CredentialID))
	if err != nil {
		return result, mapNotFound(err)
	}
	if result.Status != "revoked" {
		if _, err := tx.Exec(ctx, `UPDATE product.product_client_credentials SET status='revoked', revoked_at=$4
			WHERE product_id=$1 AND client_id=$2 AND credential_id=$3`, record.ProductID, record.ClientID, record.CredentialID, record.Now); err != nil {
			return result, err
		}
		result.Status = "revoked"
		result.RevokedAt = &record.Now
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return result, err
	}
	result.AuditID = record.Event.Payload.AuditID
	if err := completeIdempotency(ctx, tx, record.Idempotency, result, record.Now); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (r *Repository) ResolveClientAuthentication(ctx context.Context, clientID, credentialID string, now time.Time) (product.ClientAuthentication, error) {
	row := r.pool.QueryRow(ctx, `SELECT pc.client_id, pc.product_id, pc.environment, pc.status, pc.context_version, pc.created_at, pc.updated_at,
		c.credential_id, c.proof_type, COALESCE(c.proof_digest,''), COALESCE(c.public_key,''), c.generation, c.status,
		c.not_before, c.expires_at, c.revoked_at, c.last_used_at, c.created_at,
		p.product_code, p.context_version, e.context_version
		FROM product.product_clients pc
		JOIN product.product_client_credentials c ON c.client_id=pc.client_id AND c.product_id=pc.product_id
		JOIN product.products p ON p.product_id=pc.product_id
		JOIN product.product_environments e ON e.product_id=pc.product_id AND e.environment=pc.environment
		WHERE pc.client_id=$1 AND c.credential_id=$2 AND pc.status='active' AND c.status='active'
		AND c.not_before <= $3 AND c.expires_at > $3 AND p.status='active' AND p.provisioning_state='ready' AND e.status='active'`, clientID, credentialID, now)
	var result product.ClientAuthentication
	var productVersion, environmentVersion int64
	err := row.Scan(&result.Client.ClientID, &result.Client.ProductID, &result.Client.Environment, &result.Client.Status, &result.Client.ContextVersion, &result.Client.CreatedAt, &result.Client.UpdatedAt,
		&result.Credential.CredentialID, &result.Credential.ProofType, &result.Credential.ProofDigest, &result.Credential.PublicKey, &result.Credential.Generation, &result.Credential.Status,
		&result.Credential.NotBefore, &result.Credential.ExpiresAt, &result.Credential.RevokedAt, &result.Credential.LastUsedAt, &result.Credential.CreatedAt,
		&result.Context.ProductCode, &productVersion, &environmentVersion)
	if err != nil {
		return result, mapUnavailable(err, product.ErrCredentialUnavailable)
	}
	result.Credential.ClientID = result.Client.ClientID
	result.Credential.ProductID = result.Client.ProductID
	result.Context.ProductID = result.Client.ProductID
	result.Context.Environment = result.Client.Environment
	result.Context.ContextVersion = compositeContextVersion(productVersion, environmentVersion)
	return result, nil
}

func (r *Repository) CreateClientSession(ctx context.Context, record product.CreateSessionRecord) (result product.StoredClientSession, err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, err
	}
	defer rollback(tx, ctx)
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM product.product_clients pc
		JOIN product.product_client_credentials c ON c.client_id=pc.client_id AND c.product_id=pc.product_id
		JOIN product.products p ON p.product_id=pc.product_id
		JOIN product.product_environments e ON e.product_id=pc.product_id AND e.environment=pc.environment
		WHERE pc.client_id=$1 AND pc.product_id=$2 AND c.credential_id=$3
		AND pc.status='active' AND c.status='active' AND c.not_before <= $4 AND c.expires_at > $4
		AND p.status='active' AND p.provisioning_state='ready' AND e.status='active')`, record.Authentication.Client.ClientID, record.Authentication.Client.ProductID, record.Authentication.Credential.CredentialID, record.Session.CreatedAt).Scan(&valid)
	if err != nil {
		return result, err
	}
	if !valid {
		return result, product.ErrClientUnavailable
	}
	tag, err := tx.Exec(ctx, `INSERT INTO product.client_proof_nonces
		(client_id, nonce_digest, request_digest, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, record.Session.ClientID, record.NonceDigest, record.RequestDigest, record.Session.ExpiresAt, record.Session.CreatedAt)
	if err != nil {
		return result, err
	}
	if tag.RowsAffected() != 1 {
		return result, product.ErrNonceReplayed
	}
	_, err = tx.Exec(ctx, `INSERT INTO product.client_sessions
		(session_id, token_digest, product_id, environment, application_id, tenant_id, client_id, credential_id,
		client_version, product_context_version, application_context_version, tenant_context_version, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, record.Session.SessionID, record.Session.TokenDigest, record.Session.ProductID,
		record.Session.Environment, record.Session.ApplicationID, record.Session.TenantID, record.Session.ClientID, record.Session.CredentialID,
		record.Session.ClientVersion, record.Session.ProductContextVersion, record.Session.ApplicationContextVersion, record.Session.TenantContextVersion,
		record.Session.CreatedAt, record.Session.ExpiresAt)
	if err != nil {
		return result, mapWriteError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE product.client_proof_nonces SET session_id=$3 WHERE client_id=$1 AND nonce_digest=$2`, record.Session.ClientID, record.NonceDigest, record.Session.SessionID); err != nil {
		return result, err
	}
	if _, err := tx.Exec(ctx, `UPDATE product.product_client_credentials SET last_used_at=$4
		WHERE product_id=$1 AND client_id=$2 AND credential_id=$3`, record.Session.ProductID, record.Session.ClientID, record.Session.CredentialID, record.Session.CreatedAt); err != nil {
		return result, err
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return result, err
	}
	return record.Session, tx.Commit(ctx)
}

func (r *Repository) FindClientSessionByTokenDigest(ctx context.Context, tokenDigest string, now time.Time) (product.StoredClientSession, error) {
	result, err := scanSession(r.pool.QueryRow(ctx, sessionSelect+` JOIN product.product_clients pc ON pc.client_id=s.client_id AND pc.product_id=s.product_id
		JOIN product.product_client_credentials c ON c.credential_id=s.credential_id AND c.client_id=s.client_id AND c.product_id=s.product_id
		JOIN product.products p ON p.product_id=s.product_id
		JOIN product.product_environments e ON e.product_id=s.product_id AND e.environment=s.environment
		WHERE s.token_digest=$1 AND s.revoked_at IS NULL AND s.expires_at>$2 AND pc.status='active' AND c.status='active'
		AND p.status='active' AND p.provisioning_state='ready' AND e.status='active'`, tokenDigest, now))
	if err != nil {
		return result, mapUnavailable(err, product.ErrSessionUnavailable)
	}
	return result, nil
}

func (r *Repository) RevokeClientSession(ctx context.Context, sessionID string, now time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE product.client_sessions SET revoked_at=COALESCE(revoked_at,$2) WHERE session_id=$1`, sessionID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return product.ErrNotFound
	}
	return nil
}

func (r *Repository) ReplaceCapabilitySet(ctx context.Context, record product.ReplaceCapabilitySetRecord) (result product.CapabilitySet, err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, err
	}
	defer rollback(tx, ctx)
	if replay, ok, err := claimIdempotency(ctx, tx, record.Idempotency, record.Set.CreatedAt); err != nil {
		return result, err
	} else if ok {
		if err := json.Unmarshal(replay, &result); err != nil {
			return result, err
		}
		return result, tx.Commit(ctx)
	}
	var productReady bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM product.products WHERE product_id=$1 AND status='active' AND provisioning_state='ready')`, record.Set.ProductID).Scan(&productReady); err != nil {
		return result, err
	}
	if !productReady {
		return result, product.ErrProductUnavailable
	}
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT version FROM product.product_capability_sets WHERE product_id=$1 ORDER BY version DESC LIMIT 1 FOR UPDATE`, record.Set.ProductID).Scan(&currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		currentVersion = 0
	} else if err != nil {
		return result, err
	}
	if currentVersion != record.ExpectedVersion {
		return result, product.ErrCapabilityVersionConflict
	}
	if record.Set.Version != currentVersion+1 {
		return result, product.ErrCapabilityVersionConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO product.product_capability_sets
		(capability_set_id, product_id, version, source_plan_id, catalog_revision, catalog_snapshot_sha256, content_sha256, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, record.Set.CapabilitySetID, record.Set.ProductID, record.Set.Version, record.Set.SourcePlanID,
		record.Set.CatalogRevision, record.Set.CatalogSnapshotSHA256, record.Set.ContentSHA256, record.Set.CreatedBy, record.Set.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return result, product.ErrCapabilityVersionConflict
		}
		return result, err
	}
	for _, item := range record.Set.Items {
		if _, err := tx.Exec(ctx, `INSERT INTO product.product_capability_items
			(capability_set_id, product_id, capability_id, enabled, policy, source_package_id, source_package_version)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, record.Set.CapabilitySetID, record.Set.ProductID, item.CapabilityID, item.Enabled, item.Policy, item.SourcePackageID, item.SourcePackageVersion); err != nil {
			return result, err
		}
	}
	if err := insertOutbox(ctx, tx, record.Event); err != nil {
		return result, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, record.Set, record.Set.CreatedAt); err != nil {
		return result, err
	}
	return record.Set, tx.Commit(ctx)
}

func (r *Repository) CurrentCapabilitySet(ctx context.Context, productID string) (product.CapabilitySet, error) {
	var result product.CapabilitySet
	err := r.pool.QueryRow(ctx, `SELECT capability_set_id, product_id, version, source_plan_id, catalog_revision,
		catalog_snapshot_sha256, content_sha256, created_by, created_at
		FROM product.product_capability_sets WHERE product_id=$1 ORDER BY version DESC LIMIT 1`, productID).
		Scan(&result.CapabilitySetID, &result.ProductID, &result.Version, &result.SourcePlanID, &result.CatalogRevision,
			&result.CatalogSnapshotSHA256, &result.ContentSHA256, &result.CreatedBy, &result.CreatedAt)
	if err != nil {
		return result, mapNotFound(err)
	}
	rows, err := r.pool.Query(ctx, `SELECT capability_id, enabled, policy, source_package_id, source_package_version
		FROM product.product_capability_items WHERE capability_set_id=$1 AND product_id=$2 ORDER BY capability_id`, result.CapabilitySetID, productID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item product.CapabilityItem
		if err := rows.Scan(&item.CapabilityID, &item.Enabled, &item.Policy, &item.SourcePackageID, &item.SourcePackageVersion); err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

func (r *Repository) AppendOutboxEvent(ctx context.Context, event product.OutboxEvent) error {
	if r.pool == nil {
		return errors.New("product repository is not configured")
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO product.outbox_events
		(event_id, aggregate_id, event_type, payload, occurred_at, next_attempt_at)
		VALUES ($1,$2,$3,$4,$5,$5)`, event.EventID, event.AggregateID, event.EventType, payload, event.OccurredAt)
	return err
}

func (r *Repository) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]product.ClaimedOutboxEvent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx, ctx)
	rows, err := tx.Query(ctx, `SELECT event_id, aggregate_id, event_type, payload, occurred_at, attempt_count
		FROM product.outbox_events WHERE published_at IS NULL AND dead=FALSE AND next_attempt_at <= $1
		ORDER BY occurred_at, event_id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]product.ClaimedOutboxEvent, 0)
	for rows.Next() {
		var item product.ClaimedOutboxEvent
		var payload []byte
		if err := rows.Scan(&item.EventID, &item.AggregateID, &item.EventType, &payload, &item.OccurredAt, &item.AttemptCount); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, fmt.Errorf("decode product outbox %s: %w", item.EventID, err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, item := range items {
		if _, err := tx.Exec(ctx, `UPDATE product.outbox_events SET attempt_count=attempt_count+1, next_attempt_at=$2 WHERE event_id=$1`, item.EventID, now.Add(30*time.Second)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repository) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE product.outbox_events SET published_at=$2, last_error=NULL WHERE event_id=$1 AND published_at IS NULL`, eventID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return product.ErrNotFound
	}
	return nil
}

func (r *Repository) MarkOutboxFailed(ctx context.Context, eventID, errorSummary string, next time.Time, dead bool) error {
	tag, err := r.pool.Exec(ctx, `UPDATE product.outbox_events SET next_attempt_at=$2, last_error=$3, dead=$4 WHERE event_id=$1 AND published_at IS NULL`, eventID, next, errorSummary, dead)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return product.ErrNotFound
	}
	return nil
}

const credentialSelect = `SELECT credential_id, client_id, product_id, proof_type, COALESCE(proof_digest,''),
	COALESCE(public_key,''), generation, status, not_before, expires_at, revoked_at, last_used_at, created_at
	FROM product.product_client_credentials`

const sessionSelect = `SELECT s.session_id, s.token_digest, s.product_id, s.environment, s.application_id, s.tenant_id,
	s.client_id, s.credential_id, s.client_version, s.product_context_version, s.application_context_version,
	s.tenant_context_version, s.created_at, s.expires_at, s.revoked_at FROM product.client_sessions s`

type scanner interface{ Scan(...any) error }

func scanProduct(row scanner) (product.Product, error) {
	var item product.Product
	err := row.Scan(&item.ProductID, &item.ProductCode, &item.Name, &item.Status, &item.ProvisioningState, &item.OfficialTenantID, &item.ContextVersion, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanCredential(row scanner) (product.ClientCredential, error) {
	var item product.ClientCredential
	err := row.Scan(&item.CredentialID, &item.ClientID, &item.ProductID, &item.ProofType, &item.ProofDigest, &item.PublicKey,
		&item.Generation, &item.Status, &item.NotBefore, &item.ExpiresAt, &item.RevokedAt, &item.LastUsedAt, &item.CreatedAt)
	return item, err
}

func scanSession(row scanner) (product.StoredClientSession, error) {
	var item product.StoredClientSession
	err := row.Scan(&item.SessionID, &item.TokenDigest, &item.ProductID, &item.Environment, &item.ApplicationID, &item.TenantID,
		&item.ClientID, &item.CredentialID, &item.ClientVersion, &item.ProductContextVersion, &item.ApplicationContextVersion,
		&item.TenantContextVersion, &item.CreatedAt, &item.ExpiresAt, &item.RevokedAt)
	return item, err
}

func insertCredential(ctx context.Context, tx pgx.Tx, credential product.ClientCredential) error {
	_, err := tx.Exec(ctx, `INSERT INTO product.product_client_credentials
		(credential_id, client_id, product_id, proof_type, proof_digest, public_key, generation, status,
		not_before, expires_at, revoked_at, last_used_at, created_at)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7,$8,$9,$10,$11,$12,$13)`, credential.CredentialID, credential.ClientID,
		credential.ProductID, credential.ProofType, credential.ProofDigest, credential.PublicKey, credential.Generation, credential.Status,
		credential.NotBefore, credential.ExpiresAt, credential.RevokedAt, credential.LastUsedAt, credential.CreatedAt)
	return mapWriteError(err)
}

func claimIdempotency(ctx context.Context, tx pgx.Tx, idem product.Idempotency, now time.Time) ([]byte, bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO product.idempotency_records
		(operation, actor_id, scope_id, key_digest, request_digest, state, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,'pending',$6,$6) ON CONFLICT DO NOTHING`, idem.Operation, idem.ActorID, idem.ScopeID, idem.KeyDigest, idem.RequestDigest, now)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 1 {
		return nil, false, nil
	}
	var requestDigest, state string
	var response []byte
	err = tx.QueryRow(ctx, `SELECT request_digest, state, response_json FROM product.idempotency_records
		WHERE operation=$1 AND actor_id=$2 AND scope_id=$3 AND key_digest=$4 FOR UPDATE`, idem.Operation, idem.ActorID, idem.ScopeID, idem.KeyDigest).
		Scan(&requestDigest, &state, &response)
	if err != nil {
		return nil, false, err
	}
	if !product.DigestsEqual(requestDigest, idem.RequestDigest) {
		return nil, false, product.ErrIdempotencyConflict
	}
	if state != "completed" || len(response) == 0 {
		return nil, false, product.ErrConflict
	}
	return response, true, nil
}

func completeIdempotency(ctx context.Context, tx pgx.Tx, idem product.Idempotency, response any, now time.Time) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE product.idempotency_records SET state='completed', response_json=$5, updated_at=$6
		WHERE operation=$1 AND actor_id=$2 AND scope_id=$3 AND key_digest=$4`, idem.Operation, idem.ActorID, idem.ScopeID, idem.KeyDigest, encoded, now)
	return err
}

func insertOutbox(ctx context.Context, tx pgx.Tx, event product.OutboxEvent) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product.outbox_events
		(event_id, aggregate_id, event_type, payload, occurred_at, next_attempt_at)
		VALUES ($1,$2,$3,$4,$5,$5)`, event.EventID, event.AggregateID, event.EventType, payload, event.OccurredAt)
	return err
}

func rollback(tx pgx.Tx, ctx context.Context) { _ = tx.Rollback(ctx) }

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return product.ErrNotFound
	}
	return err
}

func mapUnavailable(err, unavailable error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return unavailable
	}
	return err
}

func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: %v", product.ErrConflict, err)
	}
	return err
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

func compositeContextVersion(productVersion, environmentVersion int64) int64 {
	return productVersion + environmentVersion - 1
}
