package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/ricardocabral/ajq/internal/jq"
)

const plannerExecutorInvariantSeed int64 = 20260705006
const plannerExecutorInvariantAcceptedCases = 1000

type plannerExecutorInvariantCase struct {
	Seed  int64  `json:"seed"`
	Index int    `json:"index"`
	Query string `json:"query"`
	Input any    `json:"input"`
}

func TestFuzzPlannerExecutorInvariant(t *testing.T) {
	for _, tc := range loadPlannerExecutorInvariantCorpus(t) {
		runPlannerExecutorInvariantCase(t, tc)
	}

	rng := rand.New(rand.NewSource(plannerExecutorInvariantSeed)) //nolint:gosec // deterministic pseudo-random corpus generation for repeatable tests.
	accepted := 0
	attempts := 0
	for accepted < plannerExecutorInvariantAcceptedCases {
		attempts++
		if attempts > plannerExecutorInvariantAcceptedCases*4 {
			t.Fatalf("accepted %d/%d generated cases after %d attempts", accepted, plannerExecutorInvariantAcceptedCases, attempts)
		}
		tc := generatePlannerExecutorInvariantCase(rng, plannerExecutorInvariantSeed, accepted)
		if _, err := compileThreePhase(tc.Query, &recordingBackend{}); err != nil {
			var planErr *PlanError
			if errors.As(err, &planErr) {
				continue
			}
			t.Fatalf("seed=%d index=%d query=%s input=%s: compile failed before acceptance: %v", tc.Seed, tc.Index, tc.Query, mustJSON(tc.Input), err)
		}
		runPlannerExecutorInvariantCase(t, tc)
		accepted++
	}
}

func TestFuzzPlannerExecutorInvariantNegativeControl(t *testing.T) {
	be := &recordingBackend{}
	query := `.items[] | select(sem_match(.msg; "keep")) | .id`
	input := representativeInvariantInput(rand.New(rand.NewSource(plannerExecutorInvariantSeed))) //nolint:gosec // deterministic pseudo-random input for repeatable negative-control test.
	program, err := compileThreePhase(query, be)
	if err != nil {
		t.Fatalf("negative-control compile failed: %v", err)
	}
	publicHarvest, err := jq.CompileWithOptions(query, program.runtime.harvestOptions()...)
	if err != nil {
		t.Fatalf("negative-control public harvest compile failed: %v", err)
	}
	program.harvestProgram = publicHarvest

	err = program.harvest(input)
	var invariantErr *SemanticInvariantError
	if !errors.As(err, &invariantErr) {
		t.Fatalf("negative control error = %T %[1]v, want SemanticInvariantError", err)
	}
	if invariantErr.Witness.Op != "sem_match" || invariantErr.Witness.Query != query || invariantErr.Witness.Source.Expression == "" {
		t.Fatalf("negative control witness = %#v, want op/query/source context", invariantErr.Witness)
	}
	if len(be.batches) != 0 {
		t.Fatalf("negative control called backend before invariant abort: %#v", be.batches)
	}
}

func runPlannerExecutorInvariantCase(t *testing.T, tc plannerExecutorInvariantCase) {
	t.Helper()
	be := &recordingBackend{}
	program, err := compileThreePhase(tc.Query, be)
	if err != nil {
		t.Fatalf("seed=%d index=%d query=%s input=%s: compileThreePhase failed: %v", tc.Seed, tc.Index, tc.Query, mustJSON(tc.Input), err)
	}
	if err := program.harvest(tc.Input); err != nil {
		t.Fatalf("seed=%d index=%d query=%s input=%s: harvest failed: %v", tc.Seed, tc.Index, tc.Query, mustJSON(tc.Input), err)
	}
	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("seed=%d index=%d query=%s input=%s: resolve failed: %v", tc.Seed, tc.Index, tc.Query, mustJSON(tc.Input), err)
	}
	if _, err := program.execute(tc.Input, nil); err != nil {
		t.Fatalf("seed=%d index=%d query=%s input=%s: execute failed: %v", tc.Seed, tc.Index, tc.Query, mustJSON(tc.Input), err)
	}
	for _, witness := range program.runtime.fired {
		if !witness.Planned {
			t.Fatalf("seed=%d index=%d query=%s input=%s: unplanned witness fired: %#v", tc.Seed, tc.Index, tc.Query, mustJSON(tc.Input), witness)
		}
	}
}

