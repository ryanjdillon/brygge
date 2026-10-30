package devtoken

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// seed inserts a synthetic club + admin user and returns their ids.
func seed(t *testing.T, db *pgxpool.Pool) (clubID, userID string) {
	t.Helper()
	ctx := context.Background()
	clubID = "22222222-2222-2222-2222-" + testutil.RandomHex(6)
	userID = "11111111-1111-1111-1111-" + testutil.RandomHex(6)
	if _, err := db.Exec(ctx, "INSERT INTO clubs (id, slug, name) VALUES ($1, $2, 'Synthetic Club')",
		clubID, "synthetic-"+testutil.RandomHex(4)); err != nil {
		t.Fatalf("seed club: %v", err)
	}
	if _, err := db.Exec(ctx,
		"INSERT INTO users (id, club_id, email, first_name, last_name) VALUES ($1, $2, $3, 'Test', 'Admin')",
		userID, clubID, "admin-"+testutil.RandomHex(4)+"@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(ctx,
		"INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, $3::user_role)",
		userID, clubID, MinterRole); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	return clubID, userID
}

func TestMintAuthenticateRevoke(t *testing.T) {
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := NewService(db)
	clubID, userID := seed(t, db)

	plain, tok, err := svc.Mint(ctx, clubID, userID, "agent session", 0)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !strings.HasPrefix(plain, Prefix) || !strings.HasPrefix(plain, tok.Prefix) {
		t.Fatalf("plaintext %q / display prefix %q malformed", plain, tok.Prefix)
	}
	if got := tok.ExpiresAt.Sub(tok.CreatedAt); got < DefaultTTL-time.Minute || got > DefaultTTL+time.Minute {
		t.Errorf("default ttl = %s, want ~%s", got, DefaultTTL)
	}

	// Only the hash is stored.
	var stored string
	if err := db.QueryRow(ctx, "SELECT token_hash FROM developer_tokens WHERE id::text = $1", tok.ID).Scan(&stored); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if stored == plain || strings.Contains(stored, plain[len(Prefix):]) {
		t.Fatal("plaintext token stored at rest")
	}

	got, err := svc.Authenticate(ctx, plain, ScopeDBQuery)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if got.ClubID != clubID || got.CreatedBy == nil || *got.CreatedBy != userID || got.LastUsedAt == nil {
		t.Errorf("authenticated token = %+v", got)
	}

	for name, bad := range map[string]string{
		"wrong prefix": "xyz_" + plain,
		"truncated":    plain[:len(plain)-1],
		"bare prefix":  Prefix,
	} {
		if _, err := svc.Authenticate(ctx, bad, ScopeDBQuery); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := svc.Authenticate(ctx, plain, "mcp"); !errors.Is(err, ErrInvalid) {
		t.Errorf("wrong scope: err = %v, want ErrInvalid", err)
	}

	otherClub, _ := seed(t, db)
	if err := svc.Revoke(ctx, otherClub, tok.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-club revoke: err = %v, want ErrNotFound", err)
	}
	if err := svc.Revoke(ctx, clubID, tok.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.Authenticate(ctx, plain, ScopeDBQuery); !errors.Is(err, ErrInvalid) {
		t.Errorf("revoked token still authenticates: %v", err)
	}
	if err := svc.Revoke(ctx, clubID, tok.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("double revoke: err = %v, want ErrNotFound", err)
	}

	list, err := svc.List(ctx, clubID)
	if err != nil || len(list) != 1 || list[0].RevokedAt == nil {
		t.Errorf("list = %+v, err %v; want one revoked token", list, err)
	}
}

func TestMintValidation(t *testing.T) {
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := NewService(db)
	clubID, userID := seed(t, db)

	if _, _, err := svc.Mint(ctx, clubID, userID, "x", MaxTTL+time.Hour); !errors.Is(err, ErrTTL) {
		t.Errorf("over-max ttl: err = %v, want ErrTTL", err)
	}
	if _, _, err := svc.Mint(ctx, clubID, userID, "   ", time.Hour); !errors.Is(err, ErrName) {
		t.Errorf("blank name: err = %v, want ErrName", err)
	}
	if _, tok, err := svc.Mint(ctx, clubID, userID, "max", MaxTTL); err != nil || tok.ExpiresAt.Sub(tok.CreatedAt) < MaxTTL-time.Minute {
		t.Errorf("max ttl mint: tok %+v err %v", tok, err)
	}
}

func TestExpiryAndPurge(t *testing.T) {
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := NewService(db)
	clubID, userID := seed(t, db)

	plain, tok, err := svc.Mint(ctx, clubID, userID, "short", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := db.Exec(ctx, "UPDATE developer_tokens SET expires_at = now() - interval '1 minute' WHERE id::text = $1", tok.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if _, err := svc.Authenticate(ctx, plain, ScopeDBQuery); !errors.Is(err, ErrInvalid) {
		t.Errorf("expired token authenticates: %v", err)
	}

	// Inside the grace period the expired token stays listed.
	if n, err := svc.PurgeExpired(ctx); err != nil || n != 0 {
		t.Errorf("purge within grace: n=%d err=%v, want 0", n, err)
	}
	if _, err := db.Exec(ctx, "UPDATE developer_tokens SET expires_at = now() - interval '8 days' WHERE id::text = $1", tok.ID); err != nil {
		t.Fatalf("age: %v", err)
	}
	if n, err := svc.PurgeExpired(ctx); err != nil || n != 1 {
		t.Errorf("purge past grace: n=%d err=%v, want 1", n, err)
	}
}

func TestAuthenticateRequiresActiveAdminMinter(t *testing.T) {
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := NewService(db)

	// Each case mints for a freshly seeded admin, then mutates the minter or
	// the token before authenticating.
	cases := []struct {
		name    string
		mutate  func(t *testing.T, clubID, userID string)
		wantErr error
	}{
		{"active admin", func(*testing.T, string, string) {}, nil},
		{"deactivated minter", func(t *testing.T, _, userID string) {
			mustExec(t, db, "UPDATE users SET deactivated_at = now() WHERE id::text = $1", userID)
		}, ErrInvalid},
		{"admin role removed", func(t *testing.T, _, userID string) {
			mustExec(t, db, "DELETE FROM user_roles WHERE user_id::text = $1", userID)
		}, ErrInvalid},
		{"demoted to board", func(t *testing.T, clubID, userID string) {
			mustExec(t, db, "DELETE FROM user_roles WHERE user_id::text = $1", userID)
			mustExec(t, db, "INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'board'::user_role)", userID, clubID)
		}, ErrInvalid},
		{"admin of another club only", func(t *testing.T, _, userID string) {
			otherClub, _ := seed(t, db)
			mustExec(t, db, "DELETE FROM user_roles WHERE user_id::text = $1", userID)
			mustExec(t, db, "INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'admin'::user_role)", userID, otherClub)
		}, ErrInvalid},
		{"minter moved to another club", func(t *testing.T, _, userID string) {
			otherClub, _ := seed(t, db)
			mustExec(t, db, "UPDATE users SET club_id = $2 WHERE id::text = $1", userID, otherClub)
		}, ErrInvalid},
		{"minter row gone", func(t *testing.T, _, userID string) {
			mustExec(t, db, "UPDATE developer_tokens SET created_by = NULL WHERE created_by::text = $1", userID)
		}, ErrInvalid},
		{"expired", func(t *testing.T, _, userID string) {
			mustExec(t, db, "UPDATE developer_tokens SET expires_at = now() - interval '1 minute' WHERE created_by::text = $1", userID)
		}, ErrInvalid},
		{"revoked", func(t *testing.T, _, userID string) {
			mustExec(t, db, "UPDATE developer_tokens SET revoked_at = now() WHERE created_by::text = $1", userID)
		}, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clubID, userID := seed(t, db)
			plain, _, err := svc.Mint(ctx, clubID, userID, "agent", time.Hour)
			if err != nil {
				t.Fatalf("mint: %v", err)
			}
			tc.mutate(t, clubID, userID)

			tok, err := svc.Authenticate(ctx, plain, ScopeDBQuery)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if tok.ID != "" {
					t.Errorf("refused token returned metadata: %+v", tok)
				}
				var used *time.Time
				if err := db.QueryRow(ctx, "SELECT last_used_at FROM developer_tokens WHERE token_hash = $1", hashToken(plain)).Scan(&used); err != nil {
					t.Fatalf("read last_used_at: %v", err)
				}
				if used != nil {
					t.Error("refused token had its last use stamped")
				}
			}
		})
	}
}

func TestAuthenticateFailsClosedOnDBError(t *testing.T) {
	db := testutil.SetupTestDB(t)
	ctx := context.Background()
	svc := NewService(db)
	clubID, userID := seed(t, db)

	plain, _, err := svc.Mint(ctx, clubID, userID, "agent", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	db.Close()

	tok, err := svc.Authenticate(ctx, plain, ScopeDBQuery)
	if err == nil {
		t.Fatalf("authenticate on a closed pool succeeded: %+v", tok)
	}
	if errors.Is(err, ErrInvalid) {
		t.Errorf("db error reported as ErrInvalid, want a distinct error: %v", err)
	}
	if tok.ID != "" {
		t.Errorf("db error returned token metadata: %+v", tok)
	}
}

func mustExec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}
