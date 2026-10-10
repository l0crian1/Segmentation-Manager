package app

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func mapName(value string) *string { return &value }

func routeMapFixture(t *testing.T, evpn bool) VRFInventory {
	t.Helper()
	b := newVRFIntent()
	b.VRF.IPv6 = b.VRF.IPv4
	b.VRF.EVPN.Enabled = evpn
	raw := M{"policy": M{"route-map": M{
		"V4-IN": M{}, "V4-OUT": M{}, "V6-IN": M{}, "V6-OUT": M{}, "REPLACEMENT": M{},
	}}}
	p, err := buildVRFPlan(managementInventory(raw), b, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"ipv4-unicast", "ipv6-unicast"} {
		base := []string{"vrf", "name", "TEST", "protocols", "bgp", "address-family", family}
		configSet(p.Expected, append(slices.Clone(base), "route-target", "vpn", "import"), "65000:999 65000:777")
		configSet(p.Expected, append(slices.Clone(base), "route-target", "vpn", "export"), "65000:888 65000:3901")
		configSet(p.Expected, append(slices.Clone(base), "redistribute", "connected", "route-map"), "OTHER")
		configSet(p.Expected, append(slices.Clone(base), "route-map", "vrf", "import"), "OTHER")
	}
	return managementInventory(p.Expected)
}

func TestVRFRouteMapCreationAndDiscovery(t *testing.T) {
	for _, evpn := range []bool{false, true} {
		s := routeMapFixture(t, evpn)
		b := newVRFIntent()
		b.VRF.Name, b.VRF.Table, b.VRF.EVPN.VNI = "SECOND", "3902", "63902"
		b.VRF.EVPN.Enabled = evpn
		b.VRF.IPv6 = b.VRF.IPv4
		b.VRF.IPv4.ImportRouteMap, b.VRF.IPv4.ExportRouteMap = mapName("V4-IN"), mapName("V4-OUT")
		b.VRF.IPv6.ImportRouteMap, b.VRF.IPv6.ExportRouteMap = mapName("V6-IN"), mapName("V6-OUT")
		p, err := buildVRFPlan(s, b, false)
		if err != nil {
			t.Fatal(err)
		}
		fresh := managementInventory(p.Expected)
		v := fresh.VRFs[0] // SECOND sorts before TEST.
		if v.Name != "SECOND" || *v.IPv4.ImportRouteMap != "V4-IN" || *v.IPv4.ExportRouteMap != "V4-OUT" || *v.IPv6.ImportRouteMap != "V6-IN" || *v.IPv6.ExportRouteMap != "V6-OUT" {
			t.Fatal("assignments not discovered independently", v)
		}
		if !slices.Equal(fresh.RouteMaps, []string{"REPLACEMENT", "V4-IN", "V4-OUT", "V6-IN", "V6-OUT"}) {
			t.Fatal("route-map dropdown inventory", fresh.RouteMaps)
		}
		if !reflect.DeepEqual(at(s.Raw, "policy"), at(p.Expected, "policy")) {
			t.Fatal("route-map definitions changed")
		}
		unchanged, err := buildVRFPlan(fresh, VRFIntent{Action: "update", VRF: v}, false)
		if err != nil || len(unchanged.Commands) != 0 {
			t.Fatal("unchanged assignments generated commands", err, unchanged.Commands)
		}
	}

	s := managementInventory(M{})
	encoded, _ := json.Marshal(s)
	if !strings.Contains(string(encoded), `"route_maps":[]`) {
		t.Fatal("empty dropdown inventory must be an array", string(encoded))
	}
	s = routeMapFixture(t, true)
	raw := cloneConfig(s.Raw)
	configDelete(raw, []string{"policy", "route-map", "V4-IN"})
	configSet(raw, []string{"policy", "route-map", "ADDED-OUTSIDE"}, M{"rule": M{"10": M{"action": "permit"}}})
	fresh := managementInventory(raw)
	if slices.Contains(fresh.RouteMaps, "V4-IN") || !slices.Contains(fresh.RouteMaps, "ADDED-OUTSIDE") || fresh.Fingerprint == s.Fingerprint {
		t.Fatal("external changes did not refresh choices and baseline", fresh)
	}
}

