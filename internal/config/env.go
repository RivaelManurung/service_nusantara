package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// envReader collects lookup errors instead of panicking or calling os.Exit, so
// that Load can report every misconfigured variable in a single pass.
type envReader struct {
	errs []string
}

func (r *envReader) fail(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *envReader) err() error {
	if len(r.errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(r.errs, "\n  - "))
}

func (r *envReader) str(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func (r *envReader) required(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		r.fail("%s is required but not set", key)
	}
	return v
}

func (r *envReader) requiredMinLen(key string, minLen int) string {
	v := r.required(key)
	if v != "" && len(v) < minLen {
		r.fail("%s must be at least %d characters", key, minLen)
	}
	return v
}

func (r *envReader) int(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		r.fail("%s must be an integer, got %q", key, raw)
		return fallback
	}
	return v
}

func (r *envReader) duration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		r.fail("%s must be a duration such as 15m or 24h, got %q", key, raw)
		return fallback
	}
	return v
}

func (r *envReader) boolean(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		r.fail("%s must be a boolean, got %q", key, raw)
		return fallback
	}
	return v
}

func (r *envReader) csv(key string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}
