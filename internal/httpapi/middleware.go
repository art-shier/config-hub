package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
			if recover() != nil {
				logger.Error("request handler panic recovered", "request_id", requestIDFromContext(r.Context()))
				writeError(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
			}
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
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
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
	mu             sync.Mutex
	buckets        map[string]loginBucket
	capacity       float64
	refillInterval time.Duration
	maxEntries     int
	now            func() time.Time
	operations     uint64
}

func newLoginLimiter(options RateLimitOptions, now func() time.Time) (*loginLimiter, error) {
	if options.Capacity < 0 || options.RefillInterval < 0 || options.MaxEntries < 0 {
		return nil, errors.New("invalid login rate limit options")
	}
	if options.Capacity == 0 {
		options.Capacity = 5
	}
	if options.RefillInterval == 0 {
		options.RefillInterval = time.Minute
	}
	if options.MaxEntries == 0 {
		options.MaxEntries = 4096
	}
	if now == nil {
		now = time.Now
	}
	return &loginLimiter{buckets: make(map[string]loginBucket), capacity: float64(options.Capacity), refillInterval: options.RefillInterval, maxEntries: options.MaxEntries, now: now}, nil
}

// Allow consumes one token for every login attempt. Username matching for the
// bucket is case-insensitive, and a digest bounds attacker-controlled key size.
func (l *loginLimiter) Allow(sourceIP, username string) bool {
	digest := sha256.Sum256([]byte(strings.ToLower(username)))
	key := sourceIP + ":" + hex.EncodeToString(digest[:])
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.operations++
	if l.operations%256 == 0 {
		l.cleanup(now)
	}
	bucket, exists := l.buckets[key]
	if !exists {
		if len(l.buckets) >= l.maxEntries {
			l.evictOldest()
		}
		bucket = loginBucket{tokens: l.capacity, updated: now}
	}
	if elapsed := now.Sub(bucket.updated); elapsed > 0 {
		bucket.tokens += float64(elapsed) / float64(l.refillInterval)
		if bucket.tokens > l.capacity {
			bucket.tokens = l.capacity
		}
		bucket.updated = now
	}
	bucket.lastSeen = now
	allowed := bucket.tokens >= 1
	if allowed {
		bucket.tokens--
	}
	l.buckets[key] = bucket
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
}

func (l *loginLimiter) evictOldest() {
	var oldestKey string
	var oldest time.Time
	for key, bucket := range l.buckets {
		if oldestKey == "" || bucket.lastSeen.Before(oldest) {
			oldestKey, oldest = key, bucket.lastSeen
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}
