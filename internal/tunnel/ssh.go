// Package tunnel supplies per-connection SSH forwarding without local listening ports.
package tunnel

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adminbase/adminbase/internal/policy"
	"github.com/kayrus/putty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type Hop struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Auth          string `json:"auth"`
	Fingerprint   string `json:"fingerprint"`
	Password      string `json:"password,omitempty"`
	PrivateKey    string `json:"privateKey,omitempty"`
	Passphrase    string `json:"passphrase,omitempty"`
	KeyFile       string `json:"keyFile,omitempty"`
	KeyLabel      string `json:"keyLabel,omitempty"`
	HasPassword   bool   `json:"hasPassword,omitempty"`
	HasPrivateKey bool   `json:"hasPrivateKey,omitempty"`
}

type Config struct {
	Enabled bool `json:"enabled"`
	Hop
	Jump *Hop `json:"jump,omitempty"`
}

func (h Hop) Redacted() Hop {
	h.HasPassword = h.Password != ""
	h.HasPrivateKey = h.PrivateKey != ""
	h.Password, h.PrivateKey, h.Passphrase = "", "", ""
	return h
}

// Detached retains only references; shared profiles never embed a private SSH key.
func (c Config) Detached() Config {
	c.PrivateKey, c.Passphrase = "", ""
	if c.Jump != nil {
		j := *c.Jump
		j.PrivateKey, j.Passphrase = "", ""
		c.Jump = &j
	}
	return c
}

func (c Config) Redacted() Config {
	c.Hop = c.Hop.Redacted()
	if c.Jump != nil {
		j := c.Jump.Redacted()
		c.Jump = &j
	}
	return c
}

func Merge(c, old Config) Config {
	merge := func(h *Hop, prev Hop) {
		newKey := h.PrivateKey != ""
		if h.Password == "" {
			h.Password = prev.Password
		}
		if h.PrivateKey == "" && h.KeyFile == prev.KeyFile && h.KeyLabel == prev.KeyLabel {
			h.PrivateKey = prev.PrivateKey
		}
		if !newKey && h.Passphrase == "" && h.KeyFile == prev.KeyFile && h.KeyLabel == prev.KeyLabel {
			h.Passphrase = prev.Passphrase
		}
		h.HasPassword, h.HasPrivateKey = false, false
	}
	merge(&c.Hop, old.Hop)
	if c.Jump != nil && old.Jump != nil {
		merge(c.Jump, *old.Jump)
	}
	return c
}

func ValidHost(host string) bool {
	return host != "" && len(host) <= 253 && !strings.ContainsAny(host, " /\\@?#\r\n\t\x00")
}

func (h *Hop) Normalize(requirePin bool) error {
	h.Host, h.User, h.Fingerprint = strings.TrimSpace(h.Host), strings.TrimSpace(h.User), strings.TrimSpace(h.Fingerprint)
	if h.Port == 0 {
		h.Port = 22
	}
	if h.Auth == "" {
		h.Auth = "key"
	}
	if !ValidHost(h.Host) || h.Port < 1 || h.Port > 65535 || h.User == "" || len(h.User) > 128 {
		return errors.New("SSH: indica host, puerto y usuario válidos")
	}
	if requirePin && (!strings.HasPrefix(h.Fingerprint, "SHA256:") || len(h.Fingerprint) != 50) {
		return errors.New("SSH: verifica y acepta la huella SHA256 del servidor")
	}
	switch h.Auth {
	case "password":
		if h.Password == "" && requirePin {
			return errors.New("SSH: falta la contraseña")
		}
	case "key":
		if !requirePin && h.PrivateKey == "" {
			return nil
		}
		if _, err := signer(*h); err != nil {
			return fmt.Errorf("SSH: clave privada o passphrase no válida: %w", err)
		}
	case "agent":
		if os.Getenv("SSH_AUTH_SOCK") == "" {
			return errors.New("SSH: el servidor DByson no tiene ssh-agent configurado")
		}
	default:
		return errors.New("SSH: método de autenticación no válido")
	}
	return nil
}

func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if err := c.Hop.Normalize(true); err != nil {
		return err
	}
	if c.Jump != nil {
		return c.Jump.Normalize(true)
	}
	return nil
}

var keySlots = make(chan struct{}, 2)

func signer(h Hop) (ssh.Signer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return signerContext(ctx, h)
}

