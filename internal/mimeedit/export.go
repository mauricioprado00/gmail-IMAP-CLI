package mimeedit

import "net/textproto"

// IsAttachment reports whether a part is an attachment, and its decoded
// filename (empty for unnamed attachment parts). See attachmentName.
func IsAttachment(h textproto.MIMEHeader) (name string, ok bool) {
	return attachmentName(h)
}

// DecodeBody decodes a part body according to its Content-Transfer-Encoding.
func DecodeBody(encoding string, body []byte) ([]byte, error) {
	return decodeBody(encoding, body)
}

// EncodeBody encodes a part body for the given Content-Transfer-Encoding,
// wrapping base64 at 76 columns.
func EncodeBody(encoding string, body []byte) ([]byte, error) {
	return encodeBody(encoding, body)
}
