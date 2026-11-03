package main

// Thin client for the ratatosk agent API v2 (https://ratatosk.io/v2 describes
// itself). Read-only; the base URL is the only configuration. v2 returns the
// same shape the website shows — a release with before-upgrade checks and
// changes in seven kinds — so the tools mostly pass it through; only the types
// check_stack inspects are spelled out here, and unknown fields are ignored so
// the server can add fields without breaking this client (the v2 numbering
// rule: additions never change the path, removals and renames move it to /v3).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type apiClient struct {
	baseURL string
	http    *http.Client
	cache   *respCache
}

func newAPIClient(baseURL string) *apiClient {
	return &apiClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 20 * time.Second},
		cache:   newRespCache(5*time.Minute, 4000),
	}
}

// ---------------------------------------------------------------------------
// v2 shapes (only what check_stack reads)
// ---------------------------------------------------------------------------

// v2Project is one row of /v2/projects.
type v2Project struct {
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	ImageAliases []string `json:"image_aliases"`
}

// v2Row is one row of /v2/releases — a release without its changes.
type v2Row struct {
	Project    string         `json:"project"`
	Version    string         `json:"version"`
	ReleasedAt string         `json:"released_at"`
	URL        string         `json:"url"`
	Counts     map[string]int `json:"counts"`
	Warnings   map[string]int `json:"warnings"`
}

// v2Attr is an attribute that says whether the release notes stated it:
// status stated | inferred | not_stated | not_read.
type v2Attr struct {
	Text   string `json:"text"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type v2Item struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Body  *string `json:"body"`
}

type v2Change struct {
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Title           string            `json:"title"`
	Body            *string           `json:"body"`
	Severity        *string           `json:"severity"`
	Advisories      []string          `json:"advisories"`
	Affects         *v2Attr           `json:"affects"`
	ActionRequired  *string           `json:"action_required"`
	AlsoRequired    *string           `json:"also_required"`
	NoDowngrade     *v2Attr           `json:"no_downgrade"`
	RemovedIn       *v2Attr           `json:"removed_in"`
	ImpactUnclear   bool              `json:"impact_unclear"`
	SilentFailure   bool              `json:"silent_failure"`
	Workaround      *v2Attr           `json:"workaround"`
	RegressionSince *v2Attr           `json:"regression_since"`
	Later           []json.RawMessage `json:"later"`
}

// v2Release is GET /v2/releases/{project}/{version}. Raw keeps the document
// as received for detail:"full", so nothing the server adds is lost on the way.
type v2Release struct {
	Project       string         `json:"project"`
	Version       string         `json:"version"`
	ReleasedAt    string         `json:"released_at"`
	URL           string         `json:"url"`
	Counts        map[string]int `json:"counts"`
	Warnings      map[string]int `json:"warnings"`
	Processing    bool           `json:"processing"`
	BeforeUpgrade struct {
		Prerequisites []v2Item `json:"prerequisites"`
		KnownIssues   []v2Item `json:"known_issues"`
		UpgradeGuide  []v2Item `json:"upgrade_guide"`
	} `json:"before_upgrade"`
	Changes []v2Change        `json:"changes"`
	Later   []json.RawMessage `json:"later"`
	Raw     json.RawMessage   `json:"-"`
}

// ---------------------------------------------------------------------------
// transport
// ---------------------------------------------------------------------------

// respCache holds public documents check_stack reads: the project roster, a
// project's release list and single releases, keyed by their public path. Every
// key is something anyone can fetch, and none of them is derived from a
// caller's running version — check_stack reads a project's releases whatever
// version it was asked about (see fetchSet), so the cache cannot hold one
// either. It exists because that design reads more than one call strictly
// needs, and agents re-run check_stack within minutes: the second run should
// cost nothing upstream. /v2/upgrade and /v2/search, which carry what a
// caller typed, never go through it.
type respCache struct {
	mu   sync.Mutex
	ttl  time.Duration
	max  int
	data map[string]cacheEntry
}

type cacheEntry struct {
	body []byte
	at   time.Time
}

func newRespCache(ttl time.Duration, max int) *respCache {
	return &respCache{ttl: ttl, max: max, data: map[string]cacheEntry{}}
}

func (c *respCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[key]
	if !ok || time.Since(e.at) > c.ttl {
		return nil, false
	}
	return e.body, true
}

func (c *respCache) put(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) >= c.max {
		for k, e := range c.data {
			if time.Since(e.at) > c.ttl {
				delete(c.data, k)
			}
		}
		if len(c.data) >= c.max {
			c.data = map[string]cacheEntry{} // still full of live entries: start over rather than grow
		}
	}
	c.data[key] = cacheEntry{body: body, at: time.Now()}
}

// getRaw fetches one /v2 resource. endpoint is the log-safe route PATTERN
// ("/v2/releases/{project}/{version}") — path itself can name a version, so it
// appears in errors returned to the agent (which needs the detail) but never in
// a log field (the invariant).
func (c *apiClient) getRaw(ctx context.Context, endpoint, path string, q url.Values, cached bool) ([]byte, error) {
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
	req.Header.Set("User-Agent", "ratatosk-mcp/"+buildVersion)
	if tp := traceparentFrom(ctx); tp != "" {
		req.Header.Set("traceparent", tp)
	}
	// One bit, no address: lets a hosted deployment's upstream separate real
	// users from the operator's own calls, which are otherwise indistinguishable
	// over an internal network (see callerClassMiddleware).
	if cc := callerClassFrom(ctx); cc != "" {
		req.Header.Set("X-Ratatosk-Caller", cc)
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		slog.ErrorContext(ctx, "upstream fetch failed", "upstream", endpoint, "kind", errKind(err))
		// Transport errors carry the full request URL and the resolved address
		// ("dial tcp 172.x.x.x:3000: ..."). That string is returned to the
		// caller verbatim by errResult, so on the hosted endpoint an anonymous
		// internet caller could read our internal topology off a failed
		// request. The detail stays in the log line above; the caller gets the
		// class, which is all an agent can act on anyway.
		return nil, fmt.Errorf("upstream request failed (%s)", errKind(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		slog.ErrorContext(ctx, "upstream body read failed", "upstream", endpoint, "kind", errKind(err))
		return nil, fmt.Errorf("upstream response read failed (%s)", errKind(err))
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
		return nil, &httpError{Path: path, Status: resp.StatusCode, Body: truncate(string(body), 400)}
	}
	if cached {
		c.cache.put(u, body)
	}
	return body, nil
}

// httpError keeps the status inspectable: check_stack reads a 404 on a single
// release as "not on record", not as a failed call.
type httpError struct {
	Path   string
	Status int
	Body   string
}

func (e *httpError) Error() string {
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

func (c *apiClient) projectsRaw(ctx context.Context, cached bool) ([]byte, error) {
	return c.getRaw(ctx, "/v2/projects", "/v2/projects", nil, cached)
}

func (c *apiClient) projects(ctx context.Context) ([]v2Project, error) {
	b, err := c.projectsRaw(ctx, true)
	if err != nil {
		return nil, err
	}
	var out struct {
		Projects []v2Project `json:"projects"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("/v2/projects: %w", err)
	}
	return out.Projects, nil
}

