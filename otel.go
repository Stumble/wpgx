package wpgx

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// TracerName is the name of the tracer. This will be used as an attribute
	// on each span.
	tracerName = "github.com/stumble/wpgx"

	// InstrumentationVersion is the version of the wpgx library. This will
	// be used as an attribute on each span.
	instrumentationVersion = "v0.0.1"

	// replicaKey is the wpgx instance that ran the operation: "primary" or a
	// replica name. The semantic conventions have no attribute for it.
	replicaKey = attribute.Key("wpgx.replica")
)

type tracer struct {
	tracer trace.Tracer
	attrs  []attribute.KeyValue
}

// NewTracer returns a new Tracer.
func newTracer() *tracer {
	return &tracer{
		tracer: otel.GetTracerProvider().Tracer(
			tracerName,
			trace.WithInstrumentationVersion(instrumentationVersion),
			trace.WithSchemaURL(semconv.SchemaURL)),
		attrs: []attribute.KeyValue{
			semconv.DBSystemNamePostgreSQL,
		},
	}
}

// start starts a client span for operation @p queryName as a child of the span
// in @p ctx. It returns a nil span, and @p ctx unchanged, when that span is not
// recording: wpgx only traces operations that are part of a recorded trace.
func (t *tracer) start(ctx context.Context, queryName string, replicaName *ReplicaName) (context.Context, trace.Span) {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx, nil
	}
	return t.tracer.Start(ctx, queryName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(t.attrs...),
		trace.WithAttributes(
			semconv.DBQuerySummary(queryName),
			replicaKey.String(toLabel(replicaName)),
		),
	)
}

// endSpan ends @p span, marking it failed if @p err is not nil. A PostgreSQL
// error is identified by its SQLSTATE code, other errors by their Go type.
func endSpan(span trace.Span, err error) {
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			span.SetAttributes(
				semconv.DBResponseStatusCode(pgErr.Code),
				semconv.ErrorTypeKey.String(pgErr.Code),
			)
		} else {
			span.SetAttributes(semconv.ErrorType(err))
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
