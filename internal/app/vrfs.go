package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Management models contain configuration only, never the full router response.
type VPNFamily struct {
	ImportRouteMap *string  `json:"import_route_map"`
	ExportRouteMap *string  `json:"export_route_map"`
	PerNexthop     *bool    `json:"per_nexthop"`
	Enabled        bool     `json:"enabled"`
	RD             string   `json:"rd"`
	Exports        []string `json:"exports"`
	Imports        []string `json:"imports"`
	Label          string   `json:"label"`
}
type EVPNSettings struct {
	Enabled bool     `json:"enabled"`
	VNI     string   `json:"vni"`
	RD      string   `json:"rd"`
	Exports []string `json:"exports"`
	Imports []string `json:"imports"`
	IPv4    bool     `json:"ipv4"`
	IPv6    bool     `json:"ipv6"`
}
type ManagedVRF struct {
	Name         string       `json:"name"`
	Table        string       `json:"table"`
	Description  string       `json:"description"`
	ASN          string       `json:"asn"`
	IPv4         VPNFamily    `json:"ipv4"`
	IPv6         VPNFamily    `json:"ipv6"`
	EVPN         EVPNSettings `json:"evpn"`
	Dependencies []string     `json:"dependencies"`
}
type VRFInventory struct {
	RouteMaps   []string     `json:"route_maps"`
	DeviceID    int64        `json:"device_id"`
	Version     string       `json:"version"`
	Fingerprint string       `json:"fingerprint"`
	Writable    bool         `json:"writable"`
	GlobalASN   string       `json:"global_asn"`
	VRFs        []ManagedVRF `json:"vrfs"`
	Raw         M            `json:"-"`
}
type VRFIntent struct {
	DeviceID       int64      `json:"device_id"`
	Fingerprint    string     `json:"fingerprint"`
	OperationID    string     `json:"operation_id"`
	Action         string     `json:"action"`
	VRF            ManagedVRF `json:"vrf"`
	ConfirmName    string     `json:"confirm_name"`
	RemovePolicies bool       `json:"remove_policies"`
}
type VRFPlan struct {
	Commands []Command `json:"commands"`
	Warnings []string  `json:"warnings"`
	Save     bool      `json:"save"`
	Expected M         `json:"-"`
}

func vrfWritable(v string) bool {
	return strings.Contains(v, "VyOS 1.5.") || strings.Contains(v, "2026.09.17-0028-rolling")
}
func (r *Router) vrfInventory(ctx context.Context, d Device) (VRFInventory, error) {
	v, e := r.version(ctx)
	if e != nil {
		return VRFInventory{}, e
	}
	raw, e := r.read(ctx, []string{}...)
	if e != nil {
		return VRFInventory{}, e
	}
	return inventoryFromConfig(raw, v, d), nil
}
func inventoryFromConfig(raw M, version string, d Device) VRFInventory {
	s := VRFInventory{DeviceID: d.ID, Version: version, Writable: vrfWritable(version), Raw: raw, VRFs: []ManagedVRF{}, RouteMaps: []string{}, GlobalASN: str(at(raw, "protocols", "bgp", "system-as"))}
	for name := range object(at(raw, "policy", "route-map")) {
		s.RouteMaps = append(s.RouteMaps, name)
	}
	slices.Sort(s.RouteMaps)
	for name, v := range object(at(raw, "vrf", "name")) {
		cfg := object(v)
		bgp := object(at(cfg, "protocols", "bgp"))
		af := object(bgp["address-family"])
		row := ManagedVRF{Name: name, Table: str(cfg["table"]), Description: str(cfg["description"]), ASN: str(bgp["system-as"]), Dependencies: []string{}}
		for i, f := range []string{"ipv4-unicast", "ipv6-unicast"} {
			a := object(af[f])
			rt := object(at(a, "route-target", "vpn"))
			perNexthop := at(a, "label", "vpn", "allocation-mode", "per-nexthop") != nil
			vpn := VPNFamily{PerNexthop: &perNexthop, Enabled: len(rt) > 0 || at(a, "import", "vpn") != nil || at(a, "export", "vpn") != nil || at(a, "rd", "vpn") != nil || at(a, "label", "vpn") != nil, RD: str(at(a, "rd", "vpn", "export")), Label: str(at(a, "label", "vpn", "export")), Exports: union(targets(rt["export"]), targets(rt["both"])), Imports: union(targets(rt["import"]), targets(rt["both"]))}
			importMap := str(at(a, "route-map", "vpn", "import"))
			exportMap := str(at(a, "route-map", "vpn", "export"))
			vpn.ImportRouteMap, vpn.ExportRouteMap = &importMap, &exportMap
			vpn.Enabled = vpn.Enabled || importMap != "" || exportMap != ""
			if i == 0 {
				row.IPv4 = vpn
			} else {
				row.IPv6 = vpn
			}
		}
		ev := object(af["l2vpn-evpn"])
		row.EVPN = EVPNSettings{Enabled: present(cfg, "vni") || present(af, "l2vpn-evpn"), VNI: str(cfg["vni"]), RD: str(ev["route-distinguisher"]), Exports: union(targets(at(ev, "route-target", "export")), targets(at(ev, "route-target", "both"))), Imports: union(targets(at(ev, "route-target", "import")), targets(at(ev, "route-target", "both"))), IPv4: at(ev, "advertise", "ipv4", "unicast") != nil, IPv6: at(ev, "advertise", "ipv6", "unicast") != nil}
		row.Dependencies = vrfDependencies(raw, name)
		s.VRFs = append(s.VRFs, row)
	}
	slices.SortFunc(s.VRFs, func(a, b ManagedVRF) int { return strings.Compare(a.Name, b.Name) })
	b, _ := json.Marshal([]any{raw, version, d.Host, d.Port, d.Trust, d.Fingerprint, d.Save})
	s.Fingerprint = digest(string(b))
	return s
}

