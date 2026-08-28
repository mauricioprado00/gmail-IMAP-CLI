package imapc

import (
	"bufio"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestParseParenListSimple(t *testing.T) {
	lits := [][]byte{}
	got, pos := parseParenList(`(FLAGS (\Seen \Answered) UID 42)`, 0, &lits)
	want := []interface{}{"FLAGS", []interface{}{`\Seen`, `\Answered`}, "UID", "42"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if pos != len(`(FLAGS (\Seen \Answered) UID 42)`) {
		t.Errorf("pos = %d", pos)
	}
}

func TestParseParenListQuotedAndNil(t *testing.T) {
	lits := [][]byte{}
	got, _ := parseParenList(`("a b" NIL "c\"d")`, 0, &lits)
	want := []interface{}{"a b", nil, `c"d`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseParenListWithLiteralSentinel(t *testing.T) {
	// readResponseLine would have replaced "{5}\r\n" with a single 0x00
	// byte and queued the literal bytes.
	lits := [][]byte{[]byte("hello")}
	line := "(BODY[] \x00)"
	got, _ := parseParenList(line, 0, &lits)
	want := []interface{}{"BODY[]", "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if len(lits) != 0 {
		t.Errorf("expected literal to be consumed, %d left", len(lits))
	}
}

func TestAsMapAndListToStrings(t *testing.T) {
	l := []interface{}{"UID", "42", "FLAGS", []interface{}{`\Seen`}}
	m := asMap(l)
	if m["UID"] != "42" {
		t.Errorf("UID = %v", m["UID"])
	}
	flags := listToStrings(m["FLAGS"].([]interface{}))
	if !reflect.DeepEqual(flags, []string{`\Seen`}) {
		t.Errorf("flags = %v", flags)
	}
}

// fakeServer wires a net.Pipe so Conn can be exercised with scripted
// server responses without real network access.
func fakeServer(t *testing.T, script string) (*Conn, net.Conn) {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	go func() {
		w := bufio.NewWriter(serverSide)
		w.WriteString(script)
		w.Flush()
	}()
	c := &Conn{r: bufio.NewReaderSize(clientSide, 4096), w: clientSide}
	return c, serverSide
}

func TestReadResponseLineWithLiteral(t *testing.T) {
	script := "* 1 FETCH (BODY[] {5}\r\nhello)\r\n" + "a001 OK done\r\n"
	c, srv := fakeServer(t, script)
	defer srv.Close()

	line, lits, err := c.readResponseLine()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "\x00") {
		t.Errorf("expected sentinel in line, got %q", line)
	}
	if len(lits) != 1 || string(lits[0]) != "hello" {
		t.Errorf("literals = %v", lits)
	}
}
