package mqtt

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declaredIntentKinds parses types.go and returns the value of every constant
// declared with type IntentKind.
func declaredIntentKinds(t *testing.T) []IntentKind {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "types.go", nil, 0)
	require.NoError(t, err)
	var out []IntentKind
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if ident, ok := vs.Type.(*ast.Ident); !ok || ident.Name != "IntentKind" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				require.True(t, ok, "IntentKind constants must be string literals")
				s, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				out = append(out, IntentKind(s))
			}
		}
	}
	return out
}

// Expected use: IntentKinds lists exactly the declared kinds, so a new kind
// cannot be added without the runner's exhaustive intent tests seeing it.
func TestIntentKindsComplete(t *testing.T) {
	declared := declaredIntentKinds(t)
	require.NotEmpty(t, declared)
	assert.ElementsMatch(t, declared, IntentKinds())
}

// Edge case: no kind is listed twice.
func TestIntentKindsUnique(t *testing.T) {
	seen := map[IntentKind]bool{}
	for _, k := range IntentKinds() {
		assert.False(t, seen[k], "duplicate intent kind %q", k)
		seen[k] = true
	}
}
