package tools

// The calls a route hands the caller ready to make: a step's next (the tool
// that was called, for that step alone, as a briefing) and its verify (the
// get_release call that reads the evidence of its must_read lines).

// toolCall is a tool call ready to make.
type toolCall struct {
	Tool      string `json:"tool"`
	Arguments any    `json:"arguments"`
}

type upgradeCallArgs struct {
	Project string   `json:"project"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Detail  string   `json:"detail"`
	Kinds   []string `json:"kinds,omitempty"`
}

type stackCallArgs struct {
	Components []stackCallComponent `json:"components"`
	Detail     string               `json:"detail"`
	Kinds      []string             `json:"kinds,omitempty"`
}

type stackCallComponent struct {
	Project       string `json:"project"`
	Version       string `json:"version"`
	TargetVersion string `json:"target_version"`
}

type verifyArgs struct {
	Project string   `json:"project"`
	Changes []string `json:"changes"`
}

// verifyCalls is the get_release call that reads the evidence of a step's
// must_read lines: one call, or several when the ids pass what one call takes
// (maxPickedChanges ids, maxPickedReleases releases). nil when there is none.
func verifyCalls(project string, lines []mustReadLine) any {
	if len(lines) == 0 {
		return nil
	}
	var calls []*toolCall
	var ids []string
	rels := map[string]bool{}
	flush := func() {
		if len(ids) > 0 {
			calls = append(calls, &toolCall{Tool: "get_release", Arguments: verifyArgs{Project: project, Changes: ids}})
		}
		ids, rels = nil, map[string]bool{}
	}
	for _, l := range lines {
		_, v, _ := releaseOfID(l.ID)
		if len(ids) == maxPickedChanges || (!rels[v] && len(rels) == maxPickedReleases) {
			flush()
		}
		ids = append(ids, l.ID)
		rels[v] = true
	}
	flush()
	if len(calls) == 1 {
		return calls[0]
	}
	return calls
}
