package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/basphere/ratatosk-mcp/internal/release"
	"github.com/basphere/ratatosk-mcp/internal/releaseline"
	"github.com/basphere/ratatosk-mcp/internal/upstream"
	"github.com/basphere/ratatosk-mcp/internal/version"
)

// fetchSetSize is how many of a project's newest releases check_stack reads,
// whatever the running version. Measured on 2026-11-03 over 77 projects: 977
// analyzed releases, about 13 a project; the busiest publish several a week.
// 60 covers months even for those. A running version older than the oldest
// release read is told so in note and its unread releases are named — never a
// silent partial answer.
const fetchSetSize = 60

// fetchBudget bounds the release documents one check_stack call may read
// upstream (cache hits are free). A self-hosted server shares its IP's 1200
// requests a minute; one call must not spend half of it. Components past the
// budget go to could_not_check with a reason, so the caller can split the call.
const fetchBudget = 600

// fetchParallel is how many release documents are in flight at once.
const fetchParallel = 6

// fullReleasesCap bounds detail:"full", where each release is a whole document.
const fullReleasesCap = 15

// fullBytesBudget bounds the release documents one detail:"full" call carries,
// summed over its components. fullReleasesCap alone bounded each component, not
// the call: forty components of fifteen documents made one 32 MB answer that
// held 382 MB while it was built (independent review, 2026-11-11). Measured the
// same day over the 70 newest releases: a document is 15 KB at the median,
// 120 KB at the 90th percentile, 302 KB at most, so 2 MiB is about nine
// components of ordinary releases. What does not fit is counted in
// releases_full_omitted and named in note, never dropped silently.
const fullBytesBudget = 2 << 20

// fetchReason words a failed fetch for could_not_check. Out of time is said as
// such, with what to do next, rather than as a transport error.
func fetchReason(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "not checked: this call ran out of time — call check_stack again with the remaining components"
	}
	return "fetch failed: " + err.Error()
}

// fetchSet is the releases check_stack reads for a project: its newest
// fetchSetSize by release date, across every line. It is a function of the
// project alone — that is the privacy property, so nothing about the running
// version, its line or the target may enter here.
func fetchSet(rows []release.Row) []release.Row {
	if len(rows) > fetchSetSize {
		return rows[:fetchSetSize]
	}
	return rows
}

// fetchDocs reads release documents through the cache, a few at a time. A 404
// is "not on record" (the release list and the documents come from different
// moments) and is skipped; anything else is an error.
func (h *handlers) fetchDocs(ctx context.Context, rows []release.Row, lang string) (map[string]*release.Doc, error) {
	out := make(map[string]*release.Doc, len(rows))
	var mu sync.Mutex
	var firstErr error
	// One failed document fails the component, so the rest are not worth
	// fetching: cancel them instead of reading up to 60 more.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, fetchParallel)
	var wg sync.WaitGroup
	for _, r := range rows {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(r release.Row) {
			defer wg.Done()
			defer func() { <-sem }()
			b, err := h.api.ReleaseRaw(ctx, r.Project, r.Version, lang, true)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if he, ok := err.(*upstream.HTTPError); ok && he.Status == 404 {
					return
				}
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			d, perr := release.Parse(b)
			if perr != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("/v2/releases/{project}/{version}: %w", perr)
					cancel()
				}
				return
			}
			out[r.Version] = d
		}(r)
	}
	wg.Wait()
	if firstErr == nil && ctx.Err() != nil {
		firstErr = ctx.Err() // the caller's deadline, not our own cancel
	}
	return out, firstErr
}

// fixedOnBranch reads the advisory ids from's own branch carries at or below
// from — what check_stack reads off its fetch set. The rows come newest first,
// so the newest fetchSetSize releases of the branch at or below from are read;
// older ones are not, and nothing says so. check_stack reads the project's
// newest fetchSetSize releases instead, whatever the branch, as what it sends
// must not depend on the running version: when the branch's releases at or
// below the running version reach past those, it settles less than this does
// and says so in note. Otherwise the two settle the same.
func (h *handlers) fixedOnBranch(ctx context.Context, rows []release.Row, from, lang string) (map[string]bool, error) {
	fromKey := version.NormalizeVersion(from)
	if len(fromKey) < 2 {
		return nil, nil // no branch to read: nothing is settled, as before 1.2.0
	}
	var branch []release.Row
	for _, r := range rows {
		k := version.NormalizeVersion(r.Version)
		if k == nil || !version.SameLine(r.Version, from) || !releaseline.SameBranch(k, fromKey) || version.Compare(k, fromKey) > 0 {
			continue
		}
		branch = append(branch, r)
		if len(branch) == fetchSetSize {
			break
		}
	}
	docs, err := h.fetchDocs(ctx, branch, lang)
	if err != nil {
		return nil, err
	}
	fixed := map[string]bool{}
	for _, d := range docs {
		for _, c := range d.Changes {
			if c.Kind != "security" {
				continue
			}
			for _, id := range c.Advisories {
				fixed[strings.ToUpper(strings.TrimSpace(id))] = true
			}
		}
	}
	return fixed, nil
}
