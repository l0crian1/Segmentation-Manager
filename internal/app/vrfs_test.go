package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func newVRFIntent() VRFIntent {
	return VRFIntent{Action: "create", VRF: ManagedVRF{Name: "TEST", Table: "3901", ASN: "65000", IPv4: VPNFamily{Enabled: true, RD: "65000:3901", Label: "auto", Exports: []string{"65000:3901"}}, EVPN: EVPNSettings{Enabled: true, VNI: "63901", Exports: []string{"65000:3901"}, IPv4: true}}}
}
func managementInventory(raw M) VRFInventory { return inventoryFromConfig(raw, "VyOS 1.5.0", Device{}) }

func TestPerNexthopLabelAllocation(t *testing.T) {
	for _, family := range []string{"ipv4-unicast", "ipv6-unicast"} {
		t.Run(family, func(t *testing.T) {
			b := newVRFIntent()
			if family == "ipv6-unicast" {
				b.VRF.IPv6 = b.VRF.IPv4
				b.VRF.IPv4.Enabled = false
			}
			p, e := buildVRFPlan(managementInventory(M{}), b, false)
			if e != nil {
				t.Fatal(e)
			}
			path := []string{"vrf", "name", "TEST", "protocols", "bgp", "address-family", family, "label", "vpn", "allocation-mode", "per-nexthop"}
			importPath := []string{"vrf", "name", "TEST", "protocols", "bgp", "address-family", family, "route-target", "vpn", "import"}
			configSet(p.Expected, importPath, "65000:777")
			for _, enabled := range []bool{true, false} {
				s := managementInventory(p.Expected)
				v := s.VRFs[0]
				if family == "ipv4-unicast" {
					v.IPv4.PerNexthop = &enabled
				} else {
					v.IPv6.PerNexthop = &enabled
				}
				p, e = buildVRFPlan(s, VRFIntent{Action: "update", VRF: v}, false)
				if e != nil {
					t.Fatal(e)
				}
				if len(p.Commands) != 1 {
					t.Fatal("toggle touched other fields", p.Commands)
				}
				expectedPath := path
				op := "delete"
				if enabled {
					op = "set"
					expectedPath = path
				}
				if p.Commands[0].Op != op || !reflect.DeepEqual(p.Commands[0].Path, expectedPath) {
					t.Fatal(p.Commands)
				}
				if at(p.Expected, importPath...) != "65000:777" {
					t.Fatal("import policy changed")
				}
				fresh := managementInventory(p.Expected)
				vpn := fresh.VRFs[0].IPv4
				if family == "ipv6-unicast" {
					vpn = fresh.VRFs[0].IPv6
				}
				if vpn.PerNexthop == nil || *vpn.PerNexthop != enabled {
					t.Fatal("read-back lost allocation mode")
				}
				unchanged, e := buildVRFPlan(fresh, VRFIntent{Action: "update", VRF: fresh.VRFs[0]}, false)
				if e != nil || len(unchanged.Commands) != 0 {
					t.Fatal("no-op edit rewrote allocation", e, unchanged.Commands)
				}
				if enabled {
					older := fresh.VRFs[0]
					if family == "ipv4-unicast" {
						older.IPv4.PerNexthop = nil
					} else {
						older.IPv6.PerNexthop = nil
					}
					older.Description = "Older client"
					preserved, e := buildVRFPlan(fresh, VRFIntent{Action: "update", VRF: older}, false)
					if e != nil || at(preserved.Expected, path...) == nil {
						t.Fatal("omitted field reset existing mode", e)
					}
				}
			}
		})
	}
}
func TestVRFCreateCapabilitiesAndScope(t *testing.T) {
	for _, mode := range []string{"plain", "mpls", "evpn", "both"} {
		t.Run(mode, func(t *testing.T) {
			b := newVRFIntent()
			b.VRF.IPv4.Enabled = mode == "mpls" || mode == "both"
			b.VRF.EVPN.Enabled = mode == "evpn" || mode == "both"
			raw := M{"interfaces": M{"bridge": M{"br0": M{"description": "Keep me"}}}, "protocols": M{"bgp": M{"system-as": "65000"}}}
			p, e := buildVRFPlan(managementInventory(raw), b, false)
			if e != nil {
				t.Fatal(e)
			}
			for _, c := range p.Commands {
				if strings.Join(c.Path[:3], " ") != "vrf name TEST" {
					t.Fatal("write escaped VRF scope", c)
				}
			}
			if !reflect.DeepEqual(raw["interfaces"], p.Expected["interfaces"]) || !reflect.DeepEqual(raw["protocols"], p.Expected["protocols"]) {
				t.Fatal("infrastructure changed")
			}
			rows := managementInventory(p.Expected).VRFs
			if len(rows) != 1 || rows[0].IPv4.Enabled != b.VRF.IPv4.Enabled || rows[0].EVPN.Enabled != b.VRF.EVPN.Enabled {
				t.Fatal(rows)
			}
			if mode == "both" && at(p.Expected, "vrf", "name", "TEST", "protocols", "bgp", "address-family", "ipv4-unicast", "import", "vpn") == nil {
				t.Fatal("missing MPLS import participation")
			}
		})
	}
}
func TestVRFUpdatePreservesImportsAndOtherRouting(t *testing.T) {
	p, e := buildVRFPlan(managementInventory(M{}), newVRFIntent(), false)
	if e != nil {
		t.Fatal(e)
	}
	root := []string{"vrf", "name", "TEST", "protocols", "bgp", "address-family"}
	configSet(p.Expected, append(root, "ipv4-unicast", "route-target", "vpn", "import"), "65000:777 65000:888")
	configSet(p.Expected, append(root, "l2vpn-evpn", "route-target", "import"), []any{"65000:777"})
	configSet(p.Expected, append(root, "ipv4-unicast", "redistribute", "connected", "route-map"), "POLICY")
	s := managementInventory(p.Expected)
	before := cloneConfig(s.Raw)
	b := VRFIntent{Action: "update", VRF: s.VRFs[0]}
	b.VRF.Description = "edited"
	b.VRF.IPv4.Exports = []string{"65000:44"}
	b.VRF.EVPN.Exports = []string{"65000:55"}
	next, e := buildVRFPlan(s, b, false)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(s.Raw, before) {
		t.Fatal("mutated baseline")
	}
	for _, path := range [][]string{{"ipv4-unicast", "route-target", "vpn", "import"}, {"l2vpn-evpn", "route-target", "import"}, {"ipv4-unicast", "redistribute"}} {
		full := append(root, path...)
		if !reflect.DeepEqual(at(before, full...), at(next.Expected, full...)) {
			t.Fatal("unrelated policy changed", path)
		}
	}
	b.VRF.IPv4.Enabled = false
	b.VRF.EVPN.Enabled = false
	if _, e = buildVRFPlan(s, b, false); e == nil {
		t.Fatal("policy removal not acknowledged")
	}
	b.RemovePolicies = true
	next, e = buildVRFPlan(s, b, false)
	if e != nil {
		t.Fatal(e)
	}
	if at(next.Expected, append(root, "ipv4-unicast", "redistribute", "connected", "route-map")...) != "POLICY" {
		t.Fatal("redistribution removed")
	}
}
func TestVRFDeleteDependencyProtection(t *testing.T) {
	p, _ := buildVRFPlan(managementInventory(M{}), newVRFIntent(), false)
	for _, path := range [][]string{{"interfaces", "dummy", "dum1", "vrf"}, {"interfaces", "bridge", "br0", "vif", "10", "vrf"}, {"service", "ssh", "vrf"}, {"protocols", "static", "route", "192.0.2.0/24", "next-hop", "192.0.2.1", "next-hop-vrf"}, {"vrf", "name", "OTHER", "protocols", "bgp", "address-family", "ipv4-unicast", "import", "vrf"}} {
		raw := cloneConfig(p.Expected)
		configSet(raw, path, "TEST")
		s := managementInventory(raw)
		b := VRFIntent{Action: "delete", VRF: ManagedVRF{Name: "TEST"}, ConfirmName: "TEST"}
		if _, e := buildVRFPlan(s, b, false); e == nil {
			t.Fatal("dependency ignored", path)
		}
	}
	b := VRFIntent{Action: "delete", VRF: ManagedVRF{Name: "TEST"}}
	s := managementInventory(p.Expected)
	if _, e := buildVRFPlan(s, b, false); e == nil {
		t.Fatal("confirmation missing")
	}
	b.ConfirmName = "TEST"
	p, e := buildVRFPlan(s, b, false)
	if e != nil || len(p.Commands) != 1 || at(p.Expected, "vrf", "name", "TEST") != nil {
		t.Fatal(e, p.Commands)
	}
}
func TestVRFValidation(t *testing.T) {
	for _, modify := range []func(*VRFIntent){
		func(b *VRFIntent) { b.VRF.Name = "eth1" }, func(b *VRFIntent) { b.VRF.Table = "99" }, func(b *VRFIntent) { b.VRF.ASN = "0" }, func(b *VRFIntent) { b.VRF.IPv4.RD = "bad" }, func(b *VRFIntent) { b.VRF.IPv4.Label = "-1" }, func(b *VRFIntent) { b.VRF.IPv4.Exports = nil }, func(b *VRFIntent) { b.VRF.EVPN.Exports = []string{"auto"} }, func(b *VRFIntent) { b.VRF.EVPN.VNI = "16777216" }, func(b *VRFIntent) { b.VRF.EVPN.IPv4 = false },
	} {
		b := newVRFIntent()
		modify(&b)
		if _, e := buildVRFPlan(managementInventory(M{}), b, false); e == nil {
			t.Fatal("invalid intent accepted", b)
		}
	}
	b := newVRFIntent()
	p, _ := buildVRFPlan(managementInventory(M{}), b, false)
	s := managementInventory(p.Expected)
	if _, e := buildVRFPlan(s, b, false); e == nil {
		t.Fatal("duplicate name accepted")
	}
	b.VRF.Name = "OTHER"
	if _, e := buildVRFPlan(s, b, false); e == nil {
		t.Fatal("duplicate table accepted")
	}
	b.VRF.Table = "3902"
	if _, e := buildVRFPlan(s, b, false); e == nil {
		t.Fatal("duplicate VNI accepted")
	}
	b = VRFIntent{Action: "update", VRF: s.VRFs[0]}
	b.VRF.Table = "4000"
	if _, e := buildVRFPlan(s, b, false); e == nil {
		t.Fatal("changed existing table")
	}
	s.Version = "VyOS 1.4.0"
	s.Writable = false
	if _, e := buildVRFPlan(s, newVRFIntent(), false); e == nil {
		t.Fatal("unsupported write")
	}
}
func TestVRFVerificationDetectsDriftAndMissingFlags(t *testing.T) {
	p, _ := buildVRFPlan(managementInventory(M{}), newVRFIntent(), false)
	s := managementInventory(cloneConfig(p.Expected))
	if !verifyVRFPlan(p, s) {
		t.Fatal("own result rejected")
	}
	configDelete(s.Raw, []string{"vrf", "name", "TEST", "protocols", "bgp", "address-family", "ipv4-unicast", "import", "vpn"})
	if verifyVRFPlan(p, s) {
		t.Fatal("missing import flag accepted")
	}
	s = managementInventory(cloneConfig(p.Expected))
	configSet(s.Raw, []string{"interfaces", "ethernet", "eth0", "description"}, "external edit")
	if verifyVRFPlan(p, s) {
		t.Fatal("unrelated change accepted as success")
	}

	s = managementInventory(cloneConfig(p.Expected))
	configSet(s.Raw, []string{"protocols", "bgp", "neighbor", "192.0.2.1", "address-family", "ipv4-unicast"}, M{})
	if verifyVRFPlan(p, s) {
		t.Fatal("external neighbor AF flag was normalized away")
	}
	encoded, _ := json.Marshal(s)
	if strings.Contains(string(encoded), "external edit") {
		t.Fatal("raw config exposed")
	}
}
func TestVRFAsyncApplyAndStaleGuard(t *testing.T) {
	a := testApp(t)
	cookie, csrf := setupTest(t, a)
	cookie, csrf = accountForRole(t, a, cookie, csrf, roleNetworkAdmin)
	var mu sync.Mutex
	raw := M{"policy": M{"route-map": M{"VPN-POLICY": M{}}}}
	var writes atomic.Int32
	intent := newVRFIntent()
	intent.VRF.IPv4.ImportRouteMap = mapName("VPN-POLICY")
	planned, _ := buildVRFPlan(managementInventory(raw), intent, false)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var data any = raw
		if r.URL.Path == "/show" {
			data = "Version: VyOS 1.5.0\n"
		}
		if r.URL.Path == "/configure" {
			writes.Add(1)
			raw = cloneConfig(planned.Expected)
			w.WriteHeader(504)
			w.Write([]byte(`{"success":false,"error":"timeout after commit"}`))
			return
		}
		json.NewEncoder(w).Encode(M{"success": true, "data": data})
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	secret, _ := a.seal("key")
	a.db.Exec("INSERT INTO sites(name) VALUES('Lab')")
	a.db.Exec("INSERT INTO devices(site_id,name,host,port,trust,fingerprint,save,secret) VALUES(1,'router',?,?,'pin',?,0,?)", u.Hostname(), port, fingerprint(srv.Certificate()), secret)
	d, _ := a.device(1)
	router, _ := a.router(d)
	s, e := router.vrfInventory(context.Background(), d)
	if e != nil {
		t.Fatal(e)
	}
	intent.DeviceID = 1
	intent.OperationID = "vrf-creation-test-001"
	intent.Fingerprint = "stale"
	if w := request(t, a, "POST", "/api/vrfs/apply", intent, cookie, csrf); w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	intent.Fingerprint = s.Fingerprint
	// Removing a policy after discovery invalidates the entire intent before any write.
	mu.Lock()
	configDelete(raw, []string{"policy", "route-map", "VPN-POLICY"})
	mu.Unlock()
	if w := request(t, a, "POST", "/api/vrfs/apply", intent, cookie, csrf); w.Code != 409 {
		t.Fatal("route-map removal was not treated as stale", w.Code, w.Body.String())
	}
	mu.Lock()
	configSet(raw, []string{"policy", "route-map", "VPN-POLICY"}, M{})
	mu.Unlock()
	if w := request(t, a, "POST", "/api/vrfs/plan", intent, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if writes.Load() != 0 {
		t.Fatal("preview wrote configuration")
	}
	if w := request(t, a, "POST", "/api/vrfs/apply", intent, cookie, csrf); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	result := waitOperation(t, a, cookie, intent.OperationID)
	if result["status"] != "applied-unsaved" {
		t.Fatal(result)
	}
	if w := request(t, a, "POST", "/api/vrfs/apply", intent, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if writes.Load() != 1 {
		t.Fatal("configure replayed")
	}
	intent.VRF.Description = "changed"
	if w := request(t, a, "POST", "/api/vrfs/apply", intent, cookie, csrf); w.Code != 409 {
		t.Fatal("ID reused")
	}
}
