package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/atombasedev/atombase/config"
)

// Turso HTTP Pipeline API types
// See: https://docs.turso.tech/sdk/http/quickstart

type batchRequest struct {
	Requests []pipelineStatement `json:"requests"`
}

type pipelineStatement struct {
	Type string    `json:"type"`
	Stmt *stmtBody `json:"stmt,omitempty"`
}

type stmtBody struct {
	SQL string `json:"sql"`
}

type batchResponse struct {
	Results []pipelineResult `json:"results"`
}

type pipelineResult struct {
	Type     string         `json:"type"`
	Response *resultDetails `json:"response,omitempty"`
	Error    *pipelineError `json:"error,omitempty"`
}

type resultDetails struct {
	Type            string `json:"type"`
	AffectedRows    int    `json:"affected_row_count,omitempty"`
	LastInsertRowID int64  `json:"last_insert_rowid,omitempty"`
}

type pipelineError struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// BatchExecuteWithToken sends multiple SQL statements to a Turso database using a per-database token.
// Turso automatically executes all statements as a single transaction - if any statement
// fails, the entire batch is rolled back.
//
// This is significantly more efficient than individual ExecContext calls:
// - 1 HTTP round-trip instead of N
// - Turso benchmarks show ~3x improvement (5.3s for 10 individual queries vs 1.7s for 1000 batched)
func BatchExecuteWithToken(ctx context.Context, dbName, token string, statements []string) error {
	if len(statements) == 0 {
		return nil
	}

	org := config.Cfg.TursoOrganization
	if org == "" {
		return fmt.Errorf("TURSO_ORGANIZATION is not set")
	}

	if token == "" {
		return fmt.Errorf("auth token is required")
	}

	// Build pipeline request
	requests := make([]pipelineStatement, 0, len(statements)+1)
	for _, sql := range statements {
		requests = append(requests, pipelineStatement{
			Type: "execute",
			Stmt: &stmtBody{SQL: sql},
		})
	}
	requests = append(requests, pipelineStatement{Type: "close"})

	body, err := json.Marshal(batchRequest{Requests: requests})
	if err != nil {
		return fmt.Errorf("failed to marshal batch request: %w", err)
	}

	// POST to Turso pipeline API
	url := fmt.Sprintf("https://%s-%s.turso.io/v2/pipeline", dbName, org)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("batch request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		var errBody bytes.Buffer
		errBody.ReadFrom(resp.Body)
		return fmt.Errorf("turso pipeline error: %s - %s", resp.Status, errBody.String())
	}

	var batchResp batchResponse
	if err := json.NewDecoder(resp.Body).Decode(&batchResp); err != nil {
		return fmt.Errorf("failed to parse batch response: %w", err)
	}

	// Check for statement errors
	for i, result := range batchResp.Results {
		if result.Type == "error" && result.Error != nil {
			return fmt.Errorf("statement %d failed: %s", i+1, result.Error.Message)
		}
	}

	return nil
}
