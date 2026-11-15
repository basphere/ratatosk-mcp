package main

// Tests for what the independent review before the 1.0 launch found on
// 2026-11-15. The worst kind of answer an agent can get is one that reads as
// "nothing to do" without an error; most of these replay one.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolDescriptions lists the descriptions exactly as a client receives them.
func toolDescriptions(t *testing.T) map[string]string {
	t.Helper()
	res, err := sdkSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool.Description
	}
	return out
}

// "What happened to the running version after it shipped" was read as "what is
// wrong with it": an advisory_after_release is an advisory published later for
// a vulnerability that version already fixes. And list_releases, which lists by
// release date, read as the place to ask what is new about a version.
func TestDescriptionsSayWhatLaterEventsMean(t *testing.T) {
	d := toolDescriptions(t)
	for _, c := range []struct{ where, text string }{
		{"check_stack", d["check_stack"]},
		{"check_upgrade", d["check_upgrade"]},
		{"hint", stackHint},
	} {
		for _, want := range []string{"already fixes", "does not mean the release is vulnerable", "recorded on that version, not here"} {
			if !strings.Contains(c.text, want) {
				t.Errorf("%s does not say %q", c.where, want)
			}
		}
		if strings.Contains(c.text, "what happened to the") {
			t.Errorf("%s still says the later events are what happened to the version", c.where)
		}
	}
	if !strings.Contains(d["get_release"], "already fixes, which does not make it vulnerable") {
		t.Errorf("get_release description: %s", d["get_release"])
	}
	lr := d["list_releases"]
	if strings.Contains(lr, "THE tool for") || !strings.Contains(lr, "/v2/events") || !strings.Contains(lr, "later_for_running") {
		t.Errorf("list_releases must say it lists by release date and where later events are: %s", lr)
	}
}

// severity:null was described as "the advisory states none". It means no grade
// was found in what was read, an advisory not yet published included: the docs
// example measured on 2026-11-03 showed GHSA-3wh4-j44w-pg92 on etcd v3.5.32 as
// null, and the same change reads medium on 2026-11-15.
func TestDescriptionsSayWhatSeverityNullMeans(t *testing.T) {
	d := toolDescriptions(t)
	for _, c := range []struct{ where, text string }{{"get_release", d["get_release"]}, {"hint", stackHint}} {
		if strings.Contains(c.text, "states none") || !strings.Contains(c.text, "no grade was found in what was read") {
			t.Errorf("%s must say null is a grade not found: %s", c.where, c.text)
		}
	}
}

// A running version above every analyzed release on its line used to come back
// as an empty range with a target below it, which reads as "already up to
// date" (etcd v3.55.0 → releases [], target_version v3.7.3 on the preview API).
// It is listed in could_not_check, with no range at all.
func TestCheckStackRefusesAVersionNewerThanEveryAnalyzedRelease(t *testing.T) {
	swapAPI(t, httptest.NewServer(newFakeEtcd().handler(t)))
	out := runStack(t, checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.6.99", TargetVersion: "v3.7.0"},
		{Project: "etcd", Version: "v3.6.9-rc.1"},
		{Project: "etcd", Version: "v3.6.8"},
		{Project: "etcd", Version: "v3.6.8-rc.1"},
	}})
	if len(out.CouldNotCheck) != 2 {
		t.Fatalf("could_not_check %+v, want the two versions above v3.6.8", out.CouldNotCheck)
	}
	for i, want := range []string{"v3.6.99", "v3.6.9-rc.1"} {
		cnc, c := out.CouldNotCheck[i], out.Components[i]
		if cnc.Version != want || !strings.Contains(cnc.Reason, "newer than every analyzed release on its line (newest analyzed: v3.6.8)") {
			t.Errorf("could_not_check[%d] = %+v", i, cnc)
		}
		if c.Releases != nil || c.TargetVersion != "" || !strings.Contains(c.Note, "not analyzed this version yet") {
			t.Errorf("%s must carry no range and no target below it: releases %v target %q note %q", want, c.Releases, c.TargetVersion, c.Note)
		}
	}
	if c := out.Components[2]; c.Releases == nil || len(c.Releases) != 0 || c.Note != "" {
		t.Errorf("the newest release on record is checked and already newest: %+v", c)
	}
	if c := out.Components[3]; len(c.Releases) != 1 || c.Releases[0].Version != "v3.6.8" {
		t.Errorf("a pre-release of the newest release is below it: %+v", c.Releases)
	}
}

