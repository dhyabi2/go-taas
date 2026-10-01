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

// Command seed_costanalytics seeds charge_records rows directly into the
// compose stack's PostgreSQL so the cost-analytics-dashboard e2e suite
// (feature-29) can exercise the populated states of the admin Cost pages
// (/admin/cost, /admin/cost/:dimension/:value) and the end-user Cost
// pages (/cost, /cost/:dimension/:value) against the compose stack.
//
// The compose stack's taas-server is the only writer of charge_records
// through the charging pipeline, but the compose stack has no controller
// or inference service to produce them. This seed writes the rows
// directly into PostgreSQL (the cost-analytics module is a read-only
// aggregation over charge_records, so seeding the table is the only way
// to populate the dashboard).
//
// Usage:
//
//	go run ./test/e2e/seed/costanalytics/seed_costanalytics.go -dsn "postgres://taas:taas@postgres:5432/taas?sslmode=disable" -org "org-e2e-ca-..." -model "11111111-1111-1111-1111-111111111111" -key "33333333-3333-3333-3333-333333333333"
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
	key := flag.String("key", "", "api key id (default: derived from the org id so each org gets a unique key)")
	otherOrg := flag.String("other-org", "", "a second org whose usage must NOT appear in the first org's view")
	clear := flag.Bool("clear", false, "delete all charge_records rows for the given org (for the empty-state e2e case)")
	flag.Parse()

	// The charge_records unique group constraint is (api_key_id,
	// model_id, accelerator_type, period_start) WITHOUT organization_id,
	// so the same key+model+period across different orgs conflicts. Derive
	// a unique UUID per org from the org id so repeated seeds for
	// different orgs never collide (the api_keys.id column is a UUID).
	if *key == "" {
		*key = uuid.NewSHA1(uuid.NameSpaceOID, []byte("ca-"+*org)).String()
	}

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

	// Ensure the model and api_keys rows exist for name resolution.
	if _, err := db.Exec(`
		INSERT INTO models (id, name, description, created_at, updated_at)
		VALUES ($1, 'costanalytics-model', 'e2e cost-analytics model', now(), now())
		ON CONFLICT (id) DO NOTHING`, *model); err != nil {
		log.Fatalf("seed model: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO api_keys
			(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
		VALUES ($1, 'costanalytics-key', 'sk-', $3, 'seed', 'seed', $2, now())
		ON CONFLICT (id) DO NOTHING`, *key, *org, "seed-ca-"+*org); err != nil {
		log.Fatalf("seed key: %v", err)
	}

	// The unique group constraint is (api_key_id, model_id,
	// accelerator_type, period_start) WITHOUT organization_id, so use a
	// unique key per org and delete the org's existing rows first so the
	// seed is idempotent across runs. The charge_records api_key_id must
	// match the seeded key id so the api_key dimension resolves.
	if _, err := db.Exec(`DELETE FROM charge_records WHERE organization_id = $1`, *org); err != nil {
		log.Fatalf("clear org charge_records: %v", err)
	}
	if *otherOrg != "" {
		if _, err := db.Exec(`DELETE FROM charge_records WHERE organization_id = $1`, *otherOrg); err != nil {
			log.Fatalf("clear other-org charge_records: %v", err)
		}
	}

	now := time.Now().UTC()
	seedCharge := func(orgID, keyID, modelID string, daysAgo int, prompt, comp int64, amount float64) {
		periodStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
			AddDate(0, 0, -daysAgo).Unix()
		if _, err := db.Exec(`
			INSERT INTO charge_records
				(id, organization_id, api_key_id, model_id, accelerator_type,
				 period_start, period_end, prompt_tokens, completion_tokens,
				 cached_tokens, reasoning_tokens, request_count, amount,
				 currency, tier_index, priced, charged_at)
			VALUES ($1, $2, $3, $4, 'default', $5, $6, $7, $8, 0, 0, 1, $9, 'USD', -1, true, now())`,
			uuid.NewString(), orgID, keyID, modelID,
			periodStart, periodStart+3600, prompt, comp, amount,
		); err != nil {
			log.Fatalf("seed charge: %v", err)
		}
	}

	// Seed a few charge records for the org across the last 3 days so a
	// report over the last 7 days has data. Each row is one (key, model,
	// hour) group.
	seedCharge(*org, *key, *model, 0, 100, 200, 1.25)
	seedCharge(*org, *key, *model, 1, 50, 100, 0.75)
	seedCharge(*org, *key, *model, 2, 200, 400, 2.50)

	// A second model for the dimension breakdown.
	modelB := "22222222-2222-2222-2222-222222222222"
	if _, err := db.Exec(`
		INSERT INTO models (id, name, description, created_at, updated_at)
		VALUES ($1, 'costanalytics-model-b', 'e2e cost-analytics model B', now(), now())
		ON CONFLICT (id) DO NOTHING`, modelB); err != nil {
		log.Fatalf("seed model B: %v", err)
	}
	seedCharge(*org, *key, modelB, 0, 50, 100, 0.50)

	// A second org's charge records: must NOT appear in the first org's
	// end-user view (AC4/AC10 tenant scoping).
	if *otherOrg != "" {
		seedCharge(*otherOrg, uuid.NewSHA1(uuid.NameSpaceOID, []byte("ca-"+*otherOrg)).String(), *model, 0, 999, 999, 9.99)
	}

	log.Printf("seeded charge_records for org %s (model %s, key %s)", *org, *model, *key)
}