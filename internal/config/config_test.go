package config

// What an unset or unreadable value means. The limiter and the in-flight cap
// are built from these numbers in internal/httpfront (a limiter only when the
// limit is above 0, a cap only when it is above 0).

import "testing"

// Off by default is what the self-hosted binary needs: it is single-tenant, so
// limiting its one caller would only get in the way.
func TestRateLimitFromEnvOffUnlessSet(t *testing.T) {
	for _, raw := range []string{"", "0", "-5", "abc"} {
		if rl := rateLimitFromEnv(raw); rl != 0 {
			t.Fatalf("MCP_RATE_LIMIT_PER_MIN=%q should leave the limiter off", raw)
		}
	}
	rl := rateLimitFromEnv("60")
	if rl != 60 {
		t.Fatalf("MCP_RATE_LIMIT_PER_MIN=60 should build a limiter of 60, got %+v", rl)
	}
}

func TestInflightOffByDefault(t *testing.T) {
	for _, raw := range []string{"", "0", "-3", "x"} {
		if n := inflightFromEnv(raw); n != 0 {
			t.Errorf("inflightFromEnv(%q) = %d, want 0 (off)", raw, n)
		}
	}
	if n := inflightFromEnv("16"); n != 16 {
		t.Errorf("inflightFromEnv(16) = %d", n)
	}
}
