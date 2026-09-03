// Package mimeedit rewrites MIME messages without interpreting their content.
package mimeedit

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"sort"
	"strings"
)

// Attachment describes a MIME attachment removed from a message.
type Attachment struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

// RemoveAttachments removes all attachments selected by matchName. The
// callback receives the decoded filename; unnamed attachment parts use an
// empty name. A visible removal notice and unique replacement marker are
// added. The marker is empty when no attachment matched.
func RemoveAttachments(raw []byte, matchName func(string) bool) ([]byte, []Attachment, string, error) {
	header, body, separator, err := splitMessage(raw)
	if err != nil {
		return nil, nil, "", err
	}
	h, err := parseHeader(header)
	if err != nil {
		return nil, nil, "", fmt.Errorf("parsing message headers: %w", err)
	}

	rewritten, removed, changed, err := rewriteEntity(h, body, matchName)
	if err != nil {
		return nil, nil, "", err
	}
	if !changed {
		return raw, nil, "", nil
	}

	notice := removalNotice(removed)
	rewritten, err = addNotice(h, rewritten, notice)
	if err != nil {
		return nil, nil, "", fmt.Errorf("adding removal notice: %w", err)
	}
	marker, err := newMarker()
	if err != nil {
		return nil, nil, "", fmt.Errorf("generating replacement marker: %w", err)
	}
	header = replacementHeaders(header, h.Get("Message-ID"), marker)

	out := make([]byte, 0, len(header)+len(separator)+len(rewritten))
	out = append(out, header...)
	out = append(out, separator...)
	out = append(out, rewritten...)
	return out, removed, marker, nil
}

func newMarker() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func replacementHeaders(raw []byte, originalMessageID, marker string) []byte {
	lineEnding := "\r\n"
	if !bytes.Contains(raw, []byte("\r\n")) {
		lineEnding = "\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	var kept []string
	skip := false
	for _, line := range lines {
		if line != "" && (line[0] == ' ' || line[0] == '\t') {
			if !skip {
				kept = append(kept, line)
			}
			continue
		}
		name := line
		if colon := strings.IndexByte(line, ':'); colon >= 0 {
			name = line[:colon]
		}
		switch strings.ToLower(name) {
		case "message-id", "dkim-signature", "arc-seal", "arc-message-signature",
			"arc-authentication-results", "x-gic-original-message-id",
			"x-gic-attachments-removed":
			skip = true
		default:
			skip = false
			kept = append(kept, line)
		}
	}
	kept = append(kept, "Message-ID: <gic."+marker+"@replacement.local>")
	if originalMessageID != "" {
		kept = append(kept, "X-GIC-Original-Message-ID: "+originalMessageID)
	}
	kept = append(kept, "X-GIC-Attachments-Removed: "+marker)
	return []byte(strings.Join(kept, lineEnding))
}

func splitMessage(raw []byte) (header, body, separator []byte, err error) {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[:i], raw[i+4:], raw[i : i+4], nil
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[:i], raw[i+2:], raw[i : i+2], nil
	}
	return nil, nil, nil, fmt.Errorf("message has no header/body separator")
}

func parseHeader(raw []byte) (textproto.MIMEHeader, error) {
	normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	return textproto.NewReader(bufio.NewReader(bytes.NewReader(append(normalized, '\n', '\n')))).ReadMIMEHeader()
}

func rewriteEntity(h textproto.MIMEHeader, body []byte, matchName func(string) bool) ([]byte, []Attachment, bool, error) {
	mediaType, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil && h.Get("Content-Type") != "" {
		return nil, nil, false, fmt.Errorf("parsing Content-Type %q: %w", h.Get("Content-Type"), err)
	}

	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		if strings.EqualFold(mediaType, "multipart/signed") {
			return nil, nil, false, fmt.Errorf("refusing to rewrite a multipart/signed entity")
		}
		boundary := params["boundary"]
		if boundary == "" {
			return nil, nil, false, fmt.Errorf("multipart entity has no boundary")
		}
		return rewriteMultipart(body, boundary, matchName)
	}
	return body, nil, false, nil
}

func rewriteMultipart(body []byte, boundary string, matchName func(string) bool) ([]byte, []Attachment, bool, error) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var parts []mimePart
	var removed []Attachment
	changed := false

	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, false, fmt.Errorf("reading multipart body: %w", err)
		}
		partBody, err := io.ReadAll(part)
		if err != nil {
			return nil, nil, false, fmt.Errorf("reading MIME part: %w", err)
		}
		name, attachment := attachmentName(part.Header)
		if attachment && matchName(name) {
			if name == "" {
				name = "unnamed attachment"
			}
			removed = append(removed, Attachment{Name: name, Size: decodedSize(part.Header, partBody)})
			changed = true
			continue
		}

		newBody, nested, nestedChanged, err := rewriteEntity(part.Header, partBody, matchName)
		if err != nil {
			return nil, nil, false, err
		}
		removed = append(removed, nested...)
		changed = changed || nestedChanged
		parts = append(parts, mimePart{header: part.Header, body: newBody})
	}
	if !changed {
		return body, nil, false, nil
	}
	rewritten, err := writeMultipart(boundary, parts)
	return rewritten, removed, true, err
}

type mimePart struct {
	header textproto.MIMEHeader
	body   []byte
}

