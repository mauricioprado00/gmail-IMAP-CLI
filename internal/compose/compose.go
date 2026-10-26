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
	for _, p := range parts {
		h.WriteString("--" + boundary + "\r\n")
		for _, k := range sortedKeys(p.Header) {
			writeHeader(&h, k, p.Header.Get(k))
		}
		h.WriteString("\r\n")
		h.Write(p.Body)
		if !bytes.HasSuffix(p.Body, []byte("\r\n")) {
			h.WriteString("\r\n")
		}
	}
	h.WriteString("--" + boundary + "--\r\n")
	return h.Bytes(), nil
}

// bodyParts returns the text part followed by every attachment part.
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

	for _, a := range m.Attachments {
		ct := a.ContentType
		if ct == "" {
			ct = mime.TypeByExtension(strings.ToLower(filepath.Ext(a.Name)))
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		body, err := mimeedit.EncodeBody("base64", a.Data)
		if err != nil {
			return nil, err
		}
		parts = append(parts, RawPart{
			Header: textproto.MIMEHeader{
				"Content-Type":              {mime.FormatMediaType(ct, map[string]string{"name": a.Name})},
				"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": a.Name})},
				"Content-Transfer-Encoding": {"base64"},
			},
			Body: body,
		})
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
