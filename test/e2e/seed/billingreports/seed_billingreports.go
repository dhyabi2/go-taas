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

// Command seed_billingreports seeds charge_records rows directly into the
// compose stack's PostgreSQL so the billing-reports e2e suite (feature-25)
// can exercise the populated states of the admin Billing Reports page
// (/admin/billing/reports) and the end-user Billing Reports page
// (/billing/reports) against the compose stack.
//
// The compose stack's taas-server is the only writer of charge_records
// through the charging pipeline, but the compose stack has no controller
// or inference service to produce them. This seed writes the rows directly
// into PostgreSQL (the billing-reports module is a read-only aggregation
// over charge_records, so seeding the table is the only way to populate
// the reports).
//
// Usage:
//
//	go run ./test/e2e/seed/billingreports/seed_billingreports.go -dsn "postgres://taas:taas@postgres:5432/taas?sslmode=disable" -org "org-e2e-br-..." -model "11111111-1111-1111-1111-111111111111" -key "33333333-3333-3333-3333-333333333333"
//
// The default DSN targets the compose network (service name "postgres").
package main

import (
	"database/sql"
	"flag"
	"log"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dsn := flag.String("dsn", "postgres://taas:taas@postgres:5432/taas?sslmode=disable", "PostgreSQL DSN")
	org := flag.String("org", "org-default", "organization that owns the charge records")
	model := flag.String("model", "11111111-1111-1111-1111-111111111111", "model id")
	otherOrg := flag.String("other-org", "", "a second org whose usage must NOT appear in the first org's view")
	clear := flag.Bool("clear", false, "delete all charge_records rows for the given org (for the empty-state e2e case)")
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
		if _, err := db.Exec(`DELETE FROM charge_records WHERE organization_id = $1`, *org); err != nil {
			log.Fatalf("clear charge_records: %v", err)
		}
		log.Printf("cleared charge_records for org %s", *org)
		return
	}

	// The unique group constraint is (api_key_id, model_id,
	// accelerator_type, period_start) WITHOUT organization_id, so the
	// same key+model+period_start across different orgs conflicts. Use a
	// unique key per org so each org's charge records never collide with
	// another org's, and delete the org's existing rows first so the seed
	// is idempotent across runs.
	// Always use a unique key per org so the group constraint never
	// collides across orgs.
	orgKey := "key-" + *org
	if _, err := db.Exec(`DELETE FROM charge_records WHERE organization_id = $1`, *org); err != nil {
		log.Fatalf("clear org charge_records: %v", err)
	}
	if *otherOrg != "" {
		if _, err := db.Exec(`DELETE FROM charge_records WHERE organization_id = $1`, *otherOrg); err != nil {
			log.Fatalf("clear other-org charge_records: %v", err)
		}
	}

	// Seed a few charge records for the org across the last 3 days so a
	// report over the last 7 days has data. Each row is one (key, model,
	// hour) group.
	now := time.Now().UTC()
	rows := []struct {
		org, key, model string
		daysAgo         int
		prompt, comp    int64
		amount          float64
	}{
		{*org, orgKey, *model, 0, 100, 200, 1.25},
		{*org, orgKey, *model, 1, 50, 100, 0.75},
		{*org, orgKey, *model, 2, 200, 400, 2.50},
	}
	for _, r := range rows {
		periodStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
			AddDate(0, 0, -r.daysAgo).Unix()
		if _, err := db.Exec(`
			INSERT INTO charge_records
				(id, organization_id, api_key_id, model_id, accelerator_type,
				 period_start, period_end, prompt_tokens, completion_tokens,
				 cached_tokens, reasoning_tokens, request_count, amount,
				 currency, tier_index, priced, charged_at)
			VALUES ($1, $2, $3, $4, 'default', $5, $6, $7, $8, 0, 0, 1, $9, 'USD', -1, true, now())`,
			uuid.NewString(), r.org, r.key, r.model,
			periodStart, periodStart+3600, r.prompt, r.comp, r.amount,
		); err != nil {
			log.Fatalf("seed charge: %v", err)
		}
	}

	// Seed a charge record for the other org so the tenant-scoped e2e
	// case can assert it never appears in the first org's report. The
	// other org uses a unique key so it never collides with the main
	// org's rows or a previous run's rows.
	if *otherOrg != "" {
		periodStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
		if _, err := db.Exec(`
			INSERT INTO charge_records
				(id, organization_id, api_key_id, model_id, accelerator_type,
				 period_start, period_end, prompt_tokens, completion_tokens,
				 cached_tokens, reasoning_tokens, request_count, amount,
				 currency, tier_index, priced, charged_at)
			VALUES ($1, $2, $3, $4, 'default', $5, $6, $7, $8, 0, 0, 1, $9, 'USD', -1, true, now())`,
			uuid.NewString(), *otherOrg, "key-"+*otherOrg, *model,
			periodStart, periodStart+3600, 999, 999, 9.99,
		); err != nil {
			log.Fatalf("seed other-org charge: %v", err)
		}
	}

	log.Printf("seeded charge_records for org %s", *org)
}
