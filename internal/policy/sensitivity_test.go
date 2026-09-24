package policy

import (
	"errors"
	"testing"
)

func TestSensitivityClassFailClosed(t *testing.T) {
	for _, c := range []SensitivityClass{SensitivityLow, SensitivityMedium, SensitivityHigh} {
		if !c.Valid() {
			t.Fatalf("sensitivity class %d must be valid", c)
		}
	}
	for _, bad := range []SensitivityClass{SensitivityUnspecified, 4, 255} {
		if bad.Valid() {
			t.Fatalf("sensitivity class %d must be invalid", bad)
		}
		if _, err := Prohibits(bad, DestinationLexicalIndex); !errors.Is(err, ErrInvalidSensitivity) {
			t.Fatalf("Prohibits(%d,...): want ErrInvalidSensitivity, got %v", bad, err)
		}
		if _, err := PermitsByClassification(bad, MarkerNone, DestinationLexicalIndex); !errors.Is(err, ErrInvalidSensitivity) {
			t.Fatalf("PermitsByClassification(%d,...): want ErrInvalidSensitivity, got %v", bad, err)
		}
	}
}

func TestMarkerOrthogonalAndFailClosed(t *testing.T) {
	all := []Marker{
		MarkerNone, MarkerPII, MarkerSecret, MarkerOneTime,
		MarkerPII | MarkerSecret, MarkerPII | MarkerOneTime, MarkerSecret | MarkerOneTime,
		MarkerPII | MarkerSecret | MarkerOneTime,
	}
	for _, m := range all {
		if !m.Valid() {
			t.Fatalf("marker combination %d must be valid", m)
		}
	}
	for _, bad := range []Marker{1 << 3, 1 << 7, MarkerPII | 1<<5} {
		if bad.Valid() {
			t.Fatalf("marker %d must be invalid", bad)
		}
		if _, err := PermitsByClassification(SensitivityLow, bad, DestinationLexicalIndex); !errors.Is(err, ErrInvalidMarker) {
			t.Fatalf("PermitsByClassification(...,%d,...): want ErrInvalidMarker, got %v", bad, err)
		}
	}
	// Orthogonality: a marker is meaningful with every class and never
	// derives or changes the class dimension; marker validity does not depend
	// on the class it is checked against.
	for _, c := range []SensitivityClass{SensitivityLow, SensitivityMedium, SensitivityHigh} {
		for _, m := range all {
			if !m.Valid() {
				t.Fatalf("marker %d must stay valid against class %d", m, c)
			}
			if _, err := PermitsByClassification(c, m, DestinationLexicalIndex); err != nil {
				t.Fatalf("class %d with marker %d: %v", c, m, err)
			}
		}
	}
}

func TestDestinationFailClosed(t *testing.T) {
	for _, d := range []Destination{DestinationThirdPartyEmbedding, DestinationVectorIndex, DestinationLexicalIndex} {
		if !d.Valid() {
			t.Fatalf("destination %d must be valid", d)
		}
	}
	for _, bad := range []Destination{DestinationUnspecified, 4, 255} {
		if bad.Valid() {
			t.Fatalf("destination %d must be invalid", bad)
		}
		if _, err := Prohibits(SensitivityLow, bad); !errors.Is(err, ErrInvalidDestination) {
			t.Fatalf("Prohibits(...,%d): want ErrInvalidDestination, got %v", bad, err)
		}
		if _, err := PermitsByClassification(SensitivityLow, MarkerNone, bad); !errors.Is(err, ErrInvalidDestination) {
			t.Fatalf("PermitsByClassification(...,%d): want ErrInvalidDestination, got %v", bad, err)
		}
	}
}

func TestProhibitsMatchesDocumentedMatrix(t *testing.T) {
	// spec §17.2 and data-classification §2: high bars every listed
	// destination; medium bars third-party embedding and the vector path
	// (G-M default disabled); low alone bars none of them.
	for _, tt := range []struct {
		class SensitivityClass
		dest  Destination
		want  bool
	}{
		{SensitivityHigh, DestinationThirdPartyEmbedding, true},
		{SensitivityHigh, DestinationVectorIndex, true},
		{SensitivityHigh, DestinationLexicalIndex, true},
		{SensitivityMedium, DestinationThirdPartyEmbedding, true},
		{SensitivityMedium, DestinationVectorIndex, true},
		{SensitivityMedium, DestinationLexicalIndex, false},
		{SensitivityLow, DestinationThirdPartyEmbedding, false},
		{SensitivityLow, DestinationVectorIndex, false},
		{SensitivityLow, DestinationLexicalIndex, false},
	} {
		got, err := Prohibits(tt.class, tt.dest)
		if err != nil || got != tt.want {
			t.Fatalf("Prohibits(%d,%d): got %v, %v; want %v", tt.class, tt.dest, got, err, tt.want)
		}
	}
	// The matrix must stay monotone: a more sensitive class never loses a
	// prohibition a less sensitive class has.
	for d := DestinationThirdPartyEmbedding; d <= DestinationLexicalIndex; d++ {
		low, _ := Prohibits(SensitivityLow, d)
		medium, _ := Prohibits(SensitivityMedium, d)
		high, _ := Prohibits(SensitivityHigh, d)
		if low && !medium || medium && !high {
			t.Fatalf("prohibitions not monotone for %d: low=%v medium=%v high=%v", d, low, medium, high)
		}
	}
}

func TestClassificationAloneNeverReleases(t *testing.T) {
	// While G5/G6/G-M are unresolved, no valid combination of class, markers
	// and destination may be released by classification alone, and markers
	// never unlock a destination that another marker combination would not.
	markers := []Marker{
		MarkerNone, MarkerPII, MarkerSecret, MarkerOneTime,
		MarkerPII | MarkerSecret | MarkerOneTime,
	}
	for c := SensitivityLow; c <= SensitivityHigh; c++ {
		for d := DestinationThirdPartyEmbedding; d <= DestinationLexicalIndex; d++ {
			for _, m := range markers {
				permitted, err := PermitsByClassification(c, m, d)
				if err != nil {
					t.Fatalf("PermitsByClassification(%d,%d,%d): %v", c, m, d, err)
				}
				if permitted {
					t.Fatalf("classification alone released class %d with markers %d to destination %d while G5/G6/G-M are unresolved", c, m, d)
				}
			}
		}
	}
	// The most permissive-looking combination stays closed too.
	if permitted, err := PermitsByClassification(SensitivityLow, MarkerNone, DestinationLexicalIndex); err != nil || permitted {
		t.Fatalf("low/unmarked/lexical must stay closed: %v, %v", permitted, err)
	}
	// Any invalid input rejects even alongside other invalid inputs.
	if _, err := PermitsByClassification(SensitivityUnspecified, Marker(255), Destination(9)); err == nil {
		t.Fatal("any invalid input must reject the release question")
	}
}
