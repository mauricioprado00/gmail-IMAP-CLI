package compose

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func addr(s string) *mail.Address {
	a, err := mail.ParseAddress(s)
	if err != nil {
		panic(err)
	}
	return a
}

func TestBuildPlainText(t *testing.T) {
	m := &Message{
		From:    addr("me@example.com"),
		To:      []*mail.Address{addr("Ann <ann@example.com>")},
		Bcc:     []*mail.Address{addr("hidden@example.com")},
		Subject: "Café ☕",
		Text:    "Hello\nwith ünïcode and a long line " + strings.Repeat("x", 100) + "\n",
		Date:    time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	}
	raw, err := m.Build()
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "hidden@example.com") {
		t.Error("Bcc leaked into headers")
	}
	if !strings.Contains(s, "From: me@example.com\r\n") || !strings.Contains(s, "To: \"Ann\" <ann@example.com>\r\n") {
		t.Errorf("missing To header:\n%s", s)
	}
	if !strings.Contains(s, "Subject: =?utf-8?q?Caf=C3=A9_=E2=98=95?=\r\n") {
		t.Errorf("subject not RFC 2047 encoded:\n%s", s)
	}
	if !strings.HasSuffix(m.MessageID, "@gic>") || !strings.Contains(s, "Message-ID: "+m.MessageID) {
		t.Errorf("Message-ID not generated/written: %q", m.MessageID)
	}
	if !strings.Contains(s, "Content-Transfer-Encoding: quoted-printable\r\n") {
		t.Error("body not quoted-printable")
	}
	for _, line := range strings.Split(s, "\r\n") {
		if len(line) > 78 {
			t.Errorf("line longer than 78 chars: %q", line)
		}
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(msg.Body)
	o, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(o.Text, "Hello\nwith ünïcode") {
		t.Errorf("decoded text = %q (raw body %q)", o.Text, body)
	}
	got := m.Recipients()
	if len(got) != 2 || got[0] != "ann@example.com" || got[1] != "hidden@example.com" {
		t.Errorf("Recipients() = %v", got)
	}
}

func TestBuildWithAttachmentsAndEmbedded(t *testing.T) {
	m := &Message{
		From:        addr("me@example.com"),
		To:          []*mail.Address{addr("ann@example.com")},
		Subject:     "files",
		Text:        "see attached",
		Attachments: []Attachment{{Name: "report.pdf", Data: []byte("%PDF-1.4 fake")}},
		RawParts: []RawPart{{
			Header: map[string][]string{
				"Content-Type":              {`image/png; name="a.png"`},
				"Content-Disposition":       {`attachment; filename="a.png"`},
				"Content-Transfer-Encoding": {"base64"},
			},
			Body: []byte("iVBORw0KGgo=\r\n"),
		}},
		Embedded: &Embedded{Name: "orig.eml", Raw: []byte("Subject: inner\r\n\r\nbody\r\n")},
	}
	raw, err := m.Build()
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("Content-Type = %q, %v", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var types []string
	var bodies []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		types = append(types, p.Header.Get("Content-Type"))
		bodies = append(bodies, string(b))
	}
	want := []string{
		"text/plain; charset=utf-8",
		`application/pdf; name=report.pdf`,
		`image/png; name="a.png"`,
		`message/rfc822; name=orig.eml`,
	}
	if strings.Join(types, "|") != strings.Join(want, "|") {
		t.Errorf("part types = %q, want %q", types, want)
	}
	// multipart.NextPart decodes quoted-printable but leaves base64 alone.
	pdf, _ := base64.StdEncoding.DecodeString(bodies[1])
	if bodies[0] != "see attached" || string(pdf) != "%PDF-1.4 fake" {
		t.Errorf("decoded bodies = %q", bodies[:2])
	}
	if !strings.HasPrefix(bodies[3], "Subject: inner") {
		t.Errorf("embedded message altered: %q", bodies[3])
	}
}

