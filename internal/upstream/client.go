// Package upstream is a thin client for the ratatosk agent API v2
// (https://ratatosk.io/v2 describes itself). Read-only; the base URL is the
// only configuration. v2 returns the same shape the website shows — a release
// with before-upgrade checks and changes in seven kinds — so the tools mostly
// pass it through; only the types check_stack inspects are spelled out
// (internal/release), and unknown fields are ignored so the server can add
// fields without breaking this client (the v2 numbering rule: additions never
// change the path, removals and renames move it to /v3).
//
// It is the one package that talks to the network. What leaves the process is
// what its callers ask it to fetch, so check_stack's privacy property — the
// requests depend on project slugs only — is kept by the tool (fetchSet in
// internal/tools), and the packages that decide on releases (briefing,
// releaseline, advisory) do not import this one.
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/basphere/ratatosk-mcp/internal/logging"
	"github.com/basphere/ratatosk-mcp/internal/metrics"
	"github.com/basphere/ratatosk-mcp/internal/release"
)

// Client reads the /v2 API at one base URL.
type Client struct {
	baseURL string
	version string // this server's version, sent as User-Agent
	http    *http.Client
	cache   *respCache
	mx      *metrics.Metrics // nil: not counted
}

// New is a client for baseURL. version goes out in User-Agent
// ("ratatosk-mcp/<version>"); cacheMaxBytes bounds the response cache
// (MCP_CACHE_MAX_MB, see internal/config); m counts every request sent (nil:
// metrics off).
func New(baseURL, version string, cacheMaxBytes int, m *metrics.Metrics) *Client {
	return &Client{
		baseURL: baseURL,
		version: version,
		http:    &http.Client{Timeout: 20 * time.Second},
		cache:   newRespCache(5*time.Minute, 4000, cacheMaxBytes),
		mx:      m,
	}
}

// ---------------------------------------------------------------------------
// v2 shapes this client reads itself (a release's are in internal/release)
// ---------------------------------------------------------------------------

// Project is one row of /v2/projects.
type Project struct {
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	ImageAliases []string `json:"image_aliases"`
	// Latest is the project's latest release as the API defines it: the highest
	// version (on the main line when a project has several), not the most
	// recently published. nil when nothing is analyzed yet.
	Latest *struct {
		Version string `json:"version"`
	} `json:"latest"`
}

// ---------------------------------------------------------------------------
// transport (the response cache is in cache.go)
// ---------------------------------------------------------------------------

