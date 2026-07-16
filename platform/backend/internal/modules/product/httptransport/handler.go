package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

const (
	productsPath            = "/api/v1/admin/products"
	productReadPermission   = "product.read"
	productManagePermission = "product.manage"
	maxRequestBody          = 1 << 20
	maxProductList          = 200
)

var (
	identifierPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{15,127}$`)
	stableCodePattern  = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	packageIDPattern   = regexp.MustCompile(`^package\.[a-z][a-z0-9-]*$`)
	sha256Pattern      = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

type Service interface {
	ListProducts(context.Context, int) ([]product.Product, error)
	GetProduct(context.Context, string) (product.Product, error)
	CurrentCapabilitySet(context.Context, string) (product.CapabilitySet, error)
	ReplaceCapabilitySet(context.Context, product.ReplaceCapabilitySetCommand) (product.CapabilitySet, error)
}

type ProvisionCommand struct {
	ProductCode    string
	Name           string
	Status         string
	ActorID        string
	TraceID        string
	IdempotencyKey string
}

type ProvisionedProduct struct {
	Product product.Product
}

// Provisioner owns the complete Product + official Tenant workflow. A Handler
// must never return the pending Product produced by BeginProvisioning alone.
type Provisioner interface {
	ProvisionProduct(context.Context, ProvisionCommand) (ProvisionedProduct, error)
}

type Handler struct {
	service     Service
	provisioner Provisioner
	guard       *adminrequest.Guard
}

func New(service Service, provisioner Provisioner, guard *adminrequest.Guard) *Handler {
	return &Handler{service: service, provisioner: provisioner, guard: guard}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.service == nil || h.provisioner == nil || h.guard == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	route, ok := parseRoute(r)
	if !ok {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "product.invalid_query", "query parameters are not supported")
		return
	}
	switch route.kind {
	case routeProducts:
		switch r.Method {
		case http.MethodGet:
			h.list(w, r)
		case http.MethodPost:
			h.create(w, r)
		default:
			httpx.MethodNotAllowed(w, r, "GET, POST")
		}
	case routeProduct:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.get(w, r, route.productID)
	case routeCapabilities:
		switch r.Method {
		case http.MethodGet:
			h.getCapabilities(w, r, route.productID)
		case http.MethodPut:
			h.replaceCapabilities(w, r, route.productID)
		default:
			httpx.MethodNotAllowed(w, r, "GET, PUT")
		}
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

type capabilitySetProjectionResponse struct {
	ProductID     string                 `json:"product_id"`
	CapabilitySet *capabilitySetResponse `json:"capability_set"`
}

type capabilitySetResponse struct {
	ProductID             string                   `json:"product_id"`
	Version               int64                    `json:"version"`
	Capabilities          []capabilityItemResponse `json:"capabilities"`
	SourcePlanID          string                   `json:"source_plan_id"`
	CatalogRevision       string                   `json:"catalog_revision"`
	CatalogSnapshotSHA256 string                   `json:"catalog_snapshot_sha256"`
	AuditID               string                   `json:"audit_id"`
}

type capabilityItemResponse struct {
	CapabilityID         string          `json:"capability_id"`
	Enabled              bool            `json:"enabled"`
	Policy               json.RawMessage `json:"policy,omitempty"`
	SourcePackageID      string          `json:"source_package_id"`
	SourcePackageVersion string          `json:"source_package_version"`
}

func (h *Handler) getCapabilities(w http.ResponseWriter, r *http.Request, productID string) {
	if _, ok := h.guard.Authorize(w, r, productReadPermission, productTarget(productID), false); !ok {
		return
	}
	item, err := h.service.GetProduct(r.Context(), productID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if item.ProductID != productID || !validProduct(item, false) {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	set, err := h.service.CurrentCapabilitySet(r.Context(), productID)
	if errors.Is(err, product.ErrNotFound) {
		httpx.JSON(w, http.StatusOK, capabilitySetProjectionResponse{ProductID: productID})
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	projection, ok := projectCapabilitySet(set, productID)
	if !ok {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, http.StatusOK, capabilitySetProjectionResponse{ProductID: productID, CapabilitySet: &projection})
}

func projectCapabilitySet(set product.CapabilitySet, productID string) (capabilitySetResponse, bool) {
	if set.ProductID != productID || set.Version < 1 || !validIdentifier(set.SourcePlanID) || !stableCodePattern.MatchString(set.CatalogRevision) || !sha256Pattern.MatchString(set.CatalogSnapshotSHA256) || !validIdentifier(set.AuditID) {
		return capabilitySetResponse{}, false
	}
	items := make([]capabilityItemResponse, len(set.Items))
	for index, item := range set.Items {
		if !stableCodePattern.MatchString(item.CapabilityID) || !packageIDPattern.MatchString(item.SourcePackageID) || strings.TrimSpace(item.SourcePackageVersion) == "" {
			return capabilitySetResponse{}, false
		}
		if len(item.Policy) != 0 {
			var policy map[string]any
			if err := json.Unmarshal(item.Policy, &policy); err != nil || policy == nil || len(policy) > 64 {
				return capabilitySetResponse{}, false
			}
		}
		items[index] = capabilityItemResponse{
			CapabilityID: item.CapabilityID, Enabled: item.Enabled, Policy: append(json.RawMessage(nil), item.Policy...),
			SourcePackageID: item.SourcePackageID, SourcePackageVersion: item.SourcePackageVersion,
		}
	}
	sort.Slice(items, func(left, right int) bool { return items[left].CapabilityID < items[right].CapabilityID })
	return capabilitySetResponse{
		ProductID: set.ProductID, Version: set.Version, Capabilities: items,
		SourcePlanID: set.SourcePlanID, CatalogRevision: set.CatalogRevision,
		CatalogSnapshotSHA256: set.CatalogSnapshotSHA256, AuditID: set.AuditID,
	}, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.guard.Authorize(w, r, productReadPermission, adminrequest.TargetScope{Type: "platform"}, false); !ok {
		return
	}
	items, err := h.service.ListProducts(r.Context(), maxProductList)
	if err != nil {
		writeError(w, r, err)
		return
	}
	response := productListResponse{Items: make([]productResponse, len(items))}
	for index := range items {
		if !validProduct(items[index], false) {
			httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		response.Items[index] = respondProduct(items[index])
	}
	httpx.JSON(w, http.StatusOK, response)
}

type createProductRequest struct {
	ProductCode string `json:"code"`
	Name        string `json:"name"`
	Status      string `json:"status"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.guard.Authorize(w, r, productManagePermission, adminrequest.TargetScope{Type: "platform"}, false)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body createProductRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	traceID := requestid.FromContext(r.Context())
	if traceID == "" {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	result, err := h.provisioner.ProvisionProduct(r.Context(), ProvisionCommand{
		ProductCode: body.ProductCode, Name: body.Name, Status: body.Status,
		ActorID: principal.AdminUserID, TraceID: traceID, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validProduct(result.Product, true) {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, http.StatusCreated, respondProduct(result.Product))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, productID string) {
	if _, ok := h.guard.Authorize(w, r, productReadPermission, productTarget(productID), false); !ok {
		return
	}
	item, err := h.service.GetProduct(r.Context(), productID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if item.ProductID != productID || !validProduct(item, false) {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, http.StatusOK, respondProduct(item))
}

type replaceCapabilitiesRequest struct {
	ExpectedVersion *int64                   `json:"expected_version"`
	ChangePlan      *capabilityChangePlanRef `json:"change_plan"`
}

type capabilityChangePlanRef struct {
	AssemblyPlanID        string `json:"assembly_plan_id"`
	CatalogRevision       string `json:"catalog_revision"`
	CatalogSnapshotSHA256 string `json:"catalog_snapshot_sha256"`
}

func (h *Handler) replaceCapabilities(w http.ResponseWriter, r *http.Request, productID string) {
	principal, ok := h.guard.Authorize(w, r, productManagePermission, productTarget(productID), true)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body replaceCapabilitiesRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ExpectedVersion == nil || *body.ExpectedVersion < 0 || body.ChangePlan == nil {
		httpx.Error(w, r, http.StatusBadRequest, "product.invalid_request", "product capability request is invalid")
		return
	}
	traceID := requestid.FromContext(r.Context())
	if traceID == "" {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	set, err := h.service.ReplaceCapabilitySet(r.Context(), product.ReplaceCapabilitySetCommand{
		Plan: product.TrustedCapabilityChangePlan{
			ProductID: productID, SourcePlanID: body.ChangePlan.AssemblyPlanID, CatalogRevision: body.ChangePlan.CatalogRevision,
			CatalogSnapshotSHA256: body.ChangePlan.CatalogSnapshotSHA256,
		},
		ExpectedVersion: *body.ExpectedVersion, ActorID: principal.AdminUserID,
		IdempotencyKey: idempotencyKey, TraceID: traceID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if set.ProductID != productID || set.CapabilitySetID == "" || set.Version < 1 || set.SourcePlanID == "" || set.AuditID == "" {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, http.StatusOK, set)
}

type productResponse struct {
	ProductID         string    `json:"product_id"`
	ProductCode       string    `json:"code"`
	Name              string    `json:"name"`
	Status            string    `json:"status"`
	ProvisioningState string    `json:"provisioning_state"`
	OfficialTenantID  string    `json:"official_tenant_id,omitempty"`
	ContextVersion    int64     `json:"context_version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	AuditID           string    `json:"audit_id,omitempty"`
}

type productListResponse struct {
	Items []productResponse `json:"items"`
}

func respondProduct(value product.Product) productResponse {
	return productResponse{
		ProductID: value.ProductID, ProductCode: value.ProductCode, Name: value.Name,
		Status: value.Status, ProvisioningState: value.ProvisioningState, OfficialTenantID: value.OfficialTenantID,
		ContextVersion: value.ContextVersion, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, AuditID: value.AuditID,
	}
}

func validProduct(value product.Product, requireReady bool) bool {
	if !validIdentifier(value.ProductID) || !validIdentifier(value.ProductCode) || strings.TrimSpace(value.Name) == "" || value.ContextVersion < 1 || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() {
		return false
	}
	if value.Status != "active" && value.Status != "suspended" {
		return false
	}
	if value.ProvisioningState != "pending" && value.ProvisioningState != "ready" && value.ProvisioningState != "failed" {
		return false
	}
	return !requireReady || (value.ProvisioningState == "ready" && validIdentifier(value.OfficialTenantID) && validIdentifier(value.AuditID))
}

func productTarget(productID string) adminrequest.TargetScope {
	return adminrequest.TargetScope{Type: "product", ID: productID, ProductID: productID}
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		httpx.Error(w, r, http.StatusBadRequest, "product.invalid_idempotency_key", "exactly one Idempotency-Key is required")
		return "", false
	}
	key := strings.TrimSpace(values[0])
	if key != values[0] || !idempotencyPattern.MatchString(key) {
		httpx.Error(w, r, http.StatusBadRequest, "product.invalid_idempotency_key", "Idempotency-Key must contain 16 to 128 safe characters")
		return "", false
	}
	return key, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		httpx.Error(w, r, http.StatusUnsupportedMediaType, "product.unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeDecodeError(w, r, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeDecodeError(w, r, err)
				return false
			}
		}
		httpx.Error(w, r, http.StatusBadRequest, "product.invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "product.request_too_large", "request body exceeds 1 MiB")
		return
	}
	httpx.Error(w, r, http.StatusBadRequest, "product.invalid_request", "invalid request body")
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, product.ErrInvalidCommand):
		httpx.Error(w, r, http.StatusBadRequest, "product.invalid_request", "product request is invalid")
	case errors.Is(err, product.ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "product.not_found", "product was not found")
	case errors.Is(err, product.ErrIdempotencyConflict):
		httpx.Error(w, r, http.StatusConflict, "product.idempotency_conflict", "Idempotency-Key was reused with a different request")
	case errors.Is(err, product.ErrCapabilityVersionConflict):
		httpx.Error(w, r, http.StatusConflict, "product.capability_version_conflict", "product capability version changed")
	case errors.Is(err, product.ErrUntrustedChangePlan):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "product.capability_plan_rejected", "product capability change plan was rejected")
	case errors.Is(err, product.ErrProvisioningState), errors.Is(err, product.ErrProductUnavailable):
		httpx.Error(w, r, http.StatusConflict, "product.unavailable", "product is not available for this operation")
	case errors.Is(err, product.ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "product.conflict", "product conflicts with an existing resource")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

type routeKind int

const (
	routeProducts routeKind = iota + 1
	routeProduct
	routeCapabilities
)

type parsedRoute struct {
	kind      routeKind
	productID string
}

func parseRoute(r *http.Request) (parsedRoute, bool) {
	if r.URL.RawPath != "" || strings.Contains(r.URL.EscapedPath(), "%") || r.URL.Path != path.Clean(r.URL.Path) {
		return parsedRoute{}, false
	}
	if r.URL.Path == productsPath {
		return parsedRoute{kind: routeProducts}, true
	}
	if !strings.HasPrefix(r.URL.Path, productsPath+"/") {
		return parsedRoute{}, false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, productsPath+"/"), "/")
	if len(parts) == 1 && validIdentifier(parts[0]) {
		return parsedRoute{kind: routeProduct, productID: parts[0]}, true
	}
	if len(parts) == 2 && validIdentifier(parts[0]) && parts[1] == "capabilities" {
		return parsedRoute{kind: routeCapabilities, productID: parts[0]}, true
	}
	return parsedRoute{}, false
}

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }
