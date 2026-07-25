package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Bearer struct {
	digest   [sha256.Size]byte
	failures atomic.Uint64
	mu       sync.Mutex
	tokens   float64
	updated  time.Time
}

func NewBearer(token string) *Bearer {
	return &Bearer{digest: sha256.Sum256([]byte(token)), tokens: 20, updated: time.Now()}
}

func (b *Bearer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		provided := ""
		if strings.HasPrefix(header, "Bearer ") && len(header) > len("Bearer ") {
			provided = header[len("Bearer "):]
		}
		digest := sha256.Sum256([]byte(provided))
		if subtle.ConstantTimeCompare(digest[:], b.digest[:]) != 1 {
			b.failures.Add(1)
			if !b.allowFailure() {
				http.Error(w, "too many authentication failures", http.StatusTooManyRequests)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="openwrt-presence-agent"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (b *Bearer) allowFailure() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens += now.Sub(b.updated).Seconds() * 5
	if b.tokens > 20 {
		b.tokens = 20
	}
	b.updated = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (b *Bearer) Failures() uint64 {
	return b.failures.Load()
}
