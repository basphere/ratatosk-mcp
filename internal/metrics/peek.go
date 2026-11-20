package metrics

// Naming a request from the first bytes of its body, for a POST the server
// never ran (see metricsPeekBytes for why this is bounded and single-pass).

import (
	"bytes"
	"encoding/json"
)

// peekLabels reads the JSON-RPC method and, for tools/call, params.name from
// the beginning of a request body. The prefix may end mid-value; whatever was
// read before that counts. Anything not a single JSON object (a batch, a
// response, garbage) has no method. One pass over b, no recursion, and no
// allocation but the two strings it returns, decoded once at the end — it is
// not a validator: the SDK judges the body, this only names what it was for,
// and each name ends up as a closed-list label anyway. Keys are read as the
// SDK's decoder reads them (segmentio/encoding/json, case-sensitive): escapes
// decoded ("\u006dethod" is method), and a key given twice is its last value
// (a later "params" replaces an earlier one whole). Checked against the SDK
// on 2026-11-18; a 202 is labelled without this scanner anyway.
func peekLabels(b []byte) (method, tool string) {
	var m, n []byte // the last method and params.name strings seen, quotes included
	sc := jsonScan{b: b}
	defer func() { method, tool = unquote(m), unquote(n) }()
	if !sc.next('{') {
		return
	}
	for {
		key, ok := sc.str()
		if !ok || !sc.next(':') {
			return
		}
		switch {
		case keyIs(key, "method"):
			m = nil
			if sc.peek() != '"' {
				if !sc.skipValue() {
					return
				}
				break
			}
			if m, ok = sc.str(); !ok {
				m = nil
				return
			}
		case keyIs(key, "params") && sc.peek() == '{':
			n = nil
			sc.i++
			if sc.peek() != '}' {
				for {
					pk, ok := sc.str()
					if !ok || !sc.next(':') {
						return
					}
					if keyIs(pk, "name") {
						n = nil
						if sc.peek() == '"' {
							if n, ok = sc.str(); !ok {
								n = nil
								return
							}
						} else if !sc.skipValue() {
							return
						}
					} else if !sc.skipValue() {
						return
					}
					if !sc.next(',') {
						break
					}
				}
			}
			if !sc.next('}') {
				return
			}
		default:
			if keyIs(key, "params") {
				n = nil // a params that is not an object names no tool
			}
			if !sc.skipValue() {
				return
			}
		}
		if !sc.next(',') {
			return
		}
	}
}

// keyIs says the JSON string raw (quotes included) decodes to want, an ASCII
// key, without allocating: escapes are decoded, case counts.
func keyIs(raw []byte, want string) bool {
	if len(raw) < 2 {
		return false
	}
	b, i := raw[1:len(raw)-1], 0
	for j := 0; j < len(b); j++ {
		c := b[j]
		if c == '\\' {
			if j++; j >= len(b) {
				return false
			}
			switch b[j] {
			case '"', '\\', '/':
				c = b[j]
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'u':
				if j+4 >= len(b) {
					return false
				}
				r := 0
				for _, h := range b[j+1 : j+5] {
					switch {
					case '0' <= h && h <= '9':
						r = r<<4 | int(h-'0')
					case 'a' <= h && h <= 'f':
						r = r<<4 | int(h-'a'+10)
					case 'A' <= h && h <= 'F':
						r = r<<4 | int(h-'A'+10)
					default:
						return false
					}
				}
				if r >= 0x80 { // want is ASCII
					return false
				}
				c, j = byte(r), j+4
			default:
				return false
			}
		}
		if i >= len(want) || want[i] != c {
			return false
		}
		i++
	}
	return i == len(want)
}

// unquote decodes one JSON string, quotes included; "" for nil or invalid.
func unquote(q []byte) string {
	if len(q) < 2 {
		return ""
	}
	if !bytes.ContainsRune(q, '\\') {
		return string(q[1 : len(q)-1])
	}
	var v string
	if json.Unmarshal(q, &v) != nil {
		return ""
	}
	return v
}

// jsonScan walks a JSON prefix one byte at a time; i only moves forward.
type jsonScan struct {
	b []byte
	i int
}

// peek skips white space and returns the next byte, 0 at the end.
func (s *jsonScan) peek() byte {
	for s.i < len(s.b) {
		switch c := s.b[s.i]; c {
		case ' ', '\t', '\n', '\r':
			s.i++
		default:
			return c
		}
	}
	return 0
}

// next consumes c if it comes next.
func (s *jsonScan) next(c byte) bool {
	if s.peek() != c {
		return false
	}
	s.i++
	return true
}

// str reads a string and returns it as written, quotes included; false when
// none comes next or it is cut short.
func (s *jsonScan) str() ([]byte, bool) {
	if !s.next('"') {
		return nil, false
	}
	start := s.i - 1
	for ; s.i < len(s.b); s.i++ {
		switch s.b[s.i] {
		case '\\':
			s.i++
		case '"':
			s.i++
			return s.b[start:s.i], true
		}
	}
	return nil, false
}

// skipValue reads past one value of any kind; false when the input ends
// first. Objects and arrays are skipped by counting brackets outside strings.
func (s *jsonScan) skipValue() bool {
	switch s.peek() {
	case 0:
		return false
	case '"':
		_, ok := s.str()
		return ok
	case '{', '[':
		for depth := 0; s.i < len(s.b); {
			switch s.b[s.i] {
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					s.i++
					return true
				}
			case '"':
				if _, ok := s.str(); !ok {
					return false
				}
				continue
			}
			s.i++
		}
		return false
	}
	for ; s.i < len(s.b); s.i++ { // a number, true, false or null
		switch s.b[s.i] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			return true
		}
	}
	return false
}
