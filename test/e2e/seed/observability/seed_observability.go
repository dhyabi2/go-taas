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

// Command seed_observability seeds request_logs rows directly into the
// compose stack's PostgreSQL so the model-observability e2e suite
// (feature #24) can exercise the populated states of the admin
// Observability pages (/admin/observability, /admin/observability/models/
// :modelId) and the end-user Model Observability page
// (/models/:modelId/observability) against the compose stack.
//
// The compose stack's taas-server is the only writer of request_logs
// through the inference pipeline, but the compose stack has no controller
// or inference service to produce them. This seed writes the rows directly
// into PostgreSQL (the observability module is a read-only aggregation over
// request_logs, so seeding the table is the only way to populate the
// dashboards).
//
// Usage:
//
//	go run ./test/e2e/seed/observability/seed_observability.go -dsn "postgres://taas:taas@postgres:5432/taas?sslmode=disable" -org "org-e2e-obs-..." -model "11111111-1111-1111-1111-111111111111" -key "33333333-3333-3333-3333-333333333333"
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
	key := flag.String("key", "33333333-3333-3333-3333-333333333333", "api key id")
	otherOrg := flag.String("other-org", "", "a second org whose usage must NOT appear in the first org's view")
	otherKey := flag.String("other-key", "44444444-4444-4444-4444-444444444444", "api key id for the other org")
	clear := flag.Bool("clear", false, "delete all request_logs rows for the given org (for the empty-state e2e case)")
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
		if _, err := db.Exec(`DELETE FROM request_logs WHERE organization_id = $1`, *org); err != nil {
			log.Fatalf("clear request_logs: %v", err)
		}
		log.Printf("cleared request_logs for org %s", *org)
		return
	}

	// Ensure the model and api_keys rows exist for name resolution (AD3).
	if _, err := db.Exec(`
		INSERT INTO models (id, name, description, created_at, updated_at)
		VALUES ($1, 'observability-model', 'e2e observability model', now(), now())
		ON CONFLICT (id) DO NOTHING`, *model); err != nil {
		log.Fatalf("seed model: %v", err)
	}
	// api_keys requires the auth columns (prefix, lookup_hash, salt,
	// salted_hash, organization_id); the observability module resolves the
	// display name by id (AD3). lookup_hash is unique, so each key gets a
	// distinct value.
	if _, err := db.Exec(`
		INSERT INTO api_keys
			(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
		VALUES ($1, 'observability-key', 'sk-', 'seed-key-a', 'seed', 'seed', $2, now())
		ON CONFLICT (id) DO NOTHING`, *key, *org); err != nil {
		log.Fatalf("seed key: %v", err)
	}

	// Seed request logs across the last 3 hours so the default 24h range
	// shows data. Two models for the fleet table; a mix of success and
	// error statuses; varied latencies and token counts.
	now := time.Now().UTC().Truncate(time.Hour)
	seq := 0
	seed := func(orgID, keyID, modelID string, at time.Time, latencyMs int64, status string, prompt, completion int64) {
		seq++
		// request_id is the idempotency key (unique index); include the org
		// and a per-run random component so repeated seeds for different orgs
		// never collide (the request-log retention runner is the only deleter).
		reqID := fmt.Sprintf("req-%s-%s-%d-%d", orgID, modelID, at.UnixNano(), seq)
		if _, err := db.Exec(`
			INSERT INTO request_logs
				(id, request_id, organization_id, api_key_id, model_id,
				 prompt_tokens, completion_tokens, cached_tokens, reasoning_tokens,
				 latency_ms, status, error, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 0, $8, $9, $10, $11)
			ON CONFLICT (request_id) DO NOTHING`,
			uuid.NewString(), reqID, orgID, keyID, modelID,
			prompt, completion, latencyMs, status, statusError(status), at); err != nil {
			log.Fatalf("seed request_log: %v", err)
		}
	}

	// Primary org: model A with several requests (some errors), model B with
	// a couple of requests.
	seed(*org, *key, *model, now.Add(-3*time.Hour), 100, "success", 10, 20)
	seed(*org, *key, *model, now.Add(-3*time.Hour), 200, "error", 10, 20)
	seed(*org, *key, *model, now.Add(-2*time.Hour), 150, "success", 15, 30)
	seed(*org, *key, *model, now.Add(-1*time.Hour), 300, "success", 20, 40)
	seed(*org, *key, *model, now.Add(-1*time.Hour), 250, "error", 20, 40)
	seed(*org, *key, *model, now.Add(-30*time.Minute), 120, "success", 12, 24)

	// A second model for the fleet table.
	modelB := "22222222-2222-2222-2222-222222222222"
	if _, err := db.Exec(`
		INSERT INTO models (id, name, description, created_at, updated_at)
		VALUES ($1, 'observability-model-b', 'e2e observability model B', now(), now())
		ON CONFLICT (id) DO NOTHING`, modelB); err != nil {
		log.Fatalf("seed model B: %v", err)
	}
	seed(*org, *key, modelB, now.Add(-2*time.Hour), 80, "success", 5, 10)
	seed(*org, *key, modelB, now.Add(-1*time.Hour), 90, "success", 6, 12)

	// A second key for the per-key breakdown on model A.
	keyB := "55555555-5555-5555-5555-555555555555"
	if _, err := db.Exec(`
		INSERT INTO api_keys
			(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
		VALUES ($1, 'observability-key-b', 'sk-', 'seed-key-b', 'seed', 'seed', $2, now())
		ON CONFLICT (id) DO NOTHING`, keyB, *org); err != nil {
		log.Fatalf("seed key B: %v", err)
	}
	seed(*org, keyB, *model, now.Add(-2*time.Hour), 400, "success", 30, 60)

	// A second org's usage of the same model: must NOT appear in the first
	// org's end-user view (AC4/AC10 tenant scoping).
	if *otherOrg != "" {
		if _, err := db.Exec(`
			INSERT INTO api_keys
				(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
			VALUES ($1, 'observability-other-key', 'sk-', 'seed-key-other', 'seed', 'seed', $2, now())
			ON CONFLICT (id) DO NOTHING`, *otherKey, *otherOrg); err != nil {
			log.Fatalf("seed other key: %v", err)
		}
		seed(*otherOrg, *otherKey, *model, now.Add(-2*time.Hour), 999, "success", 50, 100)
	}

	log.Printf("seeded request_logs for org %s (model %s, key %s)", *org, *model, *key)
}

func statusError(status string) string {
	if status == "error" {
		return "upstream_error"
	}
	return ""
}