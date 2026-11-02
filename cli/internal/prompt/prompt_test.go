package prompt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
)

var categories = []string{"Automotive", "Chore", "Computer", "Dingo"}

// recordingSession keeps the choices each ask was handed, which is what Tab
// would have had to work with at that point in the form.
type recordingSession struct {
	*PlainSession
	offered [][]string
}

func (s *recordingSession) ReadLine(q Question) (string, error) {
	s.offered = append(s.offered, q.Choices)
	return s.PlainSession.ReadLine(q)
}

// run drives a form with typed lines and returns the answers plus everything the
// form said.
func run(t *testing.T, form Form, typed string) (Answers, string) {
	t.Helper()
	var out bytes.Buffer
	answers, err := form.Run(NewPlainSession(strings.NewReader(typed), &out))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return answers, out.String()
}

func taskForm() Form {
	return Form{
		Fields: []Field{
			{Key: "name", Label: "Name"},
			{Key: "category", Label: "Category", Choices: categories, Validate: OneOf(categories)},
			{Key: "window-days", Label: "Window in days", Default: "30", Validate: Int},
			{Key: "notes", Label: "Notes", Optional: true},
		},
	}
}

func TestForm_AsksEveryFieldInOrder(t *testing.T) {
	answers, out := run(t, taskForm(), "Renew registration\nChore\n3\nby friday\n")

	want := map[string]string{"name": "Renew registration", "category": "Chore", "window-days": "3", "notes": "by friday"}
	for key, value := range want {
		if answers.Get(key) != value {
			t.Errorf("answers[%q] = %q, want %q", key, answers.Get(key), value)
		}
	}
	if got := strings.Index(out, "Name:"); got > strings.Index(out, "Category:") {
		t.Error("Category was asked before Name — fields are asked in the order they are declared")
	}
}

func TestForm_RejectedAnswerComesBackAndNothingElseIsLost(t *testing.T) {
	answers, out := run(t, taskForm(), "Renew registration\nChorre\nChore\n3\n\n")

	if answers.Get("name") != "Renew registration" {
		t.Errorf("name = %q, want it kept — a bad category must not cost the fields already typed", answers.Get("name"))
	}
	if answers.Get("category") != "Chore" {
		t.Errorf("category = %q, want the corrected value", answers.Get("category"))
	}
	if !strings.Contains(out, `unknown value "Chorre"`) {
		t.Errorf("output did not name the rejected value:\n%s", out)
	}
	if !strings.Contains(out, `(was "Chorre")`) {
		t.Errorf("the rejected answer was not offered back for correction:\n%s", out)
	}
	if strings.Contains(out, "Name:(was") {
		t.Error("Name was re-asked — only the rejected field is asked again")
	}
}

func TestForm_EmptyAnswerTakesTheDefault(t *testing.T) {
	answers, out := run(t, taskForm(), "Renew\nChore\n\n\n")

	if answers.Get("window-days") != "30" {
		t.Errorf("window-days = %q, want the default 30", answers.Get("window-days"))
	}
	if !strings.Contains(out, "Window in days [30]:") {
		t.Errorf("the prompt did not show the default:\n%s", out)
	}
}

func TestForm_SkippedOptionalFieldIsAbsent(t *testing.T) {
	answers, out := run(t, taskForm(), "Renew\nChore\n2\n\n")

	if answers.Has("notes") {
		t.Errorf("notes = %q, want it absent — skipped is not the same as set to empty", answers.Get("notes"))
	}
	if !strings.Contains(out, "Notes (optional):") {
		t.Errorf("the prompt did not mark the field skippable:\n%s", out)
	}
}

func TestForm_RequiredFieldIsAskedAgainWhenEmpty(t *testing.T) {
	answers, out := run(t, taskForm(), "\nRenew\nChore\n1\n\n")

	if answers.Get("name") != "Renew" {
		t.Errorf("name = %q, want the answer given after the empty one", answers.Get("name"))
	}
	if !strings.Contains(out, "Name is required.") {
		t.Errorf("output did not say the field is required:\n%s", out)
	}
}

func TestForm_ListsTheChoicesBeforeAsking(t *testing.T) {
	_, out := run(t, taskForm(), "Renew\nChore\n1\n\n")

	if !strings.Contains(out, "Category is one of:") {
		t.Errorf("the choices were not introduced:\n%s", out)
	}
	for _, category := range categories {
		if !strings.Contains(out, category) {
			t.Errorf("choice %q was not listed:\n%s", category, out)
		}
	}
	if strings.Contains(out, "Tab") {
		t.Error("a session that cannot complete must not advertise Tab")
	}
}

