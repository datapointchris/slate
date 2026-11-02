package transcript

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// fixtureSession is the session whose transcripts testdata/project holds.
const fixtureSession = "0f6e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b"

// The fixture repeats msg_a across three lines and msg_s1 across two, holds a
// message with no id and a last line cut short, and names flat1 as a subagent.
// flat2 sits beside it unnamed, so it belongs to another session.
func TestSessionMessagesCountsEachMessageOnceAcrossTheSessionAndItsSubagents(t *testing.T) {
	got, err := SessionMessages(filepath.Join("testdata", "project", fixtureSession+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	at := func(clock string) time.Time {
		return time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC).Add(mustDuration(t, clock))
	}
	want := []Message{
		{Key: "msg_a", WrittenAt: at("7s"), Model: "claude-opus-5-5", Tokens: Tokens{3, 20000, 0, 50}},
		{Key: "msg_f1", WrittenAt: at("1m30s"), Model: "claude-sonnet-5-5", Subagent: "flat1", Tokens: Tokens{2, 1000, 500, 40}},
		{Key: fixtureSession + ".jsonl:6", WrittenAt: at("2m"), Model: "<synthetic>"},
		{Key: "msg_s1", WrittenAt: at("2m31s"), Model: "claude-haiku-5-5", Subagent: "nested1", Tokens: Tokens{5, 3000, 0, 260}},
		{Key: "msg_b", WrittenAt: at("3m"), Model: "claude-opus-5-5", Tokens: Tokens{1, 500, 20000, 70}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("messages:\n got  %+v\n want %+v", got, want)
	}
}

func mustDuration(t *testing.T, s string) time.Duration {
	t.Helper()
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
