package plan

// Severity classifies whether a diagnostic blocks planning.
type Severity string

// SeverityError marks a diagnostic that blocks semantic planning.
const (
	SeverityError Severity = "error"
)

// DiagnosticCode is stable for tests, CLI output, and later explain/error UX.
type DiagnosticCode string

// DiagnosticParseError codes classify stable plan-time validation failures.
const (
	DiagnosticParseError     DiagnosticCode = "AJQ_PLAN_PARSE_ERROR"
	DiagnosticUnknownSemOp   DiagnosticCode = "AJQ_PLAN_UNKNOWN_SEM_OP"
	DiagnosticArity          DiagnosticCode = "AJQ_PLAN_ARITY"
	DiagnosticNonLiteralSpec DiagnosticCode = "AJQ_PLAN_NON_LITERAL_SPEC"
	DiagnosticMaxArity       DiagnosticCode = "AJQ_PLAN_MAX_ARITY"
	DiagnosticUnsupported    DiagnosticCode = "AJQ_PLAN_UNSUPPORTED_CONSTRUCT"
)

// Diagnostic describes a plan-time validation failure. All diagnostics emitted
// by Step 1 are errors; Build still returns a partial Plan containing all valid
// semantic nodes found before/after invalid calls.
type Diagnostic struct {
	Code     DiagnosticCode
	Severity Severity
	Message  string
	Op       string
	Source   Source
}

func errorDiagnostic(code DiagnosticCode, op, expression, message string) Diagnostic {
	return Diagnostic{
		Code:     code,
		Severity: SeverityError,
		Message:  message,
		Op:       op,
		Source:   unknownSource(expression),
	}
}
