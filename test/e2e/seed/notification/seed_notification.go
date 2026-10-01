// Copyright 2025 The go-taas Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command seed_notification seeds notification rows directly into the
// compose stack's PostgreSQL so the notification-center e2e suite
// (feature #26) can exercise the populated states of the admin
// Notifications page (/admin/notifications) and the end-user
// Notifications page (/notifications) against the compose stack.
//
// The compose stack's notification module creates notifications
// asynchronously via the notification.events consumer, but the compose
// stack has no inference pipeline or billing pipeline to produce the
// events. This seed writes the rows directly into PostgreSQL (the
// notification module is a read-only consumer of the events, so seeding
// the tables is the only way to populate the inboxes).
//
// The seeded user ids must match the deterministic user ids the session
// seed (test/e2e/seed/session/seed_session.go) assigns: admin =
// 11111111-1111-1111-1111-111111111111, user =
// 22222222-2222-2222-2222-222222222222. The notification service scopes
// the inbox to the session's user id, so a notification is visible only
// when its user_id matches the session user.
//
// Usage:
//
//	go run ./test/e2e/seed/notification/seed_notification.go -dsn "postgres://taas:taas@postgres:5432/taas?sslmode=disable" -org "org-e2e-nc-..." -other-org "org-e2e-nc-b-..."
//
// The default DSN targets the compose network (service name "postgres").
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Deterministic session user ids (must match seed_session.go).
const (
	adminUserID = "11111111-1111-1111-1111-111111111111"
	userUserID  = "22222222-2222-2222-2222-222222222222"
)

func main() {
	dsn := flag.String("dsn", "postgres://taas:taas@postgres:5432/taas?sslmode=disable", "PostgreSQL DSN")
	org := flag.String("org", "org-default", "organization that owns the notifications")
	otherOrg := flag.String("other-org", "", "a second org whose notifications must NOT appear in the first org's view")
	clear := flag.Bool("clear", false, "delete all notification rows for the given org (for the empty-state e2e case)")
	flag.Parse()

	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		log.Fatalf("ping: %v", err)
	}

	if *clear {
		for _, t := range []string{"notifications", "notification_preferences", "notification_thresholds"} {
			if _, err := db.Exec(`DELETE FROM `+t+` WHERE organization_id = $1`, *org); err != nil { //nolint:gosec // fixed table-name allowlist
				log.Fatalf("clear %s: %v", t, err)
			}
		}
		log.Printf("cleared notification tables for org %s", *org)
		return
	}

	// Clear the org's existing rows first so the seed is idempotent across
	// runs.
	for _, t := range []string{"notifications", "notification_preferences", "notification_thresholds"} {
		if _, err := db.Exec(`DELETE FROM `+t+` WHERE organization_id = $1`, *org); err != nil { //nolint:gosec // fixed table-name allowlist
			log.Fatalf("clear %s: %v", t, err)
		}
	}
	if *otherOrg != "" {
		for _, t := range []string{"notifications", "notification_preferences", "notification_thresholds"} {
			if _, err := db.Exec(`DELETE FROM `+t+` WHERE organization_id = $1`, *otherOrg); err != nil { //nolint:gosec // fixed table-name allowlist
				log.Fatalf("clear other-org %s: %v", t, err)
			}
		}
	}

	now := time.Now().UTC()

	// ---- Admin surface notifications (platform orchestration events) ----
	// Two unread + one read for the admin user, so the inbox and the bell
	// badge have data. The admin event catalog is deployment.status_changed,
	// autoscaling.scaled, autoscaling.scale_to_zero.
	adminNotifs := []struct {
		eventType, title, body, severity, link string
		read                                   bool
		minutesAgo                             time.Duration
	}{
		{"deployment.status_changed", "Deployment failed", "service svc-a failed to deploy", "critical", "/admin/inference-services", false, 5},
		{"autoscaling.scaled", "Autoscaled to 10 replicas", "service svc-a scaled to 10 replicas", "warning", "/admin/autoscaling", false, 30},
		{"autoscaling.scale_to_zero", "Scaled to zero", "service svc-b scaled to zero", "info", "/admin/autoscaling", true, 120},
	}
	for _, n := range adminNotifs {
		insertNotification(db, *org, adminUserID, "admin", n.eventType, n.title, n.body, n.severity, n.link, n.read, now.Add(-time.Duration(n.minutesAgo)*time.Minute))
	}

	// ---- End-user surface notifications (tenant account events) ----
	// Two unread + one read for the user, so the inbox and the bell badge
	// have data. The user event catalog is billing.invoice_created,
	// billing.invoice_paid, billing.spend_limit_breached, billing.balance_low.
	userNotifs := []struct {
		eventType, title, body, severity, link string
		read                                   bool
		minutesAgo                             time.Duration
	}{
		{"billing.balance_low", "Balance low", "your balance is below 1000", "warning", "/billing", false, 5},
		{"billing.spend_limit_breached", "Spend limit breached", "spend exceeded 500 this period", "critical", "/billing", false, 40},
		{"billing.invoice_paid", "Invoice paid", "invoice INV-1001 was paid", "info", "/billing", true, 200},
	}
	for _, n := range userNotifs {
		insertNotification(db, *org, userUserID, "user", n.eventType, n.title, n.body, n.severity, n.link, n.read, now.Add(-time.Duration(n.minutesAgo)*time.Minute))
	}

	// ---- Other-org notifications (isolation case) ----
	// A notification owned by the other org's user surface must never
	// appear in the primary org's inbox. Use the same user id so the only
	// differentiator is the organization_id.
	if *otherOrg != "" {
		insertNotification(db, *otherOrg, userUserID, "user", "billing.balance_low", "Other org balance low", "other tenant's balance is low", "warning", "/billing", false, now.Add(-10*time.Minute))
	}

	// ---- Preferences ----
	// The primary org's user disables billing.invoice_created so the
	// preferences tab shows a non-default state (AC4/AC11). The admin user
	// keeps all enabled (default).
	insertPreference(db, *org, userUserID, "user", []string{
		"billing.invoice_paid", "billing.spend_limit_breached", "billing.balance_low",
	}, now)
	insertPreference(db, *org, adminUserID, "admin", []string{
		"deployment.status_changed", "autoscaling.scaled", "autoscaling.scale_to_zero",
	}, now)

	// ---- Thresholds ----
	// One enabled + one disabled threshold on each surface so the
	// thresholds tab has data and the enable/disable toggle is exercised.
	insertThreshold(db, *org, userUserID, "user", "Low balance alert", "balance_low", "lt", 1000, true, now)
	insertThreshold(db, *org, userUserID, "user", "High spend alert", "spend_limit", "gt", 500, false, now)
	insertThreshold(db, *org, adminUserID, "admin", "Replica spike alert", "autoscaling_replicas", "gt", 8, true, now)
	insertThreshold(db, *org, adminUserID, "admin", "Deploy failure alert", "deployment_failure", "gt", 0, false, now)

	log.Printf("seeded notification tables for org %s", *org)
}

