package cli

import (
	"os"
	"sync"

	"github.com/pkg/browser"
)

// browserStreams guards browser's package-wide streams across two opens at once.
var browserStreams sync.Mutex

// openQuietly opens link with the browser's output sent to /dev/null, so the output never draws
// over what the program shows: a board, or a login's code and its URL. The streams are a file
// rather than io.Discard. os/exec hands a file to the launcher as a descriptor, and gives any
// other writer a pipe. Wait then holds that pipe until every process writing to it exits, the
// launched browser among them. A login opening the browser before it polls would then not poll.
func openQuietly(link string) error {
	browserStreams.Lock()
	defer browserStreams.Unlock()
	quiet, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = quiet.Close() }()
	browser.Stdout, browser.Stderr = quiet, quiet
	return browser.OpenURL(link)
}
