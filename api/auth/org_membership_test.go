package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/definitions"
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

func (s testOrganizationStore) CreateOrganization(ctx context.Context, req CreateOrganizationParams) (*Organization, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	metadata := "{}"
	if len(req.Metadata) > 0 {
		metadata = string(req.Metadata)
	}
	tenantPath := filepath.Join(filepath.Dir(s.databaseID), req.ID+".db")
	tenantDB, err := sql.Open("sqlite3", tenantPath)
	if err != nil {
		return nil, err
	}
	defer tenantDB.Close()
	if _, err := tenantDB.Exec(`
		CREATE TABLE atombase_membership (
			user_id TEXT PRIMARY KEY,
			role TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return nil, err
	}
	if _, err := tenantDB.Exec(`
		CREATE TABLE atombase_invites (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL,
			role TEXT NOT NULL,
			invited_by TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(email)
		)
	`); err != nil {
		return nil, err
	}
	if _, err := tenantDB.Exec(`INSERT INTO atombase_membership (user_id, role, status, created_at) VALUES (?, 'owner', 'active', ?)`, req.OwnerID, now); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO atombase_databases (id, definition_id, definition_version) VALUES (?, 1, 1)`, tenantPath); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO atombase_organizations (id, database_id, name, owner_id, max_members, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, req.ID, tenantPath, req.Name, req.OwnerID, req.MaxMembers, metadata, now, now); err != nil {
		return nil, err
	}
	org := &Organization{
		ID:        req.ID,
		Name:      req.Name,
		OwnerID:   req.OwnerID,
		Metadata:  []byte(metadata),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if req.MaxMembers != nil {
		value := *req.MaxMembers
		org.MaxMembers = &value
	}
	return org, nil
}

func (s testOrganizationStore) CreateUserDatabase(ctx context.Context, req CreateUserDatabaseParams) (*UserDatabase, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	databaseID := filepath.Join(filepath.Dir(s.databaseID), req.UserID+"-user.db")
	if _, err := s.db.ExecContext(ctx, `INSERT INTO atombase_databases (id, definition_id, definition_version) VALUES (?, 1, 1)`, databaseID); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE atombase_users SET database_id = ?, updated_at = ? WHERE id = ?`, databaseID, now, req.UserID); err != nil {
		return nil, err
	}
	return &UserDatabase{
		ID:                databaseID,
		DefinitionID:      1,
		DefinitionName:    req.Definition,
		DefinitionType:    "user",
		DefinitionVersion: 1,
	}, nil
}

func (s testOrganizationStore) LookupDefinitionProvision(ctx context.Context, name string) (*DefinitionProvisionMeta, error) {
	return &DefinitionProvisionMeta{
		ID:      1,
		Name:    name,
		Type:    definitions.DefinitionTypeUser,
		Version: 1,
	}, nil
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

	origInviteCallback := config.Cfg.AuthInviteCallbackURL
	config.Cfg.AuthInviteCallbackURL = "http://localhost:3000/invite"
	t.Cleanup(func() {
		config.Cfg.AuthInviteCallbackURL = origInviteCallback
	})

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
	if _, err := tenantDB.Exec(`
		CREATE TABLE atombase_invites (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL,
			role TEXT NOT NULL,
			invited_by TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(email)
		)
	`); err != nil {
		t.Fatalf("create tenant invite schema: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO atombase_users (id, email, created_at, updated_at) VALUES (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?)`,
		"user-owner", "owner@example.com", now, now,
		"user-admin", "admin@example.com", now, now,
		"user-member", "member@example.com", now, now,
		"user-invitee", "invitee@example.com", now, now,
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

func TestGetOrganizationContext_ReturnsOrganizationMembersAndInvites(t *testing.T) {
	api, tenantDB, _ := setupOrganizationMembershipAPI(t)

	expiresAt := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if _, err := tenantDB.Exec(`
		INSERT INTO atombase_invites (id, email, role, invited_by, expires_at, created_at)
		VALUES ('invite-1', 'invitee@example.com', 'member', 'user-owner', ?, ?)
	`, expiresAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	orgCtx, err := api.getOrganizationContext(context.Background(), &orgActor{
		Session: &Session{Id: "sess-member", UserID: "user-member"},
		UserID:  "user-member",
	}, "org-1")
	if err != nil {
		t.Fatalf("get organization context: %v", err)
	}
	if orgCtx.Organization == nil || orgCtx.Organization.ID != "org-1" {
		t.Fatalf("expected organization org-1, got %#v", orgCtx.Organization)
	}
	if orgCtx.Member == nil || orgCtx.Member.UserID != "user-member" || orgCtx.Member.Role != "member" {
		t.Fatalf("expected current member role, got %#v", orgCtx.Member)
	}
	if orgCtx.Member.Email != "member@example.com" {
		t.Fatalf("expected current member email, got %#v", orgCtx.Member)
	}
	if len(orgCtx.Members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(orgCtx.Members))
	}
	if orgCtx.Members[0].Email == "" {
		t.Fatalf("expected member emails to be populated, got %#v", orgCtx.Members)
	}
	if len(orgCtx.Invites) != 1 || orgCtx.Invites[0].ID != "invite-1" {
		t.Fatalf("expected invite-1, got %#v", orgCtx.Invites)
	}
}

func TestGetOrganizationContext_ServiceCanReadWithoutMembership(t *testing.T) {
	api, tenantDB, _ := setupOrganizationMembershipAPI(t)

	expiresAt := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if _, err := tenantDB.Exec(`
		INSERT INTO atombase_invites (id, email, role, invited_by, expires_at, created_at)
		VALUES ('invite-2', 'ops@example.com', 'viewer', 'service', ?, ?)
	`, expiresAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	orgCtx, err := api.getOrganizationContext(context.Background(), &orgActor{IsService: true}, "org-1")
	if err != nil {
		t.Fatalf("service get organization context: %v", err)
	}
	if orgCtx.Member != nil {
		t.Fatalf("expected nil member for service actor, got %#v", orgCtx.Member)
	}
	if len(orgCtx.Members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(orgCtx.Members))
	}
	if len(orgCtx.Invites) != 1 || orgCtx.Invites[0].ID != "invite-2" {
		t.Fatalf("expected invite-2, got %#v", orgCtx.Invites)
	}
}

func TestGetOrganizationContext_HidesOrganizationExistenceFromOutsiders(t *testing.T) {
	api, _, _ := setupOrganizationMembershipAPI(t)

	outsider := &orgActor{
		Session: &Session{Id: "sess-outsider", UserID: "user-outsider"},
		UserID:  "user-outsider",
	}

	_, errExisting := api.getOrganizationContext(context.Background(), outsider, "org-1")
	if !errors.Is(errExisting, tools.ErrUnauthorized) {
		t.Fatalf("expected unauthorized for existing org outsider access, got %v", errExisting)
	}

	_, errMissing := api.getOrganizationContext(context.Background(), outsider, "missing-org")
	if !errors.Is(errMissing, tools.ErrUnauthorized) {
		t.Fatalf("expected unauthorized for missing org outsider access, got %v", errMissing)
	}
}

func TestCreateOrganization_CreatesBackedOrganizationForSessionUser(t *testing.T) {
	api, _, _ := setupOrganizationMembershipAPI(t)

	maxMembers := 25
	org, err := api.createOrganization(context.Background(), &orgActor{
		Session: &Session{Id: "sess-owner", UserID: "user-owner"},
		UserID:  "user-owner",
	}, createOrganizationRequest{
		ID:         "org-2",
		Name:       "Beta",
		Definition: "workspace",
		MaxMembers: &maxMembers,
		Metadata:   []byte(`{"tier":"pro"}`),
	})
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if org.ID != "org-2" || org.OwnerID != "user-owner" {
		t.Fatalf("unexpected organization: %#v", org)
	}

	got, err := api.getOrganization(context.Background(), &orgActor{
		Session: &Session{Id: "sess-owner", UserID: "user-owner"},
		UserID:  "user-owner",
	}, "org-2")
	if err != nil {
		t.Fatalf("get created organization: %v", err)
	}
	if got.Name != "Beta" {
		t.Fatalf("expected Beta, got %s", got.Name)
	}
}

func TestCreateOrganization_EnforcesPerUserQuotaFromConfig(t *testing.T) {
	api, _, _ := setupOrganizationMembershipAPI(t)

	previousLimit := config.Cfg.MaxOrganizationsPerUser
	config.Cfg.MaxOrganizationsPerUser = 1
	t.Cleanup(func() {
		config.Cfg.MaxOrganizationsPerUser = previousLimit
	})

	_, err := api.createOrganization(context.Background(), &orgActor{
		Session: &Session{Id: "sess-owner", UserID: "user-owner"},
		UserID:  "user-owner",
	}, createOrganizationRequest{
		ID:         "org-2",
		Name:       "Blocked",
		Definition: "workspace",
	})
	if err == nil {
		t.Fatal("expected org quota enforcement error")
	}
}

func TestListAndGetOrganizations_RespectMembershipVisibility(t *testing.T) {
	api, _, _ := setupOrganizationMembershipAPI(t)

	orgs, err := api.listOrganizations(context.Background(), &orgActor{
		Session: &Session{Id: "sess-member", UserID: "user-member"},
		UserID:  "user-member",
	})
	if err != nil {
		t.Fatalf("list organizations: %v", err)
	}
	if len(orgs) != 1 || orgs[0].ID != "org-1" {
		t.Fatalf("expected org-1 visibility for member, got %#v", orgs)
	}

	if _, err := api.getOrganization(context.Background(), &orgActor{
		Session: &Session{Id: "sess-outsider", UserID: "user-outsider"},
		UserID:  "user-outsider",
	}, "org-1"); !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected unauthorized for outsider get org, got %v", err)
	}
}

func TestOrganizationInvites_FollowManagementAndAcceptanceFlow(t *testing.T) {
	api, tenantDB, _ := setupOrganizationMembershipAPI(t)
	sent := outboundEmail{}
	oldSendEmail := sendEmailFn
	sendEmailFn = func(_ context.Context, msg outboundEmail) error {
		sent = msg
		return nil
	}
	t.Cleanup(func() { sendEmailFn = oldSendEmail })

	invite, err := api.createOrganizationInvite(context.Background(), &orgActor{
		Session: &Session{Id: "sess-admin", UserID: "user-admin"},
		UserID:  "user-admin",
	}, "org-1", createOrganizationInviteRequest{
		Email: "invitee@example.com",
		Role:  "member",
	})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if invite.Role != "member" {
		t.Fatalf("expected member invite role, got %s", invite.Role)
	}
	if sent.To != "invitee@example.com" {
		t.Fatalf("expected invite email recipient, got %#v", sent)
	}
	if sent.Subject == "" {
		t.Fatalf("expected invite email subject to be populated")
	}

	if _, err := api.createOrganizationInvite(context.Background(), &orgActor{
		Session: &Session{Id: "sess-admin", UserID: "user-admin"},
		UserID:  "user-admin",
	}, "org-1", createOrganizationInviteRequest{
		Email: "invitee@example.com",
		Role:  "owner",
	}); !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected admin owner invite to fail, got %v", err)
	}

	invites, err := api.listOrganizationInvites(context.Background(), &orgActor{
		Session: &Session{Id: "sess-member", UserID: "user-member"},
		UserID:  "user-member",
	}, "org-1")
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(invites) != 1 || invites[0].ID != invite.ID {
		t.Fatalf("expected one invite, got %#v", invites)
	}

	member, err := api.acceptOrganizationInvite(context.Background(), &orgActor{
		Session: &Session{Id: "sess-invitee", UserID: "user-invitee"},
		UserID:  "user-invitee",
	}, "org-1", invite.ID)
	if err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	if member.UserID != "user-invitee" || member.Role != "member" {
		t.Fatalf("unexpected accepted member: %#v", member)
	}

	var inviteCount int
	if err := tenantDB.QueryRow(`SELECT COUNT(*) FROM atombase_invites WHERE id = ?`, invite.ID).Scan(&inviteCount); err != nil {
		t.Fatalf("count invites: %v", err)
	}
	if inviteCount != 0 {
		t.Fatalf("expected invite to be deleted after acceptance, got %d", inviteCount)
	}
}

