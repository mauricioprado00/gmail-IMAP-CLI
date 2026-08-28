package imapc

import (
	"encoding/gob"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"gic/internal/broker"
)

type brokerTransport struct {
	conn net.Conn
	enc  *gob.Encoder
	dec  *gob.Decoder
	mu   sync.Mutex
}

func connectBroker(socket, host string, port int, user, pass string, debug bool) (*Client, error) {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to gic daemon: %w", err)
	}
	t := &brokerTransport{conn: conn, enc: gob.NewEncoder(conn), dec: gob.NewDecoder(conn)}
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		conn.Close()
		return nil, err
	}
	if err := t.enc.Encode(broker.LeaseRequest{
		Host: host, Port: port, User: user, Password: pass, Debug: debug,
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("requesting IMAP connection lease: %w", err)
	}
	var reply broker.Reply
	if err := t.dec.Decode(&reply); err != nil {
		conn.Close()
		return nil, fmt.Errorf("receiving IMAP connection lease: %w", err)
	}
	if reply.Error != "" {
		conn.Close()
		return nil, fmt.Errorf("%s", reply.Error)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	return &Client{transport: t, pooled: true}, nil
}

func (t *brokerTransport) Cmd(command string) (*Response, error) {
	return t.exchange(broker.Request{Command: command})
}

func (t *brokerTransport) CmdLiteral(prefix string, literal []byte) (*Response, error) {
	return t.exchange(broker.Request{Command: prefix, Literal: literal, HasLiteral: true})
}

func (t *brokerTransport) exchange(req broker.Request) (*Response, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.enc.Encode(req); err != nil {
		return nil, fmt.Errorf("sending command to gic daemon: %w", err)
	}
	var reply broker.Reply
	if err := t.dec.Decode(&reply); err != nil {
		return nil, fmt.Errorf("receiving command from gic daemon: %w", err)
	}
	var resp *Response
	if reply.Response != nil {
		resp = &Response{Status: reply.Response.Status, Text: reply.Response.Text}
		for _, line := range reply.Response.Untagged {
			resp.Untagged = append(resp.Untagged, Line{Text: line.Text, Literals: line.Literals})
		}
	}
	if reply.Error != "" {
		return resp, errors.New(reply.Error)
	}
	return resp, nil
}

func (t *brokerTransport) Close() error {
	return t.conn.Close()
}
