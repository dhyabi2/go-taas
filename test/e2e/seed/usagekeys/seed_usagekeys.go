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

// Command seed_usagekeys seeds request_logs and charge_records rows
// directly into the compose stack's PostgreSQL so the api-key-usage-
// analytics e2e suite (feature-28) can exercise the populated states of
// the admin Usage Keys pages (/admin/usage/keys, /admin/usage/keys/
// :apiKeyId) and the end-user Usage Keys pages (/usage/keys,
// /usage/keys/:apiKeyId) against the compose stack.
//
// The compose stack's taas-server is the only writer of request_logs and
// charge_records through the inference/charging pipelines, but the
// compose stack has no controller or inference service to produce them.
// This seed writes the rows directly into PostgreSQL (the usage-keys
// module is a read-only aggregation over request_logs and charge_records,
// so seeding the tables is the only way to populate the analytics).
//
// Usage:
//
//	go run ./test/e2e/seed/usagekeys/seed_usagekeys.go -dsn "postgres://taas:taas@postgres:5432/taas?sslmode=disable" -org "org-e2e-uk-..." -model "11111111-1111-1111-1111-111111111111" -key "33333333-3333-3333-3333-333333333333"
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

func main() {
	dsn := flag.String("dsn", "postgres://taas:taas@postgres:5432/taas?sslmode=disable", "PostgreSQL DSN")
	org := flag.String("org", "org-default", "organization that owns the request logs and charge records")
	model := flag.String("model", "11111111-1111-1111-1111-111111111111", "model id")
	key := flag.String("key", "", "api key id (default: derived from the org id so each org gets a unique key)")
	otherOrg := flag.String("other-org", "", "a second org whose usage must NOT appear in the first org's view")
	otherKey := flag.String("other-key", "", "api key id for the other org (default: derived from the other org id)")
	clear := flag.Bool("clear", false, "delete all request_logs and charge_records rows for the given org (for the empty-state e2e case)")
	flag.Parse()

	// The charge_records unique group constraint is (api_key_id,
	// model_id, accelerator_type, period_start) WITHOUT organization_id,
	// so the same key+model+period across different orgs conflicts. Derive
	// a unique UUID per org from the org id so repeated seeds for
	// different orgs never collide (the api_keys.id column is a UUID).
	if *key == "" {
		*key = uuid.NewSHA1(uuid.NameSpaceOID, []byte("uk-"+*org)).String()
	}
	if *otherKey == "" {
		*otherKey = uuid.NewSHA1(uuid.NameSpaceOID, []byte("uk-"+*otherOrg)).String()
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
		if _, err := db.Exec(`DELETE FROM request_logs WHERE organization_id = $1`, *org); err != nil {
			log.Fatalf("clear request_logs: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM charge_records WHERE organization_id = $1`, *org); err != nil {
			log.Fatalf("clear charge_records: %v", err)
		}
		log.Printf("cleared request_logs and charge_records for org %s", *org)
		return
	}

	// Ensure the model and api_keys rows exist for name resolution (AD3).
	if _, err := db.Exec(`
		INSERT INTO models (id, name, description, created_at, updated_at)
		VALUES ($1, 'usagekeys-model', 'e2e usage-keys model', now(), now())
		ON CONFLICT (id) DO NOTHING`, *model); err != nil {
		log.Fatalf("seed model: %v", err)
	}
	// api_keys requires the auth columns (prefix, lookup_hash, salt,
	// salted_hash, organization_id); the usage-keys module resolves the
	// display name by id (AD3). lookup_hash is unique, so each key gets a
	// distinct value derived from the org id.
	if _, err := db.Exec(`
		INSERT INTO api_keys
			(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
		VALUES ($1, 'usagekeys-key', 'sk-', $3, 'seed', 'seed', $2, now())
		ON CONFLICT (id) DO NOTHING`, *key, *org, "seed-uk-"+*org); err != nil {
		log.Fatalf("seed key: %v", err)
	}

	// Seed request logs across the last 3 hours so the default 24h range
	// shows data. A mix of success and error statuses; varied latencies
	// and token counts.
	now := time.Now().UTC().Truncate(time.Hour)
	seq := 0
	seed := func(orgID, keyID, modelID string, at time.Time, latencyMs int64, status string, prompt, completion int64, errCode string) {
		seq++
		reqID := fmt.Sprintf("req-uk-%s-%s-%d-%d", orgID, modelID, at.UnixNano(), seq)
		if _, err := db.Exec(`
			INSERT INTO request_logs
				(id, request_id, organization_id, api_key_id, model_id,
				 prompt_tokens, completion_tokens, cached_tokens, reasoning_tokens,
				 latency_ms, status, error, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 0, $8, $9, $10, $11)
			ON CONFLICT (request_id) DO NOTHING`,
			uuid.NewString(), reqID, orgID, keyID, modelID,
			prompt, completion, latencyMs, status, errCode, at); err != nil {
			log.Fatalf("seed request_log: %v", err)
		}
	}

	// Primary org: the main key with several requests (some errors).
	seed(*org, *key, *model, now.Add(-3*time.Hour), 100, "success", 10, 20, "")
	seed(*org, *key, *model, now.Add(-3*time.Hour), 200, "error", 10, 20, "rate_limit_exceeded")
	seed(*org, *key, *model, now.Add(-2*time.Hour), 150, "success", 15, 30, "")
	seed(*org, *key, *model, now.Add(-1*time.Hour), 300, "success", 20, 40, "")
	seed(*org, *key, *model, now.Add(-1*time.Hour), 250, "error", 20, 40, "upstream_error")
	seed(*org, *key, *model, now.Add(-30*time.Minute), 120, "success", 12, 24, "")

	// A second key for the per-key ranking (higher cost).
	keyB := uuid.NewSHA1(uuid.NameSpaceOID, []byte("uk-b-"+*org)).String()
	if _, err := db.Exec(`
		INSERT INTO api_keys
			(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
		VALUES ($1, 'usagekeys-key-b', 'sk-', $3, 'seed', 'seed', $2, now())
		ON CONFLICT (id) DO NOTHING`, keyB, *org, "seed-uk-b-"+*org); err != nil {
		log.Fatalf("seed key B: %v", err)
	}
	seed(*org, keyB, *model, now.Add(-2*time.Hour), 400, "success", 30, 60, "")
	seed(*org, keyB, *model, now.Add(-1*time.Hour), 350, "success", 25, 50, "")

	// Seed charge records for the org so the per-key cost is populated.
	// The unique group constraint is (api_key_id, model_id,
	// accelerator_type, period_start) WITHOUT organization_id, so use a
	// unique key per org and delete the org's existing rows first so the
	// seed is idempotent across runs. The charge_records api_key_id must
	// match the request_logs api_key_id for the per-key cost to be
	// attributed (AD3).
	if _, err := db.Exec(`DELETE FROM charge_records WHERE organization_id = $1`, *org); err != nil {
		log.Fatalf("clear org charge_records: %v", err)
	}
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
	seedCharge(*org, *key, *model, 0, 100, 200, 1.25)
	seedCharge(*org, *key, *model, 1, 50, 100, 0.75)
	seedCharge(*org, *key, *model, 2, 200, 400, 2.50)
	seedCharge(*org, keyB, *model, 0, 30, 60, 3.00)

	// A second org's usage of the same model: must NOT appear in the first
	// org's end-user view (AC4/AC10 tenant scoping).
	if *otherOrg != "" {
		// Delete the other org's charge records first so the seed is
		// idempotent across runs (the group constraint is key+model+period
		// without org, so a leftover row from a previous run conflicts).
		if _, err := db.Exec(`DELETE FROM charge_records WHERE organization_id = $1`, *otherOrg); err != nil {
			log.Fatalf("clear other-org charge_records: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO api_keys
				(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
			VALUES ($1, 'usagekeys-other-key', 'sk-', $3, 'seed', 'seed', $2, now())
			ON CONFLICT (id) DO NOTHING`, *otherKey, *otherOrg, "seed-uk-other-"+*otherOrg); err != nil {
			log.Fatalf("seed other key: %v", err)
		}
		seed(*otherOrg, *otherKey, *model, now.Add(-2*time.Hour), 999, "success", 50, 100, "")
		seedCharge(*otherOrg, *otherKey, *model, 0, 999, 999, 9.99)
	}

	log.Printf("seeded request_logs and charge_records for org %s (model %s, key %s)", *org, *model, *key)
}