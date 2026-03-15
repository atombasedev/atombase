package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/atombasedev/atombase/tools"
	_ "github.com/mattn/go-sqlite3"
)

type testOrganizationStore struct {
	db         *sql.DB
	databaseID string
	authToken  string
}

func (s testOrganizationStore) DB() *sql.DB {
	return s.db
}

func (s testOrganizationStore) LookupOrganizationTenant(ctx context.Context, organizationID string) (string, string, error) {
	var resolvedID string
	err := s.db.QueryRowContext(ctx, `SELECT database_id FROM atombase_organizations WHERE id = ?`, organizationID).Scan(&resolvedID)
	if err != nil {
		return "", "", err
	}
	return resolvedID, s.authToken, nil
}

func (s testOrganizationStore) LookupOrganizationAuthz(ctx context.Context, organizationID string) (string, string, ManagementMap, error) {
	databaseID, authToken, err := s.LookupOrganizationTenant(ctx, organizationID)
	if err != nil {
		return "", "", nil, err
	}
	return databaseID, authToken, ManagementMap{
		"owner": {
			Invite:            ManagementPermission{Any: true},
			AssignRole:        ManagementPermission{Any: true},
			RemoveMember:      ManagementPermission{Any: true},
			UpdateOrg:         true,
			DeleteOrg:         true,
			TransferOwnership: true,
		},
		"admin": {
			Invite:       ManagementPermission{Roles: []string{"member", "viewer"}},
			AssignRole:   ManagementPermission{Roles: []string{"member", "viewer"}},
			RemoveMember: ManagementPermission{Roles: []string{"member", "viewer"}},
		},
	}, nil
}

func (s testOrganizationStore) DeleteOrganization(ctx context.Context, organizationID string) error {
	var databaseID string
	if err := s.db.QueryRowContext(ctx, `SELECT database_id FROM atombase_organizations WHERE id = ?`, organizationID).Scan(&databaseID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM atombase_organizations WHERE id = ?`, organizationID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM atombase_databases WHERE id = ?`, databaseID)
	return err
}

func setupOrganizationMembershipAPI(t *testing.T) (*API, *sql.DB, string) {
	t.Helper()

	db := setupAuthTestDB(t)
	for _, stmt := range []string{
		`CREATE TABLE atombase_definitions (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			definition_type TEXT NOT NULL,
			current_version INTEGER NOT NULL
		)`,
		`CREATE TABLE atombase_databases (
			id TEXT PRIMARY KEY,
			definition_id INTEGER NOT NULL,
			definition_version INTEGER NOT NULL,
			auth_token_encrypted BLOB
		)`,
		`CREATE TABLE atombase_organizations (
			id TEXT PRIMARY KEY,
			database_id TEXT NOT NULL,
			name TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			max_members INTEGER,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at TEXT,
			updated_at TEXT
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create org membership primary schema: %v", err)
		}
	}

	tenantPath := filepath.Join(t.TempDir(), "org-tenant.db")
	tenantDB, err := sql.Open("sqlite3", tenantPath)
	if err != nil {
		t.Fatalf("open tenant db: %v", err)
	}
	t.Cleanup(func() { _ = tenantDB.Close() })
	if _, err := tenantDB.Exec(`
		CREATE TABLE atombase_membership (
			user_id TEXT PRIMARY KEY,
			role TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatalf("create tenant membership schema: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO atombase_users (id, email, created_at, updated_at) VALUES (?, ?, ?, ?), (?, ?, ?, ?)`,
		"user-owner", "owner@example.com", now, now,
		"user-admin", "admin@example.com", now, now,
	); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO atombase_definitions (id, name, definition_type, current_version) VALUES (1, 'workspace', 'organization', 1)`); err != nil {
		t.Fatalf("seed definitions: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO atombase_databases (id, definition_id, definition_version) VALUES (?, 1, 1)`, tenantPath); err != nil {
		t.Fatalf("seed databases: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO atombase_organizations (id, database_id, name, owner_id, created_at, updated_at) VALUES (?, ?, 'Acme', 'user-owner', ?, ?)`,
		"org-1", tenantPath, now, now); err != nil {
		t.Fatalf("seed organizations: %v", err)
	}
	if _, err := tenantDB.Exec(`
		INSERT INTO atombase_membership (user_id, role, status, created_at)
		VALUES
			('user-owner', 'owner', 'active', ?),
			('user-admin', 'admin', 'active', ?),
			('user-member', 'member', 'active', ?)
	`, now, now, now); err != nil {
		t.Fatalf("seed tenant membership: %v", err)
	}

	api := NewAPI(testOrganizationStore{db: db, databaseID: tenantPath})

	oldOpen := openOrganizationTenantDB
	openOrganizationTenantDB = func(databaseID, authToken string) (*sql.DB, error) {
		return sql.Open("sqlite3", databaseID)
	}
	t.Cleanup(func() {
		openOrganizationTenantDB = oldOpen
	})

	return api, tenantDB, tenantPath
}

func TestListOrganizationMembers_RequiresActiveMembership(t *testing.T) {
	api, _, _ := setupOrganizationMembershipAPI(t)

	members, err := api.listOrganizationMembers(context.Background(), &orgActor{Session: &Session{Id: "sess-1", UserID: "user-owner"}, UserID: "user-owner"}, "org-1")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(members))
	}

	_, err = api.listOrganizationMembers(context.Background(), &orgActor{Session: &Session{Id: "sess-2", UserID: "user-outsider"}, UserID: "user-outsider"}, "org-1")
	if !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected unauthorized for outsider, got %v", err)
	}
}

