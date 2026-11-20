// Package config reads the server's environment variables — all of them, here
// and nowhere else — and turns them into values. What a malformed or missing
// value means is decided here too, and it is never an error: a value that
// cannot be read means "off" or the default, because a self-hosted server
// started with a typo in an optional setting must still serve. The meanings
// are documented for operators in docs/install.en.md "Configuration
// reference".
//
// Every value is read once, at startup; the rest of the process is handed the
// Config (or the part of it a package needs) and never reads the environment.
package config

import (
	"log/slog"
	"strconv"
	"strings"
)

// Config is the whole configuration, one field per environment variable.
type Config struct {
	// UpstreamURL is RATATOSK_URL, the /v2 API's base. Default
	// https://ratatosk.io.
	UpstreamURL string
	// HTTPAddr is MCP_HTTP_ADDR. Set, the server serves streamable HTTP at
	// <addr>/mcp (and /healthz); empty, it serves stdio.
	HTTPAddr string
	// Stateless is MCP_HTTP_STATELESS=1: every POST on a temporary session.
	Stateless bool
	// MetricsAddr is MCP_METRICS_ADDR: GET /metrics on a listener of its own,
	// in HTTP mode only. Empty: metrics off.
	MetricsAddr string
	// RateLimitPerMin is MCP_RATE_LIMIT_PER_MIN, requests per minute per
	// caller. 0: off.
	RateLimitPerMin int
	// MaxInflight is MCP_MAX_INFLIGHT, requests worked on at once. 0: off.
	MaxInflight int
	// SelfCallers is MCP_SELF_CALLERS: the operator's own addresses (an entry
	// ending in "." is a prefix). Empty: every caller is external.
	SelfCallers []string
	// CacheMaxBytes is MCP_CACHE_MAX_MB in bytes, the response cache's budget.
	CacheMaxBytes int
	// LogLevel is MCP_LOG. Default info.
	LogLevel slog.Level
	// AuditMode is MCP_AUDIT: "metadata", "full", or "" (off).
	AuditMode string
}

// Read builds the configuration from getenv (os.Getenv in the server).
func Read(getenv func(string) string) Config {
	c := Config{
		UpstreamURL:     getenv("RATATOSK_URL"),
		HTTPAddr:        getenv("MCP_HTTP_ADDR"),
		Stateless:       getenv("MCP_HTTP_STATELESS") == "1",
		MetricsAddr:     getenv("MCP_METRICS_ADDR"),
		RateLimitPerMin: rateLimitFromEnv(getenv("MCP_RATE_LIMIT_PER_MIN")),
		MaxInflight:     inflightFromEnv(getenv("MCP_MAX_INFLIGHT")),
		SelfCallers:     selfCallersFromEnv(getenv("MCP_SELF_CALLERS")),
		CacheMaxBytes:   cacheBytesFromEnv(getenv("MCP_CACHE_MAX_MB")),
		LogLevel:        logLevelFromEnv(getenv("MCP_LOG")),
		AuditMode:       auditModeFromEnv(getenv("MCP_AUDIT")),
	}
	if c.UpstreamURL == "" {
		c.UpstreamURL = "https://ratatosk.io"
	}
	return c
}

// rateLimitFromEnv reads MCP_RATE_LIMIT_PER_MIN. Absent, zero, or unparseable
// means off — the default, and what a self-hosted server wants.
func rateLimitFromEnv(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// inflightFromEnv reads MCP_MAX_INFLIGHT: absent, zero or unparseable means
// off — the default, and what a single-tenant self-hosted server wants.
func inflightFromEnv(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// selfCallersFromEnv reads MCP_SELF_CALLERS: a comma-separated list of
// addresses; an entry ending in "." is a prefix match. Unset means every
// caller is external, which is the right default for a deployment that has
// no "own" traffic.
func selfCallersFromEnv(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, e := range parts {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// cacheBytesFromEnv reads MCP_CACHE_MAX_MB — the response cache's byte budget.
// Absent or unparseable means 32 MiB: half of the smallest container the Helm
// chart and the hosted compose have shipped with, so the cache alone can never
// take a process down.
func cacheBytesFromEnv(raw string) int {
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n << 20
	}
	return 32 << 20
}

// logLevelFromEnv reads MCP_LOG: debug adds per-call timing, still
// argument-free; warn and error reduce output; anything else is info, without
// a warning.
func logLevelFromEnv(raw string) slog.Level {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// auditModeFromEnv reads MCP_AUDIT: "metadata" or "full" turn the audit
// stream on (internal/audit); anything else leaves it off.
func auditModeFromEnv(raw string) string {
	switch raw {
	case "metadata", "full":
		return raw
	}
	return ""
}
