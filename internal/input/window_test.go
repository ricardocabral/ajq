package input_test

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/input"
)

type framesFramer struct {
	frames []input.Frame
	err    error
	next   int
}

func (f *framesFramer) Next() (input.Frame, error) {
	if f.next < len(f.frames) {
		frame := f.frames[f.next]
		f.next++
		return frame, nil
	}
	if f.err != nil {
		err := f.err
		f.err = nil
		return input.Frame{}, err
	}
	return input.Frame{}, io.EOF
}

func windowFrames(t *testing.T, it *input.WindowIterator) input.Window {
	t.Helper()
	window, err := it.Next(context.Background())
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	return window
}

func TestFrameByteSpans(t *testing.T) {
	t.Run("json whitespace", func(t *testing.T) {
		f := input.NewFramer(strings.NewReader(" \n {\"a\":1}\t \n [2]  \n"), input.ModeAuto)
		first, err := f.Next()
		if err != nil {
			t.Fatal(err)
		}
		second, err := f.Next()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := []int64{first.Index, first.Offset, first.Bytes, second.Index, second.Offset, second.Bytes}, []int64{0, 0, 10, 1, 10, 7}; !equalInt64s(got, want) {
			t.Fatalf("spans = %v, want %v", got, want)
		}
		if _, err := f.Next(); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal JSON whitespace should be EOF, got %v", err)
		}
	})
	t.Run("raw LF CRLF final", func(t *testing.T) {
		f := input.NewFramer(strings.NewReader("a\r\nb\nc"), input.ModeRaw)
		for i, want := range []struct{ offset, bytes int64 }{{0, 3}, {3, 2}, {5, 1}} {
			frame, err := f.Next()
			if err != nil {
				t.Fatal(err)
			}
			if frame.Index != int64(i) || frame.Offset != want.offset || frame.Bytes != want.bytes {
				t.Fatalf("frame %d = %#v; want offset=%d bytes=%d", i, frame, want.offset, want.bytes)
			}
		}
	})
	t.Run("null", func(t *testing.T) {
		frame, err := input.NewFramer(strings.NewReader("ignored"), input.ModeNull).Next()
		if err != nil || frame.Index != 0 || frame.Offset != 0 || frame.Bytes != 0 {
			t.Fatalf("null frame = %#v, %v", frame, err)
		}
	})
}

func TestWindowIteratorGroupsCompleteFrames(t *testing.T) {
	frames := []input.Frame{{Index: 0, Bytes: 3}, {Index: 1, Bytes: 2}, {Index: 2, Bytes: 4}}
	framer := &framesFramer{frames: frames}
	it, err := input.NewWindowIterator(framer, 5)
	if err != nil {
		t.Fatal(err)
	}
	first := windowFrames(t, it)
	if got := indexes(first.Frames); !equalInt64s(got, []int64{0, 1}) || first.Bytes != 5 || framer.next != 2 {
		t.Fatalf("exact window = %#v, reads=%d", first, framer.next)
	}
	// The exact boundary must not consume frame 3 as a lookahead.
	second := windowFrames(t, it)
	if got := indexes(second.Frames); !equalInt64s(got, []int64{2}) || second.Bytes != 4 || framer.next != 3 {
		t.Fatalf("under-budget window = %#v, reads=%d", second, framer.next)
	}
	if _, err := it.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF = %v", err)
	}
}

func TestWindowIteratorOversizedAndDeferredError(t *testing.T) {
	t.Run("oversized first and middle", func(t *testing.T) {
		it, err := input.NewWindowIterator(&framesFramer{frames: []input.Frame{{Index: 0, Bytes: 8}, {Index: 1, Bytes: 2}, {Index: 2, Bytes: 7}, {Index: 3, Bytes: 1}}}, 5)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []struct {
			indexes   []int64
			oversized bool
		}{{[]int64{0}, true}, {[]int64{1}, false}, {[]int64{2}, true}, {[]int64{3}, false}} {
			window := windowFrames(t, it)
			if !equalInt64s(indexes(window.Frames), want.indexes) || window.Oversized != want.oversized {
				t.Fatalf("window = %#v; want indexes=%v oversized=%v", window, want.indexes, want.oversized)
			}
		}
	})
	t.Run("source error after completed window is deferred", func(t *testing.T) {
		boom := errors.New("boom")
		it, err := input.NewWindowIterator(&framesFramer{frames: []input.Frame{{Index: 0, Bytes: 2}}, err: boom}, 5)
		if err != nil {
			t.Fatal(err)
		}
		window := windowFrames(t, it)
		if !equalInt64s(indexes(window.Frames), []int64{0}) {
			t.Fatalf("window = %#v", window)
		}
		if _, err := it.Next(context.Background()); !errors.Is(err, boom) {
			t.Fatalf("deferred error = %v, want %v", err, boom)
		}
	})
}