func TestCreateOrganizationMember_OnlyOwnerCanAssignOwnerRole(t *testing.T) {
	api, tenantDB, _ := setupOrganizationMembershipAPI(t)

	if _, err := api.createOrganizationMember(context.Background(), &orgActor{Session: &Session{Id: "sess-admin", UserID: "user-admin"}, UserID: "user-admin"}, "org-1", createOrganizationMemberRequest{
		UserID: "user-new-owner",
		Role:   "owner",
	}); !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected admin owner assignment to fail, got %v", err)
	}

	member, err := api.createOrganizationMember(context.Background(), &orgActor{Session: &Session{Id: "sess-owner", UserID: "user-owner"}, UserID: "user-owner"}, "org-1", createOrganizationMemberRequest{
		UserID: "user-new-owner",
		Role:   "owner",
	})
	if err != nil {
		t.Fatalf("owner create member: %v", err)
	}
	if member.Role != "owner" {
		t.Fatalf("expected owner role, got %s", member.Role)
	}

	var count int
	if err := tenantDB.QueryRow(`SELECT COUNT(*) FROM atombase_membership WHERE user_id = 'user-new-owner' AND role = 'owner'`).Scan(&count); err != nil {
		t.Fatalf("count created member: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected created owner row, got count=%d", count)
	}
}

func TestOrganizationMembership_PreservesLastOwner(t *testing.T) {
	api, tenantDB, _ := setupOrganizationMembershipAPI(t)

	memberRole := "member"
	if _, err := api.updateOrganizationMember(context.Background(), &orgActor{Session: &Session{Id: "sess-owner", UserID: "user-owner"}, UserID: "user-owner"}, "org-1", "user-owner", updateOrganizationMemberRequest{
		Role: &memberRole,
	}); !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected last-owner demotion to fail, got %v", err)
	}

	if err := api.deleteOrganizationMember(context.Background(), &orgActor{Session: &Session{Id: "sess-owner", UserID: "user-owner"}, UserID: "user-owner"}, "org-1", "user-owner"); !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected last-owner delete to fail, got %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tenantDB.Exec(`INSERT INTO atombase_membership (user_id, role, status, created_at) VALUES ('user-owner-2', 'owner', 'active', ?)`, now); err != nil {
		t.Fatalf("seed second owner: %v", err)
	}

	if err := api.deleteOrganizationMember(context.Background(), &orgActor{Session: &Session{Id: "sess-owner", UserID: "user-owner"}, UserID: "user-owner"}, "org-1", "user-owner-2"); err != nil {
		t.Fatalf("delete second owner: %v", err)
	}

	var remaining int
	if err := tenantDB.QueryRow(`SELECT COUNT(*) FROM atombase_membership WHERE role = 'owner' AND status = 'active'`).Scan(&remaining); err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("expected 1 remaining owner, got %d", remaining)
	}
}

