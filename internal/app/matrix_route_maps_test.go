package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestMPLSMatrixWithVPNRouteMaps(t *testing.T) {
	for _, family := range []string{"ipv4-unicast", "ipv6-unicast"} {
		for _, directions := range [][]string{{"import"}, {"export"}, {"import", "export"}} {
			t.Run(family+"/"+directions[0]+"/"+directions[len(directions)-1], func(t *testing.T) {
				raw := routerFixture()
				base := []string{"name", "A", "protocols", "bgp", "address-family", family}
				if family == "ipv6-unicast" {
					for _, name := range []string{"A", "B"} {
						af := []string{"name", name, "protocols", "bgp", "address-family"}
						configSet(raw, append(slices.Clone(af), family), cloneConfig(object(at(raw, append(af, "ipv4-unicast")...))))
					}
				}
				for _, direction := range directions {
					configSet(raw, append(slices.Clone(base), "route-map", "vpn", direction), "VPN-"+direction)
				}
				configSet(raw, append(slices.Clone(base), "route-target", "vpn", "import"), "65000:999")
				original := cloneConfig(raw)
				var mu sync.Mutex
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					data := any(raw)
					if r.URL.Path == "/show" {
						data = "Version: VyOS 1.5.0\n"
					}
					json.NewEncoder(w).Encode(M{"success": true, "data": data})
				}))
				defer server.Close()
				router := &Router{client: server.Client(), base: server.URL, key: "key"}
				read := func() Snapshot {
					t.Helper()
					s, err := router.snapshot(context.Background(), Device{}, "mpls", family)
					if err != nil {
						t.Fatal(err)
					}
					if len(s.VRFs) != 2 || s.VRFs[0].Reason != "" || s.VRFs[1].Reason != "" {
						t.Fatal("VPN route-map made matrix read-only", s.VRFs)
					}
					return s
				}
				for _, edit := range []Edit{{"A", "B", true}, {"A", "A", true}, {"A", "A", false}, {"A", "B", false}} {
					s := read()
					p, err := buildPlan(s, []Edit{edit}, false)
					if err != nil || len(p.Commands) == 0 {
						t.Fatal("route-target change rejected", edit, err)
					}
					mu.Lock()
					for _, command := range p.Commands {
						path := command.Path[1:]
						if command.Op == "set" {
							path = path[:len(path)-1]
						}
						if !slices.Equal(path[3:], []string{"bgp", "address-family", family, "route-target", "vpn", "import"}) {
							t.Fatal("matrix changed something other than import targets", command)
						}
						if command.Op == "set" {
							configSet(raw, path, command.Path[len(command.Path)-1])
						} else {
							configDelete(raw, path)
						}
					}
					mu.Unlock()
					fresh := read()
					if !verifyPlan(p, fresh) {
						t.Fatal("matrix result with route-maps did not verify")
					}
					mapPath := append(slices.Clone(base), "route-map", "vpn")
					if !reflect.DeepEqual(at(fresh.Raw, mapPath...), at(original, mapPath...)) {
						t.Fatal("route-map assignments were modified")
					}
					fresh.Raw = cloneConfig(fresh.Raw)
					configSet(fresh.Raw, append(mapPath, directions[0]), "EXTERNAL-CHANGE")
					if verifyPlan(p, fresh) {
						t.Fatal("external route-map assignment drift was ignored")
					}
				}
				mu.Lock()
				if !reflect.DeepEqual(raw, original) {
					t.Fatal("connect/disconnect changed original policy")
				}
				mu.Unlock()
			})
		}
	}
}
