package semantics

import "testing"

func TestStaticLookupKnownAndUnknown(t *testing.T) {
	match, ok := Lookup("sem_match")
	if !ok {
		t.Fatal("Lookup(sem_match) missing")
	}
	if match.Name != "sem_match" || match.Kind != KindPredicate || match.Return != ReturnBool {
		t.Fatalf("Lookup(sem_match) = %#v", match)
	}

	classify, ok := Lookup("sem_classify")
	if !ok {
		t.Fatal("Lookup(sem_classify) missing")
	}
	if classify.Name != "sem_classify" || classify.Kind != KindValue || classify.Return != ReturnString || !classify.PreferImplicitAllString {
		t.Fatalf("Lookup(sem_classify) = %#v", classify)
	}

	if got, ok := Lookup("sem_unknown"); ok || got != (OpSpec{}) {
		t.Fatalf("Lookup(sem_unknown) = %#v, %t; want zero, false", got, ok)
	}
}

func TestStaticAllOrder(t *testing.T) {
	got := All()
	want := []string{"sem_match", "sem_classify", "sem_extract", "sem_score", "sem_norm", "sem_redact"}
	if len(got) != len(want) {
		t.Fatalf("All len = %d, want %d", len(got), len(want))
	}
	for i, spec := range got {
		if spec.Name != want[i] {
			t.Fatalf("All[%d] = %q, want %q", i, spec.Name, want[i])
		}
	}
}

func TestStaticAllReturnsIndependentSnapshot(t *testing.T) {
	first := All()
	first[0] = OpSpec{Name: "mutated"}
	first[1].PreferImplicitAllString = false

	match, ok := Lookup("sem_match")
	if !ok || match.Name != "sem_match" || match.Kind != KindPredicate {
		t.Fatalf("Lookup after All mutation = %#v, %t", match, ok)
	}

	second := All()
	if second[0].Name != "sem_match" {
		t.Fatalf("All()[0] after prior slice mutation = %q, want sem_match", second[0].Name)
	}
	if !second[1].PreferImplicitAllString {
		t.Fatal("All()[1].PreferImplicitAllString changed after prior element mutation")
	}
}
