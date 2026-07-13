// Package bench is the Phase 2.5 latency and throughput benchmark harness for
// ajq. It measures split-execution overhead, semantic dedup effectiveness,
// backend batching, and (in real mode) cold-start and warm-daemon latency
// against a local llama-server.
//
// The harness runs in two modes:
//
//   - Fake mode (default, CI-safe): uses backend.MockBackend so no model,
//     network, daemon, or cloud work is performed. It is fully deterministic
//     and fast enough to run under `go test -bench`.
//   - Real mode (opt-in): detects a provisioned local llama-server binary and
//     GGUF model and, when present, measures real cold/warm latency. It is
//     gated behind an explicit opt-in so `go test ./...` never spawns a daemon
//     or loads a multi-gigabyte model. See realbench.go.
//
// Datasets are generated in-memory (see GenerateArray / GenerateNDJSON) so no
// large fixture files are committed. Tiny representative fixtures live under
// testdata/bench for readability.
package bench

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ricardocabral/ajq/internal/input"
)

type jsonRecordEncoder interface {
	Encode(v any) error
}

var (
	marshalWorkloadJSON    = json.Marshal
	newWorkloadJSONEncoder = func(buf *bytes.Buffer) jsonRecordEncoder {
		return json.NewEncoder(buf)
	}
)

// Shape selects how a generated dataset is framed for the engine.
type Shape int

const (
	// ShapeArray emits a single top-level JSON array frame. All records land in
	// one three-phase window, so semantic dedup and backend batching are
	// exercised within a single resolve call. This is the shape used to tune
	// Phase 4 window sizing.
	ShapeArray Shape = iota
	// ShapeNDJSON emits one JSON object per line. Complete adjacent frames share
	// a bounded three-phase semantic window, which models streaming throughput
	// while preserving byte-budgeted memory bounds.
	ShapeNDJSON
)

// Workload is one benchmark scenario: a jq-like query plus the input it runs
// against. Input is the exact bytes fed to the engine; Mode selects framing.
type Workload struct {
	// Name is a stable identifier used in reports and benchmark sub-names.
	Name string
	// Query is the ajq/jq query to execute.
	Query string
	// Input is the raw input bytes (a JSON array, NDJSON, or raw lines).
	Input []byte
	// Mode selects how Input is framed. Defaults to input.ModeAuto.
	Mode input.Mode
	// Shape records how Input was generated, for reporting only.
	Shape Shape
	// WindowBytes is the configured byte budget for three-phase semantic windows.
	// Zero uses engine's default budget.
	WindowBytes int64
	// Distinct records how many distinct field values the generator used, so
	// reports can explain the expected post-dedup judgement count.
	Distinct int
	// Records is the number of records the workload contains.
	Records int
}

// record is a single synthetic input row. Fields are chosen to exercise the
// supported semantic operators: msg for sem_match/sem_score, company for
// sem_norm, and category for sem_classify.
type record struct {
	ID      int    `json:"id"`
	Msg     string `json:"msg"`
	Company string `json:"company"`
}

// Message vocabulary used by generated workloads. Individual constants keep the
// static data immutable-by-default while preserving the same bounded pool that
// gives semantic dedup repeated values to collapse.
const (
	msgVocabularyLen = 8

	msgUrgentProductionDown = "urgent: production is down, please respond"
	msgWeeklyNewsletter     = "weekly newsletter and product updates"
	msgInvoiceAttached      = "your invoice is attached for review"
	msgTeamMeeting          = "reminder about tomorrow's team meeting"
	msgSecurityAlert        = "critical security alert on your account"
	msgWelcomeAboard        = "thanks for signing up, welcome aboard"
	msgPaymentFailed        = "payment failed, action required immediately"
	msgOfficeSnacks         = "low priority: office snacks restocked"
)

// Company vocabulary used for sem_norm workloads. The values are constants so
// ordinary package code cannot mutate the benchmark's static facts.
const (
	companyVocabularyLen = 5

	companyAcmeCorp        = "Acme Corp"
	companyAcmeCorporation = "acme corporation"
	companyGlobex          = "Globex, Inc."
	companyInitech         = "Initech LLC"
	companyUmbrella        = "Umbrella Co"
)

func messageAt(i int) string {
	switch i % msgVocabularyLen {
	case 0:
		return msgUrgentProductionDown
	case 1:
		return msgWeeklyNewsletter
	case 2:
		return msgInvoiceAttached
	case 3:
		return msgTeamMeeting
	case 4:
		return msgSecurityAlert
	case 5:
		return msgWelcomeAboard
	case 6:
		return msgPaymentFailed
	default:
		return msgOfficeSnacks
	}
}

