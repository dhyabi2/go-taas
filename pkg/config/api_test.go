package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestArgon2ParamsDefaults(t *testing.T) {
	// Zero values fall back to the shipped defaults at the use site.
	p := Argon2Params{}
	d := p.WithDefaults()
	if d.Algorithm != "argon2id" {
		t.Fatalf("default algorithm: %s", d.Algorithm)
	}
	if d.Time != 1 {
		t.Fatalf("default time: %d", d.Time)
	}
	if d.MemoryMiB != 64 {
		t.Fatalf("default memoryMiB: %d", d.MemoryMiB)
	}
	if d.Parallelism != 1 {
		t.Fatalf("default parallelism: %d", d.Parallelism)
	}
	// Explicit values are preserved.
	p = Argon2Params{Algorithm: "argon2id", Time: 2, MemoryMiB: 128, Parallelism: 2}
	d = p.WithDefaults()
	if d.Time != 2 || d.MemoryMiB != 128 || d.Parallelism != 2 {
		t.Fatalf("explicit values not preserved: %+v", d)
	}
}

func TestValidateAuthNegativeValues(t *testing.T) {
	cfg := &Configuration{}
	cfg.Auth.APIKeyHash.Time = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative argon2 time must fail validation")
	}
	cfg = &Configuration{}
	cfg.Auth.APIKeyHash.MemoryMiB = -64
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative argon2 memory must fail validation")
	}
	cfg = &Configuration{}
	cfg.Auth.APIKeyHash.Parallelism = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative argon2 parallelism must fail validation")
	}
	cfg = &Configuration{}
	cfg.Auth.APIKeyCacheTTL = -time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative apiKeyCacheTTL must fail validation")
	}
	// Valid config passes.
	cfg = &Configuration{}
	cfg.Billing.Currency = "USD"
	cfg.Auth.APIKeyHash = Argon2Params{Algorithm: "argon2id", Time: 1, MemoryMiB: 64, Parallelism: 1}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid auth config rejected: %v", err)
	}
}

func TestParseConfigsArgon2Section(t *testing.T) {
	path := writeTempConfig(t, `
auth:
  sessionTTL: 24h
  apiKeyCacheTTL: 30s
  apiKeyHash:
    algorithm: argon2id
    time: 1
    memoryMiB: 64
    parallelism: 1
`)
	ParseConfigs(path)
	cfg := GetConfig()
	if cfg.Auth.APIKeyCacheTTL != 30*time.Second {
		t.Fatalf("apiKeyCacheTTL: %v", cfg.Auth.APIKeyCacheTTL)
	}
	if cfg.Auth.APIKeyHash.Algorithm != "argon2id" {
		t.Fatalf("algorithm: %s", cfg.Auth.APIKeyHash.Algorithm)
	}
	if cfg.Auth.APIKeyHash.Time != 1 || cfg.Auth.APIKeyHash.MemoryMiB != 64 || cfg.Auth.APIKeyHash.Parallelism != 1 {
		t.Fatalf("argon2 params: %+v", cfg.Auth.APIKeyHash)
	}
}

func TestValidateModelAuthCacheTTL(t *testing.T) {
	cfg := &Configuration{}
	cfg.Billing.Currency = "USD"
	cfg.Model.Auth.CacheTTL = -time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative model.auth.cacheTTL must fail validation")
	}
}

// TestParseConfigsModelAuthSection proves the key reaches the loaded
// configuration: a key that is absent from the shipped YAML is silently
// ignored by the env override, so it must be parsed from a file as well.
func TestParseConfigsModelAuthSection(t *testing.T) {
	path := writeTempConfig(t, `
model:
  auth:
    cacheTTL: 7s
`)
	ParseConfigs(path)
	cfg := GetConfig()
	if cfg.Model.Auth.CacheTTL != 7*time.Second {
		t.Fatalf("model.auth.cacheTTL: %v", cfg.Model.Auth.CacheTTL)
	}
}

// TestParseConfigsModelAuthSectionDefault pins the default: an omitted
// key (or a config that predates the section) gets 5s, not 0.
func TestParseConfigsModelAuthSectionDefault(t *testing.T) {
	path := writeTempConfig(t, "log:\n  level: info\n")
	ParseConfigs(path)
	if got := GetConfig().Model.Auth.CacheTTL; got != 5*time.Second {
		t.Fatalf("default model.auth.cacheTTL: %v", got)
	}
}

// TestShippedConfigModelAuth pins the shipped configuration file itself:
// the key must be present with a valid, non-zero duration, because a
// malformed value breaks config loading and a zero value silently falls
// back to the default.
func TestShippedConfigModelAuth(t *testing.T) {
	ParseConfigs(filepath.Join("..", "..", "configs", "config.yaml"))
	cfg := GetConfig()
	if cfg == nil {
		t.Fatal("GetConfig returned nil")
	}
	if cfg.Model.Auth.CacheTTL != 5*time.Second {
		t.Fatalf("shipped model.auth.cacheTTL = %v, want 5s", cfg.Model.Auth.CacheTTL)
	}
}
