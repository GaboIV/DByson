package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adminbase/adminbase/internal/policy"
	"golang.org/x/crypto/ssh"
)

func fixtureSSH(t *testing.T) Hop {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	hostKey, _ := ssh.NewSignerFromKey(key)
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == "tester" && string(p) == "test-password" {
			return nil, nil
		}
		return nil, fmt.Errorf("bad password")
	}, PublicKeyCallback: func(_ ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(hostKey)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sockets []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range sockets {
			c.Close()
		}
	})
	go func() {
		for {
			raw, e := ln.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			sockets = append(sockets, raw)
			mu.Unlock()
			go func() {
				defer raw.Close()
				_, channels, reqs, e := ssh.NewServerConn(raw, config)
				if e != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range channels {
					if ch.ChannelType() != "direct-tcpip" {
						ch.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					var d struct {
						Host       string
						Port       uint32
						Origin     string
						OriginPort uint32
					}
					if ssh.Unmarshal(ch.ExtraData(), &d) != nil {
						ch.Reject(ssh.ConnectionFailed, "bad address")
						continue
					}
					dst, e := net.DialTimeout("tcp", net.JoinHostPort(d.Host, strconv.Itoa(int(d.Port))), time.Second)
					if e != nil {
						ch.Reject(ssh.ConnectionFailed, "not reachable")
						continue
					}
					stream, rs, e := ch.Accept()
					if e != nil {
						dst.Close()
						continue
					}
					go ssh.DiscardRequests(rs)
					go func() {
						defer dst.Close()
						defer stream.Close()
						go func() { io.Copy(dst, stream); dst.Close() }()
						io.Copy(stream, dst)
					}()
				}
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(port)
	return Hop{Host: "127.0.0.1", Port: n, User: "tester", Auth: "password", Password: "test-password", Fingerprint: ssh.FingerprintSHA256(hostKey.PublicKey())}
}

func TestForwardAndJump(t *testing.T) {
	p, _ := policy.ParseAllowlist("")
	hop := fixtureSSH(t)
	jump := fixtureSSH(t)
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	for _, withJump := range []bool{false, true} {
		t.Run(fmt.Sprint("jump=", withJump), func(t *testing.T) {
			cfg := Config{Enabled: true, Hop: hop}
			if withJump {
				cfg.Jump = &jump
			}
			observation, e := ObserveVia(context.Background(), hop, cfg.Jump, p)
			if e != nil || observation.Fingerprint != hop.Fingerprint {
				t.Fatalf("observe: %+v %v", observation, e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, e := Dialer(cfg, p)(ctx, "tcp", echo.Addr().String())
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(time.Second))
			if _, e = conn.Write([]byte("through-ssh")); e != nil {
				t.Fatal(e)
			}
			buf := make([]byte, 11)
			if _, e = io.ReadFull(conn, buf); e != nil || string(buf) != "through-ssh" {
				t.Fatalf("echo %q %v", buf, e)
			}
			if e = conn.Close(); e != nil {
				t.Fatal(e)
			}
			if _, e = conn.Write([]byte("closed")); e == nil {
				t.Fatal("closed forwarding still usable")
			}
		})
	}
	bad := Config{Enabled: true, Hop: hop}
	bad.Fingerprint = jump.Fingerprint
	if _, e := Dialer(bad, p)(context.Background(), "tcp", echo.Addr().String()); e == nil || !strings.Contains(e.Error(), "huella") {
		t.Fatalf("host key mismatch accepted: %v", e)
	}
	if _, e := Dialer(Config{Enabled: true, Hop: hop}, p)(context.Background(), "tcp", "169.254.169.254:3306"); e == nil {
		t.Fatal("metadata forwarding allowed")
	}
	restricted, _ := policy.ParseAllowlist("127.0.0.1")
	if _, e := Dialer(Config{Enabled: true, Hop: hop}, restricted)(context.Background(), "tcp", "remote.internal:3306"); e == nil {
		t.Fatal("remote DNS bypassed allowlist")
	}
}

func TestCancellationDuringHandshake(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := ln.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(port)
	p, _ := policy.ParseAllowlist("")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Observe(ctx, Hop{Host: "127.0.0.1", Port: n}, p)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("handshake did not cancel: %v", err)
	}
	(<-accepted).Close()
}

func TestKeysAndDetachedReferences(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKey(key, "test fixture only")
	open := string(pem.EncodeToMemory(block))
	encrypted, _ := ssh.MarshalPrivateKeyWithPassphrase(key, "test fixture only", []byte("a-passphrase"))
	for _, h := range []Hop{{PrivateKey: open}, {PrivateKey: string(pem.EncodeToMemory(encrypted)), Passphrase: "a-passphrase"}} {
		if _, e := signer(h); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := signer(Hop{PrivateKey: string(pem.EncodeToMemory(encrypted)), Passphrase: "wrong"}); e == nil {
		t.Fatal("bad passphrase accepted")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "id_test"), []byte(open), 0600)
	t.Setenv("ADMINBASE_SSH_KEY_DIR", dir)
	if _, e := signer(Hop{KeyFile: "id_test"}); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte(open), 0600)
	os.Symlink(outside, filepath.Join(dir, "escape"))
	for _, path := range []string{outside, "../outside", "escape"} {
		if _, e := signer(Hop{KeyFile: path}); e == nil {
			t.Fatal("escaped key directory", path)
		}
	}
	cfg := Config{Enabled: true, Hop: Hop{PrivateKey: open, Passphrase: "secret", KeyLabel: "test.ppk", KeyFile: "team/test.ppk"}, Jump: &Hop{PrivateKey: open, Passphrase: "secret"}}
	detached := cfg.Detached()
	if detached.PrivateKey != "" || detached.Jump.PrivateKey != "" || detached.Passphrase != "" || detached.KeyFile != cfg.KeyFile || cfg.Jump.PrivateKey == "" {
		t.Fatal("invalid detached profile")
	}
	for _, file := range []string{"v2.ppk", "v3.ppk", "v2-encrypted.ppk", "v3-encrypted.ppk"} {
		data, e := os.ReadFile(filepath.Join("testdata", file))
		if e != nil {
			t.Fatal(e)
		}
		h := Hop{PrivateKey: string(data)}
		if strings.Contains(file, "encrypted") {
			h.Passphrase = "dbyson-fixture-passphrase"
		}
		if _, e = signer(h); e != nil {
			t.Fatalf("%s: %v", file, e)
		}
		if h.Passphrase != "" {
			h.Passphrase = "wrong"
			if _, e = signer(h); e == nil {
				t.Fatal("wrong PPK passphrase accepted")
			}
		}
	}
}

func TestKeyQueueRespectsCancellation(t *testing.T) {
	keySlots <- struct{}{}
	keySlots <- struct{}{}
	defer func() { <-keySlots; <-keySlots }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := signerContext(ctx, Hop{}); err != context.DeadlineExceeded || time.Since(start) > time.Second {
		t.Fatalf("key queue did not cancel: %v", err)
	}
}