func companyAt(i int) string {
	switch i % companyVocabularyLen {
	case 0:
		return companyAcmeCorp
	case 1:
		return companyAcmeCorporation
	case 2:
		return companyGlobex
	case 3:
		return companyInitech
	default:
		return companyUmbrella
	}
}

// generateRecords builds n synthetic records drawing field values from bounded
// vocabularies so that distinct >= min(n, len(vocab)). The distinct count of
// msg values is returned so callers can annotate expected dedup behavior.
func generateRecords(n int) ([]record, int) {
	if n < 0 {
		n = 0
	}
	records := make([]record, n)
	distinct := map[string]struct{}{}
	for i := 0; i < n; i++ {
		msg := messageAt(i)
		company := companyAt(i)
		records[i] = record{ID: i + 1, Msg: msg, Company: company}
		distinct[msg] = struct{}{}
	}
	return records, len(distinct)
}

// GenerateArray builds a single-frame JSON array workload with n records. The
// array shape places every record in one three-phase window, so dedup and
// batching are visible in a single resolve call.
func GenerateArray(name, query string, n int) (Workload, error) {
	records, distinct := generateRecords(n)
	buf, err := marshalWorkloadJSON(records)
	if err != nil {
		return Workload{}, fmt.Errorf("bench: marshal array workload: %w", err)
	}
	return Workload{
		Name:     name,
		Query:    query,
		Input:    buf,
		Mode:     input.ModeAuto,
		Shape:    ShapeArray,
		Distinct: distinct,
		Records:  n,
	}, nil
}

// GenerateNDJSON builds an NDJSON workload with n records, one JSON object per
// line. The zero WindowBytes value selects the engine default budget.
func GenerateNDJSON(name, query string, n int) (Workload, error) {
	records, distinct := generateRecords(n)
	var buf bytes.Buffer
	enc := newWorkloadJSONEncoder(&buf)
	for i := range records {
		if err := enc.Encode(records[i]); err != nil {
			return Workload{}, fmt.Errorf("bench: encode ndjson workload: %w", err)
		}
	}
	return Workload{
		Name:     name,
		Query:    query,
		Input:    buf.Bytes(),
		Mode:     input.ModeAuto,
		Shape:    ShapeNDJSON,
		Distinct: distinct,
		Records:  n,
	}, nil
}

// Built-in query templates covering the supported semantic operators. Array
// workloads must gate value ops through the bounded contexts the three-phase
// planner supports (sort_by for sem_score, group_by for sem_norm).
const (
	// QuerySemMatch filters an array by a semantic predicate.
	QuerySemMatch = `.[] | select(sem_match(.msg; "urgent")) | .id`
	// QuerySemClassify classifies each record into one of two labels.
	QuerySemClassify = `.[] | sem_classify(.msg; "urgent"; "routine")`
	// QuerySemScore sorts an array by a semantic score (bounded value op).
	QuerySemScore = `sort_by(sem_score(.msg; "urgent"))`
	// QuerySemNorm groups an array by a normalized company name (bounded value
	// op).
	QuerySemNorm = `group_by(sem_norm(.company; "acme"))`
)

// StandardWorkloads returns the default Phase-2 fake-mode workload set. Sizes
// are kept small so the whole set runs quickly under `go test -bench`.
func StandardWorkloads(records int) ([]Workload, error) {
	if records <= 0 {
		records = 64
	}
	workloads := make([]Workload, 0, 7)
	for _, spec := range []struct {
		name  string
		query string
	}{
		{name: "sem_match/array", query: QuerySemMatch},
		{name: "sem_classify/array", query: QuerySemClassify},
		{name: "sem_score/array", query: QuerySemScore},
		{name: "sem_norm/array", query: QuerySemNorm},
	} {
		w, err := GenerateArray(spec.name, spec.query, records)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, w)
	}
	for _, budget := range []int64{256, 4096} {
		w, err := GenerateNDJSON(fmt.Sprintf("sem_match/ndjson/window-%d", budget), `select(sem_match(.msg; "urgent")) | .id`, records)
		if err != nil {
			return nil, err
		}
		w.WindowBytes = budget
		workloads = append(workloads, w)
	}
	oversized := Workload{
		Name:        "sem_match/ndjson/oversized",
		Query:       `select(sem_match(.msg; "urgent")) | .id`,
		Input:       []byte(`{"id":1,"msg":"urgent ` + string(bytes.Repeat([]byte("x"), 256)) + `"}` + "\n"),
		Mode:        input.ModeAuto,
		Shape:       ShapeNDJSON,
		WindowBytes: 64,
		Distinct:    1,
		Records:     1,
	}
	return append(workloads, oversized), nil
}
