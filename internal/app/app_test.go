package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixture() Snapshot {
	return Snapshot{Mode: "mpls", Family: "ipv4-unicast", Raw: M{}, VRFs: []VRF{{Name: "A-prod", Imports: []string{"64512:999"}, Exports: []string{"65000:1"}}, {Name: "B", Imports: []string{}, Exports: []string{"65000:2", "65000:22"}}, {Name: "C", Imports: []string{}, Exports: []string{"65000:3"}}}}
}
func TestConnectDisconnectPreservesForeignImports(t *testing.T) {
	s := fixture()
	p, e := buildPlan(s, []Edit{{"A-prod", "B", true}}, true)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Commands) != 2 || !slices.Equal(p.After[0].Imports, []string{"64512:999", "65000:2", "65000:22"}) {
		t.Fatalf("unexpected plan: %+v", p)
	}
	if len(s.VRFs[0].Imports) != 1 {
		t.Fatal("planner mutated baseline")
	}
	s.VRFs = p.After
	p, e = buildPlan(s, []Edit{{"A-prod", "B", false}}, false)
	if e != nil {
		t.Fatal(e)
	}
	if !slices.Equal(p.After[0].Imports, []string{"64512:999"}) || len(p.After[1].Imports) != 0 {
		t.Fatal("disconnect changed unrelated imports")
	}
	if p.Commands[1].Op != "delete" {
		t.Fatal("empty MPLS import leaf must be deleted")
	}
}
func TestSharedTargetCollateralBlocked(t *testing.T) {
	s := fixture()
	s.VRFs[2].Exports = []string{"65000:2"}
	if _, e := buildPlan(s, []Edit{{"A-prod", "B", true}}, false); e == nil {
		t.Fatal("shared target collateral change was accepted")
	}
}
func TestPartialRelationshipCompleted(t *testing.T) {
	s := fixture()
	s.VRFs[0].Imports = []string{"65000:2", "65000:22"}
	p, e := buildPlan(s, []Edit{{"A-prod", "B", true}}, false)
	if e != nil || len(p.Commands) != 1 || relationship(p.After[0], p.After[1]) != 2 {
		t.Fatalf("partial completion failed: %v %+v", e, p)
	}
}
func TestIPv6AndEVPNPaths(t *testing.T) {
	s := fixture()
	s.Family = "ipv6-unicast"
	p, e := buildPlan(s, []Edit{{"A-prod", "B", true}}, false)
	if e != nil || !slices.Contains(p.Commands[0].Path, "ipv6-unicast") {
		t.Fatal("IPv6 path missing")
	}
	s.Mode = "evpn"
	s.VRFs[1].Imports = []string{"65000:2"}
	p, e = buildPlan(s, []Edit{{"A-prod", "B", true}}, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Commands) != 3 {
		t.Fatalf("EVPN expected individual target operations: %+v", p.Commands)
	}
	for _, c := range p.Commands {
		if slices.Contains(c.Path, "vpn") || !slices.Contains(c.Path, "l2vpn-evpn") {
			t.Fatal("wrong EVPN path")
		}
	}
}
func TestUnsafeStatesRejected(t *testing.T) {
	for _, reason := range []string{"Automatic targets", "Combined import/export targets", "Unknown release"} {
		s := fixture()
		s.VRFs[0].Reason = reason
		if _, e := buildPlan(s, []Edit{{"A-prod", "B", true}}, false); e == nil {
			t.Fatal(reason)
		}
	}
	s := fixture()
	if _, e := buildPlan(s, []Edit{{"A-prod", "B", true}, {"B", "A-prod", false}}, false); e == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestEVPNLastExplicitImportRemoved(t *testing.T) {
	s := fixture()
	s.Mode = "evpn"
	s.VRFs[0].Imports = []string{"65000:2"}
	s.VRFs[1].Imports = []string{"65000:1"}
	p, err := buildPlan(s, []Edit{{"A-prod", "B", false}}, false)
	if err != nil || len(p.Commands) != 2 || len(p.After[0].Imports) != 0 || len(p.After[1].Imports) != 0 {
		t.Fatalf("last explicit imports: %v %+v", err, p)
	}
}
func TestRTEncodingBounds(t *testing.T) {
	for _, v := range []string{"65000:4294967295", "4200000000:65535", "192.0.2.1:65535"} {
		if !validRT(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"4200000000:65536", "192.0.2.1:65536", "65000:4294967296", "1:2;reboot", "*:123", "bad"} {
		if validRT(v) {
			t.Fatal(v)
		}
	}
}
func TestBackupEncryptionAuthenticated(t *testing.T) {
	raw := []byte("database plus key")
	b, e := protect(raw, "long-backup-password")
	if e != nil {
		t.Fatal(e)
	}
	out, e := unprotect(b, "long-backup-password")
	if e != nil || !bytes.Equal(out, raw) {
		t.Fatal(e)
	}
	if _, e = unprotect(b, "wrong-password"); e == nil {
		t.Fatal("wrong password accepted")
	}
	b[len(b)-1] ^= 1
	if _, e = unprotect(b, "long-backup-password"); e == nil {
		t.Fatal("tamper accepted")
	}
}
func testApp(t *testing.T) *App {
	t.Helper()
	a, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(a.Close)
	return a
}
func request(t *testing.T, a *App, method, path string, data any, cookie *http.Cookie, csrfToken string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(data)
	r := httptest.NewRequest(method, "https://localhost"+path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrfToken)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func setupTest(t *testing.T, a *App) (*http.Cookie, string) {
	t.Helper()
	w := request(t, a, "POST", "/api/setup", M{"claim": a.claim, "name": "admin", "password": "a-long-test-password", "title": "Lab"}, nil, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("insecure cookie")
	}
	return cookies[0], a.csrf(cookies[0].Value)
}
func TestAuthCSRFAndLastAdmin(t *testing.T) {
	a := testApp(t)
	if w := request(t, a, "GET", "/api/devices", nil, nil, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	cookie, token := setupTest(t, a)
	if w := request(t, a, "POST", "/api/sites", M{"name": "Lab"}, cookie, ""); w.Code != 403 {
		t.Fatal("CSRF missing was allowed")
	}
	if w := request(t, a, "POST", "/api/sites", M{"name": "Lab", "description": ""}, cookie, token); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(t, a, "DELETE", "/api/users?id=1", nil, cookie, token); w.Code != 409 {
		t.Fatal("last admin removed")
	}
	if w := request(t, a, "POST", "/api/setup", M{}, nil, ""); w.Code != 409 {
		t.Fatal("setup reopened")
	}
}
func TestViewerCannotWrite(t *testing.T) {
	a := testApp(t)
	cookie, token := setupTest(t, a)
	w := request(t, a, "POST", "/api/users", M{"name": "observer", "password": "viewer-password-long", "role": "view-only"}, cookie, token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(t, a, "POST", "/api/login", M{"name": "observer", "password": "viewer-password-long"}, nil, "")
	vc := w.Result().Cookies()[0]
	if w = request(t, a, "POST", "/api/sites", M{"name": "bad"}, vc, a.csrf(vc.Value)); w.Code != 403 {
		t.Fatal("viewer wrote inventory")
	}
	if w = request(t, a, "GET", "/api/settings", nil, vc, ""); w.Code != 200 {
		t.Fatal("view-only could not read application settings")
	}
}
func TestPersistenceAndEncryptedCredentials(t *testing.T) {
	dir := t.TempDir()
	a, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	setupTest(t, a)
	encrypted, e := a.seal("router-secret-value")
	if e != nil || strings.Contains(encrypted, "router-secret-value") {
		t.Fatal("credential not encrypted")
	}
	a.Close()
	b, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if !b.configured() || b.claim != "" {
		t.Fatal("setup returned after restart")
	}
	plain, e := b.unseal(encrypted)
	if e != nil || plain != "router-secret-value" {
		t.Fatal("key did not persist")
	}
}
func TestBackupRestoresAccountsAndSecrets(t *testing.T) {
	a := testApp(t)
	cookie, token := setupTest(t, a)
	encrypted, _ := a.seal("device-secret")
	w := request(t, a, "POST", "/api/backup", M{"password": "backup-password-long"}, cookie, token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	b := testApp(t)
	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	mp.WriteField("claim", b.claim)
	mp.WriteField("password", "backup-password-long")
	f, _ := mp.CreateFormFile("file", "backup.msb")
	f.Write(w.Body.Bytes())
	mp.Close()
	r := httptest.NewRequest("POST", "https://localhost/api/restore", &buf)
	r.Header.Set("Content-Type", mp.FormDataContentType())
	out := httptest.NewRecorder()
	b.Handler().ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal(out.Body.String())
	}
	plain, e := b.unseal(encrypted)
	if !b.configured() || e != nil || plain != "device-secret" {
		t.Fatal("restore incomplete")
	}
	if request(t, b, "GET", "/api/me", nil, cookie, "").Code != 401 {
		t.Fatal("restored session was retained")
	}
}

func routerFixture() M {
	return M{"name": M{"A": M{"table": "101", "protocols": M{"bgp": M{"address-family": M{"ipv4-unicast": M{"import": M{"vpn": M{}}, "export": M{"vpn": M{}}, "route-target": M{"vpn": M{"export": "65000:1"}}}}}}}, "B": M{"table": "102", "protocols": M{"bgp": M{"address-family": M{"ipv4-unicast": M{"import": M{"vpn": M{}}, "export": M{"vpn": M{}}, "route-target": M{"vpn": M{"export": "65000:2"}}}}}}}}}
}
func TestDiscoveryAllowsEveryRelease(t *testing.T) {
	for _, version := range releaseFixtures {
		t.Run(version, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				var q M
				json.Unmarshal([]byte(r.Form.Get("data")), &q)
				data := any(releaseVRFFixture())
				if r.URL.Path == "/show" {
					data = "Version:          " + version + "\n"
				}
				json.NewEncoder(w).Encode(M{"success": true, "data": data})
			}))
			defer server.Close()
			router := &Router{client: server.Client(), base: server.URL, key: "key"}
			for _, mode := range []string{"mpls", "evpn"} {
				for _, family := range []string{"ipv4-unicast", "ipv6-unicast"} {
					s, e := router.snapshot(context.Background(), Device{}, mode, family)
					if e != nil || len(s.VRFs) != 2 || s.VRFs[0].Reason != "" || s.VRFs[1].Reason != "" {
						t.Fatalf("%s %s: %v %+v", mode, family, e, s)
					}
					p, e := buildPlan(s, []Edit{{"A", "B", true}}, false)
					if e != nil || len(p.Commands) != 2 {
						t.Fatal(mode, family, e)
					}
				}
			}
		})
	}
}
func TestApplyReconcilesTimeoutAndDetectsDrift(t *testing.T) {
	a := testApp(t)
	cookie, token := setupTest(t, a)
	raw := routerFixture()
	writes := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		data := any(raw)
		if r.URL.Path == "/show" {
			data = "Version: VyOS 1.5.0\n"
		}
		if r.URL.Path == "/configure" {
			writes++
			for name, v := range object(raw["name"]) {
				target := "65000:2"
				if name == "B" {
					target = "65000:1"
				}
				object(at(object(v), "protocols", "bgp", "address-family", "ipv4-unicast", "route-target", "vpn"))["import"] = target
			}
			w.WriteHeader(504)
			io.WriteString(w, `{"success":false,"error":"upstream timed out"}`)
			return
		}
		json.NewEncoder(w).Encode(M{"success": true, "data": data})
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	secret, _ := a.seal("key")
	a.db.Exec("INSERT INTO sites(name) VALUES('Lab')")
	res, e := a.db.Exec("INSERT INTO devices(site_id,name,host,port,trust,fingerprint,save,secret) VALUES(1,'router',?,?,'pin',?,0,?)", u.Hostname(), port, fingerprint(server.Certificate()), secret)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := res.LastInsertId()
	get := request(t, a, "GET", fmt.Sprintf("/api/snapshot?id=%d&mode=mpls", id), nil, cookie, "")
	if get.Code != 200 {
		t.Fatal(get.Body.String())
	}
	var s Snapshot
	json.Unmarshal(get.Body.Bytes(), &s)
	payload := M{"device_id": id, "mode": "mpls", "family": "ipv4-unicast", "fingerprint": s.Fingerprint, "edits": []Edit{{"A", "B", true}}}
	w := request(t, a, "POST", "/api/apply", payload, cookie, token)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var started M
	json.Unmarshal(w.Body.Bytes(), &started)
	result := waitOperation(t, a, cookie, str(started["operation_id"]))
	if result["status"] != "applied-unsaved" {
		t.Fatal(result)
	}
	w = request(t, a, "POST", "/api/apply", payload, cookie, token)
	if w.Code != 409 || writes != 1 {
		t.Fatal("stale state replayed", w.Body.String(), writes)
	}
}

func TestEVPNExplicitExportsWithoutRuntime(t *testing.T) {
	for _, exported := range []bool{true, false} {
		t.Run(fmt.Sprint(exported), func(t *testing.T) {
			rt := M{}
			if exported {
				rt["export"] = []any{"65000:201", "65000:202"}
			}
			raw := M{"name": M{"BLUE": M{"vni": "10201", "protocols": M{"bgp": M{"address-family": M{"l2vpn-evpn": M{"route-target": rt}}}}}}}
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.ParseForm()
				var q M
				json.Unmarshal([]byte(r.Form.Get("data")), &q)
				data := any(raw)
				if r.URL.Path == "/show" {
					if fmt.Sprint(q["path"]) != "[version]" {
						t.Error("runtime discovery must not be required")
					}
					data = "Version: VyOS 2026.09.17-0028-rolling\n"
				}
				json.NewEncoder(w).Encode(M{"success": true, "data": data})
			}))
			defer srv.Close()
			router := &Router{client: srv.Client(), base: srv.URL, key: "test"}
			snapshot, err := router.snapshot(context.Background(), Device{}, "evpn", "ipv4-unicast")
			if err != nil || len(snapshot.VRFs) != 1 {
				t.Fatal(err, snapshot)
			}
			v := snapshot.VRFs[0]
			if exported {
				if !v.Known || v.Reason != "" || len(v.Imports) != 0 || len(v.Exports) != 2 {
					t.Fatal(v)
				}
				p, err := buildPlan(snapshot, []Edit{{"BLUE", "BLUE", true}}, false)
				if err != nil || len(p.Commands) != 2 {
					t.Fatal(err, p)
				}
			} else if !strings.Contains(v.Reason, "set vrf name BLUE") || v.Known {
				t.Fatal("missing export guidance", v)
			}
		})
	}
}

