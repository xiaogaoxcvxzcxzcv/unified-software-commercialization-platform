package server

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
)

var (
	ErrInvalidModulePrefix   = errors.New("invalid module route prefix")
	ErrDuplicateModulePrefix = errors.New("duplicate module route prefix")
	ErrNilModuleHandler      = errors.New("module route handler is nil")
	ErrModuleRegistrarSealed = errors.New("module registrar is sealed")
)

type moduleRoute struct {
	prefix  string
	handler http.Handler
}

// ModuleRegistrar collects module-owned HTTP handlers during process startup.
// It is sealed when the platform handler is built so runtime routing is immutable.
type ModuleRegistrar struct {
	mu       sync.Mutex
	routes   []moduleRoute
	prefixes map[string]struct{}
	sealed   bool
}

func NewModuleRegistrar() *ModuleRegistrar {
	return &ModuleRegistrar{prefixes: make(map[string]struct{})}
}

func (r *ModuleRegistrar) Register(prefix string, handler http.Handler) error {
	if r == nil {
		return fmt.Errorf("%w: registrar is nil", ErrModuleRegistrarSealed)
	}
	if err := validateModulePrefix(prefix); err != nil {
		return err
	}
	if handler == nil {
		return fmt.Errorf("%w: %s", ErrNilModuleHandler, prefix)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return ErrModuleRegistrarSealed
	}
	if r.prefixes == nil {
		r.prefixes = make(map[string]struct{})
	}
	if _, exists := r.prefixes[prefix]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateModulePrefix, prefix)
	}
	r.prefixes[prefix] = struct{}{}
	r.routes = append(r.routes, moduleRoute{prefix: prefix, handler: handler})
	return nil
}

func (r *ModuleRegistrar) install(mux *http.ServeMux) {
	if r == nil {
		return
	}

	r.mu.Lock()
	r.sealed = true
	routes := append([]moduleRoute(nil), r.routes...)
	r.mu.Unlock()

	for _, route := range routes {
		mux.Handle(route.prefix, route.handler)
	}
}

func validateModulePrefix(prefix string) error {
	if prefix == "" || prefix == "/" || !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") {
		return fmt.Errorf("%w: %q must be an absolute non-root path ending in '/'", ErrInvalidModulePrefix, prefix)
	}
	if strings.Contains(prefix, "//") {
		return fmt.Errorf("%w: %q contains reserved path characters", ErrInvalidModulePrefix, prefix)
	}
	for _, character := range prefix {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("/-._~", character) {
			continue
		}
		return fmt.Errorf("%w: %q contains a non-literal path character", ErrInvalidModulePrefix, prefix)
	}
	if path.Clean(strings.TrimSuffix(prefix, "/"))+"/" != prefix {
		return fmt.Errorf("%w: %q is not canonical", ErrInvalidModulePrefix, prefix)
	}
	if prefix == "/health/" || strings.HasPrefix(prefix, "/health/") {
		return fmt.Errorf("%w: %q is reserved by the platform", ErrInvalidModulePrefix, prefix)
	}
	return nil
}
