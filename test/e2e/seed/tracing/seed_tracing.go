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

// Command seed_tracing seeds traces and trace_spans rows directly into the
// compose stack's PostgreSQL so the request-tracing e2e suite (feature-27)
// can exercise the populated states of the admin Traces pages
// (/admin/traces, /admin/traces/:traceId) and the end-user Traces pages
// (/traces, /traces/:traceId) against the compose stack.
//
// The compose stack's taas-server is the only writer of traces through the
// inference pipeline, but the compose stack has no controller or inference
// service to produce them. This seed writes the rows directly into
// PostgreSQL (the tracing module is a read-only query over traces, so
// seeding the tables is the only way to populate the explorer).
//
// Usage:
//
//	go run ./test/e2e/seed/tracing/seed_tracing.go -dsn "postgres://taas:taas@postgres:5432/taas?sslmode=disable" -org "org-e2e-tr-..." -model "11111111-1111-1111-1111-111111111111" -key "33333333-3333-3333-3333-333333333333"
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
	org := flag.String("org", "org-default", "organization that owns the traces")
	model := flag.String("model", "aaaaaaaa-1111-1111-1111-111111111111", "model id")
	key := flag.String("key", "bbbbbbbb-3333-3333-3333-333333333333", "api key id")
	otherOrg := flag.String("other-org", "", "a second org whose traces must NOT appear in the first org's view")
	otherKey := flag.String("other-key", "cccccccc-4444-4444-4444-444444444444", "api key id for the other org")
	clear := flag.Bool("clear", false, "delete all traces rows for the given org (for the empty-state e2e case)")
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
		if _, err := db.Exec(`DELETE FROM trace_spans WHERE trace_id IN (SELECT trace_id FROM traces WHERE organization_id = $1)`, *org); err != nil {
			log.Fatalf("clear trace_spans: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM traces WHERE organization_id = $1`, *org); err != nil {
			log.Fatalf("clear traces: %v", err)
		}
		log.Printf("cleared traces for org %s", *org)
		return
	}

	// Ensure the model and api_keys rows exist for name resolution (AD3).
	if _, err := db.Exec(`
		INSERT INTO models (id, name, description, created_at, updated_at)
		VALUES ($1, 'tracing-model', 'e2e tracing model', now(), now())
		ON CONFLICT (id) DO NOTHING`, *model); err != nil {
		log.Fatalf("seed model: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO api_keys
			(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
		VALUES ($1, 'tracing-key', 'sk-', 'seed-trace-key-a', 'seed', 'seed', $2, now())
		ON CONFLICT (id) DO NOTHING`, *key, *org); err != nil {
		log.Fatalf("seed key: %v", err)
	}

	// Seed traces across the last 3 hours so the default 24h range shows
	// data. A mix of success and error statuses; varied latencies and
	// token counts; each trace carries a gateway + inference span pair.
	now := time.Now().UTC().Truncate(time.Hour)
	seq := 0
	seed := func(orgID, keyID, modelID, serviceID, status string, at time.Time, total, ttft, gen int64) string {
		seq++
		// trace_id is the idempotency key (unique index); include the org
		// and a per-run random component so repeated seeds for different
		// orgs never collide (the retention runner is the only deleter).
		traceID := fmt.Sprintf("tr-%s-%s-%d-%d", orgID, modelID, at.UnixNano(), seq)
		if _, err := db.Exec(`
			INSERT INTO traces
				(id, trace_id, organization_id, api_key_id, model_id, service_id,
				 status, error, total_latency_ms, ttft_ms, generation_ms,
				 prompt_tokens, completion_tokens, cached_tokens, reasoning_tokens,
				 created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (trace_id) DO NOTHING`,
			uuid.NewString(), traceID, orgID, keyID, modelID, serviceID,
			status, statusError(status), total, ttft, gen,
			10, 20, 0, 0, at); err != nil {
			log.Fatalf("seed trace: %v", err)
		}
		// Gateway span (root) + inference span (child).
		if _, err := db.Exec(`
			INSERT INTO trace_spans
				(id, trace_id, parent_span_id, name, kind, start_offset_ms,
				 duration_ms, status, error, attributes)
			VALUES ($1, $2, NULL, 'gateway', 'server', 0, $3, $4, $5, $6)`,
			uuid.NewString(), traceID, total, status, statusError(status),
			fmt.Sprintf(`{"model_id": %q, "service_id": %q}`, modelID, serviceID)); err != nil {
			log.Fatalf("seed gateway span: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO trace_spans
				(id, trace_id, parent_span_id, name, kind, start_offset_ms,
				 duration_ms, status, error, attributes)
			VALUES ($1, $2, $3, 'inference', 'internal', 0, $4, $5, $6, $7)`,
			uuid.NewString(), traceID, traceID, total, status, statusError(status),
			fmt.Sprintf(`{"model_id": %q, "service_id": %q}`, modelID, serviceID)); err != nil {
			log.Fatalf("seed inference span: %v", err)
		}
		return traceID
	}

	// Primary org: model A with several traces (some errors), model B with
	// a couple of traces.
	seed(*org, *key, *model, "svc-1", "success", now.Add(-3*time.Hour), 1000, 400, 600)
	seed(*org, *key, *model, "svc-1", "error", now.Add(-3*time.Hour), 2000, 800, 1200)
	seed(*org, *key, *model, "svc-1", "success", now.Add(-2*time.Hour), 1500, 500, 1000)
	seed(*org, *key, *model, "svc-1", "success", now.Add(-1*time.Hour), 3000, 1200, 1800)
	seed(*org, *key, *model, "svc-1", "error", now.Add(-1*time.Hour), 2500, 900, 1600)
	seed(*org, *key, *model, "svc-1", "success", now.Add(-30*time.Minute), 1200, 300, 900)

	// A second model for the fleet table.
	modelB := "aaaaaaaa-2222-2222-2222-222222222222"
	if _, err := db.Exec(`
		INSERT INTO models (id, name, description, created_at, updated_at)
		VALUES ($1, 'tracing-model-b', 'e2e tracing model B', now(), now())
		ON CONFLICT (id) DO NOTHING`, modelB); err != nil {
		log.Fatalf("seed model B: %v", err)
	}
	seed(*org, *key, modelB, "svc-2", "success", now.Add(-2*time.Hour), 800, 200, 600)
	seed(*org, *key, modelB, "svc-2", "success", now.Add(-1*time.Hour), 900, 250, 650)

	// A second key for the per-key filter on model A.
	keyB := "bbbbbbbb-5555-5555-5555-555555555555"
	if _, err := db.Exec(`
		INSERT INTO api_keys
			(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
		VALUES ($1, 'tracing-key-b', 'sk-', 'seed-trace-key-b', 'seed', 'seed', $2, now())
		ON CONFLICT (id) DO NOTHING`, keyB, *org); err != nil {
		log.Fatalf("seed key B: %v", err)
	}
	seed(*org, keyB, *model, "svc-1", "success", now.Add(-2*time.Hour), 4000, 1500, 2500)

	// A second org's traces of the same model: must NOT appear in the first
	// org's end-user view (AC4/AC10 tenant scoping).
	if *otherOrg != "" {
		if _, err := db.Exec(`
			INSERT INTO api_keys
				(id, name, prefix, lookup_hash, salt, salted_hash, organization_id, created_at)
			VALUES ($1, 'tracing-other-key', 'sk-', 'seed-trace-key-other', 'seed', 'seed', $2, now())
			ON CONFLICT (id) DO NOTHING`, *otherKey, *otherOrg); err != nil {
			log.Fatalf("seed other key: %v", err)
		}
		seed(*otherOrg, *otherKey, *model, "svc-9", "success", now.Add(-2*time.Hour), 9999, 4000, 5999)
	}

	log.Printf("seeded traces for org %s (model %s, key %s)", *org, *model, *key)
}

func statusError(status string) string {
	if status == "error" {
		return "upstream_error"
	}
	return ""
}
