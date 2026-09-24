package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func allCodes() []Code {
	return []Code{
		CodeInvalidRequest,
		CodeUnauthorized,
		CodeForbidden,
		CodeNotFound,
		CodeConflict,
		CodeInternal,
	}
}

func mustTrace(t *testing.T) TraceRef {
	t.Helper()
	trace, err := NewTraceRef()
	if err != nil {
		t.Fatalf("NewTraceRef: %v", err)
	}
	return trace
}

func TestClosedSetCodesHaveDistinctFixedMessages(t *testing.T) {
	seenMessages := map[string]bool{}
	seenCodes := map[Code]bool{}
	for _, code := range allCodes() {
		if !code.Valid() {
			t.Fatalf("code %q must be valid", code)
		}
		if code.Message() == "" {
			t.Fatalf("code %q must have a fixed message", code)
		}
		if seenMessages[code.Message()] {
			t.Fatalf("message %q is not unique to its code", code.Message())
		}
		if seenCodes[code] {
			t.Fatalf("code %q declared twice", code)
		}
		seenMessages[code.Message()] = true
		seenCodes[code] = true
	}
	// Outside the closed set: no validity, no message, no serialization path.
	for _, bad := range []Code{"", "internal ", " Internal", "invalid_request\n", "INTERNAL", "boom"} {
		if bad.Valid() {
			t.Fatalf("code %q must be invalid", bad)
		}
		if bad.Message() != "" {
			t.Fatalf("invalid code %q must have no message", bad)
		}
	}
}

func TestStandaloneCodeSerializationFailsClosed(t *testing.T) {
	for _, valid := range allCodes() {
		data, err := json.Marshal(valid)
		if err != nil {
			t.Fatalf("marshal valid code %q: %v", valid, err)
		}
		var decoded Code
		if err := json.Unmarshal(data, &decoded); err != nil || decoded != valid {
			t.Fatalf("round trip %q: got %q, err %v", valid, decoded, err)
		}
	}
	for _, bad := range []Code{"", "boom", "INTERNAL"} {
		if _, err := json.Marshal(struct{ Code Code }{bad}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("unknown code %q serialized: %v", bad, err)
		}
	}
	for _, data := range []string{`"boom"`, `""`, `null`, `5`, `{}`} {
		decoded := CodeInternal
		if err := json.Unmarshal([]byte(data), &decoded); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("invalid standalone code %s: %v", data, err)
		}
		if decoded != CodeInternal {
			t.Fatalf("invalid code %s changed receiver", data)
		}
	}
}

func TestTraceRefShapeRoundTripAndRejections(t *testing.T) {
	trace := mustTrace(t)
	text := trace.String()
	if !strings.HasPrefix(text, traceRefPrefix) {
		t.Fatalf("trace ref %q lacks prefix", text)
	}
	if len(text) != len(traceRefPrefix)+traceRefEncodedLen {
		t.Fatalf("trace ref %q has wrong length %d", text, len(text))
	}
	for _, r := range text {
		if r < 0x20 || r > 0x7e {
			t.Fatalf("trace ref %q is not printable ASCII", text)
		}
	}
	parsed, err := ParseTraceRef(text)
	if err != nil || parsed != trace {
		t.Fatalf("round trip failed: %v", err)
	}
	if trace.IsZero() {
		t.Fatal("generated trace ref reported zero")
	}
	if !(TraceRef{}).IsZero() {
		t.Fatal("zero value not reported zero")
	}
	// Raw identity values and infrastructure strings cannot fit the shape.
	for _, bad := range []string{
		"",
		"trc-",
		"user@example.com",
		"+12025550123",
		"postgres://memx:fake@127.0.0.1:5432/memx_test?sslmode=disable",
		"trc-AAAAAAAAAAAAAAAAAAAAAA",          // 16 zero bytes
		"trc-AAAAAAAAAAAAAAAAAAAAAA=",         // padded (illegal for raw encoding)
		"trc-AAAAAAAAAAAAAAAAAAAAA=",          // padded at contract length
		"trc-AAAAAAAAAAAAAAAAAAAAAR",          // non-canonical trailing bits
		"trc-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", // wrong length
		"trc_AAAAAAAAAAAAAAAAAAAAAA",          // wrong prefix
		"TRC-aaaaaaaaaaaaaaaaaaaaaa",          // wrong prefix case
		"trc-a+a+a+a+a+a+a+a+a+a+a+",          // standard-alphabet characters
		"trc-äöüäöüäöüäöüäöüäöü",              // non-ASCII
	} {
		if _, err := ParseTraceRef(bad); !errors.Is(err, ErrInvalidTraceRef) {
			t.Fatalf("ParseTraceRef(%q): want ErrInvalidTraceRef, got %v", bad, err)
		}
	}
	if _, err := json.Marshal(TraceRef{}); !errors.Is(err, ErrInvalidTraceRef) {
		t.Fatalf("zero TraceRef must fail closed, got %v", err)
	}
	if _, err := json.Marshal(TraceRef{value: "user@example.com"}); !errors.Is(err, ErrInvalidTraceRef) {
		t.Fatalf("hand-built TraceRef must fail closed, got %v", err)
	}
	var decoded TraceRef
	if err := decoded.UnmarshalJSON([]byte(`"` + text + `"`)); err != nil || decoded != trace {
		t.Fatalf("UnmarshalJSON round trip failed: %v", err)
	}
	for _, bad := range []string{"null", "42", `{"v":1}`, `"user@example.com"`, `"trc-AAAAAAAAAAAAAAAAAAAAAA"`} {
		var target TraceRef
		if err := target.UnmarshalJSON([]byte(bad)); !errors.Is(err, ErrInvalidTraceRef) {
			t.Fatalf("UnmarshalJSON(%s): want ErrInvalidTraceRef, got %v", bad, err)
		}
	}
}

