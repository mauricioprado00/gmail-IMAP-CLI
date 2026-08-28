// Package model defines the JSON shape returned by `gic search` and
// friends, and how to build it from a raw IMAP FETCH result.
package model

import (
	"bytes"
	"encoding/base64"
	"net/mail"
	"strings"

	"gic/internal/imapc"
)

// Body holds the best-effort text content of a message (see docs/COMMANDS
// §4.3: this is the raw BODY[TEXT] section, not MIME-part-split).
type Body struct {
	Text string `json:"text,omitempty"`
}

// Message is one search result / fetch result, in the JSON shape
// documented in docs/COMMANDS.md §4.3.
type Message struct {
	UID       uint32   `json:"uid"`
	Mailbox   string   `json:"mailbox"`
	MessageID string   `json:"messageId,omitempty"`
	ThreadID  string   `json:"threadId,omitempty"`
	From      string   `json:"from,omitempty"`
	To        []string `json:"to,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Date      string   `json:"date,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Size      int      `json:"size"`
	Body      *Body    `json:"body,omitempty"`
	Raw       string   `json:"raw,omitempty"` // base64, full mode only
}

// FromFetch builds a Message from a raw IMAP fetch result.
func FromFetch(fr imapc.FetchResult, mailbox, mode string) *Message {
	m := &Message{
		UID:      fr.UID,
		Mailbox:  mailbox,
		Labels:   fr.Labels,
		ThreadID: fr.ThreadID,
		Size:     fr.Size,
	}

	headerBytes := fr.Headers
	if len(headerBytes) == 0 && len(fr.Raw) > 0 {
		// full mode fetches BODY[] only; split off the header section.
		if idx := bytes.Index(fr.Raw, []byte("\r\n\r\n")); idx >= 0 {
			headerBytes = fr.Raw[:idx+2]
		} else {
			headerBytes = fr.Raw
		}
	}
	if len(headerBytes) > 0 {
		if msg, err := mail.ReadMessage(bytes.NewReader(append(headerBytes, '\r', '\n'))); err == nil {
			h := msg.Header
			m.MessageID = strings.Trim(h.Get("Message-Id"), "<>")
			m.From = h.Get("From")
			m.Subject = h.Get("Subject")
			m.Date = h.Get("Date")
			if addrs, err := h.AddressList("To"); err == nil {
				for _, a := range addrs {
					m.To = append(m.To, a.Address)
				}
			}
		}
	}

	if mode == "standard" && len(fr.Text) > 0 {
		m.Body = &Body{Text: string(fr.Text)}
	}
	if mode == "full" {
		if len(fr.Raw) > 0 {
			m.Raw = base64.StdEncoding.EncodeToString(fr.Raw)
		}
	}
	return m
}

// FetchItems returns the IMAP FETCH data-item list for a retrieval mode.
func FetchItems(mode string) string {
	const headerFields = `BODY.PEEK[HEADER.FIELDS (FROM TO SUBJECT DATE MESSAGE-ID)]`
	switch mode {
	case "light":
		return "UID FLAGS X-GM-LABELS X-GM-THRID RFC822.SIZE " + headerFields
	case "full":
		return "UID FLAGS X-GM-LABELS X-GM-THRID RFC822.SIZE BODY.PEEK[]"
	default: // "standard"
		return "UID FLAGS X-GM-LABELS X-GM-THRID RFC822.SIZE " + headerFields + " BODY.PEEK[TEXT]"
	}
}
