package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/tools"
)

func generateSchemaSQL(schema Schema) []string {
	statements := make([]string, 0, len(schema.Tables))
	for _, table := range schema.Tables {
		statements = append(statements, generateCreateTableSQL(table))
		for _, idx := range table.Indexes {
			statements = append(statements, generateCreateIndexSQL(table.Name, idx))
		}
		if len(table.FTSColumns) > 0 {
			statements = append(statements, generateFTSSQL(table.Name, table.FTSColumns, table.Pk)...)
		}
	}
	return statements
}

func decodeStoredDatabaseToken(storedToken []byte) (string, error) {
	if len(storedToken) == 0 {
		return "", nil
	}
	if !tools.EncryptionEnabled() {
		return string(storedToken), nil
	}
	decrypted, err := tools.Decrypt(storedToken)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt auth token: %w", err)
	}
	return string(decrypted), nil
}

var (
	tursoCreateDatabaseFn = tursocreateDatabase
	tursoDeleteDatabaseFn = tursodeleteDatabase
	tursoCreateTokenFn    = tursoCreateToken
)

func tursocreateDatabase(ctx context.Context, name string) error {
	url := fmt.Sprintf("https://api.turso.tech/v1/organizations/%s/databases", config.Cfg.TursoOrganization)
	group := strings.TrimSpace(config.Cfg.TursoGroup)
	if group == "" {
		group = "default"
	}
	body, _ := json.Marshal(map[string]any{
		"name":  name,
		"group": group,
	})
	return doTursoJSON(ctx, http.MethodPost, url, body, nil)
}

func tursodeleteDatabase(ctx context.Context, name string) error {
	url := fmt.Sprintf("https://api.turso.tech/v1/organizations/%s/databases/%s", config.Cfg.TursoOrganization, name)
	return doTursoJSON(ctx, http.MethodDelete, url, nil, nil)
}

func tursoCreateToken(ctx context.Context, name string) (string, error) {
	url := fmt.Sprintf("https://api.turso.tech/v1/organizations/%s/databases/%s/auth/tokens", config.Cfg.TursoOrganization, name)
	var resp struct {
		JWT string `json:"jwt"`
	}
	if err := doTursoJSON(ctx, http.MethodPost, url, []byte(`{"authorization":"full-access"}`), &resp); err != nil {
		return "", err
	}
	return resp.JWT, nil
}

func doTursoJSON(ctx context.Context, method, url string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+config.Cfg.TursoAPIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("turso api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
