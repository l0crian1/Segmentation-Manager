package app

import "testing"

// Include unknown, custom, older and future versions so this cannot become an
// allow-list again. These fixtures test version independence, not vendor support.
var releaseFixtures = []string{
	"VyOS 1.3.8", "VyOS 1.4.4", "VyOS 1.5.1", "VyOS 1.6.0",
	"VyOS 2026.09.17-0028-rolling", "VyOS 2026.10.12-0100-rolling",
	"VyOS 1.5-stream-custom", "VyOS 9.0.0-development", "Unknown", "",
}

func releaseVRFFixture() M {
	raw := routerFixture()
	for _, name := range []string{"A", "B"} {
		vrf := object(at(raw, "name", name))
		af := object(at(vrf, "protocols", "bgp", "address-family"))
		af["ipv6-unicast"] = cloneConfig(object(af["ipv4-unicast"]))
		rt := str(at(af, "ipv4-unicast", "route-target", "vpn", "export"))
		vrf["vni"] = "10" + str(vrf["table"])
		af["l2vpn-evpn"] = M{"route-target": M{"export": []any{rt}}}
	}
	return raw
}

func TestVRFManagementAllowsEveryRelease(t *testing.T) {
	for _, version := range releaseFixtures {
		t.Run(version, func(t *testing.T) {
			s := inventoryFromConfig(M{}, version, Device{})
			if !s.Writable {
				t.Fatal("release made inventory read-only")
			}
			b := newVRFIntent()
			b.VRF.IPv6 = b.VRF.IPv4
			p, err := buildVRFPlan(s, b, false)
			if err != nil || len(p.Commands) == 0 {
				t.Fatal("create rejected", err)
			}
			s = inventoryFromConfig(p.Expected, version, Device{})
			b = VRFIntent{Action: "update", VRF: s.VRFs[0]}
			b.VRF.Description = "Edited on any release"
			p, err = buildVRFPlan(s, b, false)
			if err != nil || len(p.Commands) != 1 {
				t.Fatal("update rejected or changed unrelated fields", err, p.Commands)
			}
			s = inventoryFromConfig(p.Expected, version, Device{})
			p, err = buildVRFPlan(s, VRFIntent{Action: "delete", VRF: s.VRFs[0], ConfirmName: b.VRF.Name}, false)
			if err != nil || len(p.Commands) != 1 || len(inventoryFromConfig(p.Expected, version, Device{}).VRFs) != 0 {
				t.Fatal("delete rejected", err)
			}
		})
	}
}
