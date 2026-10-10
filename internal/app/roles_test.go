package app

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func accountForRole(t *testing.T, a *App, admin *http.Cookie, csrf, role string) (*http.Cookie, string) {
	t.Helper()
	name := "account-" + role
	w := request(t, a, "POST", "/api/users", M{"name": name, "password": "a-long-test-password", "role": role}, admin, csrf)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(t, a, "POST", "/api/login", M{"name": name, "password": "a-long-test-password"}, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	return cookie, a.csrf(cookie.Value)
}

func TestRoleAPIPermissions(t *testing.T) {
	a := testApp(t)
	admin, csrf := setupTest(t, a)
	network, ncsrf := accountForRole(t, a, admin, csrf, roleNetworkAdmin)
	viewer, vcsrf := accountForRole(t, a, admin, csrf, roleViewOnly)
	for _, c := range []*http.Cookie{network, viewer} {
		for _, path := range []string{"/api/users", "/api/settings", "/api/audit", "/api/devices", "/api/sites", "/api/operation?device_id=1"} {
			w := request(t, a, "GET", path, nil, c, "")
			if w.Code != 200 {
				t.Fatal(path, w.Code, w.Body.String())
			}
			if path == "/api/users" && strings.Contains(w.Body.String(), "password") {
				t.Fatal("password data exposed")
			}
		}
	}
	// Exercise the actual guard before malformed or missing-resource request bodies
	// can reach any mutation handler. A view-only account has no write route.
	for _, path := range []string{"/api/sites", "/api/devices", "/api/trust", "/api/plan", "/api/apply", "/api/vrfs/plan", "/api/vrfs/apply", "/api/users", "/api/settings", "/api/backup", "/api/certificate"} {
		for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			if w := request(t, a, method, path, M{}, viewer, vcsrf); w.Code != 403 {
				t.Fatal(method, path, w.Code)
			}
		}
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if w := request(t, a, method, "/api/users?id=1", M{"id": 1, "name": "admin", "role": roleNetworkAdmin, "password": "a-long-test-password"}, network, ncsrf); w.Code != 403 {
			t.Fatal("network admin managed accounts", method, w.Code)
		}
	}
	for _, path := range []string{"/api/backup", "/api/certificate"} {
		if w := request(t, a, "POST", path, M{}, network, ncsrf); w.Code != 403 {
			t.Fatal("network account gained installation access", path)
		}
	}
	if w := request(t, a, "POST", "/api/sites", M{"name": "Network managed", "description": "test"}, network, ncsrf); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(t, a, "PUT", "/api/settings", a.settings(), network, ncsrf); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(t, a, "POST", "/api/sites", M{"name": "Missing CSRF"}, network, ""); w.Code != 403 {
		t.Fatal("network-admin bypassed CSRF")
	}
	for _, legacy := range []string{"admin", "viewer", "root"} {
		if w := request(t, a, "POST", "/api/users", M{"name": "invalid-role", "password": "a-long-test-password", "role": legacy}, admin, csrf); w.Code != 400 {
			t.Fatal("invalid role accepted", legacy)
		}
	}
	if w := request(t, a, "PUT", "/api/users", M{"id": 1, "name": "admin", "role": roleNetworkAdmin}, admin, csrf); w.Code != 409 {
		t.Fatal("last administrator demoted", w.Body.String())
	}
	// Account demotion revokes its already-authenticated session.
	var id int64
	a.db.QueryRow("SELECT id FROM users WHERE name=?", "account-network-admin").Scan(&id)
	if w := request(t, a, "PUT", "/api/users", M{"id": id, "name": "account-network-admin", "role": roleViewOnly}, admin, csrf); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(t, a, "POST", "/api/sites", M{"name": "Old session"}, network, ncsrf); w.Code != 401 {
		t.Fatal("demoted user's session survived")
	}
	for _, c := range []struct {
		cookie *http.Cookie
		token  string
	}{{viewer, vcsrf}, {admin, csrf}} {
		if w := request(t, a, "POST", "/api/logout", nil, c.cookie, c.token); w.Code != 200 {
			t.Fatal("logout blocked")
		}
	}
}

