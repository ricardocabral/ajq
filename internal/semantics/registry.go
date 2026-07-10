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

// AvailabilityStatus describes an operator's supported execution surface.
type AvailabilityStatus string

// AvailabilityStatus values are the closed v1 availability vocabulary.
const (
	// AvailabilityShipped means an operator is supported in every execution context.
	AvailabilityShipped AvailabilityStatus = "shipped"
	// AvailabilityLimited means an operator is supported only in listed contexts.
	AvailabilityLimited AvailabilityStatus = "limited"
)

// ExecutionContext identifies a supported semantic execution context.
type ExecutionContext string

// ExecutionContext values are the closed v1 execution-context vocabulary.
const (
	// ContextAll means the operator is supported in all semantic execution contexts.
	ContextAll ExecutionContext = "all"
	// ContextInterleavedGated means the operator is supported in gated interleaved execution.
	ContextInterleavedGated ExecutionContext = "interleaved_gated"
	// ContextThreePhaseSortBy means the operator is supported as a three-phase sort key.
	ContextThreePhaseSortBy ExecutionContext = "three_phase_sort_by"
	// ContextThreePhaseGroupBy means the operator is supported as a three-phase grouping key.
	ContextThreePhaseGroupBy ExecutionContext = "three_phase_group_by"
)

// Limitation identifies a known, intentional availability restriction.
type Limitation string

// Limitation values are the closed v1 limitation vocabulary.
const (
	// LimitationNonGatedUnboundedFailsLoudly describes unsupported unbounded contexts.
	LimitationNonGatedUnboundedFailsLoudly Limitation = "non_gated_unbounded_fails_loudly"
)

// Availability is canonical, static metadata about an operator's execution
// support. It is deliberately separate from planner arity details so callers
// can safely expose it without constructing a backend or inspecting config.
// Its fixed storage keeps OpSpec comparable for planner callers.
type Availability struct {
	Status          AvailabilityStatus
	contexts        [3]ExecutionContext
	contextCount    int
	limitations     [1]Limitation
	limitationCount int
}

func availability(status AvailabilityStatus, contexts []ExecutionContext, limitations []Limitation) Availability {
	var value Availability
	value.Status = status
	value.contextCount = copy(value.contexts[:], contexts)
	value.limitationCount = copy(value.limitations[:], limitations)
	return value
}

// SupportedContexts returns the operator's supported execution contexts in
// deterministic declaration order.
func (a Availability) SupportedContexts() []ExecutionContext {
	return append([]ExecutionContext(nil), a.contexts[:a.contextCount]...)
}

// Limitations returns the operator's known limitations in deterministic
// declaration order.
func (a Availability) Limitations() []Limitation {
	return append([]Limitation(nil), a.limitations[:a.limitationCount]...)
}

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
	Availability            Availability
}

func semMatchSpec() OpSpec {
	return OpSpec{
		Name: "sem_match", Kind: KindPredicate, Return: ReturnBool,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
		Availability: availability(AvailabilityShipped, []ExecutionContext{ContextAll}, nil),
	}
}

func semClassifySpec() OpSpec {
	return OpSpec{
		Name: "sem_classify", Kind: KindValue, Return: ReturnString,
		ImplicitMinArity: 2, ImplicitMaxArity: MaxJQFunctionArity, ExplicitMinArity: 3, ExplicitMaxArity: MaxJQFunctionArity,
		ImplicitMinSpecs: 2, ImplicitMaxSpecs: MaxJQFunctionArity, ExplicitMinSpecs: 2, ExplicitMaxSpecs: MaxJQFunctionArity - 1,
		PreferImplicitAllString: true,
		Availability:            availability(AvailabilityShipped, []ExecutionContext{ContextAll}, nil),
	}
}

func semExtractSpec() OpSpec {
	return OpSpec{
		Name: "sem_extract", Kind: KindValue, Return: ReturnString,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
		Availability: availability(AvailabilityLimited, []ExecutionContext{ContextInterleavedGated}, []Limitation{LimitationNonGatedUnboundedFailsLoudly}),
	}
}

func semScoreSpec() OpSpec {
	return OpSpec{
		Name: "sem_score", Kind: KindValue, Return: ReturnNumber,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
		Availability: availability(AvailabilityLimited, []ExecutionContext{ContextInterleavedGated, ContextThreePhaseSortBy}, []Limitation{LimitationNonGatedUnboundedFailsLoudly}),
	}
}

func semNormSpec() OpSpec {
	return OpSpec{
		Name: "sem_norm", Kind: KindValue, Return: ReturnString,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
		Availability: availability(AvailabilityLimited, []ExecutionContext{ContextInterleavedGated, ContextThreePhaseGroupBy}, []Limitation{LimitationNonGatedUnboundedFailsLoudly}),
	}
}

func semRedactSpec() OpSpec {
	return OpSpec{
		Name: "sem_redact", Kind: KindValue, Return: ReturnString,
		ImplicitMinArity: 1, ImplicitMaxArity: 1, ExplicitMinArity: 2, ExplicitMaxArity: 2,
		ImplicitMinSpecs: 1, ImplicitMaxSpecs: 1, ExplicitMinSpecs: 1, ExplicitMaxSpecs: 1,
		Availability: availability(AvailabilityLimited, []ExecutionContext{ContextInterleavedGated}, []Limitation{LimitationNonGatedUnboundedFailsLoudly}),
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
