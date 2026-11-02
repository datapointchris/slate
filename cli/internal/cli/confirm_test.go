package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newConfirmTarget is a command with the streams a test controls. Its stdin is a
// buffer rather than a terminal, which is exactly the caller confirm must refuse
// to prompt.
func newConfirmTarget(stdin string) (*cobra.Command, *bytes.Buffer) {
	var errOut bytes.Buffer
	cmd := &cobra.Command{Use: "target"}
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetErr(&errOut)
	cmd.SetOut(&bytes.Buffer{})
	return cmd, &errOut
}

func TestConfirm_RefusesToPromptWithoutATerminal(t *testing.T) {
	noInput = false
	cmd, errOut := newConfirmTarget("y\n")

	ok, err := confirm(cmd, "Delete everything?")

	if err == nil {
		t.Fatal("confirm returned no error — a non-TTY caller would block on a stdin that never closes")
	}
	if ok {
		t.Error("confirm approved without a terminal")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error = %q, want it to name the flag that would have answered", err)
	}
	if errOut.Len() != 0 {
		t.Errorf("prompt was written anyway: %q", errOut.String())
	}
}

func TestInteractive_NoInputRefusesEvenOnATerminal(t *testing.T) {
	cmd, _ := newConfirmTarget("")

	noInput = true
	defer func() { noInput = false }()

	if interactive(cmd) {
		t.Error("--no-input must force the non-interactive path")
	}
}

func TestNoInputFlag_IsPersistentAndDefaultsOff(t *testing.T) {
	noInput = true // a previous command's value must not survive

	root := NewRootCommand()

	flag := root.PersistentFlags().Lookup("no-input")
	if flag == nil {
		t.Fatal("--no-input is not registered on the root command")
	}
	if noInput {
		t.Error("noInput survived NewRootCommand — a test would inherit the previous value")
	}
	if flag.DefValue != "false" {
		t.Errorf("--no-input default = %q, want false: interactivity is the terminal default", flag.DefValue)
	}
}

func TestReadConfirmation(t *testing.T) {
	cases := []struct {
		answer string
		want   bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{" YES \n", true},
		{"n\n", false},
		{"\n", false},
		{"", false}, // EOF declines rather than erroring
		{"yep\n", false},
	}
	for _, c := range cases {
		t.Run(strings.TrimSpace(c.answer), func(t *testing.T) {
			var out bytes.Buffer
			got, err := readConfirmation(&out, strings.NewReader(c.answer), "Delete?")
			if err != nil {
				t.Fatalf("readConfirmation(%q) errored: %v", c.answer, err)
			}
			if got != c.want {
				t.Errorf("readConfirmation(%q) = %v, want %v", c.answer, got, c.want)
			}
			if !strings.Contains(out.String(), "Delete? [y/N]") {
				t.Errorf("prompt = %q, want the question and the default-no hint", out.String())
			}
		})
	}
}
