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

// Command seed_erroranalysis seeds request_logs rows (with error
// statuses) directly into the compose stack's PostgreSQL so the
// error-analysis e2e suite (feature-31) can exercise the populated states
// of the admin Error Analysis pages (/admin/errors, /admin/errors/
// :errorCode) and the end-user Error Analysis pages (/errors,
// /errors/:errorCode) against the compose stack.
//
// The compose stack's taas-server is the only writer of request_logs
// through the inference pipeline, but the compose stack has no controller
// or inference service to produce them. This seed writes the rows
// directly into PostgreSQL (the error-analysis module is a read-only
// aggregation over request_logs, so seeding the table is the only way to
// populate the dashboard).
//
// Usage:
//
//	go run ./test/e2e/seed/erroranalysis/seed_erroranalysis.go -dsn "postgres://taas:taas@postgres:5432/taas?sslmode=disable" -org "org-e2e-ea-..." -model "11111111-1111-1111-1111-111111111111" -key "33333333-3333-3333-3333-333333333333"
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
	org := flag.String("org", "org-default", "organization that owns the request logs")
	model := flag.String("model", "11111111-1111-1111-1111-111111111111", "model id")
	key := flag.String("key", "", "api key id (default: derived from the org id so each org gets a unique key)")
	otherOrg := flag.String("other-org", "", "a second org whose errors must NOT appear in the first org's view")
	clear := flag.Bool("clear", false, "delete all request_logs rows for the given org (for the empty-state e2e case)")
	flag.Parse()

	// Derive a unique UUID per org from the org id so repeated seeds for
	// different orgs never collide (the api_keys.id column is a UUID and
	// the lookup_hash is unique).
	if *key == "" {
		*key = uuid.NewSHA1(uuid.NameSpaceOID, []byte("ea-"+*org)).String()
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
		log.Printf("cleared request_logs for org %s", *org)
		return
	}

	// Ensure the model and api_keys rows exist for name resolution.
	if _, err := db.Exec(`
		INSERT INTO models (id, name, description, created_at, updated_at)
		VALUES ($1, 'erroranalysis-model', 'e2e error-analysis model', now(), now())
		ON CONFLICT (id) DO NOTHING`, *model); err != nil {
		log.Fatalf("seed model: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO api_keys
			(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
		VALUES ($1, 'erroranalysis-key', 'sk-', $3, 'seed', 'seed', $2, now())
		ON CONFLICT (id) DO NOTHING`, *key, *org, "seed-ea-"+*org); err != nil {
		log.Fatalf("seed key: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Hour)
	seq := 0
	seed := func(orgID, keyID, modelID string, at time.Time, latencyMs int64, status string, prompt, completion int64, errCode string) {
		seq++
		reqID := fmt.Sprintf("req-ea-%s-%s-%d-%d", orgID, modelID, at.UnixNano(), seq)
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

	// Primary org: a mix of errors and successes so the error-rate trend
	// and top-causes ranking have data. rate_limit_exceeded is the top
	// cause (3 errors), upstream_error second (2 errors).
	seed(*org, *key, *model, now.Add(-3*time.Hour), 100, "error", 10, 20, "rate_limit_exceeded")
	seed(*org, *key, *model, now.Add(-3*time.Hour), 100, "error", 10, 20, "rate_limit_exceeded")
	seed(*org, *key, *model, now.Add(-3*time.Hour), 100, "success", 10, 20, "")
	seed(*org, *key, *model, now.Add(-2*time.Hour), 200, "error", 10, 20, "upstream_error")
	seed(*org, *key, *model, now.Add(-2*time.Hour), 150, "success", 15, 30, "")
	seed(*org, *key, *model, now.Add(-1*time.Hour), 250, "error", 20, 40, "rate_limit_exceeded")
	seed(*org, *key, *model, now.Add(-1*time.Hour), 300, "success", 20, 40, "")
	seed(*org, *key, *model, now.Add(-30*time.Minute), 120, "error", 12, 24, "upstream_error")
	seed(*org, *key, *model, now.Add(-30*time.Minute), 120, "success", 12, 24, "")

	// A second org's errors: must NOT appear in the first org's end-user
	// view (AC4/AC10 tenant scoping).
	if *otherOrg != "" {
		otherKey := uuid.NewSHA1(uuid.NameSpaceOID, []byte("ea-"+*otherOrg)).String()
		if _, err := db.Exec(`
			INSERT INTO api_keys
				(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
			VALUES ($1, 'erroranalysis-other-key', 'sk-', $3, 'seed', 'seed', $2, now())
			ON CONFLICT (id) DO NOTHING`, otherKey, *otherOrg, "seed-ea-other-"+*otherOrg); err != nil {
			log.Fatalf("seed other key: %v", err)
		}
		seed(*otherOrg, otherKey, *model, now.Add(-2*time.Hour), 999, "error", 50, 100, "rate_limit_exceeded")
	}

	log.Printf("seeded request_logs for org %s (model %s, key %s)", *org, *model, *key)
}