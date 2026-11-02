package prompt

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestSeedReader_HandsTheSeedOverBeforeTheStream(t *testing.T) {
	reader := &seedReader{src: strings.NewReader("typed")}
	reader.seed("Chorre")

	all, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(all) != "Chorretyped" {
		t.Errorf("read %q, want the seed first — that is what puts a rejected answer back on the line", all)
	}
}

func TestSeedReader_WithoutASeedIsJustTheStream(t *testing.T) {
	reader := &seedReader{src: strings.NewReader("typed")}

	all, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(all) != "typed" {
		t.Errorf("read %q, want the stream unchanged", all)
	}
}

// tab presses Tab on a line and reports what the cycler put there.
func tab(t *testing.T, c *cycler, line string) string {
	t.Helper()
	completed, pos, ok := c.complete(line, len(line), '\t')
	if !ok {
		t.Fatalf("Tab on %q completed nothing", line)
	}
	if pos != len(completed) {
		t.Errorf("cursor at %d, want the end of %q", pos, completed)
	}
	return completed
}

func TestCycler_TabWalksTheMatchesAndWraps(t *testing.T) {
	c := &cycler{choices: []string{"Chore", "Computer", "Dingo"}}

	if got := tab(t, c, "c"); got != "Chore" {
		t.Errorf("first Tab = %q, want Chore", got)
	}
	if got := tab(t, c, "Chore"); got != "Computer" {
		t.Errorf("second Tab = %q, want the next match", got)
	}
	if got := tab(t, c, "Computer"); got != "Chore" {
		t.Errorf("third Tab = %q, want the walk to wrap", got)
	}
}

func TestCycler_TabOnAnEmptyLineBrowsesEverything(t *testing.T) {
	c := &cycler{choices: []string{"Chore", "Computer"}}

	if got := tab(t, c, ""); got != "Chore" {
		t.Errorf("Tab on an empty line = %q, want the first choice", got)
	}
}

func TestCycler_TypingStartsANewCycle(t *testing.T) {
	c := &cycler{choices: []string{"Chore", "Computer", "Dingo"}}
	tab(t, c, "c")

	if _, _, ok := c.complete("d", 1, 'd'); ok {
		t.Fatal("a printable key was completed — only Tab completes")
	}
	if got := tab(t, c, "d"); got != "Dingo" {
		t.Errorf("Tab after typing = %q, want the cycle restarted from what is on the line", got)
	}
}

func TestCycler_NoMatchLeavesTheLineAlone(t *testing.T) {
	c := &cycler{choices: []string{"Chore"}}

	if _, _, ok := c.complete("zz", 2, '\t'); ok {
		t.Error("a prefix matching nothing was completed anyway")
	}
}

func TestCycler_WithoutChoicesNeverCompletes(t *testing.T) {
	c := &cycler{}

	if _, _, ok := c.complete("", 0, '\t'); ok {
		t.Error("a field with no choices completed on Tab")
	}
}

func TestCycler_KeepsWhatFollowsTheCursor(t *testing.T) {
	c := &cycler{choices: []string{"Chore"}}

	completed, pos, ok := c.complete("cX", 1, '\t')
	if !ok {
		t.Fatal("Tab completed nothing")
	}
	if completed != "ChoreX" {
		t.Errorf("line = %q, want the text after the cursor kept", completed)
	}
	if pos != len("Chore") {
		t.Errorf("cursor at %d, want it after the completion", pos)
	}
}

// fakeSession is a [Session] that counts the calls its Write took.
//
// The count is the point. TerminalSession.Write erases the prompt line, writes,
// and repaints the prompt, so every call is a repaint and a listing that
// arrives in pieces is a listing sewn through with fragments of the prompt. A
// bytes.Buffer cannot see that, which is how the fault got shipped: a plain
// append behaves identically at one Write and at 123.
type fakeSession struct {
	bytes.Buffer
	writes int
	width  int
}

func (s *fakeSession) Write(p []byte) (int, error) {
	s.writes++
	return s.Buffer.Write(p)
}

func (s *fakeSession) ReadLine(Question) (string, error) { return "", io.EOF }
func (s *fakeSession) Completes() bool                   { return true }

func (s *fakeSession) Width() int {
	if s.width > 0 {
		return s.width
	}
	return 80
}

// listing drives a cycler that prints, and returns the session it printed to.
func listing(choices []string) (*cycler, *fakeSession) {
	session := &fakeSession{}
	return &cycler{choices: choices, session: session}, session
}

func TestCycler_TabOnAnEmptyLinePrintsEveryChoice(t *testing.T) {
	c, out := listing([]string{"Chore", "Computer", "Dingo"})

	if _, _, ok := c.complete("", 0, '\t'); ok {
		t.Error("the line was changed — a listing is a look, not a choice")
	}
	for _, choice := range c.choices {
		if !strings.Contains(out.String(), choice) {
			t.Errorf("choice %q was not printed:\n%s", choice, out)
		}
	}
}

func TestCycler_ATabAfterTheListingWalksTheMatches(t *testing.T) {
	c, out := listing([]string{"Chore", "Computer", "Dingo"})
	c.complete("c", 1, '\t')
	printed := out.String()

	if got := tab(t, c, "c"); got != "Chore" {
		t.Errorf("Tab after the listing = %q, want the walk started", got)
	}
	if got := tab(t, c, "Chore"); got != "Computer" {
		t.Errorf("the next Tab = %q, want the next match", got)
	}
	if out.String() != printed {
		t.Errorf("the list was printed again instead of walked:\n%s", out)
	}
}

