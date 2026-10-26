package compose

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"unicode/utf8"

	"gic/internal/mimeedit"
)

// Original is the view of an existing message that reply and forward need.
type Original struct {
	Header      mail.Header
	Text        string    // first text/plain part, decoded; "" when none
	Attachments []RawPart // attachment parts, verbatim
}

// Parse reads a raw RFC 5322 message. Text is the first text/plain part found
// walking multipart bodies depth-first; attachment parts are collected with
// their encoded bodies intact so they can be forwarded without re-encoding.
func Parse(raw []byte) (*Original, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parsing message: %w", err)
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, err
	}
	o := &Original{Header: msg.Header}
	o.walk(textproto.MIMEHeader(msg.Header), body)
	return o, nil
}

func (o *Original) walk(h textproto.MIMEHeader, body []byte) {
	mediaType, params, _ := mime.ParseMediaType(h.Get("Content-Type"))
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "multipart/") && params["boundary"] != "" {
		r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			p, err := r.NextRawPart()
			if err != nil {
				return
			}
			pb, err := io.ReadAll(p)
			if err != nil {
				return
			}
			o.walk(p.Header, pb)
		}
	}
	if _, ok := mimeedit.IsAttachment(h); ok {
		o.Attachments = append(o.Attachments, RawPart{Header: h, Body: body})
		return
	}
	if o.Text == "" && (mediaType == "text/plain" || mediaType == "") {
		o.Text = decodeText(h, params["charset"], body)
	}
}

// decodeText decodes the transfer encoding and, for the charsets Go can
// handle without an extra dependency, converts to UTF-8. Anything else is
// passed through as-is.
func decodeText(h textproto.MIMEHeader, charset string, body []byte) string {
	decoded, err := mimeedit.DecodeBody(h.Get("Content-Transfer-Encoding"), body)
	if err != nil {
		decoded = body
	}
	switch strings.ToLower(charset) {
	case "iso-8859-1", "latin1", "windows-1252", "us-ascii", "ascii":
		if utf8.Valid(decoded) {
			break
		}
		var sb strings.Builder
		for _, b := range decoded {
			sb.WriteRune(rune(b))
		}
		return strings.ReplaceAll(sb.String(), "\r\n", "\n")
	}
	return strings.ReplaceAll(string(decoded), "\r\n", "\n")
}

// decodedHeader returns a header value with RFC 2047 words decoded.
func (o *Original) decodedHeader(name string) string {
	v := o.Header.Get(name)
	if d, err := new(mime.WordDecoder).DecodeHeader(v); err == nil {
		return d
	}
	return v
}

// Subject returns the decoded Subject.
func (o *Original) Subject() string { return o.decodedHeader("Subject") }

// MessageID returns the Message-ID with angle brackets, or "".
func (o *Original) MessageID() string {
	id := strings.TrimSpace(o.Header.Get("Message-ID"))
	if id == "" {
		return ""
	}
	return "<" + strings.Trim(id, "<>") + ">"
}

// ReplyAddresses computes the To and Cc of a reply. To is Reply-To, or From
// when there is none. With all, the original To and Cc become Cc. self (the
// replying account) and duplicates are dropped from both lists.
func (o *Original) ReplyAddresses(self string, all bool) (to, cc []*mail.Address) {
	to = o.addressList("Reply-To")
	if len(to) == 0 {
		to = o.addressList("From")
	}
	if all {
		cc = append(o.addressList("To"), o.addressList("Cc")...)
	}
	seen := map[string]bool{strings.ToLower(self): true}
	dedupe := func(in []*mail.Address) []*mail.Address {
		var out []*mail.Address
		for _, a := range in {
			key := strings.ToLower(a.Address)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, a)
		}
		return out
	}
	to = dedupe(to)
	cc = dedupe(cc)
	if len(to) == 0 && len(cc) > 0 {
		// Replying to one's own message with --all: promote the Cc.
		to, cc = cc, nil
	}
	return to, cc
}

func (o *Original) addressList(name string) []*mail.Address {
	if o.Header.Get(name) == "" {
		return nil
	}
	addrs, err := o.Header.AddressList(name)
	if err != nil {
		return nil
	}
	return addrs
}

// ReplySubject prefixes "Re: " unless the subject already carries it.
func ReplySubject(subject string) string {
	if hasPrefixFold(subject, "re:") {
		return subject
	}
	return "Re: " + subject
}

// ForwardSubject prefixes "Fwd: " unless the subject already carries it.
func ForwardSubject(subject string) string {
	if hasPrefixFold(subject, "fwd:") || hasPrefixFold(subject, "fw:") {
		return subject
	}
	return "Fwd: " + subject
}

func hasPrefixFold(s, prefix string) bool {
	s = strings.TrimSpace(s)
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// ReplyReferences returns the References for a reply: the original's
// References followed by its Message-ID.
func (o *Original) ReplyReferences() string {
	refs := strings.Fields(o.Header.Get("References"))
	if id := o.MessageID(); id != "" {
		refs = append(refs, id)
	}
	return strings.Join(refs, "\r\n ")
}

// Quote renders the original text as a reply quotation:
// "On <date>, <from> wrote:" followed by "> "-prefixed lines. It returns ""
// when the original has no text/plain part.
func (o *Original) Quote() string {
	if o.Text == "" {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "On %s, %s wrote:\n", o.Header.Get("Date"), o.decodedHeader("From"))
	for _, line := range strings.Split(strings.TrimRight(o.Text, "\n"), "\n") {
		sb.WriteString("> ")
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return sb.String()
}

// ForwardBlock renders the inline-forward header block followed by the
// original text.
func (o *Original) ForwardBlock() string {
	var sb strings.Builder
	sb.WriteString("---------- Forwarded message ----------\n")
	fmt.Fprintf(&sb, "From: %s\n", o.decodedHeader("From"))
	fmt.Fprintf(&sb, "Date: %s\n", o.Header.Get("Date"))
	fmt.Fprintf(&sb, "Subject: %s\n", o.Subject())
	fmt.Fprintf(&sb, "To: %s\n", o.decodedHeader("To"))
	if cc := o.decodedHeader("Cc"); cc != "" {
		fmt.Fprintf(&sb, "Cc: %s\n", cc)
	}
	sb.WriteString("\n")
	sb.WriteString(o.Text)
	return sb.String()
}
