package cli

import (
	"reflect"
	"testing"
)

func TestAppendableFlags(t *testing.T) {
	got := appendableFlags([]string{`\Seen`, `\Recent`, `\Flagged`, `custom`, `\Deleted`})
	want := []string{`\Seen`, `\Flagged`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMutableLabels(t *testing.T) {
	got := mutableLabels(
		[]string{`\All`, `\Sent`, `\Important`, `project`, `[Gmail]/All Mail`, `\Trash`},
		`[Gmail]/All Mail`,
	)
	want := []string{`\Sent`, `\Important`, `project`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
