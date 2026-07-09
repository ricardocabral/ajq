package input_test

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/input"
)

func collect(t *testing.T, data string, mode input.Mode) []input.Frame {
	t.Helper()
	f := input.NewFramer(strings.NewReader(data), mode)
	var frames []input.Frame
	for {
		frame, err := f.Next()
		if errors.Is(err, io.EOF) {
			return frames
		}
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
		frames = append(frames, frame)
	}
}

func TestAutoFramesSingleJSONValue(t *testing.T) {
	frames := collect(t, " {\"a\": 1, \"b\": [true]} \n", input.ModeAuto)
	if len(frames) != 1 {
		t.Fatalf("expected one frame, got %d", len(frames))
	}
	obj, ok := frames[0].Value.(map[string]any)
	if !ok {
		t.Fatalf("expected object frame, got %T", frames[0].Value)
	}
	if got := fmt.Sprint(obj["a"]); got != "1" {
		t.Fatalf("expected json number 1, got %s", got)
	}
}

func TestAutoFramesNDJSONWithoutWholeStreamCollection(t *testing.T) {
	f := input.NewFramer(strings.NewReader("{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n"), input.ModeAuto)
	for want := int64(0); want < 3; want++ {
		frame, err := f.Next()
		if err != nil {
			t.Fatalf("frame %d error: %v", want, err)
		}
		if frame.Index != want {
			t.Fatalf("expected frame index %d, got %d", want, frame.Index)
		}
	}
	if _, err := f.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after frames, got %v", err)
	}
}

func TestNullInputDoesNotConsumeReader(t *testing.T) {
	r := strings.NewReader("{\"ignored\":true}")
	f := input.NewFramer(r, input.ModeNull)
	frame, err := f.Next()
	if err != nil {
		t.Fatalf("null frame error: %v", err)
	}
	if frame.Value != nil {
		t.Fatalf("expected nil value, got %#v", frame.Value)
	}
	if _, err := f.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after null frame, got %v", err)
	}
	if r.Len() == 0 {
		t.Fatal("null-input framer consumed stdin")
	}
}

func TestRawInputFramesLinesAsStrings(t *testing.T) {
	frames := collect(t, "not json\r\n{still:not-json}\nlast", input.ModeRaw)
	want := []string{"not json", "{still:not-json}", "last"}
	if len(frames) != len(want) {
		t.Fatalf("expected %d raw frames, got %d", len(want), len(frames))
	}
	for i := range want {
		if frames[i].Value != want[i] {
			t.Fatalf("frame %d = %#v, want %#v", i, frames[i].Value, want[i])
		}
	}
}

type generatedNDJSON struct {
	next    int
	total   int
	pending []byte
}

func (r *generatedNDJSON) Read(p []byte) (int, error) {
	if len(r.pending) == 0 {
		if r.next >= r.total {
			return 0, io.EOF
		}
		r.pending = []byte(fmt.Sprintf("{\"n\":%d,\"payload\":\"abcdefghijklmnopqrstuvwxyz\"}\n", r.next))
		r.next++
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func TestMalformedTrailingGarbageReportsPosition(t *testing.T) {
	f := input.NewFramer(strings.NewReader("{\"ok\":true} trailing"), input.ModeAuto)
	if _, err := f.Next(); err != nil {
		t.Fatalf("first frame should decode: %v", err)
	}
	_, err := f.Next()
	if err == nil {
		t.Fatal("expected trailing garbage to fail")
	}
	if !strings.Contains(err.Error(), "frame 2") || !strings.Contains(err.Error(), "near byte") {
		t.Fatalf("error lacks frame/position detail: %v", err)
	}
}

func TestMalformedTruncatedJSONReportsPosition(t *testing.T) {
	f := input.NewFramer(strings.NewReader("{\"a\":"), input.ModeAuto)
	_, err := f.Next()
	if err == nil {
		t.Fatal("expected truncated JSON to fail")
	}
	if !strings.Contains(err.Error(), "frame 1") || !strings.Contains(err.Error(), "near byte") {
		t.Fatalf("error lacks frame/position detail: %v", err)
	}
}

func TestMalformedNDJSONLineReportsFrame(t *testing.T) {
	f := input.NewFramer(strings.NewReader("{\"ok\":1}\n{bad json}\n"), input.ModeAuto)
	if _, err := f.Next(); err != nil {
		t.Fatalf("first frame should decode: %v", err)
	}
	_, err := f.Next()
	if err == nil {
		t.Fatal("expected malformed second line to fail")
	}
	if !strings.Contains(err.Error(), "frame 2") || !strings.Contains(err.Error(), "near byte") {
		t.Fatalf("error lacks frame/position detail: %v", err)
	}
}

func TestLargeNDJSONUsesBoundedHeap(t *testing.T) {
	const frames = 100_000
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	framer := input.NewFramer(&generatedNDJSON{total: frames}, input.ModeAuto)
	for i := 0; i < frames; i++ {
		frame, err := framer.Next()
		if err != nil {
			t.Fatalf("frame %d error: %v", i, err)
		}
		if frame.Index != int64(i) {
			t.Fatalf("frame index = %d, want %d", frame.Index, i)
		}
	}
	if _, err := framer.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after generated stream, got %v", err)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.Alloc > before.Alloc {
		const ceiling = 8 << 20
		if delta := after.Alloc - before.Alloc; delta > ceiling {
			t.Fatalf("streaming %d NDJSON frames retained %d bytes, want <= %d", frames, delta, ceiling)
		}
	}
}
