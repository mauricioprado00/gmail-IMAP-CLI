// Package compose builds RFC 5322 messages for `gic send`, `gic reply` and
// `gic forward`, and extracts what those need from an existing message.
package compose

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gic/internal/mimeedit"
)

// Attachment is a file attached from disk (or any decoded bytes).
type Attachment struct {
	Name        string
	ContentType string // empty: sniffed from Name's extension
	Data        []byte
}

// RawPart is a MIME part carried over verbatim from another message: its
// header fields and still-encoded body, so nothing is re-encoded.
type RawPart struct {
	Header textproto.MIMEHeader
	Body   []byte
}

// Embedded is a whole message attached as message/rfc822.
type Embedded struct {
	Name string // filename shown by the receiving client
	Raw  []byte
}

// Message is what Build turns into bytes.
type Message struct {
	From        *mail.Address
	To, Cc, Bcc []*mail.Address
	Subject     string
	Text        string // plain-text body; "\n" or "\r\n" line endings
	InReplyTo   string // "<id>" or ""
	References  string
	Date        time.Time // zero: now
	MessageID   string    // "<id>"; empty: generated
	Attachments []Attachment
	Invite      []byte // iCalendar data sent as a calendar invitation; nil: none
	RawParts    []RawPart
	Embedded    *Embedded
}

// Recipients returns the SMTP envelope recipients: every To, Cc and Bcc
// address, deduplicated case-insensitively, in that order.
func (m *Message) Recipients() []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]*mail.Address{m.To, m.Cc, m.Bcc} {
		for _, a := range list {
			key := strings.ToLower(a.Address)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, a.Address)
		}
	}
	return out
}

// NewMessageID returns a fresh "<random@gic>" identifier.
func NewMessageID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "<" + hex.EncodeToString(b[:]) + "@gic>", nil
}

// Build renders the message with CRLF line endings. Bcc is never written to
// the headers; it only takes part in Recipients. The Message-ID is generated
// when unset and written back to m so callers can report it.
func (m *Message) Build() ([]byte, error) {
	if m.From == nil {
		return nil, fmt.Errorf("a From address is required")
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		return nil, fmt.Errorf("at least one recipient is required")
	}
	if m.MessageID == "" {
		id, err := NewMessageID()
		if err != nil {
			return nil, fmt.Errorf("generating Message-ID: %w", err)
		}
		m.MessageID = id
	}
	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}

	var h bytes.Buffer
	writeHeader(&h, "Date", date.Format(time.RFC1123Z))
	writeHeader(&h, "From", formatAddress(m.From))
	if len(m.To) > 0 {
		writeHeader(&h, "To", joinAddresses(m.To))
	}
	if len(m.Cc) > 0 {
		writeHeader(&h, "Cc", joinAddresses(m.Cc))
	}
	writeHeader(&h, "Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	writeHeader(&h, "Message-ID", m.MessageID)
	if m.InReplyTo != "" {
		writeHeader(&h, "In-Reply-To", m.InReplyTo)
	}
	if m.References != "" {
		writeHeader(&h, "References", m.References)
	}
	writeHeader(&h, "MIME-Version", "1.0")

	parts, err := m.bodyParts()
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 {
		p := parts[0]
		for _, k := range sortedKeys(p.Header) {
			writeHeader(&h, k, p.Header.Get(k))
		}
		h.WriteString("\r\n")
		h.Write(p.Body)
		return h.Bytes(), nil
	}

	boundary, err := newBoundary()
	if err != nil {
		return nil, err
	}
	writeHeader(&h, "Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", boundary))
	h.WriteString("\r\n")
	writeParts(&h, boundary, parts)
	return h.Bytes(), nil
}

// writeParts writes parts as the body of a multipart entity, closing
// delimiter included.
func writeParts(b *bytes.Buffer, boundary string, parts []RawPart) {
	for _, p := range parts {
		b.WriteString("--" + boundary + "\r\n")
		for _, k := range sortedKeys(p.Header) {
			writeHeader(b, k, p.Header.Get(k))
		}
		b.WriteString("\r\n")
		b.Write(p.Body)
		if !bytes.HasSuffix(p.Body, []byte("\r\n")) {
			b.WriteString("\r\n")
		}
	}
	b.WriteString("--" + boundary + "--\r\n")
}

