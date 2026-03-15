-- Definitions: schema blueprints with type determining database ownership and access
CREATE TABLE IF NOT EXISTS atombase_definitions (
    id INTEGER PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    definition_type TEXT NOT NULL CHECK(definition_type IN ('global', 'organization', 'user')),
    roles_json TEXT,
    current_version INTEGER DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Databases (pure storage, linked to a definition)
CREATE TABLE IF NOT EXISTS atombase_databases (
    id TEXT PRIMARY KEY NOT NULL,
    definition_id INTEGER NOT NULL REFERENCES atombase_definitions(id),
    definition_version INTEGER DEFAULT 1,
    auth_token_encrypted BLOB,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_databases_definition ON atombase_databases(definition_id);

-- Users
CREATE TABLE IF NOT EXISTS atombase_users (
    id TEXT PRIMARY KEY NOT NULL,
    database_id TEXT UNIQUE REFERENCES atombase_databases(id),
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
ON atombase_users(phone)
WHERE phone IS NOT NULL;

-- Organizations (identity layer on top of databases)
CREATE TABLE IF NOT EXISTS atombase_organizations (
    id TEXT PRIMARY KEY NOT NULL,
    database_id TEXT NOT NULL UNIQUE REFERENCES atombase_databases(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    owner_id TEXT NOT NULL REFERENCES atombase_users(id),
    max_members INTEGER,
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_organizations_owner ON atombase_organizations(owner_id);

-- Sessions
CREATE TABLE IF NOT EXISTS atombase_sessions (
    id TEXT PRIMARY KEY NOT NULL,
    secret_hash BLOB NOT NULL,
    user_id TEXT NOT NULL REFERENCES atombase_users(id) ON DELETE CASCADE,
    mfa_verified INTEGER NOT NULL DEFAULT 0,
    last_verified_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON atombase_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON atombase_sessions(expires_at);

-- Magic links
CREATE TABLE IF NOT EXISTS email_magic_links (
    id TEXT NOT NULL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE COLLATE NOCASE,
    token_hash BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    CHECK (expires_at > created_at),
    CHECK (length(token_hash) = 32)
);
CREATE INDEX IF NOT EXISTS email_magic_links_token_hash_expires_idx ON email_magic_links(token_hash, expires_at);
CREATE INDEX IF NOT EXISTS email_magic_links_expires_at_idx ON email_magic_links(expires_at);

-- Schema snapshots per version
CREATE TABLE IF NOT EXISTS atombase_definitions_history (
    id INTEGER PRIMARY KEY,
    definition_id INTEGER NOT NULL REFERENCES atombase_definitions(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    schema_json TEXT NOT NULL,
    checksum TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(definition_id, version)
);
CREATE INDEX IF NOT EXISTS idx_definitions_history_version ON atombase_definitions_history(definition_id, version);

-- Access policies (one row per table/operation)
CREATE TABLE IF NOT EXISTS atombase_access_policies (
    definition_id INTEGER NOT NULL REFERENCES atombase_definitions(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    table_name TEXT NOT NULL,
    operation TEXT NOT NULL CHECK(operation IN ('select', 'insert', 'update', 'delete')),
    conditions_json TEXT,
    PRIMARY KEY(definition_id, version, table_name, operation)
);
CREATE INDEX IF NOT EXISTS idx_access_policies_lookup
ON atombase_access_policies(definition_id, version, operation);

-- Management policies
CREATE TABLE IF NOT EXISTS atombase_management_policies (
    definition_id INTEGER NOT NULL REFERENCES atombase_definitions(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    action TEXT NOT NULL CHECK(action IN ('invite', 'assignRole', 'removeMember', 'updateOrg', 'deleteOrg', 'transferOwnership')),
    target_roles_json TEXT,
    PRIMARY KEY(definition_id, role, action)
);

-- Provision policies
CREATE TABLE IF NOT EXISTS atombase_provision_policies (
    definition_id INTEGER NOT NULL REFERENCES atombase_definitions(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    conditions_json TEXT,
    PRIMARY KEY(definition_id, version)
);

-- Migrations between versions
CREATE TABLE IF NOT EXISTS atombase_migrations (
    id INTEGER PRIMARY KEY,
    definition_id INTEGER NOT NULL REFERENCES atombase_definitions(id) ON DELETE CASCADE,
    from_version INTEGER NOT NULL,
    to_version INTEGER NOT NULL,
    sql TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(definition_id, from_version, to_version)
);
CREATE INDEX IF NOT EXISTS idx_migrations_definition ON atombase_migrations(definition_id);

-- Failed lazy migrations
CREATE TABLE IF NOT EXISTS atombase_migration_failures (
    database_id TEXT PRIMARY KEY REFERENCES atombase_databases(id) ON DELETE CASCADE,
    from_version INTEGER NOT NULL,
    to_version INTEGER NOT NULL,
    error TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
