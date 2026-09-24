// Package httpapi freezes the stable public error-response contract of the
// offline personal development track (plan §8.2–§8.5). The package contains
// no HTTP server, routes, middleware, auth, database, broker, or model
// client: it defines only the serialized shape of an error payload and its
// strict validation. Per ADR 0005 this is a personal-phase freeze only; it
// is not a production error contract, claims no organizational G9 or legal
// sign-off, and because G4 has no real issuer no business API is opened.
//
// Contract invariants (spec §17.2, §14.5):
//   - a serialized error is exactly the three fields code, message and
//     trace_ref, in that order, with no extra fields;
//   - codes form a minimal closed set and the message is a fixed string
//     derived only from the code: no underlying exception, request input, or
//     identity value ever reaches a message;
//   - trace_ref is an opaque ASCII correlation shape that cannot carry an
//     email address, phone number, or DSN;
//   - decoding is strict: unknown fields, duplicate fields, invalid codes,
//     tampered messages, missing or malformed trace refs, and trailing JSON
//     are rejected;
//   - construction and serialization fail closed: a value that cannot be
//     validated is never serialized, so callers cannot bypass validation by
//     building values directly.
//
// Every failure is reported as a fixed sentinel whose text is static;
// underlying decoder errors are discarded, never wrapped or echoed.
package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Code is a stable error code from the minimal closed set of plan §8.3.
// Codes cover exactly the failure classes the public API can report; a
// code outside the set has no message and cannot be serialized. The zero
// value "" is deliberately invalid so an unset code fails closed.
type Code string

const (
	CodeInvalidRequest Code = "invalid_request" // malformed or invalid request payload
	CodeUnauthorized   Code = "unauthorized"    // missing or invalid identity (G4: no real issuer yet)
	CodeForbidden      Code = "forbidden"       // valid identity without the required permission
	CodeNotFound       Code = "not_found"       // unknown resource or opaque ref
	CodeConflict       Code = "conflict"        // state, revision or proposal conflict (spec §14.3: 409)
	CodeInternal       Code = "internal"        // internal failure; discloses nothing
)

// fixedMessages binds each closed-set code to its fixed public message.
// The message is a pure function of the code: no exception text, request
// input, identity value, or DSN ever flows into it (spec §17.2). The map is
// closed; a code absent from it is invalid and serializes to nothing.
var fixedMessages = map[Code]string{
	CodeInvalidRequest: "request failed validation",
	CodeUnauthorized:   "authentication required",
	CodeForbidden:      "access denied",
	CodeNotFound:       "resource not found",
	CodeConflict:       "request conflicts with current state",
	CodeInternal:       "internal error",
}

// Valid reports whether c is in the stable closed set.
func (c Code) Valid() bool { _, ok := fixedMessages[c]; return ok }

// MarshalJSON refuses to serialize unknown codes even when Code is used
// outside Envelope. A caller cannot bypass the closed set with a struct field.
func (c Code) MarshalJSON() ([]byte, error) {
	if !c.Valid() {
		return nil, ErrInvalidCode
	}
	return json.Marshal(string(c))
}

// UnmarshalJSON validates a standalone code before changing the receiver.
func (c *Code) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return ErrInvalidCode
	}
	candidate := Code(text)
	if !candidate.Valid() {
		return ErrInvalidCode
	}
	*c = candidate
	return nil
}

// Message returns the fixed message bound to a valid code and "" for any
// other value. The result never depends on runtime input.
func (c Code) Message() string { return fixedMessages[c] }

// Fixed sentinel failures. Their text is static by construction: no input
// value, code, message, trace ref, or underlying decoder error is ever
// embedded, so a sentinel can never leak payload content.
var (
	ErrInvalidCode     = errors.New("httpapi: error code outside the stable closed set")
	ErrInvalidTraceRef = errors.New("httpapi: trace reference missing or malformed")
	ErrMessageMismatch = errors.New("httpapi: message does not match the fixed message of the code")
	ErrTraceRefEntropy = errors.New("httpapi: trace reference generation failed")
	ErrMalformed       = errors.New("httpapi: envelope is not a single valid JSON object")
	ErrUnknownField    = errors.New("httpapi: envelope contains an unknown field")
	ErrDuplicateField  = errors.New("httpapi: envelope contains a duplicate field")
	ErrTrailingData    = errors.New("httpapi: envelope is followed by trailing JSON")
)

