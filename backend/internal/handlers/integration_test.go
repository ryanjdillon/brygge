package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type integrationEnv struct {
	db     *pgxpool.Pool
	redis  *redis.Client
	cfg    *config.Config
	jwt    *auth.JWTService
	log    zerolog.Logger
	clubID string
}

func setupIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)

	db := testutil.SetupTestDB(t)
	rdb := testutil.SetupTestRedis(t)

	clubID := testutil.SeedClub(t, db)

	cfg := &config.Config{
		Port:             8080,
		JWTSecret:        "integration-test-secret-key-32bytes!",
		JWTAccessExpiry:  15 * time.Minute,
		JWTRefreshExpiry: 7 * 24 * time.Hour,
		VippsTestMode:    true,
	}

	var clubSlug string
	err := db.QueryRow(context.Background(),
		`SELECT slug FROM clubs WHERE id = $1`, clubID,
	).Scan(&clubSlug)
	if err != nil {
		t.Fatalf("fetching club slug: %v", err)
	}
	cfg.ClubSlug = clubSlug

	return &integrationEnv{
		db:     db,
		redis:  rdb,
		cfg:    cfg,
		jwt:    auth.NewJWTService(cfg),
		log:    zerolog.Nop(),
		clubID: clubID,
	}
}

func (e *integrationEnv) authHandler() *AuthHandler {
	return NewAuthHandler(e.db, e.redis, e.jwt, nil, e.cfg, e.log)
}

func (e *integrationEnv) membersHandler() *MembersHandler {
	return NewMembersHandler(e.db, e.cfg, e.log)
}

func (e *integrationEnv) calendarHandler() *CalendarHandler {
	return NewCalendarHandler(e.db, e.cfg, e.log)
}

func (e *integrationEnv) adminUsersHandler() *AdminUsersHandler {
	return NewAdminUsersHandler(e.db, e.cfg, e.log)
}

func (e *integrationEnv) bookingsHandler() *BookingsHandler {
	return NewBookingsHandler(e.db, e.redis, e.cfg, e.log)
}

func (e *integrationEnv) generateToken(userID string, roles []string) string {
	token, err := e.jwt.GenerateAccessToken(userID, e.clubID, roles)
	if err != nil {
		panic("failed to generate test token: " + err.Error())
	}
	return token
}

