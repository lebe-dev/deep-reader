package config

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"strings"
)

// loadDotEnv reads a ".env"-style file and sets each KEY=VALUE pair into the
// process environment, but never overrides a variable that is already set.
// This keeps the real environment (e.g. docker compose env_file, exported
// shell vars) authoritative while making `go run ./cmd/server` pick up local
// settings automatically.
//
// A missing file is not an error: in production the env is provided directly
// and no .env exists. Supported syntax is intentionally minimal: comments (#),
// blank lines, an optional leading "export", and single/double-quoted values.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := parseDotEnvLine(scanner.Text())
		if !ok {
			continue
		}
		if _, present := os.LookupEnv(key); present {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// parseDotEnvLine parses a single line into a key/value pair. It returns ok=false
// for blank lines, comments, and malformed entries.
func parseDotEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}

	line = strings.TrimPrefix(line, "export ")

	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return "", "", false
	}

	key = strings.TrimSpace(line[:eq])
	value = strings.TrimSpace(line[eq+1:])
	if key == "" {
		return "", "", false
	}

	return key, unquoteDotEnvValue(value), true
}

// unquoteDotEnvValue strips matching surrounding quotes. For unquoted values it
// also drops a trailing inline "# comment". Quoted values are taken verbatim so
// that '#' and spaces inside quotes are preserved.
func unquoteDotEnvValue(value string) string {
	if len(value) >= 2 {
		first := value[0]
		if (first == '"' || first == '\'') && value[len(value)-1] == first {
			return value[1 : len(value)-1]
		}
	}

	if i := strings.IndexByte(value, '#'); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	return value
}
