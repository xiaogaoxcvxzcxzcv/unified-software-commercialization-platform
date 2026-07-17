package productapplication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

var (
	stableCodePattern     = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	deepLinkSchemePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
)

type IDGenerator interface {
	ID(string) (string, error)
}

type Service struct {
	repository Repository
	ids        IDGenerator
	now        func() time.Time
}

func NewService(repository Repository, ids IDGenerator, now func() time.Time) *Service {
	if ids == nil {
		generator := securevalue.DefaultGenerator()
		ids = generator
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, ids: ids, now: now}
}

type CreateCommand struct {
	Product             ProductContext
	ApplicationCode     string
	Name                string
	Platform            Platform
	DistributionChannel string
	ReleaseTrack        ReleaseTrack
	Status              Status
	ActorID             string
	TraceID             string
	IdempotencyKey      string
}

func (s *Service) CreateApplication(ctx context.Context, command CreateCommand) (Application, error) {
	if s == nil || s.repository == nil {
		return Application{}, fmt.Errorf("product application service is not configured")
	}
	command.ApplicationCode = strings.TrimSpace(command.ApplicationCode)
	command.Name = strings.TrimSpace(command.Name)
	command.DistributionChannel = strings.TrimSpace(command.DistributionChannel)
	if err := validateProductContext(command.Product); err != nil || !stableCodePattern.MatchString(command.ApplicationCode) || !stableCodePattern.MatchString(command.DistributionChannel) || len(command.Name) < 1 || len(command.Name) > 120 || !validPlatform(command.Platform) || !validReleaseTrack(command.ReleaseTrack) || !validStatus(command.Status) || !validMutationMetadata(command.ActorID, command.TraceID, command.IdempotencyKey) {
		return Application{}, ErrInvalidArgument
	}
	now := s.now().UTC()
	applicationID, err := s.ids.ID("app_")
	if err != nil {
		return Application{}, err
	}
	auditID, eventID, err := s.eventIDs()
	if err != nil {
		return Application{}, err
	}
	requestDigest, err := digestJSON(struct {
		ProductID, ApplicationCode, Name, Platform, Channel, ReleaseTrack, Status string
	}{command.Product.ProductID, command.ApplicationCode, command.Name, string(command.Platform), command.DistributionChannel, string(command.ReleaseTrack), string(command.Status)})
	if err != nil {
		return Application{}, err
	}
	application := Application{ApplicationID: applicationID, ProductID: command.Product.ProductID, ApplicationCode: command.ApplicationCode, Name: command.Name, Platform: command.Platform, DistributionChannel: command.DistributionChannel, ReleaseTrack: command.ReleaseTrack, Status: command.Status, ContextVersion: 1, CreatedAt: now, UpdatedAt: now, AuditID: auditID}
	return s.repository.CreateApplication(ctx, CreateRecord{Application: application, ActorID: command.ActorID, TraceID: command.TraceID, EventID: eventID, Idempotency: newIdempotency("create_application", command.ActorID, command.Product.ProductID, command.IdempotencyKey, requestDigest, now)})
}

func (s *Service) ListApplications(ctx context.Context, product ProductContext) ([]Application, error) {
	if s == nil || s.repository == nil || validateProductContext(product) != nil {
		return nil, ErrInvalidArgument
	}
	return s.repository.ListApplications(ctx, product.ProductID)
}

func (s *Service) GetApplication(ctx context.Context, product ProductContext, applicationID string) (Application, error) {
	if s == nil || s.repository == nil || validateProductContext(product) != nil || strings.TrimSpace(applicationID) == "" {
		return Application{}, ErrInvalidArgument
	}
	return s.repository.GetApplication(ctx, product.ProductID, applicationID)
}

type BindClientCommand struct {
	Product        ProductContext
	ApplicationID  string
	Client         ClientIdentity
	ActorID        string
	TraceID        string
	IdempotencyKey string
}