const original = "From: =?utf-8?q?Jos=C3=A9?= <jose@example.com>\r\n" +
	"Reply-To: replies@example.com\r\n" +
	"To: me@example.com, Bob <bob@example.com>\r\n" +
	"Cc: carol@example.com\r\n" +
	"Subject: RE: hello\r\n" +
	"Date: Mon, 26 Oct 2026 10:00:00 +0000\r\n" +
	"Message-ID: <orig@example.com>\r\n" +
	"References: <first@example.com>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"outer\"\r\n" +
	"\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/alternative; boundary=\"inner\"\r\n" +
	"\r\n" +
	"--inner\r\n" +
	"Content-Type: text/plain; charset=iso-8859-1\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"Hola Jos=E9,\r\nsecond line\r\n" +
	"--inner\r\n" +
	"Content-Type: text/html\r\n" +
	"\r\n" +
	"<p>Hola</p>\r\n" +
	"--inner--\r\n" +
	"--outer\r\n" +
	"Content-Type: application/pdf; name=\"doc.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0=\r\n" +
	"--outer--\r\n"

func TestParseOriginal(t *testing.T) {
	o, err := Parse([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	// The CRLF before a boundary belongs to the boundary, not the part.
	if o.Text != "Hola José,\nsecond line" {
		t.Errorf("Text = %q", o.Text)
	}
	if len(o.Attachments) != 1 || o.Attachments[0].Header.Get("Content-Type") != `application/pdf; name="doc.pdf"` {
		t.Fatalf("Attachments = %+v", o.Attachments)
	}
	if string(o.Attachments[0].Body) != "JVBERi0=" {
		t.Errorf("attachment body re-encoded: %q", o.Attachments[0].Body)
	}
	if o.MessageID() != "<orig@example.com>" {
		t.Errorf("MessageID = %q", o.MessageID())
	}
	if got := o.ReplyReferences(); got != "<first@example.com>\r\n <orig@example.com>" {
		t.Errorf("ReplyReferences = %q", got)
	}
	if ReplySubject(o.Subject()) != "RE: hello" || ReplySubject("hello") != "Re: hello" {
		t.Error("ReplySubject prefixing wrong")
	}
	if ForwardSubject("fw: x") != "fw: x" || ForwardSubject("x") != "Fwd: x" {
		t.Error("ForwardSubject prefixing wrong")
	}
	q := o.Quote()
	if !strings.HasPrefix(q, "On Mon, 26 Oct 2026 10:00:00 +0000, José <jose@example.com> wrote:\n> Hola José,\n> second line\n") {
		t.Errorf("Quote = %q", q)
	}
	fb := o.ForwardBlock()
	if !strings.Contains(fb, "---------- Forwarded message ----------\nFrom: José <jose@example.com>\n") || !strings.HasSuffix(fb, "\n\nHola José,\nsecond line") {
		t.Errorf("ForwardBlock = %q", fb)
	}
}

func TestReplyAddresses(t *testing.T) {
	o, _ := Parse([]byte(original))
	to, cc := o.ReplyAddresses("me@example.com", false)
	if len(to) != 1 || to[0].Address != "replies@example.com" || len(cc) != 0 {
		t.Errorf("plain reply: to=%v cc=%v", to, cc)
	}
	to, cc = o.ReplyAddresses("ME@example.com", true)
	if len(to) != 1 || to[0].Address != "replies@example.com" {
		t.Errorf("reply-all to=%v", to)
	}
	if len(cc) != 2 || cc[0].Address != "bob@example.com" || cc[1].Address != "carol@example.com" {
		t.Errorf("reply-all cc=%v", cc)
	}

	// No Reply-To: From is used. Own message with --all: Cc promoted to To.
	own, _ := Parse([]byte("From: me@example.com\r\nTo: x@example.com\r\n\r\nhi\r\n"))
	to, cc = own.ReplyAddresses("me@example.com", true)
	if len(to) != 1 || to[0].Address != "x@example.com" || len(cc) != 0 {
		t.Errorf("own message reply-all: to=%v cc=%v", to, cc)
	}
}

func TestParseAddresses(t *testing.T) {
	got, err := ParseAddresses([]string{"a@x.com, B <b@x.com>", "c@x.com"})
	if err != nil || len(got) != 3 || got[1].Name != "B" {
		t.Errorf("got %v, %v", got, err)
	}
	if _, err := ParseAddresses([]string{"not an address"}); err == nil {
		t.Error("expected error for invalid address")
	}
}

func TestBuildRequiresRecipient(t *testing.T) {
	m := &Message{From: addr("me@example.com")}
	if _, err := m.Build(); err == nil {
		t.Error("expected error without recipients")
	}
}