func (e *integrationEnv) seedUserWithPassword(t *testing.T, password string, roles []string) (userID, email string) {
	t.Helper()
	ctx := context.Background()

	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hashing password: %v", err)
	}

	email = "testuser-" + testutil.RandomHex(4) + "@example.com"
	err = e.db.QueryRow(ctx,
		`INSERT INTO users (club_id, email, password_hash, full_name, phone)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		e.clubID, email, hash, "Test User", "+4700000000",
	).Scan(&userID)
	if err != nil {
		t.Fatalf("seeding user with password: %v", err)
	}

	for _, role := range roles {
		if _, err := e.db.Exec(ctx,
			`INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, $3)`,
			userID, e.clubID, role,
		); err != nil {
			t.Fatalf("granting role %q: %v", role, err)
		}
	}

	return userID, email
}

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling JSON body: %v", err)
	}
	return bytes.NewBuffer(b)
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decoding response JSON: %v (body: %s)", err, rec.Body.String())
	}
	return v
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if rec.Code != expected {
		t.Fatalf("expected status %d, got %d; body: %s", expected, rec.Code, rec.Body.String())
	}
}

// ---------- Test 1: Auth Register + Login + Me ----------

func TestIntegration_AuthEmailRegisterLoginMe(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	ah := env.authHandler()

	r := chi.NewRouter()
	r.Post("/auth/register", ah.HandleEmailRegister)
	r.Post("/auth/login", ah.HandleEmailLogin)
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(env.jwt))
		r.Get("/auth/me", ah.HandleMe)
	})

	email := "register-" + testutil.RandomHex(4) + "@example.com"
	password := "SecurePass123!"

	// Register
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", jsonBody(t, map[string]string{
		"email":     email,
		"password":  password,
		"full_name": "Test Register User",
	}))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	// Login
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth/login", jsonBody(t, map[string]string{
		"email":    email,
		"password": password,
	}))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	loginResp := decodeJSON[tokenResponse](t, rec)
	if loginResp.AccessToken == "" {
		t.Fatal("expected non-empty access_token")
	}
	if loginResp.RefreshToken == "" {
		t.Fatal("expected non-empty refresh_token")
	}
	if loginResp.TokenType != "Bearer" {
		t.Fatalf("expected token_type Bearer, got %q", loginResp.TokenType)
	}

	// Me
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	meResp := decodeJSON[meResponse](t, rec)
	if meResp.UserID == "" {
		t.Fatal("expected non-empty user_id")
	}
	if meResp.ClubID != env.clubID {
		t.Fatalf("expected club_id %q, got %q", env.clubID, meResp.ClubID)
	}
	found := false
	for _, role := range meResp.Roles {
		if role == "applicant" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected applicant role in %v", meResp.Roles)
	}
}

// ---------- Test 2: Refresh Token Flow ----------

func TestIntegration_AuthRefreshToken(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	ah := env.authHandler()

	r := chi.NewRouter()
	r.Post("/auth/login", ah.HandleEmailLogin)
	r.Post("/auth/refresh", ah.HandleRefreshToken)
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(env.jwt))
		r.Get("/auth/me", ah.HandleMe)
	})

	password := "RefreshPass123!"
	_, email := env.seedUserWithPassword(t, password, []string{"member"})

	// Login
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", jsonBody(t, map[string]string{
		"email":    email,
		"password": password,
	}))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	loginResp := decodeJSON[tokenResponse](t, rec)

	// Refresh
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth/refresh", jsonBody(t, map[string]string{
		"refresh_token": loginResp.RefreshToken,
	}))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	refreshResp := decodeJSON[tokenResponse](t, rec)
	if refreshResp.AccessToken == "" {
		t.Fatal("expected non-empty access_token from refresh")
	}

	// Use new access token
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+refreshResp.AccessToken)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
}

// ---------- Test 3: Logout Revokes Refresh Token ----------

func TestIntegration_AuthLogoutRevokesRefresh(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	ah := env.authHandler()

	r := chi.NewRouter()
	r.Post("/auth/login", ah.HandleEmailLogin)
	r.Post("/auth/logout", ah.HandleLogout)
	r.Post("/auth/refresh", ah.HandleRefreshToken)

	password := "LogoutPass123!"
	_, email := env.seedUserWithPassword(t, password, []string{"member"})

	// Login
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", jsonBody(t, map[string]string{
		"email":    email,
		"password": password,
	}))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	loginResp := decodeJSON[tokenResponse](t, rec)

	// Logout
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth/logout", jsonBody(t, map[string]string{
		"refresh_token": loginResp.RefreshToken,
	}))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	// Refresh should fail
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth/refresh", jsonBody(t, map[string]string{
		"refresh_token": loginResp.RefreshToken,
	}))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusUnauthorized)

	errResp := decodeJSON[errorResponse](t, rec)
	if errResp.Error != "token has been revoked" {
		t.Fatalf("expected revoked error, got %q", errResp.Error)
	}
}

// ---------- Test 4: Members Profile CRUD ----------

func TestIntegration_MembersProfileCRUD(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	mh := env.membersHandler()

	userID, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})
	token := env.generateToken(userID, []string{"member"})

	r := chi.NewRouter()
	r.Use(middleware.Authenticate(env.jwt))
	r.Get("/me", mh.HandleGetMe)
	r.Patch("/me", mh.HandleUpdateMe)

	// Get profile
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	profile := decodeJSON[memberProfile](t, rec)
	if profile.ID != userID {
		t.Fatalf("expected user ID %q, got %q", userID, profile.ID)
	}
	if profile.FullName != "Test User" {
		t.Fatalf("expected full_name 'Test User', got %q", profile.FullName)
	}

	// Update profile
	rec = httptest.NewRecorder()
	newName := "Updated Name"
	newCity := "Oslo"
	req = httptest.NewRequest(http.MethodPatch, "/me", jsonBody(t, map[string]string{
		"full_name": newName,
		"city":      newCity,
	}))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	updated := decodeJSON[memberProfile](t, rec)
	if updated.FullName != newName {
		t.Fatalf("expected full_name %q, got %q", newName, updated.FullName)
	}
	if updated.City != newCity {
		t.Fatalf("expected city %q, got %q", newCity, updated.City)
	}

	// Verify persistence
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	persisted := decodeJSON[memberProfile](t, rec)
	if persisted.FullName != newName {
		t.Fatalf("expected persisted full_name %q, got %q", newName, persisted.FullName)
	}
}

// ---------- Test 5: Members Boat CRUD ----------

func TestIntegration_MembersBoatCRUD(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	mh := env.membersHandler()

	userID, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})
	token := env.generateToken(userID, []string{"member"})

	r := chi.NewRouter()
	r.Use(middleware.Authenticate(env.jwt))
	r.Post("/boats", mh.HandleCreateBoat)
	r.Get("/boats", mh.HandleListMyBoats)
	r.Put("/boats/{boatID}", mh.HandleUpdateBoat)
	r.Delete("/boats/{boatID}", mh.HandleDeleteBoat)

	// Create boat
	length := 10.5
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/boats", jsonBody(t, map[string]any{
		"name":                "Sea Breeze",
		"type":                "sailboat",
		"length_m":            length,
		"registration_number": "NOR-12345",
	}))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	created := decodeJSON[boat](t, rec)
	if created.Name != "Sea Breeze" {
		t.Fatalf("expected boat name 'Sea Breeze', got %q", created.Name)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty boat ID")
	}

	// List boats
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/boats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	boats := decodeJSON[[]boat](t, rec)
	if len(boats) != 1 {
		t.Fatalf("expected 1 boat, got %d", len(boats))
	}
	if boats[0].ID != created.ID {
		t.Fatalf("expected boat ID %q, got %q", created.ID, boats[0].ID)
	}

	// Update boat
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/boats/"+created.ID, jsonBody(t, map[string]any{
		"name": "Sea Breeze II",
	}))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	updatedBoat := decodeJSON[boat](t, rec)
	if updatedBoat.Name != "Sea Breeze II" {
		t.Fatalf("expected updated name 'Sea Breeze II', got %q", updatedBoat.Name)
	}

	// Delete boat
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/boats/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	// List should be empty
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/boats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	boatsAfterDelete := decodeJSON[[]boat](t, rec)
	if len(boatsAfterDelete) != 0 {
		t.Fatalf("expected 0 boats after delete, got %d", len(boatsAfterDelete))
	}
}

// ---------- Test 6: Calendar Event CRUD ----------

func TestIntegration_CalendarEventCRUD(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	ch := env.calendarHandler()

	userID, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"board"})
	token := env.generateToken(userID, []string{"board"})

	r := chi.NewRouter()
	r.Get("/events/public", ch.HandleListPublicEvents)
	r.Get("/events/export.ics", ch.HandleExportICS)
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(env.jwt))
		r.Post("/events", ch.HandleCreateEvent)
		r.Put("/events/{eventID}", ch.HandleUpdateEvent)
		r.Delete("/events/{eventID}", ch.HandleDeleteEvent)
	})

	startTime := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	endTime := startTime.Add(2 * time.Hour)

	// Create event
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/events", jsonBody(t, map[string]any{
		"title":       "Dugnad Weekend",
		"description": "Harbour cleanup",
		"location":    "Main dock",
		"start_time":  startTime.Format(time.RFC3339),
		"end_time":    endTime.Format(time.RFC3339),
		"tag":         "volunteer",
		"is_public":   true,
	}))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	created := decodeJSON[event](t, rec)
	if created.Title != "Dugnad Weekend" {
		t.Fatalf("expected title 'Dugnad Weekend', got %q", created.Title)
	}
	if created.Tag != "volunteer" {
		t.Fatalf("expected tag 'volunteer', got %q", created.Tag)
	}

	// List public events
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/events/public", nil)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	events := decodeJSON[[]event](t, rec)
	if len(events) < 1 {
		t.Fatal("expected at least 1 public event")
	}
	found := false
	for _, e := range events {
		if e.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created event not found in public list")
	}

	// Export ICS
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/events/export.ics", nil)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	ct := rec.Header().Get("Content-Type")
	if ct != "text/calendar; charset=utf-8" {
		t.Fatalf("expected ICS content type, got %q", ct)
	}
	icsBody := rec.Body.String()
	if len(icsBody) == 0 {
		t.Fatal("expected non-empty ICS body")
	}

	// Update event
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/events/"+created.ID, jsonBody(t, map[string]any{
		"title": "Dugnad Weekend Updated",
	}))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	updated := decodeJSON[event](t, rec)
	if updated.Title != "Dugnad Weekend Updated" {
		t.Fatalf("expected updated title, got %q", updated.Title)
	}

	// Delete event
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/events/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	// Verify deleted
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/events/public", nil)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	eventsAfter := decodeJSON[[]event](t, rec)
	for _, e := range eventsAfter {
		if e.ID == created.ID {
			t.Fatal("deleted event still found in public list")
		}
	}
}

// ---------- Test 7: Admin Users List and Role Management ----------

func TestIntegration_AdminUsersListAndRoles(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	auh := env.adminUsersHandler()

	adminID, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"admin"})
	adminToken := env.generateToken(adminID, []string{"admin"})

	targetID, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"applicant"})

	r := chi.NewRouter()
	r.Use(middleware.Authenticate(env.jwt))
	r.Get("/admin/users", auh.HandleListUsers)
	r.Put("/admin/users/{userID}/roles", auh.HandleUpdateUserRoles)

	// List users
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users?page=1&limit=50", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	listResp := decodeJSON[map[string]any](t, rec)
	totalCount := int(listResp["total_count"].(float64))
	if totalCount < 2 {
		t.Fatalf("expected at least 2 users, got %d", totalCount)
	}

	// Update roles
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/admin/users/"+targetID+"/roles", jsonBody(t, map[string]any{
		"roles": []string{"member", "slip_holder"},
	}))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rolesResp := decodeJSON[map[string]any](t, rec)
	roles := rolesResp["roles"].([]any)
	if len(roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(roles))
	}
	roleSet := map[string]bool{}
	for _, r := range roles {
		roleSet[r.(string)] = true
	}
	if !roleSet["member"] || !roleSet["slip_holder"] {
		t.Fatalf("expected member and slip_holder roles, got %v", roles)
	}

	// Verify audit log
	var auditCount int
	err := env.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_log
		 WHERE club_id = $1 AND entity_type = 'user' AND entity_id = $2 AND action = 'update_roles'`,
		env.clubID, targetID,
	).Scan(&auditCount)
	if err != nil {
		t.Fatalf("querying audit log: %v", err)
	}
	if auditCount == 0 {
		t.Fatal("expected audit log entry for role update")
	}
}

