package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// emuConsole talks to the emulator's telnet console to inject key events.
// This is far lower latency than `adb shell input keyevent`.
type emuConsole struct {
	mu     sync.Mutex
	conn   net.Conn
	r      *bufio.Reader
	useSyn bool
}

func dialConsole(port int, tokenDirs []string) (*emuConsole, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)
	if err != nil {
		return nil, err
	}
	c := &emuConsole{conn: conn, r: bufio.NewReader(conn), useSyn: true}
	if _, err := c.readUntilOK(); err != nil {
		conn.Close()
		return nil, err
	}
	authed := false
	for _, d := range tokenDirs {
		b, err := os.ReadFile(filepath.Join(d, ".emulator_console_auth_token"))
		if err != nil {
			continue
		}
		tok := strings.TrimSpace(string(b))
		if tok == "" {
			continue
		}
		if resp, err := c.cmd("auth " + tok); err == nil && !strings.Contains(resp, "KO") {
			authed = true
			break
		}
	}
	// Verify we can actually issue commands (unauthenticated consoles answer KO).
	resp, err := c.cmd("event types")
	if err != nil || strings.Contains(resp, "KO") {
		conn.Close()
		return nil, fmt.Errorf("console not usable (authenticated=%v): %s %v", authed, strings.TrimSpace(resp), err)
	}
	return c, nil
}

func (c *emuConsole) readUntilOK() (string, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var sb strings.Builder
	for {
		line, err := c.r.ReadString('\n')
		sb.WriteString(line)
		t := strings.TrimSpace(line)
		if t == "OK" || strings.HasPrefix(t, "KO") {
			return sb.String(), nil
		}
		if err != nil {
			return sb.String(), err
		}
	}
}

func (c *emuConsole) cmd(s string) (string, error) {
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.conn.Write([]byte(s + "\r\n")); err != nil {
		return "", err
	}
	return c.readUntilOK()
}

// key sends a Linux input key code press (down=true) or release.
func (c *emuConsole) key(code int, down bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := 0
	if down {
		v = 1
	}
	ev := fmt.Sprintf("event send EV_KEY:%d:%d", code, v)
	if c.useSyn {
		resp, err := c.cmd(ev + " EV_SYN:0:0")
		if err != nil {
			return err
		}
		if !strings.Contains(resp, "KO") {
			return nil
		}
		c.useSyn = false
	}
	resp, err := c.cmd(ev)
	if err == nil && strings.Contains(resp, "KO") {
		err = fmt.Errorf("console: %s", strings.TrimSpace(resp))
	}
	return err
}

func (c *emuConsole) Close() { c.conn.Close() }
