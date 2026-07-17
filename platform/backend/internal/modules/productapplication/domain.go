package productapplication

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument      = errors.New("invalid product application argument")
	ErrNotFound             = errors.New("product application not found")
	ErrConflict             = errors.New("product application conflict")
	ErrIdempotencyConflict  = errors.New("product application idempotency conflict")
	ErrOperationInProgress  = errors.New("product application operation in progress")
	ErrContextRejected      = errors.New("product application context rejected")
	ErrApplicationSuspended = errors.New("product application suspended")
)

type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

type Environment string

const (
	EnvironmentLocal      Environment = "local"
	EnvironmentTest       Environment = "test"
	EnvironmentProduction Environment = "production"
)

type Platform string

const (
	PlatformWindows           Platform = "windows"
	PlatformMacOS             Platform = "macos"
	PlatformLinux             Platform = "linux"
	PlatformWeb               Platform = "web"
	PlatformH5                Platform = "h5"
	PlatformAndroid           Platform = "android"
	PlatformIOS               Platform = "ios"
	PlatformWechatMiniProgram Platform = "wechat_miniprogram"
	PlatformOther             Platform = "other"
)

type ReleaseTrack string

const (
	ReleaseTrackStable   ReleaseTrack = "stable"
	ReleaseTrackBeta     ReleaseTrack = "beta"
	ReleaseTrackInternal ReleaseTrack = "internal"
	ReleaseTrackCustom   ReleaseTrack = "custom"
)

type SessionPolicy string

const (
	SessionPolicyKeepExisting   SessionPolicy = "keep_existing"
	SessionPolicyRevokeExisting SessionPolicy = "revoke_existing"
)

// ProductContext is trusted input from Product. HTTP payload identifiers are
// not sufficient to construct it.
type ProductContext struct {
	ProductID   string
	Environment Environment
}

// ClientIdentity is a client identity already verified by Product. This
// module owns only its binding to a Product Application.
type ClientIdentity struct {
	ProductID      string
	ClientID       string
	Environment    Environment
	CredentialType string
}

type Application struct {
	ApplicationID                string       `json:"application_id"`
	ProductID                    string       `json:"product_id"`
	ApplicationCode              string       `json:"application_code"`
	Name                         string       `json:"name"`
	Platform                     Platform     `json:"platform"`
	DistributionChannel          string       `json:"distribution_channel"`
	ReleaseTrack                 ReleaseTrack `json:"release_track"`
	Status                       Status       `json:"status"`
	ContextVersion               int64        `json:"context_version"`
	CurrentRedirectPolicyVersion int64        `json:"current_redirect_policy_version"`
	CreatedAt                    time.Time    `json:"created_at"`
	UpdatedAt                    time.Time    `json:"updated_at"`
	AuditID                      string       `json:"audit_id,omitempty"`
}

type ClientBinding struct {
	BindingID     string      `json:"binding_id"`
	ProductID     string      `json:"product_id"`
	ApplicationID string      `json:"application_id"`
	ClientID      string      `json:"client_id"`
	Environment   Environment `json:"environment"`
	Status        Status      `json:"status"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	AuditID       string      `json:"audit_id,omitempty"`
}

type DeepLinkRule struct {
	Scheme      string `json:"scheme"`
	PathPattern string `json:"path_pattern"`
}

type AuthReturnTarget struct {
	Code string `json:"code"`
	URI  string `json:"uri"`
}

type RedirectPolicy struct {
	WebRedirectURIs   []string           `json:"web_redirect_uris"`
	AllowedOrigins    []string           `json:"allowed_origins"`
	DeepLinks         []DeepLinkRule     `json:"deep_links"`
	AuthReturnTargets []AuthReturnTarget `json:"auth_return_targets"`
}

type RedirectPolicyVersion struct {
	PolicyID      string    `json:"policy_id"`
	ProductID     string    `json:"product_id"`
	ApplicationID string    `json:"application_id"`
	Version       int64     `json:"version"`
	ContentSHA256 string    `json:"content_sha256"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	AuditID       string    `json:"audit_id,omitempty"`
}

type AuthReturnTargetKind string

const (
	AuthReturnTargetWebRedirect AuthReturnTargetKind = "web_redirect"
	AuthReturnTargetDeepLink    AuthReturnTargetKind = "deep_link"
)

type ResolvedAuthReturnTarget struct {
	ProductID     string               `json:"product_id"`
	ApplicationID string               `json:"application_id"`
	Code          string               `json:"code"`
	URI           string               `json:"uri"`
	Kind          AuthReturnTargetKind `json:"kind"`
	PolicyVersion int64                `json:"policy_version"`
}

type ApplicationContext struct {
	ProductID           string       `json:"product_id"`
	Environment         Environment  `json:"environment"`
	ApplicationID       string       `json:"application_id"`
	ApplicationCode     string       `json:"application_code"`
	Platform            Platform     `json:"platform"`
	DistributionChannel string       `json:"distribution_channel"`
	ClientID            string       `json:"client_id"`
	ClientVersion       string       `json:"client_version"`
	ReleaseTrack        ReleaseTrack `json:"release_track"`
	ContextVersion      int64        `json:"context_version"`
}

type SuspendResult struct {
	ApplicationID          string        `json:"application_id"`
	Status                 Status        `json:"status"`
	SessionPolicy          SessionPolicy `json:"session_policy"`
	AffectedClientBindings int64         `json:"affected_client_bindings"`
	AuditID                string        `json:"audit_id,omitempty"`
}
