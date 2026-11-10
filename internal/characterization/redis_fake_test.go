package characterization

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeRedis speaks just enough of the Redis protocol for the history store:
// GET, SET, EXISTS and DEL, plus the handshake the client library opens with.
type fakeRedis struct {
	listener net.Listener
	record   func(format string, args ...any)

	mu      sync.Mutex
	data    map[string]string
	failSet bool
}

func newFakeRedis(t *testing.T, record func(format string, args ...any)) *fakeRedis {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for fake redis: %v", err)
	}
	r := &fakeRedis{listener: listener, record: record, data: map[string]string{}}
	t.Cleanup(func() { _ = listener.Close() })
	go r.serve()
	return r
}

func (r *fakeRedis) addr() string { return r.listener.Addr().String() }

func (r *fakeRedis) setFailSet(fail bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failSet = fail
}

func (r *fakeRedis) serve() {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return
		}
		go r.handle(conn)
	}
}

func (r *fakeRedis) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		args, err := readCommand(reader)
		if err != nil {
			return
		}
		if _, err := io.WriteString(conn, r.reply(args)); err != nil {
			return
		}
	}
}

func (r *fakeRedis) reply(args []string) string {
	if len(args) == 0 {
		return "-ERR empty command\r\n"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch strings.ToUpper(args[0]) {
	case "HELLO":
		// Refusing HELLO makes the client fall back to the older protocol.
		return "-ERR unknown command 'HELLO'\r\n"
	case "CLIENT":
		return "+OK\r\n"
	case "PING":
		return "+PONG\r\n"
	case "GET":
		value, ok := r.data[args[1]]
		if !ok {
			r.record("redis GET %s -> (nil)", args[1])
			return "$-1\r\n"
		}
		r.record("redis GET %s -> hit", args[1])
		return fmt.Sprintf("$%d\r\n%s\r\n", len(value), value)
	case "SET":
		r.record("redis SET %s %s\n%s", args[1], strings.Join(args[3:], " "), indentJSON(args[2]))
		if r.failSet {
			return "-ERR fake redis refuses writes\r\n"
		}
		r.data[args[1]] = args[2]
		return "+OK\r\n"
	case "EXISTS":
		if _, ok := r.data[args[1]]; ok {
			r.record("redis EXISTS %s -> 1", args[1])
			return ":1\r\n"
		}
		r.record("redis EXISTS %s -> 0", args[1])
		return ":0\r\n"
	case "DEL":
		_, ok := r.data[args[1]]
		delete(r.data, args[1])
		r.record("redis DEL %s", args[1])
		if ok {
			return ":1\r\n"
		}
		return ":0\r\n"
	default:
		return "-ERR unknown command '" + args[0] + "'\r\n"
	}
}

func readCommand(reader *bufio.Reader) ([]string, error) {
	line, err := readLine(reader)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("expected array, got %q", line)
	}
	count, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, count)
	for range count {
		header, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(header, "$") {
			return nil, fmt.Errorf("expected bulk string, got %q", header)
		}
		size, err := strconv.Atoi(header[1:])
		if err != nil {
			return nil, err
		}
		payload := make([]byte, size+2)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
		args = append(args, string(payload[:size]))
	}
	return args, nil
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
