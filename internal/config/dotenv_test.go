package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "" +
		"# a comment\n" +
		"\n" +
		"HTTP_PORT=9090\n" +
		"export AUTH_TOKEN=secret-token\n" +
		"DATABASE_PATH = /tmp/db.sqlite \n" +
		"LLM_API_KEY=\"sk-quoted\"\n" +
		"LLM_MODEL='gpt-quoted'\n" +
		"LLM_API_BASE_URL=https://example.com/v1 # inline comment\n" +
		"EMPTY=\n"

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"HTTP_PORT", "AUTH_TOKEN", "DATABASE_PATH", "LLM_API_KEY", "LLM_MODEL", "LLM_API_BASE_URL", "EMPTY"} {
		t.Setenv(k, "") // register for cleanup
		os.Unsetenv(k)
	}

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}

	cases := map[string]string{
		"HTTP_PORT":        "9090",
		"AUTH_TOKEN":       "secret-token",
		"DATABASE_PATH":    "/tmp/db.sqlite",
		"LLM_API_KEY":      "sk-quoted",
		"LLM_MODEL":        "gpt-quoted",
		"LLM_API_BASE_URL": "https://example.com/v1",
		"EMPTY":            "",
	}
	for k, want := range cases {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestLoadDotEnvDoesNotOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("AUTH_TOKEN=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AUTH_TOKEN", "from-environment")

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}

	if got := os.Getenv("AUTH_TOKEN"); got != "from-environment" {
		t.Errorf("AUTH_TOKEN = %q, want it untouched (from-environment)", got)
	}
}

func TestLoadDotEnvMissingFileIsNotAnError(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Errorf("missing file should be a no-op, got %v", err)
	}
}
