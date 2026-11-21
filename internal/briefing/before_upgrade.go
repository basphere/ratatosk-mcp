package briefing

import (
	"encoding/json"
	"strings"

	"github.com/basphere/ratatosk-mcp/internal/release"
)

// briefItem is a before_upgrade item as 1.0.0 and 1.1.0 answered it. Only the
// frozen 1.0.0 briefing builds it; 1.2.0 answers with [UpgradeItem].
type briefItem struct {
	Version string `json:"version"`
	Section string `json:"section"` // prerequisites | known_issues | upgrade_guide
	ID      string `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
}

// UpgradeItem is a before_upgrade item of a briefing (1.2.0). The section an
// operator must read before upgrading is the one a briefing cut hardest: a
// title only, the body cut to 160 characters or left out when empty, and the
// item's folded entries never carried. cilium v1.19.9 → v1.20.5 answered
// {"title": "legacy features and CNI configuration upgrade notes"} and lost
// the warning folded under it — install Gateway API v1.6.1 before upgrading
// or TLSRoute objects are lost (reproduced 2026-11-19). It is a few items per
// range, so they go out whole: body (null when the item has none) and folded,
// as get_release has them. An item whose text recurs on several releases (the
// same fingerprint after "#" in the same section) is listed once, at the
// newest release, with also_in naming the others: containerd v2.2.9 → v2.4.2
// carried "runc and CNI plugins are installed separately" six times, 17 items
// in all for 8 different texts. The newest is kept because each release words
// its card anew and the newest wording is the latest reading.
//
// Evidence (1.3.0) is carried only by an item without a body. The card writer
// leaves the body empty when it has nothing to add to the title, and the
// website shows the release-note lines under such an item; a briefing showed
// the title alone. 498 of the 858 before_upgrade items on record had no body
// (2026-11-21), and for 92 of them the reading named identifiers or versions
// the title does not: cilium v1.20.0 answered "Upgrade guidance for legacy
// features and CNI configurations" where the note names Mutual Authentication,
// Kafka-aware policies, the v2alpha1 CiliumNodeConfig API and custom CNI
// configuration. Two agents asked to read every before_upgrade item had to
// call get_release for those lines (2026-11-21).
type UpgradeItem struct {
	Version string            `json:"version"`
	Section string            `json:"section"` // prerequisites | known_issues | upgrade_guide
	ID      string            `json:"id"`
	Title   string            `json:"title"`
	Body    *string           `json:"body"`
	Folded  []json.RawMessage `json:"folded,omitempty"`
	// Evidence: the item's evidence as get_release has it, when Body is nil
	// or empty.
	Evidence json.RawMessage `json:"evidence,omitempty"`
	AlsoIn   []string        `json:"also_in,omitempty"`
}

// beforeUpgradeOf lists the before_upgrade items of the releases in range
// (oldest first), recurring texts grouped (see UpgradeItem). Never nil: an
// empty list and a component that was not checked must stay apart.
func beforeUpgradeOf(docs []*release.Doc) []UpgradeItem {
	var all []UpgradeItem
	var keys []string
	newest := map[string]int{}
	for _, d := range docs {
		for _, sec := range []struct {
			name  string
			items []release.Item
		}{{"prerequisites", d.BeforeUpgrade.Prerequisites}, {"known_issues", d.BeforeUpgrade.KnownIssues}, {"upgrade_guide", d.BeforeUpgrade.UpgradeGuide}} {
			for _, it := range sec.items {
				k := ""
				if _, fp, ok := strings.Cut(it.ID, "#"); ok && fp != "" {
					k = sec.name + "#" + fp
					newest[k] = len(all)
				}
				var folded []json.RawMessage
				if len(it.Folded) > 0 {
					folded = it.Folded
				}
				var evidence json.RawMessage
				if e := strings.TrimSpace(string(it.Evidence)); (it.Body == nil || strings.TrimSpace(*it.Body) == "") && e != "" && e != "null" && e != "[]" {
					evidence = it.Evidence
				}
				all = append(all, UpgradeItem{Version: d.Version, Section: sec.name, ID: it.ID, Title: it.Title, Body: it.Body, Folded: folded, Evidence: evidence})
				keys = append(keys, k)
			}
		}
	}
	out := []UpgradeItem{}
	earlier := map[string][]string{}
	for i, it := range all {
		k := keys[i]
		if k != "" && newest[k] != i {
			earlier[k] = append(earlier[k], it.Version)
			continue
		}
		if k != "" {
			it.AlsoIn = earlier[k]
		}
		out = append(out, it)
	}
	return out
}
