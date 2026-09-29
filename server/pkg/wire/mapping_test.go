package wire

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

/*
 * Marshal is deliberately panic-free: `b, _ := bodyCodec.Marshal(v)` discards the
 * error so a malformed body cannot take a connection down. The cost is that a
 * type with no protobuf mapping marshals to ZERO BYTES with nothing reported
 * anywhere.
 *
 * That trap has already cost this project once. internal/fanout built its push
 * jobs as a map[string]any and encoded them with Marshal; the map had no mapping,
 * so every push job on the bus was empty, the notify worker could not decode one,
 * and no notification was ever delivered — with no log line saying so.
 *
 * The mapping itself is a type switch, so the only thing between a NEW body type
 * and the same silent failure is somebody remembering to add a case. The test
 * below is that somebody: it discovers the body types from the source rather than
 * from a hand-written list, so it cannot quietly fall behind the package it
 * guards.
 */

// declaredBodyTypes parses the package's own source for `type XBody struct`.
//
// Reflection cannot enumerate a package's types at runtime, and a hand-kept list
// would drift — which is exactly the failure mode this test exists to prevent.
// Reading the declarations makes a new type covered the moment it is written.
func declaredBodyTypes(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	// parser.ParseDir is deprecated in favour of go/packages, which would also
	// type-check — but this test deliberately does NOT want that: it reads the
	// package's own source to discover every declared *Body type, and pulling in
	// a type-checking loader to walk one directory of one package trades a
	// stdlib call for a module dependency and a slower test. Revisit if it is
	// removed rather than merely discouraged.
	//
	//nolint:staticcheck // SA1019: see above
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse package source: %v", err)
	}

	var names []string
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				spec, ok := n.(*ast.TypeSpec)
				if !ok || !strings.HasSuffix(spec.Name.Name, "Body") {
					return true
				}
				if _, isStruct := spec.Type.(*ast.StructType); isStruct {
					names = append(names, spec.Name.Name)
				}
				return true
			})
		}
	}
	if len(names) == 0 {
		t.Fatal("found no *Body types; the discovery above has stopped working")
	}
	return names
}

// instantiate builds a zero value of a body type by name, using a registry the
// compiler checks — `reflect` cannot look a type up from a string on its own.
func instantiate(name string) (any, bool) {
	v, ok := bodyRegistry[name]
	return v, ok
}

func TestEveryDeclaredBodyTypeIsCoveredByThisTest(t *testing.T) {
	/*
	 * The registry below is what lets the rest of this file instantiate a type
	 * from its name. It is hand-written, so it can fall behind — and this test is
	 * what stops that being silent. A new *Body type fails here first, with the
	 * name it needs to be added under.
	 */
	var missing []string
	for _, name := range declaredBodyTypes(t) {
		if _, ok := instantiate(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("these body types are not in bodyRegistry, so nothing checks that "+
			"they can be marshalled: %v", missing)
	}
}

func TestEveryBodyTypeHasAProtobufMapping(t *testing.T) {
	// A missing case in the type switch is invisible at compile time and silent at
	// runtime: the body encodes to nothing and the peer decodes an empty message.
	for _, name := range declaredBodyTypes(t) {
		body, ok := instantiate(name)
		if !ok {
			continue // reported by TestEveryDeclaredBodyTypeIsCoveredByThisTest
		}
		if toProto(body) == nil {
			t.Errorf("%s has no protobuf mapping — it would marshal to zero bytes "+
				"and the peer would see an empty body", name)
		}
	}
}

func TestMarshalAcceptsAPointerToABody(t *testing.T) {
	// toProto switches on values, so `wire.Marshal(&body)` used to compile, run
	// and return zero bytes. A pointer now encodes exactly what the value does.
	body := HelloBody{ClientVersion: "web/1", DeviceID: "d1"}
	byValue, byPointer := Marshal(body), Marshal(&body)
	if len(byValue) == 0 || !bytes.Equal(byValue, byPointer) {
		t.Fatalf("pointer encoded %x, value encoded %x", byPointer, byValue)
	}
	var nilBody *HelloBody
	if got := Marshal(nilBody); len(got) != 0 {
		t.Fatalf("a nil pointer produced %d bytes", len(got))
	}
}

func TestUnmarshalAcceptsAPointerToEveryBody(t *testing.T) {
	// The decode direction is where a pointer is mandatory, and an unmapped type
	// there means an inbound frame is silently dropped rather than an outbound one
	// being empty. Same class of failure, opposite direction.
	for _, name := range declaredBodyTypes(t) {
		body, ok := instantiate(name)
		if !ok {
			continue
		}
		target := reflect.New(reflect.TypeOf(body))

		if err := Unmarshal(nil, target.Interface()); err != nil {
			t.Errorf("*%s cannot be decoded into: %v", name, err)
		}
	}
}

func TestAnUnmappedTypeMarshalsToNothing(t *testing.T) {
	/*
	 * The trap itself, pinned as documentation rather than as an aspiration.
	 * Marshal swallows the codec's error by design, so the only signal an unmapped
	 * type gives is an empty slice — which is what a caller is least likely to
	 * check.
	 *
	 * If this ever fails because Marshal learned to report the problem, that is an
	 * improvement: delete this test and the comment above it.
	 */
	for _, v := range []any{
		map[string]any{"user_id": "u1"},
		"a bare string",
		42,
		nil,
	} {
		if got := Marshal(v); len(got) != 0 {
			t.Errorf("unmapped %T produced %d bytes; the trap has changed shape", v, len(got))
		}
	}
}

func TestTheCodecReportsWhatMarshalHides(t *testing.T) {
	// The error exists; only the convenience wrapper drops it. Anything that needs
	// to know can ask the codec directly — which is what the fix in internal/fanout
	// does by using encoding/json for its JSON-shaped payload.
	_, err := protoCodec{}.Marshal(map[string]any{"user_id": "u1"})

	if err == nil {
		t.Fatal("the codec accepted an unmapped type")
	}
	if !strings.Contains(err.Error(), "no protobuf mapping") {
		t.Errorf("error = %q, want it to name the missing mapping", err)
	}
}

func TestAPopulatedBodyNeverMarshalsToNothing(t *testing.T) {
	/*
	 * The positive half of the same invariant: a mapped type with real content has
	 * to produce bytes. An empty result would mean the mapping exists but drops
	 * everything, which reads identically to "not mapped" at the call site.
	 *
	 * A body whose every field sits at its proto3 default legitimately encodes to
	 * nothing, so each case here sets something.
	 */
	populated := []any{
		HelloBody{ClientVersion: "web/1"},
		AuthBody{Username: "alice"},
		AuthOKBody{UserID: "u1"},
		SendBody{ChatID: "c1", Text: "hello"},
		NewMessageBody{MessageID: "m1", ChatID: "c1"},
		ReadUpdateBody{ChatID: "c1", UpToChatSeq: 5},
		TypingBody{ChatID: "c1", UserID: "u1", Active: true},
		PresenceBody{UserID: "u1", Online: true},
		ReactUpdateBody{MessageID: "m1", Emoji: "👍"},
		CallStateBody{CallID: "call-1", State: "ringing"},
		PollStateBody{PollID: "p1", Question: "Lunch?"},
		ProfileBody{UserID: "u1", Username: "alice"},
		ErrorBody{Code: 3001, Message: "not found"},
	}

	for _, body := range populated {
		name := reflect.TypeOf(body).Name()
		if got := Marshal(body); len(got) == 0 {
			t.Errorf("%s with real content marshalled to zero bytes", name)
		}
	}
}
