package g2a06acceptance

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
	hostedpostgres "platform.local/capability-platform/backend/internal/modules/hostedinteraction/postgres"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/modules/product"
	productpostgres "platform.local/capability-platform/backend/internal/modules/product/postgres"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	"sort"
	"time"
)

const (
	fixtureProductCode   = "g2a05-account-acceptance"
	fixtureProductName   = "[ACCEPTANCE FIXTURE] G2A-05 Account"
	fixtureWebCode       = "g2a05-account-acceptance.web"
	fixtureWebName       = "[ACCEPTANCE FIXTURE] G2A-05 Web"
	fixtureDesktopCode   = "g2a06-desktop"
	fixtureDesktopName   = "[ACCEPTANCE FIXTURE] G2A-06 Desktop"
	orphanFixtureActor   = "acceptance.g2a06.fixture"
	fixtureWebActor      = "acceptance.g2a05.fixture"
	fixtureWebTrace      = "trace.g2a05.acceptance.fixture"
	fixtureDesktopTrace  = "trace.g2a06.desktop-application"
	fixtureClientTrace   = "trace.g2a06.client"
	fixtureClientVersion = "g2a06-acceptance"
	fixtureDesktopClient = "g2a06-acceptance-desktop"
)

// OrphanCounts is deliberately aggregate-only so acceptance recovery output
// cannot disclose fixture identifiers or credentials.
type OrphanCounts struct {
	NonterminalInteractions int `json:"nonterminal_interactions"`
	CompletedInteractions   int `json:"completed_interactions"`
	ActiveUserSessions      int `json:"active_user_sessions"`
	ActiveClientSessions    int `json:"active_client_sessions"`
}

type OrphanCleanupResult struct {
	Before    OrphanCounts `json:"before"`
	Resolved  OrphanCounts `json:"resolved"`
	Remaining OrphanCounts `json:"remaining"`
}

type orphanInventory struct {
	counts         OrphanCounts
	interactions   []string
	userSessions   []orphanUserSession
	clientSessions []string
}

type orphanUserSession struct {
	productID, userID, sessionID string
}

type fixtureScope struct {
	productID, tenantID, webApplicationID, desktopApplicationID string
}

// AuditOrphans performs only read-only discovery. Exact trace, application,
// client-version, and creation-event markers must all agree before it returns.
func AuditOrphans(ctx context.Context, pool *pgxpool.Pool, o Options) (OrphanCounts, error) {
	if !o.AcceptanceFixture || validatePool(pool) != nil {
		return OrphanCounts{}, errRejected
	}
	value, err := discoverOrphans(ctx, pool)
	if err != nil {
		return OrphanCounts{}, err
	}
	return value.counts, nil
}

// CleanupOrphans discovers and validates the complete candidate set before any
// mutation, then delegates every state transition to the owning module service.
func CleanupOrphans(ctx context.Context, pool *pgxpool.Pool, o Options) (OrphanCleanupResult, error) {
	if !o.AcceptanceFixture || validatePool(pool) != nil || len(o.UserAuth.TokenPepper) < 32 {
		return OrphanCleanupResult{}, errRejected
	}
	inventory, err := discoverOrphans(ctx, pool)
	if err != nil {
		return OrphanCleanupResult{}, err
	}
	hosted, _, _, err := runtime(pool, o)
	if err != nil {
		return OrphanCleanupResult{}, err
	}
	hostedRepository := hostedpostgres.New(pool)
	for _, interactionID := range inventory.interactions {
		browser, _, openErr := hosted.OpenBrowserSession(ctx, interactionID)
		if openErr != nil {
			latest, getErr := hostedRepository.Get(ctx, interactionID)
			if getErr == nil && cleanupTerminal(latest.Status) {
				continue
			}
			return OrphanCleanupResult{}, errors.New("recover acceptance interaction")
		}
		if _, cancelErr := hosted.Cancel(ctx, interactionID, browser.Token, "g2a06-orphan-cleanup-"+interactionID); cancelErr != nil {
			latest, getErr := hostedRepository.Get(ctx, interactionID)
			if getErr == nil && cleanupTerminal(latest.Status) {
				continue
			}
			return OrphanCleanupResult{}, errors.New("recover acceptance interaction")
		}
	}
	if err = revokeOrphanUserSessions(ctx, pool, o, inventory.userSessions); err != nil {
		return OrphanCleanupResult{}, err
	}
	clients := product.NewService(productpostgres.New(pool), nil, nil, nil, nil, nil)
	for _, sessionID := range inventory.clientSessions {
		if err = clients.RevokeClientSession(ctx, sessionID); err != nil && !errors.Is(err, product.ErrNotFound) {
			return OrphanCleanupResult{}, errors.New("recover acceptance client session")
		}
	}
	after, err := discoverOrphans(ctx, pool)
	if err != nil {
		return OrphanCleanupResult{}, err
	}
	return OrphanCleanupResult{Before: inventory.counts, Resolved: subtractCounts(inventory.counts, after.counts), Remaining: after.counts}, nil
}

