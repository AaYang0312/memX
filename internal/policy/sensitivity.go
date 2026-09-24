package policy

import "errors"

// SensitivityClass is the field-level classification of spec §17.2 and
// data-classification §2: low, medium or high. It is orthogonal to Marker:
// a class never implies, adds or clears a marker, and a marker never implies
// a class. The zero value is deliberately invalid so an unset class fails
// closed. No namespace is mapped to any class here; that assignment is the
// unapproved G5 decision and stays empty.
type SensitivityClass uint8

const (
	SensitivityUnspecified SensitivityClass = iota // 0: invalid, rejected
	SensitivityLow                                 // 1: eligible destinations only after every downstream gate
	SensitivityMedium                              // 2: no third-party embedding; no vector path while G-M is unapproved
	SensitivityHigh                                // 3: protected canonical storage only; no ES, no Qdrant, no outbound
)

// Marker records possible content properties (data-classification §2),
// independent of the sensitivity class: PII, secret/credential, and
// one-time/ephemeral value. Markers only ever restrict downstream handling;
// they never unlock a destination or relax a prohibition. MarkerNone means no
// marker is recorded; documented bits combine freely.
type Marker uint8

const (
	MarkerNone    Marker = 0
	MarkerPII     Marker = 1 << 0 // may carry personally identifiable data
	MarkerSecret  Marker = 1 << 1 // may carry secrets or credentials
	MarkerOneTime Marker = 1 << 2 // one-time or per-request ephemeral value

	markerAll = MarkerPII | MarkerSecret | MarkerOneTime
)

// Destination enumerates the outbound or indexed channels a classification
// question can be about. It enumerates only questions, never answers: no
// destination is releasable by classification while G5/G6/G-M are unresolved.
type Destination uint8

const (
	DestinationUnspecified         Destination = iota // 0: invalid, rejected
	DestinationThirdPartyEmbedding                    // 1: third-party embedding provider (gated by unapproved G6)
	DestinationVectorIndex                            // 2: Qdrant semantic path (default disabled, unapproved G-M)
	DestinationLexicalIndex                           // 3: Elasticsearch lexical path
)

var (
	ErrInvalidSensitivity = errors.New("invalid sensitivity class")
	ErrInvalidMarker      = errors.New("invalid marker")
	ErrInvalidDestination = errors.New("invalid destination")
)

// Valid reports whether the class is one of the three documented classes.
func (c SensitivityClass) Valid() bool { return c >= SensitivityLow && c <= SensitivityHigh }

// Valid reports whether only documented marker bits are set.
func (m Marker) Valid() bool { return m&^markerAll == 0 }

// Valid reports whether the destination is one of the documented channels.
func (d Destination) Valid() bool {
	return d >= DestinationThirdPartyEmbedding && d <= DestinationLexicalIndex
}

// Prohibits reports the categorical, classification-derived rejections of
// spec §17.2 and data-classification §2: high bars every listed destination;
// medium bars third-party embedding and the vector path (G-M default
// disabled); low alone bars none of them. This is the rejection direction
// only. A false result is NOT permission: deterministic redaction, PII/secret
// policy, authorization metadata, canonical checks and the unresolved
// G5/G6/G-M gates still apply, and PermitsByClassification keeps every
// classification-only release closed.
func Prohibits(c SensitivityClass, d Destination) (bool, error) {
	if !c.Valid() {
		return false, ErrInvalidSensitivity
	}
	if !d.Valid() {
		return false, ErrInvalidDestination
	}
	switch d {
	case DestinationThirdPartyEmbedding, DestinationVectorIndex:
		return c >= SensitivityMedium, nil
	default: // DestinationLexicalIndex; the value is already validated
		return c == SensitivityHigh, nil
	}
}

// PermitsByClassification answers whether classification alone could release
// content to an outbound or indexed destination. While G5 (namespace to
// sensitivity mapping and confirmation whitelist), G6 (embedding provider and
// outbound) and G-M (medium-sensitivity semantic path) are unresolved, the
// answer is false for every valid combination; any invalid input rejects
// before the question is answered. Flipping any answer here is an owner gate
// decision, never a code change, and would still be NECESSARY, never
// SUFFICIENT: redaction, PII/secret policy, authorization metadata, canonical
// revalidation and per-request authorization remain mandatory (spec §10.1
// step 8, §17.2). Any future gate-approved release must also enforce the
// marker-level prohibitions in data-classification §2: secrets never enter
// supplemental indexes and one-time values never become lasting facts.
func PermitsByClassification(c SensitivityClass, m Marker, d Destination) (bool, error) {
	if !c.Valid() {
		return false, ErrInvalidSensitivity
	}
	if !m.Valid() {
		return false, ErrInvalidMarker
	}
	if !d.Valid() {
		return false, ErrInvalidDestination
	}
	return false, nil
}
