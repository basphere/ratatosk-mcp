// Package logging is operational logging (P1) and trace propagation phase 0
// (P2) of docs/logging-design.md. The invariant lives here as much as in the
// call sites: no request arguments in any log line or field, in any mode, at
// any level — error text is reconstructed (endpoint pattern, status class,
// error kind), never copied, because upstream URLs embed running versions.
//
// It also owns what a request carries through the process: the tool name, the
// caller's trace context and the caller class. The context keys are private;
// the accessors below are the only way in, so no other package can put a
// caller-chosen value where a log field or an upstream header reads it.
package logging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"regexp"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ctxKey int

const (
	ctxKeyTool ctxKey = iota
	ctxKeyTraceparent
	ctxKeyTraceID
	ctxKeyCallerClass
)

// W3C trace context: version(2)-trace_id(32)-parent_id(16)-flags(2), lowercase hex.
var traceparentRe = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

// ValidTraceparent says tp is a well-formed W3C traceparent; its trace id is
// then tp[3:35]. Malformed values are dropped, not forwarded.
func ValidTraceparent(tp string) bool { return traceparentRe.MatchString(tp) }

// RequestContext tags the context with the tool name and, when the caller sent
// W3C trace context in _meta (the MCP 2026-07-28 convention), the traceparent —
// the join key across the agent layer, this server's log lines, and the
// upstream /v2 call. Malformed values are dropped, not forwarded.
func RequestContext(ctx context.Context, tool string, req *mcp.CallToolRequest) context.Context {
	ctx = context.WithValue(ctx, ctxKeyTool, tool)
	if req == nil || req.Params == nil {
		return ctx
	}
	if tp, ok := req.Params.Meta["traceparent"].(string); ok && ValidTraceparent(tp) {
		ctx = context.WithValue(ctx, ctxKeyTraceparent, tp)
		ctx = context.WithValue(ctx, ctxKeyTraceID, tp[3:35])
	}
	return ctx
}

// TraceparentFrom is the caller's traceparent, when it sent a valid one.
func TraceparentFrom(ctx context.Context) string {
	tp, _ := ctx.Value(ctxKeyTraceparent).(string)
	return tp
}

// CallerClassFrom returns "external" or "self" when the HTTP layer classified
// the caller, else "". It is a single bit — never the address that produced it.
func CallerClassFrom(ctx context.Context) string {
	c, _ := ctx.Value(ctxKeyCallerClass).(string)
	return c
}

// WithCallerClass records the HTTP layer's judgement of the caller: "self" or
// "external", never the address.
func WithCallerClass(ctx context.Context, class string) context.Context {
	return context.WithValue(ctx, ctxKeyCallerClass, class)
}

// ToolFrom is the tool a request context was tagged with, "" outside a tool
// call.
func ToolFrom(ctx context.Context) string {
	t, _ := ctx.Value(ctxKeyTool).(string)
	return t
}

// ctxHandler stamps tool and trace_id from the request context onto every
// record, so correlation costs nothing at the call sites.
type ctxHandler struct{ slog.Handler }

func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if tool, ok := ctx.Value(ctxKeyTool).(string); ok {
		r.AddAttrs(slog.String("tool", tool))
	}
	if tid, ok := ctx.Value(ctxKeyTraceID).(string); ok {
		r.AddAttrs(slog.String("trace_id", tid))
	}
	return h.Handler.Handle(ctx, r)
}

func (h ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ctxHandler{h.Handler.WithAttrs(attrs)}
}

func (h ctxHandler) WithGroup(name string) slog.Handler {
	return ctxHandler{h.Handler.WithGroup(name)}
}

// newLogger builds the house-schema JSON logger (time, level, msg,
// service:"mcp", plus event fields) — the same shape the ratatosk worker
// emits, so a collector lifts level into a severity label with no config.
func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(ctxHandler{h}).With("service", "mcp")
}

// Setup makes the default logger write the house schema to w at level. The
// server passes stderr — stdout belongs to the stdio transport — and the
// level MCP_LOG selects (default info; "debug" adds per-call timing, still
// argument-free); a test passes a buffer.
func Setup(w io.Writer, level slog.Level) {
	slog.SetDefault(newLogger(w, level))
}

// ErrKind classifies a transport error without quoting it: the error string
// of a failed request embeds the full URL, and get_release URLs embed the
// running version. Coarse buckets are exactly what an operator can act on.
func ErrKind(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	return "transport"
}
