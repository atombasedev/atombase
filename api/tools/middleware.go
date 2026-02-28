package tools

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/atombasedev/atombase/config"
)

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// generateRequestID creates a random request ID for tracing.
func generateRequestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// LoggingMiddleware logs all HTTP requests with structured JSON output.
// Logs: method, path, status, duration, client IP, and request ID.
// Also logs to activity database if activity logging is enabled.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Add request ID to response headers
		w.Header().Set("X-Request-ID", requestID)

		// Wrap response writer to capture status
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		// Process request
		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)

		// Get client IP
		clientIP := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			clientIP = strings.Split(forwarded, ",")[0]
		}
		clientIP = strings.TrimSpace(clientIP)

		// Log the request to stdout
		Logger.Info("request",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.status,
			"duration", duration,
			"client_ip", clientIP,
			"user_agent", r.UserAgent(),
		)

		// Log to activity database
		LogActivity(
			detectAPIType(r.URL.Path),
			r.Method,
			r.URL.Path,
			wrapped.status,
			duration.Milliseconds(),
			clientIP,
			r.Header.Get("Database"),
			requestID,
			"", // error field
		)
	})
}

// detectAPIType determines whether a request is for the data or platform API.
func detectAPIType(path string) string {
	if strings.HasPrefix(path, "/platform") {
		return "platform"
	}
	if strings.HasPrefix(path, "/data") {
		return "data"
	}
	return "other"
}

// CORSMiddleware handles Cross-Origin Resource Sharing.
// If ATOMICBASE_CORS_ORIGINS is not set, CORS is disabled (no cross-origin access).
// Set to "*" to allow all origins, or comma-separated list of specific origins.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origins := config.Cfg.CORSOrigins
		if len(origins) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		origin := r.Header.Get("Origin")
		allowed := false

		for _, o := range origins {
			if o == "*" || o == origin {
				allowed = true
				w.Header().Set("Access-Control-Allow-Origin", origin)
				break
			}
		}

		if !allowed && origin != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Database, DB-Token, Prefer")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// TimeoutMiddleware adds a request timeout to prevent long-running requests.
// Default timeout is 30 seconds, configurable via ATOMICBASE_REQUEST_TIMEOUT.
func TimeoutMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timeout := time.Duration(config.Cfg.RequestTimeout) * time.Second
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

// AuthRole represents the authenticated role type.
type AuthRole string

const (
	RoleAnonymous AuthRole = "anonymous"
	RoleService   AuthRole = "service"
	RoleUser      AuthRole = "user"
)

type authContextKey struct{}

// AuthContext contains authentication information set by the middleware.
type AuthContext struct {
	Role  AuthRole
	Token string // Raw token (for session validation by handlers)
}

// GetAuthContext retrieves auth context from request context.
func GetAuthContext(ctx context.Context) AuthContext {
	if auth, ok := ctx.Value(authContextKey{}).(AuthContext); ok {
		return auth
	}
	return AuthContext{Role: RoleAnonymous}
}

func respondUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{
		"code":    "UNAUTHORIZED",
		"message": msg,
	})
}

// AuthMiddleware identifies the caller and sets auth context.
// Token formats:
//   - "service.<api_key>" → RoleService (admin access)
//   - "<sessionId>.<secret>" → RoleUser (session validated by handler)
//   - No header → RoleAnonymous
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")

		// No auth header - anonymous access
		if auth == "" {
			ctx := context.WithValue(r.Context(), authContextKey{}, AuthContext{Role: RoleAnonymous})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Must be "Bearer <token>" format
		if len(auth) < 8 || !strings.EqualFold(auth[:7], "Bearer ") {
			respondUnauthorized(w, "invalid authorization format")
			return
		}

		token := auth[7:]

		// Service role: "service.<api_key>"
		if strings.HasPrefix(token, "service.") {
			apiKey := config.Cfg.APIKey
			if apiKey == "" {
				respondUnauthorized(w, "service authentication not configured")
				return
			}

			secret := strings.TrimPrefix(token, "service.")
			if subtle.ConstantTimeCompare([]byte(secret), []byte(apiKey)) != 1 {
				respondUnauthorized(w, "invalid service key")
				return
			}

			ctx := context.WithValue(r.Context(), authContextKey{}, AuthContext{Role: RoleService})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// User session token: "<sessionId>.<secret>"
		// Session validation happens in handler
		if strings.Contains(token, ".") {
			ctx := context.WithValue(r.Context(), authContextKey{}, AuthContext{
				Role:  RoleUser,
				Token: token,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Unrecognized token format
		respondUnauthorized(w, "invalid token format")
	})
}

// PanicRecoveryMiddleware recovers from panics and returns a 500 error.
// Logs the panic message and stack trace for debugging.
func PanicRecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				stack := debug.Stack()

				Logger.Error("panic recovered",
					"error", err,
					"path", r.URL.Path,
					"method", r.Method,
					"stack", string(stack),
				)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "internal server error",
				})
			}
		}()

		next.ServeHTTP(w, r)
	})
}
