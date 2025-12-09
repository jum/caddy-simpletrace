package simpletrace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func init() {
	caddy.RegisterModule(SimpleTrace{})
	httpcaddyfile.RegisterHandlerDirective("simpletrace", parseCaddyfile)
}

// SimpleTrace implements a lightweight trace context handler for Caddy
type SimpleTrace struct {
	// Format specifies the log field naming format
	// Options: “otel” (default), “stackdriver”, “tempo”, “ecs”
	Format string `json:"format,omitempty"`
	// ProjectID is the GCP project ID, required for Stackdriver format
	ProjectID string `json:"project_id,omitempty"`
}

// CaddyModule returns the Caddy module information
func (SimpleTrace) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.simpletrace",
		New: func() caddy.Module { return new(SimpleTrace) },
	}
}

// Provision implements caddy.Provisioner
func (st *SimpleTrace) Provision(ctx caddy.Context) error {
	// Replace environment variables and other placeholders in ProjectID
	repl := caddy.NewReplacer()
	st.ProjectID = repl.ReplaceAll(st.ProjectID, "")

	// If ProjectID is still empty and format is stackdriver/gcp,
	// default to GOOGLE_CLOUD_PROJECT environment variable
	if st.ProjectID == "" && (st.Format == "stackdriver" || st.Format == "gcp") {
		st.ProjectID = repl.ReplaceAll("{env.GOOGLE_CLOUD_PROJECT}", "")
	}

	return nil

}

// ServeHTTP implements caddyhttp.MiddlewareHandler
func (st SimpleTrace) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	traceParent := r.Header.Get("traceparent")

	var traceID, spanID, parentSpanID string
	var flags string
	var sampled bool

	if traceParent != "" {
		// Parse existing traceparent header
		// Format: version-trace_id-parent_span_id-trace_flags
		parts := strings.Split(traceParent, "-")
		if len(parts) == 4 && parts[0] == "00" {
			traceID = parts[1]
			parentSpanID = parts[2]
			flags = parts[3]
			// Parse sampled flag (least significant bit of flags byte)
			sampled = parseSampledFlag(flags)
			// Generate new span ID for this request
			spanID = generateSpanID()
		} else {
			// Invalid format, generate new trace
			traceID = generateTraceID()
			spanID = generateSpanID()
			flags = "01" // Sampled
			sampled = true
		}
	} else {
		// No traceparent header, generate new trace
		traceID = generateTraceID()
		spanID = generateSpanID()
		flags = "01" // Sampled
		sampled = true
	}

	// Create new traceparent for downstream services
	newTraceParent := fmt.Sprintf("00-%s-%s-%s", traceID, spanID, flags)

	// Build log fields based on format preference
	var logFields []zapcore.Field

	format := st.Format
	if format == "" {
		format = "otel" // Default to OpenTelemetry format
	}

	switch format {
	case "stackdriver", "gcp":
		// Google Cloud Logging format
		// See: https://cloud.google.com/logging/docs/structured-logging
		traceField := traceID
		if st.ProjectID != "" {
			traceField = fmt.Sprintf("projects/%s/traces/%s", st.ProjectID, traceID)
		}

		logFields = []zapcore.Field{
			zap.String("logging.googleapis.com/trace", traceField),
			zap.String("logging.googleapis.com/spanId", spanID),
			zap.Bool("logging.googleapis.com/trace_sampled", sampled),
		}
		if parentSpanID != "" {
			logFields = append(logFields, zap.String("parent_span_id", parentSpanID))
		}

	case "tempo":
		// Grafana Tempo format (camelCase)
		logFields = []zapcore.Field{
			zap.String("traceID", traceID),
			zap.String("spanID", spanID),
			zap.Bool("traceSampled", sampled),
		}
		if parentSpanID != "" {
			logFields = append(logFields, zap.String("parentSpanID", parentSpanID))
		}

	case "ecs":
		// Elastic Common Schema format (dot notation)
		logFields = []zapcore.Field{
			zap.String("trace.id", traceID),
			zap.String("span.id", spanID),
			zap.Bool("trace.sampled", sampled),
		}
		if parentSpanID != "" {
			logFields = append(logFields, zap.String("span.parent_id", parentSpanID))
		}

	case "datadog", "dd":
		// Datadog format
		logFields = []zapcore.Field{
			zap.String("dd.trace_id", traceID),
			zap.String("dd.span_id", spanID),
			zap.Bool("dd.sampled", sampled),
		}
		if parentSpanID != "" {
			logFields = append(logFields, zap.String("dd.parent_id", parentSpanID))
		}

	default: // "otel" or unrecognized
		// OpenTelemetry format (default, most widely supported)
		logFields = []zapcore.Field{
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
			zap.Bool("trace_sampled", sampled),
		}
		if parentSpanID != "" {
			logFields = append(logFields, zap.String("parent_span_id", parentSpanID))
		}
	}

	// Add trace fields to request context for access logging
	// Caddy will pick these up automatically when logging the request
	extra := r.Context().Value(caddyhttp.ExtraLogFieldsCtxKey).(*caddyhttp.ExtraLogFields)
	for _, field := range logFields {
		extra.Add(field)
	}

	// Set traceparent header for proxied requests
	r.Header.Set("traceparent", newTraceParent)

	return next.ServeHTTP(w, r)

}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler
func (st *SimpleTrace) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "format":
				if !d.NextArg() {
					return d.ArgErr()
				}
				st.Format = d.Val()
			case "project_id":
				if !d.NextArg() {
					return d.ArgErr()
				}
				st.ProjectID = d.Val()
			default:
				return d.Errf("unknown subdirective: %s", d.Val())
			}
		}
	}
	return nil
}

// parseCaddyfile unmarshals tokens from h into a new Middleware
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var st SimpleTrace
	err := st.UnmarshalCaddyfile(h.Dispenser)
	return st, err
}

// parseSampledFlag extracts the sampled bit from the trace flags
// The sampled flag is the least significant bit (bit 0) of the flags byte
func parseSampledFlag(flags string) bool {
	if len(flags) != 2 {
		return false
	}
	// Parse hex string to int
	var flagByte byte
	_, err := fmt.Sscanf(flags, "%02x", &flagByte)
	if err != nil {
		return false
	}
	// Check least significant bit
	return (flagByte & 0x01) == 0x01
}

// generateTraceID generates a 32-character hex trace ID (16 bytes)
func generateTraceID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to less random but functional approach
		return fmt.Sprintf("%032x", 0)
	}
	return hex.EncodeToString(b)
}

// generateSpanID generates a 16-character hex span ID (8 bytes)
func generateSpanID() string {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to less random but functional approach
		return fmt.Sprintf("%016x", 0)
	}
	return hex.EncodeToString(b)
}

// Interface guards
var (
	_ caddy.Module                = (*SimpleTrace)(nil)
	_ caddy.Provisioner           = (*SimpleTrace)(nil)
	_ caddyhttp.MiddlewareHandler = (*SimpleTrace)(nil)
	_ caddyfile.Unmarshaler       = (*SimpleTrace)(nil)
)
