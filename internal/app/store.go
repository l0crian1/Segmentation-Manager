package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type App struct {
	db       *sql.DB
	key      []byte
	data     string
	locks    sync.Map
	workers  sync.WaitGroup
	setupMu  sync.Mutex
	limitMu  sync.Mutex
	gate     sync.RWMutex
	attempts map[string]attempt
	claim    string
}
type attempt struct {
	n     int
	until time.Time
}
type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}
type Site struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
type Device struct {
	ID          int64  `json:"id"`
	SiteID      int64  `json:"site_id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Trust       string `json:"trust"`
	Fingerprint string `json:"fingerprint"`
	Save        bool   `json:"save"`
	Secret      string `json:"-"`
	Status      string `json:"status"`
	Checked     string `json:"checked"`
	Version     string `json:"version"`
}
type Settings struct {
	Name          string `json:"name"`
	Refresh       int    `json:"refresh"`
	Timeout       int    `json:"timeout"`
	SessionHours  int    `json:"session_hours"`
	RetentionDays int    `json:"retention_days"`
}

func defaults() Settings { return Settings{"Segmentation Manager", 30, 15, 8, 90} }
func New(dir string) (*App, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := recoverRestore(dir); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(filepath.Join(dir, "master.key"))
	if os.IsNotExist(err) {
		if info, e := os.Stat(filepath.Join(dir, databaseName)); e == nil && info.Size() > 0 {
			return nil, errors.New("master.key is missing for an existing database; restore the matching key from backup")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		err = os.WriteFile(filepath.Join(dir, "master.key"), key, 0600)
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid master key")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, databaseName))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	a := &App{db: db, key: key, data: dir, attempts: map[string]attempt{}}
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	var ver int
	if err = db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil {
		db.Close()
		return nil, err
	}
	if ver > 2 {
		db.Close()
		return nil, errors.New("database is newer than this application")
	}
	if ver == 0 {
		tx, e := db.Begin()
		if e != nil {
			return nil, e
		}
		_, e = tx.Exec(`
 CREATE TABLE users(id INTEGER PRIMARY KEY,name TEXT NOT NULL COLLATE NOCASE UNIQUE,password TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('admin','viewer')));
 CREATE TABLE sessions(token TEXT PRIMARY KEY,user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,expires INTEGER NOT NULL,last_seen INTEGER NOT NULL);
 CREATE TABLE settings(id INTEGER PRIMARY KEY CHECK(id=1),value TEXT NOT NULL);
 CREATE TABLE sites(id INTEGER PRIMARY KEY,name TEXT NOT NULL UNIQUE,description TEXT NOT NULL DEFAULT '');
 CREATE TABLE devices(id INTEGER PRIMARY KEY,site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE RESTRICT,name TEXT NOT NULL,host TEXT NOT NULL,port INTEGER NOT NULL,trust TEXT NOT NULL,fingerprint TEXT NOT NULL,save INTEGER NOT NULL,secret TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'Not checked',checked TEXT NOT NULL DEFAULT '',version TEXT NOT NULL DEFAULT '',UNIQUE(host,port));
 CREATE TABLE operations(id TEXT PRIMARY KEY,device_id INTEGER,user_id INTEGER,created TEXT NOT NULL,status TEXT NOT NULL,detail TEXT NOT NULL);
 CREATE TABLE audit(id INTEGER PRIMARY KEY,created TEXT NOT NULL,user_name TEXT NOT NULL,action TEXT NOT NULL,detail TEXT NOT NULL);
 PRAGMA user_version=1;`)
		if e != nil {
			tx.Rollback()
			db.Close()
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
	}
	if err = migrateRoles(db); err != nil {
		db.Close()
		return nil, err
	}
	b, _ := json.Marshal(defaults())
	if _, err = db.Exec("INSERT OR IGNORE INTO settings VALUES(1,?)", string(b)); err != nil {
		return nil, err
	}
	existing := a.settings()
	if existing.Name == "Microseg" {
		existing.Name = "Segmentation Manager"
		encoded, _ := json.Marshal(existing)
		if _, err = db.Exec("UPDATE settings SET value=? WHERE id=1", string(encoded)); err != nil {
			return nil, err
		}
	}
	// A crash can happen after the router commits. Never replay interrupted writes.
	if _, err = db.Exec("UPDATE operations SET status='unresolved' WHERE status='applying'"); err != nil {
		return nil, err
	}
	if !a.configured() {
		p := filepath.Join(dir, "setup.claim")
		b, e := os.ReadFile(p)
		if os.IsNotExist(e) {
			b = []byte(random(24))
			e = os.WriteFile(p, b, 0600)
		}
		if e != nil {
			return nil, e
		}
		a.claim = string(b)
		slog.Info("First installation claim code (keep private)", "code", a.claim)
	}
	return a, nil
}
func (a *App) Close() { a.workers.Wait(); a.db.Close() }
func (a *App) configured() bool {
	var n int
	_ = a.db.QueryRow("SELECT count(*) FROM users").Scan(&n)
	return n > 0
}
func (a *App) settings() Settings {
	s := defaults()
	var b string
	if a.db.QueryRow("SELECT value FROM settings WHERE id=1").Scan(&b) == nil {
		_ = json.Unmarshal([]byte(b), &s)
	}
	return s
}
func random(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (a *App) seal(s string) (string, error) {
	b, e := aes.NewCipher(a.key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return "", e
	}
	return base64.RawStdEncoding.EncodeToString(g.Seal(n, n, []byte(s), []byte(legacyCredentialAAD))), nil
}
func (a *App) unseal(s string) (string, error) {
	b, e := base64.RawStdEncoding.DecodeString(s)
	if e != nil {
		return "", e
	}
	c, e := aes.NewCipher(a.key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(c)
	if e != nil || len(b) < g.NonceSize() {
		return "", errors.New("invalid encrypted credential")
	}
	p, e := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], []byte(legacyCredentialAAD))
	return string(p), e
}
func (a *App) device(id int64) (Device, error) {
	var d Device
	err := a.db.QueryRow("SELECT id,site_id,name,host,port,trust,fingerprint,save,secret,status,checked,version FROM devices WHERE id=?", id).Scan(&d.ID, &d.SiteID, &d.Name, &d.Host, &d.Port, &d.Trust, &d.Fingerprint, &d.Save, &d.Secret, &d.Status, &d.Checked, &d.Version)
	return d, err
}
func (a *App) lock(id int64) *sync.Mutex {
	v, _ := a.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func (a *App) audit(u User, action, detail string) {
	if _, e := a.db.Exec("INSERT INTO audit(created,user_name,action,detail) VALUES(?,?,?,?)", time.Now().UTC().Format(time.RFC3339), u.Name, action, detail); e != nil {
		slog.Error("audit write failed", "error", e)
	}
}
func (a *App) operation(id string, device int64, u User, status, detail string) error {
	_, e := a.db.Exec("INSERT INTO operations(id,device_id,user_id,created,status,detail) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,detail=excluded.detail", id, device, u.ID, time.Now().UTC().Format(time.RFC3339), status, detail)
	return e
}
func dbError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("cannot save record: check duplicate names, required site, or referenced devices")
}
