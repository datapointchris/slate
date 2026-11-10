package cli

import (
	"strings"
	"time"

	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermstyle"

	"github.com/datapointchris/slate/cli/internal/api"
)

// stepDayLayout is the month and day a checked step's mark gives, which is
// enough beside its neighbors since a record's steps fall within months.
const stepDayLayout = "01-02"

// stepMark is one step of a record's life as a row draws it. Day is the local
// day its latest worklog ended, empty while it is unchecked.
type stepMark struct {
	step string
	day  string
}

func (m stepMark) text() string {
	if m.day == "" {
		return "· " + m.step
	}
	return "✓" + m.step + " " + m.day
}

// stepMarks is every step in the order a record goes through them, each
// checked one with the day its latest worklog ended.
func stepMarks(steps []api.CheckedStep) []stepMark {
	ended := make(map[string]time.Time, len(steps))
	for _, step := range steps {
		ended[step.Step] = step.EndedTS
	}
	marks := make([]stepMark, 0, len(api.WorklogSteps))
	for _, name := range api.WorklogSteps {
		mark := stepMark{step: name}
		if at, ok := ended[name]; ok {
			mark.day = at.Local().Format(stepDayLayout)
		}
		marks = append(marks, mark)
	}
	return marks
}

// paintedStepMarks is the marks on one line, a checked step green and an
// unchecked one dim.
func paintedStepMarks(p gotermstyle.Palette, steps []api.CheckedStep) string {
	marks := stepMarks(steps)
	cells := make([]string, 0, len(marks))
	for _, mark := range marks {
		code := dimCode
		if mark.day != "" {
			code = gotermstyle.CodeGreen
		}
		cells = append(cells, p.Paint(code, mark.text()))
	}
	return strings.Join(cells, "  ")
}

// stepMarksLine is the marks as a board line, colored as paintedStepMarks colors them.
func stepMarksLine(steps []api.CheckedStep) gotermboard.Line {
	var line gotermboard.Line
	for at, mark := range stepMarks(steps) {
		if at > 0 {
			line = append(line, gotermboard.C("  ", ""))
		}
		code := dimCode
		if mark.day != "" {
			code = gotermboard.Green
		}
		line = append(line, gotermboard.C(mark.text(), code))
	}
	return line
}
