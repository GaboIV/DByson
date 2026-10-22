package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/adminbase/adminbase/internal/drivers/mysql"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const profileFormat = "dbyson-connections"

var transferSlots = make(chan struct{}, 2)

type profilePackage struct {
	Format    string      `json:"format"`
	Version   int         `json:"version"`
	Encrypted bool        `json:"encrypted"`
	Profiles  []connInput `json:"profiles,omitempty"`
	Salt      string      `json:"salt,omitempty"`
	Nonce     string      `json:"nonce,omitempty"`
	Data      string      `json:"data,omitempty"`
}

func transferKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
}
func strictDecode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("contenido adicional no permitido")
	}
	return nil
}
func claimTransfer(w http.ResponseWriter) bool {
	select {
	case transferSlots <- struct{}{}:
		return true
	default:
		writeError(w, http.StatusTooManyRequests, "Hay otra transferencia en proceso. Reintenta en unos segundos")
		return false
	}
}

func (s *Server) exportProfiles(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs            []string `json:"ids"`
		IncludeSecrets bool     `json:"includeSecrets"`
		Passphrase     string   `json:"passphrase"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.IDs) < 1 || len(in.IDs) > 25 {
		writeError(w, 400, "Selecciona entre 1 y 25 conexiones")
		return
	}
	if in.IncludeSecrets && (len(in.Passphrase) < 12 || len(in.Passphrase) > 1024) {
		writeError(w, 400, "Usa una frase de al menos 12 caracteres para cifrar las credenciales")
		return
	}
	profiles := []connInput{}
	for _, id := range in.IDs {
		rec, err := scanConn(s.Store.DB.QueryRowContext(r.Context(), `SELECT `+connColumns+` FROM connections WHERE id=? AND owner_id=?`, id, sessionFrom(r).UserID))
		if err != nil {
			s.fail(w, err)
			return
		}
		sec, err := s.openSecret(rec)
		if err != nil {
			s.fail(w, err)
			return
		}
		ssh := sec.SSH.Redacted()
		p := connInput{Name: rec.Name, Env: rec.Env, Host: rec.Host, Port: rec.Port, Username: rec.Username, Database: rec.Database, TLSMode: rec.TLSMode, ReadOnly: rec.ReadOnly, SSH: &ssh}
		if in.IncludeSecrets {
			p.Password = &sec.Password
			p.CACert = &sec.CACert
			p.ClientCert = &sec.ClientCert
			p.ClientKey = &sec.ClientKey
			c := sec.SSH.Detached()
			p.SSH = &c
		} else if sec.CACert != "" {
			p.CACert = &sec.CACert
		}
		profiles = append(profiles, p)
	}
	pkg := profilePackage{Format: profileFormat, Version: 1, Profiles: profiles}
	if in.IncludeSecrets {
		if !claimTransfer(w) {
			return
		}
		defer func() { <-transferSlots }()
		plain, err := json.Marshal(profiles)
		if err != nil || len(plain) > 500*1024 {
			writeError(w, 400, "El paquete excede 500 KiB. Exporta menos conexiones")
			return
		}
		salt := make([]byte, 16)
		nonce := make([]byte, chacha20poly1305.NonceSizeX)
		if _, err = rand.Read(salt); err != nil {
			s.fail(w, err)
			return
		}
		if _, err = rand.Read(nonce); err != nil {
			s.fail(w, err)
			return
		}
		key := transferKey(in.Passphrase, salt)
		defer clear(key)
		aead, _ := chacha20poly1305.NewX(key)
		pkg = profilePackage{Format: profileFormat, Version: 1, Encrypted: true, Salt: base64.StdEncoding.EncodeToString(salt), Nonce: base64.StdEncoding.EncodeToString(nonce), Data: base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, plain, []byte(profileFormat+":1")))}
	}
	file, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(file) > 750*1024 {
		writeError(w, 400, "El paquete es demasiado grande")
		return
	}
	writeJSON(w, 200, map[string]any{"file": string(file), "filename": "dbyson-conexiones.dbyson", "encrypted": pkg.Encrypted})
}

func (s *Server) importProfiles(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File       string `json:"file"`
		Passphrase string `json:"passphrase"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.File) > 750*1024 || len(in.Passphrase) > 1024 {
		writeError(w, 400, "Paquete demasiado grande")
		return
	}
	var pkg profilePackage
	if err := strictDecode([]byte(in.File), &pkg); err != nil || pkg.Format != profileFormat || pkg.Version != 1 {
		writeError(w, 400, "Formato de conexiones no compatible")
		return
	}
	profiles := pkg.Profiles
	if pkg.Encrypted {
		salt, e1 := base64.StdEncoding.DecodeString(pkg.Salt)
		nonce, e2 := base64.StdEncoding.DecodeString(pkg.Nonce)
		data, e3 := base64.StdEncoding.DecodeString(pkg.Data)
		if e1 != nil || e2 != nil || e3 != nil || len(salt) != 16 || len(nonce) != 24 || len(data) > 510*1024 || len(pkg.Profiles) > 0 {
			writeError(w, 400, "Paquete cifrado no válido")
			return
		}
		if !claimTransfer(w) {
			return
		}
		defer func() { <-transferSlots }()
		key := transferKey(in.Passphrase, salt)
		defer clear(key)
		aead, _ := chacha20poly1305.NewX(key)
		plain, err := aead.Open(nil, nonce, data, []byte(profileFormat+":1"))
		if err != nil {
			writeError(w, 400, "Frase incorrecta o archivo modificado")
			return
		}
		if strictDecode(plain, &profiles) != nil {
			writeError(w, 400, "Perfiles no válidos")
			return
		}
	} else if pkg.Data != "" || pkg.Salt != "" || pkg.Nonce != "" {
		writeError(w, 400, "Paquete no válido")
		return
	}
	if len(profiles) < 1 || len(profiles) > 25 {
		writeError(w, 400, "El paquete debe contener entre 1 y 25 conexiones")
		return
	}
	// A transaction ensures malformed packages cannot leave a partial import.
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer tx.Rollback()
	out := []connView{}
	for _, p := range profiles {
		if msg := p.validate(); msg != "" {
			writeError(w, 400, msg)
			return
		}
		sec := secretBlob{Password: str(p.Password), CACert: str(p.CACert), ClientCert: str(p.ClientCert), ClientKey: str(p.ClientKey)}
		if p.SSH != nil {
			sec.SSH = *p.SSH
			if sec.SSH.PrivateKey != "" || sec.SSH.Passphrase != "" || (sec.SSH.Jump != nil && (sec.SSH.Jump.PrivateKey != "" || sec.SSH.Jump.Passphrase != "")) {
				writeError(w, 400, "Las claves SSH y passphrases se adjuntan por separado")
				return
			}
			if sec.SSH.Enabled {
				if err := sec.SSH.Hop.Normalize(false); err != nil {
					writeError(w, 400, err.Error())
					return
				}
				if sec.SSH.Jump != nil {
					if err := sec.SSH.Jump.Normalize(false); err != nil {
						writeError(w, 400, err.Error())
						return
					}
				}
			}
		}
		if !pkg.Encrypted && (sec.Password != "" || sec.ClientKey != "" || sec.SSH.Password != "" || sec.SSH.PrivateKey != "" || sec.SSH.Passphrase != "" || (sec.SSH.Jump != nil && (sec.SSH.Jump.Password != "" || sec.SSH.Jump.PrivateKey != "" || sec.SSH.Jump.Passphrase != ""))) {
			writeError(w, 400, "Las credenciales solo se admiten en paquetes cifrados")
			return
		}
		if _, err := mysql.BuildTLS(p.driverConfig(sec)); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		nameRunes := []rune(p.Name)
		if len(nameRunes) > 88 {
			nameRunes = nameRunes[:88]
		}
		p.Name = string(nameRunes)
		id := newID()
		wrapped, sealed, err := s.sealSecret(id, sec)
		if err != nil {
			s.fail(w, err)
			return
		}
		now := time.Now().Unix()
		ro := 0
		if p.ReadOnly {
			ro = 1
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO connections (`+connColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, sessionFrom(r).UserID, p.Name+" (importada)", p.Env, p.Host, p.Port, p.Username, p.Database, p.TLSMode, ro, wrapped, sealed, now, now)
		if err != nil {
			s.fail(w, err)
			return
		}
		out = append(out, view(&connRecord{ID: id, Name: p.Name + " (importada)", Env: p.Env, Host: p.Host, Port: p.Port, Username: p.Username, Database: p.Database, TLSMode: p.TLSMode, ReadOnly: p.ReadOnly, CreatedAt: now, UpdatedAt: now}, sec))
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 201, out)
}