// References can be scalar leaves, lists, or tag-node keys (import vrf NAME).
func vrfDependencies(raw M, name string) []string {
	out := []string{}
	var walk func(any, []string)
	walk = func(v any, path []string) {
		if len(path) == 3 && path[0] == "vrf" && path[1] == "name" && path[2] == name {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				p := append(slices.Clone(path), k)
				if (k == "vrf" || k == "vrf-name" || strings.HasSuffix(k, "-vrf")) && (slices.Contains(targets(val), name) || present(object(val), name)) {
					out = append(out, strings.Join(p, " "))
				}
				walk(val, p)
			}
		}
	}
	walk(raw, nil)
	slices.Sort(out)
	return slices.Compact(out)
}
func cloneConfig(m M) M { b, _ := json.Marshal(m); var n M; _ = json.Unmarshal(b, &n); return n }
func configSet(m M, path []string, v any) {
	for _, k := range path[:len(path)-1] {
		if _, ok := m[k].(map[string]any); !ok {
			m[k] = M{}
		}
		m = object(m[k])
	}
	m[path[len(path)-1]] = v
}
func configDelete(m M, path []string) {
	if len(path) == 1 {
		delete(m, path[0])
		return
	}
	child, ok := m[path[0]].(map[string]any)
	if !ok {
		return
	}
	configDelete(child, path[1:])
	if len(child) == 0 {
		delete(m, path[0])
	}
}

