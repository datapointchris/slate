package cli

import (
	"slices"
	"testing"
)

func TestTheSessionCOpensCarriesItsRecordAndTheFlowSlateInheritedOnItsResource(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "host.name=desk")
	t.Setenv("BAGGAGE", "flow=4bf92f35,note=a%20b")
	want := []string{
		"OTEL_RESOURCE_ATTRIBUTES=host.name=desk,slate.record.number=12,flow=4bf92f35,note=a%20b",
		"BAGGAGE=flow=4bf92f35,note=a%20b",
	}
	if got := sessionTelemetry(12); !slices.Equal(got, want) {
		t.Errorf("the session's environment was %q, want %q", got, want)
	}
}
