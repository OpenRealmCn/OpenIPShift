package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessToken(t *testing.T) {
	for _, configured := range []string{"", "test-access-token"} {
		t.Setenv("ROTATOR_API_TOKEN", configured)
		for _, path := range []string{"/api/state", "/api/tasks/task", "/api/nodes", "/api/rotations"} {
			for _, supplied := range []string{"", "wrong", "test-access-token"} {
				req := httptest.NewRequest("GET", path, nil)
				req.Header.Set("Authorization", "Bearer "+supplied)
				out := httptest.NewRecorder()
				auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(out, req)
				want := 401
				if configured != "" && supplied == configured {
					want = 204
				}
				if out.Code != want {
					t.Fatalf("%s: got %d want %d", path, out.Code, want)
				}
			}
		}
	}
}