var managedName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,14}$`)
var interfacePrefix = regexp.MustCompile(`^(bond|br|dum|eth|lan|eno|ens|enp|enx|gnv|ipoe|l2tp|vtun|ppp|peth|tun|vti|vxlan|wg|wlan|wwan)`)

func inRange(v string, lo, hi uint64) bool {
	n, e := strconv.ParseUint(v, 10, 32)
	return e == nil && n >= lo && n <= hi && strconv.FormatUint(n, 10) == v
}
func validateExports(rt []string) error {
	if len(rt) == 0 || len(rt) > 128 {
		return errors.New("configure between 1 and 128 explicit export route-targets")
	}
	for _, t := range rt {
		if !validRT(t) {
			return fmt.Errorf("invalid route-target %q", t)
		}
	}
	return nil
}

func buildVRFPlan(s VRFInventory, b VRFIntent, save bool) (VRFPlan, error) {
	p := VRFPlan{Commands: []Command{}, Warnings: []string{}, Save: save, Expected: cloneConfig(s.Raw)}
	bad := func(msg string) (VRFPlan, error) { return p, errors.New(msg) }
	if !s.Writable {
		return bad("VRF management requires VyOS 1.5 or a tested rolling build")
	}
	v := b.VRF
	base := []string{"vrf", "name", v.Name}
	old := object(at(s.Raw, base...))
	exists := len(old) > 0
	var prior ManagedVRF
	for _, r := range s.VRFs {
		if r.Name == v.Name {
			prior = r
		}
	}
	del := func(path ...string) {
		if at(p.Expected, path...) != nil {
			p.Commands = append(p.Commands, Command{Op: "delete", Path: path})
			configDelete(p.Expected, path)
		}
	}
	set := func(value any, path ...string) {
		if reflect.DeepEqual(at(p.Expected, path...), value) {
			return
		}
		cmd := slices.Clone(path)
		if val, ok := value.(string); ok {
			cmd = append(cmd, val)
		}
		p.Commands = append(p.Commands, Command{Op: "set", Path: cmd})
		configSet(p.Expected, path, value)
	}
	leaf := func(value string, path ...string) {
		if value == "" {
			del(path...)
		} else {
			set(value, path...)
		}
	}
	bp := func(path ...string) []string { return append(slices.Clone(base), path...) }
	if b.Action == "delete" {
		if !exists {
			return bad("VRF no longer exists")
		}
		if b.ConfirmName != v.Name {
			return bad("type the VRF name to confirm deletion")
		}
		deps := vrfDependencies(p.Expected, v.Name)
		if len(deps) > 0 {
			return bad("Detach these references on VyOS before deleting: " + strings.Join(deps, ", "))
		}
		del(base...)
		p.Warnings = append(p.Warnings, "Deletes all routing configuration owned by this VRF. Other interfaces and resources are retained.")
		return p, nil
	}
	if b.Action != "create" && b.Action != "update" {
		return bad("unknown VRF action")
	}
	if b.Action == "create" && exists {
		return bad("VRF name already exists")
	}
	if b.Action == "update" && !exists {
		return bad("VRF no longer exists")
	}
	if !exists && (!managedName.MatchString(v.Name) || interfacePrefix.MatchString(v.Name) || v.Name == "lo" || v.Name == "default") {
		return bad("use a unique VRF name of 1–15 letters, digits, underscores or hyphens, starting with a letter; interface names are reserved")
	}
	if !inRange(v.Table, 100, 65535) {
		return bad("routing table must be between 100 and 65535")
	}
	if exists && v.Table != prior.Table {
		return bad("routing table IDs are immutable; VyOS requires VRF recreation")
	}
	if len(v.Description) > 256 || strings.ContainsAny(v.Description, "\r\n\x00") {
		return bad("description must be a single line of at most 256 characters")
	}
	for _, other := range s.VRFs {
		if other.Name != v.Name && other.Table == v.Table {
			return bad("routing table ID is already assigned")
		}
	}
	active := v.IPv4.Enabled || v.IPv6.Enabled || v.EVPN.Enabled
	if active && !inRange(v.ASN, 1, 4294967295) {
		return bad("BGP ASN must be between 1 and 4294967295")
	}
	if exists && v.ASN != prior.ASN && len(object(at(old, "protocols", "bgp", "neighbor"))) > 0 {
		return bad("this VRF has BGP neighbors; change its ASN on VyOS after reviewing those peerings")
	}
	set(v.Table, bp("table")...)
	leaf(v.Description, bp("description")...)
	if active {
		set(v.ASN, bp("protocols", "bgp", "system-as")...)
	}
	for i, f := range []string{"ipv4-unicast", "ipv6-unicast"} {
		want, was := v.IPv4, prior.IPv4
		if i == 1 {
			want, was = v.IPv6, prior.IPv6
		}
		ap := func(path ...string) []string {
			return bp(append([]string{"protocols", "bgp", "address-family", f}, path...)...)
		}
		// Omitted fields from older clients preserve existing assignments.
		if want.ImportRouteMap == nil {
			want.ImportRouteMap = was.ImportRouteMap
		}
		if want.ExportRouteMap == nil {
			want.ExportRouteMap = was.ExportRouteMap
		}
		if want.PerNexthop == nil {
			want.PerNexthop = was.PerNexthop
		}
		if want.Enabled {
			for _, binding := range []struct {
				direction string
				value     *string
			}{{"import", want.ImportRouteMap}, {"export", want.ExportRouteMap}} {
				if binding.value == nil || *binding.value == str(at(old, "protocols", "bgp", "address-family", f, "route-map", "vpn", binding.direction)) {
					continue
				}
				if *binding.value != "" {
					if _, found := object(at(s.Raw, "policy", "route-map"))[*binding.value]; !found {
						return bad(f + ": VPN " + binding.direction + " route-map is not configured on the router; refresh and select an existing route-map")
					}
				}
				leaf(*binding.value, ap("route-map", "vpn", binding.direction)...)
			}
		}
		// Assignment-only edits must not normalize or rewrite other VPN settings.
		want.ImportRouteMap, want.ExportRouteMap = was.ImportRouteMap, was.ExportRouteMap
		if reflect.DeepEqual(want, was) && exists {
			continue
		}
		afi := object(at(old, "protocols", "bgp", "address-family", f))
		if at(afi, "route-target", "vpn", "both") != nil || at(afi, "import", "vrf") != nil {
			return bad(f + ": combined targets or shortcut leaking must be edited on VyOS")
		}
		if !want.Enabled {
			if !was.Enabled {
				continue
			}
			if !b.RemovePolicies {
				return bad("confirm removal of import/export VPN policies when disabling MPLS")
			}
			for _, path := range [][]string{{"import", "vpn"}, {"export", "vpn"}, {"rd", "vpn"}, {"label", "vpn"}, {"route-target", "vpn"}} {
				del(ap(path...)...)
			}
			del(ap("route-map", "vpn", "import")...)
			del(ap("route-map", "vpn", "export")...)
			p.Warnings = append(p.Warnings, f+": MPLS VPN import/export policy will be removed; redistribution and other routing settings stay unchanged.")
			continue
		}
		if !validRT(want.RD) {
			return bad(f + ": enter a valid route distinguisher")
		}
		if e := validateExports(want.Exports); e != nil {
			return p, e
		}
		if want.Label != "auto" && !inRange(want.Label, 16, 1048575) {
			return bad("VPN label must be auto or a number between 16 and 1048575")
		}
		set(M{}, ap("import", "vpn")...)
		set(M{}, ap("export", "vpn")...)
		set(want.RD, ap("rd", "vpn", "export")...)
		set(want.Label, ap("label", "vpn", "export")...)
		// Omitted by older clients: preserve the existing allocation mode.
		if want.PerNexthop != nil {
			if *want.PerNexthop {
				set(M{}, ap("label", "vpn", "allocation-mode", "per-nexthop")...)
			} else {
				del(ap("label", "vpn", "allocation-mode", "per-nexthop")...)
			}
		}
		set(strings.Join(union(want.Exports, nil), " "), ap("route-target", "vpn", "export")...)
	}
	ep := func(path ...string) []string {
		return bp(append([]string{"protocols", "bgp", "address-family", "l2vpn-evpn"}, path...)...)
	}
	if !reflect.DeepEqual(v.EVPN, prior.EVPN) || !exists {
		if at(old, "protocols", "bgp", "address-family", "l2vpn-evpn", "route-target", "both") != nil {
			return bad("combined EVPN route-targets must be split on VyOS before editing")
		}
		if v.EVPN.Enabled {
			if !inRange(v.EVPN.VNI, 1, 16777215) {
				return bad("L3VNI must be between 1 and 16777215")
			}
			if e := validateExports(v.EVPN.Exports); e != nil {
				return p, e
			}
			if v.EVPN.RD != "" && !validRT(v.EVPN.RD) {
				return bad("enter a valid EVPN route distinguisher or leave it automatic")
			}
			if !v.EVPN.IPv4 && !v.EVPN.IPv6 {
				return bad("select at least one EVPN unicast advertisement family")
			}
			for _, other := range s.VRFs {
				if other.Name != v.Name && other.EVPN.VNI == v.EVPN.VNI {
					return bad("L3VNI is already assigned to another VRF")
				}
			}
			set(v.EVPN.VNI, bp("vni")...)
			leaf(v.EVPN.RD, ep("route-distinguisher")...)
			oldTargets := targets(at(old, "protocols", "bgp", "address-family", "l2vpn-evpn", "route-target", "export"))
			newTargets := union(v.EVPN.Exports, nil)
			// EVPN targets are multi-valued; replace only the export leaf, never imports.
			if !slices.Equal(oldTargets, newTargets) {
				del(ep("route-target", "export")...)
				for _, rt := range newTargets {
					p.Commands = append(p.Commands, Command{Op: "set", Path: append(ep("route-target", "export"), rt)})
				}
				vals := []any{}
				for _, rt := range newTargets {
					vals = append(vals, rt)
				}
				configSet(p.Expected, ep("route-target", "export"), vals)
			}
			for i, f := range []string{"ipv4", "ipv6"} {
				enabled := v.EVPN.IPv4
				if i == 1 {
					enabled = v.EVPN.IPv6
				}
				if enabled {
					set(M{}, ep("advertise", f, "unicast")...)
				} else {
					del(ep("advertise", f, "unicast")...)
				}
			}
		} else if prior.EVPN.Enabled {
			if !b.RemovePolicies {
				return bad("confirm removal of EVPN import/export policy when disabling VXLAN")
			}
			del(bp("vni")...)
			del(ep("route-target")...)
			del(ep("route-distinguisher")...)
			del(ep("advertise", "ipv4", "unicast")...)
			del(ep("advertise", "ipv6", "unicast")...)
			p.Warnings = append(p.Warnings, "EVPN import/export targets and advertisements will be removed. Other BGP settings are retained.")
		}
	}
	if active {
		p.Warnings = append(p.Warnings, "Only VRF and per-VRF BGP settings are changed. Existing MPLS, EVPN peering, VXLAN and bridge infrastructure must provide transport; interfaces and redistribution are not configured here.")
	}
	if exists {
		p.Warnings = append(p.Warnings, "Existing import targets are preserved unless a technology is explicitly disabled. Export changes can affect remote PEs.")
	}
	return p, nil
}

// Normalize only VRF schema containers and route-target representations. Empty
// nodes elsewhere (for example BGP neighbor AF activation) are meaningful flags.
func emptyVRFContainer(path []string) bool {
	if slices.Equal(path, []string{"vrf"}) || slices.Equal(path, []string{"vrf", "name"}) {
		return true
	}
	if len(path) < 4 || path[0] != "vrf" || path[1] != "name" {
		return false
	}
	rel := strings.Join(path[3:], "/")
	if rel == "protocols" || rel == "protocols/bgp" || rel == "protocols/bgp/address-family" {
		return true
	}
	for _, af := range []string{"ipv4-unicast", "ipv6-unicast", "l2vpn-evpn"} {
		prefix := "protocols/bgp/address-family/" + af
		if rel == prefix {
			return true
		}
		if strings.HasPrefix(rel, prefix+"/") {
			tail := strings.TrimPrefix(rel, prefix+"/")
			return slices.Contains([]string{"route-target", "route-target/vpn", "route-map", "route-map/vpn", "rd", "rd/vpn", "label", "label/vpn", "label/vpn/allocation-mode", "import", "export", "advertise", "advertise/ipv4", "advertise/ipv6"}, tail)
		}
	}
	return false
}
func normalizedConfig(v any, path ...string) any {
	switch x := v.(type) {
	case map[string]any:
		out := M{}
		for k, val := range x {
			child := append(slices.Clone(path), k)
			n := normalizedConfig(val, child...)
			if m, ok := n.(map[string]any); ok && len(m) == 0 && emptyVRFContainer(child) {
				continue
			}
			out[k] = n
		}
		return out
	case []any, string:
		if len(path) > 3 && path[0] == "vrf" && path[1] == "name" && slices.Contains(path, "route-target") {
			return strings.Join(targets(x), " ")
		}
	}
	return v
}
func verifyVRFPlan(p VRFPlan, fresh VRFInventory) bool {
	return reflect.DeepEqual(normalizedConfig(p.Expected), normalizedConfig(fresh.Raw))
}
