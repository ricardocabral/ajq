// Package batchdispatch runs semantic backend judgements with a bounded number
// of in-flight callbacks while retaining their input order.
package batchdispatch

import (
	"context"
	"fmt"
	"sync"

	"github.com/ricardocabral/ajq/internal/backend"
)

// Failure identifies a whole-batch callback failure. Err is the original
// callback error; callers can use Index to add provider-specific context while
// errors.Is and errors.As continue to inspect Err through Unwrap.
type Failure struct {
	Index int
	Err   error
}

// Error returns a provider-neutral description of the failed judgement.
func (f *Failure) Error() string {
	if f == nil {
		return "<nil>"
	}
	return fmt.Sprintf("batch dispatch judgement %d: %v", f.Index, f.Err)
}

// Unwrap returns the callback error that caused the batch failure.
func (f *Failure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Err
}

// Run resolves batch with at most maxConcurrency callbacks in flight. Each
// callback receives its original batch index and a child context. Results are
// returned in input order. A callback error is a whole-batch failure: Run
// cancels queued and in-flight siblings, waits for admitted work to finish, and
// returns nil results with a Failure. A backend.Result.Error is callback data,
// not a Run failure.
//
// A bound of one or less executes synchronously in ascending index order. If
// parent context cancellation stops the batch, Run returns ctx.Err() and no
// results. An already-recorded callback failure takes precedence over a
// concurrent parent cancellation.
func Run(ctx context.Context, batch []backend.Judgement, maxConcurrency int, run func(context.Context, int, backend.Judgement) (backend.Result, error)) ([]backend.Result, error) {
	if len(batch) == 0 {
		return nil, nil
	}

	child, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]backend.Result, len(batch))
	if maxConcurrency <= 1 {
		return runSequential(ctx, child, cancel, batch, results, run)
	}
	return runParallel(ctx, child, cancel, batch, results, maxConcurrency, run)
}

func runSequential(parent, child context.Context, cancel context.CancelFunc, batch []backend.Judgement, results []backend.Result, run func(context.Context, int, backend.Judgement) (backend.Result, error)) ([]backend.Result, error) {
	for i, judgement := range batch {
		if err := parent.Err(); err != nil {
			return nil, err
		}
		if err := child.Err(); err != nil {
			return nil, parentOrChildError(parent, err)
		}
		result, err := run(child, i, judgement)
		if err != nil {
			if parentErr := parent.Err(); parentErr != nil {
				return nil, parentErr
			}
			cancel()
			return nil, &Failure{Index: i, Err: err}
		}
		if err := parent.Err(); err != nil {
			return nil, err
		}
		results[i] = result
	}
	return results, nil
}

func runParallel(parent, child context.Context, cancel context.CancelFunc, batch []backend.Judgement, results []backend.Result, maxConcurrency int, run func(context.Context, int, backend.Judgement) (backend.Result, error)) ([]backend.Result, error) {
	workers := min(maxConcurrency, len(batch))
	jobs := make(chan int)

	var wg sync.WaitGroup
	var failureMu sync.Mutex
	var failure *Failure
	recordFailure := func(index int, err error) {
		failureMu.Lock()
		defer failureMu.Unlock()
		if failure != nil || parent.Err() != nil {
			return
		}
		failure = &Failure{Index: index, Err: err}
		cancel()
	}

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-child.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					// A worker can win the jobs select concurrently with cancellation.
					// Do not admit that callback after cancellation has been observed.
					if child.Err() != nil {
						return
					}
					judgement := batch[index]
					// Keep this check immediately adjacent to callback entry so queued
					// work cannot begin after cancellation between handoff and invocation.
					if child.Err() != nil {
						return
					}
					result, err := run(child, index, judgement)
					if err != nil {
						recordFailure(index, err)
						return
					}
					if parent.Err() != nil {
						return
					}
					results[index] = result
				}
			}
		}()
	}

sendJobs:
	for index := range batch {
		if child.Err() != nil {
			break
		}
		select {
		case <-child.Done():
			break sendJobs
		case jobs <- index:
		}
	}
	close(jobs)
	wg.Wait()

	failureMu.Lock()
	defer failureMu.Unlock()
	if failure != nil {
		return nil, failure
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func parentOrChildError(parent context.Context, childErr error) error {
	if err := parent.Err(); err != nil {
		return err
	}
	return childErr
}