// The personal-phase trace_ref shape: "trc-" followed by the canonical
// unpadded base64url encoding of 16 random bytes (22 characters, 26 total).
// The shape matches the opaque "trc-..." form of the design spec envelope
// examples and is verifiable, so raw emails, phone numbers, and DSNs cannot
// fit: the alphabet is ASCII base64url only and the length is fixed. The
// shape is local to this package and this phase; internal/domain/ref keeps
// its own separate transport freeze.
const (
	traceRefPrefix = "trc-"
	traceRefBytes  = 16
	// Constant form of base64.RawURLEncoding.EncodedLen(traceRefBytes):
	// ceil(16*8/6) = 22 encoded characters.
	traceRefEncodedLen = (traceRefBytes*8 + 5) / 6
)

// TraceRef is an opaque ASCII trace correlation reference for error
// responses. The zero value is unset and fails closed; the only valid
// values come from NewTraceRef or ParseTraceRef, and MarshalJSON
// revalidates so a hand-built value cannot bypass the shape check.
type TraceRef struct{ value string }

// NewTraceRef generates a fresh random trace reference. On generation
// failure only the fixed entropy sentinel is returned; no partial value.
func NewTraceRef() (TraceRef, error) {
	var raw [traceRefBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return TraceRef{}, ErrTraceRefEntropy
	}
	trace, err := ParseTraceRef(traceRefPrefix + base64.RawURLEncoding.EncodeToString(raw[:]))
	if err != nil { // also rejects the all-zero (non-reference) RNG outcome
		return TraceRef{}, ErrTraceRefEntropy
	}
	return trace, nil
}

// ParseTraceRef validates the personal-phase shape and returns the
// reference. Every rejection is the fixed ErrInvalidTraceRef sentinel; the
// rejected text is never echoed.
func ParseTraceRef(text string) (TraceRef, error) {
	if len(text) != len(traceRefPrefix)+traceRefEncodedLen || !strings.HasPrefix(text, traceRefPrefix) {
		return TraceRef{}, ErrInvalidTraceRef
	}
	encoded := text[len(traceRefPrefix):]
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) != traceRefBytes {
		return TraceRef{}, ErrInvalidTraceRef
	}
	// Canonical re-encode rejects non-canonical inputs (padded forms,
	// standard-alphabet "+/" characters, non-zero trailing bits).
	if base64.RawURLEncoding.EncodeToString(raw) != encoded {
		return TraceRef{}, ErrInvalidTraceRef
	}
	for _, b := range raw {
		if b != 0 {
			return TraceRef{value: text}, nil
		}
	}
	return TraceRef{}, ErrInvalidTraceRef // all-zero payload is not a reference
}

// String returns the opaque reference text; the zero value returns "".
func (t TraceRef) String() string { return t.value }

// IsZero reports whether the reference is the unset zero value.
func (t TraceRef) IsZero() bool { return t.value == "" }

// MarshalJSON emits the reference as a JSON string only after revalidating
// the shape. Zero or malformed values fail closed with a fixed sentinel;
// they are never serialized.
func (t TraceRef) MarshalJSON() ([]byte, error) {
	if _, err := ParseTraceRef(t.value); err != nil {
		return nil, err
	}
	return json.Marshal(t.value)
}

// UnmarshalJSON accepts only a JSON string that satisfies ParseTraceRef.
// Numbers, objects, null, and malformed shapes are rejected with the fixed
// sentinel, so any parent struct decoding a TraceRef is protected too.
func (t *TraceRef) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return ErrInvalidTraceRef
	}
	parsed, err := ParseTraceRef(text)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// Envelope is the stable error response of plan §8.5: exactly the fields
// code, message and trace_ref. Fields are private so an invalid envelope
// cannot be constructed outside the package; the zero value fails closed.
type Envelope struct {
	code  Code
	trace TraceRef
}

// NewError builds the error envelope for a closed-set code and a validated
// trace reference. The message is not stored: it is derived from the code
// at serialization time, so no call site can attach free-form text.
func NewError(code Code, trace TraceRef) (Envelope, error) {
	if !code.Valid() {
		return Envelope{}, ErrInvalidCode
	}
	if _, err := ParseTraceRef(trace.value); err != nil {
		return Envelope{}, ErrInvalidTraceRef
	}
	return Envelope{code: code, trace: trace}, nil
}

// Code returns the envelope's stable error code.
func (e Envelope) Code() Code { return e.code }

// Message returns the fixed message bound to the code. It never contains
// exception text, request input, or identity values (spec §17.2).
func (e Envelope) Message() string { return e.code.Message() }

// TraceRef returns the envelope's opaque trace reference.
func (e Envelope) TraceRef() TraceRef { return e.trace }

// wireEnvelope is the serialization mirror of Envelope. Marshal always goes
// through the three fields below, so the output object has exactly these
// keys in exactly this order.
type wireEnvelope struct {
	Code    Code     `json:"code"`
	Message string   `json:"message"`
	Trace   TraceRef `json:"trace_ref"`
}

// MarshalJSON serializes exactly {"code","message","trace_ref"}. It
// revalidates both fields and fails closed with a fixed sentinel instead of
// ever emitting a payload outside the contract (plan §8.5).
func (e Envelope) MarshalJSON() ([]byte, error) {
	if !e.code.Valid() {
		return nil, ErrInvalidCode
	}
	if _, err := ParseTraceRef(e.trace.value); err != nil {
		return nil, ErrInvalidTraceRef
	}
	return json.Marshal(wireEnvelope{
		Code:    e.code,
		Message: e.code.Message(),
		Trace:   e.trace,
	})
}

// UnmarshalJSON decodes with the same strictness as ParseEnvelope and
// leaves the receiver untouched on any failure.
func (e *Envelope) UnmarshalJSON(data []byte) error {
	parsed, err := parseEnvelope(data)
	if err != nil {
		return err
	}
	*e = parsed
	return nil
}

// ParseEnvelope strictly decodes one error envelope from data. It rejects,
// with fixed sentinels: anything that is not a single JSON object, unknown
// or duplicate fields, key spellings other than the exact contract names,
// codes outside the closed set, messages that do not match the fixed
// message of the code, and missing or malformed trace refs, including any
// trailing JSON after the object.
func ParseEnvelope(data []byte) (Envelope, error) { return parseEnvelope(data) }

func parseEnvelope(data []byte) (Envelope, error) {
	if err := scanEnvelopeObject(data); err != nil {
		return Envelope{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var wire wireEnvelope
	if err := dec.Decode(&wire); err != nil {
		// Code and TraceRef use fixed sentinels. Every other decode
		// failure is a JSON type or syntax mismatch; discard its details.
		if errors.Is(err, ErrInvalidCode) {
			return Envelope{}, ErrInvalidCode
		}
		if errors.Is(err, ErrInvalidTraceRef) {
			return Envelope{}, ErrInvalidTraceRef
		}
		return Envelope{}, ErrMalformed
	}
	if !wire.Code.Valid() {
		return Envelope{}, ErrInvalidCode
	}
	if wire.Message != wire.Code.Message() {
		return Envelope{}, ErrMessageMismatch
	}
	if _, err := ParseTraceRef(wire.Trace.value); err != nil {
		return Envelope{}, ErrInvalidTraceRef
	}
	return Envelope{code: wire.Code, trace: wire.Trace}, nil
}

// scanEnvelopeObject verifies, at the token level, that data holds exactly
// one top-level JSON object whose keys are the exact contract field names
// with no duplicates. encoding/json struct matching would silently accept
// case-insensitive spellings such as "CODE", so the exact comparison here
// is the strict gate; DisallowUnknownFields stays as defense in depth.
func scanEnvelopeObject(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return ErrMalformed
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return ErrMalformed
	}
	var seenCode, seenMessage, seenTrace bool
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return ErrMalformed
		}
		key, ok := keyTok.(string)
		if !ok {
			return ErrMalformed
		}
		switch key {
		case "code":
			if seenCode {
				return ErrDuplicateField
			}
			seenCode = true
		case "message":
			if seenMessage {
				return ErrDuplicateField
			}
			seenMessage = true
		case "trace_ref":
			if seenTrace {
				return ErrDuplicateField
			}
			seenTrace = true
		default:
			return ErrUnknownField
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return ErrMalformed
		}
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return ErrMalformed
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ErrTrailingData
	}
	return nil
}
