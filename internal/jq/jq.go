// Package jq wraps github.com/itchyny/gojq behind the small execution
// contract ajq needs for deterministic pure-jq queries.
package jq

import (
	"fmt"

	"github.com/itchyny/gojq"
)

// Program is a parsed and compiled jq query. It is safe to reuse for multiple
// input values, which keeps NDJSON processing independent without reparsing for
// every frame.
type Program struct {
	query *gojq.Query
	code  *gojq.Code
}

// RunResult summarizes the values emitted by one query execution.
type RunResult struct {
	Emitted bool
	Last    any
}

// Compile parses and compiles a pure jq query with gojq.
func Compile(src string) (*Program, error) {
	return CompileWithOptions(src)
}

// CompileWithOptions parses and compiles a jq query with explicit gojq compiler
// options. Semantic execution uses this hook to register phase-local functions;
// pure jq callers use Compile and therefore keep the original option-free
// behavior.
func CompileWithOptions(src string, opts ...gojq.CompilerOption) (*Program, error) {
	query, err := gojq.Parse(src)
	if err != nil {
		return nil, err
	}
	code, err := gojq.Compile(query, opts...)
	if err != nil {
		return nil, err
	}
	return &Program{query: query, code: code}, nil
}

// Run executes the compiled query for one input value and calls emit for each
// yielded jq result in iterator order.
func (p *Program) Run(input any, emit func(any) error) (RunResult, error) {
	if p == nil || p.code == nil {
		return RunResult{}, fmt.Errorf("jq program is not compiled")
	}

	iter := p.code.Run(input)
	var result RunResult
	for {
		value, ok := iter.Next()
		if !ok {
			return result, nil
		}
		if err, ok := value.(error); ok {
			return result, err
		}

		result.Emitted = true
		result.Last = value
		if emit != nil {
			if err := emit(value); err != nil {
				return result, err
			}
		}
	}
}
