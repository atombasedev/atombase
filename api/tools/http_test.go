package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseHeaderCommas(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "single value",
			input: []string{"operation=select"},
			want:  []string{"operation=select"},
		},
		{
			name:  "comma separated values",
			input: []string{"operation=select, count=exact"},
			want:  []string{"operation=select", "count=exact"},
		},
		{
			name:  "multiple headers and empty parts",
			input: []string{"operation=insert, , on-conflict=replace", "count=exact"},
			want:  []string{"operation=insert", "on-conflict=replace", "count=exact"},
		},
		{
			name:  "trims whitespace",
			input: []string{"  one  , two ", " three "},
			want:  []string{"one", "two", "three"},
		},
		{
			name:  "empty input",
			input: nil,
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseHeaderCommas(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d parts, got %d (%v)", len(tt.want), len(got), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("expected part %d to be %q, got %q", i, tt.want[i], got[i])
				}
			}
		})
	}
}

func TestRequest_SuccessWithJSONBodyAndHeaders(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test" {
			t.Fatalf("expected Authorization header, got %q", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}

		var got payload
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if got.Name != "alice" {
			t.Fatalf("expected payload name alice, got %q", got.Name)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	resp, err := Request(http.MethodPost, server.URL, map[string]string{
		"Authorization": "Bearer test",
	}, payload{Name: "alice"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestRequest_SuccessWithoutBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		if len(body) != 0 {
			t.Fatalf("expected empty body, got %q", string(body))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := Request(http.MethodGet, server.URL, nil, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer resp.Body.Close()
}

func TestRequest_Non200ReturnsBodyError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request body"))
	}))
	defer server.Close()

	resp, err := Request(http.MethodGet, server.URL, nil, nil)
	if resp == nil {
		t.Fatal("expected response to be returned on non-200")
	}
	defer resp.Body.Close()

	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "bad request body" {
		t.Fatalf("expected body-backed error, got %q", err.Error())
	}
}