func TestForm_CanceledInputReturnsEOF(t *testing.T) {
	var out bytes.Buffer
	answers, err := taskForm().Run(NewPlainSession(strings.NewReader("Renew\n"), &out))

	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if answers != nil {
		t.Errorf("answers = %v, want none — a half-filled record is not a record", answers)
	}
}

func TestForm_IntRejectsAWordAndKeepsIt(t *testing.T) {
	answers, out := run(t, taskForm(), "Renew\nChore\nhigh\n2\n\n")

	if answers.Get("window-days") != "2" {
		t.Errorf("window-days = %q, want the corrected value", answers.Get("window-days"))
	}
	if !strings.Contains(out, `"high" is not a whole number`) {
		t.Errorf("output did not explain the rejection:\n%s", out)
	}
}

func TestAnswers_MergeKeepsWhatIsAlreadyThere(t *testing.T) {
	answers := Answers{"name": {"from the flag"}}
	answers.Merge(Answers{"name": {"from the prompt"}, "category": {"Chore"}})

	if answers.Get("name") != "from the flag" {
		t.Errorf("name = %q, want the flag to outrank the prompt", answers.Get("name"))
	}
	if answers.Get("category") != "Chore" {
		t.Errorf("category = %q, want the prompt to fill what the flags left out", answers.Get("category"))
	}
}

func TestOneOf_MatchesCaseInsensitivelyAndReturnsTheListSpelling(t *testing.T) {
	value, err := OneOf(categories)("chore")
	if err != nil {
		t.Fatalf("OneOf rejected a known value: %v", err)
	}
	if value != "Chore" {
		t.Errorf("value = %q, want the spelling the list carries", value)
	}
}

func TestOneOf_RejectionNamesEveryAcceptedValue(t *testing.T) {
	_, err := OneOf(categories)("nope")
	if err == nil {
		t.Fatal("OneOf accepted a value that is not in the list")
	}
	for _, category := range categories {
		if !strings.Contains(err.Error(), category) {
			t.Errorf("error = %q, want it to name %q", err, category)
		}
	}
}

// An initiative name is the case that breaks a bare comma join: several carry
// commas of their own, so the separator has to be told apart from the content.
var commaCarryingTitles = []string{
	"Release shape — survey, cross-examine, steer",
	"release facts — the measurement layer",
	"Standardize identifier naming in every release",
}

func TestOneOf_RejectionDelimitsACandidateThatCarriesCommas(t *testing.T) {
	_, err := OneOf(commaCarryingTitles)("release")
	if err == nil {
		t.Fatal("OneOf accepted a value that is not in the list")
	}
	for _, title := range commaCarryingTitles {
		if !strings.Contains(err.Error(), `"`+title+`"`) {
			t.Errorf("error = %q, want %q delimited whole", err, title)
		}
	}
	if strings.Contains(err.Error(), "steer, release facts") {
		t.Errorf("error = %q, want a candidate's own comma not to read as the separator", err)
	}
}

func TestOneOf_ShortListDelimitsEveryChoice(t *testing.T) {
	_, err := OneOf(categories)("nope")
	if err == nil {
		t.Fatal("OneOf accepted a value that is not in the list")
	}
	for _, category := range categories {
		if !strings.Contains(err.Error(), `"`+category+`"`) {
			t.Errorf("error = %q, want %q delimited whole", err, category)
		}
	}
}

func TestInt_TrimsToTheParsedNumber(t *testing.T) {
	value, err := Int("007")
	if err != nil {
		t.Fatalf("Int rejected a number: %v", err)
	}
	if value != "7" {
		t.Errorf("value = %q, want the parsed form", value)
	}
}

func TestIntAtLeast_RefusesBelowTheFloorAndKeepsTheFloor(t *testing.T) {
	atLeastOne := IntAtLeast(1)
	if _, err := atLeastOne("0"); err == nil || !strings.Contains(err.Error(), "minimum of 1") {
		t.Errorf("err = %v, want the floor named", err)
	}
	if value, err := atLeastOne("1"); err != nil || value != "1" {
		t.Errorf("value = %q err = %v, want the floor itself accepted", value, err)
	}
	if _, err := atLeastOne("soon"); err == nil {
		t.Error("a word was accepted")
	}
}

func TestForm_RepeatKeepsAskingUntilAnEmptyAnswer(t *testing.T) {
	form := Form{Fields: []Field{{Key: "project", Label: "Project", Repeat: true}}}

	answers, out := run(t, form, "one\ntwo\nthree\n\n")

	got := strings.Join(answers.All("project"), ",")
	if got != "one,two,three" {
		t.Errorf("project = %q, want every answer in order", got)
	}
	if !strings.Contains(out, "Project (another, or Enter to move on):") {
		t.Errorf("the prompt did not say what Enter does after the first answer:\n%s", out)
	}
}