func TestCycler_ListsOnlyWhatMatchesWhatIsTyped(t *testing.T) {
	c, out := listing([]string{"Chore", "Computer", "Dingo"})

	c.complete("c", 1, '\t')

	// Both halves, because the absence alone is satisfied by a listing that
	// never happened — which is exactly what this test used to allow.
	for _, want := range []string{"Chore", "Computer"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("match %q was not listed:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Dingo") {
		t.Errorf("a choice that does not match was listed:\n%s", out.String())
	}
}

func TestCycler_TheListingArrivesInOneWrite(t *testing.T) {
	choices := make([]string, 54)
	for i := range choices {
		choices[i] = fmt.Sprintf("project number %02d in the list", i)
	}
	c, out := listing(choices)

	c.complete("", 0, '\t')

	if out.writes != 1 {
		t.Errorf("the listing took %d writes, want 1 — every one repaints the prompt on a real terminal", out.writes)
	}
}

func TestCycler_TheListingIsLaidOutToTheWidthAtTheTimeItPrints(t *testing.T) {
	c, out := listing([]string{"alpha", "bravo", "charlie", "delta"})
	// The pane narrows while the prompt is up, after the read began.
	out.width = 12

	c.complete("", 0, '\t')

	if got := strings.Count(strings.TrimRight(out.String(), "\n"), "\n") + 1; got != 4 {
		t.Errorf("%d rows, want 4 — the listing used a width the pane no longer has:\n%s", got, out.String())
	}
}

func TestCycler_ASingleMatchIsCompletedRatherThanListed(t *testing.T) {
	c, out := listing([]string{"Chore", "Dingo"})

	if got := tab(t, c, "d"); got != "Dingo" {
		t.Errorf("Tab = %q, want the only match completed", got)
	}
	if out.Len() > 0 {
		t.Errorf("one match was printed as a list, which is a keypress that buys nothing:\n%s", out)
	}
}

func TestCycler_TypingAfterAListingListsTheNarrowedMatches(t *testing.T) {
	c, out := listing([]string{"Chore", "Computer", "Dingo"})
	c.complete("", 0, '\t')
	out.Reset()

	c.complete("c", 1, 'c')
	c.complete("c", 1, '\t')

	if !strings.Contains(out.String(), "Computer") {
		t.Errorf("the narrowed matches were not listed:\n%s", out)
	}
	if strings.Contains(out.String(), "Dingo") {
		t.Errorf("the listing was not narrowed by what was typed:\n%s", out)
	}
}

func TestCycler_WithoutASessionTabWalksFromTheFirstPress(t *testing.T) {
	c := &cycler{choices: []string{"Chore", "Computer"}}

	if got := tab(t, c, ""); got != "Chore" {
		t.Errorf("Tab = %q, want the walk — a field that listed its choices up front does not list again", got)
	}
}

func TestTooManyToList_IsWhereTheFieldStopsPrintingItsChoicesUnasked(t *testing.T) {
	short := make([]string, maxListedChoices)
	for i := range short {
		short[i] = fmt.Sprintf("choice-%02d", i)
	}

	if tooManyToList(short) {
		t.Error("a list already printed above the prompt would be printed again under it")
	}
	if !tooManyToList(append(short, "one more")) {
		t.Error("a list the field never showed has no other way to be seen")
	}
}

func TestWriteColumns_ANarrowWidthPutsOneChoicePerLine(t *testing.T) {
	var out bytes.Buffer
	choices := []string{"Convert theme and font from bash to Go", "release facts", "ifiles"}

	writeColumns(&out, 40, choices)

	if got := strings.Count(strings.TrimRight(out.String(), "\n"), "\n") + 1; got != len(choices) {
		t.Errorf("%d lines for %d choices, want one each — a phrase wraps in a column:\n%s", got, len(choices), out.String())
	}
}

func TestWriteColumns_ShortChoicesShareALine(t *testing.T) {
	var out bytes.Buffer

	writeColumns(&out, 80, []string{"build", "chore", "life"})

	if got := strings.Count(out.String(), "\n"); got != 1 {
		t.Errorf("%d lines, want one — three short words waste a screen stacked:\n%s", got, out.String())
	}
}

func TestCycler_FindsAPhraseByAWordInsideIt(t *testing.T) {
	c := &cycler{choices: []string{"release facts", "Convert theme and font from bash to Go"}}

	if got := tab(t, c, "theme"); got != "Convert theme and font from bash to Go" {
		t.Errorf("Tab on %q = %q, want the phrase found by a word inside it", "theme", got)
	}
}

func TestCycler_APrefixMatchComesBeforeAMerelyContainedOne(t *testing.T) {
	c := &cycler{choices: []string{"Chore", "Home"}}

	if got := tab(t, c, "ho"); got != "Home" {
		t.Errorf("first Tab = %q, want Home — a prefix outranks a substring", got)
	}
	if got := tab(t, c, "Home"); got != "Chore" {
		t.Errorf("second Tab = %q, want the substring match after it", got)
	}
}