func revokeOrphanUserSessions(ctx context.Context, pool *pgxpool.Pool, o Options, sessions []orphanUserSession) error {
	if len(sessions) == 0 {
		return nil
	}
	hasher, err := securevalue.NewHasher(o.UserAuth.TokenPepper)
	if err != nil {
		return err
	}
	service, err := identity.NewAdminEndUserService(identitypostgres.New(pool), identity.StrictIdentifierNormalizer{}, hasher, acceptanceIDGenerator{}, nil)
	if err != nil {
		return err
	}
	byUser := make(map[string][]orphanUserSession)
	for _, session := range sessions {
		byUser[session.userID] = append(byUser[session.userID], session)
	}
	users := make([]string, 0, len(byUser))
	for userID := range byUser {
		users = append(users, userID)
	}
	sort.Strings(users)
	for _, userID := range users {
		sessionRecords := byUser[userID]
		sort.Slice(sessionRecords, func(i, j int) bool { return sessionRecords[i].sessionID < sessionRecords[j].sessionID })
		for start := 0; start < len(sessionRecords); start += 100 {
			end := start + 100
			if end > len(sessionRecords) {
				end = len(sessionRecords)
			}
			keyMaterial := sessionRecords[start].sessionID + sessionRecords[end-1].sessionID
			sessionIDs := make([]string, 0, end-start)
			for _, record := range sessionRecords[start:end] {
				sessionIDs = append(sessionIDs, record.sessionID)
			}
			_, revokeErr := service.RevokeSessions(ctx, identity.AdminSessionRevocationCommand{
				Scope:  identity.AdminUserScope{Type: identity.AdminUserScopeProduct, ProductID: sessionRecords[start].productID},
				UserID: userID, SessionIDs: sessionIDs, ReasonCode: "acceptance.fixture_cleanup",
				IdempotencyKey: "g2a06-orphan-revoke-" + hasher.DigestHex(keyMaterial), ActorID: orphanFixtureActor, TraceID: "trace.g2a06.cleanup-orphans",
			})
			if revokeErr != nil {
				return errors.New("recover acceptance user session")
			}
		}
	}
	return nil
}

type acceptanceIDGenerator struct{}

func (acceptanceIDGenerator) ID(prefix string) (string, error) { return securevalue.ID(prefix) }

func discoverOrphans(ctx context.Context, pool *pgxpool.Pool) (orphanInventory, error) {
	scope, err := discoverFixtureScope(ctx, pool)
	if err != nil {
		return orphanInventory{}, fmt.Errorf("discover fixture scope: %w", err)
	}
	result := orphanInventory{}
	if err = discoverInteractions(ctx, pool, scope, &result); err != nil {
		return orphanInventory{}, fmt.Errorf("discover interactions: %w", err)
	}
	if err = discoverClientSessions(ctx, pool, scope, &result); err != nil {
		return orphanInventory{}, fmt.Errorf("discover client sessions: %w", err)
	}
	return result, nil
}