// /v2/upgrade answers the same case with a 404 (ratatosk.io, 2026-11-15);
// check_upgrade must pass that error on, not an empty range.
func TestCheckUpgradePassesTheNewerThanEveryReleaseError(t *testing.T) {
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"v3.55.0 is newer than every analyzed release of etcd in its line — latest analyzed: v3.7.3"}`, http.StatusNotFound)
	})))
	res, _, err := checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.55.0"})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !res.IsError || !strings.Contains(text, "HTTP 404") || !strings.Contains(text, "newer than every analyzed release") || !strings.Contains(text, "v3.7.3") {
		t.Errorf("isError=%v %s", res.IsError, text)
	}
}

// Every slug in /v2/projects is lower case. Matched as sent, "Kubernetes" was
// answered as untracked by check_stack and as an empty list by list_releases.
func TestSlugsAreMatchedWithoutCaseOrSurroundingSpace(t *testing.T) {
	f := newFakeEtcd()
	swapAPI(t, httptest.NewServer(f.handler(t)))

	out := runStack(t, checkStackArgs{Components: []stackComponent{{Project: " Etcd ", Version: "v3.6.7"}, {Project: "ETCD", Version: "v3.6.7"}}})
	if len(out.CouldNotCheck) != 0 || len(out.Components) != 1 {
		t.Fatalf("Etcd and ETCD are etcd, listed twice: %+v", out)
	}
	if c := out.Components[0]; c.Project != "etcd" || len(c.Releases) != 1 || !strings.Contains(c.Note, "listed 2 times") {
		t.Errorf("component %+v", c)
	}

	if res, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "ETCD ", Version: "v3.6.7"}); res.IsError {
		t.Errorf("get_release ETCD: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	if res, _, _ := listReleasesTool(context.Background(), nil, listReleasesArgs{Project: " Etcd"}); res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, `"version":"v3.6.8"`) {
		t.Errorf("list_releases Etcd: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	reqs := strings.Join(f.requests(), " ")
	for _, want := range []string{"/v2/releases/etcd/v3.6.7", "/v2/releases?limit=10&project=etcd"} {
		if !strings.Contains(reqs, want) {
			t.Errorf("no request %s in %s", want, reqs)
		}
	}

	// An unknown slug is an error before the release list is asked for.
	n := len(f.requests())
	res, _, _ := listReleasesTool(context.Background(), nil, listReleasesArgs{Project: "argo-cd"})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.HasPrefix(text, "unknown project argo-cd") || !strings.Contains(text, "list_projects") {
		t.Errorf("list_releases argo-cd: isError=%v %s", res.IsError, text)
	}
	if got := f.requests()[n:]; len(got) != 0 {
		t.Errorf("an unknown slug went upstream: %v", got)
	}

	var seen string
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		w.Write([]byte(`{"project":"etcd","from":"v3.6.7","to":"v3.6.8","releases":[],"gaps":[],"later_for_from":[]}`))
	})))
	checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "Etcd ", From: "v3.6.7"})
	if seen != "/v2/upgrade/etcd" {
		t.Errorf("check_upgrade asked %s", seen)
	}
}

// An empty range answered "changes": null and "before_upgrade": null; the docs
// say array. Same for later_for_from when the server leaves it out.
func TestCheckUpgradeAnswersArraysForAnEmptyRange(t *testing.T) {
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"project":"etcd","from":"v3.7.3","to":"v3.7.3","releases":[],"gaps":[]}`))
	})))
	res, _, err := checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.7.3"})
	if err != nil || res.IsError {
		t.Fatalf("check_upgrade: %v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{`"changes":[]`, `"before_upgrade":[]`, `"later_for_from":[]`, `"releases":[]`} {
		if !strings.Contains(text, want) {
			t.Errorf("no %s in %s", want, text)
		}
	}
	if strings.Contains(text, ":null") {
		t.Errorf("a null in the answer: %s", text)
	}
}

