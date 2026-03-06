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

	// Create admin user (admin@brygge.local / admin123)
	hash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	var adminID string
	err = db.QueryRow(ctx, `
		INSERT INTO users (club_id, email, password_hash, full_name, phone)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (club_id, email) DO UPDATE SET password_hash = EXCLUDED.password_hash
		RETURNING id
	`, clubID, "admin@brygge.local", string(hash), "Admin Bruker", "+4712345678").Scan(&adminID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create admin user: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  admin user: admin@brygge.local (id: %s)\n", adminID)

	// Grant admin + styre roles
	for _, role := range []string{"admin", "styre", "member"} {
		_, err = db.Exec(ctx, `
			INSERT INTO user_roles (user_id, club_id, role)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, club_id, role) DO NOTHING
		`, adminID, clubID, role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to grant role %s: %v\n", role, err)
			os.Exit(1)
		}
	}
	fmt.Println("  roles: admin, styre, member")

	// Create a regular member (member@brygge.local / member123)
	memberHash, _ := bcrypt.GenerateFromPassword([]byte("member123"), bcrypt.DefaultCost)
	var memberID string
	err = db.QueryRow(ctx, `
		INSERT INTO users (club_id, email, password_hash, full_name, phone)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (club_id, email) DO UPDATE SET password_hash = EXCLUDED.password_hash
		RETURNING id
	`, clubID, "member@brygge.local", string(memberHash), "Medlem Hansen", "+4798765432").Scan(&memberID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create member user: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  member user: member@brygge.local (id: %s)\n", memberID)

	_, err = db.Exec(ctx, `
		INSERT INTO user_roles (user_id, club_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, club_id, role) DO NOTHING
	`, memberID, clubID, "member")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to grant member role: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  roles: member")

	// Create some booking resources
	resources := []struct {
		typ, name, desc, unit string
		capacity              int
		price                 float64
	}{
		{"guest_slip", "Gjesteplass A", "Gjesteplass ved hovedbrygga", "night", 5, 250},
		{"guest_slip", "Gjesteplass B", "Gjesteplass ved nordbrygga", "night", 3, 200},
		{"bobil_spot", "Bobilplass", "Bobilparkering med strøm", "night", 4, 300},
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

	// Create some sample events
	now := time.Now()
	events := []struct {
		title, description, location, tag string
		startOffset, endOffset            time.Duration
	}{
		{"Vårregatta 2026", "Årets første regatta!", "Fjorden", "regatta", 7 * 24 * time.Hour, 7*24*time.Hour + 8*time.Hour},
		{"Dugnad vår", "Vårdugnad for alle medlemmer", "Brygga", "dugnad", 14 * 24 * time.Hour, 14*24*time.Hour + 4*time.Hour},
		{"Sommerfest", "Sommeravslutning med grilling", "Klubbhuset", "social", 30 * 24 * time.Hour, 30*24*time.Hour + 6*time.Hour},
		{"Årsmøte 2026", "Ordinært årsmøte", "Klubbhuset", "agm", 60 * 24 * time.Hour, 60*24*time.Hour + 3*time.Hour},
	}
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

	fmt.Println("\ndone! you can now log in with:")
	fmt.Println("  admin:  admin@brygge.local  / admin123")
	fmt.Println("  member: member@brygge.local / member123")
}