func writeMultipart(boundary string, parts []mimePart) ([]byte, error) {
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, fmt.Errorf("preserving multipart boundary: %w", err)
	}
	for _, part := range parts {
		dst, err := writer.CreatePart(part.header)
		if err != nil {
			return nil, fmt.Errorf("writing MIME part headers: %w", err)
		}
		if _, err := dst.Write(part.body); err != nil {
			return nil, fmt.Errorf("writing MIME part body: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("closing multipart body: %w", err)
	}
	return out.Bytes(), nil
}

func attachmentName(h textproto.MIMEHeader) (string, bool) {
	disposition, dispParams, _ := mime.ParseMediaType(h.Get("Content-Disposition"))
	_, typeParams, _ := mime.ParseMediaType(h.Get("Content-Type"))
	name := dispParams["filename"]
	if name == "" {
		name = typeParams["name"]
	}
	if decoded, err := new(mime.WordDecoder).DecodeHeader(name); err == nil {
		name = decoded
	}
	return name, strings.EqualFold(disposition, "attachment") || name != ""
}

func decodedSize(h textproto.MIMEHeader, body []byte) int {
	decoded, err := decodeBody(h.Get("Content-Transfer-Encoding"), body)
	if err != nil {
		return len(body)
	}
	return len(decoded)
}

func removalNotice(removed []Attachment) string {
	names := make([]string, len(removed))
	for i, attachment := range removed {
		names[i] = attachment.Name
	}
	sort.Strings(names)
	lines := make([]string, len(names))
	for i, name := range names {
		lines[i] = name + " removed with gic"
	}
	return strings.Join(lines, "\r\n")
}

func addNotice(h textproto.MIMEHeader, body []byte, notice string) ([]byte, error) {
	mediaType, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil && h.Get("Content-Type") != "" {
		return nil, err
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "" {
		mediaType = "text/plain"
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		var parts []mimePart
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			partBody, err := io.ReadAll(part)
			if err != nil {
				return nil, err
			}
			parts = append(parts, mimePart{header: part.Header, body: partBody})
		}
		if mediaType == "multipart/mixed" {
			parts = append(parts, mimePart{
				header: textproto.MIMEHeader{
					"Content-Type":              []string{`text/plain; charset="UTF-8"`},
					"Content-Transfer-Encoding": []string{"quoted-printable"},
				},
				body: encodeQuotedPrintable([]byte(notice + "\r\n")),
			})
			return writeMultipart(boundary, parts)
		}

		added := false
		for i := len(parts) - 1; i >= 0; i-- {
			newBody, ok, err := addNoticeToText(parts[i].header, parts[i].body, notice)
			if err != nil {
				return nil, err
			}
			if ok {
				parts[i].body = newBody
				added = true
				if mediaType != "multipart/alternative" {
					break
				}
			}
		}
		if !added {
			return nil, fmt.Errorf("message has no displayable text part for the removal notice")
		}
		return writeMultipart(boundary, parts)
	}

	rewritten, ok, err := addNoticeToText(h, body, notice)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("message has no displayable text part for the removal notice")
	}
	return rewritten, nil
}

func addNoticeToText(h textproto.MIMEHeader, body []byte, notice string) ([]byte, bool, error) {
	mediaType, params, _ := mime.ParseMediaType(h.Get("Content-Type"))
	mediaType = strings.ToLower(mediaType)
	if mediaType == "" {
		mediaType = "text/plain"
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		rewritten, err := addNotice(h, body, notice)
		return rewritten, err == nil, err
	}
	if mediaType != "text/plain" && mediaType != "text/html" {
		return body, false, nil
	}
	charset := strings.ToLower(params["charset"])
	switch charset {
	case "", "us-ascii", "utf-8", "iso-8859-1", "windows-1252":
	default:
		return nil, false, fmt.Errorf("cannot safely add a notice to text using charset %q", charset)
	}

	decoded, err := decodeBody(h.Get("Content-Transfer-Encoding"), body)
	if err != nil {
		return nil, false, err
	}
	if mediaType == "text/html" {
		note := "<p>" + strings.ReplaceAll(html.EscapeString(notice), "\r\n", "<br>\r\n") + "</p>"
		lower := strings.ToLower(string(decoded))
		if i := strings.LastIndex(lower, "</body>"); i >= 0 {
			decoded = append(append(append([]byte{}, decoded[:i]...), []byte(note)...), decoded[i:]...)
		} else {
			decoded = append(decoded, []byte("\r\n"+note)...)
		}
	} else {
		decoded = append(decoded, []byte("\r\n\r\n"+notice+"\r\n")...)
	}
	encoded, err := encodeBody(h.Get("Content-Transfer-Encoding"), decoded)
	return encoded, true, err
}

func decodeBody(encoding string, body []byte) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		compact := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(body))
		return base64.StdEncoding.DecodeString(compact)
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
	default:
		return append([]byte(nil), body...), nil
	}
}

func encodeBody(encoding string, body []byte) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		encoded := base64.StdEncoding.EncodeToString(body)
		var out strings.Builder
		for len(encoded) > 76 {
			out.WriteString(encoded[:76])
			out.WriteString("\r\n")
			encoded = encoded[76:]
		}
		out.WriteString(encoded)
		out.WriteString("\r\n")
		return []byte(out.String()), nil
	case "quoted-printable":
		return encodeQuotedPrintable(body), nil
	default:
		return body, nil
	}
}

func encodeQuotedPrintable(body []byte) []byte {
	var out bytes.Buffer
	writer := quotedprintable.NewWriter(&out)
	_, _ = writer.Write(body)
	_ = writer.Close()
	return out.Bytes()
}
