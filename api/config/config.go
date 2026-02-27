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
	ApiURL         string
	Port           string   // HTTP server port (e.g., ":8080")
	PrimaryDBPath  string   // Path to primary SQLite database file
	DataDir        string   // Directory for storing database files
	MaxRequestBody int64    // Maximum request body size in bytes
	APIKey         string   // API key for authentication (empty disables auth)
	CORSOrigins    []string // Allowed CORS origins (empty allows none, "*" allows all)
	RequestTimeout int      // Request timeout in seconds (0 uses default of 30s)
	MaxQueryDepth  int      // Maximum nesting depth for queries (default 5)
	MaxQueryLimit  int      // Maximum rows per query (default 1000, 0 = unlimited)
	DefaultLimit   int      // Default limit when not specified (default 100, 0 = unlimited)

	// Turso configuration (for multi-tenant external databases)
	TursoOrganization   string // Turso organization name
	TursoAPIKey         string // Turso API key for management operations
	TursoGroup          string // Turso group name (default: "default")
	TursoGroupAuthToken string // Auth token for the Turso group (required when Turso is enabled)

	// Activity logging
	ActivityLogEnabled   bool   // Whether activity logging is enabled
	ActivityLogPath      string // Path to activity log database
	ActivityLogRetention int    // Days to retain logs (0 = forever)
}

// Cfg is the global configuration instance, loaded at startup.
var Cfg Config

func init() {
	// Load .env file before reading config (ignore error if file doesn't exist)
	godotenv.Load()
	Cfg = Load()

	// Validate required Turso configuration
	if Cfg.TursoOrganization != "" && Cfg.TursoGroupAuthToken == "" {
		panic("TURSO_GROUP_AUTH_TOKEN is required when TURSO_ORGANIZATION is set")
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
		ApiURL:         getEnv("API_URL", "http://localhost:8080"),
		Port:           getEnv("PORT", ":8080"),
		PrimaryDBPath:  getEnv("DB_PATH", "atomicdata/primary.db"),
		DataDir:        getEnv("DATA_DIR", "atomicdata"),
		MaxRequestBody: 1 << 20, // 1MB
		APIKey:         os.Getenv("ATOMICBASE_API_KEY"),
		CORSOrigins:    corsOrigins,
		RequestTimeout: requestTimeout,
		MaxQueryDepth:  maxQueryDepth,
		MaxQueryLimit:  maxQueryLimit,
		DefaultLimit:   defaultLimit,

		// Turso configuration
		TursoOrganization:   os.Getenv("TURSO_ORGANIZATION"),
		TursoAPIKey:         os.Getenv("TURSO_API_KEY"),
		TursoGroup:          getEnv("TURSO_GROUP", "default"),
		TursoGroupAuthToken: os.Getenv("TURSO_GROUP_AUTH_TOKEN"),

		ActivityLogEnabled:   strings.ToLower(os.Getenv("ATOMICBASE_ACTIVITY_LOG_ENABLED")) == "true",
		ActivityLogPath:      getEnv("ATOMICBASE_ACTIVITY_LOG_PATH", "atomicdata/logs.db"),
		ActivityLogRetention: parseIntEnv("ATOMICBASE_ACTIVITY_LOG_RETENTION", 30),
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