func signerContext(ctx context.Context, h Hop) (result ssh.Signer, err error) {
	select {
	case keySlots <- struct{}{}:
		defer func() { <-keySlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Protect the service from malformed third-party key parsers and expensive KDF settings.
	defer func() {
		if recover() != nil {
			result = nil
			err = errors.New("formato de clave no válido")
		}
	}()
	key := []byte(strings.TrimSpace(h.PrivateKey))
	if len(key) == 0 && h.KeyFile != "" {
		dir := os.Getenv("ADMINBASE_SSH_KEY_DIR")
		if dir == "" {
			return nil, errors.New("adjunta la clave o configura ADMINBASE_SSH_KEY_DIR para usar archivos del servidor")
		}
		name := h.KeyFile
		if filepath.IsAbs(name) {
			name, err = filepath.Rel(dir, name)
			if err != nil {
				return nil, err
			}
		}
		root, e := os.OpenRoot(dir)
		if e != nil {
			return nil, errors.New("directorio de claves no disponible")
		}
		defer root.Close()
		f, e := root.Open(name)
		if e != nil {
			return nil, errors.New("clave no disponible dentro del directorio autorizado; adjúntala de nuevo")
		}
		defer f.Close()
		stat, e := f.Stat()
		if e != nil || !stat.Mode().IsRegular() {
			return nil, errors.New("se requiere un archivo de clave regular")
		}
		key, err = io.ReadAll(io.LimitReader(f, 128*1024+1))
		if err != nil {
			return nil, err
		}
	}
	if len(key) == 0 || len(key) > 128*1024 {
		return nil, errors.New("adjunta una clave privada de hasta 128 KiB")
	}
	if strings.HasPrefix(string(key), "PuTTY-User-Key-File-") {
		ppk, e := putty.New(key)
		if e != nil {
			return nil, e
		}
		if ppk.Argon2Memory > 128*1024 || ppk.Argon2Passes > 50 || ppk.Argon2Parallelism > 16 {
			return nil, errors.New("PPK: parámetros Argon2 demasiado costosos; reduce sus valores en PuTTYgen")
		}
		raw, e := ppk.ParseRawPrivateKey([]byte(h.Passphrase))
		if e != nil {
			return nil, errors.New("PPK: clave o passphrase incorrecta")
		}
		return ssh.NewSignerFromKey(raw)
	}
	if block, _ := pem.Decode(key); block != nil && block.Type == "OPENSSH PRIVATE KEY" && len(block.Bytes) > 15 {
		var envelope struct {
			Cipher  string
			KDF     string
			Options string
			Rest    []byte `ssh:"rest"`
		}
		if ssh.Unmarshal(block.Bytes[15:], &envelope) == nil && envelope.KDF == "bcrypt" {
			var opts struct {
				Salt   string
				Rounds uint32
			}
			if ssh.Unmarshal([]byte(envelope.Options), &opts) != nil || opts.Rounds > 256 {
				return nil, errors.New("OpenSSH: parámetros bcrypt no permitidos")
			}
		}
	}
	if h.Passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(key, []byte(h.Passphrase))
	}
	return ssh.ParsePrivateKey(key)
}

type Observation struct {
	Fingerprint string `json:"fingerprint"`
	Algorithm   string `json:"algorithm"`
}

var observed = errors.New("host key observed")

// Observe reads the offered key and stops before authentication. Trust is a separate user action.
func Observe(ctx context.Context, h Hop, p *policy.HostPolicy) (*Observation, error) {
	if !ValidHost(h.Host) || h.Port < 1 || h.Port > 65535 {
		return nil, errors.New("SSH: host o puerto no válido")
	}
	raw, err := p.DirectDialer(8*time.Second)(ctx, "tcp", net.JoinHostPort(h.Host, strconv.Itoa(h.Port)))
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	raw.SetDeadline(time.Now().Add(8 * time.Second))
	var result *Observation
	_, _, _, err = ssh.NewClientConn(raw, net.JoinHostPort(h.Host, strconv.Itoa(h.Port)), &ssh.ClientConfig{User: h.User, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		result = &Observation{ssh.FingerprintSHA256(key), key.Type()}
		return observed
	}})
	if result != nil {
		return result, nil
	}
	return nil, err
}

func ObserveVia(ctx context.Context, h Hop, jump *Hop, p *policy.HostPolicy) (*Observation, error) {
	if jump == nil {
		return Observe(ctx, h, p)
	}
	if !ValidHost(h.Host) || h.Port < 1 || h.Port > 65535 {
		return nil, errors.New("SSH: destino no válido")
	}
	if err := jump.Normalize(true); err != nil {
		return nil, err
	}
	if err := p.CheckRemote(h.Host); err != nil {
		return nil, err
	}
	raw, err := p.DirectDialer(8*time.Second)(ctx, "tcp", net.JoinHostPort(jump.Host, strconv.Itoa(jump.Port)))
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	raw.SetDeadline(time.Now().Add(10 * time.Second))
	cl, err := connect(ctx, raw, *jump)
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	via, err := cl.DialContext(ctx, "tcp", net.JoinHostPort(h.Host, strconv.Itoa(h.Port)))
	if err != nil {
		return nil, err
	}
	defer via.Close()
	var observation *Observation
	_, _, _, err = ssh.NewClientConn(via, net.JoinHostPort(h.Host, strconv.Itoa(h.Port)), &ssh.ClientConfig{User: h.User, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		observation = &Observation{ssh.FingerprintSHA256(key), key.Type()}
		return observed
	}})
	if observation != nil {
		return observation, nil
	}
	return nil, err
}

