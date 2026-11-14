package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/pflag"

	"github.com/datapointchris/slate/cli/internal/api"
)

// The help --not-planned carries on a create or a promotion, and on an edit.
var (
	notPlannedSetHelp = "Step it will not run, so slate next never offers it, one of: " +
		strings.Join(api.InitiativeOptionalSteps, ", ") + " (repeatable; default every step planned)"
	notPlannedReplaceHelp = "Step it will not run, so slate next never offers it, one of: " +
		strings.Join(api.InitiativeOptionalSteps, ", ") + " (repeatable; replaces the set, empty plans every step)"
)

// notPlannedEdited reads --not-planned: nil where it was not passed, an empty
// set where it was passed empty, and the steps otherwise. build is refused,
// because an initiative's issues are its build.
func notPlannedEdited(f *pflag.FlagSet, steps []string) (*[]string, error) {
	switch readFlagIntent(f, "not-planned") {
	case flagClears:
		return &[]string{}, nil
	case flagMixesEmptyWithValues:
		return nil, goclikit.UsageError(errors.New("--not-planned given both a step and an empty one — an empty value plans every step, so pass one or the other"))
	case flagCarriesValues:
		for _, step := range steps {
			if step == "build" {
				return nil, goclikit.UsageError(errors.New("--not-planned build: build is always planned, because an initiative's issues are its build"))
			}
			if !slices.Contains(api.InitiativeOptionalSteps, step) {
				return nil, goclikit.UsageError(fmt.Errorf("--not-planned: unknown step %q — one of: %s", step, strings.Join(api.InitiativeOptionalSteps, ", ")))
			}
		}
		return &steps, nil
	case flagUntouched:
	}
	return nil, nil
}

// honoredNotPlannedWrite refuses a write whose answer marks other steps than
// the --not-planned it was sent, as an API from before them drops the field and
// answers as though it held it. A nil want sent none.
func honoredNotPlannedWrite(want *[]string, number int, carried []string) error {
	if want == nil || sameSet(*want, carried) {
		return nil
	}
	return fmt.Errorf("the API ignored --not-planned: #%d was written not planning %s rather than %s, so it predates planned steps",
		number, stepsOrNone(carried), stepsOrNone(*want))
}

func stepsOrNone(steps []string) string {
	if len(steps) == 0 {
		return "no step"
	}
	return strings.Join(steps, ", ")
}
