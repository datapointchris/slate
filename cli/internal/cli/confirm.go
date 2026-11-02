package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// confirm asks prompt and reports whether the answer approved. Destructive verbs
// call it unless --yes bypasses it.
//
// A prompt is only ever offered on an interactive stdin. Prompting a
// non-interactive caller blocks on a stdin that never closes, leaving it with no
// output and no exit code — the one failure a caller cannot recover from, and
// the reason the gate lives in here rather than at the fourteen call sites.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	if !interactive(cmd) {
		return false, fmt.Errorf("refusing to prompt without an interactive terminal; pass --yes to confirm")
	}
	return readConfirmation(cmd.ErrOrStderr(), cmd.InOrStdin(), prompt)
}

// interactive reports whether the command may prompt: --no-input never may, and
// otherwise stdin has to be a terminal. A reader a test substituted is not an
// *os.File, so it reads as non-interactive — which is what makes the gate
// testable without a pty.
func interactive(cmd *cobra.Command) bool {
	if noInput {
		return false
	}
	file, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

// readConfirmation prints prompt to out and reads a yes/no answer from in. Only
// "y"/"yes" (case-insensitive) approves; EOF or anything else declines. Split
// from confirm so the parsing stays testable on plain buffers.
func readConfirmation(out io.Writer, in io.Reader, prompt string) (bool, error) {
	_, _ = fmt.Fprintf(out, "%s [y/N] ", prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
