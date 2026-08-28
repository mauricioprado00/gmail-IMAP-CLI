package imapd

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"gic/internal/broker"
	"gic/internal/imapc"

	"golang.org/x/sys/unix"
)

func Serve(socket string) error {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(filepath.Dir(socket))
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("daemon runtime directory must be a real directory owned by the current user")
	}
	if err := os.Chmod(filepath.Dir(socket), 0o700); err != nil {
		return err
	}

	lock, err := os.OpenFile(socket+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)

	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		listener.Close()
		os.Remove(socket)
		return err
	}

	pool := NewPool()
	var handlers sync.WaitGroup
	defer func() {
		listener.Close()
		handlers.Wait()
		pool.Close()
		os.Remove(socket)
	}()
	started := time.Now()

	unixListener := listener.(*net.UnixListener)
	for {
		if err := unixListener.SetDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		conn, err := unixListener.AcceptUnix()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				pool.Prune()
				if pool.Empty() && time.Since(started) >= IdleTTL {
					return nil
				}
				continue
			}
			return err
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			handle(conn, pool)
		}()
	}
}

func handle(conn net.Conn, pool *Pool) {
	defer conn.Close()
	dec := gob.NewDecoder(conn)
	enc := gob.NewEncoder(conn)

	var leaseReq broker.LeaseRequest
	if err := dec.Decode(&leaseReq); err != nil {
		return
	}
	item, key, err := pool.Acquire(leaseReq)
	if err != nil {
		_ = enc.Encode(broker.Reply{Error: fmt.Sprintf("connecting to %s: %v", leaseReq.User, err)})
		return
	}
	selected := false
	healthy := true
	defer func() {
		if selected && healthy {
			if _, err := item.client.Cmd("UNSELECT"); err != nil {
				healthy = false
			}
		}
		pool.Release(key, item, healthy)
	}()

	if err := enc.Encode(broker.Reply{}); err != nil {
		return
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		var req broker.Request
		if err := dec.Decode(&req); err != nil {
			if !errors.Is(err, io.EOF) {
				healthy = false
			}
			return
		}

		var resp *imapc.Response
		if req.HasLiteral {
			resp, err = item.client.CmdLiteral(req.Command, req.Literal)
		} else {
			resp, err = item.client.Cmd(req.Command)
		}
		upper := strings.ToUpper(strings.TrimSpace(req.Command))
		if strings.HasPrefix(upper, "SELECT ") || strings.HasPrefix(upper, "EXAMINE ") {
			selected = err == nil
		} else if err == nil {
			if upper == "UNSELECT" || upper == "CLOSE" {
				selected = false
			}
		}
		if err != nil && (resp == nil || req.HasLiteral) {
			healthy = false
		}
		reply := broker.Reply{}
		if resp != nil {
			reply.Response = wireResponse(resp)
		}
		if err != nil {
			reply.Error = err.Error()
		}
		if err := enc.Encode(reply); err != nil {
			healthy = false
			return
		}
	}
}

func wireResponse(resp *imapc.Response) *broker.Response {
	out := &broker.Response{Status: resp.Status, Text: resp.Text}
	for _, line := range resp.Untagged {
		out.Untagged = append(out.Untagged, broker.Line{Text: line.Text, Literals: line.Literals})
	}
	return out
}

func Start(socket string) error {
	if Ping(socket) == nil {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()
	proc, err := os.StartProcess(exe, []string{exe, "daemon", "serve", "--socket", socket}, &os.ProcAttr{
		Files: []*os.File{devNull, devNull, devNull},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	if err != nil {
		return err
	}
	_ = proc.Release()
	for i := 0; i < 100; i++ {
		if Ping(socket) == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("gic daemon did not start within one second")
}

func Ping(socket string) error {
	conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
	if err != nil {
		return err
	}
	return conn.Close()
}
