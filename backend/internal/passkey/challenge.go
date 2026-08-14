package passkey

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/redis/go-redis/v9"
)

// ErrChallengeNotFound means the in-flight ceremony state was missing or
// expired — the client must restart the ceremony.
var ErrChallengeNotFound = errors.New("passkey: challenge not found or expired")

const challengePrefix = "passkey:chal:"

// ChallengeStore holds the short-lived webauthn.SessionData between the begin
// and finish halves of a ceremony, in Redis (already used for rate-limiting).
// Keyed by session id for register/step-up, or an ephemeral id for login.
type ChallengeStore struct {
	rdb *redis.Client
}

func NewChallengeStore(rdb *redis.Client) *ChallengeStore { return &ChallengeStore{rdb: rdb} }

// Put stores ceremony state under key with the given TTL.
func (c *ChallengeStore) Put(ctx context.Context, key string, sd *webauthn.SessionData, ttl time.Duration) error {
	b, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, challengePrefix+key, b, ttl).Err()
}

// Take fetches and atomically deletes ceremony state — challenges are
// single-use, so a replayed finish finds nothing.
func (c *ChallengeStore) Take(ctx context.Context, key string) (*webauthn.SessionData, error) {
	b, err := c.rdb.GetDel(ctx, challengePrefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrChallengeNotFound
	}
	if err != nil {
		return nil, err
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(b, &sd); err != nil {
		return nil, fmt.Errorf("passkey: decode challenge: %w", err)
	}
	return &sd, nil
}
