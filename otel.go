package wpgx

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.11.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// TracerName is the name of the tracer. This will be used as an attribute
	// on each span.
	tracerName = "github.com/stumble/wpgx"

	// InstrumentationVersion is the version of the wpgx library. This will
	// be used as an attribute on each span.
	instrumentationVersion = "v0.0.1"
)

type tracer struct {
	tracer trace.Tracer
	attrs  []attribute.KeyValue
}

// NewTracer returns a new Tracer.
func newTracer() *tracer {
	return &tracer{
		tracer: otel.GetTracerProvider().Tracer(
			tracerName, trace.WithInstrumentationVersion(instrumentationVersion)),
		attrs: []attribute.KeyValue{
			semconv.DBSystemPostgreSQL,
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
			semconv.DBStatementKey.String(queryName),
			semconv.DBConnectionStringKey.String(toLabel(replicaName)),
		),
	)
}

// endSpan ends @p span, marking it failed if @p err is not nil.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
