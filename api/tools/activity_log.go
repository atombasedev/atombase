package tools

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/atombasedev/atombase/config"
)

// ActivityLog represents a single activity log record.
type ActivityLog struct {
	Time       time.Time
	Level      slog.Level
	Message    string
	API        string
	Method     string
	Path       string
	Status     int
	DurationMs int64
	ClientIP   string
	Database   string
	RequestID  string
	Error      string
}

// ActivityHandler implements slog.Handler for activity logging.
// For now, it emits structured logs to stdout only.
type ActivityHandler struct {
	mu     sync.RWMutex
	closed bool
}

var (
	activityHandler *ActivityHandler
	activityOnce    sync.Once
)

// InitActivityLogger initializes the activity logger if enabled.
func InitActivityLogger() error {
	if !config.Cfg.ActivityLogEnabled {
		return nil
	}

	var initErr error
	activityOnce.Do(func() {
		initErr = initActivityLoggerInternal()
	})
	return initErr
}

func initActivityLoggerInternal() error {
	activityHandler = &ActivityHandler{
		closed: false,
	}

	return nil
}

// Enabled reports whether the handler handles records at the given level.
func (h *ActivityHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

// Handle processes a log record.
func (h *ActivityHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.RLock()
	if h.closed {
		h.mu.RUnlock()
		return nil
	}
	h.mu.RUnlock()

	log := &ActivityLog{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
	}

	// Extract our custom attributes
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "api":
			log.API = a.Value.String()
		case "method":
			log.Method = a.Value.String()
		case "path":
			log.Path = a.Value.String()
		case "status":
			log.Status = int(a.Value.Int64())
		case "duration_ms":
			log.DurationMs = a.Value.Int64()
		case "client_ip":
			log.ClientIP = a.Value.String()
		case "database":
			log.Database = a.Value.String()
		case "request_id":
			log.RequestID = a.Value.String()
		case "error":
			log.Error = a.Value.String()
		}
		return true
	})

	Logger.Info("activity",
		"time", log.Time.Format(time.RFC3339),
		"level", log.Level,
		"message", log.Message,
		"api", log.API,
		"method", log.Method,
		"path", log.Path,
		"status", log.Status,
		"duration_ms", log.DurationMs,
		"client_ip", log.ClientIP,
		"database", log.Database,
		"request_id", log.RequestID,
		"error", log.Error,
	)

	return nil
}

// WithAttrs returns a new handler with the given attributes.
func (h *ActivityHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h // Activity logs don't use persistent attributes
}

// WithGroup returns a new handler with the given group name.
func (h *ActivityHandler) WithGroup(name string) slog.Handler {
	return h // Activity logs don't use groups
}

// Flush is currently a no-op because activity logs are emitted immediately.
func (h *ActivityHandler) Flush() {}

// LogActivity logs a request activity entry.
func LogActivity(api, method, path string, status int, durationMs int64, clientIP, database, requestID, errMsg string) {
	if activityHandler == nil {
		return
	}

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "request", 0)
	record.AddAttrs(
		slog.String("api", api),
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("status", status),
		slog.Int64("duration_ms", durationMs),
		slog.String("client_ip", clientIP),
		slog.String("database", database),
		slog.String("request_id", requestID),
		slog.String("error", errMsg),
	)

	activityHandler.Handle(context.Background(), record)
}

// CloseActivityLogger shuts down the activity logger gracefully.
func CloseActivityLogger() {
	if activityHandler == nil {
		return
	}

	activityHandler.mu.Lock()
	activityHandler.closed = true
	activityHandler.mu.Unlock()
}
