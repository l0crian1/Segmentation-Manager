package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/argon2"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type bundle struct {
	Version int    `json:"version"`
	DB      []byte `json:"db"`
	Key     []byte `json:"key"`
}

func protect(data []byte, password string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return nil, e
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	c, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(c)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	header := append([]byte("MSB1"), salt...)
	header = append(header, nonce...)
	return g.Seal(header, nonce, data, []byte("MSB1")), nil
}
func unprotect(data []byte, password string) ([]byte, error) {
	if len(data) < 48 || string(data[:4]) != "MSB1" {
		return nil, errors.New("invalid backup format")
	}
	key := argon2.IDKey([]byte(password), data[4:20], 3, 64*1024, 2, 32)
	c, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(c)
	out, e := g.Open(nil, data[20:32], data[32:], []byte("MSB1"))
	if e != nil {
		return nil, errors.New("wrong backup password or damaged backup")
	}
	return out, nil
}
func (a *App) backupHTTP(w http.ResponseWriter, r *http.Request, u User) {
	if r.Method != "POST" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	var b struct {
		Password string `json:"password"`
	}
	if e := body(w, r, &b); e != nil {
		fail(w, 400, e)
		return
	}
	if !validPassword(b.Password) {
		fail(w, 400, errors.New("backup password must be 12–256 characters"))
		return
	}
	path := filepath.Join(a.data, "backup-"+random(12)+".db")
	defer os.Remove(path)
	if _, e := a.db.Exec("VACUUM INTO '" + strings.ReplaceAll(path, "'", "''") + "'"); e != nil {
		fail(w, 500, errors.New("could not create consistent database snapshot"))
		return
	}
	info, e := os.Stat(path)
	if e != nil || info.Size() > 48<<20 {
		fail(w, 413, errors.New("backup exceeds the 48 MiB database limit; use an offline volume backup"))
		return
	}
	db, e := os.ReadFile(path)
	if e != nil {
		fail(w, 500, e)
		return
	}
	raw, _ := json.Marshal(bundle{1, db, a.key})
	encrypted, e := protect(raw, b.Password)
	if e != nil {
		fail(w, 500, e)
		return
	}
	a.audit(u, "backup.exported", "Encrypted application backup")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=segmentation-manager-backup.msb")
	w.Write(encrypted)
}
func (a *App) restoreHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	if a.configured() {
		fail(w, 409, errors.New("restore is available only on a fresh installation"))
		return
	}
	if !a.allowed(r) {
		fail(w, 429, errors.New("too many restore attempts"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 80<<20)
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		fail(w, 400, errors.New("invalid or oversized backup upload"))
		return
	}
	defer r.MultipartForm.RemoveAll()
	if a.claim == "" || subtle.ConstantTimeCompare([]byte(r.FormValue("claim")), []byte(a.claim)) != 1 {
		fail(w, 403, errors.New("invalid installation claim code"))
		return
	}
	password := r.FormValue("password")
	if !validPassword(password) {
		fail(w, 400, errors.New("invalid backup password"))
		return
	}
	f, _, e := r.FormFile("file")
	if e != nil {
		fail(w, 400, errors.New("backup file required"))
		return
	}
	defer f.Close()
	data, e := io.ReadAll(f)
	if e != nil {
		fail(w, 400, e)
		return
	}
	data, e = unprotect(data, password)
	if e != nil {
		fail(w, 400, e)
		return
	}
	var b bundle
	if e = json.Unmarshal(data, &b); e != nil || b.Version != 1 || len(b.Key) != 32 {
		fail(w, 400, errors.New("unsupported backup"))
		return
	}
	path := filepath.Join(a.data, "restore-"+random(12)+".db")
	journalWritten := false
	defer func() {
		if !journalWritten {
			_ = os.Remove(path)
		}
	}()
	if e = os.WriteFile(path, b.DB, 0600); e != nil {
		fail(w, 500, e)
		return
	}
	check, e := sql.Open("sqlite", path)
	if e != nil {
		fail(w, 400, e)
		return
	}
	var integrity string
	var version, n int
	e = check.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	_ = check.QueryRow("PRAGMA user_version").Scan(&version)
	backupAdminRole := roleAdministrator
	if version == 1 {
		backupAdminRole = "admin"
	}
	_ = check.QueryRow("SELECT count(*) FROM users WHERE role=?", backupAdminRole).Scan(&n)
	if e != nil || integrity != "ok" || (version != 1 && version != 2) || n < 1 {
		check.Close()
		fail(w, 400, errors.New("backup database failed validation"))
		return
	}
	if e = migrateRoles(check); e != nil {
		check.Close()
		fail(w, 400, errors.New("could not migrate backup roles"))
		return
	}
	_, e = check.Exec("DELETE FROM sessions; UPDATE operations SET status='unresolved' WHERE status='applying';")
	check.Close()
	if e != nil {
		fail(w, 400, errors.New("could not prepare restored database"))
		return
	}
	prepared, e := os.ReadFile(path)
	if e != nil {
		fail(w, 500, e)
		return
	}
	journal, _ := json.Marshal(restoreJournal{Candidate: filepath.Base(path), Digest: digest(string(prepared)), Key: b.Key})
	if e = atomicFile(filepath.Join(a.data, "restore.pending"), journal); e != nil {
		fail(w, 500, e)
		return
	}
	journalWritten = true
	// Only a fresh instance can restore, and the request gate excludes all other database users.
	if e = a.db.Close(); e != nil {
		fail(w, 500, e)
		return
	}
	target := filepath.Join(a.data, databaseName)
	if e = recoverRestore(a.data); e != nil {
		fail(w, 500, errors.New("restore interrupted; restart to finish the recorded restore operation"))
		return
	}
	a.key = b.Key
	a.db, e = sql.Open("sqlite", target)
	if e != nil {
		fail(w, 500, e)
		return
	}
	a.db.SetMaxOpenConns(1)
	_, e = a.db.Exec("PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; DELETE FROM sessions; UPDATE operations SET status='unresolved' WHERE status='applying';")
	if e != nil {
		fail(w, 500, e)
		return
	}
	a.claim = ""
	_ = os.Remove(filepath.Join(a.data, "setup.claim"))
	a.audit(User{Name: "restore"}, "backup.restored", "Sessions revoked; application TLS certificate remains local to this installation")
	send(w, 200, M{"ok": true})
}

