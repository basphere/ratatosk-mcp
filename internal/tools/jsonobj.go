package tools

// Reading part of an upstream answer and passing the rest on as it came.
//
// Why this exists (1.2.0): kinds, changes and search's project filter cut
// arrays out of answers that were passed through verbatim until now. Decoding
// them into Go maps and encoding again would sort every key and drop nothing,
// but it would reorder the server's fields, and a struct would drop the fields
// the server adds later (internal/release: unknown fields are ignored so the
// server can add them). These helpers keep each object's keys in the order
// received and every value byte for byte, and touch only the arrays a filter
// names.

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// objField is one key of a JSON object, its value kept as received.
type objField struct {
	Key string
	Val json.RawMessage
}

// readObject splits a JSON object into its fields, in order.
func readObject(b []byte) ([]objField, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	var out []objField
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("object key is not a string")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, objField{Key: k, Val: v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

// writeObject joins fields back into one object, in their order.
func writeObject(fields []objField) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(f.Key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(f.Val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// writeArray joins raw elements into one array.
func writeArray(elems []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, e := range elems {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(e)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// docFilter says what a release document keeps when it is cut down. A nil
// filter keeps the document whole.
type docFilter struct {
	// kinds keeps only the changes of these kinds; empty keeps every kind.
	kinds map[string]bool
	// ids keeps only the changes and before-upgrade items named here; nil keeps
	// every one. found records the ids that named something in the document.
	ids   map[string]bool
	found map[string]bool
	// foundUpgrade records the ids that named a before-upgrade item.
	foundUpgrade map[string]bool
}

func (f *docFilter) keepChange(id, kind string) bool {
	if len(f.kinds) > 0 && !f.kinds[kind] {
		return false
	}
	if f.ids != nil {
		if !f.ids[id] {
			return false
		}
	}
	return true
}

// filterRelease cuts one /v2 release document down to what f keeps:
// changes of the kinds asked for and, with ids, only the named changes and
// before-upgrade items. folded is left out whenever a filter applies: it holds
// lines that are not changes of any kind (dependency bumps the notes do not tie
// to security, entries shipped earlier), and on cilium v1.20.0 it is 21,000
// characters — more than the one change an id names (measured 2026-11-20).
// Every other field is passed on as received.
func filterRelease(raw []byte, f *docFilter) ([]byte, error) {
	if f == nil {
		return raw, nil
	}
	fields, err := readObject(raw)
	if err != nil {
		return nil, err
	}
	out := fields[:0]
	for _, fl := range fields {
		switch fl.Key {
		case "folded":
			continue
		case "changes":
			var elems []json.RawMessage
			if err := json.Unmarshal(fl.Val, &elems); err != nil {
				return nil, fmt.Errorf("changes: %w", err)
			}
			kept := []json.RawMessage{}
			for _, e := range elems {
				var c struct {
					ID   string `json:"id"`
					Kind string `json:"kind"`
				}
				if err := json.Unmarshal(e, &c); err != nil {
					return nil, fmt.Errorf("change: %w", err)
				}
				if f.ids != nil && f.ids[c.ID] {
					f.found[c.ID] = true
				}
				if f.keepChange(c.ID, c.Kind) {
					kept = append(kept, e)
				}
			}
			fl.Val = writeArray(kept)
		case "before_upgrade":
			if f.ids == nil {
				break
			}
			secs, err := readObject(fl.Val)
			if err != nil {
				return nil, fmt.Errorf("before_upgrade: %w", err)
			}
			for i, s := range secs {
				var elems []json.RawMessage
				if err := json.Unmarshal(s.Val, &elems); err != nil {
					continue // not an item list: passed on as received
				}
				kept := []json.RawMessage{}
				for _, e := range elems {
					var it struct {
						ID string `json:"id"`
					}
					if json.Unmarshal(e, &it) == nil && f.ids[it.ID] {
						f.found[it.ID] = true
						if f.foundUpgrade != nil {
							f.foundUpgrade[it.ID] = true
						}
						kept = append(kept, e)
					}
				}
				secs[i].Val = writeArray(kept)
			}
			fl.Val = writeObject(secs)
		}
		out = append(out, fl)
	}
	return writeObject(out), nil
}
