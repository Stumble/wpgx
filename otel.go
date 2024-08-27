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

func recordError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// TraceStart is called at the beginning of Query, QueryRow, and Exec calls.
// The returned context is used for the rest of the call and will be passed to TraceQueryEnd.
func (t *tracer) TraceStart(ctx context.Context, queryName string, repliReplicaName *ReplicaName) context.Context {
	if !trace.SpanFromContext(ctx).IsRecording() {
		return ctx
	}

	opts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(t.attrs...),
		trace.WithAttributes(semconv.DBStatementKey.String(queryName)),
		trace.WithAttributes(semconv.DBConnectionStringKey.String(toLabel(repliReplicaName))),
	}
	ctx, _ = t.tracer.Start(ctx, queryName, opts...)
	return ctx
}

// TraceQueryEnd is called at the end of Query, QueryRow, and Exec calls.
func (t *tracer) TraceEnd(ctx context.Context, errPtr *error) {
	span := trace.SpanFromContext(ctx)
	if errPtr != nil {
		recordError(span, *errPtr)
	}
	span.End()
}