func TestForm_RepeatStillNeedsItsFirstAnswer(t *testing.T) {
	form := Form{Fields: []Field{{Key: "project", Label: "Project", Repeat: true}}}

	answers, out := run(t, form, "\none\n\n")

	if strings.Join(answers.All("project"), ",") != "one" {
		t.Errorf("project = %v, want the answer given after the empty one", answers.All("project"))
	}
	if !strings.Contains(out, "Project is required.") {
		t.Errorf("an empty first answer was accepted:\n%s", out)
	}
}

func TestForm_RepeatValidatesEveryAnswerAndKeepsTheEarlierOnes(t *testing.T) {
	form := Form{Fields: []Field{
		{Key: "project", Label: "Project", Repeat: true, Validate: OneOf(categories)},
	}}

	answers, out := run(t, form, "Chore\nDingoo\nDingo\n\n")

	got := strings.Join(answers.All("project"), ",")
	if got != "Chore,Dingo" {
		t.Errorf("project = %q, want the first answer kept and the second corrected", got)
	}
	if !strings.Contains(out, `(was "Dingoo")`) {
		t.Errorf("the rejected answer was not offered back:\n%s", out)
	}
}

func TestForm_MultilineReadsUntilABlankLine(t *testing.T) {
	form := Form{Fields: []Field{{Key: "notes", Label: "Notes", Multiline: true, Optional: true}}}

	answers, out := run(t, form, "first line\nsecond line\n\n")

	if answers.Get("notes") != "first line\nsecond line" {
		t.Errorf("notes = %q, want the lines joined with newlines", answers.Get("notes"))
	}
	if !strings.Contains(out, "blank line ends it") {
		t.Errorf("the prompt did not say how to finish:\n%s", out)
	}
}

func TestForm_MultilineSkippedWhenOptional(t *testing.T) {
	form := Form{Fields: []Field{{Key: "notes", Label: "Notes", Multiline: true, Optional: true}}}

	answers, _ := run(t, form, "\n")

	if answers.Has("notes") {
		t.Errorf("notes = %q, want it absent", answers.Get("notes"))
	}
}

func TestForm_ALongChoiceListIsCountedRatherThanPrinted(t *testing.T) {
	many := make([]string, maxListedChoices+1)
	for i := range many {
		many[i] = fmt.Sprintf("choice-%02d", i)
	}
	form := Form{Fields: []Field{{Key: "pick", Label: "Pick", Choices: many}}}

	_, out := run(t, form, "choice-00\n")

	if strings.Contains(out, "choice-07") {
		t.Errorf("a list past the readable limit was printed in full:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d to choose from", len(many))) {
		t.Errorf("the count stood in for nothing:\n%s", out)
	}
}

// escapeForm is one field whose choices do not cover everything, with a way to
// add what is missing.
func escapeForm(make func(Session) (string, error)) Form {
	return Form{Fields: []Field{{
		Key:      "project",
		Label:    "Project",
		Choices:  []string{"Chore"},
		Repeat:   true,
		Validate: OneOf([]string{"Chore"}),
		Escape:   &Escape{Trigger: "+", Hint: "+ makes a new one.", Run: make},
	}}}
}

func TestForm_TheEscapeAnswersTheFieldWithWhatItMade(t *testing.T) {
	runs := 0
	answers, out := run(t, escapeForm(func(Session) (string, error) {
		runs++
		return "Brand new", nil
	}), "+\n\n")

	if got := strings.Join(answers.All("project"), ","); got != "Brand new" {
		t.Errorf("project = %q, want what the escape made — it made the value, so nothing validates it", got)
	}
	if runs != 1 {
		t.Errorf("the escape ran %d times, want once", runs)
	}
	if !strings.Contains(out, "+ makes a new one.") {
		t.Errorf("the hint was not printed — a trigger nothing mentions is a trigger nobody presses:\n%s", out)
	}
}

func TestForm_WhatTheEscapeMadeJoinsTheChoices(t *testing.T) {
	session := &recordingSession{PlainSession: NewPlainSession(strings.NewReader("+\n\n"), io.Discard)}

	if _, err := escapeForm(func(Session) (string, error) { return "Brand new", nil }).Run(session); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(session.offered) < 2 {
		t.Fatalf("the field was asked %d times, want the repeat after the escape", len(session.offered))
	}
	if !slices.Contains(session.offered[1], "Brand new") {
		t.Errorf("the next ask offered %v, want the new project among them — Tab has to reach it", session.offered[1])
	}
	if slices.Contains(session.offered[0], "Brand new") {
		t.Error("the first ask offered a project that did not exist yet")
	}
}

