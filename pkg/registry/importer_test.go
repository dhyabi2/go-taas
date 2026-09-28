package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseReference(t *testing.T) {
	cases := []struct {
		in      string
		host    string
		repo    string
		tag     string
		wantErr bool
	}{
		{"ghcr.io/go-taas/vllm:v0.6.3", "ghcr.io", "go-taas/vllm", "v0.6.3", false},
		{"ghcr.io/go-taas/vllm", "ghcr.io", "go-taas/vllm", "latest", false},
		{"docker.1ms.run/library/vllm:latest", "docker.1ms.run", "library/vllm", "latest", false},
		{"vllm:v0.6.3", "", "vllm", "v0.6.3", false},
		{"library/vllm:latest", "", "library/vllm", "latest", false},
		{"localhost:5000/vllm:latest", "localhost:5000", "vllm", "latest", false},
		{"", "", "", "", true},
	}
	for _, c := range cases {
		host, repo, tag, err := ParseReference(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("ParseReference(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseReference(%q): %v", c.in, err)
		}
		if host != c.host || repo != c.repo || tag != c.tag {
			t.Fatalf("ParseReference(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.in, host, repo, tag, c.host, c.repo, c.tag)
		}
	}
}

func TestJoinReference(t *testing.T) {
	if got := JoinReference("hub.example.com", "taas/vllm", "v0.6.3"); got != "hub.example.com/taas/vllm:v0.6.3" {
		t.Fatalf("JoinReference = %q", got)
	}
	if got := JoinReference("", "vllm", "latest"); got != "vllm:latest" {
		t.Fatalf("JoinReference = %q", got)
	}
}

func TestSkopeoImporterSuccess(t *testing.T) {
	var gotArgs []string
	imp := &SkopeoImporter{Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "skopeo" {
			t.Fatalf("expected skopeo, got %s", name)
		}
		gotArgs = args
		return []byte("ok"), nil
	}}

	err := imp.Import(context.Background(),
		"docker.1ms.run/library/vllm:latest",
		&Credentials{Username: "src", Password: "sp"},
		"hub.example.com/taas/vllm:v0.6.3",
		&Credentials{Username: "admin", Password: "pw"},
	)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--src-creds src:sp") {
		t.Fatalf("missing src creds: %s", joined)
	}
	if !strings.Contains(joined, "--dest-creds admin:pw") {
		t.Fatalf("missing dest creds: %s", joined)
	}
	if !strings.Contains(joined, "docker://docker.1ms.run/library/vllm:latest") {
		t.Fatalf("missing src ref: %s", joined)
	}
	if !strings.Contains(joined, "docker://hub.example.com/taas/vllm:v0.6.3") {
		t.Fatalf("missing dest ref: %s", joined)
	}
}

func TestSkopeoImporterNoCreds(t *testing.T) {
	var gotArgs []string
	imp := &SkopeoImporter{Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		gotArgs = args
		return nil, nil
	}}
	err := imp.Import(context.Background(), "vllm:latest", nil, "hub.example.com/taas/vllm:latest", nil)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	joined := strings.Join(gotArgs, " ")
	if strings.Contains(joined, "--src-creds") || strings.Contains(joined, "--dest-creds") {
		t.Fatalf("creds should be omitted: %s", joined)
	}
}

func TestSkopeoImporterFailure(t *testing.T) {
	imp := &SkopeoImporter{Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("manifest unknown"), errors.New("exit status 1")
	}}
	err := imp.Import(context.Background(), "vllm:latest", nil, "hub.example.com/taas/vllm:latest", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("error should include output: %v", err)
	}
}
