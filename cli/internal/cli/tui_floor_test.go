package cli

import (
	"testing"

	"github.com/datapointchris/gotermboard/floortest"
)

// slate tui's own tabs, on the stand-in with every read landed, keep the keys every board shares.
func TestTheTUIMeetsTheFloor(t *testing.T) {
	floortest.Check(t, openJumpTabs(t, &jumpAPI{}, ""), floortest.Has{Views: true, List: true})
}
