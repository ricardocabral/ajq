// Package desugar rewrites ajq-only surface syntax into jq function-core
// syntax before gojq parses the query.
package desugar

import (
	"fmt"
	"strings"
	"unicode"
)

// Rewrite converts ajq's jq-adjacent infix semantic match operators into the
// sem_match function form understood by the semantic planner/executor.
func Rewrite(src string) (string, error) {
	withInterpolations, err := rewriteInterpolations(src)
	if err != nil {
		return "", err
	}

	out := withInterpolations
	for searchFrom := 0; ; {
		op, negated, ok := nextOperator(out, searchFrom)
		if !ok {
			return out, nil
		}

		leftStart := leftOperandStart(out, op)
		leftEnd := trimLeftEnd(out, op)
		rightStart := trimRightStart(out, op+2)
		rightEnd, err := rightOperandEnd(out, rightStart)
		if err != nil {
			return "", err
		}
		if leftStart >= leftEnd || rightStart >= rightEnd {
			return "", fmt.Errorf("desugar: missing operand for %q at byte %d", out[op:op+2], op)
		}

		left, err := Rewrite(strings.TrimSpace(out[leftStart:leftEnd]))
		if err != nil {
			return "", err
		}
		right, err := Rewrite(strings.TrimSpace(out[rightStart:rightEnd]))
		if err != nil {
			return "", err
		}

		replacement := fmt.Sprintf("sem_match(%s; %s)", left, right)
		if left == "." {
			replacement = fmt.Sprintf("sem_match(%s)", right)
		}
		if negated {
			replacement = "(" + replacement + " | not)"
		}
		if needsSeparatorBefore(out, leftStart, replacement) {
			replacement = " " + replacement
		}
		out = out[:leftStart] + replacement + out[rightEnd:]
		searchFrom = leftStart + len(replacement)
	}
}

func rewriteInterpolations(src string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(src); {
		if src[i] == '#' {
			next := skipLineComment(src, i)
			b.WriteString(src[i:next])
			i = next
			continue
		}
		if src[i] != '"' {
			b.WriteByte(src[i])
			i++
			continue
		}
		rewritten, next, err := rewriteString(src, i)
		if err != nil {
			return "", err
		}
		b.WriteString(rewritten)
		i = next
	}
	return b.String(), nil
}

func rewriteString(src string, start int) (string, int, error) {
	var b strings.Builder
	b.WriteByte(src[start])
	for i := start + 1; i < len(src); {
		if src[i] == '\\' {
			if i+1 < len(src) && src[i+1] == '(' {
				close, err := matchingInterpolationClose(src, i+1)
				if err != nil {
					return "", 0, err
				}
				body, err := Rewrite(src[i+2 : close])
				if err != nil {
					return "", 0, err
				}
				b.WriteString("\\(")
				b.WriteString(body)
				b.WriteByte(')')
				i = close + 1
				continue
			}
			if i+1 < len(src) {
				b.WriteByte(src[i])
				b.WriteByte(src[i+1])
				i += 2
				continue
			}
		}
		b.WriteByte(src[i])
		if src[i] == '"' {
			return b.String(), i + 1, nil
		}
		i++
	}
	return "", 0, fmt.Errorf("desugar: unterminated string literal")
}

