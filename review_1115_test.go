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