func (s *Service) BindClientToApplication(ctx context.Context, command BindClientCommand) (ClientBinding, error) {
	if s == nil || s.repository == nil || validateProductContext(command.Product) != nil || strings.TrimSpace(command.ApplicationID) == "" || strings.TrimSpace(command.Client.ClientID) == "" || !stableCodePattern.MatchString(command.Client.CredentialType) || command.Client.ProductID != command.Product.ProductID || command.Client.Environment != command.Product.Environment || !validMutationMetadata(command.ActorID, command.TraceID, command.IdempotencyKey) {
		return ClientBinding{}, ErrInvalidArgument
	}
	now := s.now().UTC()
	bindingID, err := s.ids.ID("appbind_")
	if err != nil {
		return ClientBinding{}, err
	}
	auditID, eventID, err := s.eventIDs()
	if err != nil {
		return ClientBinding{}, err
	}
	requestDigest, err := digestJSON(struct{ ProductID, ApplicationID, ClientID, Environment string }{command.Product.ProductID, command.ApplicationID, command.Client.ClientID, string(command.Client.Environment)})
	if err != nil {
		return ClientBinding{}, err
	}
	binding := ClientBinding{BindingID: bindingID, ProductID: command.Product.ProductID, ApplicationID: command.ApplicationID, ClientID: command.Client.ClientID, Environment: command.Client.Environment, Status: StatusActive, CreatedAt: now, UpdatedAt: now, AuditID: auditID}
	return s.repository.BindClient(ctx, BindRecord{Binding: binding, ActorID: command.ActorID, TraceID: command.TraceID, EventID: eventID, Idempotency: newIdempotency("bind_client", command.ActorID, command.Product.ProductID+":"+command.ApplicationID, command.IdempotencyKey, requestDigest, now)})
}

type ReplaceRedirectsCommand struct {
	Product       ProductContext
	ApplicationID string
	Policy        RedirectPolicy
	ActorID       string
	TraceID       string
}

func (s *Service) ReplaceRedirects(ctx context.Context, command ReplaceRedirectsCommand) (RedirectPolicyVersion, error) {
	if s == nil || s.repository == nil || validateProductContext(command.Product) != nil || strings.TrimSpace(command.ApplicationID) == "" || strings.TrimSpace(command.ActorID) == "" || strings.TrimSpace(command.TraceID) == "" {
		return RedirectPolicyVersion{}, ErrInvalidArgument
	}
	application, err := s.repository.GetApplication(ctx, command.Product.ProductID, command.ApplicationID)
	if err != nil {
		return RedirectPolicyVersion{}, err
	}
	policy, err := normalizeRedirectPolicy(command.Product.Environment, application.Platform, command.Policy)
	if err != nil {
		return RedirectPolicyVersion{}, err
	}
	contentSHA, err := digestJSON(policy)
	if err != nil {
		return RedirectPolicyVersion{}, err
	}
	policyID, err := s.ids.ID("redir_")
	if err != nil {
		return RedirectPolicyVersion{}, err
	}
	auditID, eventID, err := s.eventIDs()
	if err != nil {
		return RedirectPolicyVersion{}, err
	}
	now := s.now().UTC()
	return s.repository.ReplaceRedirects(ctx, RedirectRecord{Policy: policy, Version: RedirectPolicyVersion{PolicyID: policyID, ProductID: command.Product.ProductID, ApplicationID: command.ApplicationID, ContentSHA256: "sha256:" + contentSHA, CreatedBy: command.ActorID, CreatedAt: now, AuditID: auditID}, ActorID: command.ActorID, TraceID: command.TraceID, EventID: eventID})
}

func (s *Service) ResolveAuthReturnTarget(ctx context.Context, product ProductContext, applicationID, code string) (ResolvedAuthReturnTarget, error) {
	applicationID = strings.TrimSpace(applicationID)
	if s == nil || s.repository == nil || validateProductContext(product) != nil || applicationID == "" || !stableCodePattern.MatchString(code) {
		return ResolvedAuthReturnTarget{}, ErrInvalidArgument
	}
	stored, err := s.repository.ResolveAuthReturnTarget(ctx, AuthReturnTargetQuery{ProductID: product.ProductID, ApplicationID: applicationID, Code: code})
	if err != nil {
		return ResolvedAuthReturnTarget{}, err
	}
	if stored.ProductID != product.ProductID || stored.ApplicationID != applicationID || stored.Code != code || stored.Status != StatusActive || stored.PolicyVersion < 1 {
		return ResolvedAuthReturnTarget{}, ErrContextRejected
	}
	uri, kind, err := normalizeAuthReturnTargetURI(product.Environment, stored.Platform, stored.URI, stored.WebRedirectURIs, stored.DeepLinks)
	if err != nil {
		return ResolvedAuthReturnTarget{}, ErrContextRejected
	}
	return ResolvedAuthReturnTarget{ProductID: stored.ProductID, ApplicationID: stored.ApplicationID, Code: stored.Code, URI: uri, Kind: kind, PolicyVersion: stored.PolicyVersion}, nil
}

type SuspendCommand struct {
	Product        ProductContext
	ApplicationID  string
	Reason         string
	SessionPolicy  SessionPolicy
	ActorID        string
	TraceID        string
	IdempotencyKey string
}

