package engine

import "github.com/itchyny/gojq"

func hasUnsafeGeneratorContext(src string) (bool, error) {
	query, err := gojq.Parse(src)
	if err != nil {
		return false, err
	}
	return queryHasUnsafeGeneratorContext(query), nil
}

func queryHasUnsafeGeneratorContext(q *gojq.Query) bool {
	if q == nil {
		return false
	}
	if len(q.FuncDefs) > 0 {
		return true
	}
	if termHasUnsafeGeneratorContext(q.Term) || queryHasUnsafeGeneratorContext(q.Left) || queryHasUnsafeGeneratorContext(q.Right) {
		return true
	}
	for _, fd := range q.FuncDefs {
		if queryHasUnsafeGeneratorContext(fd.Body) {
			return true
		}
	}
	for _, pattern := range q.Patterns {
		if patternHasUnsafeGeneratorContext(pattern) {
			return true
		}
	}
	return false
}

func termHasUnsafeGeneratorContext(t *gojq.Term) bool {
	if t == nil {
		return false
	}
	switch t.Type {
	case gojq.TermTypeFunc:
		if t.Func != nil {
			switch t.Func.Name {
			case "all", "any", "first", "last", "limit", "nth", "until", "while":
				return true
			}
			for _, arg := range t.Func.Args {
				if queryHasUnsafeGeneratorContext(arg) {
					return true
				}
			}
		}
	case gojq.TermTypeArray:
		if t.Array != nil {
			if queryContainsSemanticFunc(t.Array.Query) {
				return true
			}
			if queryHasUnsafeGeneratorContext(t.Array.Query) {
				return true
			}
		}
	case gojq.TermTypeIndex:
		if indexHasUnsafeGeneratorContext(t.Index) {
			return true
		}
	case gojq.TermTypeObject:
		if objectHasUnsafeGeneratorContext(t.Object) {
			return true
		}
	case gojq.TermTypeString, gojq.TermTypeFormat:
		if stringHasUnsafeGeneratorContext(t.Str) {
			return true
		}
	case gojq.TermTypeUnary:
		if t.Unary != nil && termHasUnsafeGeneratorContext(t.Unary.Term) {
			return true
		}
	case gojq.TermTypeIf:
		if ifHasUnsafeGeneratorContext(t.If) {
			return true
		}
	case gojq.TermTypeTry:
		if t.Try != nil && (queryHasUnsafeGeneratorContext(t.Try.Body) || queryHasUnsafeGeneratorContext(t.Try.Catch)) {
			return true
		}
	case gojq.TermTypeReduce:
		if t.Reduce != nil && (queryContainsSemanticFunc(t.Reduce.Query) || queryContainsSemanticFunc(t.Reduce.Start) || queryContainsSemanticFunc(t.Reduce.Update) || queryHasUnsafeGeneratorContext(t.Reduce.Query) || patternHasUnsafeGeneratorContext(t.Reduce.Pattern) || queryHasUnsafeGeneratorContext(t.Reduce.Start) || queryHasUnsafeGeneratorContext(t.Reduce.Update)) {
			return true
		}
	case gojq.TermTypeForeach:
		if t.Foreach != nil && (queryContainsSemanticFunc(t.Foreach.Query) || queryContainsSemanticFunc(t.Foreach.Start) || queryContainsSemanticFunc(t.Foreach.Update) || queryContainsSemanticFunc(t.Foreach.Extract) || queryHasUnsafeGeneratorContext(t.Foreach.Query) || patternHasUnsafeGeneratorContext(t.Foreach.Pattern) || queryHasUnsafeGeneratorContext(t.Foreach.Start) || queryHasUnsafeGeneratorContext(t.Foreach.Update) || queryHasUnsafeGeneratorContext(t.Foreach.Extract)) {
			return true
		}
	case gojq.TermTypeLabel:
		if t.Label != nil && queryHasUnsafeGeneratorContext(t.Label.Body) {
			return true
		}
	case gojq.TermTypeQuery:
		if queryHasUnsafeGeneratorContext(t.Query) {
			return true
		}
	}
	for _, suffix := range t.SuffixList {
		if suffix != nil && indexHasUnsafeGeneratorContext(suffix.Index) {
			return true
		}
	}
	return false
}

func queryContainsSemanticFunc(q *gojq.Query) bool {
	if q == nil {
		return false
	}
	if termContainsSemanticFunc(q.Term) || queryContainsSemanticFunc(q.Left) || queryContainsSemanticFunc(q.Right) {
		return true
	}
	for _, fd := range q.FuncDefs {
		if queryContainsSemanticFunc(fd.Body) {
			return true
		}
	}
	for _, pattern := range q.Patterns {
		if patternContainsSemanticFunc(pattern) {
			return true
		}
	}
	return false
}

