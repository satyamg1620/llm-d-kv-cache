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

package tokenization

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/llm-d/llm-d-kv-cache/pkg/telemetry"
	types "github.com/llm-d/llm-d-kv-cache/pkg/tokenization/types"
)

const (
	defaultWorkers = 5
)

// tokenizationResponse holds the result of a tokenization operation.
type tokenizationResponse struct {
	Tokens   []uint32
	Features *MultiModalFeatures
}

// Task represents a unit of work for tokenizing a prompt.
type Task struct {
	// Ctx carries the caller's trace context across the worker-queue boundary
	// so the tokenization span is parented to the originating request span.
	// A nil Ctx is treated as context.Background().
	Ctx       context.Context
	RenderReq *types.RenderChatRequest
	Prompt    string
	ModelName string
	ResultCh  chan<- tokenizationResponse // nil => fire-and-forget
}

// Pool encapsulates the queue and worker pool for tokenization tasks.
//
// Deprecated: tokenize externally and call kvcache.Indexer.ScoreTokens.
type Pool struct {
	modelName string // base model name for tokenization
	workers   int
	queue     workqueue.TypedRateLimitingInterface[Task]
	wg        sync.WaitGroup

	// Tokenizer is configured for the specific model this pool handles.
	// It's shared between all pool workers.
	// Since the tokenizer is immutable,
	// tokenizer functions are safe for concurrent use without locks.
	tokenizer Tokenizer
}

// EnqueueTokenization enqueues a new tokenization task.
// This method only enqueues the task and does not start processing it.
func (pool *Pool) EnqueueTokenization(ctx context.Context, prompt string) {
	task := Task{
		Ctx:    ctx,
		Prompt: prompt,
	}
	pool.queue.Add(task)
}

// Tokenize queues a task and blocks until the final result is available.
// ctx is propagated to the worker so the tokenization span links to the
// caller's request span.
func (pool *Pool) Tokenize(
	ctx context.Context, renderReq *types.RenderChatRequest, prompt string,
) ([]uint32, *MultiModalFeatures) {
	resultCh := make(chan tokenizationResponse, 1)
	pool.queue.Add(Task{
		Ctx:       ctx,
		RenderReq: renderReq,
		Prompt:    prompt,
		ResultCh:  resultCh,
	})

	res := <-resultCh
	return res.Tokens, res.Features
}

// Run launches worker goroutines that process tasks until the context is
// cancelled.
func (pool *Pool) Run(ctx context.Context) {
	for i := 0; i < pool.workers; i++ {
		pool.wg.Add(1)
		go pool.workerLoop(i)
	}

	<-ctx.Done()

	pool.queue.ShutDown()
	pool.wg.Wait()
}

// workerLoop is the main processing loop for each worker.
func (pool *Pool) workerLoop(_ int) {
	defer pool.wg.Done()
	// max number of times to retry a failed task before dropping it.
	const maxRetries = 3

	for {
		task, shutdown := pool.queue.Get()
		if shutdown {
			return
		}

		// Process the task.
		err := pool.processTask(task)
		switch {
		case err == nil:
			pool.queue.Forget(task)
		case pool.queue.NumRequeues(task) < maxRetries:
			pool.queue.AddRateLimited(task)
		default:
			// Retries exceeded. Drop the task and unblock the caller.
			log.Log.Error(err, "Dropping tokenization task after max retries", "prompt", task.Prompt,
				"retries", maxRetries)
			pool.queue.Forget(task)
			if task.ResultCh != nil {
				// Closing the channel signals failure (zero value received by caller)
				close(task.ResultCh)
			}
		}
		pool.queue.Done(task)
	}
}

// processTask tokenizes the prompt and returns the tokens via ResultCh.
// It sends exactly one response (success or error) if ResultCh is provided.
func (pool *Pool) processTask(task Task) error {
	// Resume the caller's trace context across the worker-queue boundary so the
	// tokenization span is parented to the originating request span. When tracing
	// is not configured, otel.Tracer() returns a no-op implementation.
	ctx := task.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	mode := "text"
	if task.RenderReq != nil {
		mode = "chat"
	}
	_, span := telemetry.Tracer("llm-d-kv-cache/pkg/tokenization").Start(
		ctx, "llm_d.kv_cache.tokenization",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	defer span.End()
	span.SetAttributes(
		attribute.String("llm_d.kv_cache.tokenization.mode", mode),
		attribute.Int("llm_d.kv_cache.tokenization.prompt_length", len(task.Prompt)),
	)

	var tokens []uint32
	var features *MultiModalFeatures
	var err error
	if task.RenderReq == nil {
		tokens, _, err = pool.tokenizer.Render(task.Prompt)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			log.Log.Error(err, "failed to render tokens", "prompt", task.Prompt)
			return err
		}
	} else {
		tokens, features, err = pool.tokenizer.RenderChat(task.RenderReq)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			log.Log.Error(err, "failed to render tokens", "task", task.RenderReq)
			return err
		}
	}

	span.SetAttributes(
		attribute.Int("llm_d.kv_cache.tokenization.token_count", len(tokens)),
		attribute.Bool("llm_d.kv_cache.tokenization.multimodal", features != nil),
	)

	// On success, send the response if a channel is provided and close the channel.
	if task.ResultCh != nil {
		resp := tokenizationResponse{
			Tokens:   tokens,
			Features: features,
		}
		task.ResultCh <- resp
		close(task.ResultCh)
	}

	return nil
}

func (pool *Pool) SetTokenizer(tokenizer Tokenizer, modelName string) {
	pool.tokenizer = tokenizer
	pool.modelName = modelName
}
