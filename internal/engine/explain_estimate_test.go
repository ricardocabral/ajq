package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/input"
)

func TestExplainEstimatesPostDedupMatchesActualMockJudgements(t *testing.T) {
	estimate := EstimateExplain(context.Background(), `.[] | sem_match(.msg; "keep")`, strings.NewReader(`[{"msg":"keep"},{"msg":"keep"},{"msg":"drop"}]`), input.ModeAuto)
	if estimate.Status != ExplainEstimateAvailable {
		t.Fatalf("Status = %q reason=%q, want available", estimate.Status, estimate.Reason)
	}
	if estimate.StaticCallSites != 1 {
		t.Fatalf("StaticCallSites = %d, want 1", estimate.StaticCallSites)
	}
	if estimate.InputFrames != 1 {
		t.Fatalf("InputFrames = %d, want 1", estimate.InputFrames)
	}
	if estimate.HarvestedJudgements != 3 {
		t.Fatalf("HarvestedJudgements = %d, want 3", estimate.HarvestedJudgements)
	}
	if estimate.PostDedupJudgements != 2 {
		t.Fatalf("PostDedupJudgements = %d, want 2 distinct mock judgements", estimate.PostDedupJudgements)
	}
	if estimate.MockJudgeBatches != 1 {
		t.Fatalf("MockJudgeBatches = %d, want one batched Backend.Judge call", estimate.MockJudgeBatches)
	}
}

func TestExplainEstimatesPredicateChainDocumentsSafeOverHarvestBound(t *testing.T) {
	estimate := EstimateExplain(context.Background(), `.[] | select(sem_match(.gate; "yes")) | sem_match(.downstream; "needed")`, strings.NewReader(`[{"gate":"yes","downstream":"needed"},{"gate":"no","downstream":"skip"},{"gate":"yes","downstream":"needed"}]`), input.ModeAuto)
	if estimate.Status != ExplainEstimateAvailable {
		t.Fatalf("Status = %q reason=%q, want available", estimate.Status, estimate.Reason)
	}
	if estimate.StaticCallSites != 2 {
		t.Fatalf("StaticCallSites = %d, want 2", estimate.StaticCallSites)
	}
	if estimate.HarvestedJudgements != 6 {
		t.Fatalf("HarvestedJudgements = %d, want harvest superset across both semantic predicates", estimate.HarvestedJudgements)
	}
	if estimate.PostDedupJudgements != 4 {
		t.Fatalf("PostDedupJudgements = %d, want distinct gate/downstream judgements including over-harvested downstream", estimate.PostDedupJudgements)
	}
	neededExecuteDistinctJudgements := 3 // gate=yes, gate=no, downstream=needed.
	if estimate.PostDedupJudgements < neededExecuteDistinctJudgements {
		t.Fatalf("PostDedupJudgements = %d underestimates execute-needed %d", estimate.PostDedupJudgements, neededExecuteDistinctJudgements)
	}
}

func TestExplainEstimatesUnavailableKeepsStaticPlanForInvalidInput(t *testing.T) {
	estimate := EstimateExplain(context.Background(), `.[] | sem_match(.msg; "keep")`, strings.NewReader(`not json`), input.ModeAuto)
	if estimate.Status != ExplainEstimateUnavailableInvalid {
		t.Fatalf("Status = %q, want invalid input", estimate.Status)
	}
	if estimate.StaticCallSites != 1 || len(estimate.SemanticPlan.Semantic) != 1 {
		t.Fatalf("static plan missing after invalid input: %#v", estimate)
	}
	if estimate.Reason == "" {
		t.Fatal("invalid input estimate missing stable reason")
	}
}

func TestExplainEstimatesUnavailableKeepsStaticPlanForUnsupportedHarvest(t *testing.T) {
	estimate := EstimateExplain(context.Background(), `sem_extract(.msg; "summary")`, strings.NewReader(`{"msg":"hello"}`), input.ModeAuto)
	if estimate.Status != ExplainEstimateUnavailableHarvest {
		t.Fatalf("Status = %q reason=%q, want unsupported harvest", estimate.Status, estimate.Reason)
	}
	if estimate.StaticCallSites != 1 || len(estimate.SemanticPlan.Semantic) != 1 {
		t.Fatalf("static plan missing after unsupported harvest: %#v", estimate)
	}
}

func TestExplainEstimatesNullAndRawInputModes(t *testing.T) {
	nullEstimate := EstimateExplain(context.Background(), `sem_match(.; "")`, strings.NewReader(`ignored`), input.ModeNull)
	if nullEstimate.Status != ExplainEstimateAvailable || nullEstimate.InputFrames != 1 || nullEstimate.PostDedupJudgements != 1 {
		t.Fatalf("null input estimate = %#v, want one available null-frame judgement", nullEstimate)
	}
	rawEstimate := EstimateExplain(context.Background(), `sem_match(.; "urgent")`, strings.NewReader("urgent\nother\nurgent\n"), input.ModeRaw)
	if rawEstimate.Status != ExplainEstimateAvailable || rawEstimate.InputFrames != 3 || rawEstimate.HarvestedJudgements != 3 || rawEstimate.PostDedupJudgements != 2 {
		t.Fatalf("raw input estimate = %#v, want three frames/two distinct judgements", rawEstimate)
	}
}