// getRaw fetches one /v2 resource. endpoint is the log-safe route PATTERN
// ("/v2/releases/{project}/{version}") — path itself can name a version, so it
// appears in errors returned to the agent (which needs the detail) but never in
// a log field (the invariant).
func (c *Client) getRaw(ctx context.Context, endpoint, path string, q url.Values, cached bool) ([]byte, error) {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	if cached {
		if b, ok := c.cache.get(u); ok {
			return b, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		// err quotes the full URL; keep the upstream address out of the answer.
		return nil, fmt.Errorf("could not build upstream request for %s", endpoint)
	}
	req.Header.Set("User-Agent", "ratatosk-mcp/"+c.version)
	if tp := logging.TraceparentFrom(ctx); tp != "" {
		req.Header.Set("traceparent", tp)
	}
	// One bit, no address: lets a hosted deployment's upstream separate real
	// users from the operator's own calls, which are otherwise indistinguishable
	// over an internal network (see the caller class in internal/httpfront).
	if cc := logging.CallerClassFrom(ctx); cc != "" {
		req.Header.Set("X-Ratatosk-Caller", cc)
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.mx.CountUpstream(logging.ToolFrom(ctx), metrics.UpstreamOutcome(err))
		slog.ErrorContext(ctx, "upstream fetch failed", "upstream", endpoint, "kind", logging.ErrKind(err))
		// Transport errors carry the full request URL and the resolved address
		// ("dial tcp 172.x.x.x:3000: ..."). That string is returned to the
		// caller verbatim by the tools' errResult, so on the hosted endpoint an anonymous
		// internet caller could read our internal topology off a failed
		// request. The detail stays in the log line above; the caller gets the
		// class, which is all an agent can act on anyway.
		return nil, fmt.Errorf("upstream request failed (%s)", logging.ErrKind(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		c.mx.CountUpstream(logging.ToolFrom(ctx), metrics.UpstreamOutcome(err))
		slog.ErrorContext(ctx, "upstream body read failed", "upstream", endpoint, "kind", logging.ErrKind(err))
		return nil, fmt.Errorf("upstream response read failed (%s)", logging.ErrKind(err))
	}
	// ok is a 200; error is any other answer (a 404 for a version not on
	// record included). Requests that got no answer are counted above.
	if resp.StatusCode == http.StatusOK {
		c.mx.CountUpstream(logging.ToolFrom(ctx), "ok")
	} else {
		c.mx.CountUpstream(logging.ToolFrom(ctx), "error")
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		slog.DebugContext(ctx, "upstream ok", "upstream", endpoint, "status", resp.StatusCode, "ms", time.Since(start).Milliseconds())
	case resp.StatusCode == http.StatusTooManyRequests:
		slog.WarnContext(ctx, "upstream rate limited", "upstream", endpoint, "status", resp.StatusCode)
	case resp.StatusCode >= 500:
		slog.ErrorContext(ctx, "upstream error", "upstream", endpoint, "status", resp.StatusCode)
	default:
		// A client mistake (unknown slug, bad params) is answered with guidance
		// by the tool itself — that answer IS the handling. DEBUG, never ERROR:
		// one confused agent's typo loop must not page an operator.
		slog.DebugContext(ctx, "upstream rejected request", "upstream", endpoint, "status", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		// 400 chars so self-correcting errors survive intact — the /v2 404 carries
		// the project's recent versions for the agent to retry with.
		return nil, &HTTPError{Path: path, Status: resp.StatusCode, Body: truncate(string(body), 400)}
	}
	if cached {
		c.cache.put(u, body)
	}
	return body, nil
}

// HTTPError keeps the status inspectable: check_stack reads a 404 on a single
// release as "not on record", not as a failed call.
type HTTPError struct {
	Path   string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Path, e.Status, e.Body)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func langQuery(q url.Values, lang string) url.Values {
	if lang != "" && lang != "en" {
		if q == nil {
			q = url.Values{}
		}
		q.Set("lang", lang)
	}
	return q
}

// ---------------------------------------------------------------------------
// endpoints
// ---------------------------------------------------------------------------

// ProjectsRaw is /v2/projects as received.
func (c *Client) ProjectsRaw(ctx context.Context, cached bool) ([]byte, error) {
	return c.getRaw(ctx, "/v2/projects", "/v2/projects", nil, cached)
}

// Projects is the roster, read through the cache.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	b, err := c.ProjectsRaw(ctx, true)
	if err != nil {
		return nil, err
	}
	var out struct {
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("/v2/projects: %w", err)
	}
	return out.Projects, nil
}

// ReleasesQuery is the filter of /v2/releases; empty fields are not sent.
type ReleasesQuery struct {
	Project, Kind, Since, Before, Lang string
	Limit                              int
}

// ReleasesRaw is /v2/releases as received.
func (c *Client) ReleasesRaw(ctx context.Context, rq ReleasesQuery, cached bool) ([]byte, error) {
	q := url.Values{}
	for k, v := range map[string]string{"project": rq.Project, "kind": rq.Kind, "since": rq.Since, "before": rq.Before} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if rq.Limit > 0 {
		q.Set("limit", strconv.Itoa(rq.Limit))
	}
	return c.getRaw(ctx, "/v2/releases", "/v2/releases", langQuery(q, rq.Lang), cached)
}

// maxReleasePages bounds the release-list paging for one project. 200 rows a
// page, so this is 2,000 releases — far past any project on record; it only
// stops a server that keeps answering next_before from looping forever.
const maxReleasePages = 10

// ProjectReleases pages through every release row of one project, newest
// first. Only the project slug is sent — never a version.
func (c *Client) ProjectReleases(ctx context.Context, project string) ([]release.Row, error) {
	// An empty slug drops the project filter and this loop would page every
	// project. check_stack refuses such a component first; this is the
	// backstop at the one place the fan-out happens.
	if project == "" {
		return nil, fmt.Errorf("refusing to list releases without a project")
	}
	var all []release.Row
	before := ""
	for page := 0; page < maxReleasePages; page++ {
		b, err := c.ReleasesRaw(ctx, ReleasesQuery{Project: project, Before: before, Limit: 200}, true)
		if err != nil {
			return nil, err
		}
		var out struct {
			Releases   []release.Row `json:"releases"`
			NextBefore *string       `json:"next_before"`
		}
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("/v2/releases: %w", err)
		}
		all = append(all, out.Releases...)
		if out.NextBefore == nil || *out.NextBefore == "" || *out.NextBefore == before {
			return all, nil
		}
		before = *out.NextBefore
	}
	return all, nil
}

// PathSegment escapes one caller-supplied path segment and refuses "." and
// "..". url.PathEscape leaves both as they are, and they are dot segments: the
// upstream resolves them before routing, so get_release(kubernetes, "..")
// read /v2/releases and returned every project's release list as if it were
// one release, and ("..", "..") returned the website's HTML home page as a
// normal answer (independent review, 2026-11-15). Neither is a slug or a
// version on record.
func PathSegment(what, v string) (string, error) {
	if v == "." || v == ".." {
		if what == "project" {
			return "", fmt.Errorf("project %q is not a project slug — check it with list_projects", v)
		}
		return "", fmt.Errorf("%s %q is not a release version — pass it as the project publishes it, e.g. v1.36.6", what, v)
	}
	return url.PathEscape(v), nil
}

// ReleaseRaw is one release document as received.
func (c *Client) ReleaseRaw(ctx context.Context, project, version, lang string, cached bool) ([]byte, error) {
	p, err := PathSegment("project", project)
	if err != nil {
		return nil, err
	}
	v, err := PathSegment("version", version)
	if err != nil {
		return nil, err
	}
	return c.getRaw(ctx, "/v2/releases/{project}/{version}", "/v2/releases/"+p+"/"+v, langQuery(nil, lang), cached)
}

// UpgradeRaw is the explicit server-side range: from and to travel to the
// server. check_upgrade uses it and says so; check_stack never does.
func (c *Client) UpgradeRaw(ctx context.Context, project, from, to, lang string) ([]byte, error) {
	p, err := PathSegment("project", project)
	if err != nil {
		return nil, err
	}
	q := url.Values{"from": {from}}
	if to != "" {
		q.Set("to", to)
	}
	return c.getRaw(ctx, "/v2/upgrade/{project}", "/v2/upgrade/"+p, langQuery(q, lang), false)
}

// SearchRaw is /v2/search as received; it never goes through the cache.
func (c *Client) SearchRaw(ctx context.Context, query, lang string) ([]byte, error) {
	return c.getRaw(ctx, "/v2/search", "/v2/search", langQuery(url.Values{"q": {query}}, lang), false)
}