type releasesQuery struct {
	Project, Kind, Since, Before, Lang string
	Limit                              int
}

func (c *apiClient) releasesRaw(ctx context.Context, rq releasesQuery, cached bool) ([]byte, error) {
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

// projectReleases pages through every release row of one project, newest
// first. Only the project slug is sent — never a version.
func (c *apiClient) projectReleases(ctx context.Context, project string) ([]v2Row, error) {
	// An empty slug drops the project filter and this loop would page every
	// project. check_stack refuses such a component first; this is the
	// backstop at the one place the fan-out happens.
	if project == "" {
		return nil, fmt.Errorf("refusing to list releases without a project")
	}
	var all []v2Row
	before := ""
	for page := 0; page < maxReleasePages; page++ {
		b, err := c.releasesRaw(ctx, releasesQuery{Project: project, Before: before, Limit: 200}, true)
		if err != nil {
			return nil, err
		}
		var out struct {
			Releases   []v2Row `json:"releases"`
			NextBefore *string `json:"next_before"`
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

func (c *apiClient) releaseRaw(ctx context.Context, project, version, lang string, cached bool) ([]byte, error) {
	path := "/v2/releases/" + url.PathEscape(project) + "/" + url.PathEscape(version)
	return c.getRaw(ctx, "/v2/releases/{project}/{version}", path, langQuery(nil, lang), cached)
}

func parseRelease(b []byte) (*v2Release, error) {
	var r v2Release
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	r.Raw = b
	return &r, nil
}

// upgradeRaw is the explicit server-side range: from and to travel to the
// server. check_upgrade uses it and says so; check_stack never does.
func (c *apiClient) upgradeRaw(ctx context.Context, project, from, to, lang string) ([]byte, error) {
	q := url.Values{"from": {from}}
	if to != "" {
		q.Set("to", to)
	}
	return c.getRaw(ctx, "/v2/upgrade/{project}", "/v2/upgrade/"+url.PathEscape(project), langQuery(q, lang), false)
}

func (c *apiClient) searchRaw(ctx context.Context, query, lang string) ([]byte, error) {
	return c.getRaw(ctx, "/v2/search", "/v2/search", langQuery(url.Values{"q": {query}}, lang), false)
}