type restoreJournal struct {
	Candidate string
	Digest    string
	Key       []byte
}

func atomicFile(path string, data []byte) error {
	tmp := path + "." + random(8) + ".tmp"
	defer os.Remove(tmp)
	f, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil
}

// A persisted intent makes replacing the database and its separate encryption key
// recoverable across process crashes. Recovery runs before opening any database.
func recoverRestore(dir string) error {
	marker := filepath.Join(dir, "restore.pending")
	raw, e := os.ReadFile(marker)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var j restoreJournal
	if json.Unmarshal(raw, &j) != nil || len(j.Key) != 32 || filepath.Base(j.Candidate) != j.Candidate || !strings.HasPrefix(j.Candidate, "restore-") {
		return errors.New("invalid restore journal; restore from a verified backup")
	}
	candidate := filepath.Join(dir, j.Candidate)
	target := filepath.Join(dir, databaseName)
	data, e := os.ReadFile(candidate)
	if e == nil {
		if digest(string(data)) != j.Digest {
			return errors.New("restore candidate checksum mismatch")
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Rename(target+suffix, target+".before-restore"+suffix); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if e = os.Rename(candidate, target); e != nil {
			return e
		}
	} else if os.IsNotExist(e) {
		data, e = os.ReadFile(target)
		if e != nil || digest(string(data)) != j.Digest {
			return errors.New("restore target checksum mismatch")
		}
	} else {
		return e
	}
	if e = atomicFile(filepath.Join(dir, "master.key"), j.Key); e != nil {
		return e
	}
	return os.Remove(marker)
}
func (a *App) certificateHTTP(w http.ResponseWriter, r *http.Request, u User) {
	if r.Method != "POST" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	var b struct {
		Certificate string `json:"certificate"`
		Key         string `json:"key"`
	}
	if e := body(w, r, &b); e != nil {
		fail(w, 400, e)
		return
	}
	if _, e := tls.X509KeyPair([]byte(b.Certificate), []byte(b.Key)); e != nil {
		fail(w, 400, errors.New("invalid certificate and private-key pair"))
		return
	}
	path := filepath.Join(a.data, "server.pem.next")
	defer os.Remove(path)
	if e := os.WriteFile(path, []byte(b.Certificate+"\n"+b.Key), 0600); e != nil {
		fail(w, 500, e)
		return
	}
	if e := os.Rename(path, filepath.Join(a.data, "server.pem")); e != nil {
		fail(w, 500, e)
		return
	}
	a.audit(u, "tls.updated", "Certificate installed; restart required")
	send(w, 200, M{"ok": true, "message": "Certificate installed. Restart the container to activate it."})
}
