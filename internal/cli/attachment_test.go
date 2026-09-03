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
	want := []string{`\Important`, `project`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Gmail derives \Sent and \Draft from the message itself; STORE X-GM-LABELS
// cannot set them, and one rejected label fails the whole request.
func TestMutableLabelsDropsUnsettableSystemLabels(t *testing.T) {
	got := mutableLabels([]string{`\Sent`, `\Draft`, `\Inbox`, `\Starred`, `\Spam`}, "INBOX")
	want := []string{`\Inbox`, `\Starred`, `\Spam`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
