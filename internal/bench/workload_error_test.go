package bench

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type failingWorkloadEncoder struct {
	err error
}

func (e failingWorkloadEncoder) Encode(any) error { return e.err }

func TestGenerateArrayReturnsMarshalError(t *testing.T) {
	marshalErr := errors.New("marshal failed")
	old := marshalWorkloadJSON
	marshalWorkloadJSON = func(any) ([]byte, error) {
		return nil, marshalErr
	}
	t.Cleanup(func() { marshalWorkloadJSON = old })

	_, err := GenerateArray("broken", QuerySemMatch, 1)
	if err == nil {
		t.Fatal("expected marshal error, got nil")
	}
	if !errors.Is(err, marshalErr) || !strings.Contains(err.Error(), "bench: marshal array workload") {
		t.Fatalf("GenerateArray error = %v, want wrapped marshal failure", err)
	}
}

func TestGenerateNDJSONReturnsEncodeError(t *testing.T) {
	encodeErr := errors.New("encode failed")
	old := newWorkloadJSONEncoder
	newWorkloadJSONEncoder = func(*bytes.Buffer) jsonRecordEncoder {
		return failingWorkloadEncoder{err: encodeErr}
	}
	t.Cleanup(func() { newWorkloadJSONEncoder = old })

	_, err := GenerateNDJSON("broken", QuerySemMatch, 1)
	if err == nil {
		t.Fatal("expected encode error, got nil")
	}
	if !errors.Is(err, encodeErr) || !strings.Contains(err.Error(), "bench: encode ndjson workload") {
		t.Fatalf("GenerateNDJSON error = %v, want wrapped encode failure", err)
	}
}

func TestStandardWorkloadsPropagatesGenerationError(t *testing.T) {
	marshalErr := errors.New("standard workload marshal failed")
	old := marshalWorkloadJSON
	marshalWorkloadJSON = func(any) ([]byte, error) {
		return nil, marshalErr
	}
	t.Cleanup(func() { marshalWorkloadJSON = old })

	workloads, err := StandardWorkloads(2)
	if err == nil {
		t.Fatal("expected generation error, got nil")
	}
	if workloads != nil {
		t.Fatalf("StandardWorkloads returned %d workloads on error, want nil", len(workloads))
	}
	if !errors.Is(err, marshalErr) {
		t.Fatalf("StandardWorkloads error = %v, want wrapped generation failure", err)
	}
}

func TestCheckedWorkloadGenerationSuccess(t *testing.T) {
	array, err := GenerateArray("array", QuerySemMatch, 3)
	if err != nil {
		t.Fatalf("GenerateArray: %v", err)
	}
	if array.Name != "array" || array.Shape != ShapeArray || array.Records != 3 || array.Distinct != 3 {
		t.Fatalf("GenerateArray metadata = name=%q shape=%v records=%d distinct=%d", array.Name, array.Shape, array.Records, array.Distinct)
	}
	var rows []record
	if err := json.Unmarshal(array.Input, &rows); err != nil {
		t.Fatalf("GenerateArray input is not JSON array: %v", err)
	}
	if len(rows) != 3 || rows[0].ID != 1 || rows[0].Msg == "" || rows[0].Company == "" {
		t.Fatalf("GenerateArray rows = %+v, want generated records", rows)
	}

	ndjson, err := GenerateNDJSON("ndjson", QuerySemMatch, 2)
	if err != nil {
		t.Fatalf("GenerateNDJSON: %v", err)
	}
	if ndjson.Shape != ShapeNDJSON || ndjson.Records != 2 || ndjson.Distinct != 2 {
		t.Fatalf("GenerateNDJSON metadata = shape=%v records=%d distinct=%d", ndjson.Shape, ndjson.Records, ndjson.Distinct)
	}
	lines := bytes.Split(bytes.TrimSpace(ndjson.Input), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("GenerateNDJSON lines = %d, want 2", len(lines))
	}
	for i, line := range lines {
		var row record
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatalf("GenerateNDJSON line %d is not JSON: %v", i, err)
		}
		if row.ID != i+1 || row.Msg == "" || row.Company == "" {
			t.Fatalf("GenerateNDJSON row %d = %+v, want generated record", i, row)
		}
	}

	workloads, err := StandardWorkloads(2)
	if err != nil {
		t.Fatalf("StandardWorkloads: %v", err)
	}
	if len(workloads) != 7 {
		t.Fatalf("StandardWorkloads len = %d, want 7", len(workloads))
	}
}