func matchingInterpolationClose(src string, open int) (int, error) {
	depth := 1
	for i := open + 1; i < len(src); i++ {
		switch src[i] {
		case '#':
			i = skipLineComment(src, i) - 1
		case '"':
			next, err := skipJQString(src, i)
			if err != nil {
				return 0, err
			}
			i = next - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("desugar: unterminated interpolation")
}

func skipJQString(src string, start int) (int, error) {
	for i := start + 1; i < len(src); i++ {
		if src[i] == '\\' {
			if i+1 < len(src) && src[i+1] == '(' {
				close, err := matchingInterpolationClose(src, i+1)
				if err != nil {
					return 0, err
				}
				i = close
				continue
			}
			i++
			continue
		}
		if src[i] == '"' {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("desugar: unterminated string literal")
}

func skipString(src string, start int) (string, int, error) {
	next, err := skipJQString(src, start)
	if err != nil {
		return "", 0, err
	}
	return src[start:next], next, nil
}

func nextOperator(src string, start int) (int, bool, bool) {
	for i := start; i < len(src)-1; i++ {
		switch src[i] {
		case '#':
			i = skipLineComment(src, i) - 1
		case '"':
			_, next, err := skipString(src, i)
			if err != nil {
				return 0, false, false
			}
			i = next - 1
		case '=':
			if src[i+1] == '~' {
				return i, false, true
			}
		case '!':
			if src[i+1] == '~' {
				return i, true, true
			}
		}
	}
	return 0, false, false
}

func trimLeftEnd(src string, end int) int {
	for {
		for end > 0 && unicode.IsSpace(rune(src[end-1])) {
			end--
		}
		lineStart := strings.LastIndexByte(src[:end], '\n') + 1
		if comment := commentStart(src, lineStart, end); comment >= 0 {
			end = comment
			continue
		}
		return end
	}
}

func trimRightStart(src string, start int) int {
	for start < len(src) {
		for start < len(src) && unicode.IsSpace(rune(src[start])) {
			start++
		}
		if start < len(src) && src[start] == '#' {
			start = skipLineComment(src, start)
			continue
		}
		break
	}
	return start
}

func leftOperandStart(src string, op int) int {
	targetParen, targetSquare, targetBrace, err := depthAt(src, op)
	if err != nil {
		return 0
	}
	if start, ok := controlLHSStart(src, op, targetParen, targetSquare, targetBrace); ok {
		return start
	}
	lastBoundary := 0
	paren, square, brace := 0, 0, 0
	for i := 0; i < op; i++ {
		if src[i] == '#' {
			i = skipLineComment(src, i) - 1
			continue
		}
		if src[i] == '"' {
			next, err := skipJQString(src, i)
			if err != nil {
				return lastBoundary
			}
			i = next - 1
			continue
		}

		if paren == targetParen && square == targetSquare && brace == targetBrace {
			if assignmentEnd, ok := assignmentBoundaryEnd(src, i); ok {
				lastBoundary = assignmentEnd
				i = assignmentEnd - 1
				continue
			}
			if (src[i] == '|' && (i+1 >= len(src) || src[i+1] != '=')) || src[i] == ',' || src[i] == ';' || src[i] == ':' {
				lastBoundary = i + 1
				continue
			}
			if wordBoundaryEnd, ok := leftWordDelimiterEnd(src, i); ok {
				lastBoundary = wordBoundaryEnd
				i = wordBoundaryEnd - 1
				continue
			}
		}

		switch src[i] {
		case '(':
			paren++
			if paren == targetParen && square == targetSquare && brace == targetBrace {
				lastBoundary = i + 1
			}
		case '[':
			square++
			if paren == targetParen && square == targetSquare && brace == targetBrace {
				lastBoundary = i + 1
			}
		case '{':
			brace++
			if paren == targetParen && square == targetSquare && brace == targetBrace {
				lastBoundary = i + 1
			}
		case ')':
			paren--
		case ']':
			square--
		case '}':
			brace--
		}
	}
	return lastBoundary
}

func depthAt(src string, pos int) (int, int, int, error) {
	paren, square, brace := 0, 0, 0
	for i := 0; i < pos; i++ {
		switch src[i] {
		case '#':
			i = skipLineComment(src, i) - 1
		case '"':
			next, err := skipJQString(src, i)
			if err != nil {
				return 0, 0, 0, err
			}
			i = next - 1
		case '(':
			paren++
		case '[':
			square++
		case '{':
			brace++
		case ')':
			paren--
		case ']':
			square--
		case '}':
			brace--
		}
	}
	return paren, square, brace, nil
}

func assignmentBoundaryEnd(src string, i int) (int, bool) {
	for _, op := range []string{"?//=", "//=", "|=", "+=", "-=", "*=", "/=", "%="} {
		if strings.HasPrefix(src[i:], op) {
			end := i + len(op)
			for end < len(src) && unicode.IsSpace(rune(src[end])) {
				end++
			}
			return end, true
		}
	}
	if src[i] == '=' && (i+1 >= len(src) || (src[i+1] != '=' && src[i+1] != '~')) {
		end := i + 1
		for end < len(src) && unicode.IsSpace(rune(src[end])) {
			end++
		}
		return end, true
	}
	return 0, false
}

func leftWordDelimiterEnd(src string, i int) (int, bool) {
	if !isWordBoundaryBefore(src, i) {
		return 0, false
	}
	for _, word := range []string{"if", "then", "else", "elif", "try", "catch", "and", "or"} {
		end := i + len(word)
		if strings.HasPrefix(src[i:], word) && isWordBoundaryAfter(src, end) {
			for end < len(src) && unicode.IsSpace(rune(src[end])) {
				end++
			}
			return end, true
		}
	}
	return 0, false
}

func rightOperandEnd(src string, start int) (int, error) {
	if end, ok, err := controlRHSEnd(src, start); ok || err != nil {
		return end, err
	}
	ignoreCatchDelimiter := hasWordAt(src, start, "try")
	paren, square, brace := 0, 0, 0
	for i := start; i < len(src); i++ {
		if paren == 0 && square == 0 && brace == 0 {
			if isRightDelimiter(src, i, ignoreCatchDelimiter) {
				return trimLeftEnd(src, i), nil
			}
		}
		switch src[i] {
		case '#':
			i = skipLineComment(src, i) - 1
		case '"':
			_, next, err := skipString(src, i)
			if err != nil {
				return 0, err
			}
			i = next - 1
		case '(':
			paren++
		case '[':
			square++
		case '{':
			brace++
		case ')':
			if paren == 0 {
				return trimLeftEnd(src, i), nil
			}
			paren--
		case ']':
			if square == 0 {
				return trimLeftEnd(src, i), nil
			}
			square--
		case '}':
			if brace == 0 {
				return trimLeftEnd(src, i), nil
			}
			brace--
		}
	}
	if paren != 0 || square != 0 || brace != 0 {
		return 0, fmt.Errorf("desugar: unbalanced right operand")
	}
	return trimLeftEnd(src, len(src)), nil
}

func controlLHSStart(src string, op, targetParen, targetSquare, targetBrace int) (int, bool) {
	end := trimLeftEnd(src, op)
	if !wordImmediatelyBefore(src, end, "end") {
		return 0, false
	}
	paren, square, brace := 0, 0, 0
	var ifStack []int
	for i := 0; i < end; i++ {
		if src[i] == '#' {
			i = skipLineComment(src, i) - 1
			continue
		}
		if src[i] == '"' {
			next, err := skipJQString(src, i)
			if err != nil {
				return 0, false
			}
			i = next - 1
			continue
		}
		if paren == targetParen && square == targetSquare && brace == targetBrace {
			if assignmentEnd, ok := assignmentBoundaryEnd(src, i); ok {
				ifStack = nil
				i = assignmentEnd - 1
				continue
			}
			if (src[i] == '|' && (i+1 >= len(src) || src[i+1] != '=')) || src[i] == ',' || src[i] == ';' || src[i] == ':' {
				ifStack = nil
				continue
			}
			if hasWordAt(src, i, "if") {
				ifStack = append(ifStack, i)
				i += len("if") - 1
				continue
			}
			if hasWordAt(src, i, "end") {
				if len(ifStack) == 0 {
					return 0, false
				}
				match := ifStack[len(ifStack)-1]
				ifStack = ifStack[:len(ifStack)-1]
				if i+len("end") == end {
					return match, true
				}
				i += len("end") - 1
				continue
			}
		}
		switch src[i] {
		case '(':
			paren++
		case '[':
			square++
		case '{':
			brace++
		case ')':
			paren--
		case ']':
			square--
		case '}':
			brace--
		}
	}
	return 0, false
}

func controlRHSEnd(src string, start int) (int, bool, error) {
	if !hasWordAt(src, start, "if") {
		return 0, false, nil
	}
	paren, square, brace := 0, 0, 0
	ifDepth := 0
	for i := start; i < len(src); i++ {
		if src[i] == '#' {
			i = skipLineComment(src, i) - 1
			continue
		}
		if src[i] == '"' {
			next, err := skipJQString(src, i)
			if err != nil {
				return 0, true, err
			}
			i = next - 1
			continue
		}
		if paren == 0 && square == 0 && brace == 0 {
			if hasWordAt(src, i, "if") {
				ifDepth++
				i += len("if") - 1
				continue
			}
			if hasWordAt(src, i, "end") {
				ifDepth--
				if ifDepth == 0 {
					return i + len("end"), true, nil
				}
				i += len("end") - 1
				continue
			}
		}
		switch src[i] {
		case '(':
			paren++
		case '[':
			square++
		case '{':
			brace++
		case ')':
			if paren > 0 {
				paren--
			}
		case ']':
			if square > 0 {
				square--
			}
		case '}':
			if brace > 0 {
				brace--
			}
		}
	}
	return 0, true, fmt.Errorf("desugar: unterminated if expression operand")
}

func wordImmediatelyBefore(src string, end int, word string) bool {
	start := end - len(word)
	return start >= 0 && hasWordAt(src, start, word) && start+len(word) == end
}

func hasWordAt(src string, i int, word string) bool {
	return i >= 0 && i+len(word) <= len(src) && strings.HasPrefix(src[i:], word) && isWordBoundaryBefore(src, i) && isWordBoundaryAfter(src, i+len(word))
}

func isRightDelimiter(src string, i int, ignoreCatch bool) bool {
	switch src[i] {
	case '|', ',', ';', ')', ']', '}', '#':
		return true
	}
	if !isWordBoundaryBefore(src, i) {
		return false
	}
	for _, word := range []string{"and", "or", "then", "else", "elif", "end", "as", "catch"} {
		if ignoreCatch && word == "catch" {
			continue
		}
		if strings.HasPrefix(src[i:], word) && isWordBoundaryAfter(src, i+len(word)) {
			return true
		}
	}
	return false
}

func needsSeparatorBefore(src string, start int, replacement string) bool {
	if start == 0 || replacement == "" {
		return false
	}
	prev := src[start-1]
	first := replacement[0]
	return isIdent(prev) && (isIdent(first) || first == '(')
}

func skipLineComment(src string, start int) int {
	for i := start; i < len(src); i++ {
		if src[i] == '\n' {
			return i
		}
	}
	return len(src)
}

func commentStart(src string, start, end int) int {
	for i := start; i < end; i++ {
		switch src[i] {
		case '"':
			next, err := skipJQString(src, i)
			if err != nil || next > end {
				return -1
			}
			i = next - 1
		case '#':
			return i
		}
	}
	return -1
}

func isWordBoundaryBefore(src string, i int) bool {
	if i == 0 {
		return true
	}
	return !isIdent(src[i-1])
}

func isWordBoundaryAfter(src string, i int) bool {
	if i >= len(src) {
		return true
	}
	return !isIdent(src[i])
}

func isIdent(b byte) bool {
	return b == '_' || b == '$' || b == '.' || b == '-' || b == '?' || ('0' <= b && b <= '9') || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}
