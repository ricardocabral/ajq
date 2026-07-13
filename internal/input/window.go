package input

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrInvalidWindowBudget reports a non-positive window byte budget.
var ErrInvalidWindowBudget = errors.New("window byte budget must be positive")

// Window is a bounded collection of complete input frames. A frame larger than
// the configured budget is returned alone as an oversized window.
type Window struct {
	Frames    []Frame
	Bytes     int64
	Oversized bool
}

// Release clears references retained by Window once its frames have been
// executed. It is safe to call more than once.
func (w *Window) Release() {
	for i := range w.Frames {
		w.Frames[i].Value = nil
	}
	w.Frames = nil
	w.Bytes = 0
	w.Oversized = false
}

// WindowIterator returns complete frames grouped up to a fixed byte budget. It
// retains at most one pending frame while looking ahead. A source error found
// after a nonempty window is deferred until the next Next call, so callers can
// resolve and execute the completed window before observing the error.
type WindowIterator struct {
	framer  Framer
	budget  int64
	pending *Frame
	err     error
	done    bool
}

// NewWindowIterator constructs a complete-frame window iterator.
func NewWindowIterator(framer Framer, budget int64) (*WindowIterator, error) {
	if budget <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidWindowBudget, budget)
	}
	if framer == nil {
		return nil, fmt.Errorf("window iterator requires a framer")
	}
	return &WindowIterator{framer: framer, budget: budget}, nil
}

// WindowFrameError reports a callback failure for a frame while a window is
// being filled. Frames returned in the accompanying Window are the successful
// prefix; the failing frame is deliberately not retained.
type WindowFrameError struct {
	Frame Frame
	Err   error
}

func (e *WindowFrameError) Error() string { return e.Err.Error() }
func (e *WindowFrameError) Unwrap() error { return e.Err }

// Next returns the next populated window. It checks ctx before every frame
// read, preserving the framing cancellation boundary while filling a window.
func (it *WindowIterator) Next(ctx context.Context) (Window, error) {
	return it.next(ctx, nil)
}

// NextWith returns a window while calling consume once for each frame before it
// is retained. A consume failure returns the successful prefix and a
// WindowFrameError without reading another frame, so callers can finish that
// prefix before reporting the frame-specific failure.
func (it *WindowIterator) NextWith(ctx context.Context, consume func(Frame) error) (Window, error) {
	return it.next(ctx, consume)
}

func (it *WindowIterator) next(ctx context.Context, consume func(Frame) error) (Window, error) {
	if err := contextErr(ctx); err != nil {
		return Window{}, err
	}
	if it.err != nil {
		err := it.err
		it.err = nil
		return Window{}, err
	}
	if it.done {
		return Window{}, io.EOF
	}

	var window Window
	for {
		if err := contextErr(ctx); err != nil {
			return Window{}, err
		}
		frame, ok, err := it.nextFrame()
		if err != nil {
			if len(window.Frames) != 0 {
				it.err = err
				return window, nil
			}
			return Window{}, err
		}
		if !ok {
			if len(window.Frames) == 0 {
				return Window{}, io.EOF
			}
			return window, nil
		}

		if len(window.Frames) != 0 && frame.Bytes > it.budget-window.Bytes {
			// Avoid addition overflow: equivalent to window.Bytes+frame.Bytes > budget
			// for non-negative frame sizes.
			it.pending = &frame
			return window, nil
		}
		if consume != nil {
			if err := consume(frame); err != nil {
				return window, &WindowFrameError{Frame: frame, Err: err}
			}
		}
		window.Frames = append(window.Frames, frame)
		window.Bytes += frame.Bytes
		window.Oversized = len(window.Frames) == 1 && frame.Bytes > it.budget
		// An exact boundary is complete without consuming a lookahead.
		if window.Bytes >= it.budget {
			return window, nil
		}
	}
}

func (it *WindowIterator) nextFrame() (Frame, bool, error) {
	if it.pending != nil {
		frame := *it.pending
		it.pending.Value = nil
		it.pending = nil
		return frame, true, nil
	}
	frame, err := it.framer.Next()
	if errors.Is(err, io.EOF) {
		it.done = true
		return Frame{}, false, nil
	}
	if err != nil {
		it.done = true
		return Frame{}, false, err
	}
	if frame.Bytes < 0 {
		return Frame{}, false, fmt.Errorf("frame %d has negative byte count", frame.Index+1)
	}
	return frame, true, nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