func (s *Service) SuspendApplication(ctx context.Context, command SuspendCommand) (SuspendResult, error) {
	command.Reason = strings.TrimSpace(command.Reason)
	if s == nil || s.repository == nil || validateProductContext(command.Product) != nil || strings.TrimSpace(command.ApplicationID) == "" || len(command.Reason) < 1 || len(command.Reason) > 500 || (command.SessionPolicy != SessionPolicyKeepExisting && command.SessionPolicy != SessionPolicyRevokeExisting) || !validMutationMetadata(command.ActorID, command.TraceID, command.IdempotencyKey) {
		return SuspendResult{}, ErrInvalidArgument
	}
	now := s.now().UTC()
	auditID, eventID, err := s.eventIDs()
	if err != nil {
		return SuspendResult{}, err
	}
	requestDigest, err := digestJSON(struct{ ProductID, ApplicationID, Reason, Policy string }{command.Product.ProductID, command.ApplicationID, command.Reason, string(command.SessionPolicy)})
	if err != nil {
		return SuspendResult{}, err
	}
	return s.repository.SuspendApplication(ctx, SuspendRecord{ProductID: command.Product.ProductID, ApplicationID: command.ApplicationID, Reason: command.Reason, Policy: command.SessionPolicy, ActorID: command.ActorID, TraceID: command.TraceID, Now: now, AuditID: auditID, EventID: eventID, Idempotency: newIdempotency("suspend_application", command.ActorID, command.Product.ProductID+":"+command.ApplicationID, command.IdempotencyKey, requestDigest, now)})
}

type ResolveCommand struct {
	Product                     ProductContext
	Client                      ClientIdentity
	ClientVersion               string
	ObservedDistributionChannel string
}

func (s *Service) ResolveApplicationContext(ctx context.Context, command ResolveCommand) (ApplicationContext, error) {
	command.ClientVersion = strings.TrimSpace(command.ClientVersion)
	command.ObservedDistributionChannel = strings.TrimSpace(command.ObservedDistributionChannel)
	if s == nil || s.repository == nil || validateProductContext(command.Product) != nil || command.Client.ProductID != command.Product.ProductID || command.Client.Environment != command.Product.Environment || strings.TrimSpace(command.Client.ClientID) == "" || !stableCodePattern.MatchString(command.Client.CredentialType) || len(command.ClientVersion) < 1 || len(command.ClientVersion) > 64 || (command.ObservedDistributionChannel != "" && !stableCodePattern.MatchString(command.ObservedDistributionChannel)) {
		return ApplicationContext{}, ErrContextRejected
	}
	application, binding, err := s.repository.ResolveApplication(ctx, ResolveQuery{ProductID: command.Product.ProductID, ClientID: command.Client.ClientID, Environment: command.Product.Environment})
	if err != nil {
		return ApplicationContext{}, err
	}
	if application.Status != StatusActive || binding.Status != StatusActive {
		return ApplicationContext{}, ErrApplicationSuspended
	}
	if command.ObservedDistributionChannel != "" && application.DistributionChannel != command.ObservedDistributionChannel {
		return ApplicationContext{}, ErrContextRejected
	}
	return ApplicationContext{ProductID: application.ProductID, Environment: binding.Environment, ApplicationID: application.ApplicationID, ApplicationCode: application.ApplicationCode, Platform: application.Platform, DistributionChannel: application.DistributionChannel, ClientID: binding.ClientID, ClientVersion: command.ClientVersion, ReleaseTrack: application.ReleaseTrack, ContextVersion: application.ContextVersion}, nil
}

func (s *Service) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]ClaimedOutboxEvent, error) {
	if s == nil || s.repository == nil || limit < 1 || limit > 200 {
		return nil, ErrInvalidArgument
	}
	return s.repository.ClaimOutbox(ctx, now.UTC(), limit)
}

func (s *Service) MarkOutboxPublished(ctx context.Context, eventID, leaseToken string, now time.Time) error {
	if s == nil || s.repository == nil || strings.TrimSpace(eventID) == "" || strings.TrimSpace(leaseToken) == "" || now.IsZero() {
		return ErrInvalidArgument
	}
	return s.repository.MarkOutboxPublished(ctx, eventID, leaseToken, now.UTC())
}

