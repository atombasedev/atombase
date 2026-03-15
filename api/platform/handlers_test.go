package platform

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/tools"
)

func TestValidateResourceName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCode  string
		wantValid bool
	}{
		// Valid names
		{"lowercase", "my-database", "", true},
		{"with numbers", "tenant123", "", true},
		{"dashes", "a-b-c", "", true},
		{"single char", "a", "", true},
		{"64 chars", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", true},

		// Invalid: uppercase
		{"uppercase start", "My-Database", tools.CodeInvalidName, false},
		{"uppercase middle", "myTenant", tools.CodeInvalidName, false},
		{"all uppercase", "TENANT", tools.CodeInvalidName, false},

		// Invalid: special characters
		{"underscore", "tenant_name", tools.CodeInvalidName, false},
		{"dot", "database.name", tools.CodeInvalidName, false},
		{"space", "database name", tools.CodeInvalidName, false},
		{"at sign", "database@name", tools.CodeInvalidName, false},

		// Invalid: too long
		{"65 chars", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tools.CodeInvalidName, false},
		{"100 chars", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tools.CodeInvalidName, false},

		// Edge cases
		{"empty string", "", tools.CodeInvalidName, false},
		{"only dash", "-", "", true},
		{"starts with dash", "-database", "", true},
		{"ends with dash", "database-", "", true},
		{"only numbers", "12345", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, msg, hint := validateResourceName(tt.input)
			if tt.wantValid {
				if code != "" {
					t.Errorf("validateResourceName(%q) = (%q, %q, %q), want valid", tt.input, code, msg, hint)
				}
			} else {
				if code != tt.wantCode {
					t.Errorf("validateResourceName(%q) code = %q, want %q", tt.input, code, tt.wantCode)
				}
				if msg == "" {
					t.Errorf("validateResourceName(%q) message should not be empty for invalid input", tt.input)
				}
				if hint == "" {
					t.Errorf("validateResourceName(%q) hint should not be empty for invalid input", tt.input)
				}
			}
		})
	}
}

func TestHandleCreateTemplate_ValidationErrors(t *testing.T) {
	api := &API{}

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "invalid json",
			body:       `{"name":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   tools.CodeInvalidJSON,
			wantMsg:    tools.ErrInvalidJSON.Error(),
		},
		{
			name:       "missing name",
			body:       `{"schema":{"tables":[{"name":"users","pk":["id"],"columns":{"id":{"name":"id","type":"INTEGER"}}}]}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   tools.CodeInvalidRequest,
			wantMsg:    "name is required",
		},
		{
			name:       "invalid name",
			body:       `{"name":"BadName","schema":{"tables":[{"name":"users","pk":["id"],"columns":{"id":{"name":"id","type":"INTEGER"}}}]}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   tools.CodeInvalidRequest,
			wantMsg:    "name contains invalid characters",
		},
		{
			name:       "empty schema",
			body:       `{"name":"valid-name","schema":{"tables":[]}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   tools.CodeInvalidRequest,
			wantMsg:    "schema must have at least one table",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/platform/templates", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			api.handleCreateTemplate(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}

			var apiErr tools.APIError
			if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
				t.Fatalf("failed to decode error response: %v", err)
			}

			if apiErr.Code != tt.wantCode {
				t.Fatalf("expected code %q, got %q", tt.wantCode, apiErr.Code)
			}
			if apiErr.Message != tt.wantMsg {
				t.Fatalf("expected message %q, got %q", tt.wantMsg, apiErr.Message)
			}
		})
	}
}

func TestHandleDiffTemplate_InvalidJSON(t *testing.T) {
	api := &API{}
	req := httptest.NewRequest(http.MethodPost, "/platform/templates/my-template/diff", strings.NewReader(`{"schema":`))
	req.SetPathValue("name", "my-template")
	rec := httptest.NewRecorder()

	api.handleDiffTemplate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var apiErr tools.APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if apiErr.Code != tools.CodeInvalidJSON {
		t.Fatalf("expected code %q, got %q", tools.CodeInvalidJSON, apiErr.Code)
	}
}

func TestWithBody_EnforcesMaxRequestBody(t *testing.T) {
	originalLimit := config.Cfg.MaxRequestBody
	config.Cfg.MaxRequestBody = 8
	defer func() {
		config.Cfg.MaxRequestBody = originalLimit
	}()

	handler := withBody(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/platform/templates", strings.NewReader("123456789"))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status %d, got %d", http.StatusRequestEntityTooLarge, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "request body too large") {
		t.Fatalf("expected body too large error, got %q", rec.Body.String())
	}
}

func TestRespondJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	respondJSON(rec, http.StatusCreated, map[string]any{
		"ok":   true,
		"name": "template-a",
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content-type application/json, got %q", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body["ok"])
	}
	if body["name"] != "template-a" {
		t.Fatalf("expected name template-a, got %v", body["name"])
	}
}
