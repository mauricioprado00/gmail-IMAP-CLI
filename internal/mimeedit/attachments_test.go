package mimeedit

import (
	"bytes"
	"strings"
	"testing"
)

const testMessage = "From: sender@example.com\r\n" +
	"To: receiver@example.com\r\n" +
	"Subject: Files\r\n" +
	"Message-ID: <original@example.com>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
	"--outer\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nHello\r\n" +
	"--outer\r\nContent-Type: image/jpeg; name=\"photo.jpg\"\r\n" +
	"Content-Disposition: attachment; filename=\"photo.jpg\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\nYWJj\r\n" +
	"--outer\r\nContent-Type: application/pdf; name=\"keep.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"keep.pdf\"\r\n\r\nPDF\r\n" +
	"--outer--\r\n"

func TestRemoveSelectedAttachment(t *testing.T) {
	got, removed, marker, err := RemoveAttachments([]byte(testMessage), func(name string) bool {
		return name == "photo.jpg"
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Name != "photo.jpg" || removed[0].Size != 3 {
		t.Fatalf("removed = %+v", removed)
	}
	if marker == "" || !bytes.Contains(got, []byte("X-GIC-Attachments-Removed: "+marker)) {
		t.Fatalf("replacement marker missing:\n%s", got)
	}
	if bytes.Contains(got, []byte("\r\nMessage-ID: <original@example.com>")) {
		t.Fatalf("original Message-ID was retained:\n%s", got)
	}
	if bytes.Contains(got, []byte("YWJj")) || bytes.Contains(got, []byte(`filename="photo.jpg"`)) {
		t.Errorf("removed attachment remains in message:\n%s", got)
	}
	if !bytes.Contains(got, []byte(`filename="keep.pdf"`)) {
		t.Errorf("unselected attachment was removed:\n%s", got)
	}
	if !bytes.Contains(got, []byte("photo.jpg removed with gic")) {
		t.Errorf("removal notice missing:\n%s", got)
	}
	if !bytes.Contains(got, []byte("Subject: Files")) {
		t.Errorf("top-level headers changed:\n%s", got)
	}
}

func TestRemoveAllAttachments(t *testing.T) {
	got, removed, _, err := RemoveAttachments([]byte(testMessage), func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %+v", removed)
	}
	for _, name := range []string{"photo.jpg", "keep.pdf"} {
		if !bytes.Contains(got, []byte(name+" removed with gic")) {
			t.Errorf("notice for %q missing:\n%s", name, got)
		}
	}
}

func TestRemoveAttachmentNoMatchLeavesMessageUnchanged(t *testing.T) {
	raw := []byte(testMessage)
	got, removed, marker, err := RemoveAttachments(raw, func(name string) bool { return name == "missing.zip" })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %+v", removed)
	}
	if marker != "" {
		t.Fatalf("marker = %q without a match", marker)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("message changed without a matching attachment")
	}
}

func TestRemoveNestedAttachmentAddsNoticeToAlternative(t *testing.T) {
	raw := strings.Replace(testMessage,
		"Content-Type: image/jpeg; name=\"photo.jpg\"\r\nContent-Disposition: attachment; filename=\"photo.jpg\"\r\nContent-Transfer-Encoding: base64\r\n\r\nYWJj",
		"Content-Type: multipart/related; boundary=inner\r\n\r\n--inner\r\nContent-Type: text/html\r\n\r\n<html><body>Hello</body></html>\r\n--inner\r\nContent-Type: image/jpeg; name=\"photo.jpg\"\r\nContent-Disposition: inline; filename=\"photo.jpg\"\r\n\r\nabc\r\n--inner--",
		1,
	)
	got, removed, _, err := RemoveAttachments([]byte(raw), func(name string) bool { return name == "photo.jpg" })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %+v", removed)
	}
	if !bytes.Contains(got, []byte("photo.jpg removed with gic")) {
		t.Errorf("notice missing:\n%s", got)
	}
}

// Some mailers put a name parameter on the message body itself. Removing it
// would delete the message text, so a bare name is not enough to make a
// displayable text part an attachment.
func TestRemoveAllKeepsNamedBodyPart(t *testing.T) {
	raw := strings.Replace(testMessage,
		"Content-Type: text/plain; charset=utf-8\r\n\r\nHello",
		"Content-Type: text/plain; charset=utf-8; name=\"message.txt\"\r\n\r\nHello",
		1,
	)
	got, removed, _, err := RemoveAttachments([]byte(raw), func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %+v, want only the two real attachments", removed)
	}
	for _, attachment := range removed {
		if attachment.Name == "message.txt" {
			t.Fatalf("message body was removed as an attachment:\n%s", got)
		}
	}
	if !bytes.Contains(got, []byte("Hello")) {
		t.Errorf("message body text lost:\n%s", got)
	}
}

// A text part explicitly marked as an attachment is still removable.
func TestRemoveTextAttachmentWithDisposition(t *testing.T) {
	raw := strings.Replace(testMessage,
		"Content-Type: application/pdf; name=\"keep.pdf\"\r\nContent-Disposition: attachment; filename=\"keep.pdf\"\r\n\r\nPDF",
		"Content-Type: text/plain; charset=utf-8\r\nContent-Disposition: attachment; filename=\"notes.txt\"\r\n\r\nnotes",
		1,
	)
	_, removed, _, err := RemoveAttachments([]byte(raw), func(name string) bool { return name == "notes.txt" })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].Name != "notes.txt" {
		t.Fatalf("removed = %+v", removed)
	}
}