func discoverFixtureScope(ctx context.Context, pool *pgxpool.Pool) (fixtureScope, error) {
	var result fixtureScope
	rows, err := pool.Query(ctx, `SELECT product_id,COALESCE(official_tenant_id,'') FROM product.products WHERE product_code=$1 AND name=$2`, fixtureProductCode, fixtureProductName)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if err = rows.Scan(&result.productID, &result.tenantID); err != nil {
			return result, err
		}
	}
	if err = rows.Err(); err != nil || count != 1 || result.productID == "" || result.tenantID == "" {
		return fixtureScope{}, errRejected
	}
	type expectedApplication struct{ code, name, platform, actor, trace string }
	expected := map[string]expectedApplication{
		fixtureWebCode:     {fixtureWebCode, fixtureWebName, "web", fixtureWebActor, fixtureWebTrace},
		fixtureDesktopCode: {fixtureDesktopCode, fixtureDesktopName, "windows", orphanFixtureActor, fixtureDesktopTrace},
	}
	apps, err := pool.Query(ctx, `SELECT a.application_id,a.application_code,a.name,a.platform,a.distribution_channel,a.release_track,a.status,
		EXISTS(SELECT 1 FROM product_application.outbox_events e WHERE e.aggregate_id=a.application_id AND e.event_type='product_application.created.v1' AND e.payload->>'actor_id'=$2 AND e.payload->>'trace_id'=$3),
		EXISTS(SELECT 1 FROM product_application.outbox_events e WHERE e.aggregate_id=a.application_id AND e.event_type='product_application.created.v1' AND e.payload->>'actor_id'=$4 AND e.payload->>'trace_id'=$5)
		FROM product_application.product_applications a WHERE a.product_id=$1 AND a.application_code=ANY($6)`, result.productID, fixtureWebActor, fixtureWebTrace, orphanFixtureActor, fixtureDesktopTrace, []string{fixtureWebCode, fixtureDesktopCode})
	if err != nil {
		return fixtureScope{}, err
	}
	defer apps.Close()
	found := make(map[string]bool)
	for apps.Next() {
		var id, code, name, platform, channel, track, status string
		var webMarker, desktopMarker bool
		if err = apps.Scan(&id, &code, &name, &platform, &channel, &track, &status, &webMarker, &desktopMarker); err != nil {
			return fixtureScope{}, err
		}
		want, ok := expected[code]
		marker := webMarker
		if code == fixtureDesktopCode {
			marker = desktopMarker
		}
		if !ok || found[code] || name != want.name || platform != want.platform || channel != "official" || track != "stable" || status != "active" || !marker {
			return fixtureScope{}, errRejected
		}
		found[code] = true
		if code == fixtureWebCode {
			result.webApplicationID = id
		} else {
			result.desktopApplicationID = id
		}
	}
	if err = apps.Err(); err != nil || len(found) != 2 {
		return fixtureScope{}, errRejected
	}
	return result, nil
}

