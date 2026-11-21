// Package release holds a release as the ratatosk agent API v2 answers it: a
// row of the release list, and a release document with its before-upgrade
// items and its changes in seven kinds. It holds the shapes and nothing that
// fetches them.
//
// Why a package of its own: the code that decides on releases reads these
// shapes and must not be able to reach the network, so the shapes live apart
// from the client that fetches them (internal/upstream); a test at the
// repository root holds those packages to it. A briefing is a function of the
// documents it is given.
//
// v2 returns the same shape the website shows, so the tools mostly pass it
// through; only the fields the briefing reads are spelled out here, and
// unknown fields are ignored so the server can add fields without breaking
// this client (the v2 numbering rule: additions never change the path,
// removals and renames move it to /v3).
package release

import "encoding/json"

// Kinds are the seven kinds of change a release carries, in screen order.
// Never modified.
var Kinds = []string{"security", "breaking", "deprecations", "important_fixes", "enhancements", "dependency_updates", "other"}

// Row is one row of /v2/releases — a release without its changes.
type Row struct {
	Project    string         `json:"project"`
	Version    string         `json:"version"`
	ReleasedAt string         `json:"released_at"`
	URL        string         `json:"url"`
	Counts     map[string]int `json:"counts"`
	Warnings   map[string]int `json:"warnings"`
}

// Attr is an attribute that says whether the release notes stated it:
// status stated | inferred | not_stated | not_read.
type Attr struct {
	Text   string `json:"text"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Item is a before-upgrade item (prerequisites, known_issues, upgrade_guide).
type Item struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Body  *string `json:"body"`
	// Folded holds entries kept under the item ({title, body}); passed on as
	// received (a briefing's before_upgrade carries them since 1.2.0).
	Folded []json.RawMessage `json:"folded"`
	// Evidence holds the release-note lines the item was read from
	// ({quote, source, url}); passed on as received.
	Evidence []json.RawMessage `json:"evidence"`
}

// Change is one change of a release.
type Change struct {
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Title           string            `json:"title"`
	Body            *string           `json:"body"`
	Severity        *string           `json:"severity"`
	Advisories      []string          `json:"advisories"`
	Affects         *Attr             `json:"affects"`
	ActionRequired  *string           `json:"action_required"`
	AlsoRequired    *string           `json:"also_required"`
	NoDowngrade     *Attr             `json:"no_downgrade"`
	RemovedIn       *Attr             `json:"removed_in"`
	SecurityUnclear bool              `json:"security_unclear"`
	SilentFailure   bool              `json:"silent_failure"`
	Workaround      *Attr             `json:"workaround"`
	RegressionSince *Attr             `json:"regression_since"`
	Later           []json.RawMessage `json:"later"`
}

// Doc is GET /v2/releases/{project}/{version}. Raw keeps the document as
// received for detail:"full", so nothing the server adds is lost on the way.
type Doc struct {
	Project       string         `json:"project"`
	Version       string         `json:"version"`
	ReleasedAt    string         `json:"released_at"`
	URL           string         `json:"url"`
	Counts        map[string]int `json:"counts"`
	Warnings      map[string]int `json:"warnings"`
	Processing    bool           `json:"processing"`
	BeforeUpgrade struct {
		Prerequisites []Item `json:"prerequisites"`
		KnownIssues   []Item `json:"known_issues"`
		UpgradeGuide  []Item `json:"upgrade_guide"`
	} `json:"before_upgrade"`
	Changes []Change          `json:"changes"`
	Later   []json.RawMessage `json:"later"`
	Raw     json.RawMessage   `json:"-"`
}

// Parse reads one release document and keeps it as received in Raw.
func Parse(b []byte) (*Doc, error) {
	var r Doc
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	r.Raw = b
	return &r, nil
}