func TestNewErrorValidatesBothFields(t *testing.T) {
	trace := mustTrace(t)
	for _, bad := range []Code{"", "boom"} {
		if _, err := NewError(bad, trace); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("NewError(%q,...): want ErrInvalidCode, got %v", bad, err)
		}
	}
	if _, err := NewError(CodeInternal, TraceRef{}); !errors.Is(err, ErrInvalidTraceRef) {
		t.Fatalf("zero trace must be rejected, got %v", err)
	}
	// Direct private-field construction cannot bypass validation either.
	if _, err := NewError(CodeInternal, TraceRef{value: "postgres://d:s@h:1/db"}); !errors.Is(err, ErrInvalidTraceRef) {
		t.Fatalf("hand-built trace must be rejected, got %v", err)
	}
	if env, err := NewError(CodeInternal, trace); err != nil {
		t.Fatalf("NewError: %v", err)
	} else if env.Code() != CodeInternal || env.Message() != CodeInternal.Message() || env.TraceRef() != trace {
		t.Fatal("accessors do not reflect the constructed envelope")
	}
}

func TestEnvelopeRoundTripAndDeterministicOutput(t *testing.T) {
	for _, code := range allCodes() {
		trace := mustTrace(t)
		env, err := NewError(code, trace)
		if err != nil {
			t.Fatalf("NewError(%s): %v", code, err)
		}
		first, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal %s: %v", code, err)
		}
		second, err := json.Marshal(env)
		if err != nil || string(first) != string(second) {
			t.Fatalf("marshaling %s is not deterministic", code)
		}
		var parsed Envelope
		if err := json.Unmarshal(first, &parsed); err != nil {
			t.Fatalf("UnmarshalJSON %s: %v", code, err)
		}
		if parsed.Code() != code || parsed.Message() != code.Message() || parsed.TraceRef() != trace {
			t.Fatalf("round trip mismatch for %s", code)
		}
		reparsed, err := ParseEnvelope(first)
		if err != nil {
			t.Fatalf("ParseEnvelope %s: %v", code, err)
		}
		again, err := json.Marshal(reparsed)
		if err != nil || string(again) != string(first) {
			t.Fatalf("re-marshaled bytes differ for %s", code)
		}
		// The message must equal the fixed binding and carry nothing else.
		var wire wireEnvelope
		if err := json.Unmarshal(first, &wire); err != nil {
			t.Fatalf("wire decode %s: %v", code, err)
		}
		if wire.Message != code.Message() {
			t.Fatalf("serialized message for %s is not the fixed one", code)
		}
	}
}

