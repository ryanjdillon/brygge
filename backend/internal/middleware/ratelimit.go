package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/brygge-klubb/brygge/internal/logredact"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

type RateLimiter struct {
	rdb    *redis.Client
	log    zerolog.Logger
	limit  int
	window time.Duration
	keyFn  func(*http.Request) string
	// failClosed refuses requests while the counter cannot be read, instead
	// of letting them through. See FailClosed.
	failClosed bool
}

func NewRateLimiter(rdb *redis.Client, log zerolog.Logger, limit int, window time.Duration, keyFn func(*http.Request) string) *RateLimiter {
	return &RateLimiter{
		rdb:    rdb,
		log:    log,
		limit:  limit,
		window: window,
		keyFn:  keyFn,
	}
}

// FailClosed makes the limiter answer 503 when Redis errors, instead of the
// default of letting the request through. It is for a public route where each
// request costs something real (an email to an address the visitor chose), so
// an outage must not lift the budget; ordinary routes stay fail-open so a Redis
// blip does not take the site down. It returns rl for chaining.
func (rl *RateLimiter) FailClosed() *RateLimiter {
	rl.failClosed = true
	return rl
}

func (rl *RateLimiter) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := rl.keyFn(r)
		ctx := r.Context()

		count, ttl, err := rl.increment(ctx, key)
		if err != nil {
			rl.log.Error().Err(err).Str("key", key).Bool("fail_closed", rl.failClosed).Msg("rate limiter redis error")
			if rl.failClosed {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": "rate_limit_unavailable",
				})
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.limit))
		remaining := rl.limit - count
		if remaining < 0 {
			remaining = 0
		}
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))

		if count > rl.limit {
			retryAfter := int(ttl.Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			rl.log.Warn().Str("key", key).Str("ip", r.RemoteAddr).Str("path", logredact.Path(r.URL.Path)).Msg("rate limit exceeded")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{
				"error":       "too many requests",
				"retry_after": retryAfter,
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) increment(ctx context.Context, key string) (int, time.Duration, error) {
	// Namespace the counter by the limiter's own limit+window so independent
	// limiters don't share one per-IP bucket. Without this, the strict 5/min
	// auth limiter and the 30/min public-browse limiter both incremented
	// "ratelimit:<ip>", so merely loading the site (public API calls) burned the
	// auth budget and magic-link / passkey requests spuriously 429'd.
	fullKey := fmt.Sprintf("ratelimit:%d:%d:%s", rl.limit, int(rl.window.Seconds()), key)

	pipe := rl.rdb.Pipeline()
	incrCmd := pipe.Incr(ctx, fullKey)
	pipe.Expire(ctx, fullKey, rl.window)
	ttlCmd := pipe.TTL(ctx, fullKey)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, 0, err
	}

	count := int(incrCmd.Val())
	ttl := ttlCmd.Val()
	if ttl < 0 {
		ttl = rl.window
	}

	return count, ttl, nil
}

func IPKey(r *http.Request) string {
	return r.RemoteAddr
}

func UserOrIPKey(r *http.Request) string {
	claims := GetClaims(r.Context())
	if claims != nil {
		return "user:" + claims.UserID
	}
	return "ip:" + r.RemoteAddr
}

func RateLimitByIP(rdb *redis.Client, log zerolog.Logger, limit int, window time.Duration) func(http.Handler) http.Handler {
	rl := NewRateLimiter(rdb, log, limit, window, IPKey)
	return rl.Handler
}

func RateLimitByUser(rdb *redis.Client, log zerolog.Logger, limit int, window time.Duration) func(http.Handler) http.Handler {
	rl := NewRateLimiter(rdb, log, limit, window, UserOrIPKey)
	return rl.Handler
}