func legacyRoleFixture(t *testing.T, a *App) {
	t.Helper()
	_, e := a.db.Exec(`PRAGMA foreign_keys=OFF;
 BEGIN;
 CREATE TABLE users_old(id INTEGER PRIMARY KEY,name TEXT NOT NULL COLLATE NOCASE UNIQUE,password TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('admin','viewer')));
 INSERT INTO users_old SELECT id,name,password,CASE role WHEN 'administrator' THEN 'admin' WHEN 'view-only' THEN 'viewer' END FROM users;
 DROP TABLE users;
 ALTER TABLE users_old RENAME TO users;
 PRAGMA user_version=1;
 COMMIT;
 PRAGMA foreign_keys=ON;`)
	if e != nil {
		t.Fatal(e)
	}
}

func TestRoleMigrationPreservesAccountsSessionsAndData(t *testing.T) {
	dir := t.TempDir()
	a, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	admin, csrf := setupTest(t, a)
	viewer, _ := accountForRole(t, a, admin, csrf, roleViewOnly)
	secret, _ := a.seal("preserved-device-key")
	a.db.Exec("INSERT INTO sites(name) VALUES('Existing site')")
	a.db.Exec("INSERT INTO devices(site_id,name,host,port,trust,fingerprint,save,secret) VALUES(1,'router','192.0.2.1',443,'system','',0,?)", secret)
	var hash string
	a.db.QueryRow("SELECT password FROM users WHERE id=1").Scan(&hash)
	legacyRoleFixture(t, a)
	a.Close()
	a, e = New(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	for _, row := range []struct {
		cookie *http.Cookie
		role   string
	}{{admin, roleAdministrator}, {viewer, roleViewOnly}} {
		w := request(t, a, "GET", "/api/me", nil, row.cookie, "")
		if w.Code != 200 {
			t.Fatal("session lost", w.Body.String())
		}
		var result struct {
			User User `json:"user"`
		}
		json.Unmarshal(w.Body.Bytes(), &result)
		if result.User.Role != row.role {
			t.Fatal(result)
		}
	}
	var got string
	a.db.QueryRow("SELECT password FROM users WHERE id=1").Scan(&got)
	if got != hash {
		t.Fatal("password changed")
	}
	d, e := a.device(1)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := a.unseal(d.Secret)
	if e != nil || plain != "preserved-device-key" {
		t.Fatal("device credential lost")
	}
	if e = migrateRoles(a.db); e != nil {
		t.Fatal("migration not idempotent", e)
	}
	var fk, version int
	a.db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	a.db.QueryRow("PRAGMA user_version").Scan(&version)
	if fk != 1 || version != 2 {
		t.Fatal(fk, version)
	}
	if _, e = a.db.Exec("DELETE FROM users WHERE name='account-view-only'"); e != nil {
		t.Fatal(e)
	}
	if w := request(t, a, "GET", "/api/me", nil, viewer, ""); w.Code != 401 {
		t.Fatal("session FK cascade broken")
	}
}

func TestLegacyBackupRestoresWithNewRoles(t *testing.T) {
	source := testApp(t)
	admin, csrf := setupTest(t, source)
	accountForRole(t, source, admin, csrf, roleViewOnly)
	legacyRoleFixture(t, source)
	path := filepath.Join(t.TempDir(), "legacy.db")
	if _, e := source.db.Exec("VACUUM INTO ?", path); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(bundle{1, data, source.key})
	encrypted, e := protect(raw, "backup-password-long")
	if e != nil {
		t.Fatal(e)
	}
	target := testApp(t)
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	form.WriteField("claim", target.claim)
	form.WriteField("password", "backup-password-long")
	file, _ := form.CreateFormFile("file", "legacy.msb")
	file.Write(encrypted)
	form.Close()
	req := httptest.NewRequest("POST", "https://localhost/api/restore", &buf)
	req.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	target.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(t, target, "POST", "/api/login", M{"name": "admin", "password": "a-long-test-password"}, nil, "")
	if w.Code != 200 {
		t.Fatal("restored administrator cannot log in", w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if w = request(t, target, "POST", "/api/users", M{"name": "restored-network", "password": "a-long-test-password", "role": roleNetworkAdmin}, cookie, target.csrf(cookie.Value)); w.Code != 200 {
		t.Fatal("restored schema was not upgraded", w.Body.String())
	}
}