func TestVRFRouteMapAssignmentsAreIndependent(t *testing.T) {
	for _, evpn := range []bool{false, true} {
		for _, family := range []string{"ipv4-unicast", "ipv6-unicast"} {
			for _, direction := range []string{"import", "export"} {
				s := routeMapFixture(t, evpn)
				for _, af := range []string{"ipv4-unicast", "ipv6-unicast"} {
					for _, dir := range []string{"import", "export"} {
						configSet(s.Raw, []string{"vrf", "name", "TEST", "protocols", "bgp", "address-family", af, "route-map", "vpn", dir}, "V4-IN")
					}
				}
				path := []string{"vrf", "name", "TEST", "protocols", "bgp", "address-family", family, "route-map", "vpn", direction}
				for _, value := range []string{"REPLACEMENT", "", "V6-OUT"} {
					s = managementInventory(s.Raw)
					before := cloneConfig(s.Raw)
					v := s.VRFs[0]
					vpn := &v.IPv4
					if family == "ipv6-unicast" {
						vpn = &v.IPv6
					}
					if direction == "import" {
						vpn.ImportRouteMap = mapName(value)
					} else {
						vpn.ExportRouteMap = mapName(value)
					}
					p, err := buildVRFPlan(s, VRFIntent{Action: "update", VRF: v}, false)
					if err != nil {
						t.Fatal(family, direction, value, err)
					}
					expected := cloneConfig(before)
					command := Command{Op: "delete", Path: path}
					if value == "" {
						configDelete(expected, path)
					} else {
						configSet(expected, path, value)
						command = Command{Op: "set", Path: append(slices.Clone(path), value)}
					}
					if !reflect.DeepEqual(p.Commands, []Command{command}) || !reflect.DeepEqual(p.Expected, expected) || !reflect.DeepEqual(s.Raw, before) {
						t.Fatal("assignment modified unrelated VRF configuration", family, direction, value, p.Commands)
					}
					s = managementInventory(p.Expected)
					if !verifyVRFPlan(p, s) {
						t.Fatal("own result not verified")
					}
					configSet(expected, path, "UNEXPECTED")
					if verifyVRFPlan(p, managementInventory(expected)) {
						t.Fatal("incorrect route-map assignment verified")
					}
				}
			}
		}
	}
}

func TestVRFRouteMapValidationAndLegacyClients(t *testing.T) {
	s := routeMapFixture(t, true)
	path := []string{"vrf", "name", "TEST", "protocols", "bgp", "address-family", "ipv4-unicast", "route-map", "vpn", "import"}
	configSet(s.Raw, path, "MISSING")
	s = managementInventory(s.Raw)
	if *s.VRFs[0].IPv4.ImportRouteMap != "MISSING" {
		t.Fatal("missing assignment must still be displayed")
	}
	for _, value := range []string{"NOT-CONFIGURED", "V4-IN\nset policy", "v4-in"} {
		v := s.VRFs[0]
		v.IPv4.ExportRouteMap = mapName(value)
		if _, err := buildVRFPlan(s, VRFIntent{Action: "update", VRF: v}, false); err == nil {
			t.Fatal("nonexistent route-map accepted", value)
		}
	}
	// An unrelated edit or older client must not remove a configured assignment,
	// even if the policy definition was removed outside the application.
	v := s.VRFs[0]
	v.IPv4.ImportRouteMap, v.IPv4.ExportRouteMap = nil, nil
	v.IPv6.ImportRouteMap, v.IPv6.ExportRouteMap = nil, nil
	v.Description = "Updated by an older client"
	p, err := buildVRFPlan(s, VRFIntent{Action: "update", VRF: v}, false)
	if err != nil || len(p.Commands) != 1 || at(p.Expected, path...) != "MISSING" {
		t.Fatal("omitted fields did not preserve assignments", err, p.Commands)
	}
	v = s.VRFs[0]
	v.IPv4.ImportRouteMap = mapName("")
	p, err = buildVRFPlan(s, VRFIntent{Action: "update", VRF: v}, false)
	if err != nil || len(p.Commands) != 1 || at(p.Expected, path...) != nil {
		t.Fatal("could not clear missing assignment", err, p.Commands)
	}
	// VyOS can retain empty parent containers after deleting the last binding.
	fresh := cloneConfig(p.Expected)
	configSet(fresh, path[:len(path)-1], M{})
	if !verifyVRFPlan(p, managementInventory(fresh)) {
		t.Fatal("empty route-map parent rejected")
	}
	v = s.VRFs[0]
	v.IPv4.Enabled = false
	if _, err := buildVRFPlan(s, VRFIntent{Action: "update", VRF: v}, false); err == nil {
		t.Fatal("disabled MPLS without acknowledging policy removal")
	}
	p, err = buildVRFPlan(s, VRFIntent{Action: "update", VRF: v, RemovePolicies: true}, false)
	if err != nil || at(p.Expected, path...) != nil || !reflect.DeepEqual(at(p.Expected, "policy"), at(s.Raw, "policy")) {
		t.Fatal("disabling MPLS did not remove only its assignments", err)
	}
}
