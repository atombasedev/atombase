package main

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/atombasedev/atombase/auth"
	"github.com/atombasedev/atombase/config"
	"github.com/atombasedev/atombase/data"
	"github.com/atombasedev/atombase/platform"
	"github.com/atombasedev/atombase/primarystore"
	"github.com/atombasedev/atombase/tools"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var primarySchemaSQL string

const primaryDBPragmas = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA cache_size = -40000;
PRAGMA temp_store = MEMORY;
PRAGMA busy_timeout = 10000;
PRAGMA foreign_keys = ON;
PRAGMA journal_size_limit = 200000000;
`

func initPrimaryDB() (*sql.DB, error) {
	if err := os.MkdirAll(config.Cfg.DataDir, os.ModePerm); err != nil {
		return nil, err
	}

	conn, err := sql.Open("sqlite3", "file:"+config.Cfg.PrimaryDBPath)
	if err != nil {
		return nil, err
	}

	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	if _, err := conn.Exec(primaryDBPragmas); err != nil {
		_ = conn.Close()
		return nil, err
	}

	if _, err := conn.Exec(primarySchemaSQL); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return conn, nil
}

func logStartupInfo() {
	fmt.Println("=== Atomicbase ===")
	fmt.Printf("Port:            %s\n", config.Cfg.Port)
	fmt.Printf("Database:        %s\n", config.Cfg.PrimaryDBPath)
	fmt.Printf("Request timeout: %ds\n", config.Cfg.RequestTimeout)
	fmt.Printf("Query depth:     %d max\n", config.Cfg.MaxQueryDepth)
	fmt.Printf("Pagination:      %d default, %d max\n", config.Cfg.DefaultLimit, config.Cfg.MaxQueryLimit)

	// Security warnings
	warnings := 0
	if config.Cfg.APIKey == "" {
		fmt.Println("[WARN] No API key set - authentication disabled")
		warnings++
	} else {
		fmt.Println("[OK]   Authentication enabled")
	}

	if len(config.Cfg.CORSOrigins) == 0 {
		fmt.Println("[INFO] CORS disabled (no origins configured)")
	} else {
		fmt.Printf("[OK]   CORS origins: %v\n", config.Cfg.CORSOrigins)
	}

	if config.Cfg.ActivityLogEnabled {
		fmt.Println("[OK]   Activity logging: stdout")
	} else {
		fmt.Println("[INFO] Activity logging disabled")
	}

	if warnings > 0 {
		fmt.Printf("\n[!] %d security warning(s) - review before production\n", warnings)
	}
	fmt.Println()
}

func main() {

	logStartupInfo()

	// Initialize activity logger if enabled
	if err := tools.InitActivityLogger(); err != nil {
		log.Fatalf("Failed to initialize activity logger: %v", err)
	}

	// Initialize cache (memory for now, Redis later)
	appCache := tools.NewMemoryCache()
	tools.InitCache(appCache)

	primaryDB, err := initPrimaryDB()
	if err != nil {
		log.Fatalf("Failed to initialize primary database: %v", err)
	}

	primaryStore, err := primarystore.New(primaryDB)
	if err != nil {
		_ = primaryDB.Close()
		log.Fatalf("Failed to initialize primary store: %v", err)
	}

	dataAPI, err := data.NewAPI(primaryStore)
	if err != nil {
		_ = primaryStore.Close()
		_ = primaryDB.Close()
		log.Fatalf("Failed to initialize data API: %v", err)
	}

	platformAPI, err := platform.NewAPI(primaryStore)
	if err != nil {
		_ = primaryStore.Close()
		_ = primaryDB.Close()
		log.Fatalf("Failed to initialize platform database: %v", err)
	}

	authAPI := auth.NewAPI(primaryDB)

	app := http.NewServeMux()

	// Health check
	app.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := primaryDB.PingContext(r.Context()); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","error":"database ping failed"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	})

	// Register routes from each module
	dataAPI.RegisterRoutes(app)
	platformAPI.RegisterRoutes(app)
	authAPI.RegisterRoutes(app)

	// Apply middleware chain: panic recovery -> logging -> timeout -> cors -> auth -> handler
	handler := tools.PanicRecoveryMiddleware(
		tools.LoggingMiddleware(
			tools.TimeoutMiddleware(
				tools.CORSMiddleware(
					tools.AuthMiddleware(app)))))

	server := &http.Server{
		Addr:    config.Cfg.Port,
		Handler: handler,
	}

	// Start server in goroutine
	go func() {
		fmt.Printf("Listening on %s\n", config.Cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\nShutting down server...")

	if err := server.Shutdown(context.Background()); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	// Close cache
	appCache.Close()

	// Close database connections
	if err := primaryStore.Close(); err != nil {
		log.Printf("Error closing primary store: %v", err)
	}
	if err := primaryDB.Close(); err != nil {
		log.Printf("Error closing primary database: %v", err)
	}

	// Close activity logger
	tools.CloseActivityLogger()

	fmt.Println("Server stopped")
}
