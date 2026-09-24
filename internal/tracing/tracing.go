// Package tracing is sprue's OpenTelemetry instrumentation: the per-request
// server span, the per-invocation handler spans and the instrumented HTTP
// transport every outbound call goes through. Spans go to the global tracer
// provider, so the host decides where they are exported: `sprue serve`
// installs one with Setup; tests and other hosts install their own or leave
// OpenTelemetry's no-op default.
package tracing

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/fil-forge/ucantone/execution"
	"github.com/fil-forge/ucantone/server"
	"github.com/fil-forge/ucantone/ucan"
	"github.com/labstack/echo/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Name is the instrumentation scope of sprue's own spans.
const Name = "github.com/fil-forge/sprue"

// Tracer returns sprue's tracer from the global provider.
func Tracer() trace.Tracer {
	return otel.Tracer(Name)
}

// NewHTTPClient returns an HTTP client that records each request as a client
// span and propagates the trace context to the callee.
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: Transport(http.DefaultTransport)}
}

// Transport wraps base so each request is a client span carrying the trace
// context, for clients that bring their own transport.
func Transport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base)
}

// Middleware starts the server span for each request, continuing a trace
// context the caller sent. The span is named for the method and the matched
// route ("GET /receipt/:cid"), so IDs in the path do not each get a name of
// their own, or for the commands a UCAN request carries (see SpanNamer).
// Health checks are not traced: an orchestrator polls them every few seconds.
//
// otelhttp names the span again as the request ends whenever the request has
// a route pattern, which Echo's router always sets, so the final name has to
// come from the name formatter. Middleware gives each request a name slot in
// its context for SpanNamer to fill, and the formatter reads it.
func Middleware() echo.MiddlewareFunc {
	start := echo.WrapMiddleware(otelhttp.NewMiddleware("sprue",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if name, ok := r.Context().Value(spanNameKey{}).(*string); ok && *name != "" {
				return *name
			}
			if r.Pattern != "" {
				return r.Method + " " + r.Pattern
			}
			return r.Method
		}),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/health"
		}),
	))
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		traced := start(next)
		return func(c *echo.Context) error {
			req := c.Request()
			c.SetRequest(req.WithContext(context.WithValue(req.Context(), spanNameKey{}, new(string))))
			return traced(c)
		}
	}
}

// spanNameKey is the context key of a request's span name slot.
type spanNameKey struct{}

// SpanNamer is a UCAN server event listener that names the request's server
// span for the commands it invokes ("/space/blob/add" rather than "POST /"). A
// batch of several commands is named for each distinct one, sorted, so the
// same mix of commands always gets the same name.
type SpanNamer struct{}

var _ server.EventListener = SpanNamer{}

func (SpanNamer) OnRequestDecode(ctx context.Context, ct ucan.Container) error {
	var cmds []string
	for _, inv := range ct.Invocations() {
		cmd := inv.Command().String()
		if !slices.Contains(cmds, cmd) {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	slices.Sort(cmds)
	name := strings.Join(cmds, ", ")
	if slot, ok := ctx.Value(spanNameKey{}).(*string); ok {
		*slot = name
	}
	trace.SpanFromContext(ctx).SetName(name)
	return nil
}

func (SpanNamer) OnResponseEncode(context.Context, ucan.Container) error { return nil }

// Handler wraps a UCAN handler in a span named for its command, so the
// invocations of a batch, which run concurrently, each get their own span
// under the request's. The span starts once the invocation has been
// validated, so the gap before it is validation. A failure receipt is an
// outcome the caller receives, recorded as ucan.receipt.ok=false; only an
// error returned by the handler marks the span as failed.
func Handler(cmd ucan.Command, fn execution.HandlerFunc) execution.HandlerFunc {
	tracer := Tracer()
	name := cmd.String()
	return func(req execution.Request, res execution.Response) error {
		ctx, span := tracer.Start(req.Context(), name,
			trace.WithAttributes(attribute.String("ucan.task", req.Invocation().Task().Link().String())),
		)
		defer span.End()

		err := fn(tracedRequest{Request: req, ctx: ctx}, res)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		if rcpt := res.Receipt(); rcpt != nil {
			span.SetAttributes(attribute.Bool("ucan.receipt.ok", rcpt.Out().IsOK()))
		}
		return nil
	}
}

// tracedRequest hands a handler its invocation span's context.
type tracedRequest struct {
	execution.Request
	ctx context.Context
}

func (r tracedRequest) Context() context.Context { return r.ctx }