func discoverInteractions(ctx context.Context, pool *pgxpool.Pool, scope fixtureScope, result *orphanInventory) error {
	rows, err := pool.Query(ctx, `SELECT i.interaction_id,i.trace_id,i.route_id,i.product_id,i.application_id,COALESCE(i.tenant_id,''),i.environment,i.channel,i.initiator_kind,COALESCE(i.initiator_client_session_id,''),COALESCE(i.initiator_user_id,''),COALESCE(i.initiator_user_session_id,''),i.return_target_code,i.return_target_uri,i.status,
		COALESCE(s.product_id,''),COALESCE(s.application_id,''),COALESCE(s.tenant_id,''),COALESCE(s.environment,''),COALESCE(s.user_id,''),s.revoked_at,s.refresh_expires_at,COALESCE(cs.product_id,''),COALESCE(cs.application_id,''),COALESCE(cs.tenant_id,''),COALESCE(cs.environment,''),COALESCE(cs.client_version,''),EXISTS(SELECT 1 FROM product.outbox_events e WHERE e.aggregate_id=cs.client_id AND e.event_type='product.client_registered.v1' AND e.payload->>'actor_id'='acceptance.g2a06.fixture' AND e.payload->>'trace_id'='trace.g2a06.client')
		FROM hosted_interaction.interactions i LEFT JOIN identity.end_user_sessions s ON s.session_id=i.initiator_user_session_id LEFT JOIN product.client_sessions cs ON cs.session_id=i.initiator_client_session_id
		WHERE i.trace_id=ANY($1) ORDER BY i.interaction_id`, []string{"trace.g2a06.auth", "trace.g2a06.auth-negative", "trace.g2a06.account"})
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now().UTC()
	seenUsers := map[string]bool{}
	for rows.Next() {
		var id, trace, route, productID, appID, tenantID, environment, channel, kind, clientSessionID, userID, userSessionID, targetCode, targetURI, status string
		var sessionProduct, sessionApp, sessionTenant, sessionEnvironment, sessionUser string
		var clientProduct, clientApp, clientTenant, clientEnvironment, clientVersion string
		var clientMarker bool
		var revokedAt, refreshExpiresAt *time.Time
		if err = rows.Scan(&id, &trace, &route, &productID, &appID, &tenantID, &environment, &channel, &kind, &clientSessionID, &userID, &userSessionID, &targetCode, &targetURI, &status, &sessionProduct, &sessionApp, &sessionTenant, &sessionEnvironment, &sessionUser, &revokedAt, &refreshExpiresAt, &clientProduct, &clientApp, &clientTenant, &clientEnvironment, &clientVersion, &clientMarker); err != nil {
			return err
		}
		valid := productID == scope.productID && tenantID == scope.tenantID && environment == "test" && targetCode == ReturnTargetCode && targetURI == ReturnTargetURI
		if trace != "trace.g2a06.auth" && trace != "trace.g2a06.auth-negative" && trace != "trace.g2a06.account" {
			valid = false
		}
		isNonterminal := hostedinteraction.Status(status) == hostedinteraction.StatusCreated || hostedinteraction.Status(status) == hostedinteraction.StatusOpened || hostedinteraction.Status(status) == hostedinteraction.StatusAuthenticating
		if isNonterminal && !valid {
			return errRejected
		}
		switch hostedinteraction.Status(status) {
		case hostedinteraction.StatusCreated, hostedinteraction.StatusOpened, hostedinteraction.StatusAuthenticating:
			result.counts.NonterminalInteractions++
			result.interactions = append(result.interactions, id)
		case hostedinteraction.StatusCompleted:
			result.counts.CompletedInteractions++
		case hostedinteraction.StatusExchanged, hostedinteraction.StatusCancelled, hostedinteraction.StatusFailed, hostedinteraction.StatusExpired:
		default:
			return errRejected
		}
		if trace == "trace.g2a06.account" && revokedAt == nil && refreshExpiresAt != nil && refreshExpiresAt.After(now) && !seenUsers[userSessionID] {
			seenUsers[userSessionID] = true
			result.userSessions = append(result.userSessions, orphanUserSession{productID: sessionProduct, userID: userID, sessionID: userSessionID})
			result.counts.ActiveUserSessions++
		}
	}
	return rows.Err()
}

func discoverClientSessions(ctx context.Context, pool *pgxpool.Pool, scope fixtureScope, result *orphanInventory) error {
	rows, err := pool.Query(ctx, `SELECT s.session_id,s.product_id,s.application_id,s.tenant_id,s.environment,s.client_version,s.revoked_at,s.expires_at,
		EXISTS(SELECT 1 FROM product.outbox_events e WHERE e.aggregate_id=s.client_id AND e.event_type='product.client_registered.v1' AND e.payload->>'actor_id'=$2 AND e.payload->>'trace_id'=$3)
		FROM product.client_sessions s WHERE s.client_version=ANY($1) ORDER BY s.session_id`, []string{fixtureClientVersion, fixtureDesktopClient}, orphanFixtureActor, fixtureClientTrace)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now().UTC()
	for rows.Next() {
		var id, productID, appID, tenantID, environment, version string
		var revokedAt *time.Time
		var expiresAt time.Time
		var marker bool
		if err = rows.Scan(&id, &productID, &appID, &tenantID, &environment, &version, &revokedAt, &expiresAt, &marker); err != nil {
			return err
		}
		expectedApp := scope.webApplicationID
		if version == fixtureDesktopClient {
			expectedApp = scope.desktopApplicationID
		}
		if productID != scope.productID || appID != expectedApp || tenantID != scope.tenantID || environment != "test" || !marker {
			return errRejected
		}
		if revokedAt == nil && expiresAt.After(now) {
			result.clientSessions = append(result.clientSessions, id)
			result.counts.ActiveClientSessions++
		}
	}
	return rows.Err()
}

func subtractCounts(before, after OrphanCounts) OrphanCounts {
	return OrphanCounts{
		NonterminalInteractions: nonnegative(before.NonterminalInteractions - after.NonterminalInteractions),
		CompletedInteractions:   nonnegative(before.CompletedInteractions - after.CompletedInteractions),
		ActiveUserSessions:      nonnegative(before.ActiveUserSessions - after.ActiveUserSessions),
		ActiveClientSessions:    nonnegative(before.ActiveClientSessions - after.ActiveClientSessions),
	}
}

func nonnegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
