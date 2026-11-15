package main

// Tests for what the independent review before the 1.0 launch found on
// 2026-11-15. The worst kind of answer an agent can get is one that reads as
// "nothing to do" without an error; most of these replay one.

import (
	"context"
	"strings"
	"testing"
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
