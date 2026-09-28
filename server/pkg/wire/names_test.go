package wire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

/*
TestEveryMsgTypeHasAName reads constants.go and asserts String() covers all of it.

This is the same trick the Android parity tests use — parse the declaration rather
than maintain a second list — and for the same reason: a hand-kept list of types is
a list that falls behind. It had already fallen a long way behind. Seventy of the
hundred and sixteen declared types fell through to "UNKNOWN", which is what every
log line, every metric label and every debug dump showed for them; the whole
secret-chat block, the whole billing block and every call type were among them.

MsgReserved is the one legitimate exception: it is the zero value, declared so an
empty or truncated frame cannot be read as a valid type, and it never travels.
*/
func TestEveryMsgTypeHasAName(t *testing.T) {
	declared := declaredMsgTypes(t)
	if len(declared) < 100 {
		t.Fatalf("parsed only %d MsgType constants; the parser has drifted from the source", len(declared))
	}

	named := namedMsgTypes(t)

	var missing []string
	for _, name := range declared {
		if name == "MsgReserved" {
			continue
		}
		if !named[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("String() has no case for %d types, so they log as UNKNOWN: %s",
			len(missing), strings.Join(missing, ", "))
	}
}

/*
TestMsgTypeNamesAreUniqueAndWellFormed guards the other direction.

Two types sharing a name is worse than a missing one: a metric keyed on the name
silently merges two different messages, and the merge is invisible in the numbers.
The shape check is cheap and catches a lowercase or hyphenated entry pasted from
another platform's spelling.
*/
func TestMsgTypeNamesAreUniqueAndWellFormed(t *testing.T) {
	shape := regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	seen := map[string]MsgType{}

	// Walk the numeric space rather than the identifiers: String() is what callers
	// see, and this asserts against its actual output.
	for n := 0; n <= 300; n++ {
		typ := MsgType(n)
		name := typ.String()
		if name == "UNKNOWN" {
			continue
		}
		if !shape.MatchString(name) {
			t.Errorf("MsgType(%d) is named %q, which is not SCREAMING_SNAKE_CASE", n, name)
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("MsgType(%d) and MsgType(%d) are both named %q", prev, typ, name)
		}
		seen[name] = typ
	}
}

// declaredMsgTypes returns every `MsgFoo MsgType = N` identifier in constants.go.
func declaredMsgTypes(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "constants.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing constants.go: %v", err)
	}

	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// The type is written on the spec itself in this file
			// (`MsgHello MsgType = 1`), not inherited from an earlier line.
			ident, ok := value.Type.(*ast.Ident)
			if !ok || ident.Name != "MsgType" {
				continue
			}
			for _, name := range value.Names {
				if strings.HasPrefix(name.Name, "Msg") {
					out = append(out, name.Name)
				}
			}
		}
	}
	return out
}

// namedMsgTypes returns the identifiers String() has a case for.
//
// Read from the source rather than by calling String() over the numeric space,
// because the point is to compare IDENTIFIERS: a case that returns a name for the
// wrong constant would pass a value-based check and still be wrong.
func namedMsgTypes(t *testing.T) map[string]bool {
	t.Helper()

	source, err := os.ReadFile("types.go")
	if err != nil {
		t.Fatalf("reading types.go: %v", err)
	}
	out := map[string]bool{}
	for _, match := range regexp.MustCompile(`case (Msg\w+):`).FindAllStringSubmatch(string(source), -1) {
		out[match[1]] = true
	}
	return out
}
