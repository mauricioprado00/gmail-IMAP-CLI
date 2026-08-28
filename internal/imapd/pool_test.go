package imapd

import (
	"encoding/gob"
	"net"
	"sync"
	"testing"
	"time"

	"gic/internal/broker"
	"gic/internal/imapc"
)

type fakeClient struct {
	mu       sync.Mutex
	commands []string
	closed   bool
}

func (f *fakeClient) Cmd(command string) (*imapc.Response, error) {
	f.mu.Lock()
	f.commands = append(f.commands, command)
	f.mu.Unlock()
	return &imapc.Response{Status: "OK"}, nil
}

func (f *fakeClient) CmdLiteral(command string, _ []byte) (*imapc.Response, error) {
	return f.Cmd(command)
}

func (f *fakeClient) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func TestPoolReusesConnectionAndExpiresIt(t *testing.T) {
	now := time.Unix(1_000, 0)
	created := 0
	fake := &fakeClient{}
	pool := newPool(func(broker.LeaseRequest) (commandClient, error) {
		created++
		return fake, nil
	})
	pool.now = func() time.Time { return now }
	req := broker.LeaseRequest{Host: "imap.example", Port: 993, User: "user", Password: "secret"}

	first, key, err := pool.Acquire(req)
	if err != nil {
		t.Fatal(err)
	}
	pool.Release(key, first, true)
	second, secondKey, err := pool.Acquire(req)
	if err != nil {
		t.Fatal(err)
	}
	if second != first || created != 1 {
		t.Fatalf("connection was not reused: same=%v created=%d", second == first, created)
	}
	pool.Release(secondKey, second, true)

	now = now.Add(IdleTTL)
	pool.Prune()
	if !pool.Empty() || !fake.closed {
		t.Fatal("idle connection was not closed and removed")
	}
}

func TestPoolLimitsConnectionsPerAccount(t *testing.T) {
	pool := newPool(func(broker.LeaseRequest) (commandClient, error) {
		return &fakeClient{}, nil
	})
	req := broker.LeaseRequest{Host: "imap.example", Port: 993, User: "user", Password: "secret"}
	first, key, err := pool.Acquire(req)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := pool.Acquire(req)
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan *pooledClient, 1)
	go func() {
		item, _, _ := pool.Acquire(req)
		acquired <- item
	}()
	select {
	case <-acquired:
		t.Fatal("third connection exceeded per-account limit")
	case <-time.After(20 * time.Millisecond):
	}

	pool.Release(key, first, true)
	select {
	case item := <-acquired:
		if item != first {
			t.Fatal("waiting lease did not reuse the released connection")
		}
	case <-time.After(time.Second):
		t.Fatal("waiting lease was not unblocked")
	}
	pool.Release(key, first, true)
	pool.Release(key, second, true)
	pool.Close()
}

func TestHandlerUnselectsBeforeReturningConnection(t *testing.T) {
	fake := &fakeClient{}
	pool := newPool(func(broker.LeaseRequest) (commandClient, error) {
		return fake, nil
	})
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		handle(serverConn, pool)
		close(done)
	}()

	enc := gob.NewEncoder(clientConn)
	dec := gob.NewDecoder(clientConn)
	if err := enc.Encode(broker.LeaseRequest{
		Host: "imap.example", Port: 993, User: "user", Password: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	var reply broker.Reply
	if err := dec.Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error != "" {
		t.Fatal(reply.Error)
	}
	if err := enc.Encode(broker.Request{Command: `SELECT "Other"`}); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if err := clientConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not release the connection")
	}

	fake.mu.Lock()
	if len(fake.commands) != 2 || fake.commands[0] != `SELECT "Other"` || fake.commands[1] != "UNSELECT" {
		fake.mu.Unlock()
		t.Fatalf("commands = %#v, want SELECT followed by UNSELECT", fake.commands)
	}
	fake.mu.Unlock()
	pool.Close()
}