func TestEnvelopeSerializesExactlyThreeFields(t *testing.T) {
	env, err := NewError(CodeConflict, mustTrace(t))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if tok, err := dec.Token(); err != nil {
		t.Fatalf("top-level token: %v", err)
	} else if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		t.Fatal("envelope is not a JSON object")
	}
	wantKeys := map[string]bool{"code": true, "message": true, "trace_ref": true}
	gotKeys := map[string]int{}
	var gotOrder []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		key, ok := keyTok.(string)
		if !ok {
			t.Fatalf("non-string key %v", keyTok)
		}
		gotKeys[key]++
		gotOrder = append(gotOrder, key)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dec.Token(); err != nil {
		t.Fatalf("closing token: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatal("serialized envelope has trailing data")
	}
	if len(gotKeys) != 3 {
		t.Fatalf("serialized envelope has %d distinct fields, want exactly 3: %v", len(gotKeys), gotKeys)
	}
	if strings.Join(gotOrder, ",") != "code,message,trace_ref" {
		t.Fatalf("serialized envelope key order changed: %v", gotOrder)
	}
	for key, n := range gotKeys {
		if !wantKeys[key] {
			t.Fatalf("unexpected serialized field %q", key)
		}
		if n != 1 {
			t.Fatalf("field %q serialized %d times", key, n)
		}
	}
}

func TestParseEnvelopeRejectsTable(t *testing.T) {
	validTrace := mustTrace(t)
	// The baseline payload parses; every mutation below must be rejected.
	base := envelopeText(CodeInternal.Message(), validTrace.String())
	if _, err := ParseEnvelope([]byte(base)); err != nil {
		t.Fatalf("baseline payload must parse, got %v", err)
	}
	cases := []struct {
		name    string
		payload string
		want    error
	}{
		{"unknown field", `{"code":"internal","message":"internal error","trace_ref":"` + validTrace.String() + `","extra":1}`, ErrUnknownField},
		{"case-variant key", `{"Code":"internal","message":"internal error","trace_ref":"` + validTrace.String() + `"}`, ErrUnknownField},
		{"duplicate code", `{"code":"internal","code":"internal","message":"internal error","trace_ref":"` + validTrace.String() + `"}`, ErrDuplicateField},
		{"duplicate message", `{"code":"internal","message":"internal error","message":"internal error","trace_ref":"` + validTrace.String() + `"}`, ErrDuplicateField},
		{"duplicate trace_ref", `{"code":"internal","message":"internal error","trace_ref":"` + validTrace.String() + `","trace_ref":"` + validTrace.String() + `"}`, ErrDuplicateField},
		{"unknown code", `{"code":"boom","message":"internal error","trace_ref":"` + validTrace.String() + `"}`, ErrInvalidCode},
		{"empty code", `{"code":"","message":"","trace_ref":"` + validTrace.String() + `"}`, ErrInvalidCode},
		{"wrong code case", `{"code":"Internal","message":"internal error","trace_ref":"` + validTrace.String() + `"}`, ErrInvalidCode},
		{"numeric code", `{"code":5,"message":"internal error","trace_ref":"` + validTrace.String() + `"}`, ErrInvalidCode},
		{"null code", `{"code":null,"message":"internal error","trace_ref":"` + validTrace.String() + `"}`, ErrInvalidCode},
		{"missing code", `{"message":"internal error","trace_ref":"` + validTrace.String() + `"}`, ErrInvalidCode},
		{"tampered message", `{"code":"internal","message":"internal error: pg timeout on 127.0.0.1","trace_ref":"` + validTrace.String() + `"}`, ErrMessageMismatch},
		{"empty message", `{"code":"internal","message":"","trace_ref":"` + validTrace.String() + `"}`, ErrMessageMismatch},
		{"foreign message", `{"code":"internal","message":"resource not found","trace_ref":"` + validTrace.String() + `"}`, ErrMessageMismatch},
		{"numeric message", `{"code":"internal","message":7,"trace_ref":"` + validTrace.String() + `"}`, ErrMalformed},
		{"missing message", `{"code":"internal","trace_ref":"` + validTrace.String() + `"}`, ErrMessageMismatch},
		{"missing trace_ref", `{"code":"internal","message":"internal error"}`, ErrInvalidTraceRef},
		{"null trace_ref", `{"code":"internal","message":"internal error","trace_ref":null}`, ErrInvalidTraceRef},
		{"numeric trace_ref", `{"code":"internal","message":"internal error","trace_ref":42}`, ErrInvalidTraceRef},
		{"empty trace_ref", `{"code":"internal","message":"internal error","trace_ref":""}`, ErrInvalidTraceRef},
		{"email trace_ref", `{"code":"internal","message":"internal error","trace_ref":"user@example.com"}`, ErrInvalidTraceRef},
		{"phone trace_ref", `{"code":"internal","message":"internal error","trace_ref":"+12025550123"}`, ErrInvalidTraceRef},
		{"dsn trace_ref", `{"code":"internal","message":"internal error","trace_ref":"postgres://memx:fake@127.0.0.1:5432/memx_test"}`, ErrInvalidTraceRef},
		{"zero trace_ref", `{"code":"internal","message":"internal error","trace_ref":"trc-AAAAAAAAAAAAAAAAAAAAAA"}`, ErrInvalidTraceRef},
		{"short trace_ref", `{"code":"internal","message":"internal error","trace_ref":"trc-short"}`, ErrInvalidTraceRef},
		{"trailing object", base + ` {}`, ErrTrailingData},
		{"trailing array", base + ` []`, ErrTrailingData},
		{"trailing null", base + ` null`, ErrTrailingData},
		{"trailing garbage", base + ` junk`, ErrTrailingData},
		{"top-level array", `[]`, ErrMalformed},
		{"top-level string", `"internal"`, ErrMalformed},
		{"top-level number", `5`, ErrMalformed},
		{"top-level null", `null`, ErrMalformed},
		{"empty input", ``, ErrMalformed},
		{"truncated json", `{"code":"internal"`, ErrMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := ParseEnvelope([]byte(tc.payload))
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseEnvelope(%s): want %v, got %v", tc.payload, tc.want, err)
			}
			if err != nil && env != (Envelope{}) {
				t.Fatal("failed decode must not return a partial envelope")
			}
			// Sentinel text must stay static: no payload fragment may be echoed.
			if err != nil {
				for _, marker := range []string{"user@example.com", "+12025550123", "postgres://", "127.0.0.1", "pg timeout"} {
					if strings.Contains(err.Error(), marker) {
						t.Fatalf("error text echoes payload marker %q: %v", marker, err)
					}
				}
			}
			// The strict UnmarshalJSON path must reject every invalid payload.
			// encoding/json pre-validates the whole input before invoking
			// UnmarshalJSON, so structural failures (trailing data, empty or
			// truncated input) surface as its own syntax errors there; exact
			// sentinel parity is asserted for all other classes.
			var viaType Envelope
			viaErr := json.Unmarshal([]byte(tc.payload), &viaType)
			if viaErr == nil {
				t.Fatalf("UnmarshalJSON path accepted invalid payload %s", tc.payload)
			}
			if viaType != (Envelope{}) {
				t.Fatal("failed decode changed the receiver")
			}
			switch tc.want {
			case ErrTrailingData, ErrMalformed:
				// rejection itself is the guarantee on this path
			default:
				if !errors.Is(viaErr, tc.want) {
					t.Fatalf("UnmarshalJSON path: want %v, got %v", tc.want, viaErr)
				}
			}
		})
	}
}

