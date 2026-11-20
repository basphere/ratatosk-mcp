package upstream

// The response cache stays inside its byte budget.

import (
	"testing"
	"time"
)

func TestRespCacheByteBudget(t *testing.T) {
	c := newRespCache(time.Minute, 100, 1000)
	c.put("a", make([]byte, 100))
	c.put("b", make([]byte, 100))
	if _, ok := c.get("a"); !ok {
		t.Fatal("a should be cached")
	}
	c.put("big", make([]byte, 200)) // over maxBytes/8 — not cached
	if _, ok := c.get("big"); ok {
		t.Fatal("a body over an eighth of the budget must not be cached")
	}
	for i := 0; i < 20; i++ {
		c.put(string(rune('c'+i)), make([]byte, 100))
		if c.size > c.maxBytes {
			t.Fatalf("size %d over budget %d", c.size, c.maxBytes)
		}
	}
	c.put("b", make([]byte, 50)) // replacing a key must not double-count
	total := 0
	for _, e := range c.data {
		total += len(e.body)
	}
	if total != c.size {
		t.Fatalf("tracked size %d, actual %d", c.size, total)
	}
}
