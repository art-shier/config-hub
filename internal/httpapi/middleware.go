package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"confighub.local/internal/auth"
)

const maxRequestBodyBytes int64 = 1 << 20

type requestIDKey struct{}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return requestID
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, err := auth.RandomOpaque("req_", 12)
		if err != nil {
			requestID = "req_unavailable"
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID)))
	})
}

func recoveryMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if recovered == http.ErrAbortHandler {
				panic(http.ErrAbortHandler)
			}
			logger.Error("request handler panic recovered", "request_id", requestIDFromContext(r.Context()))
			if state, ok := w.(interface{ Committed() bool }); ok && state.Committed() {
				panic(http.ErrAbortHandler)
			}
			writeError(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
		}()
		next.ServeHTTP(w, r)
	})
}

func securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.ContentLength > maxRequestBodyBytes {
			writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Request body is too large")
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			original := r.Body
			limited := http.MaxBytesReader(w, original, maxRequestBodyBytes)
			body, err := io.ReadAll(limited)
			_ = original.Close()
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Request body is too large")
				} else {
					writeError(w, r, http.StatusBadRequest, "malformed_request", "Malformed request body")
				}
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, r)
	})
}

const currentConfigRoutePattern = "GET /api/v1/projects/{project}/environments/{environment}/config"

func authorizationSurfaceMiddleware(machineReadEnabled bool, mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Authorization")) == 0 {
			mux.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		if !machineReadEnabled || r.Method != http.MethodGet || pattern != currentConfigRoutePattern {
			writeError(w, r, http.StatusUnauthorized, "invalid_token", "Machine tokens are not accepted on this route")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func strictBearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	value := values[0]
	if len(value) <= len("Bearer ") || !strings.EqualFold(value[:len("Bearer")], "Bearer") || value[len("Bearer")] != ' ' {
		return "", false
	}
	token := value[len("Bearer "):]
	if token == "" || strings.ContainsAny(token, " \t\r\n,") {
		return "", false
	}
	return token, true
}

type responseCapture struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseCapture) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseCapture) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (w *responseCapture) Committed() bool {
	return w.status != 0
}

func (w *responseCapture) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseCapture) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func accessLogMiddleware(logger *slog.Logger, sourceIP func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		capture := &responseCapture{ResponseWriter: w}
		completed := false
		defer func() {
			status := capture.status
			if !completed && status == 0 {
				status = http.StatusInternalServerError
			}
			if status == 0 {
				status = http.StatusOK
			}
			logger.Info("request", "request_id", requestIDFromContext(r.Context()), "method", r.Method,
				"route", r.Pattern, "status", status, "bytes", capture.bytes,
				"duration_ms", time.Since(started).Milliseconds(), "source_ip", sourceIP(r))
		}()
		next.ServeHTTP(capture, r)
		completed = true
	})
}

func newSourceIPResolver(cidrs []string) (func(*http.Request) string, error) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, errors.New("invalid trusted proxy CIDR")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	isTrusted := func(address netip.Addr) bool {
		for _, prefix := range prefixes {
			if prefix.Contains(address) {
				return true
			}
		}
		return false
	}
	return func(r *http.Request) string {
		peer := remoteIP(r.RemoteAddr)
		if !peer.IsValid() {
			return "unknown"
		}
		if !isTrusted(peer) {
			return peer.String()
		}
		raw := r.Header.Get("X-Forwarded-For")
		if raw == "" {
			return peer.String()
		}
		parts := strings.Split(raw, ",")
		forwarded := make([]netip.Addr, 0, len(parts))
		for _, part := range parts {
			address, err := netip.ParseAddr(strings.TrimSpace(part))
			if err != nil {
				return peer.String()
			}
			forwarded = append(forwarded, address.Unmap())
		}
		for index := len(forwarded) - 1; index >= 0; index-- {
			if !isTrusted(forwarded[index]) {
				return forwarded[index].String()
			}
		}
		if len(forwarded) > 0 {
			return forwarded[0].String()
		}
		return peer.String()
	}, nil
}

func remoteIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}
	}
	return address.Unmap()
}

type loginBucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
}

type loginLimiter struct {
	mu               sync.Mutex
	buckets          map[string]loginBucket
	sourceBuckets    map[string]loginBucket
	capacity         float64
	sourceCapacity   float64
	refillInterval   time.Duration
	maxEntries       int
	sourceMaxEntries int
	now              func() time.Time
	nextCleanup      time.Time
}

func newLoginLimiter(options RateLimitOptions, now func() time.Time) (*loginLimiter, error) {
	if options.Capacity < 0 || options.SourceCapacity < 0 || options.RefillInterval < 0 || options.MaxEntries < 0 || options.SourceMaxEntries < 0 {
		return nil, errors.New("invalid login rate limit options")
	}
	if options.Capacity == 0 {
		options.Capacity = 5
	}
	if options.SourceCapacity == 0 {
		options.SourceCapacity = 20
	}
	if options.RefillInterval == 0 {
		options.RefillInterval = time.Minute
	}
	if options.MaxEntries == 0 {
		options.MaxEntries = 4096
	}
	if options.SourceMaxEntries == 0 {
		options.SourceMaxEntries = 2048
	}
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{
		buckets: make(map[string]loginBucket), sourceBuckets: make(map[string]loginBucket),
		capacity: float64(options.Capacity), sourceCapacity: float64(options.SourceCapacity),
		refillInterval: options.RefillInterval, maxEntries: options.MaxEntries,
		sourceMaxEntries: options.SourceMaxEntries, now: now,
	}, nil
}

// Allow consumes one token for every login attempt. Username matching for the
// bucket is case-insensitive, and a digest bounds attacker-controlled key size.
func (l *loginLimiter) Allow(sourceIP, username string) bool {
	digest := sha256.Sum256([]byte(strings.ToLower(username)))
	key := sourceIP + ":" + hex.EncodeToString(digest[:])
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nextCleanup.IsZero() || !now.Before(l.nextCleanup) {
		l.cleanup(now)
	}
	if !l.consume(l.sourceBuckets, sourceIP, l.sourceCapacity, l.sourceMaxEntries, now) {
		return false
	}
	return l.consume(l.buckets, key, l.capacity, l.maxEntries, now)
}

func (l *loginLimiter) consume(buckets map[string]loginBucket, key string, capacity float64, maxEntries int, now time.Time) bool {
	bucket, exists := buckets[key]
	if !exists {
		if len(buckets) >= maxEntries {
			return false
		}
		bucket = loginBucket{tokens: capacity, updated: now}
	}
	if elapsed := now.Sub(bucket.updated); elapsed > 0 {
		bucket.tokens += float64(elapsed) / float64(l.refillInterval)
		if bucket.tokens > capacity {
			bucket.tokens = capacity
		}
		bucket.updated = now
	}
	bucket.lastSeen = now
	allowed := bucket.tokens >= 1
	if allowed {
		bucket.tokens--
	}
	buckets[key] = bucket
	return allowed
}

func (l *loginLimiter) cleanup(now time.Time) {
	idleLimit := 10 * l.refillInterval
	if idleLimit < time.Hour {
		idleLimit = time.Hour
	}
	for key, bucket := range l.buckets {
		if now.Sub(bucket.lastSeen) > idleLimit {
			delete(l.buckets, key)
		}
	}
	for key, bucket := range l.sourceBuckets {
		if now.Sub(bucket.lastSeen) > idleLimit {
			delete(l.sourceBuckets, key)
		}
	}
	l.nextCleanup = now.Add(idleLimit)
}
