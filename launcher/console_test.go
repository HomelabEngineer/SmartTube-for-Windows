package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsole(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	got := make(chan string, 10)
	go func() {
		c, _ := l.Accept()
		w := bufio.NewWriter(c)
		w.WriteString("Android Console: Authentication required\r\nAndroid Console: type 'auth <auth_token>' to authenticate\r\nOK\r\n")
		w.Flush()
		r := bufio.NewReader(c)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			got <- line
			if strings.Contains(line, "EV_SYN") {
				w.WriteString("KO: bad event type\r\n")
			} else {
				w.WriteString("OK\r\n")
			}
			w.Flush()
		}
	}()
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, ".emulator_console_auth_token"), []byte("tok123\n"), 0o644)
	c, err := dialConsole(port, []string{d})
	if err != nil {
		t.Fatal(err)
	}
	if s := <-got; s != "auth tok123" {
		t.Fatal(s)
	}
	if s := <-got; s != "event types" {
		t.Fatal(s)
	}
	if err := c.key(28, true); err != nil {
		t.Fatal(err)
	}
	<-got
	if s := <-got; s != "event send EV_KEY:28:1" {
		t.Fatal(s)
	}
	if err := c.key(28, false); err != nil {
		t.Fatal(err)
	}
	if s := <-got; s != "event send EV_KEY:28:0" {
		t.Fatal(s)
	}
}