// ---------- Test 8: Bookings Full Flow ----------

func TestIntegration_BookingsFullFlow(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	bh := env.bookingsHandler()

	userID, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"member"})
	userToken := env.generateToken(userID, []string{"member"})

	boardID, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"board"})
	boardToken := env.generateToken(boardID, []string{"board"})

	ctx := context.Background()
	var resourceID string
	err := env.db.QueryRow(ctx,
		`INSERT INTO resources (club_id, type, name, description, capacity, price_per_unit, unit)
		 VALUES ($1, 'guest_slip', 'Guest Slip A', 'Main guest slip', 1, 250, 'night')
		 RETURNING id`,
		env.clubID,
	).Scan(&resourceID)
	if err != nil {
		t.Fatalf("seeding resource: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/resources", bh.HandleListResources)
	r.Group(func(r chi.Router) {
		r.Use(middleware.OptionalAuth(env.jwt))
		r.Post("/bookings", bh.HandleCreateBooking)
	})
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(env.jwt))
		r.Get("/bookings", bh.HandleListMyBookings)
		r.Post("/bookings/{bookingID}/confirm", bh.HandleConfirmBooking)
		r.Post("/bookings/{bookingID}/cancel", bh.HandleCancelBooking)
	})

	// List resources
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/resources", nil)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	resources := decodeJSON[[]resource](t, rec)
	if len(resources) < 1 {
		t.Fatal("expected at least 1 resource")
	}

	// Create booking
	startDate := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, 9).Format("2006-01-02")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/bookings", jsonBody(t, map[string]any{
		"resource_id": resourceID,
		"start_date":  startDate,
		"end_date":    endDate,
		"notes":       "Family visit",
	}))
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	createdBooking := decodeJSON[booking](t, rec)
	if createdBooking.Status != "pending" {
		t.Fatalf("expected pending status, got %q", createdBooking.Status)
	}

	// Confirm booking (board)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/bookings/"+createdBooking.ID+"/confirm", nil)
	req.Header.Set("Authorization", "Bearer "+boardToken)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	confirmed := decodeJSON[booking](t, rec)
	if confirmed.Status != "confirmed" {
		t.Fatalf("expected confirmed status, got %q", confirmed.Status)
	}

	// Cancel booking
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/bookings/"+createdBooking.ID+"/cancel", nil)
	req.Header.Set("Authorization", "Bearer "+boardToken)
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	cancelled := decodeJSON[booking](t, rec)
	if cancelled.Status != "cancelled" {
		t.Fatalf("expected cancelled status, got %q", cancelled.Status)
	}
}

