/*
Copyright 2025 The llm-d Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package telemetry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llm-d/llm-d-kv-cache/pkg/telemetry"
)

// captureLogger returns a logr.Logger that appends every emitted record's
// formatted key/values to *out, so tests can assert on what was logged.
func captureLogger(out *[]string) logr.Logger {
	return funcr.New(func(_, args string) {
		*out = append(*out, args)
	}, funcr.Options{})
}

// startSampledSpan starts a real, always-sampled span so its span context is
// valid (a no-op span would report IsValid() == false).
func startSampledSpan(t *testing.T, ctx context.Context) (context.Context, trace.Span) {
	t.Helper()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	return telemetry.Tracer().Start(ctx, "test")
}

func TestLoggerWithSpanContextInjectsFields(t *testing.T) {
	var logged []string
	ctx := log.IntoContext(context.Background(), captureLogger(&logged))

	ctx, span := startSampledSpan(t, ctx)
	defer span.End()

	sc := span.SpanContext()
	if !sc.IsValid() {
		t.Fatal("expected a valid span context for the sampled span")
	}

	ctx = telemetry.LoggerWithSpanContext(ctx, span)
	log.FromContext(ctx).Info("hello")

	if len(logged) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(logged))
	}
	rec := logged[0]
	if !strings.Contains(rec, telemetry.LogKeyTraceID) || !strings.Contains(rec, sc.TraceID().String()) {
		t.Errorf("log record missing trace_id: %q", rec)
	}
	if !strings.Contains(rec, telemetry.LogKeySpanID) || !strings.Contains(rec, sc.SpanID().String()) {
		t.Errorf("log record missing span_id: %q", rec)
	}
}

func TestLoggerWithSpanContextNoopForInvalidSpan(t *testing.T) {
	var logged []string
	ctx := log.IntoContext(context.Background(), captureLogger(&logged))

	// A span from the default no-op tracer has an invalid span context.
	otel.SetTracerProvider(noop.NewTracerProvider())
	ctx2, span := otel.Tracer("test").Start(ctx, "noop")
	defer span.End()

	if span.SpanContext().IsValid() {
		t.Skip("tracer unexpectedly produced a valid span context")
	}

	got := telemetry.LoggerWithSpanContext(ctx2, span)

	log.FromContext(got).Info("hello")
	if len(logged) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(logged))
	}
	if strings.Contains(logged[0], telemetry.LogKeyTraceID) || strings.Contains(logged[0], telemetry.LogKeySpanID) {
		t.Errorf("expected no correlation fields for invalid span, got: %q", logged[0])
	}
}
