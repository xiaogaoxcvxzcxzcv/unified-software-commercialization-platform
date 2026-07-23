package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/migrations"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestIdentityVerificationEnvironmentFactsAndRollbackProtection(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	digest := func(marker byte) []byte {
		value := make([]byte, 32)
		value[0] = marker
		return value
	}
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.external_auth_flows(
		flow_id,product_id,application_id,tenant_id,environment,provider,provider_application_ref,mode,
		return_target_code,return_target_uri,return_target_policy_version,state_digest,nonce_digest,status,created_at,expires_at
	) VALUES('flow_environment_guard','product_guard','application_guard','tenant_guard','test','oidc','provider.guard','redirect',
		'login.complete','https://client.example.test/callback',1,$1,$2,'pending',$3,$4)`, digest(1), digest(2), now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	insertProof := func(proofID, environment string, marker byte) error {
		_, err := database.Pool.Exec(ctx, `INSERT INTO identity.external_identity_proofs(
			proof_id,flow_id,product_id,application_id,tenant_id,environment,provider,provider_application_ref,
			subject_digest,subject_masked,proof_digest,created_at,expires_at
		) VALUES($1,'flow_environment_guard','product_guard','application_guard','tenant_guard',$2,'oidc','provider.guard',$3,'masked',$4,$5,$6)`,
			proofID, environment, digest(marker), digest(marker+20), now, now.Add(time.Minute))
		return err
	}
	if err := insertProof("proof_environment_wrong", "production", 3); err == nil || !strings.Contains(err.Error(), "external identity proof scope must match its flow") {
		t.Fatalf("cross-environment proof error=%v", err)
	}
	if err := insertProof("proof_environment_valid", "test", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.registration_verification_challenges(
		challenge_id,continuation_digest,product_id,application_id,tenant_id,environment,identifier_type,
		identifier_digest,proof_digest,delivery_id,delivery_status,max_attempts,created_at,expires_at
	) VALUES('challenge_environment_invalid',$1,'product_guard','application_guard','tenant_guard','staging','email',$2,$3,'delivery_environment_invalid','active',5,$4,$5)`,
		digest(5), digest(6), digest(7), now, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "registration_verification_environment_valid") {
		t.Fatalf("invalid challenge environment error=%v", err)
	}

	err := migrations.ApplyDownAll(ctx, database.Pool, database.MigrationPath)
	if err == nil || !strings.Contains(err.Error(), "migration 000021 rollback refused") {
		t.Fatalf("ApplyDownAll() error=%v, want verification environment rollback refusal", err)
	}
}
