// Package semantics defines ajq's semantic operator metadata without any jq
// parser or executor dependencies.
package semantics

// MaxJQFunctionArity is the jq parser's maximum function arity.
const MaxJQFunctionArity = 30

// Kind classifies whether a semantic operator is used as a predicate or returns
// a value to the jq pipeline.
type Kind string

// KindPredicate values classify semantic operator usage.
const (
	KindPredicate Kind = "predicate"
	KindValue     Kind = "value"
)

// ReturnType is the jq-visible result shape for a semantic operator.
type ReturnType string

// ReturnBool values describe semantic operator result shapes.
const (
	ReturnBool   ReturnType = "bool"
	ReturnString ReturnType = "string"
	ReturnNumber ReturnType = "number"
)

// OpSpec describes one v1 semantic operator's stable planner contract.
type OpSpec struct {
	Name                    string
	Kind                    Kind
	Return                  ReturnType
	ImplicitMinArity        int
	ImplicitMaxArity        int
	ExplicitMinArity        int
	ExplicitMaxArity        int
	ImplicitMinSpecs        int
	ImplicitMaxSpecs        int
	ExplicitMinSpecs        int
	ExplicitMaxSpecs        int
	PreferImplicitAllString bool
}

func semMatchSpec() OpSpec {
	return OpSpec{
		Name: "sem_match", Kind: KindPredicate, Return: ReturnBool,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
	}
}

func semClassifySpec() OpSpec {
	return OpSpec{
		Name: "sem_classify", Kind: KindValue, Return: ReturnString,
		ImplicitMinArity: 2, ImplicitMaxArity: MaxJQFunctionArity, ExplicitMinArity: 3, ExplicitMaxArity: MaxJQFunctionArity,
		ImplicitMinSpecs: 2, ImplicitMaxSpecs: MaxJQFunctionArity, ExplicitMinSpecs: 2, ExplicitMaxSpecs: MaxJQFunctionArity - 1,
		PreferImplicitAllString: true,
	}
}

func semExtractSpec() OpSpec {
	return OpSpec{
		Name: "sem_extract", Kind: KindValue, Return: ReturnString,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
	}
}

func semScoreSpec() OpSpec {
	return OpSpec{
		Name: "sem_score", Kind: KindValue, Return: ReturnNumber,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
	}
}

func semNormSpec() OpSpec {
	return OpSpec{
		Name: "sem_norm", Kind: KindValue, Return: ReturnString,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
	}
}

func semRedactSpec() OpSpec {
	return OpSpec{
		Name: "sem_redact", Kind: KindValue, Return: ReturnString,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
	}
}

// Lookup returns metadata for a semantic operator name.
func Lookup(name string) (OpSpec, bool) {
	switch name {
	case "sem_match":
		return semMatchSpec(), true
	case "sem_classify":
		return semClassifySpec(), true
	case "sem_extract":
		return semExtractSpec(), true
	case "sem_score":
		return semScoreSpec(), true
	case "sem_norm":
		return semNormSpec(), true
	case "sem_redact":
		return semRedactSpec(), true
	default:
		return OpSpec{}, false
	}
}

// All returns a deterministic snapshot of the v1 semantic operator registry.
func All() []OpSpec {
	return []OpSpec{
		semMatchSpec(),
		semClassifySpec(),
		semExtractSpec(),
		semScoreSpec(),
		semNormSpec(),
		semRedactSpec(),
	}
}
