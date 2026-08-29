package imapc

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// newTestClient wires a Client to an in-process fake IMAP server driven by
// `handle`, which reads one command line at a time from the server side
// and writes back whatever raw response it wants.
func newTestClient(t *testing.T, handle func(cmd string, w *bufio.Writer, r *bufio.Reader)) *Client {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	go func() {
		r := bufio.NewReader(serverSide)
		w := bufio.NewWriter(serverSide)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			handle(strings.TrimRight(line, "\r\n"), w, r)
			w.Flush()
		}
	}()
	c := &Conn{r: bufio.NewReaderSize(clientSide, 8192), w: clientSide}
	return &Client{transport: c}
}

func tag(cmd string) string {
	i := strings.IndexByte(cmd, ' ')
	if i == -1 {
		return cmd
	}
	return cmd[:i]
}

func TestClientList(t *testing.T) {
	cl := newTestClient(t, func(cmd string, w *bufio.Writer, r *bufio.Reader) {
		switch {
		case strings.Contains(cmd, "LIST"):
			w.WriteString("* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n")
			w.WriteString("* LIST (\\HasNoChildren) \"/\" \"accountant\"\r\n")
			w.WriteString(tag(cmd) + " OK LIST completed\r\n")
		}
	})
	boxes, err := cl.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 2 || boxes[0].Name != "INBOX" || boxes[1].Name != "accountant" {
		t.Errorf("got %+v", boxes)
	}
}

func TestClientUIDSearch(t *testing.T) {
	cl := newTestClient(t, func(cmd string, w *bufio.Writer, r *bufio.Reader) {
		switch {
		case strings.Contains(cmd, "SEARCH"):
			w.WriteString("* SEARCH 4821 4900\r\n")
			w.WriteString(tag(cmd) + " OK SEARCH completed\r\n")
		}
	})
	uids, err := cl.UIDSearch(`X-GM-LABELS "accountant"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(uids) != 2 || uids[0] != 4821 || uids[1] != 4900 {
		t.Errorf("got %v", uids)
	}
}

func TestClientUIDFetch(t *testing.T) {
	cl := newTestClient(t, func(cmd string, w *bufio.Writer, r *bufio.Reader) {
		switch {
		case strings.Contains(cmd, "FETCH"):
			body := "From: a@x.com\r\nSubject: Hi\r\n"
			w.WriteString("* 1 FETCH (UID 42 FLAGS (\\Seen) X-GM-LABELS (accountant) RFC822.SIZE 31 BODY[HEADER.FIELDS (FROM SUBJECT)] {" + itoa(len(body)) + "}\r\n" + body + ")\r\n")
			w.WriteString(tag(cmd) + " OK FETCH completed\r\n")
		}
	})
	frs, err := cl.UIDFetch([]uint32{42}, "UID FLAGS X-GM-LABELS RFC822.SIZE BODY.PEEK[HEADER.FIELDS (FROM SUBJECT)]")
	if err != nil {
		t.Fatal(err)
	}
	if len(frs) != 1 {
		t.Fatalf("got %d results", len(frs))
	}
	fr := frs[0]
	if fr.UID != 42 || fr.Size != 31 || len(fr.Labels) != 1 || fr.Labels[0] != "accountant" {
		t.Errorf("got %+v", fr)
	}
	if !strings.Contains(string(fr.Headers), "Subject: Hi") {
		t.Errorf("headers = %q", fr.Headers)
	}
}

func TestClientAppend(t *testing.T) {
	var gotLiteral string
	cl := newTestClient(t, func(cmd string, w *bufio.Writer, r *bufio.Reader) {
		if strings.HasPrefix(cmd, "a") && strings.Contains(cmd, "APPEND") {
			w.WriteString("+ go ahead\r\n")
			w.Flush()
			i := strings.LastIndexByte(cmd, '{')
			j := strings.LastIndexByte(cmd, '}')
			n := atoiMust(cmd[i+1 : j])
			buf := make([]byte, n+2) // + trailing CRLF the client sends
			io.ReadFull(r, buf)
			gotLiteral = string(buf[:n])
			w.WriteString(tag(cmd) + " OK APPEND completed\r\n")
		}
	})
	err := cl.Append("INBOX", nil, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), []byte("raw message body"))
	if err != nil {
		t.Fatal(err)
	}
	if gotLiteral != "raw message body" {
		t.Errorf("server got literal %q", gotLiteral)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func atoiMust(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestClientStoreFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		add  bool
		want string
	}{
		{"add", true, `UID STORE 42,43 +FLAGS (\Seen)`},
		{"remove", false, `UID STORE 42,43 -FLAGS (\Seen)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			cl := newTestClient(t, func(cmd string, w *bufio.Writer, r *bufio.Reader) {
				if strings.Contains(cmd, "STORE") {
					got = cmd[strings.IndexByte(cmd, ' ')+1:]
					w.WriteString(tag(cmd) + " OK STORE completed\r\n")
				}
			})
			if err := cl.StoreFlags([]uint32{42, 43}, tc.add, []string{`\Seen`}); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientStoreFlagsNoop(t *testing.T) {
	cl := newTestClient(t, func(cmd string, w *bufio.Writer, r *bufio.Reader) {
		t.Errorf("unexpected command %q", cmd)
	})
	if err := cl.StoreFlags(nil, true, []string{`\Seen`}); err != nil {
		t.Fatal(err)
	}
	if err := cl.StoreFlags([]uint32{1}, true, nil); err != nil {
		t.Fatal(err)
	}
}
