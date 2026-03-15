package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/brygge-klubb/brygge/internal/config"
)

type seedUser struct {
	email, name, phone string
	vippsSub           string
	isLocal            bool
	roles              []string
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
	`, cfg.ClubSlug, "Brygge Båtklubb", "En hyggelig båtklubb", 59.9075, 10.7350).Scan(&clubID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create club: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  club: %s (id: %s)\n", cfg.ClubSlug, clubID)

	hash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	memberHash, _ := bcrypt.GenerateFromPassword([]byte("member123"), bcrypt.DefaultCost)

	// All users to seed
	users := []seedUser{
		{email: "admin@brygge.local", name: "Admin Bruker", phone: "+4712345678", vippsSub: "vipps-admin-001", isLocal: true, roles: []string{"admin", "board", "member"}},
		{email: "slip-member@brygge.local", name: "Kari Sjømann", phone: "+4711111111", vippsSub: "vipps-slip-001", isLocal: true, roles: []string{"member"}},
		{email: "wl-member@brygge.local", name: "Per Venansen", phone: "+4722222222", vippsSub: "vipps-wl-001", isLocal: true, roles: []string{"member"}},
		{email: "member@brygge.local", name: "Medlem Hansen", phone: "+4798765432", vippsSub: "vipps-member-001", isLocal: false, roles: []string{"member"}},
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

	// Create login users (with password)
	for _, u := range users {
		pw := memberHash
		if u.email == "admin@brygge.local" {
			pw = hash
		}
		var id string
		err = db.QueryRow(ctx, `
			INSERT INTO users (club_id, email, password_hash, full_name, phone, vipps_sub, is_local)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (club_id, email) DO UPDATE SET
				password_hash = EXCLUDED.password_hash,
				full_name = EXCLUDED.full_name,
				vipps_sub = EXCLUDED.vipps_sub,
				is_local = EXCLUDED.is_local
			RETURNING id
		`, clubID, u.email, string(pw), u.name, u.phone, u.vippsSub, u.isLocal).Scan(&id)
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

	// Create waiting list filler users (member role, no password — Vipps-only or just data)
	for _, u := range waitingListUsers {
		var id string
		err = db.QueryRow(ctx, `
			INSERT INTO users (club_id, email, full_name, phone, is_local)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (club_id, email) DO UPDATE SET
				full_name = EXCLUDED.full_name,
				is_local = EXCLUDED.is_local
			RETURNING id
		`, clubID, u.email, u.name, u.phone, u.isLocal).Scan(&id)
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

	// Create slips and assign one to slip-member (Kari Sjømann)
	slips := []struct {
		number, section string
		lengthM, widthM float64
	}{
		{"A1", "A", 10, 3.5},
		{"A2", "A", 12, 4.0},
		{"A3", "A", 8, 3.0},
		{"B1", "B", 14, 4.5},
		{"B2", "B", 10, 3.5},
		{"B3", "B", 12, 4.0},
	}

	slipIDs := make(map[string]string)
	for _, s := range slips {
		var slipID string
		err = db.QueryRow(ctx, `
			INSERT INTO slips (club_id, number, section, length_m, width_m, status)
			VALUES ($1, $2, $3, $4, $5, 'vacant')
			ON CONFLICT (club_id, number) DO UPDATE SET section = EXCLUDED.section
			RETURNING id
		`, clubID, s.number, s.section, s.lengthM, s.widthM).Scan(&slipID)
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
			INSERT INTO slip_assignments (slip_id, user_id, club_id, harbor_membership_amount, harbor_membership_paid_at, assigned_at)
			VALUES ($1, $2, $3, 50000, $4, $4)
		`, slipA1, slipMemberID, clubID, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  failed to assign slip to Kari: %v\n", err)
		} else {
			fmt.Println("  slip A1 assigned to Kari Sjømann (harbor membership paid)")
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
