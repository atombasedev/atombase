// Package config provides centralized configuration for the Atomicbase API.
package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration values.
type Config struct {
	ApiURL                   string
	AppURL                   string   // Base app URL for user-facing links (optional)
	AuthMagicLinkCallbackURL string   // Required app callback URL that receives magic link tokens
	Port                     string   // HTTP server port (e.g., ":8080")
	PrimaryDBName            string   // Turso database name for external primary DB (empty = use local SQLite)
	PrimaryDBPath            string   // Path to local SQLite database file (fallback when PrimaryDBName is empty)
	DataDir                  string   // Directory for storing database files
	MaxRequestBody           int64    // Maximum request body size in bytes
	APIKey                   string   // API key for authentication (empty disables auth)
	CORSOrigins              []string // Allowed CORS origins (empty allows none, "*" allows all)
	TrustedProxyCIDRs        []string // Proxy CIDRs allowed to supply forwarded client IP headers
	RequestTimeout           int      // Request timeout in seconds (0 uses default of 30s)
	MaxQueryDepth            int      // Maximum nesting depth for queries (default 5)
	MaxQueryLimit            int      // Maximum rows per query (default 1000, 0 = unlimited)
	DefaultLimit             int      // Default limit when not specified (default 100, 0 = unlimited)
	MaxOrganizationsPerUser  int      // Maximum organizations a non-service user can own (0 = unlimited)

	// Turso configuration (for external databases)
	TursoOrganization  string // Turso organization name
	TursoAPIKey        string // Turso API key for management operations
	TursoGroup         string // Turso group name (default: "default")
	PrimaryDBToken     string // Auth token for the primary Turso database (when using external primary)
	TokenEncryptionKey string // 32-byte hex key for encrypting database tokens at rest

	// Email delivery
	SMTPHost     string // SMTP host for transactional email
	SMTPPort     int    // SMTP port
	SMTPUsername string // SMTP username
	SMTPPassword string // SMTP password
	SMTPFrom     string // From address for outgoing email

	// Activity logging
	ActivityLogEnabled   bool   // Whether activity logging is enabled
	ActivityLogPath      string // Path to activity log database
	ActivityLogRetention int    // Days to retain logs (0 = forever)

	// Cache configuration
	// Priority: Redis > SQLite > in-memory
	CacheRedisURL      string // Redis connection URL (empty = try SQLite or in-memory)
	CacheRedisPassword string // Redis auth password
	CacheSQLitePath    string // SQLite cache path for LiteFS (e.g., "/litefs/cache.db")
	CacheKeyPrefix     string // Key prefix for cache entries (e.g., "atomhost:instance:myapp:")

	// Startup behavior
	InitSchema bool // Run schema initialization on startup (default: false for fast cold starts)
}

// Cfg is the global configuration instance, loaded at startup.
var Cfg Config

func init() {
	// Load .env file before reading config (ignore error if file doesn't exist)
	godotenv.Load()
	Cfg = Load()

	// Validate required Turso configuration
	if Cfg.TursoOrganization != "" && Cfg.TokenEncryptionKey == "" {
		panic("TOKEN_ENCRYPTION_KEY is required when TURSO_ORGANIZATION is set")
	}
}