func TestOrganizationInvites_RollsBackWhenEmailDeliveryFails(t *testing.T) {
	api, tenantDB, _ := setupOrganizationMembershipAPI(t)
	oldSendEmail := sendEmailFn
	sendEmailFn = func(_ context.Context, msg outboundEmail) error {
		return errors.New("smtp unavailable")
	}
	t.Cleanup(func() { sendEmailFn = oldSendEmail })

	_, err := api.createOrganizationInvite(context.Background(), &orgActor{
		Session: &Session{Id: "sess-owner", UserID: "user-owner"},
		UserID:  "user-owner",
	}, "org-1", createOrganizationInviteRequest{
		Email: "rollback@example.com",
		Role:  "member",
	})
	if err == nil {
		t.Fatal("expected invite creation to fail when email delivery fails")
	}

	var count int
	if err := tenantDB.QueryRow(`SELECT COUNT(*) FROM atombase_invites WHERE email = 'rollback@example.com'`).Scan(&count); err != nil {
		t.Fatalf("count rollback invites: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected failed email delivery to remove invite row, got %d", count)
	}
}

func TestOrganizationInvites_ReinviteReplacesExistingInvite(t *testing.T) {
	api, tenantDB, _ := setupOrganizationMembershipAPI(t)
	oldSendEmail := sendEmailFn
	sendEmailFn = func(_ context.Context, msg outboundEmail) error { return nil }
	t.Cleanup(func() { sendEmailFn = oldSendEmail })

	first, err := api.createOrganizationInvite(context.Background(), &orgActor{
		Session: &Session{Id: "sess-owner", UserID: "user-owner"},
		UserID:  "user-owner",
	}, "org-1", createOrganizationInviteRequest{
		Email: "reinvite@example.com",
		Role:  "member",
	})
	if err != nil {
		t.Fatalf("first invite: %v", err)
	}

	second, err := api.createOrganizationInvite(context.Background(), &orgActor{
		Session: &Session{Id: "sess-owner", UserID: "user-owner"},
		UserID:  "user-owner",
	}, "org-1", createOrganizationInviteRequest{
		Email: "reinvite@example.com",
		Role:  "owner",
	})
	if err != nil {
		t.Fatalf("second invite: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("expected reinvite to issue a new invite id, got %q", second.ID)
	}
	if second.Role != "owner" {
		t.Fatalf("expected reinvite to replace role, got %q", second.Role)
	}

	var count int
	if err := tenantDB.QueryRow(`SELECT COUNT(*) FROM atombase_invites WHERE email = ?`, "reinvite@example.com").Scan(&count); err != nil {
		t.Fatalf("count reinvite rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one invite row after reinvite, got %d", count)
	}

	var storedID, storedRole string
	if err := tenantDB.QueryRow(`SELECT id, role FROM atombase_invites WHERE email = ?`, "reinvite@example.com").Scan(&storedID, &storedRole); err != nil {
		t.Fatalf("query reinvite row: %v", err)
	}
	if storedID != second.ID || storedRole != "owner" {
		t.Fatalf("expected replaced invite row, got id=%q role=%q", storedID, storedRole)
	}
}

func TestOrganizationInvites_RevokeRequiresInvitePermission(t *testing.T) {
	api, tenantDB, _ := setupOrganizationMembershipAPI(t)
	now := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if _, err := tenantDB.Exec(`
		INSERT INTO atombase_invites (id, email, role, invited_by, expires_at, created_at)
		VALUES ('invite-1', 'viewer@example.com', 'viewer', 'user-owner', ?, ?)
	`, now, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	if err := api.deleteOrganizationInvite(context.Background(), &orgActor{
		Session: &Session{Id: "sess-member", UserID: "user-member"},
		UserID:  "user-member",
	}, "org-1", "invite-1"); !errors.Is(err, tools.ErrUnauthorized) {
		t.Fatalf("expected member revoke to fail, got %v", err)
	}

	if err := api.deleteOrganizationInvite(context.Background(), &orgActor{
		Session: &Session{Id: "sess-admin", UserID: "user-admin"},
		UserID:  "user-admin",
	}, "org-1", "invite-1"); err != nil {
		t.Fatalf("admin revoke invite: %v", err)
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
