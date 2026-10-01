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

// Command seed_session seeds a server-side session directly into Redis so
// the e2e suites can authenticate against the compose stack, which has no
// interactive IdP login path a headless browser can complete. The session
// is written with the exact structure the auth service's SessionStore
// produces (services/auth/session_store.go), so the realm guard and the
// session RPCs accept it.
//
// The e2e suites then set the matching token in the browser's localStorage
// (go-taas.user.session-token / go-taas.admin.session-token) and the
// frontend sends it as `Authorization: Bearer <token>`, which the realm
// guard validates against this Redis session.
//
// The seeded user is also added to the org's org_members table (role
// admin for the admin realm, member for the user realm) so that
// RoleGuard-gated admin RPCs (feature #10, #23) resolve the caller as a
// valid member. The user_id defaults to a deterministic UUID so the
// RoleGuard's org_members lookup (a UUID-typed column) never fails on a
// malformed id.
//
// Usage:
//
//	go run ./test/e2e/seed/session -redis "redis:6379" -realm user -org org-default
//
// The default Redis address targets the compose network (service name
// "redis"). The token is printed to stdout; pass it to the e2e helper.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
)

const sessionKeyPrefix = "taas:auth:session:"

// deterministicUserID returns a stable UUID for a realm so repeated seeds
// for the same realm reuse the same org_members row (idempotent).
func deterministicUserID(realm string) string {
	if realm == "admin" {
		return "11111111-1111-1111-1111-111111111111"
	}
	return "22222222-2222-2222-2222-222222222222"
}

func main() {
	redisAddr := flag.String("redis", "redis:6379", "Redis address (host:port)")
	realm := flag.String("realm", "user", "session realm: user or admin")
	org := flag.String("org", "org-default", "active organization id")
	userID := flag.String("user", "", "user id (default: deterministic UUID per realm)")
	username := flag.String("username", "e2e-user", "username")
	email := flag.String("email", "e2e@example.com", "email")
	roles := flag.String("roles", "admin", "comma-separated session roles")
	ttl := flag.Duration("ttl", 24*time.Hour, "session TTL")
	pgAddr := flag.String("pg", "postgres:5432", "PostgreSQL address (host:port)")
	pgUser := flag.String("pg-user", "taas", "PostgreSQL user")
	pgPass := flag.String("pg-pass", "taas", "PostgreSQL password")
	pgDB := flag.String("pg-db", "taas", "PostgreSQL database")
	noMember := flag.Bool("no-member", false, "skip adding the user to org_members (for permission-denied cases)")
	flag.Parse()

	if *realm != "user" && *realm != "admin" {
		log.Fatalf("realm must be 'user' or 'admin', got %q", *realm)
	}
	if *userID == "" {
		*userID = deterministicUserID(*realm)
	}

	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: *redisAddr})
	defer func() { _ = client.Close() }()
	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis ping: %v", err)
	}

	// Add the user to the org's org_members so RoleGuard-gated admin RPCs
	// resolve the caller as a valid member (feature #10, #23). Idempotent.
	// Skipped with -no-member so a suite can seed a permission-denied
	// (non-member) session.
	if !*noMember {
		pgDSN := fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", *pgUser, *pgPass, *pgAddr, *pgDB)
		if db, err := sql.Open("pgx", pgDSN); err == nil {
			memberRole := "member"
			if *realm == "admin" {
				memberRole = "admin"
			}
			_, err = db.ExecContext(ctx,
				`INSERT INTO org_members (organization_id, user_id, role, joined_at)
				 VALUES ($1, $2, $3, now())
				 ON CONFLICT (organization_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
				*org, *userID, memberRole)
			if err != nil {
				log.Printf("warn: add org member: %v", err)
			}
			_ = db.Close()
		} else {
			log.Printf("warn: open postgres: %v", err)
		}
	}

	sessionID := fmt.Sprintf("e2e-session-%d", time.Now().UnixNano())
	now := time.Now().Unix()
	expiresAt := now + int64(ttl.Seconds())

	roleList := []string{}
	for _, r := range strings.Split(*roles, ",") {
		if r = strings.TrimSpace(r); r != "" {
			roleList = append(roleList, r)
		}
	}
	rolesJSON, _ := json.Marshal(roleList)
	orgs, _ := json.Marshal([]string{*org})

	key := sessionKeyPrefix + sessionID
	if err := client.HSet(ctx, key, map[string]any{
		"user_id":         *userID,
		"username":        *username,
		"email":           *email,
		"roles":           string(rolesJSON),
		"accessible_orgs": string(orgs),
		"active_org":      *org,
		"expires_at":      expiresAt,
		"created_at":      now,
		"realm":           *realm,
	}).Err(); err != nil {
		log.Fatalf("hset session: %v", err)
	}
	if err := client.Expire(ctx, key, *ttl).Err(); err != nil {
		log.Fatalf("expire session: %v", err)
	}
	// The access token is stored under a sibling key; the realm guard only
	// reads the session hash, but the session RPCs may read the token key.
	if err := client.Set(ctx, key+":token", "e2e-access-token", *ttl).Err(); err != nil {
		log.Fatalf("set token: %v", err)
	}

	log.Printf("seeded %s-realm session %s (org %s, user %s, ttl %s)", *realm, sessionID, *org, *userID, *ttl)
	fmt.Println(sessionID)
}