// Load reads configuration from environment variables with sensible defaults.
func Load() Config {

	requestTimeout := 30
	if val := os.Getenv("ATOMICBASE_REQUEST_TIMEOUT"); val != "" {
		if t, err := strconv.Atoi(val); err == nil && t > 0 {
			requestTimeout = t
		}
	}

	var corsOrigins []string
	if val := os.Getenv("ATOMICBASE_CORS_ORIGINS"); val != "" {
		corsOrigins = strings.Split(val, ",")
		for i := range corsOrigins {
			corsOrigins[i] = strings.TrimSpace(corsOrigins[i])
		}
	}

	var trustedProxyCIDRs []string
	if val := os.Getenv("ATOMICBASE_TRUSTED_PROXY_CIDRS"); val != "" {
		trustedProxyCIDRs = strings.Split(val, ",")
		for i := range trustedProxyCIDRs {
			trustedProxyCIDRs[i] = strings.TrimSpace(trustedProxyCIDRs[i])
		}
	}

	maxQueryDepth := 5
	if val := os.Getenv("ATOMICBASE_MAX_QUERY_DEPTH"); val != "" {
		if d, err := strconv.Atoi(val); err == nil && d > 0 {
			maxQueryDepth = d
		}
	}

	maxQueryLimit := 1000
	if val := os.Getenv("ATOMICBASE_MAX_QUERY_LIMIT"); val != "" {
		if l, err := strconv.Atoi(val); err == nil && l >= 0 {
			maxQueryLimit = l
		}
	}

	defaultLimit := 100
	if val := os.Getenv("ATOMICBASE_DEFAULT_LIMIT"); val != "" {
		if l, err := strconv.Atoi(val); err == nil && l >= 0 {
			defaultLimit = l
		}
	}

	return Config{
		ApiURL:                   getEnv("API_URL", "http://localhost:8080"),
		AppURL:                   strings.TrimSpace(os.Getenv("APP_URL")),
		AuthMagicLinkCallbackURL: strings.TrimSpace(os.Getenv("AUTH_MAGIC_LINK_CALLBACK_URL")),
		Port:                     getEnv("PORT", ":8080"),
		PrimaryDBName:            os.Getenv("PRIMARY_DB_NAME"),
		PrimaryDBPath:            getEnv("DB_PATH", "atomicdata/primary.db"),
		DataDir:                  getEnv("DATA_DIR", "atomicdata"),
		MaxRequestBody:           1 << 20, // 1MB
		APIKey:                   os.Getenv("ATOMICBASE_API_KEY"),
		CORSOrigins:              corsOrigins,
		TrustedProxyCIDRs:        trustedProxyCIDRs,
		RequestTimeout:           requestTimeout,
		MaxQueryDepth:            maxQueryDepth,
		MaxQueryLimit:            maxQueryLimit,
		DefaultLimit:             defaultLimit,
		MaxOrganizationsPerUser:  parseIntEnv("ATOMICBASE_MAX_ORGANIZATIONS_PER_USER", 3),

		// Turso configuration
		TursoOrganization:  os.Getenv("TURSO_ORGANIZATION"),
		TursoAPIKey:        os.Getenv("TURSO_API_KEY"),
		TursoGroup:         getEnv("TURSO_GROUP", "default"),
		PrimaryDBToken:     os.Getenv("PRIMARY_DB_TOKEN"),
		TokenEncryptionKey: os.Getenv("TOKEN_ENCRYPTION_KEY"),

		SMTPHost:     strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:     parseIntEnv("SMTP_PORT", 587),
		SMTPUsername: strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:     strings.TrimSpace(os.Getenv("SMTP_FROM")),

		ActivityLogEnabled:   strings.ToLower(os.Getenv("ATOMICBASE_ACTIVITY_LOG_ENABLED")) == "true",
		ActivityLogPath:      getEnv("ATOMICBASE_ACTIVITY_LOG_PATH", "atomicdata/logs.db"),
		ActivityLogRetention: parseIntEnv("ATOMICBASE_ACTIVITY_LOG_RETENTION", 30),

		// Cache configuration
		CacheRedisURL:      os.Getenv("CACHE_REDIS_URL"),
		CacheRedisPassword: os.Getenv("CACHE_REDIS_PASSWORD"),
		CacheSQLitePath:    os.Getenv("CACHE_SQLITE_PATH"),
		CacheKeyPrefix:     os.Getenv("CACHE_KEY_PREFIX"),

		// Startup behavior
		InitSchema: strings.ToLower(os.Getenv("INIT_SCHEMA")) != "false",
	}
}

// getEnv returns the environment variable value or a default if not set.
func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// parseIntEnv returns the environment variable as int or a default if not set/invalid.
func parseIntEnv(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}
