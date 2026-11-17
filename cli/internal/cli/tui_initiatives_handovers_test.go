package cli

import (
	"slices"
	"testing"
)

func TestTheSessionCOpensCarriesItsRecordAndTheFlowSlateInheritedOnItsResourceOnce(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "host.name=desk,flow=0aa,slate.record.number=3")
	t.Setenv("BAGGAGE", "flow=4bf92f35,note=a%20b/c")
	want := []string{
		"OTEL_RESOURCE_ATTRIBUTES=host.name=desk,slate.record.number=12,flow=4bf92f35,note=a%20b/c",
		"BAGGAGE=flow=4bf92f35,note=a%20b/c",
	}
	if got := sessionTelemetry(12); !slices.Equal(got, want) {
		t.Errorf("the session's environment was %q, want %q", got, want)
	}
}
