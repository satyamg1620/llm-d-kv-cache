/*
Copyright 2026 The llm-d Authors.

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

package tokenization

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	types "github.com/llm-d/llm-d-kv-cache/pkg/tokenization/types"
)

func TestProcessTaskSpanParentedToRequest(t *testing.T) {
	recorder := setupTokenizationSpanRecorder(t)

	mockTokenizer := &MockTokenizer{}
	pool := &Pool{modelName: "test-model", workers: 1, tokenizer: mockTokenizer}

	expectedTokens := []uint32{1, 2, 3, 4}
	mockTokenizer.On("Render", "hello world").Return(expectedTokens, []types.Offset(nil), nil)

	// Establish a parent request span and propagate it through the task context.
	ctx, parent := otel.Tracer("test").Start(context.Background(), "request")
	task := Task{Ctx: ctx, Prompt: "hello world"}

	require.NoError(t, pool.processTask(task))
	parent.End()

	span := spanByNameTokenization(t, recorder.Ended(), "llm_d.kv_cache.tokenization")
	// The tokenization span must be a child of the request span.
	require.Equal(t, parent.SpanContext().TraceID(), span.SpanContext().TraceID())
	require.Equal(t, parent.SpanContext().SpanID(), span.Parent().SpanID())

	attrs := tokenizationSpanAttributes(span)
	require.Equal(t, "text", attrs["llm_d.kv_cache.tokenization.mode"].AsString())
	require.Equal(t, int64(len("hello world")), attrs["llm_d.kv_cache.tokenization.prompt_length"].AsInt64())
	require.Equal(t, int64(len(expectedTokens)), attrs["llm_d.kv_cache.tokenization.token_count"].AsInt64())
	require.False(t, attrs["llm_d.kv_cache.tokenization.multimodal"].AsBool())
	mockTokenizer.AssertExpectations(t)
}

func TestProcessTaskSpanChatModeAndMultimodal(t *testing.T) {
	recorder := setupTokenizationSpanRecorder(t)

	mockTokenizer := &MockTokenizer{}
	pool := &Pool{modelName: "test-model", workers: 1, tokenizer: mockTokenizer}

	renderReq := &types.RenderChatRequest{
		Conversation: []types.Conversation{
			{Role: "system", Content: types.Content{Raw: "You are helpful."}},
			{Role: "user", Content: types.Content{Raw: "Hello there!"}},
		},
	}
	features := &MultiModalFeatures{MMHashes: map[string][]string{"image": {"abc"}}}
	mockTokenizer.On("RenderChat", renderReq).Return([]uint32{1, 2}, features, nil)

	require.NoError(t, pool.processTask(Task{RenderReq: renderReq, Prompt: ""}))

	span := spanByNameTokenization(t, recorder.Ended(), "llm_d.kv_cache.tokenization")
	attrs := tokenizationSpanAttributes(span)
	require.Equal(t, "chat", attrs["llm_d.kv_cache.tokenization.mode"].AsString())
	require.True(t, attrs["llm_d.kv_cache.tokenization.multimodal"].AsBool())
	// prompt_length reflects chat content length, not the empty task.Prompt.
	require.Equal(t, int64(len("You are helpful.")+len("Hello there!")),
		attrs["llm_d.kv_cache.tokenization.prompt_length"].AsInt64())
	mockTokenizer.AssertExpectations(t)
}

func TestProcessTaskSpanRecordsError(t *testing.T) {
	recorder := setupTokenizationSpanRecorder(t)

	mockTokenizer := &MockTokenizer{}
	pool := &Pool{modelName: "test-model", workers: 1, tokenizer: mockTokenizer}

	renderErr := errors.New("render failed")
	mockTokenizer.On("Render", "boom").Return(nil, []types.Offset(nil), renderErr)

	err := pool.processTask(Task{Prompt: "boom"})
	require.ErrorIs(t, err, renderErr)

	span := spanByNameTokenization(t, recorder.Ended(), "llm_d.kv_cache.tokenization")
	require.Equal(t, codes.Error, span.Status().Code)
	require.Equal(t, renderErr.Error(), span.Status().Description)
}

func setupTokenizationSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)

	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	return recorder
}

func spanByNameTokenization(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}

	require.Failf(t, "missing span", "span %q not found", name)
	return nil
}

func tokenizationSpanAttributes(span sdktrace.ReadOnlySpan) map[string]attribute.Value {
	attrs := make(map[string]attribute.Value)
	for _, attr := range span.Attributes() {
		attrs[string(attr.Key)] = attr.Value
	}
	return attrs
}
