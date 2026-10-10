package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiagonalAndFullMesh(t *testing.T) {
	for _, mode := range []string{"mpls", "evpn"} {
		t.Run(mode, func(t *testing.T) {
			s := Snapshot{Mode: mode, Family: "ipv4-unicast", Raw: M{}}
			for i := 1; i <= 15; i++ {
				s.VRFs = append(s.VRFs, VRF{Name: fmt.Sprintf("VRF%02d", i), Imports: []string{"64512:999"}, Exports: []string{fmt.Sprintf("65000:%d", i)}})
			}
			edits := []Edit{}
			for i, a := range s.VRFs {
				for _, b := range s.VRFs[i:] {
					edits = append(edits, Edit{a.Name, b.Name, true})
				}
			}
			p, err := buildPlan(s, edits, false)
			if err != nil {
				t.Fatal(err)
			}
			expected := 15
			if mode == "evpn" {
				expected = 225
			}
			if len(p.Commands) != expected {
				t.Fatalf("commands=%d", len(p.Commands))
			}
			for _, v := range p.After {
				if len(v.Imports) != 16 || relationship(v, v) != 2 {
					t.Fatal(v)
				}
			}
			s.VRFs = p.After
			p, err = buildPlan(s, []Edit{{"VRF01", "VRF01", false}}, false)
			if err != nil || len(p.Commands) != 1 || slices.Contains(p.After[0].Imports, "65000:1") || !slices.Contains(p.After[0].Imports, "64512:999") || relationship(p.After[0], p.After[1]) != 2 {
				t.Fatal(err, p)
			}
		})
	}
}

func TestDiagonalSharedTargetCollateral(t *testing.T) {
	s := fixture()
	s.VRFs[1].Exports = s.VRFs[0].Exports
	if _, err := buildPlan(s, []Edit{{"A-prod", "A-prod", true}}, false); err == nil {
		t.Fatal("diagonal altered another pair")
	}
	if _, err := buildPlan(s, []Edit{{"A-prod", "B", true}}, false); err == nil {
		t.Fatal("pair silently altered diagonals")
	}
	if _, err := buildPlan(s, []Edit{{"A-prod", "A-prod", true}, {"B", "B", true}, {"A-prod", "B", true}}, false); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncSlowCommitAndIdempotency(t *testing.T) {
	a := testApp(t)
	cookie, csrf := setupTest(t, a)
	cookie, csrf = accountForRole(t, a, cookie, csrf, roleNetworkAdmin)
	prefs := a.settings()
	prefs.Timeout = 1
	b, _ := json.Marshal(prefs)
	a.db.Exec("UPDATE settings SET value=?", string(b))
	raw := routerFixture()
	var mu sync.Mutex
	var writes atomic.Int32
	committed := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/configure" {
			writes.Add(1)
			// Longer than the read timeout: the configure client must keep waiting.
			time.Sleep(1200 * time.Millisecond)
			w.WriteHeader(504)
			w.Write([]byte(`{"success":false,"error":"upstream timeout"}`))
			go func() {
				time.Sleep(80 * time.Millisecond)
				mu.Lock()
				for name, v := range object(raw["name"]) {
					target := "65000:2"
					if name == "B" {
						target = "65000:1"
					}
					object(at(object(v), "protocols", "bgp", "address-family", "ipv4-unicast", "route-target", "vpn"))["import"] = target
				}
				mu.Unlock()
				close(committed)
			}()
			return
		}
		mu.Lock()
		defer mu.Unlock()
		data := any(raw)
		if r.URL.Path == "/show" {
			data = "Version: VyOS 1.5.0\n"
		}
		json.NewEncoder(w).Encode(M{"success": true, "data": data})
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	secret, _ := a.seal("key")
	a.db.Exec("INSERT INTO sites(name) VALUES('Lab')")
	_, err := a.db.Exec("INSERT INTO devices(site_id,name,host,port,trust,fingerprint,save,secret) VALUES(1,'router',?,?,'pin',?,0,?)", u.Hostname(), port, fingerprint(srv.Certificate()), secret)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := a.device(1)
	router, _ := a.router(d)
	if router.writeClient.Timeout <= router.client.Timeout {
		t.Fatal("write timeout still coupled to reads")
	}
	s, err := router.snapshot(context.Background(), d, "mpls", "ipv4-unicast")
	if err != nil {
		t.Fatal(err)
	}
	payload := M{"device_id": 1, "mode": "mpls", "family": "ipv4-unicast", "fingerprint": s.Fingerprint, "edits": []Edit{{"A", "B", true}}, "operation_id": "slow-commit-test-001"}
	start := time.Now()
	w := request(t, a, "POST", "/api/apply", payload, cookie, csrf)
	if w.Code != 202 || time.Since(start) > time.Second {
		t.Fatal("apply did not acknowledge promptly", w.Body.String())
	}
	// A repeated request returns the same operation, even while the device lock is held.
	if w = request(t, a, "POST", "/api/apply", payload, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	payload["fingerprint"] = "different"
	if w = request(t, a, "POST", "/api/apply", payload, cookie, csrf); w.Code != 409 {
		t.Fatal("ID reused for different intent")
	}
	payload["operation_id"] = "another-operation-002"
	payload["fingerprint"] = s.Fingerprint
	if w = request(t, a, "POST", "/api/apply", payload, cookie, csrf); w.Code != 409 {
		t.Fatal("device lock released early")
	}
	result := waitOperation(t, a, cookie, "slow-commit-test-001")
	if result["status"] != "applied-unsaved" || writes.Load() != 1 {
		t.Fatal(result, writes.Load())
	}
	<-committed
	if w = request(t, a, "POST", "/api/apply", payload, cookie, csrf); w.Code != 409 {
		t.Fatal("external/stale baseline accepted")
	}
}

func TestReconcileDoesNotMistakeUnrelatedDriftForSuccess(t *testing.T) {
	s := fixture()
	p, _ := buildPlan(s, []Edit{{"A-prod", "B", true}}, false)
	// A router with different VRFs must never be accepted, even after repeated reads.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := any(routerFixture())
		if r.URL.Path == "/show" {
			data = "Version: VyOS 1.5.0\n"
		}
		json.NewEncoder(w).Encode(M{"success": true, "data": data})
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := reconcile(ctx, &Router{client: srv.Client(), base: srv.URL}, Device{}, p, time.Millisecond)
	if err == nil {
		t.Fatal("drift accepted as our commit")
	}
}

func TestRenameKeepsStoredUsersSecretsAndSessions(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cookie, _ := setupTest(t, a)
	prefs := a.settings()
	prefs.Name = "Microseg"
	encoded, _ := json.Marshal(prefs)
	a.db.Exec("UPDATE settings SET value=?", string(encoded))
	encrypted, err := a.seal("old-device-credential")
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.settings().Name != "Segmentation Manager" {
		t.Fatal("factory branding not upgraded")
	}
	plain, err := a.unseal(encrypted)
	if err != nil || plain != "old-device-credential" {
		t.Fatal("credential compatibility broken", err)
	}
	if w := request(t, a, "GET", "/api/me", nil, cookie, ""); w.Code != 200 {
		t.Fatal("existing session lost", w.Body.String())
	}
}
