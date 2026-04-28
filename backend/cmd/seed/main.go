package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/config"
)

type seedUser struct {
	email, name, phone string
	isLocal            bool
	roles              []string
}

// users.full_name is generated; we write first/last directly. The seed
// data uses the same last-space heuristic as the SQL backfill (DIL-227).
func splitFirst(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, " "); i > 0 {
		return strings.TrimSpace(name[:i])
	}
	return name
}

func splitLast(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, " "); i > 0 {
		return strings.TrimSpace(name[i+1:])
	}
	return ""
}

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "database ping failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("seeding database...")

	// Create default club
	var clubID string
	err = db.QueryRow(ctx, `
		INSERT INTO clubs (slug, name, description, latitude, longitude)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude
		RETURNING id
	`, cfg.ClubSlug, "Klokkarvik Båtlag", "En hyggelig båtklubb i Klokkarvik", 60.224303, 5.155736).Scan(&clubID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create club: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  club: %s (id: %s)\n", cfg.ClubSlug, clubID)

	// All users to seed. Auth is magic-link only (DIL-22 + DIL-28); no
	// password storage. Test logins go through the demo-auth handler when
	// FEATURE_DEMO_AUTH is set.
	users := []seedUser{
		{email: "admin@brygge.local", name: "Admin Bruker", phone: "+4712345678", isLocal: true, roles: []string{"admin", "board", "member"}},
		{email: "slip-member@brygge.local", name: "Kari Sjømann", phone: "+4711111111", isLocal: true, roles: []string{"member"}},
		{email: "wl-member@brygge.local", name: "Per Venansen", phone: "+4722222222", isLocal: true, roles: []string{"member"}},
		{email: "member@brygge.local", name: "Medlem Hansen", phone: "+4798765432", isLocal: false, roles: []string{"member"}},
	}

	// Waiting list members (not login users, just populate the list)
	waitingListUsers := []seedUser{
		{email: "ola.nord@example.com", name: "Ola Nordmann", phone: "+4733333333", isLocal: true},
		{email: "liv.strand@example.com", name: "Liv Strand", phone: "+4744444444", isLocal: false},
		{email: "erik.berg@example.com", name: "Erik Berg", phone: "+4755555555", isLocal: true},
		{email: "anne.fjord@example.com", name: "Anne Fjord", phone: "+4766666666", isLocal: false},
		{email: "bjorn.havn@example.com", name: "Bjørn Havn", phone: "+4777777777", isLocal: true},
		{email: "ingrid.molo@example.com", name: "Ingrid Molo", phone: "+4788888801", isLocal: false},
		{email: "lars.kai@example.com", name: "Lars Kai", phone: "+4788888802", isLocal: true},
		{email: "sofie.brygge@example.com", name: "Sofie Brygge", phone: "+4788888803", isLocal: false},
	}

	userIDs := make(map[string]string) // email -> id

	// Create login users
	for _, u := range users {
		var id string
		err = db.QueryRow(ctx, `
			INSERT INTO users (club_id, email, first_name, last_name, phone, is_local)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (club_id, email) DO UPDATE SET
				first_name = EXCLUDED.first_name,
				last_name = EXCLUDED.last_name,
				is_local = EXCLUDED.is_local
			RETURNING id
		`, clubID, u.email, splitFirst(u.name), splitLast(u.name), u.phone, u.isLocal).Scan(&id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create user %s: %v\n", u.email, err)
			os.Exit(1)
		}
		userIDs[u.email] = id
		fmt.Printf("  user: %s (%s)\n", u.email, u.name)

		for _, role := range u.roles {
			_, err = db.Exec(ctx, `
				INSERT INTO user_roles (user_id, club_id, role)
				VALUES ($1, $2, $3)
				ON CONFLICT (user_id, club_id, role) DO NOTHING
			`, id, clubID, role)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to grant role %s to %s: %v\n", role, u.email, err)
			}
		}
	}

	// Create waiting list filler users (data only)
	for _, u := range waitingListUsers {
		var id string
		err = db.QueryRow(ctx, `
			INSERT INTO users (club_id, email, first_name, last_name, phone, is_local)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (club_id, email) DO UPDATE SET
				first_name = EXCLUDED.first_name,
				last_name = EXCLUDED.last_name,
				is_local = EXCLUDED.is_local
			RETURNING id
		`, clubID, u.email, splitFirst(u.name), splitLast(u.name), u.phone, u.isLocal).Scan(&id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create user %s: %v\n", u.email, err)
			continue
		}
		userIDs[u.email] = id
		fmt.Printf("  user: %s (%s, local=%v)\n", u.email, u.name, u.isLocal)

		_, err = db.Exec(ctx, `
			INSERT INTO user_roles (user_id, club_id, role)
			VALUES ($1, $2, 'member')
			ON CONFLICT (user_id, club_id, role) DO NOTHING
		`, id, clubID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to grant member role to %s: %v\n", u.email, err)
		}
	}

	// Create waiting list entries
	// wl-member (Per Venansen) is on the waiting list, plus the 8 filler users
	waitingListEmails := []string{
		"ola.nord@example.com",       // pos 1, local
		"wl-member@brygge.local",     // pos 2, local (test login user)
		"liv.strand@example.com",     // pos 3, non-local
		"erik.berg@example.com",      // pos 4, local
		"anne.fjord@example.com",     // pos 5, non-local
		"bjorn.havn@example.com",     // pos 6, local
		"member@brygge.local",        // pos 7, non-local (original member)
		"ingrid.molo@example.com",    // pos 8, non-local
		"lars.kai@example.com",       // pos 9, local
		"sofie.brygge@example.com",   // pos 10, non-local
	}

	// Clear existing waiting list entries for idempotent re-seeding
	_, _ = db.Exec(ctx, `DELETE FROM waiting_list_entries WHERE club_id = $1`, clubID)

	for i, email := range waitingListEmails {
		uid := userIDs[email]
		if uid == "" {
			fmt.Fprintf(os.Stderr, "  skipping waiting list entry for %s: user not found\n", email)
			continue
		}
		// Look up is_local from the user we created
		var isLocal bool
		_ = db.QueryRow(ctx, `SELECT is_local FROM users WHERE id = $1`, uid).Scan(&isLocal)

		_, err = db.Exec(ctx, `
			INSERT INTO waiting_list_entries (user_id, club_id, position, is_local, status)
			VALUES ($1, $2, $3, $4, 'active')
		`, uid, clubID, i+1, isLocal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  failed to create waiting list entry for %s: %v\n", email, err)
		}
	}
	fmt.Printf("  waiting list: %d entries created\n", len(waitingListEmails))

	// Seed dock fingers — placed inside the harbor outline (viewBox 757x463).
	// Two horizontal piers; slips hang off the south side.
	fingers := []struct {
		label                  string
		x1, y1, x2, y2, widthM float64
		position               int
	}{
		{"A", 200, 200, 420, 200, 1.5, 1},
		{"B", 200, 280, 440, 280, 1.5, 2},
	}
	fingerIDs := make(map[string]string)
	for _, f := range fingers {
		var fid string
		err = db.QueryRow(ctx, `
			INSERT INTO dock_fingers (club_id, label, x1, y1, x2, y2, width_m, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT DO NOTHING
			RETURNING id
		`, clubID, f.label, f.x1, f.y1, f.x2, f.y2, f.widthM, f.position).Scan(&fid)
		if err != nil {
			// ON CONFLICT DO NOTHING returns no rows; fall back to a SELECT.
			_ = db.QueryRow(ctx,
				`SELECT id FROM dock_fingers WHERE club_id = $1 AND label = $2 LIMIT 1`,
				clubID, f.label,
			).Scan(&fid)
		}
		fingerIDs[f.label] = fid
	}
	fmt.Printf("  dock fingers: %d created\n", len(fingers))

	// Create slips with map positions along the fingers and assign one
	// to slip-member (Kari Sjømann). Boats are oriented perpendicular
	// to the finger (rotation=90) and on the south (port) side.
	slips := []struct {
		number, section            string
		lengthM, widthM            float64
		mapX, mapY, mapRotation    float64
		mapFingerLabel, mapSide    string
	}{
		{"A1", "A", 10, 3.5, 240, 215, 90, "A", "port"},
		{"A2", "A", 12, 4.0, 290, 215, 90, "A", "port"},
		{"A3", "A", 8, 3.0, 340, 215, 90, "A", "port"},
		{"B1", "B", 14, 4.5, 240, 295, 90, "B", "port"},
		{"B2", "B", 10, 3.5, 300, 295, 90, "B", "port"},
		{"B3", "B", 12, 4.0, 360, 295, 90, "B", "port"},
	}

	slipIDs := make(map[string]string)
	for _, s := range slips {
		var fingerID *string
		if id, ok := fingerIDs[s.mapFingerLabel]; ok && id != "" {
			fingerID = &id
		}
		var slipID string
		err = db.QueryRow(ctx, `
			INSERT INTO slips (club_id, number, section, length_m, width_m, status,
			                   map_x, map_y, map_rotation, map_finger_id, map_side)
			VALUES ($1, $2, $3, $4, $5, 'vacant', $6, $7, $8, $9, $10)
			ON CONFLICT (club_id, number) DO UPDATE SET
			  section = EXCLUDED.section,
			  map_x = EXCLUDED.map_x,
			  map_y = EXCLUDED.map_y,
			  map_rotation = EXCLUDED.map_rotation,
			  map_finger_id = EXCLUDED.map_finger_id,
			  map_side = EXCLUDED.map_side
			RETURNING id
		`, clubID, s.number, s.section, s.lengthM, s.widthM,
			s.mapX, s.mapY, s.mapRotation, fingerID, s.mapSide).Scan(&slipID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  failed to create slip %s: %v\n", s.number, err)
			continue
		}
		slipIDs[s.number] = slipID
	}
	fmt.Printf("  slips: %d created\n", len(slips))

	// Assign slip A1 to Kari Sjømann (slip-member) with harbor membership
	slipMemberID := userIDs["slip-member@brygge.local"]
	slipA1 := slipIDs["A1"]
	if slipMemberID != "" && slipA1 != "" {
		// Mark slip as occupied
		_, _ = db.Exec(ctx, `UPDATE slips SET status = 'occupied' WHERE id = $1`, slipA1)

		// Release any existing assignment first
		_, _ = db.Exec(ctx, `
			UPDATE slip_assignments SET released_at = now()
			WHERE slip_id = $1 AND released_at IS NULL
		`, slipA1)

		now := time.Now()
		_, err = db.Exec(ctx, `
			INSERT INTO slip_assignments (slip_id, user_id, club_id, harbor_membership_amount, harbor_membership_paid_at, assigned_at, assignment_type)
			VALUES ($1, $2, $3, 50000, $4, $4, 'permanent')
		`, slipA1, slipMemberID, clubID, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  failed to assign slip to Kari: %v\n", err)
		} else {
			fmt.Println("  slip A1 assigned to Kari Sjømann (harbor membership paid)")
		}
	}

	// Assign slip B2 as a seasonal rental to Medlem Hansen — gives the
	// harbor map a second occupied slip with a different color.
	memberID := userIDs["member@brygge.local"]
	slipB2 := slipIDs["B2"]
	if memberID != "" && slipB2 != "" {
		_, _ = db.Exec(ctx, `UPDATE slips SET status = 'occupied' WHERE id = $1`, slipB2)
		_, _ = db.Exec(ctx, `
			UPDATE slip_assignments SET released_at = now()
			WHERE slip_id = $1 AND released_at IS NULL
		`, slipB2)
		now := time.Now()
		if _, err := db.Exec(ctx, `
			INSERT INTO slip_assignments (slip_id, user_id, club_id, assigned_at, assignment_type)
			VALUES ($1, $2, $3, $4, 'seasonal')
		`, slipB2, memberID, clubID, now); err != nil {
			fmt.Fprintf(os.Stderr, "  failed to assign slip B2: %v\n", err)
		} else {
			fmt.Println("  slip B2 assigned to Medlem Hansen (seasonal)")
		}
	}

	// Seed boat models
	for _, bm := range seedBoatModels {
		_, err = db.Exec(ctx, `
			INSERT INTO boat_models (manufacturer, model, year_from, year_to,
			                         length_m, beam_m, draft_m, weight_kg, boat_type, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'seed')
			ON CONFLICT DO NOTHING
		`, bm.manufacturer, bm.model, bm.yearFrom, bm.yearTo,
			bm.lengthM, bm.beamM, bm.draftM, bm.weightKg, bm.boatType)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  failed to seed boat model %s %s: %v\n", bm.manufacturer, bm.model, err)
		}
	}
	fmt.Printf("  boat models: %d seeded\n", len(seedBoatModels))

	// Give Kari a boat (linked to her slip) — Askeladden C61 Center from the model DB
	var kariModelID string
	_ = db.QueryRow(ctx,
		`SELECT id FROM boat_models WHERE manufacturer = 'Askeladden' AND model = 'C61 Center' LIMIT 1`,
	).Scan(&kariModelID)

	if slipMemberID != "" {
		var kariBoatID string
		err = db.QueryRow(ctx, `
			INSERT INTO boats (user_id, club_id, name, type, manufacturer, model,
			                   length_m, beam_m, draft_m, weight_kg, registration_number,
			                   boat_model_id, measurements_confirmed)
			VALUES ($1, $2, 'Sjøsprøyt', 'motorboat', 'Askeladden', 'C61 Center',
			        6.15, 2.28, 0.40, 1050, 'NO-12345',
			        $3, true)
			ON CONFLICT DO NOTHING
			RETURNING id
		`, slipMemberID, clubID, kariModelID).Scan(&kariBoatID)
		if err == nil && kariBoatID != "" {
			// Link boat to slip assignment
			_, _ = db.Exec(ctx,
				`UPDATE slip_assignments SET boat_id = $1
				 WHERE user_id = $2 AND club_id = $3 AND released_at IS NULL`,
				kariBoatID, slipMemberID, clubID)
			fmt.Println("  boat: Sjøsprøyt (Askeladden C61) for Kari, linked to slip A1")
		}
	}

	// Give Per a boat (linked to waiting list) — custom boat, unconfirmed
	wlMemberID := userIDs["wl-member@brygge.local"]
	if wlMemberID != "" {
		var perBoatID string
		err = db.QueryRow(ctx, `
			INSERT INTO boats (user_id, club_id, name, type, manufacturer, model,
			                   length_m, beam_m, draft_m, weight_kg, registration_number,
			                   measurements_confirmed)
			VALUES ($1, $2, 'Havansen', 'motorboat', 'Ryds', '550 GT',
			        5.48, 2.10, 0.35, 620, '',
			        false)
			ON CONFLICT DO NOTHING
			RETURNING id
		`, wlMemberID, clubID).Scan(&perBoatID)
		if err == nil && perBoatID != "" {
			// Link boat to waiting list entry
			_, _ = db.Exec(ctx,
				`UPDATE waiting_list_entries SET boat_id = $1
				 WHERE user_id = $2 AND club_id = $3 AND status = 'active'`,
				perBoatID, wlMemberID, clubID)
			fmt.Println("  boat: Havansen (Ryds 550 GT) for Per, linked to waiting list")
		}
	}

	// Clear idempotent-unsafe tables before re-seeding
	// Nullify order_lines FKs first so product/price_item deletes succeed
	_, _ = db.Exec(ctx, `UPDATE order_lines SET product_id = NULL, variant_id = NULL
		WHERE order_id IN (SELECT id FROM orders WHERE club_id = $1)`, clubID)
	_, _ = db.Exec(ctx, `UPDATE order_lines SET price_item_id = NULL
		WHERE order_id IN (SELECT id FROM orders WHERE club_id = $1)`, clubID)
	_, _ = db.Exec(ctx, `DELETE FROM price_items WHERE club_id = $1`, clubID)
	_, _ = db.Exec(ctx, `DELETE FROM events WHERE club_id = $1`, clubID)
	_, _ = db.Exec(ctx, `DELETE FROM products WHERE club_id = $1`, clubID)

	// Seed beam-based slip fee pricing tiers
	slipFeeTiers := []struct {
		name    string
		amount  float64
		beamMin float64
		beamMax float64
		order   int
	}{
		{"Plassleie ≤ 2.5m bredde", 6000, 0, 2.5, 20},
		{"Plassleie 2.5–3.5m bredde", 8500, 2.5, 3.5, 21},
		{"Plassleie 3.5–4.5m bredde", 12000, 3.5, 4.5, 22},
		{"Plassleie > 4.5m bredde", 15000, 4.5, 99, 23},
	}
	for _, t := range slipFeeTiers {
		metadata := fmt.Sprintf(`{"beam_min": %.1f, "beam_max": %.1f}`, t.beamMin, t.beamMax)
		_, err = db.Exec(ctx, `
			INSERT INTO price_items (club_id, category, name, description, amount, unit,
			                         installments_allowed, max_installments, metadata, sort_order)
			VALUES ($1, 'slip_fee', $2, 'Årlig plassleie basert på båtbredde', $3, 'year',
			        false, 1, $4::jsonb, $5)
			ON CONFLICT DO NOTHING
		`, clubID, t.name, t.amount, metadata, t.order)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  failed to seed slip fee tier %s: %v\n", t.name, err)
		}
	}
	fmt.Printf("  slip fee tiers: %d seeded\n", len(slipFeeTiers))

	// Create some booking resources
	resources := []struct {
		typ, name, desc, unit string
		capacity              int
		price                 float64
	}{
		{"guest_slip", "Gjesteplass A", "Gjesteplass ved hovedbrygga", "night", 5, 250},
		{"guest_slip", "Gjesteplass B", "Gjesteplass ved nordbrygga", "night", 3, 200},
		{"motorhome_spot", "Bobilplass", "Bobilparkering med strøm", "night", 4, 300},
		{"club_room", "Klubbhuset", "Klubbhuset med kjøkken", "day", 1, 1500},
	}
	for _, r := range resources {
		_, err = db.Exec(ctx, `
			INSERT INTO resources (club_id, type, name, description, unit, capacity, price_per_unit)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT DO NOTHING
		`, clubID, r.typ, r.name, r.desc, r.unit, r.capacity, r.price)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create resource %s: %v\n", r.name, err)
		}
	}
	fmt.Println("  booking resources: 4 created")

	// Create pricing catalog
	type priceItemSeed struct {
		category, name, description, unit string
		amount                            float64
		installments                      bool
		maxInstall                        int
		metadata                          string
		sortOrder                         int
	}
	priceItems := []priceItemSeed{
		{"harbor_membership", "Harbor Membership", "One-time harbor infrastructure equity payment", "once", 50000, true, 12, `{}`, 10},
		{"slip_fee", "Årlig plassleie", "Årlig leie for fast båtplass", "year", 8500, false, 1, `{}`, 20},
		{"seasonal_rental", "Sommersesong", "Sesongplass sommer", "season", 6000, false, 1, `{"season":"summer","period_start":"05-01","period_end":"09-30"}`, 30},
		{"seasonal_rental", "Vintersesong", "Sesongplass vinter", "season", 4000, false, 1, `{"season":"winter","period_start":"10-01","period_end":"04-30"}`, 31},
		{"guest", "Gjesteplass per døgn", "Gjesteplass ved hovedbrygga", "day", 250, false, 1, `{}`, 40},
		{"motorhome", "Bobilplass per døgn", "Bobilparkering med strøm", "day", 300, false, 1, `{}`, 50},
		{"room_hire", "Klubbhuset", "Klubbhus med kjøkken, per dag", "day", 1500, false, 1, `{}`, 60},
		{"service", "Kran – opp/utsett", "Bruk av kran for sjøsetting/opptak", "once", 1200, false, 1, `{}`, 70},
		{"service", "Strøm vinter", "Strømtilkobling gjennom vinteren", "season", 2000, false, 1, `{"season":"winter","period_start":"10-01","period_end":"04-30"}`, 71},
	}
	for _, p := range priceItems {
		_, err = db.Exec(ctx, `
			INSERT INTO price_items (club_id, category, name, description, amount, unit,
			                         installments_allowed, max_installments, metadata, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10)
			ON CONFLICT DO NOTHING
		`, clubID, p.category, p.name, p.description, p.amount, p.unit,
			p.installments, p.maxInstall, p.metadata, p.sortOrder)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create price item %s: %v\n", p.name, err)
		}
	}
	fmt.Printf("  price items: %d created\n", len(priceItems))

	// Create some sample events
	now := time.Now()
	events := []struct {
		title, description, location, tag string
		startOffset, endOffset            time.Duration
	}{
		{"Vårregatta 2026", "Årets første regatta!", "Fjorden", "regatta", 7 * 24 * time.Hour, 7*24*time.Hour + 8*time.Hour},
		{"Dugnad vår", "Vårdugnad for alle medlemmer", "Brygga", "volunteer", 14 * 24 * time.Hour, 14*24*time.Hour + 4*time.Hour},
		{"Sommerfest", "Sommeravslutning med grilling", "Klubbhuset", "social", 30 * 24 * time.Hour, 30*24*time.Hour + 6*time.Hour},
		{"Årsmøte 2026", "Ordinært årsmøte", "Klubbhuset", "agm", 60 * 24 * time.Hour, 60*24*time.Hour + 3*time.Hour},
	}
	adminID := userIDs["admin@brygge.local"]
	for _, e := range events {
		_, err = db.Exec(ctx, `
			INSERT INTO events (club_id, title, description, location, tag, start_time, end_time, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, clubID, e.title, e.description, e.location, e.tag, now.Add(e.startOffset), now.Add(e.endOffset), adminID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create event %s: %v\n", e.title, err)
		}
	}
	fmt.Println("  events: 4 created")

	// Create merchandise products with variants
	type variantSeed struct {
		size, color, imageURL string
		stock                 int
	}
	type productSeed struct {
		name, description, imageURL string
		price                       float64
		stock, sortOrder            int
		variants                    []variantSeed
	}
	products := []productSeed{
		{"Klubbvimpel", "Brygge-vimpel i flaggstoff, 30x40 cm", "/images/products/vimpel.jpg", 350, 25, 10, nil},
		{"T-skjorte", "Brygge-logo, 100% bomull", "/images/products/tskjorte-hvit.jpg", 299, 0, 20, []variantSeed{
			{"S", "Hvit", "/images/products/tskjorte-hvit.jpg", 10},
			{"M", "Hvit", "/images/products/tskjorte-hvit.jpg", 15},
			{"L", "Hvit", "/images/products/tskjorte-hvit.jpg", 12},
			{"XL", "Hvit", "/images/products/tskjorte-hvit.jpg", 8},
			{"XXL", "Hvit", "/images/products/tskjorte-hvit.jpg", 5},
			{"S", "Navy", "/images/products/tskjorte-navy.jpg", 8},
			{"M", "Navy", "/images/products/tskjorte-navy.jpg", 12},
			{"L", "Navy", "/images/products/tskjorte-navy.jpg", 10},
			{"XL", "Navy", "/images/products/tskjorte-navy.jpg", 6},
			{"XXL", "Navy", "/images/products/tskjorte-navy.jpg", 3},
		}},
		{"Caps", "Brygge-caps, one size fits all", "/images/products/caps-navy.jpg", 199, 0, 30, []variantSeed{
			{"", "Navy", "/images/products/caps-navy.jpg", 20},
			{"", "Hvit", "/images/products/caps-hvit.jpg", 15},
			{"", "Rød", "/images/products/caps-rod.jpg", 5},
		}},
		{"Seilerhanske", "Halvfinger, skinnhåndflate", "/images/products/seilerhanske.jpg", 449, 0, 40, []variantSeed{
			{"S", "", "", 5}, {"M", "", "", 8}, {"L", "", "", 10}, {"XL", "", "", 4},
		}},
		{"Drybag 10L", "Vanntett bag med Brygge-logo", "/images/products/drybag.jpg", 249, 20, 50, nil},
	}
	for _, p := range products {
		var productID string
		err = db.QueryRow(ctx, `
			INSERT INTO products (club_id, name, description, price, image_url, stock, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id
		`, clubID, p.name, p.description, p.price, p.imageURL, p.stock, p.sortOrder).Scan(&productID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create product %s: %v\n", p.name, err)
			continue
		}
		for i, v := range p.variants {
			_, err = db.Exec(ctx, `
				INSERT INTO product_variants (product_id, size, color, stock, image_url, sort_order)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (product_id, size, color) DO UPDATE SET stock = EXCLUDED.stock, image_url = EXCLUDED.image_url
			`, productID, v.size, v.color, v.stock, v.imageURL, i)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  failed to create variant %s/%s for %s: %v\n", v.size, v.color, p.name, err)
			}
		}
	}
	fmt.Printf("  products: %d created\n", len(products))

	fmt.Println("\ndone! you can now log in with:")
	fmt.Println("  admin:          admin@brygge.local / admin123")
	fmt.Println("  member (slip):  slip-member@brygge.local / member123  (Kari Sjømann, has harbor membership + slip A1)")
	fmt.Println("  member (wl):    wl-member@brygge.local / member123  (Per Venansen, on waiting list #2)")
	fmt.Println("  member:         member@brygge.local / member123  (Medlem Hansen, on waiting list #7)")
	fmt.Println("\n  or via Vipps mock with corresponding test users")
}
