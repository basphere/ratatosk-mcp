package briefing

// Narrow settles which changes a briefing lists, for the kinds asked for
// (1.2.0 kinds; none asked = all four decision kinds).
//
// changes_more is whole for security, breaking and deprecations, always: those
// are what an upgrade decision turns on, and a change that is not listed is one
// the reader does not know to ask for (the misses measured on 2026-11-20 were
// all changes a briefing did not carry, in runs that never opened the release
// they sat on). Important fixes past their cap stay a count in changes_omitted
// unless kinds names important_fixes — then every one of them is listed too.
//
// Nothing here looks at the size of the answer. An earlier build of 1.2.0 cut
// answers to 40,000 characters so that one client would not save them to a
// file, and on a five-component stack years behind it answered no entry with a
// body and none of kubernetes' 33 security changes (2026-11-20). The server
// does not guess what a client can hold: it lists what there is, and the
// caller narrows the call — kinds, one component, target_version, same_minor.
//
// summary, releases, before_upgrade and the rest describe the range and stay
// whole; only the change lists and their counts are narrowed.
func (b *Briefing) Narrow(k map[string]bool) {
	var lines []MoreLine
	for _, l := range b.ChangesMore {
		if l.Kind == "important_fixes" && !k["important_fixes"] {
			continue
		}
		if len(k) == 0 || k[l.Kind] {
			lines = append(lines, l)
		}
	}
	b.ChangesMore = lines
	if len(k) == 0 {
		return
	}
	var ch []Change
	for _, c := range b.Changes {
		if k[c.Kind] {
			ch = append(ch, c)
		}
	}
	b.Changes = ch
	for kind := range b.ChangesOmitted {
		if !k[kind] {
			delete(b.ChangesOmitted, kind)
		}
	}
	if len(b.ChangesOmitted) == 0 {
		b.ChangesOmitted = nil
	}
}
