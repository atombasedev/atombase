-- Schema templates for multi-tenant database management
CREATE TABLE IF NOT EXISTS atombase_schema_templates (
    id INTEGER PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    current_version INTEGER DEFAULT 1,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Tenant database metadata
CREATE TABLE IF NOT EXISTS atombase_databases (
    id INTEGER PRIMARY KEY,
    name TEXT UNIQUE,
    template_id INTEGER REFERENCES atombase_schema_templates(id),
    template_version INTEGER DEFAULT 1,
    auth_token_encrypted BLOB,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Version history for schema templates
CREATE TABLE IF NOT EXISTS atombase_templates_history (
    id INTEGER PRIMARY KEY,
    template_id INTEGER NOT NULL REFERENCES atombase_schema_templates(id),
    version INTEGER NOT NULL,
    schema BLOB NOT NULL,
    checksum TEXT NOT NULL,
    changes TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(template_id, version)
);
CREATE INDEX IF NOT EXISTS idx_templates_history_template ON atombase_templates_history(template_id);

CREATE TABLE IF NOT EXISTS atombase_migrations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    template_id INTEGER NOT NULL REFERENCES atombase_schema_templates(id),
    from_version INTEGER NOT NULL,
    to_version INTEGER NOT NULL,
    sql TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ready',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_migrations_template ON atombase_migrations(template_id);
CREATE INDEX IF NOT EXISTS idx_migrations_versions ON atombase_migrations(template_id, from_version, to_version);

-- Migration failures for debugging lazy migrations
CREATE TABLE IF NOT EXISTS atombase_migration_failures (
    database_id INTEGER PRIMARY KEY REFERENCES atombase_databases(id),
    from_version INTEGER NOT NULL,
    to_version INTEGER NOT NULL,
    error TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_migration_failures_created_at ON atombase_migration_failures(created_at);

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

CREATE TABLE IF NOT EXISTS atombase_sessions (
    id TEXT PRIMARY KEY NOT NULL,
    secret_hash BLOB NOT NULL,
    user_id TEXT NOT NULL REFERENCES atombase_users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sessions_user ON atombase_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON atombase_sessions(expires_at);

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