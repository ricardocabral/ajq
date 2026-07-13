package input

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Mode selects how stdin is converted into query input frames.
type Mode int

const (
	// ModeAuto decodes one JSON value or a stream of top-level/NDJSON JSON values.
	ModeAuto Mode = iota
	// ModeNull emits one nil frame without consuming stdin.
	ModeNull
	// ModeRaw emits each input line as a string with line terminators removed.
	ModeRaw
)

// Frame is one value supplied to the future query evaluator.
type Frame struct {
	Index  int64
	Value  any
	Offset int64
	// Bytes is the exact source span consumed for this frame. JSON frames span
	// decoder offsets (including leading/inter-value whitespace); raw frames
	// include their original line terminators; null input is zero bytes.
	Bytes int64
}

// Framer streams input frames.
type Framer interface {
	Next() (Frame, error)
}

// NewFramer constructs a streaming framer for the requested mode.
func NewFramer(r io.Reader, mode Mode) Framer {
	if r == nil {
		r = strings.NewReader("")
	}

	switch mode {
	case ModeNull:
		return &nullFramer{}
	case ModeRaw:
		return newRawFramer(r)
	default:
		return newJSONFramer(r)
	}
}

type nullFramer struct {
	done bool
}

func (f *nullFramer) Next() (Frame, error) {
	if f.done {
		return Frame{}, io.EOF
	}
	f.done = true
	return Frame{Index: 0, Value: nil, Offset: 0, Bytes: 0}, nil
}

type jsonFramer struct {
	dec   *json.Decoder
	index int64
}

func newJSONFramer(r io.Reader) *jsonFramer {
	dec := json.NewDecoder(bufio.NewReader(r))
	dec.UseNumber()
	return &jsonFramer{dec: dec}
}

func (f *jsonFramer) Next() (Frame, error) {
	var value any
	start := f.dec.InputOffset()
	if err := f.dec.Decode(&value); err != nil {
		if errors.Is(err, io.EOF) {
			return Frame{}, io.EOF
		}
		return Frame{}, parseError("json", f.index, f.dec.InputOffset(), err)
	}

	frame := Frame{Index: f.index, Value: value, Offset: start, Bytes: f.dec.InputOffset() - start}
	f.index++
	return frame, nil
}

type rawFramer struct {
	reader *bufio.Reader
	index  int64
	offset int64
}

func newRawFramer(r io.Reader) *rawFramer {
	return &rawFramer{reader: bufio.NewReader(r)}
}

func (f *rawFramer) Next() (Frame, error) {
	line, err := f.reader.ReadBytes('\n')
	if len(line) == 0 && errors.Is(err, io.EOF) {
		return Frame{}, io.EOF
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return Frame{}, fmt.Errorf("read raw input line %d at byte %d: %w", f.index+1, f.offset, err)
	}

	start := f.offset
	f.offset += int64(len(line))
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	// Bytes includes the original terminator(s), which were removed from Value.
	frame := Frame{Index: f.index, Value: string(line), Offset: start, Bytes: f.offset - start}
	f.index++
	return frame, nil
}

func parseError(kind string, index int64, offset int64, err error) error {
	return fmt.Errorf("parse %s frame %d near byte %d: %w", kind, index+1, offset, err)
}