func connect(ctx context.Context, raw net.Conn, h Hop) (*ssh.Client, error) {
	var auth ssh.AuthMethod
	var agentConn net.Conn
	switch h.Auth {
	case "password":
		auth = ssh.Password(h.Password)
	case "agent":
		var err error
		agentConn, err = (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", os.Getenv("SSH_AUTH_SOCK"))
		if err != nil {
			return nil, err
		}
		defer agentConn.Close()
		auth = ssh.PublicKeysCallback(agent.NewClient(agentConn).Signers)
	default:
		s, err := signerContext(ctx, h)
		if err != nil {
			return nil, err
		}
		auth = ssh.PublicKeys(s)
	}
	cfg := &ssh.ClientConfig{User: h.User, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if ssh.FingerprintSHA256(key) != h.Fingerprint {
			return errors.New("SSH: la huella del servidor cambió; conexión bloqueada. Verifica su identidad antes de actualizarla")
		}
		return nil
	}}
	cc, ch, req, err := ssh.NewClientConn(raw, net.JoinHostPort(h.Host, strconv.Itoa(h.Port)), cfg)
	if err != nil {
		return nil, err
	}
	return ssh.NewClient(cc, ch, req), nil
}

// Dialer creates one SSH chain for each physical MySQL connection. Closing it releases all hops.
func Dialer(c Config, p *policy.HostPolicy) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if err = p.CheckRemote(host); err != nil {
			return nil, err
		}
		first := c.Hop
		if c.Jump != nil {
			first = *c.Jump
			if err = p.CheckRemote(c.Host); err != nil {
				return nil, err
			}
		}
		raw, err := p.DirectDialer(10*time.Second)(ctx, "tcp", net.JoinHostPort(first.Host, strconv.Itoa(first.Port)))
		if err != nil {
			return nil, fmt.Errorf("SSH: %w", err)
		}
		stop := context.AfterFunc(ctx, func() { raw.Close() })
		defer stop()
		deadline := time.Now().Add(12 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		raw.SetDeadline(deadline)
		clients := []*ssh.Client{}
		failed := true
		defer func() {
			if failed {
				raw.Close()
				for _, cl := range clients {
					cl.Close()
				}
			}
		}()
		cl, err := connect(ctx, raw, first)
		if err != nil {
			return nil, fmt.Errorf("SSH: %w", err)
		}
		clients = append(clients, cl)
		if c.Jump != nil {
			via, err := cl.DialContext(ctx, "tcp", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
			if err != nil {
				return nil, fmt.Errorf("bastión: %w", err)
			}
			cl, err = connect(ctx, via, c.Hop)
			if err != nil {
				via.Close()
				return nil, fmt.Errorf("SSH destino: %w", err)
			}
			clients = append(clients, cl)
		}
		forward, err := cl.DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("SSH → MySQL: %w", err)
		}
		if ctx.Err() != nil {
			forward.Close()
			return nil, ctx.Err()
		}
		raw.SetDeadline(time.Time{})
		conn := &forwardConn{Conn: forward, raw: raw, clients: clients, done: make(chan struct{})}
		failed = false
		go conn.keepAlive()
		return conn, nil
	}
}

type forwardConn struct {
	net.Conn
	raw     net.Conn
	clients []*ssh.Client
	once    sync.Once
	done    chan struct{}
}

func (c *forwardConn) Close() error {
	c.once.Do(func() {
		close(c.done)
		c.raw.Close()
		for _, cl := range c.clients {
			cl.Close()
		}
		c.Conn.Close()
	})
	return nil
}
func (c *forwardConn) SetDeadline(t time.Time) error      { return c.raw.SetDeadline(t) }
func (c *forwardConn) SetReadDeadline(t time.Time) error  { return c.raw.SetReadDeadline(t) }
func (c *forwardConn) SetWriteDeadline(t time.Time) error { return c.raw.SetWriteDeadline(t) }
func (c *forwardConn) keepAlive() {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			timeout := time.AfterFunc(8*time.Second, func() { c.Close() })
			_, _, err := c.clients[len(c.clients)-1].SendRequest("keepalive@openssh.com", true, nil)
			timeout.Stop()
			if err != nil {
				c.Close()
				return
			}
		}
	}
}