func TestForm_AValueTheEscapeMadeIsNotPutToTheValidator(t *testing.T) {
	answers, out := run(t, escapeForm(func(Session) (string, error) {
		return "Brand new", nil
	}), "+\nbrand NEW\n\n")

	got := strings.Join(answers.All("project"), ",")
	if got != "Brand new,Brand new" {
		t.Errorf("project = %q, want the made value accepted again and spelled the way it was made", got)
	}
	if strings.Contains(out, "unknown value") {
		t.Errorf("the field rejected a choice it was offering:\n%s", out)
	}
}

func TestForm_RunningOneFormTwiceDoesNotCarryTheFirstRunsEscapeOver(t *testing.T) {
	form := escapeForm(func(Session) (string, error) { return "Brand new", nil })

	if _, err := form.Run(NewPlainSession(strings.NewReader("+\n\n"), io.Discard)); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	session := &recordingSession{PlainSession: NewPlainSession(strings.NewReader("brand NEW\nChore\n\n"), io.Discard)}
	answers, err := form.Run(session)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	// askValue takes a *Field so runEscape can grow Choices and made. What keeps
	// that mutation inside one run is Form.Run's range copy and ask's value
	// parameter — a convention the type-checker no longer enforces.
	if slices.Contains(session.offered[0], "Brand new") {
		t.Errorf("the second run offered %v, want the first run's escape forgotten", session.offered[0])
	}
	if strings.Join(answers.All("project"), ",") != "Chore" {
		t.Errorf("project = %v, want the first run's made value refused by the validator", answers.All("project"))
	}
}

func TestForm_AnEscapeThatMakesNothingAsksTheFieldAgain(t *testing.T) {
	answers, _ := run(t, escapeForm(func(Session) (string, error) { return "", nil }), "+\nChore\n\n")

	if got := strings.Join(answers.All("project"), ","); got != "Chore" {
		t.Errorf("project = %q, want the answer given after backing out", got)
	}
}

func TestForm_AFailedEscapeIsReportedAndTheFieldAskedAgain(t *testing.T) {
	answers, out := run(t, escapeForm(func(Session) (string, error) {
		return "", errors.New("the API refused it")
	}), "+\nChore\n\n")

	if got := strings.Join(answers.All("project"), ","); got != "Chore" {
		t.Errorf("project = %q, want the field still answerable — a choice on the list is still a good answer", got)
	}
	if !strings.Contains(out, "the API refused it") {
		t.Errorf("the failure was not reported:\n%s", out)
	}
}

func TestForm_ACanceledEscapeAbandonsTheRecord(t *testing.T) {
	var out bytes.Buffer
	form := escapeForm(func(Session) (string, error) { return "", io.EOF })

	answers, err := form.Run(NewPlainSession(strings.NewReader("+\n"), &out))

	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF — a cancel travels however deep it happened", err)
	}
	if answers != nil {
		t.Errorf("answers = %v, want none", answers)
	}
}

// completingSession claims Tab works, for the lines a form prints only where
// pressing it does something.
type completingSession struct{ *PlainSession }

func (completingSession) Completes() bool { return true }

func TestForm_ALongChoiceListSaysTabWillPrintIt(t *testing.T) {
	many := make([]string, maxListedChoices+1)
	for i := range many {
		many[i] = fmt.Sprintf("choice-%02d", i)
	}
	var out bytes.Buffer
	form := Form{Fields: []Field{{Key: "pick", Label: "Pick", Choices: many}}}

	if _, err := form.Run(completingSession{NewPlainSession(strings.NewReader("choice-00\n"), &out)}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(out.String(), "Tab lists them") {
		t.Errorf("the only way to see the list was not named:\n%s", out.String())
	}
}

func TestOneOf_ALongListSuggestsTheNearMissesInstead(t *testing.T) {
	many := make([]string, maxListedChoices+1)
	for i := range many {
		many[i] = fmt.Sprintf("choice-%02d", i)
	}

	_, err := OneOf(many)("CHOICE-03x")
	if err == nil {
		t.Fatal("OneOf accepted a value not in the list")
	}
	if strings.Contains(err.Error(), "choice-01") {
		t.Errorf("error = %q, want it not to print the whole list", err)
	}

	_, err = OneOf(many)("ice-03")
	if err == nil {
		t.Fatal("OneOf accepted a substring that is not a whole value")
	}
	if !strings.Contains(err.Error(), "choice-03") {
		t.Errorf("error = %q, want the near miss named", err)
	}
}
