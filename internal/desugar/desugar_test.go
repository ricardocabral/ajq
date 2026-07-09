package desugar_test

import (
	"testing"

	"github.com/itchyny/gojq"
	"github.com/ricardocabral/ajq/internal/desugar"
)

func TestRewriteMatchOperators(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "simple match",
			in:   `.x =~ "s"`,
			want: `sem_match(.x; "s")`,
		},
		{
			name: "implicit dot match",
			in:   `. =~ "s"`,
			want: `sem_match("s")`,
		},
		{
			name: "implicit dot negated match",
			in:   `. !~ "s"`,
			want: `(sem_match("s") | not)`,
		},
		{
			name: "negated match is grouped",
			in:   `.x !~ "s"`,
			want: `(sem_match(.x; "s") | not)`,
		},
		{
			name: "inside select",
			in:   `select(.feedback =~ "angry/frustrated") | .id`,
			want: `select(sem_match(.feedback; "angry/frustrated")) | .id`,
		},
		{
			name: "parenthesized piped left operand",
			in:   `(.msg | ascii_downcase) =~ "auth failure"`,
			want: `sem_match((.msg | ascii_downcase); "auth failure")`,
		},
		{
			name: "bracketed index left operand",
			in:   `.users[0].feedback =~ "angry"`,
			want: `sem_match(.users[0].feedback; "angry")`,
		},
		{
			name: "escaped quotes in right string",
			in:   `.msg =~ "said \"hello\""`,
			want: `sem_match(.msg; "said \"hello\"")`,
		},
		{
			name: "plain string is protected",
			in:   `"literal =~ and !~ stay"`,
			want: `"literal =~ and !~ stay"`,
		},
		{
			name: "interpolation expression rewrites while literal text stays",
			in:   `"literal =~ text \(.msg =~ "urgent")"`,
			want: `"literal =~ text \(sem_match(.msg; "urgent"))"`,
		},
		{
			name: "nested string inside interpolation is protected",
			in:   `"\("inner =~ literal")"`,
			want: `"\("inner =~ literal")"`,
		},
		{
			name: "if condition left boundary",
			in:   `if .msg =~ "x" then .a else .b end`,
			want: `if sem_match(.msg; "x") then .a else .b end`,
		},
		{
			name: "boolean and left boundary",
			in:   `.kind == "ticket" and .msg =~ "x"`,
			want: `.kind == "ticket" and sem_match(.msg; "x")`,
		},
		{
			name: "chained boolean rewrites independently",
			in:   `.a =~ "x" and .b =~ "y"`,
			want: `sem_match(.a; "x") and sem_match(.b; "y")`,
		},
		{
			name: "try catch right boundary",
			in:   `try .msg =~ "x" catch false`,
			want: `try sem_match(.msg; "x") catch false`,
		},
		{
			name: "elif right and left boundaries",
			in:   `if .a =~ "x" then true elif .b =~ "y" then false else null end`,
			want: `if sem_match(.a; "x") then true elif sem_match(.b; "y") then false else null end`,
		},
		{
			name: "string literal left operand with pipe",
			in:   `"a|b" =~ "x"`,
			want: `sem_match("a|b"; "x")`,
		},
		{
			name: "string literal left operand with comma",
			in:   `"a,b" =~ "x"`,
			want: `sem_match("a,b"; "x")`,
		},
		{
			name: "if no-space keyword boundary keeps separator",
			in:   `if(.msg) =~ "x" then .a else .b end`,
			want: `if sem_match((.msg); "x") then .a else .b end`,
		},
		{
			name: "and no-space keyword boundary keeps separator",
			in:   `.a and(.b) =~ "x"`,
			want: `.a and sem_match((.b); "x")`,
		},
		{
			name: "try no-space keyword boundary keeps separator",
			in:   `try(.msg) =~ "x" catch false`,
			want: `try sem_match((.msg); "x") catch false`,
		},
		{
			name: "line comment is protected",
			in:   ".x # =~ \"a\"\n | .y",
			want: ".x # =~ \"a\"\n | .y",
		},
		{
			name: "line comment after rhs remains outside rewrite",
			in:   ".x =~ \"a\" # !~ ignored\n | .y",
			want: "sem_match(.x; \"a\") # !~ ignored\n | .y",
		},
		{
			name: "assignment rhs boundary",
			in:   `.flag = .msg =~ "urgent"`,
			want: `.flag = sem_match(.msg; "urgent")`,
		},
		{
			name: "update assignment rhs boundary",
			in:   `.flag |= .msg =~ "urgent"`,
			want: `.flag |= sem_match(.msg; "urgent")`,
		},
		{
			name: "add assignment rhs boundary",
			in:   `.flag += .msg =~ "urgent"`,
			want: `.flag += sem_match(.msg; "urgent")`,
		},
		{
			name: "lhs adjacent comment is trimmed",
			in:   ".msg # lhs comment\n =~ \"urgent\"",
			want: "sem_match(.msg; \"urgent\")",
		},
		{
			name: "rhs adjacent comment is skipped",
			in:   ".msg =~ # rhs comment\n \"urgent\"",
			want: "sem_match(.msg; \"urgent\")",
		},
		{
			name: "if expression left operand",
			in:   `if .a then .b else .c end =~ "x"`,
			want: `sem_match(if .a then .b else .c end; "x")`,
		},
		{
			name: "if expression right operand",
			in:   `.x =~ if .a then "x" else "y" end`,
			want: `sem_match(.x; if .a then "x" else "y" end)`,
		},
		{
			name: "nested if expression left operand",
			in:   `if .a then if .b then .c else .d end else .e end =~ "x"`,
			want: `sem_match(if .a then if .b then .c else .d end else .e end; "x")`,
		},
		{
			name: "try catch expression right operand",
			in:   `.x =~ try "a" catch "b"`,
			want: `sem_match(.x; try "a" catch "b")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := desugar.Rewrite(tt.in)
			if err != nil {
				t.Fatalf("Rewrite returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Rewrite() = %q, want %q", got, tt.want)
			}
			mustParse(t, got)
		})
	}
}

func TestRewriteReportsMissingOperand(t *testing.T) {
	if _, err := desugar.Rewrite(`.msg =~`); err == nil {
		t.Fatal("expected missing RHS to fail")
	}
}

func mustParse(t *testing.T, src string) {
	t.Helper()
	query, err := gojq.Parse(src)
	if err != nil {
		t.Fatalf("gojq.Parse(%q) returned error: %v", src, err)
	}
	_, err = gojq.Compile(query, gojq.WithFunction("sem_match", 1, 2, func(any, []any) any { return true }))
	if err != nil {
		t.Fatalf("gojq.Compile(%q) returned error: %v", src, err)
	}
}
