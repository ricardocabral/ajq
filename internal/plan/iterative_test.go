package plan

import "testing"

func TestIterativeStagesRecognizesLinearPipeline(t *testing.T) {
	q := `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "yes")) | .id`
	p, ds := Build(q)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if got, ok := IterativeStages(q, p); !ok || len(got.Stages) != 2 {
		t.Fatalf("IterativeStages = %#v, %v", got, ok)
	}
}
