package smtpc

import (
	"bufio"
	"encoding/base64"
	"net"
	"strings"
	"testing"
)

// fakeServer speaks just enough SMTP (cleartext, AUTH PLAIN) to accept one
// message and record the transaction.
type fakeServer struct {
	lines     []string
	auth      string
	data      string
	rejectAll bool
}

func (f *fakeServer) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := func(s string) { conn.Write([]byte(s + "\r\n")) }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.lines = append(f.lines, line)
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			w("250-fake")
			w("250 AUTH PLAIN")
		case strings.HasPrefix(upper, "AUTH PLAIN "):
			f.auth = line[len("AUTH PLAIN "):]
			w("235 ok")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			w("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			if f.rejectAll {
				w("550 no such user")
			} else {
				w("250 ok")
			}
		case upper == "DATA":
			w("354 go")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			f.data = sb.String()
			w("250 queued")
		case upper == "QUIT":
			w("221 bye")
			return
		default:
			w("500 what")
		}
	}
}

func start(t *testing.T, f *fakeServer) net.Conn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err == nil {
			f.serve(c)
		}
		close(done)
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); ln.Close(); <-done })
	return conn
}

func TestSendOn(t *testing.T) {
	f := &fakeServer{}
	conn := start(t, f)
	env := Envelope{From: "me@example.com", Recipients: []string{"a@example.com", "b@example.com"}}
	msg := []byte("Subject: hi\r\n\r\nline one\r\n.dot-stuffed\r\n")
	if err := SendOn(conn, "localhost", "me@example.com", "secret pw", env, msg, false); err != nil {
		t.Fatal(err)
	}
	creds, _ := base64.StdEncoding.DecodeString(f.auth)
	if string(creds) != "\x00me@example.com\x00secret pw" {
		t.Errorf("AUTH PLAIN credentials = %q", creds)
	}
	joined := strings.Join(f.lines, "\n")
	for _, want := range []string{"MAIL FROM:<me@example.com>", "RCPT TO:<a@example.com>", "RCPT TO:<b@example.com>", "QUIT"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in dialogue:\n%s", want, joined)
		}
	}
	if f.data != "Subject: hi\r\n\r\nline one\r\n..dot-stuffed\r\n" {
		t.Errorf("DATA = %q", f.data)
	}
}

func TestSendOnRecipientRejected(t *testing.T) {
	f := &fakeServer{rejectAll: true}
	conn := start(t, f)
	env := Envelope{From: "me@example.com", Recipients: []string{"a@example.com"}}
	err := SendOn(conn, "localhost", "me@example.com", "pw", env, []byte("x\r\n"), false)
	if err == nil || !strings.Contains(err.Error(), "RCPT TO a@example.com") {
		t.Errorf("err = %v", err)
	}
	if f.data != "" {
		t.Error("message was sent despite rejected recipient")
	}
}

// A rejected DATA means no body follows; the trace must not swallow QUIT.
func TestTraceResetsAfterRejectedData(t *testing.T) {
	var out strings.Builder
	tc := &traceConn{w: &out}
	tc.wbuf = tc.emit("C: ", []byte("DATA\r\n"), true)
	tc.rbuf = tc.emit("S: ", []byte("554 no\r\n"), false)
	tc.wbuf = tc.emit("C: ", []byte("QUIT\r\n"), true)
	if !strings.Contains(out.String(), "C: QUIT") {
		t.Errorf("QUIT swallowed:\n%s", out.String())
	}
}

func TestTraceRedactsAuthAndBody(t *testing.T) {
	var out strings.Builder
	tc := &traceConn{w: &out}
	tc.wbuf = tc.emit("C: ", []byte("EHLO me\r\nAUTH PLAIN c2VjcmV0\r\nDATA\r\nSubject: x\r\n\r\nbody\r\n.\r\nQUIT\r\n"), true)
	got := out.String()
	if strings.Contains(got, "c2VjcmV0") || !strings.Contains(got, "AUTH PLAIN <redacted>") {
		t.Errorf("credentials not redacted:\n%s", got)
	}
	if strings.Contains(got, "Subject: x") || strings.Contains(got, "C: body") || !strings.Contains(got, "<3 body lines>") {
		t.Errorf("body not summarized:\n%s", got)
	}
	if !strings.Contains(got, "C: QUIT") {
		t.Errorf("QUIT after body missing:\n%s", got)
	}
}
