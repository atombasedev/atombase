package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/atomicbase/atomicbase/config"
	"github.com/atomicbase/atomicbase/data"
	"github.com/atomicbase/atomicbase/platform"
	"github.com/atomicbase/atomicbase/tools"
)

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
		fmt.Printf("[OK]   Activity logging: %s (retention: %d days)\n",
			config.Cfg.ActivityLogPath, config.Cfg.ActivityLogRetention)
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

	// Initialize platform database
	if err := platform.InitDB(); err != nil {
		log.Fatalf("Failed to initialize platform database: %v", err)
	}

	app := http.NewServeMux()

	// Register routes from each module
	data.RegisterRoutes(app)
	platform.RegisterRoutes(app)

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

	// Close database connections
	if err := data.ClosePrimaryDB(); err != nil {
		log.Printf("Error closing database: %v", err)
	}
	if err := platform.CloseDB(); err != nil {
		log.Printf("Error closing platform database: %v", err)
	}

	// Close activity logger
	tools.CloseActivityLogger()

	fmt.Println("Server stopped")
}