func (s *Service) MarkOutboxFailed(ctx context.Context, eventID, leaseToken, summary string, next time.Time, dead bool) error {
	if s == nil || s.repository == nil || strings.TrimSpace(eventID) == "" || strings.TrimSpace(leaseToken) == "" || strings.TrimSpace(summary) == "" || next.IsZero() {
		return ErrInvalidArgument
	}
	if len(summary) > 500 {
		summary = summary[:500]
	}
	return s.repository.MarkOutboxFailed(ctx, eventID, leaseToken, summary, next.UTC(), dead)
}

func (s *Service) eventIDs() (string, string, error) {
	auditID, err := s.ids.ID("audit_")
	if err != nil {
		return "", "", err
	}
	eventID, err := s.ids.ID("evt_")
	return auditID, eventID, err
}

func newIdempotency(operation, actorID, scopeID, key, requestDigest string, now time.Time) Idempotency {
	return Idempotency{Operation: operation, ActorID: actorID, ScopeID: scopeID, KeyDigest: digestString(key), RequestDigest: requestDigest, Now: now}
}

func validateProductContext(context ProductContext) error {
	if strings.TrimSpace(context.ProductID) == "" || !validEnvironment(context.Environment) {
		return ErrInvalidArgument
	}
	return nil
}

func validMutationMetadata(actorID, traceID, key string) bool {
	return strings.TrimSpace(actorID) != "" && strings.TrimSpace(traceID) != "" && len(key) >= 16 && len(key) <= 128
}

func validEnvironment(value Environment) bool {
	return value == EnvironmentLocal || value == EnvironmentTest || value == EnvironmentProduction
}

func validPlatform(value Platform) bool {
	switch value {
	case PlatformWindows, PlatformMacOS, PlatformLinux, PlatformWeb, PlatformH5, PlatformAndroid, PlatformIOS, PlatformWechatMiniProgram, PlatformOther:
		return true
	default:
		return false
	}
}

func validReleaseTrack(value ReleaseTrack) bool {
	return value == ReleaseTrackStable || value == ReleaseTrackBeta || value == ReleaseTrackInternal || value == ReleaseTrackCustom
}

func validStatus(value Status) bool { return value == StatusActive || value == StatusSuspended }

func digestJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func digestString(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func normalizeRedirectPolicy(environment Environment, platform Platform, source RedirectPolicy) (RedirectPolicy, error) {
	if !validPlatform(platform) || len(source.WebRedirectURIs) > 100 || len(source.AllowedOrigins) > 100 || len(source.DeepLinks) > 100 || len(source.AuthReturnTargets) > 100 {
		return RedirectPolicy{}, ErrInvalidArgument
	}
	result := RedirectPolicy{}
	var err error
	for _, raw := range source.WebRedirectURIs {
		value, normalizeErr := normalizeWebRedirect(environment, raw)
		if normalizeErr != nil {
			return RedirectPolicy{}, normalizeErr
		}
		result.WebRedirectURIs = append(result.WebRedirectURIs, value)
	}
	for _, raw := range source.AllowedOrigins {
		value, normalizeErr := normalizeOrigin(environment, raw)
		if normalizeErr != nil {
			return RedirectPolicy{}, normalizeErr
		}
		result.AllowedOrigins = append(result.AllowedOrigins, value)
	}
	for _, raw := range source.DeepLinks {
		rule, normalizeErr := normalizeDeepLink(raw)
		if normalizeErr != nil {
			return RedirectPolicy{}, normalizeErr
		}
		result.DeepLinks = append(result.DeepLinks, rule)
	}
	sort.Strings(result.WebRedirectURIs)
	if hasDuplicateStrings(result.WebRedirectURIs) {
		return RedirectPolicy{}, ErrInvalidArgument
	}
	sort.Strings(result.AllowedOrigins)
	if hasDuplicateStrings(result.AllowedOrigins) {
		return RedirectPolicy{}, ErrInvalidArgument
	}
	sort.Slice(result.DeepLinks, func(i, j int) bool {
		if result.DeepLinks[i].Scheme == result.DeepLinks[j].Scheme {
			return result.DeepLinks[i].PathPattern < result.DeepLinks[j].PathPattern
		}
		return result.DeepLinks[i].Scheme < result.DeepLinks[j].Scheme
	})
	if hasDuplicateDeepLinks(result.DeepLinks) {
		return RedirectPolicy{}, ErrInvalidArgument
	}
	seenTargetCodes := make(map[string]struct{}, len(source.AuthReturnTargets))
	for _, sourceTarget := range source.AuthReturnTargets {
		code := sourceTarget.Code
		if !stableCodePattern.MatchString(code) {
			return RedirectPolicy{}, ErrInvalidArgument
		}
		if _, exists := seenTargetCodes[code]; exists {
			return RedirectPolicy{}, ErrInvalidArgument
		}
		uri, _, normalizeErr := normalizeAuthReturnTargetURI(environment, platform, sourceTarget.URI, result.WebRedirectURIs, result.DeepLinks)
		if normalizeErr != nil {
			return RedirectPolicy{}, normalizeErr
		}
		seenTargetCodes[code] = struct{}{}
		result.AuthReturnTargets = append(result.AuthReturnTargets, AuthReturnTarget{Code: code, URI: uri})
	}
	sort.Slice(result.AuthReturnTargets, func(i, j int) bool { return result.AuthReturnTargets[i].Code < result.AuthReturnTargets[j].Code })
	return result, err
}

func normalizeAuthReturnTargetURI(environment Environment, platform Platform, raw string, webRedirects []string, deepLinks []DeepLinkRule) (string, AuthReturnTargetKind, error) {
	if value, err := normalizeWebRedirect(environment, raw); err == nil {
		if containsString(webRedirects, value) {
			return value, AuthReturnTargetWebRedirect, nil
		}
	}
	if !platformAllowsDeepLink(platform) {
		return "", "", ErrInvalidArgument
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || len(raw) < 1 || len(raw) > 2048 || parsed.Scheme == "" || parsed.Opaque != "" || parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(raw, "*\\\x00") {
		return "", "", ErrInvalidArgument
	}
	scheme := strings.ToLower(parsed.Scheme)
	if _, err := normalizeDeepLink(DeepLinkRule{Scheme: scheme, PathPattern: parsed.Path}); err != nil {
		return "", "", err
	}
	matched := false
	for _, rule := range deepLinks {
		if rule.Scheme == scheme && rule.PathPattern == parsed.Path {
			matched = true
			break
		}
	}
	if !matched {
		return "", "", ErrInvalidArgument
	}
	return scheme + ":" + parsed.EscapedPath(), AuthReturnTargetDeepLink, nil
}

func platformAllowsDeepLink(platform Platform) bool {
	switch platform {
	case PlatformWindows, PlatformMacOS, PlatformLinux, PlatformAndroid, PlatformIOS:
		return true
	default:
		return false
	}
}

func containsString(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func normalizeWebRedirect(environment Environment, raw string) (string, error) {
	if len(raw) < 1 || len(raw) > 2048 {
		return "", ErrInvalidArgument
	}
	if strings.Contains(raw, "*") || strings.ContainsAny(raw, "\x00\\") {
		return "", ErrInvalidArgument
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", ErrInvalidArgument
	}
	if err := validateTransportURL(environment, parsed); err != nil {
		return "", err
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String(), nil
}

func normalizeOrigin(environment Environment, raw string) (string, error) {
	if len(raw) < 1 || len(raw) > 512 {
		return "", ErrInvalidArgument
	}
	if strings.Contains(raw, "*") || strings.ContainsAny(raw, "\x00\\") {
		return "", ErrInvalidArgument
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", ErrInvalidArgument
	}
	if err := validateTransportURL(environment, parsed); err != nil {
		return "", err
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = ""
	return parsed.String(), nil
}

func validateTransportURL(environment Environment, parsed *url.URL) error {
	if environment == EnvironmentProduction {
		if parsed.Scheme != "https" || parsed.Port() != "" {
			return ErrInvalidArgument
		}
		return nil
	}
	if parsed.Scheme == "http" && !loopbackHost(parsed.Hostname()) {
		return ErrInvalidArgument
	}
	return nil
}

func normalizeDeepLink(source DeepLinkRule) (DeepLinkRule, error) {
	scheme := source.Scheme
	pathPattern := strings.TrimSpace(source.PathPattern)
	if !deepLinkSchemePattern.MatchString(scheme) || len(pathPattern) < 1 || len(pathPattern) > 512 || strings.ContainsAny(pathPattern, "*\\\x00") || !strings.HasPrefix(pathPattern, "/") || strings.Contains(pathPattern, "..") {
		return DeepLinkRule{}, ErrInvalidArgument
	}
	switch scheme {
	case "http", "https", "javascript", "data", "file", "ftp", "ws", "wss":
		return DeepLinkRule{}, ErrInvalidArgument
	}
	return DeepLinkRule{Scheme: scheme, PathPattern: pathPattern}, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hasDuplicateStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] == values[i] {
			return true
		}
	}
	return false
}

func hasDuplicateDeepLinks(values []DeepLinkRule) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] == values[i] {
			return true
		}
	}
	return false
}