func TestWindowIteratorCallbackStopsBeforeLaterFrames(t *testing.T) {
	boom := errors.New("harvest failed")
	framer := &framesFramer{frames: []input.Frame{{Index: 0, Bytes: 2}, {Index: 1, Bytes: 2}, {Index: 2, Bytes: 2}}}
	it, err := input.NewWindowIterator(framer, 16)
	if err != nil {
		t.Fatal(err)
	}
	window, err := it.NextWith(context.Background(), func(frame input.Frame) error {
		if frame.Index == 1 {
			return boom
		}
		return nil
	})
	var frameErr *input.WindowFrameError
	if !errors.As(err, &frameErr) || !errors.Is(err, boom) || frameErr.Frame.Index != 1 {
		t.Fatalf("NextWith error = %T %[1]v, want frame 2 callback error", err)
	}
	if got := indexes(window.Frames); !equalInt64s(got, []int64{0}) {
		t.Fatalf("successful prefix = %v, want [0]", got)
	}
	if framer.next != 2 {
		t.Fatalf("framer reads = %d, want no read after failing frame", framer.next)
	}
}

func TestJSONFramerReleasesOversizedDecoderBuffer(t *testing.T) {
	const oversizedBytes = 12 << 20
	reader, writer := io.Pipe()
	writeDone := make(chan error, 1)
	go func() {
		defer writer.Close()
		if _, err := io.WriteString(writer, `{"value":"`); err != nil {
			writeDone <- err
			return
		}
		chunk := strings.Repeat("x", 64<<10)
		for remaining := oversizedBytes; remaining > 0; remaining -= len(chunk) {
			part := chunk
			if remaining < len(part) {
				part = part[:remaining]
			}
			if _, err := io.WriteString(writer, part); err != nil {
				writeDone <- err
				return
			}
		}
		if _, err := io.WriteString(writer, `"}`+"\n"); err != nil {
			writeDone <- err
			return
		}
		for i := 0; i < 1024; i++ {
			if _, err := io.WriteString(writer, `{"value":"small"}`+"\n"); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- nil
	}()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	framer := input.NewFramer(reader, input.ModeAuto)
	for {
		frame, err := framer.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		frame.Value = nil
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.Alloc > before.Alloc && after.Alloc-before.Alloc > 8<<20 {
		t.Fatalf("ModeAuto framer retained %d bytes after an oversized JSON frame", after.Alloc-before.Alloc)
	}
}

func TestWindowIteratorCancellationAndRetention(t *testing.T) {
	framer := &framesFramer{frames: []input.Frame{{Index: 0, Bytes: 1}}}
	it, err := input.NewWindowIterator(framer, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := it.Next(ctx); !errors.Is(err, context.Canceled) || framer.next != 0 {
		t.Fatalf("canceled Next = %v, reads = %d", err, framer.next)
	}

	const count = 100_000
	long := &generatedWindowFramer{total: count}
	it, err = input.NewWindowIterator(long, 256)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for {
		window, err := it.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		window.Release()
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.Alloc > before.Alloc && after.Alloc-before.Alloc > 8<<20 {
		t.Fatalf("window iterator retained %d bytes", after.Alloc-before.Alloc)
	}
}

type generatedWindowFramer struct{ next, total int }

func (f *generatedWindowFramer) Next() (input.Frame, error) {
	if f.next >= f.total {
		return input.Frame{}, io.EOF
	}
	frame := input.Frame{Index: int64(f.next), Bytes: 64, Value: strings.Repeat("x", 64)}
	f.next++
	return frame, nil
}

func indexes(frames []input.Frame) []int64 {
	got := make([]int64, len(frames))
	for i := range frames {
		got[i] = frames[i].Index
	}
	return got
}

func equalInt64s(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