func waitOperation(t *testing.T, a *App, cookie *http.Cookie, id string) M {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		w := request(t, a, "GET", "/api/operation?id="+id, nil, cookie, "")
		var out M
		json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		if out["status"] != "applying" {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operation did not complete")
	return nil
}

func TestVerifyDetectsUnrelatedChanges(t *testing.T) {
	s := fixture()
	s.Raw = M{"name": M{"A-prod": M{"table": "101"}}}
	p, e := buildPlan(s, []Edit{{"A-prod", "B", true}}, false)
	if e != nil {
		t.Fatal(e)
	}
	after := s
	after.VRFs = p.After
	if !verifyPlan(p, after) {
		t.Fatal("expected imports rejected")
	}
	after.Raw = M{"name": M{"A-prod": M{"table": "999"}}}
	if verifyPlan(p, after) {
		t.Fatal("unrelated VRF change was not detected")
	}
}
func TestMissingMasterKeyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	a, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	a.Close()
	if e = os.Remove(filepath.Join(dir, "master.key")); e != nil {
		t.Fatal(e)
	}
	if b, e := New(dir); e == nil {
		b.Close()
		t.Fatal("regenerated a key for an existing database")
	}
}

func TestInterruptedRestoreRecoversMatchingKey(t *testing.T) {
	dir := t.TempDir()
	data := []byte("already-renamed-database")
	key := bytes.Repeat([]byte{7}, 32)
	os.WriteFile(filepath.Join(dir, databaseName), data, 0600)
	os.WriteFile(filepath.Join(dir, "master.key"), bytes.Repeat([]byte{1}, 32), 0600)
	j, _ := json.Marshal(restoreJournal{Candidate: "restore-example.db", Digest: digest(string(data)), Key: key})
	os.WriteFile(filepath.Join(dir, "restore.pending"), j, 0600)
	if e := recoverRestore(dir); e != nil {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile(filepath.Join(dir, "master.key"))
	if !bytes.Equal(actual, key) {
		t.Fatal("database/key mismatch survived recovery")
	}
	if e := recoverRestore(dir); e != nil {
		t.Fatal("recovery is not idempotent", e)
	}
}