func TestOrganizationMembership_HidesOrganizationExistenceFromOutsiders(t *testing.T) {
	api, _, _ := setupOrganizationMembershipAPI(t)

	outsider := &orgActor{Session: &Session{Id: "sess-outsider", UserID: "user-outsider"}, UserID: "user-outsider"}

	_, errExisting := api.listOrganizationMembers(context.Background(), outsider, "org-1")
	if !errors.Is(errExisting, tools.ErrUnauthorized) {
		t.Fatalf("expected unauthorized for existing org outsider access, got %v", errExisting)
	}

	_, errMissing := api.listOrganizationMembers(context.Background(), outsider, "missing-org")
	if !errors.Is(errMissing, tools.ErrUnauthorized) {
		t.Fatalf("expected unauthorized for missing org outsider access, got %v", errMissing)
	}
}

func TestUpdateOrganization_UsesManagementPolicy(t *testing.T) {
	api, _, _ := setupOrganizationMembershipAPI(t)

	renamed := "Acme 2"
	org, err := api.updateOrganization(context.Background(), &orgActor{Session: &Session{Id: "sess-owner", UserID: "user-owner"}, UserID: "user-owner"}, "org-1", updateOrganizationRequest{
		Name: &renamed,
	})
	if err != nil {
		t.Fatalf("owner update org: %v", err)
	}
	if org.Name != "Acme 2" {
		t.Fatalf("expected renamed org, got %q", org.Name)
	}

	_, err = api.updateOrganization(context.Background(), &orgActor{Session: &Session{Id: "sess-admin", UserID: "user-admin"}, UserID: "user-admin"}, "org-1", updateOrganizationRequest{
		Name: &renamed,
	})
	if !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected admin update org to be unauthorized, got %v", err)
	}
}

func TestTransferOrganizationOwnership_PromotesNewOwner(t *testing.T) {
	api, tenantDB, _ := setupOrganizationMembershipAPI(t)

	org, err := api.transferOrganizationOwnership(context.Background(), &orgActor{Session: &Session{Id: "sess-owner", UserID: "user-owner"}, UserID: "user-owner"}, "org-1", transferOrganizationOwnershipRequest{
		UserID: "user-member",
	})
	if err != nil {
		t.Fatalf("transfer ownership: %v", err)
	}
	if org.OwnerID != "user-member" {
		t.Fatalf("expected owner_id user-member, got %s", org.OwnerID)
	}

	var role string
	if err := tenantDB.QueryRow(`SELECT role FROM atombase_membership WHERE user_id = 'user-member'`).Scan(&role); err != nil {
		t.Fatalf("load transferred role: %v", err)
	}
	if role != "owner" {
		t.Fatalf("expected tenant owner role after transfer, got %s", role)
	}
}

func TestDeleteOrganization_UsesManagementPolicy(t *testing.T) {
	api, _, _ := setupOrganizationMembershipAPI(t)

	err := api.deleteOrganization(context.Background(), &orgActor{Session: &Session{Id: "sess-admin", UserID: "user-admin"}, UserID: "user-admin"}, "org-1")
	if !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected admin delete org to be unauthorized, got %v", err)
	}

	err = api.deleteOrganization(context.Background(), &orgActor{Session: &Session{Id: "sess-owner", UserID: "user-owner"}, UserID: "user-owner"}, "org-1")
	if err != nil {
		t.Fatalf("owner delete org: %v", err)
	}

	if _, err := api.getOrganizationRecord(context.Background(), "org-1"); !errors.Is(err, tools.ErrDatabaseNotFound) {
		t.Fatalf("expected deleted org lookup to fail, got %v", err)
	}
}