func generatePlannerExecutorInvariantCase(rng *rand.Rand, seed int64, index int) plannerExecutorInvariantCase {
	matchSpec := pick(rng, []string{"keep", "drop", "urgent", "normal", "hot", "cold", "yes", "no", "x", "y"})
	labelA := pick(rng, []string{"urgent", "ticket", "bug", "alpha"})
	labelB := pick(rng, []string{"normal", "note", "other", "beta"})
	field := pick(rng, []string{".msg", ".nested.msg", ".tags[0]", ".meta.kind"})
	templates := []string{
		fmt.Sprintf(`.items[] | select(sem_match(%s; %q)) | .id`, field, matchSpec),
		fmt.Sprintf(`.items[] | {id: .id, ok: sem_match(%s; %q)}`, field, matchSpec),
		fmt.Sprintf(`.items[] | if sem_match(.gate; %q) then sem_match(.a; %q) else sem_match(.b; %q) end`, pick(rng, []string{"yes", "no"}), pick(rng, []string{"x", "y"}), pick(rng, []string{"x", "y"})),
		fmt.Sprintf(`.items[] | {id: .id, label: sem_classify(%s; %q; %q)}`, field, labelA, labelB),
		fmt.Sprintf(`.items[] | select((.tags[0] == %q) and sem_match(.nested.msg; %q)) | {id: .id, kind: .meta.kind}`, pick(rng, []string{"hot", "cold"}), matchSpec),
		fmt.Sprintf(`.items[] | "label:\(sem_classify(%s; %q; %q))"`, field, labelA, labelB),
		fmt.Sprintf(`.items[] | select(sem_match(%s; %q)) | .nested.msg`, field, matchSpec),
		fmt.Sprintf(`.items[] | (sem_classify(.meta.kind; %q; %q) == .meta.kind)`, labelA, labelB),
		fmt.Sprintf(`.items[] | .tags[if sem_match(.gate; %q) then 0 else 1 end]`, pick(rng, []string{"yes", "no"})),
		fmt.Sprintf(`.items[] | .tags[0:(if sem_match(%s; %q) then 1 else 2 end)]`, field, matchSpec),
		fmt.Sprintf(`.items[] | .nested[if sem_match(.gate; %q) then "msg" else "alt" end]`, pick(rng, []string{"yes", "no"})),
	}
	return plannerExecutorInvariantCase{
		Seed:  seed,
		Index: index,
		Query: templates[rng.Intn(len(templates))],
		Input: representativeInvariantInput(rng),
	}
}

func representativeInvariantInput(rng *rand.Rand) any {
	msgs := []string{"keep", "drop", "urgent", "normal"}
	gates := []string{"yes", "no"}
	xy := []string{"x", "y"}
	tags := []string{"hot", "cold"}
	kinds := []string{"ticket", "note", "bug", "other", "alpha", "beta"}
	items := make([]any, 3)
	for i := range items {
		items[i] = map[string]any{
			"id":   i + 1,
			"msg":  pick(rng, msgs),
			"gate": pick(rng, gates),
			"a":    pick(rng, xy),
			"b":    pick(rng, xy),
			"nested": map[string]any{
				"msg": pick(rng, msgs),
				"alt": pick(rng, msgs),
			},
			"tags": []any{pick(rng, tags), pick(rng, tags)},
			"meta": map[string]any{
				"kind": pick(rng, kinds),
			},
		}
	}
	return map[string]any{"items": items}
}

func loadPlannerExecutorInvariantCorpus(t *testing.T) []plannerExecutorInvariantCase {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "fuzz", "planner_executor_invariant.jsonl")
	file, err := os.Open(path) //nolint:gosec // path is the fixed checked-in invariant corpus under testdata.
	if err != nil {
		t.Fatalf("open invariant corpus %s: %v", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatalf("close invariant corpus %s: %v", path, err)
		}
	}()

	var cases []plannerExecutorInvariantCase
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var tc plannerExecutorInvariantCase
		if err := json.Unmarshal(line, &tc); err != nil {
			t.Fatalf("decode invariant corpus line %d: %v", len(cases)+1, err)
		}
		cases = append(cases, tc)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan invariant corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatalf("invariant corpus %s is empty", path)
	}
	return cases
}

func pick[T any](rng *rand.Rand, values []T) T {
	return values[rng.Intn(len(values))]
}

func mustJSON(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<json error: %v>", err)
	}
	return string(b)
}
