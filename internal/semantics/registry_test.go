package semantics

import "testing"

func TestRegistryContainsV1Ops(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		ret  ReturnType
	}{
		{"sem_match", KindPredicate, ReturnBool},
		{"sem_classify", KindValue, ReturnString},
		{"sem_extract", KindValue, ReturnString},
		{"sem_score", KindValue, ReturnNumber},
		{"sem_norm", KindValue, ReturnString},
		{"sem_redact", KindValue, ReturnString},
	}
	for _, tt := range tests {
		spec, ok := Lookup(tt.name)
		if !ok {
			t.Fatalf("Lookup(%q) missing", tt.name)
		}
		if spec.Kind != tt.kind || spec.Return != tt.ret {
			t.Fatalf("Lookup(%q) kind/return = %s/%s, want %s/%s", tt.name, spec.Kind, spec.Return, tt.kind, tt.ret)
		}
	}
}

func TestRegistryArityContracts(t *testing.T) {
	match, _ := Lookup("sem_match")
	if match.ImplicitMinArity != 1 || match.ImplicitMaxArity != 1 || match.ExplicitMinArity != 2 || match.ExplicitMaxArity != 2 {
		t.Fatalf("sem_match arities = implicit %d..%d explicit %d..%d", match.ImplicitMinArity, match.ImplicitMaxArity, match.ExplicitMinArity, match.ExplicitMaxArity)
	}

	classify, _ := Lookup("sem_classify")
	if classify.ImplicitMinArity != 2 || classify.ImplicitMaxArity != MaxJQFunctionArity {
		t.Fatalf("sem_classify implicit arity = %d..%d", classify.ImplicitMinArity, classify.ImplicitMaxArity)
	}
	if classify.ExplicitMinArity != 3 || classify.ExplicitMaxArity != MaxJQFunctionArity {
		t.Fatalf("sem_classify explicit arity = %d..%d", classify.ExplicitMinArity, classify.ExplicitMaxArity)
	}
	if !classify.PreferImplicitAllString {
		t.Fatal("sem_classify must prefer implicit-dot for all-static-string calls")
	}
}

func TestAllRegistryOrder(t *testing.T) {
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
