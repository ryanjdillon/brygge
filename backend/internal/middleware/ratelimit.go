package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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

// FailClosed makes the limiter answer 503 rate_limit_unavailable (with
// Retry-After) when Redis errors, instead of the default of letting the request
// through. It is for a public route that is a guessing or abuse target (login,
// magic-link and passkey login on the strict limiter) or where each request
// costs something real (an email to an address the visitor chose), so an
// outage must not lift the budget; ordinary routes stay fail-open so a Redis
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
			rl.log.Warn().Err(err).Str("key", key).Bool("fail_closed", rl.failClosed).Msg("rate limiter redis error")
			if rl.failClosed {
				w.Header().Set("Retry-After", "60")
				writeError(w, http.StatusServiceUnavailable, "rate_limit_unavailable")
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

// rateLimitIncrScript counts one hit in a fixed window: the first hit
// creates the counter and starts its expiry, later hits only increment, so
// steady traffic cannot keep pushing the reset out. A counter found without
// an expiry gets one, so none can block forever. Returns {count, pttl ms}.
var rateLimitIncrScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])
if n == 1 or ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {n, ttl}
`)

func (rl *RateLimiter) increment(ctx context.Context, key string) (int, time.Duration, error) {
	// Namespace the counter by the limiter's own limit+window so independent
	// limiters don't share one per-IP bucket. Without this, the strict 5/min
	// auth limiter and the 30/min public-browse limiter both incremented
	// "ratelimit:<ip>", so merely loading the site (public API calls) burned the
	// auth budget and magic-link / passkey requests spuriously 429'd.
	fullKey := fmt.Sprintf("ratelimit:%d:%d:%s", rl.limit, int(rl.window.Seconds()), key)

	res, err := rateLimitIncrScript.Run(ctx, rl.rdb, []string{fullKey}, rl.window.Milliseconds()).Int64Slice()
	if err != nil {
		return 0, 0, err
	}
	if len(res) != 2 {
		return 0, 0, fmt.Errorf("rate limiter script returned %d values", len(res))
	}

	ttl := time.Duration(res[1]) * time.Millisecond
	if ttl <= 0 {
		ttl = rl.window
	}
	return int(res[0]), ttl, nil
}

// ipv6BucketBits is the IPv6 prefix one client is assumed to control: an ISP
// hands a subscriber at least a /64, and the host can pick any address in it.
const ipv6BucketBits = 64

// IPBucket maps a client address to the subject a per-IP limit or dedupe key
// is counted against. An IPv4 address (an IPv4-mapped IPv6 one included) is
// its own bucket; an IPv6 address is bucketed by its /64, e.g.
// "2001:db8:1:2::/64", because a client with a /64 can rotate addresses freely.
// It accepts a bare IP or host:port and drops an IPv6 zone. Anything that does
// not parse is returned trimmed as-is, and an empty address is "unknown", so
// such requests still share one bucket instead of escaping the limit.
func IPBucket(addr string) string {
	a, ok := parseAddr(strings.TrimSpace(addr))
	if !ok {
		if s := strings.TrimSpace(addr); s != "" {
			return s
		}
		return "unknown"
	}
	if a.Is4() {
		return a.String()
	}
	p, err := a.WithZone("").Prefix(ipv6BucketBits)
	if err != nil {
		return a.String()
	}
	return p.String()
}

// IPKey keys a limiter on the client's IP bucket (see IPBucket).
func IPKey(r *http.Request) string {
	return IPBucket(r.RemoteAddr)
}

// UserOrIPKey keys a limiter on the signed-in user, or on the client's IP
// bucket for an anonymous request.
func UserOrIPKey(r *http.Request) string {
	claims := GetClaims(r.Context())
	if claims != nil {
		return "user:" + claims.UserID
	}
	return "ip:" + IPBucket(r.RemoteAddr)
}

// RateLimitByIP limits per client IP bucket and fails open on a Redis error.
func RateLimitByIP(rdb *redis.Client, log zerolog.Logger, limit int, window time.Duration) func(http.Handler) http.Handler {
	rl := NewRateLimiter(rdb, log, limit, window, IPKey)
	return rl.Handler
}

// StrictRateLimitByIP limits per client IP bucket and fails closed (503) on a
// Redis error. It is the strict limiter for unauthenticated login routes.
func StrictRateLimitByIP(rdb *redis.Client, log zerolog.Logger, limit int, window time.Duration) func(http.Handler) http.Handler {
	return NewRateLimiter(rdb, log, limit, window, IPKey).FailClosed().Handler
}

func RateLimitByUser(rdb *redis.Client, log zerolog.Logger, limit int, window time.Duration) func(http.Handler) http.Handler {
	rl := NewRateLimiter(rdb, log, limit, window, UserOrIPKey)
	return rl.Handler
}