// envelopeText builds a syntactically valid envelope around an arbitrary
// message and trace value; used as the base for trailing-data mutations.
func envelopeText(message, trace string) string {
	data, err := json.Marshal(wireEnvelope{Code: CodeInternal, Message: message, Trace: TraceRef{value: trace}})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestEnvelopeMarshalFailsClosed(t *testing.T) {
	if _, err := json.Marshal(Envelope{}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("zero envelope must fail closed, got %v", err)
	}
	trace := mustTrace(t)
	if _, err := json.Marshal(Envelope{code: "boom", trace: trace}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("invalid code envelope must fail closed, got %v", err)
	}
	if _, err := json.Marshal(Envelope{code: CodeInternal, trace: TraceRef{}}); !errors.Is(err, ErrInvalidTraceRef) {
		t.Fatalf("zero trace envelope must fail closed, got %v", err)
	}
	if _, err := json.Marshal(Envelope{code: CodeInternal, trace: TraceRef{value: "user@example.com"}}); !errors.Is(err, ErrInvalidTraceRef) {
		t.Fatalf("malformed trace envelope must fail closed, got %v", err)
	}
}

func TestSentinelsAreStaticAndDistinct(t *testing.T) {
	sentinels := []error{
		ErrInvalidCode,
		ErrInvalidTraceRef,
		ErrMessageMismatch,
		ErrTraceRefEntropy,
		ErrMalformed,
		ErrUnknownField,
		ErrDuplicateField,
		ErrTrailingData,
	}
	texts := map[string]bool{}
	for _, sentinel := range sentinels {
		// Payload-shaped characters (email, DSN, JSON). The ':' of the fixed
		// "httpapi: " prefix is deliberately not treated as a violation.
		if strings.ContainsAny(sentinel.Error(), "{}\"@/+") {
			t.Fatalf("sentinel %q embeds payload-shaped content", sentinel.Error())
		}
		if texts[sentinel.Error()] {
			t.Fatalf("sentinel text %q is duplicated", sentinel.Error())
		}
		texts[sentinel.Error()] = true
	}
}