// "." and ".." are dot segments: the upstream resolved them before routing, so
// get_release(kubernetes, "..") returned the whole release list as one release
// and ("..", "..") the website's HTML. They are refused before any request.
func TestDotSegmentsAreRefusedBeforeAnyRequest(t *testing.T) {
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/projects" {
			t.Errorf("upstream was asked %s", r.URL.RequestURI())
		}
		w.Write([]byte(`{"projects":[{"slug":"kubernetes","name":"Kubernetes"}]}`))
	})))
	for _, c := range []struct {
		tool string
		call func() *mcp.CallToolResult
		want string
	}{
		{"get_release version ..", func() *mcp.CallToolResult {
			r, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "kubernetes", Version: ".."})
			return r
		}, `version ".." is not a release version`},
		{"get_release version .", func() *mcp.CallToolResult {
			r, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "kubernetes", Version: "."})
			return r
		}, `version "." is not a release version`},
		{"get_release project ..", func() *mcp.CallToolResult {
			r, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "..", Version: ".."})
			return r
		}, `project ".." is not a project slug`},
		{"check_upgrade project ..", func() *mcp.CallToolResult {
			r, _, _ := checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "..", From: "v1"})
			return r
		}, `project ".." is not a project slug`},
	} {
		res := c.call()
		if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, c.want) {
			t.Errorf("%s: isError=%v %s", c.tool, res.IsError, text)
		}
	}
}

// What a tool passes through must be a JSON object; a web page answered with
// 200 is an error, not a result. check_upgrade does not turn a JSON object that
// is not an upgrade answer into an empty briefing either.
func TestAnswersThatAreNotAPIAnswersAreErrors(t *testing.T) {
	swapAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/upgrade/"):
			w.Write([]byte(`{"name":"Ratatosk agent API","version":"v2","endpoints":[]}`))
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<!DOCTYPE html><html><body>home</body></html>"))
		}
	})))
	res, _, _ := getReleaseTool(context.Background(), nil, getReleaseArgs{Project: "etcd", Version: "v3.6.7"})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "not a JSON object") || strings.Contains(text, "<html>") {
		t.Errorf("get_release on an HTML answer: isError=%v %s", res.IsError, text)
	}
	res, _, _ = searchTool(context.Background(), nil, searchArgs{Q: "CVE-2026-1"})
	if !res.IsError {
		t.Errorf("search on an HTML answer was a result")
	}
	res, _, _ = checkUpgradeTool(context.Background(), nil, checkUpgradeArgs{Project: "etcd", From: "v3.6.7"})
	if text := res.Content[0].(*mcp.TextContent).Text; !res.IsError || !strings.Contains(text, "not an upgrade answer") {
		t.Errorf("check_upgrade on a catalog answer: isError=%v %s", res.IsError, text)
	}
	if !isJSONObject([]byte(" \n{\"a\":1}")) || isJSONObject([]byte(`[1]`)) || isJSONObject([]byte(`{"a":`)) || isJSONObject(nil) {
		t.Error("isJSONObject")
	}
}

// A target above every analyzed release used to come back as an empty (or
// short) range with no word: etcd v3.7.3 with target_version v3.7.4 read as
// "nothing between here and there". The component is still checked up to the
// newest analyzed release, and the note says the target itself was not.
func TestCheckStackNotesATargetNewerThanEveryAnalyzedRelease(t *testing.T) {
	swapAPI(t, httptest.NewServer(newFakeEtcd().handler(t)))
	out := runStack(t, checkStackArgs{Components: []stackComponent{
		{Project: "etcd", Version: "v3.6.8", TargetVersion: "v3.6.9"},
		{Project: "etcd", Version: "v3.6.8"},
	}})
	if len(out.CouldNotCheck) != 0 {
		t.Fatalf("could_not_check %+v, want none: the range up to the newest analyzed release is still checked", out.CouldNotCheck)
	}
	if c := out.Components[0]; !strings.Contains(c.Note, "target_version v3.6.9 is newer than every analyzed release on its line (newest analyzed: v3.6.8)") {
		t.Errorf("a target above the newest analyzed release must be named in the note: %q", c.Note)
	}
	if c := out.Components[1]; strings.Contains(c.Note, "target_version") {
		t.Errorf("no target, no target note: %q", c.Note)
	}
}