func termContainsSemanticFunc(t *gojq.Term) bool {
	if t == nil {
		return false
	}
	switch t.Type {
	case gojq.TermTypeFunc:
		if t.Func != nil {
			if len(t.Func.Name) >= 4 && t.Func.Name[:4] == "sem_" {
				return true
			}
			for _, arg := range t.Func.Args {
				if queryContainsSemanticFunc(arg) {
					return true
				}
			}
		}
	case gojq.TermTypeArray:
		return t.Array != nil && queryContainsSemanticFunc(t.Array.Query)
	case gojq.TermTypeIndex:
		return indexContainsSemanticFunc(t.Index)
	case gojq.TermTypeObject:
		return objectContainsSemanticFunc(t.Object)
	case gojq.TermTypeString, gojq.TermTypeFormat:
		return stringContainsSemanticFunc(t.Str)
	case gojq.TermTypeUnary:
		return t.Unary != nil && termContainsSemanticFunc(t.Unary.Term)
	case gojq.TermTypeIf:
		return ifContainsSemanticFunc(t.If)
	case gojq.TermTypeTry:
		return t.Try != nil && (queryContainsSemanticFunc(t.Try.Body) || queryContainsSemanticFunc(t.Try.Catch))
	case gojq.TermTypeReduce:
		return t.Reduce != nil && (queryContainsSemanticFunc(t.Reduce.Query) || patternContainsSemanticFunc(t.Reduce.Pattern) || queryContainsSemanticFunc(t.Reduce.Start) || queryContainsSemanticFunc(t.Reduce.Update))
	case gojq.TermTypeForeach:
		return t.Foreach != nil && (queryContainsSemanticFunc(t.Foreach.Query) || patternContainsSemanticFunc(t.Foreach.Pattern) || queryContainsSemanticFunc(t.Foreach.Start) || queryContainsSemanticFunc(t.Foreach.Update) || queryContainsSemanticFunc(t.Foreach.Extract))
	case gojq.TermTypeLabel:
		return t.Label != nil && queryContainsSemanticFunc(t.Label.Body)
	case gojq.TermTypeQuery:
		return queryContainsSemanticFunc(t.Query)
	}
	for _, suffix := range t.SuffixList {
		if suffix != nil && indexContainsSemanticFunc(suffix.Index) {
			return true
		}
	}
	return false
}

func objectHasUnsafeGeneratorContext(o *gojq.Object) bool {
	if o == nil {
		return false
	}
	for _, kv := range o.KeyVals {
		if kv != nil && (stringHasUnsafeGeneratorContext(kv.KeyString) || queryHasUnsafeGeneratorContext(kv.KeyQuery) || queryHasUnsafeGeneratorContext(kv.Val)) {
			return true
		}
	}
	return false
}

func objectContainsSemanticFunc(o *gojq.Object) bool {
	if o == nil {
		return false
	}
	for _, kv := range o.KeyVals {
		if kv != nil && (stringContainsSemanticFunc(kv.KeyString) || queryContainsSemanticFunc(kv.KeyQuery) || queryContainsSemanticFunc(kv.Val)) {
			return true
		}
	}
	return false
}

func stringHasUnsafeGeneratorContext(s *gojq.String) bool {
	if s == nil {
		return false
	}
	for _, q := range s.Queries {
		if queryHasUnsafeGeneratorContext(q) {
			return true
		}
	}
	return false
}

func stringContainsSemanticFunc(s *gojq.String) bool {
	if s == nil {
		return false
	}
	for _, q := range s.Queries {
		if queryContainsSemanticFunc(q) {
			return true
		}
	}
	return false
}

func indexHasUnsafeGeneratorContext(i *gojq.Index) bool {
	return i != nil && (stringHasUnsafeGeneratorContext(i.Str) || queryHasUnsafeGeneratorContext(i.Start) || queryHasUnsafeGeneratorContext(i.End))
}

func indexContainsSemanticFunc(i *gojq.Index) bool {
	return i != nil && (stringContainsSemanticFunc(i.Str) || queryContainsSemanticFunc(i.Start) || queryContainsSemanticFunc(i.End))
}

func ifHasUnsafeGeneratorContext(i *gojq.If) bool {
	if i == nil {
		return false
	}
	if queryHasUnsafeGeneratorContext(i.Cond) || queryHasUnsafeGeneratorContext(i.Then) || queryHasUnsafeGeneratorContext(i.Else) {
		return true
	}
	for _, elif := range i.Elif {
		if elif != nil && (queryHasUnsafeGeneratorContext(elif.Cond) || queryHasUnsafeGeneratorContext(elif.Then)) {
			return true
		}
	}
	return false
}

func ifContainsSemanticFunc(i *gojq.If) bool {
	if i == nil {
		return false
	}
	if queryContainsSemanticFunc(i.Cond) || queryContainsSemanticFunc(i.Then) || queryContainsSemanticFunc(i.Else) {
		return true
	}
	for _, elif := range i.Elif {
		if elif != nil && (queryContainsSemanticFunc(elif.Cond) || queryContainsSemanticFunc(elif.Then)) {
			return true
		}
	}
	return false
}

func patternHasUnsafeGeneratorContext(p *gojq.Pattern) bool {
	if p == nil {
		return false
	}
	for _, elem := range p.Array {
		if patternHasUnsafeGeneratorContext(elem) {
			return true
		}
	}
	for _, obj := range p.Object {
		if obj != nil && (stringHasUnsafeGeneratorContext(obj.KeyString) || queryHasUnsafeGeneratorContext(obj.KeyQuery) || patternHasUnsafeGeneratorContext(obj.Val)) {
			return true
		}
	}
	return false
}

func patternContainsSemanticFunc(p *gojq.Pattern) bool {
	if p == nil {
		return false
	}
	for _, elem := range p.Array {
		if patternContainsSemanticFunc(elem) {
			return true
		}
	}
	for _, obj := range p.Object {
		if obj != nil && (stringContainsSemanticFunc(obj.KeyString) || queryContainsSemanticFunc(obj.KeyQuery) || patternContainsSemanticFunc(obj.Val)) {
			return true
		}
	}
	return false
}