func insertNotification(db *sql.DB, org, userID, surface, eventType, title, body, severity, link string, read bool, at time.Time) {
	if _, err := db.Exec(`
		INSERT INTO notifications
			(notification_id, organization_id, user_id, surface, event_type,
			 title, body, severity, read, data, link, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		uuid.NewString(), org, userID, surface, eventType,
		title, body, severity, read, `{"seeded":true}`, link, at); err != nil {
		log.Fatalf("seed notification: %v", err) //nolint:revive // seed helper exits on failure
	}
}

func insertPreference(db *sql.DB, org, userID, surface string, enabled []string, at time.Time) {
	json := "["
	for i, t := range enabled {
		if i > 0 {
			json += ","
		}
		json += fmt.Sprintf("%q", t)
	}
	json += "]"
	if _, err := db.Exec(`
		INSERT INTO notification_preferences
			(user_id, organization_id, surface, enabled_event_types, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET
			enabled_event_types = EXCLUDED.enabled_event_types,
			updated_at = EXCLUDED.updated_at`,
		userID, org, surface, json, at, at); err != nil {
		log.Fatalf("seed preference: %v", err) //nolint:revive // seed helper exits on failure
	}
}

func insertThreshold(db *sql.DB, org, userID, surface, name, metric, operator string, value float64, enabled bool, at time.Time) {
	if _, err := db.Exec(`
		INSERT INTO notification_thresholds
			(threshold_id, organization_id, user_id, surface, name, metric,
			 operator, value, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		uuid.NewString(), org, userID, surface, name, metric,
		operator, value, enabled, at, at); err != nil {
		log.Fatalf("seed threshold: %v", err) //nolint:revive // seed helper exits on failure
	}
}