// bodyParts returns the text part (wrapped with the invitation when there is
// one) followed by every attachment part.
func (m *Message) bodyParts() ([]RawPart, error) {
	text := strings.ReplaceAll(m.Text, "\r\n", "\n")
	var qp bytes.Buffer
	w := quotedprintable.NewWriter(&qp)
	if _, err := w.Write([]byte(text)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	parts := []RawPart{{
		Header: textproto.MIMEHeader{
			"Content-Type":              {"text/plain; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		},
		Body: qp.Bytes(),
	}}
	if len(m.Invite) > 0 {
		// Gmail shows its RSVP card only for text/calendar inside a
		// multipart/alternative next to the text, plus an .ics attachment.
		alt, err := inviteAlternative(parts[0], m.Invite)
		if err != nil {
			return nil, err
		}
		ics, err := attachmentPart(Attachment{Name: "invite.ics", ContentType: "application/ics", Data: m.Invite})
		if err != nil {
			return nil, err
		}
		parts = []RawPart{alt, ics}
	}

	for _, a := range m.Attachments {
		p, err := attachmentPart(a)
		if err != nil {
			return nil, err
		}
		parts = append(parts, p)
	}
	parts = append(parts, m.RawParts...)
	if m.Embedded != nil {
		// message/rfc822 may only be 7bit/8bit/binary (RFC 2046 §5.2.1), so
		// the original goes in untouched.
		parts = append(parts, RawPart{
			Header: textproto.MIMEHeader{
				"Content-Type":        {mime.FormatMediaType("message/rfc822", map[string]string{"name": m.Embedded.Name})},
				"Content-Disposition": {mime.FormatMediaType("attachment", map[string]string{"filename": m.Embedded.Name})},
			},
			Body: m.Embedded.Raw,
		})
	}
	return parts, nil
}

func writeHeader(b *bytes.Buffer, name, value string) {
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\r\n")
}

func joinAddresses(addrs []*mail.Address) string {
	strs := make([]string, len(addrs))
	for i, a := range addrs {
		strs[i] = formatAddress(a)
	}
	return strings.Join(strs, ",\r\n ")
}

// formatAddress renders a bare address when there is no display name, and
// the RFC 2047-encoded "Name <addr>" form otherwise.
func formatAddress(a *mail.Address) string {
	if a.Name == "" {
		return a.Address
	}
	return a.String()
}

func sortedKeys(h textproto.MIMEHeader) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func newBoundary() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "=_gic_" + hex.EncodeToString(b[:]), nil
}

// ParseAddresses parses flag values, each of which may itself be a
// comma-separated list, into addresses.
func ParseAddresses(values []string) ([]*mail.Address, error) {
	var out []*mail.Address
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		addrs, err := mail.ParseAddressList(v)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q: %w", v, err)
		}
		out = append(out, addrs...)
	}
	return out, nil
}

// attachmentPart encodes a as a base64 attachment part. The content type
// comes from a.ContentType or the name's extension; parameters such as the
// "; charset=utf-8" Linux's MIME database adds to text/* types are kept.
func attachmentPart(a Attachment) (RawPart, error) {
	ct := a.ContentType
	if ct == "" {
		ct = mime.TypeByExtension(strings.ToLower(filepath.Ext(a.Name)))
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		mt, params = "application/octet-stream", map[string]string{}
	}
	params["name"] = a.Name
	body, err := mimeedit.EncodeBody("base64", a.Data)
	if err != nil {
		return RawPart{}, err
	}
	return RawPart{
		Header: textproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType(mt, params)},
			"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": a.Name})},
			"Content-Transfer-Encoding": {"base64"},
		},
		Body: body,
	}, nil
}

// inviteMethod returns the METHOD property of an iCalendar object, or
// REQUEST when it has none. METHOD is a short token, so folded lines are not
// a concern.
func inviteMethod(ics []byte) string {
	for _, line := range strings.Split(strings.ReplaceAll(string(ics), "\r\n", "\n"), "\n") {
		if v, ok := strings.CutPrefix(strings.ToUpper(line), "METHOD:"); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return "REQUEST"
}

// inviteAlternative wraps the text part and the calendar data in a
// multipart/alternative, the shape mail clients render as an invitation.
func inviteAlternative(text RawPart, ics []byte) (RawPart, error) {
	if !bytes.HasPrefix(bytes.ToUpper(bytes.TrimSpace(ics)), []byte("BEGIN:VCALENDAR")) {
		return RawPart{}, fmt.Errorf("invitation is not an iCalendar file (it must start with BEGIN:VCALENDAR)")
	}
	body, err := mimeedit.EncodeBody("base64", ics)
	if err != nil {
		return RawPart{}, err
	}
	cal := RawPart{
		Header: textproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType("text/calendar", map[string]string{"charset": "UTF-8", "method": inviteMethod(ics)})},
			"Content-Transfer-Encoding": {"base64"},
		},
		Body: body,
	}
	boundary, err := newBoundary()
	if err != nil {
		return RawPart{}, err
	}
	var b bytes.Buffer
	writeParts(&b, boundary, []RawPart{text, cal})
	return RawPart{
		Header: textproto.MIMEHeader{"Content-Type": {fmt.Sprintf("multipart/alternative; boundary=%q", boundary)}},
		Body:   b.Bytes(),
	}, nil
}
