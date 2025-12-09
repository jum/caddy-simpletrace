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
	// UseStackdriverFormat enables Google Cloud Logging (Stackdriver) field names
	UseStackdriverFormat bool `json:"use_stackdriver_format,omitempty"`
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

	if st.UseStackdriverFormat {
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
	} else {
		// Standard format
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
	for _, lf := range logFields {
		extra.Add(lf)
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
			case "stackdriver":
				st.UseStackdriverFormat = true
				if d.NextArg() {
					st.ProjectID = d.Val()
				}
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
	_ caddyhttp.MiddlewareHandler = (*SimpleTrace)(nil)
	_ caddyfile.Unmarshaler       = (*SimpleTrace)(nil)
)
