-- Definitions: schema blueprints with type determining database ownership and access
CREATE TABLE IF NOT EXISTS atomicbase_definitions (
    id INTEGER PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    definition_type TEXT NOT NULL CHECK(definition_type IN ('global', 'organization', 'user')),
    roles_json TEXT,  -- NULL for global/user types
    management_json TEXT,  -- NULL for global/user types
    current_version INTEGER DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Databases (pure storage, no ownership concepts)
CREATE TABLE IF NOT EXISTS atomicbase_databases (
    id TEXT PRIMARY KEY NOT NULL,
    definition_id INTEGER NOT NULL REFERENCES atomicbase_definitions(id),
    definition_version INTEGER DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_databases_definition ON atomicbase_databases(definition_id);

-- Users (optionally own one database)
CREATE TABLE IF NOT EXISTS atomicbase_users (
    id TEXT PRIMARY KEY NOT NULL,
    database_id TEXT UNIQUE REFERENCES atomicbase_databases(id),
    email TEXT UNIQUE COLLATE NOCASE,
    email_verified_at TEXT,
    phone TEXT,
    phone_verified_at TEXT,
    password_hash TEXT,
    last_sign_in_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_phone
ON atomicbase_users(phone)
WHERE phone IS NOT NULL;

-- Organizations (identity layer on top of databases)
CREATE TABLE IF NOT EXISTS atomicbase_organizations (
    id TEXT PRIMARY KEY NOT NULL,
    database_id TEXT NOT NULL UNIQUE REFERENCES atomicbase_databases(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    owner_id TEXT NOT NULL REFERENCES atomicbase_users(id),
    max_members INTEGER,
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_organizations_owner ON atomicbase_organizations(owner_id);

-- Membership (on organizations, not databases)
CREATE TABLE IF NOT EXISTS atomicbase_membership (
    organization_id TEXT NOT NULL REFERENCES atomicbase_organizations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES atomicbase_users(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(organization_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_membership_user ON atomicbase_membership(user_id);

-- Invitations (on organizations)
CREATE TABLE IF NOT EXISTS atomicbase_invitations (
    id TEXT PRIMARY KEY NOT NULL,
    organization_id TEXT NOT NULL REFERENCES atomicbase_organizations(id) ON DELETE CASCADE,
    email TEXT NOT NULL COLLATE NOCASE,
    role TEXT NOT NULL,
    invited_by TEXT NOT NULL REFERENCES atomicbase_users(id),
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(organization_id, email)
);
CREATE INDEX IF NOT EXISTS idx_invitations_email ON atomicbase_invitations(email);

-- Sessions
CREATE TABLE IF NOT EXISTS atomicbase_sessions (
    id TEXT PRIMARY KEY NOT NULL,
    secret_hash BLOB NOT NULL,
    user_id TEXT NOT NULL REFERENCES atomicbase_users(id) ON DELETE CASCADE,
    mfa_verified INTEGER NOT NULL DEFAULT 0,
    last_verified_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON atomicbase_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON atomicbase_sessions(expires_at);

-- Schema + access snapshots per version
CREATE TABLE IF NOT EXISTS atomicbase_definitions_history (
    id INTEGER PRIMARY KEY,
    definition_id INTEGER NOT NULL REFERENCES atomicbase_definitions(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    schema_json TEXT NOT NULL,
    access_json TEXT NOT NULL,
    checksum TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(definition_id, version)
);
CREATE INDEX IF NOT EXISTS idx_definitions_history_version ON atomicbase_definitions_history(definition_id, version);

-- Migration SQL between versions
CREATE TABLE IF NOT EXISTS atomicbase_migrations (
    id INTEGER PRIMARY KEY,
    definition_id INTEGER NOT NULL REFERENCES atomicbase_definitions(id) ON DELETE CASCADE,
    from_version INTEGER NOT NULL,
    to_version INTEGER NOT NULL,
    sql TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(definition_id, from_version, to_version)
);
CREATE INDEX IF NOT EXISTS idx_migrations_definition ON atomicbase_migrations(definition_id);

-- Migration failures for debugging
CREATE TABLE IF NOT EXISTS atomicbase_migration_failures (
    database_id TEXT PRIMARY KEY REFERENCES atomicbase_databases(id) ON DELETE CASCADE,
    from_version INTEGER NOT NULL,
    to_version INTEGER NOT NULL,
    error TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
