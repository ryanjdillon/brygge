package auth

// Claims is the authenticated principal's identity carried through
// request context. Populated by session middleware; consumed by
// handlers via middleware.GetClaims.
type Claims struct {
	UserID string   `json:"user_id"`
	ClubID string   `json:"club_id"`
	Roles  []string `json:"roles"`
}
