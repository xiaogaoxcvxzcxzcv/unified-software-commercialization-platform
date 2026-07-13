package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"
)

const Header = "X-Request-ID"

type contextKey struct{}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)
var fallbackCounter atomic.Uint64

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(Header)
		if !safeID.MatchString(id) {
			id = newID()
		}
		w.Header().Set(Header, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, id)))
	})
}

func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fallback-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(fallbackCounter.Add(1), 36)
	}
	return hex.EncodeToString(b[:])
}
