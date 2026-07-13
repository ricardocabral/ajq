package batchdispatch

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
)

func TestRunBoundsInflightWorkAndPreservesOrder(t *testing.T) {
	t.Parallel()
	batch := testBatch(4)
	release := make([]chan struct{}, len(batch))
	for i := range release {
		release[i] = make(chan struct{})
	}
	entered := make(chan int, len(batch))
	var mu sync.Mutex
	active, peak := 0, 0

	done := make(chan struct {
		results []backend.Result
		err     error
	}, 1)
	go func() {
		results, err := Run(context.Background(), batch, 2, func(_ context.Context, index int, _ backend.Judgement) (backend.Result, error) {
			mu.Lock()
			active++
			if active > peak {
				peak = active
			}
			mu.Unlock()
			entered <- index
			<-release[index]
			mu.Lock()
			active--
			mu.Unlock()
			return backend.Result{Value: fmt.Sprintf("result-%d", index)}, nil
		})
		done <- struct {
			results []backend.Result
			err     error
		}{results, err}
	}()

	first := []int{<-entered, <-entered}
	if !sameIndexes(first, []int{0, 1}) {
		t.Fatalf("initial callback indexes = %v, want 0 and 1", first)
	}
	close(release[1]) // Deliberately complete index 1 before index 0.
	third := <-entered
	if third != 2 {
		t.Fatalf("third callback index = %d, want 2", third)
	}
	close(release[0])
	fourth := <-entered
	if fourth != 3 {
		t.Fatalf("fourth callback index = %d, want 3", fourth)
	}
	close(release[2])
	close(release[3])

	got := <-done
	if got.err != nil {
		t.Fatalf("Run error = %v", got.err)
	}
	for i, result := range got.results {
		if want := fmt.Sprintf("result-%d", i); result.Value != want {
			t.Fatalf("result %d = %#v, want value %q", i, result, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != 2 {
		t.Fatalf("peak in-flight callbacks = %d, want exact bound 2", peak)
	}
}

func TestRunFailureCancelsAdmittedWorkAndDoesNotStartQueuedWork(t *testing.T) {
	t.Parallel()
	batch := testBatch(4)
	boom := errors.New("transport failed")
	allowFailure := make(chan struct{})
	started := make(chan int, 2)
	admittedFinished := make(chan struct{}, 1)
	var mu sync.Mutex
	called := make([]int, 0, len(batch))

	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), batch, 2, func(ctx context.Context, index int, _ backend.Judgement) (backend.Result, error) {
			mu.Lock()
			called = append(called, index)
			mu.Unlock()
			started <- index
			switch index {
			case 0:
				<-allowFailure
				return backend.Result{}, boom
			case 1:
				<-ctx.Done()
				admittedFinished <- struct{}{}
				return backend.Result{}, ctx.Err()
			default:
				t.Errorf("queued callback %d started after failure", index)
				return backend.Result{}, nil
			}
		})
		done <- err
	}()

	if got := []int{<-started, <-started}; !sameIndexes(got, []int{0, 1}) {
		t.Fatalf("initial callback indexes = %v, want 0 and 1", got)
	}
	close(allowFailure)
	err := <-done
	var failure *Failure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T %v, want *Failure", err, err)
	}
	if failure.Index != 0 || !errors.Is(err, boom) {
		t.Fatalf("failure = %#v, want index 0 wrapping %v", failure, boom)
	}
	select {
	case <-admittedFinished:
	default:
		t.Fatal("Run returned before an admitted sibling observed cancellation and finished")
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(called, []int{0, 1}) && !reflect.DeepEqual(called, []int{1, 0}) {
		t.Fatalf("callback indexes = %v, want only admitted indexes 0 and 1", called)
	}
}

func TestRunParentCancellationReturnsParentErrorWithoutLaterStarts(t *testing.T) {
	t.Parallel()
	t.Run("before dispatch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		called := 0
		results, err := Run(ctx, testBatch(2), 2, func(context.Context, int, backend.Judgement) (backend.Result, error) {
			called++
			return backend.Result{}, nil
		})
		if !errors.Is(err, context.Canceled) || results != nil {
			t.Fatalf("Run = (%v, %v), want (nil, context.Canceled)", results, err)
		}
		if called != 0 {
			t.Fatalf("callback calls = %d, want 0", called)
		}
	})
	t.Run("during dispatch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := make(chan int, 2)
		finished := make(chan struct{}, 2)
		var mu sync.Mutex
		called := 0
		done := make(chan struct {
			results []backend.Result
			err     error
		}, 1)
		go func() {
			results, err := Run(ctx, testBatch(4), 2, func(ctx context.Context, index int, _ backend.Judgement) (backend.Result, error) {
				mu.Lock()
				called++
				mu.Unlock()
				started <- index
				<-ctx.Done()
				finished <- struct{}{}
				return backend.Result{}, ctx.Err()
			})
			done <- struct {
				results []backend.Result
				err     error
			}{results, err}
		}()
		if got := []int{<-started, <-started}; !sameIndexes(got, []int{0, 1}) {
			t.Fatalf("initial callback indexes = %v, want 0 and 1", got)
		}
		cancel()
		got := <-done
		if !errors.Is(got.err, context.Canceled) || got.results != nil {
			t.Fatalf("Run = (%v, %v), want (nil, context.Canceled)", got.results, got.err)
		}
		if len(finished) != 2 {
			t.Fatalf("Run returned before all admitted callbacks finished; completed = %d", len(finished))
		}
		mu.Lock()
		defer mu.Unlock()
		if called != 2 {
			t.Fatalf("callback calls = %d, want 2", called)
		}
	})
}

func TestRunSequentialEmptyAndPerItemErrors(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) {
		called := false
		results, err := Run(context.Background(), nil, 3, func(context.Context, int, backend.Judgement) (backend.Result, error) {
			called = true
			return backend.Result{}, nil
		})
		if err != nil || results != nil {
			t.Fatalf("Run = (%v, %v), want (nil, nil)", results, err)
		}
		if called {
			t.Fatal("callback called for empty batch")
		}
	})
	t.Run("non-positive bound is sequential and preserves Result.Error", func(t *testing.T) {
		order := []int{}
		results, err := Run(context.Background(), testBatch(3), 0, func(_ context.Context, index int, _ backend.Judgement) (backend.Result, error) {
			order = append(order, index)
			if index == 1 {
				return backend.Result{Error: "schema mismatch"}, nil
			}
			return backend.Result{Value: index}, nil
		})
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if want := []int{0, 1, 2}; !reflect.DeepEqual(order, want) {
			t.Fatalf("callback order = %v, want %v", order, want)
		}
		if got := results[1].Error; got != "schema mismatch" {
			t.Fatalf("result error = %q, want preserved per-item error", got)
		}
	})
}

func testBatch(n int) []backend.Judgement {
	batch := make([]backend.Judgement, n)
	for i := range batch {
		batch[i].Op = fmt.Sprintf("op-%d", i)
	}
	return batch
}

func sameIndexes(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[int]bool, len(got))
	for _, index := range got {
		seen[index] = true
	}
	for _, index := range want {
		if !seen[index] {
			return false
		}
	}
	return true
}

// Keep a timeout around channel/barrier tests so a regression reports a test
// failure rather than leaving the package process stuck forever.
func TestRunBarrierTestsDoNotDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Run(ctx, testBatch(1), 1, func(context.Context, int, backend.Judgement) (backend.Result, error) {
		return backend.Result{}, nil
	})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
}
