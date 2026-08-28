package imapd

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"gic/internal/broker"
	"gic/internal/imapc"
)

const (
	MaxConnectionsPerAccount = 2
	MaxConnectionsTotal      = 8
	IdleTTL                  = 60 * time.Second
	LeaseWaitTimeout         = 2 * time.Second
)

type commandClient interface {
	Cmd(string) (*imapc.Response, error)
	CmdLiteral(string, []byte) (*imapc.Response, error)
	Close() error
}

type connector func(broker.LeaseRequest) (commandClient, error)

type pooledClient struct {
	client   commandClient
	busy     bool
	lastUsed time.Time
}

type Pool struct {
	mu       sync.Mutex
	changed  *sync.Cond
	accounts map[string][]*pooledClient
	connect  connector
	now      func() time.Time
	closed   bool
}

func NewPool() *Pool {
	return newPool(func(req broker.LeaseRequest) (commandClient, error) {
		return imapc.ConnectDirect(req.Host, req.Port, req.User, req.Password, req.Debug)
	})
}

func newPool(connect connector) *Pool {
	p := &Pool{
		accounts: make(map[string][]*pooledClient),
		connect:  connect,
		now:      time.Now,
	}
	p.changed = sync.NewCond(&p.mu)
	return p
}

func accountKey(req broker.LeaseRequest) string {
	passwordHash := sha256.Sum256([]byte(req.Password))
	return fmt.Sprintf("%s:%d\x00%s\x00%x", req.Host, req.Port, req.User, passwordHash)
}

func (p *Pool) Acquire(req broker.LeaseRequest) (*pooledClient, string, error) {
	key := accountKey(req)
	deadline := time.Now().Add(LeaseWaitTimeout)
	p.mu.Lock()
	for {
		if p.closed {
			p.mu.Unlock()
			return nil, "", fmt.Errorf("IMAP connection pool is closed")
		}
		p.pruneLocked(p.now())
		for _, item := range p.accounts[key] {
			if !item.busy && item.client != nil {
				item.busy = true
				p.mu.Unlock()
				if _, err := item.client.Cmd("NOOP"); err == nil {
					return item, key, nil
				}
				_ = item.client.Close()
				p.mu.Lock()
				p.removeLocked(key, item)
				p.changed.Broadcast()
				continue
			}
		}
		if len(p.accounts[key]) < MaxConnectionsPerAccount && p.connectionCountLocked() < MaxConnectionsTotal {
			item := &pooledClient{busy: true}
			p.accounts[key] = append(p.accounts[key], item)
			p.mu.Unlock()

			client, err := p.connect(req)
			p.mu.Lock()
			if err != nil {
				p.removeLocked(key, item)
				p.changed.Broadcast()
				p.mu.Unlock()
				return nil, "", err
			}
			if p.closed {
				p.removeLocked(key, item)
				p.mu.Unlock()
				_ = client.Close()
				return nil, "", fmt.Errorf("IMAP connection pool closed while connecting")
			}
			item.client = client
			p.mu.Unlock()
			return item, key, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			p.mu.Unlock()
			return nil, "", fmt.Errorf("timed out waiting for an available IMAP connection")
		}
		timer := time.AfterFunc(remaining, func() {
			p.mu.Lock()
			p.changed.Broadcast()
			p.mu.Unlock()
		})
		p.changed.Wait()
		timer.Stop()
	}
}

func (p *Pool) Release(key string, item *pooledClient, healthy bool) {
	p.mu.Lock()
	client := item.client
	closeClient := !healthy || p.closed
	if healthy && !p.closed {
		item.busy = false
		item.lastUsed = p.now()
	} else {
		p.removeLocked(key, item)
	}
	p.changed.Broadcast()
	p.mu.Unlock()
	if closeClient {
		_ = client.Close()
	}
}

func (p *Pool) Prune() {
	p.mu.Lock()
	p.pruneLocked(p.now())
	p.changed.Broadcast()
	p.mu.Unlock()
}

func (p *Pool) Empty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.accounts) == 0
}

func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	var clients []commandClient
	for key, items := range p.accounts {
		for _, item := range items {
			if item.client != nil {
				clients = append(clients, item.client)
			}
		}
		delete(p.accounts, key)
	}
	p.changed.Broadcast()
	p.mu.Unlock()
	for _, client := range clients {
		_ = client.Close()
	}
}

func (p *Pool) connectionCountLocked() int {
	count := 0
	for _, items := range p.accounts {
		count += len(items)
	}
	return count
}

func (p *Pool) pruneLocked(now time.Time) {
	for key, items := range p.accounts {
		kept := items[:0]
		for _, item := range items {
			if !item.busy && item.client != nil && now.Sub(item.lastUsed) >= IdleTTL {
				_ = item.client.Close()
				continue
			}
			kept = append(kept, item)
		}
		if len(kept) == 0 {
			delete(p.accounts, key)
		} else {
			p.accounts[key] = kept
		}
	}
}

func (p *Pool) removeLocked(key string, target *pooledClient) {
	items := p.accounts[key]
	for i, item := range items {
		if item == target {
			items = append(items[:i], items[i+1:]...)
			break
		}
	}
	if len(items) == 0 {
		delete(p.accounts, key)
	} else {
		p.accounts[key] = items
	}
}
