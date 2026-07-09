package bench_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ricardocabral/ajq/internal/bench"
)

type generatedRecord struct {
	ID      int    `json:"id"`
	Msg     string `json:"msg"`
	Company string `json:"company"`
}

var expectedMessages = []string{
	"urgent: production is down, please respond",
	"weekly newsletter and product updates",
	"your invoice is attached for review",
	"reminder about tomorrow's team meeting",
	"critical security alert on your account",
	"thanks for signing up, welcome aboard",
	"payment failed, action required immediately",
	"low priority: office snacks restocked",
}

var expectedCompanies = []string{
	"Acme Corp",
	"acme corporation",
	"Globex, Inc.",
	"Initech LLC",
	"Umbrella Co",
}

func TestGenerateArrayPreservesVocabularyCycle(t *testing.T) {
	w, err := bench.GenerateArray("array", bench.QuerySemMatch, 10)
	if err != nil {
		t.Fatalf("GenerateArray: %v", err)
	}
	if w.Name != "array" || w.Query != bench.QuerySemMatch || w.Shape != bench.ShapeArray || w.Records != 10 || w.Distinct != 8 {
		t.Fatalf("GenerateArray metadata = name=%q query=%q shape=%v records=%d distinct=%d", w.Name, w.Query, w.Shape, w.Records, w.Distinct)
	}

	var records []generatedRecord
	if err := json.Unmarshal(w.Input, &records); err != nil {
		t.Fatalf("unmarshal array input: %v", err)
	}
	assertRecordCycle(t, records)
}

func TestGenerateNDJSONPreservesVocabularyCycle(t *testing.T) {
	w, err := bench.GenerateNDJSON("ndjson", `select(sem_match(.msg; "urgent")) | .id`, 10)
	if err != nil {
		t.Fatalf("GenerateNDJSON: %v", err)
	}
	if w.Name != "ndjson" || w.Shape != bench.ShapeNDJSON || w.Records != 10 || w.Distinct != 8 {
		t.Fatalf("GenerateNDJSON metadata = name=%q shape=%v records=%d distinct=%d", w.Name, w.Shape, w.Records, w.Distinct)
	}

	var records []generatedRecord
	scanner := bufio.NewScanner(bytes.NewReader(w.Input))
	for scanner.Scan() {
		var rec generatedRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("unmarshal ndjson record: %v", err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan ndjson: %v", err)
	}
	assertRecordCycle(t, records)
}

func TestGeneratedWorkloadDistinctCounts(t *testing.T) {
	for _, tt := range []struct {
		records int
		want    int
	}{
		{records: -1, want: 0},
		{records: 0, want: 0},
		{records: 1, want: 1},
		{records: 7, want: 7},
		{records: 8, want: 8},
		{records: 12, want: 8},
	} {
		array, err := bench.GenerateArray("array", bench.QuerySemMatch, tt.records)
		if err != nil {
			t.Fatalf("GenerateArray(%d): %v", tt.records, err)
		}
		if array.Distinct != tt.want {
			t.Fatalf("GenerateArray(%d).Distinct = %d, want %d", tt.records, array.Distinct, tt.want)
		}
		ndjson, err := bench.GenerateNDJSON("ndjson", bench.QuerySemMatch, tt.records)
		if err != nil {
			t.Fatalf("GenerateNDJSON(%d): %v", tt.records, err)
		}
		if ndjson.Distinct != tt.want {
			t.Fatalf("GenerateNDJSON(%d).Distinct = %d, want %d", tt.records, ndjson.Distinct, tt.want)
		}
	}
}

func TestStandardWorkloadsPreserveStaticMetadata(t *testing.T) {
	workloads, err := bench.StandardWorkloads(0)
	if err != nil {
		t.Fatalf("StandardWorkloads: %v", err)
	}
	wantNames := []string{
		"sem_match/array",
		"sem_classify/array",
		"sem_score/array",
		"sem_norm/array",
		"sem_match/ndjson",
	}
	wantQueries := []string{
		bench.QuerySemMatch,
		bench.QuerySemClassify,
		bench.QuerySemScore,
		bench.QuerySemNorm,
		`select(sem_match(.msg; "urgent")) | .id`,
	}
	wantShapes := []bench.Shape{
		bench.ShapeArray,
		bench.ShapeArray,
		bench.ShapeArray,
		bench.ShapeArray,
		bench.ShapeNDJSON,
	}

	if len(workloads) != len(wantNames) {
		t.Fatalf("StandardWorkloads len = %d, want %d", len(workloads), len(wantNames))
	}
	for i, w := range workloads {
		if w.Name != wantNames[i] || w.Query != wantQueries[i] || w.Shape != wantShapes[i] || w.Records != 64 || w.Distinct != 8 {
			t.Fatalf("StandardWorkloads[%d] = name=%q query=%q shape=%v records=%d distinct=%d", i, w.Name, w.Query, w.Shape, w.Records, w.Distinct)
		}
	}

	custom, err := bench.StandardWorkloads(3)
	if err != nil {
		t.Fatalf("StandardWorkloads(3): %v", err)
	}
	if got := recordsAndDistinct(custom); !reflect.DeepEqual(got, [][2]int{{3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}}) {
		t.Fatalf("StandardWorkloads(3) records/distinct = %v", got)
	}
}

func assertRecordCycle(t *testing.T, records []generatedRecord) {
	t.Helper()
	if len(records) != 10 {
		t.Fatalf("records len = %d, want 10", len(records))
	}
	for i, rec := range records {
		if rec.ID != i+1 {
			t.Fatalf("records[%d].ID = %d, want %d", i, rec.ID, i+1)
		}
		if rec.Msg != expectedMessages[i%len(expectedMessages)] {
			t.Fatalf("records[%d].Msg = %q, want %q", i, rec.Msg, expectedMessages[i%len(expectedMessages)])
		}
		if rec.Company != expectedCompanies[i%len(expectedCompanies)] {
			t.Fatalf("records[%d].Company = %q, want %q", i, rec.Company, expectedCompanies[i%len(expectedCompanies)])
		}
	}
}

func recordsAndDistinct(workloads []bench.Workload) [][2]int {
	got := make([][2]int, 0, len(workloads))
	for _, w := range workloads {
		got = append(got, [2]int{w.Records, w.Distinct})
	}
	return got
}
