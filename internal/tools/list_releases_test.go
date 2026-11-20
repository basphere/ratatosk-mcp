package tools

// list_releases: what it refuses, and the limit it sends.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListReleasesValidates(t *testing.T) {
	h := withAPI(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("limit") != "10" || q.Get("kind") != "security" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"releases":[],"next_before":null}`))
	})))
	if res, _, _ := h.listReleasesTool(context.Background(), nil, listReleasesArgs{Kind: "security"}); res.IsError {
		t.Fatalf("valid call refused: %+v", res)
	}
	if res, _, _ := h.listReleasesTool(context.Background(), nil, listReleasesArgs{Kind: "action"}); !res.IsError {
		t.Fatal("a 0.x bucket name was accepted as a kind")
	}
	if res, _, _ := h.listReleasesTool(context.Background(), nil, listReleasesArgs{Since: "last week"}); !res.IsError {
		t.Fatal("a non-date since was accepted")
	}
}