// ---------- Test 9: Accounting — Seed Kontoplan + CRUD ----------

func (e *integrationEnv) accountingHandler() *AccountingHandler {
	svc := accounting.NewService(e.db, nil, e.log)
	return NewAccountingHandler(svc, nil, e.log)
}

func TestIntegration_AccountingSeedAndCRUD(t *testing.T) {
	t.Parallel()
	env := setupIntegrationEnv(t)
	ah := env.accountingHandler()

	userID, _ := testutil.SeedUser(t, env.db, env.clubID, []string{"treasurer"})
	token := env.generateToken(userID, []string{"treasurer"})

	r := chi.NewRouter()
	r.Use(middleware.Authenticate(env.jwt))
	r.Use(middleware.RequireRole("treasurer", "board", "admin"))
	r.Get("/accounts", ah.HandleListAccounts)
	r.Post("/accounts", ah.HandleCreateAccount)
	r.Put("/accounts/{accountID}", ah.HandleUpdateAccount)
	r.Delete("/accounts/{accountID}", ah.HandleDeleteAccount)
	r.Post("/accounts/seed", ah.HandleSeedAccounts)

	// Step 1: List accounts — should be empty initially
	req := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var accounts []accounting.Account
	json.NewDecoder(rec.Body).Decode(&accounts)
	if len(accounts) != 0 {
		t.Fatalf("expected 0 accounts before seed, got %d", len(accounts))
	}

	// Step 2: Seed kontoplan
	req = httptest.NewRequest(http.MethodPost, "/accounts/seed", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	var seedResult map[string]any
	json.NewDecoder(rec.Body).Decode(&seedResult)
	seeded := int(seedResult["seeded"].(float64))
	if seeded < 25 {
		t.Fatalf("expected at least 25 seeded accounts, got %d", seeded)
	}

	// Step 3: Seed again — should be idempotent (0 new accounts)
	req = httptest.NewRequest(http.MethodPost, "/accounts/seed", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	json.NewDecoder(rec.Body).Decode(&seedResult)
	if int(seedResult["seeded"].(float64)) != 0 {
		t.Fatalf("expected 0 seeded on second run, got %v", seedResult["seeded"])
	}

	// Step 4: List accounts — should have seeded accounts
	req = httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	json.NewDecoder(rec.Body).Decode(&accounts)
	if len(accounts) < 25 {
		t.Fatalf("expected at least 25 accounts after seed, got %d", len(accounts))
	}

	// Verify boat costs are ineligible
	for _, a := range accounts {
		if a.Code == "6100" && a.MVAEligible != "ineligible" {
			t.Errorf("account 6100 (bryggeanlegg) should be ineligible, got %q", a.MVAEligible)
		}
		if a.Code == "6200" && a.MVAEligible != "eligible" {
			t.Errorf("account 6200 (klubbhus) should be eligible, got %q", a.MVAEligible)
		}
	}

	// Step 5: Create custom account
	body := jsonBody(t, map[string]any{
		"code":         "8000",
		"name":         "Ekstraordinære kostnader",
		"account_type": "expense",
		"mva_eligible": "ineligible",
		"sort_order":   400,
	})
	req = httptest.NewRequest(http.MethodPost, "/accounts", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	var created map[string]string
	json.NewDecoder(rec.Body).Decode(&created)
	customID := created["id"]
	if customID == "" {
		t.Fatal("expected account ID in response")
	}

	// Step 6: Update custom account
	body = jsonBody(t, map[string]any{
		"name":         "Ekstraordinære utgifter",
		"description":  "Uforutsette kostnader",
		"mva_eligible": "eligible",
	})
	req = httptest.NewRequest(http.MethodPut, "/accounts/"+customID, body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	// Step 7: Delete custom account (should work — no journal lines)
	req = httptest.NewRequest(http.MethodDelete, "/accounts/"+customID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	// Step 8: Try to delete a system account — should fail
	var systemAccountID string
	env.db.QueryRow(context.Background(),
		`SELECT id FROM accounts WHERE club_id = $1 AND is_system = true LIMIT 1`,
		env.clubID,
	).Scan(&systemAccountID)

	if systemAccountID != "" {
		req = httptest.NewRequest(http.MethodDelete, "/accounts/"+systemAccountID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for system account delete, got %d; body: %s", rec.Code, rec.Body.String())
		}
	}

	// Step 9: Try to update a system account — should fail
	if systemAccountID != "" {
		body = jsonBody(t, map[string]any{
			"name":         "Hacked",
			"mva_eligible": "eligible",
		})
		req = httptest.NewRequest(http.MethodPut, "/accounts/"+systemAccountID, body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for system account update, got %d", rec.Code)
		}
	}
}
