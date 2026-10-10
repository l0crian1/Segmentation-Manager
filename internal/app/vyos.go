package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type M = map[string]any
type Router struct {
	client      *http.Client
	writeClient *http.Client
	base, key   string
}
type VRF struct {
	Name    string   `json:"name"`
	Imports []string `json:"imports"`
	Exports []string `json:"exports"`
	VNI     string   `json:"vni,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	Both    bool     `json:"both"`
	Known   bool     `json:"known"`
}
type Snapshot struct {
	DeviceID    int64    `json:"device_id"`
	Mode        string   `json:"mode"`
	Family      string   `json:"family"`
	Version     string   `json:"version"`
	At          string   `json:"at"`
	Fingerprint string   `json:"fingerprint"`
	VRFs        []VRF    `json:"vrfs"`
	Warnings    []string `json:"warnings"`
	Raw         M        `json:"-"`
}
type Edit struct {
	A       string `json:"a"`
	B       string `json:"b"`
	Enabled bool   `json:"enabled"`
}
type Command struct {
	Op   string   `json:"op"`
	Path []string `json:"path"`
}
type Plan struct {
	Commands []Command `json:"commands"`
	Before   Snapshot  `json:"before"`
	After    []VRF     `json:"after"`
	Changes  []Edit    `json:"changes"`
	Save     bool      `json:"save"`
	Warnings []string  `json:"warnings"`
}

func object(x any) M {
	m, _ := x.(map[string]any)
	if m == nil {
		return M{}
	}
	return m
}
func at(m M, path ...string) any {
	var v any = m
	for _, p := range path {
		v = object(v)[p]
	}
	return v
}
func str(x any) string           { s, _ := x.(string); return s }
func present(m M, k string) bool { _, ok := m[k]; return ok }
func targets(v any) []string {
	a := []string{}
	switch x := v.(type) {
	case string:
		a = strings.Fields(x)
	case []any:
		for _, s := range x {
			a = append(a, strings.Fields(str(s))...)
		}
	}
	slices.Sort(a)
	return slices.Compact(a)
}
func union(a, b []string) []string {
	x := append(slices.Clone(a), b...)
	slices.Sort(x)
	return slices.Compact(x)
}
func intersects(a, b []string) bool {
	for _, s := range a {
		if slices.Contains(b, s) {
			return true
		}
	}
	return false
}
func difference(a, b []string) []string {
	x := []string{}
	for _, s := range a {
		if !slices.Contains(b, s) {
			x = append(x, s)
		}
	}
	return x
}
func relationship(a, b VRF) int {
	n := 0
	if intersects(a.Imports, b.Exports) {
		n++
	}
	if intersects(b.Imports, a.Exports) {
		n++
	}
	return n
}

var hostname = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

func validateHost(h string, p int) error {
	if p < 1 || p > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if net.ParseIP(h) == nil && !hostname.MatchString(h) {
		return errors.New("enter a hostname or IP address, without a scheme or path")
	}
	return nil
}
func fingerprint(cert *x509.Certificate) string {
	h := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(h[:])
}
func (a *App) router(d Device) (*Router, error) {
	if e := validateHost(d.Host, d.Port); e != nil {
		return nil, e
	}
	key, e := a.unseal(d.Secret)
	if e != nil {
		return nil, errors.New("device credential cannot be decrypted")
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if d.Trust == "pin" {
		if len(d.Fingerprint) != 64 {
			return nil, errors.New("device certificate fingerprint is required")
		}
		cfg.InsecureSkipVerify = true // Exact certificate pin replaces PKI chain verification, never an unconditional bypass.
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || fingerprint(cs.PeerCertificates[0]) != d.Fingerprint {
				return errors.New("device certificate changed; review its fingerprint")
			}
			c := cs.PeerCertificates[0]
			if time.Now().Before(c.NotBefore) || time.Now().After(c.NotAfter) {
				return errors.New("device certificate is outside its validity period")
			}
			return nil
		}
	}
	tr := &http.Transport{TLSClientConfig: cfg, DialContext: (&net.Dialer{Timeout: time.Duration(a.settings().Timeout) * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: time.Duration(a.settings().Timeout) * time.Second, DisableKeepAlives: true}
	writeTransport := tr.Clone()
	writeTransport.ResponseHeaderTimeout = 10 * time.Minute
	writeClient := &http.Client{Transport: writeTransport, Timeout: 10 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("device redirects are not allowed") }}
	return &Router{writeClient: writeClient, client: &http.Client{Transport: tr, Timeout: time.Duration(a.settings().Timeout) * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("device redirects are not allowed") }}, base: "https://" + net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), key: key}, nil
}
func (r *Router) call(ctx context.Context, endpoint string, data any) (any, error) {
	b, e := json.Marshal(data)
	if e != nil {
		return nil, e
	}
	form := url.Values{"key": {r.key}, "data": {string(b)}}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/"+endpoint, strings.NewReader(form.Encode()))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := r.client
	if (endpoint == "configure" || endpoint == "config-file") && r.writeClient != nil {
		client = r.writeClient
	}
	res, e := client.Do(req)
	if e != nil {
		return nil, errors.New("device request failed: check availability, timeout and certificate trust")
	}
	defer res.Body.Close()
	var envelope struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
		Error   any  `json:"error"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&envelope); e != nil {
		return nil, errors.New("device returned an invalid API response")
	}
	if !envelope.Success || res.StatusCode >= 400 {
		if res.StatusCode == 401 || res.StatusCode == 403 {
			return nil, errors.New("device authentication failed")
		}
		msg := fmt.Sprint(envelope.Error)
		msg = strings.ReplaceAll(msg, r.key, "[redacted]")
		if len(msg) > 700 {
			msg = msg[:700]
		}
		return nil, fmt.Errorf("VyOS rejected request (%d): %s", res.StatusCode, msg)
	}
	return envelope.Data, nil
}
func (r *Router) read(ctx context.Context, path ...string) (M, error) {
	d, e := r.call(ctx, "retrieve", M{"op": "showConfig", "path": path})
	if e != nil && strings.Contains(e.Error(), "Configuration under specified path is empty") {
		return M{}, nil
	}
	if e != nil {
		return nil, e
	}
	m, ok := d.(map[string]any)
	if !ok {
		return nil, errors.New("unexpected configuration response shape")
	}
	return m, nil
}
func (r *Router) version(ctx context.Context) (string, error) {
	d, e := r.call(ctx, "show", M{"op": "show", "path": []string{"version"}})
	if e != nil {
		return "", e
	}
	for _, line := range strings.Split(str(d), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Version:")), nil
		}
	}
	return "Unknown", nil
}
func recognized(v string) bool {
	return strings.Contains(v, "VyOS 1.4.") || strings.Contains(v, "VyOS 1.5.") || strings.Contains(v, "2026.09.17-0028-rolling")
}
func (r *Router) snapshot(ctx context.Context, d Device, mode, family string) (Snapshot, error) {
	s := Snapshot{DeviceID: d.ID, Mode: mode, Family: family, At: time.Now().UTC().Format(time.RFC3339), VRFs: []VRF{}, Warnings: []string{}}
	if mode != "mpls" && mode != "evpn" {
		return s, errors.New("unknown technology")
	}
	if family != "ipv4-unicast" && family != "ipv6-unicast" {
		return s, errors.New("unknown address family")
	}
	v, e := r.version(ctx)
	if e != nil {
		return s, e
	}
	s.Version = v
	raw, e := r.read(ctx, "vrf")
	if e != nil {
		return s, e
	}
	if x, ok := raw["vrf"]; ok {
		raw = object(x)
	}
	s.Raw = raw
	names := object(raw["name"])
	if len(raw) > 0 && !present(raw, "name") {
		return s, errors.New("unrecognized VRF configuration layout")
	}
	for name, val := range names {
		vr := object(val)
		afi := object(at(vr, "protocols", "bgp", "address-family", family))
		rt := object(at(afi, "route-target", "vpn"))
		reason := ""
		vni := ""
		if mode == "evpn" {
			afi = object(at(vr, "protocols", "bgp", "address-family", "l2vpn-evpn"))
			vni = str(vr["vni"])
			if len(afi) == 0 && vni == "" {
				continue
			}
			rt = object(afi["route-target"])
			if vni == "" {
				reason = "No L3VNI configured on this VRF"
			}
		} else {
			if len(rt) == 0 && !present(afi, "export") && !present(afi, "import") {
				continue
			}
			if at(afi, "export", "vpn") == nil || at(afi, "import", "vpn") == nil {
				reason = "VPN import/export is not enabled"
			}
		}
		both := targets(rt["both"])
		imports := union(targets(rt["import"]), both)
		exports := union(targets(rt["export"]), both)
		known := true
		if len(both) > 0 {
			reason = "Combined import/export targets require review outside the import-only editor"
		}
		if mode == "evpn" && len(targets(rt["export"])) == 0 {
			reason = fmt.Sprintf("No explicit EVPN export RT. Configure on VyOS: set vrf name %s protocols bgp address-family l2vpn-evpn route-target export '<ASN:RT>', commit, then refresh.", name)
			known = false
		}
		if len(exports) == 0 && reason == "" {
			reason = "No export route targets"
		}
		if present(object(afi["import"]), "vrf") {
			reason = "VRF shortcut imports are outside route-target management"
		}
		if !recognized(v) {
			reason = "This VyOS build has not been recognized for writes"
		}
		for _, t := range union(imports, exports) {
			if !validRT(t) {
				reason = "Unsupported route-target representation"
				known = false
			}
		}
		s.VRFs = append(s.VRFs, VRF{Name: name, Imports: imports, Exports: exports, VNI: vni, Reason: reason, Both: len(both) > 0, Known: known})
	}
	slices.SortFunc(s.VRFs, func(a, b VRF) int { return strings.Compare(a.Name, b.Name) })
	s.Warnings = append(s.Warnings, "Route-target relationships do not prove packet reachability or isolation.", "Use a single-writer window: concurrent CLI edits cannot be atomically excluded by this API.")
	b, _ := json.Marshal([]any{raw, s.VRFs, v, mode, family, d.Host, d.Port, d.Trust, d.Fingerprint, d.Save})
	s.Fingerprint = digest(string(b))
	return s, nil
}
func validRT(t string) bool {
	p := strings.Split(t, ":")
	if len(p) != 2 {
		return false
	}
	lo, e := strconv.ParseUint(p[1], 10, 32)
	if e != nil {
		return false
	}
	if ip := net.ParseIP(p[0]); ip != nil {
		return ip.To4() != nil && lo <= 65535
	}
	hi, e := strconv.ParseUint(p[0], 10, 32)
	return e == nil && ((hi <= 65535 && lo <= 4294967295) || (hi > 65535 && lo <= 65535))
}
func buildPlan(s Snapshot, edits []Edit, save bool) (Plan, error) {
	p := Plan{Before: s, After: slices.Clone(s.VRFs), Changes: edits, Commands: []Command{}, Save: save, Warnings: []string{}}
	if len(edits) == 0 || len(edits) > 500 {
		return p, errors.New("select between 1 and 500 pair changes")
	}
	index := map[string]int{}
	for i, v := range s.VRFs {
		index[v.Name] = i
	}
	desired := map[string]bool{}
	for _, e := range edits {
		i, ok := index[e.A]
		j, ok2 := index[e.B]
		if !ok || !ok2 {
			return p, errors.New("invalid VRF pair")
		}
		a, b := p.After[i], p.After[j]
		if a.Reason != "" || b.Reason != "" {
			return p, fmt.Errorf("%s / %s is read-only: %s %s", a.Name, b.Name, a.Reason, b.Reason)
		}
		k := pairKey(a.Name, b.Name)
		if _, ok := desired[k]; ok {
			return p, errors.New("duplicate pair change")
		}
		desired[k] = e.Enabled
		if e.Enabled {
			a.Imports = union(a.Imports, b.Exports)
			b.Imports = union(b.Imports, a.Exports)
		} else {
			a.Imports = difference(a.Imports, b.Exports)
			b.Imports = difference(b.Imports, a.Exports)
		}
		p.After[i], p.After[j] = a, b
	}
	for i, a := range p.After {
		for j := i; j < len(p.After); j++ {
			b := p.After[j]
			want, edited := desired[pairKey(a.Name, b.Name)]
			n := relationship(a, b)
			if edited {
				if (want && n != 2) || (!want && n != 0) {
					return p, errors.New("shared route targets make the requested pair states conflict")
				}
			} else if intersects(a.Imports, b.Exports) != intersects(s.VRFs[i].Imports, s.VRFs[j].Exports) || intersects(b.Imports, a.Exports) != intersects(s.VRFs[j].Imports, s.VRFs[i].Exports) {
				return p, fmt.Errorf("shared route targets would also change %s / %s; this pair-only change is blocked", a.Name, b.Name)
			}
		}
		if slices.Equal(a.Imports, s.VRFs[i].Imports) {
			continue
		}

		path := []string{"vrf", "name", a.Name, "protocols", "bgp", "address-family"}
		if s.Mode == "mpls" {
			path = append(path, s.Family, "route-target", "vpn", "import")
			if len(a.Imports) == 0 {
				p.Commands = append(p.Commands, Command{"delete", path})
			} else {
				p.Commands = append(p.Commands, Command{"set", append(path, strings.Join(a.Imports, " "))})
			}
		} else {
			path = append(path, "l2vpn-evpn", "route-target", "import")
			for _, t := range difference(s.VRFs[i].Imports, a.Imports) {
				p.Commands = append(p.Commands, Command{"delete", append(slices.Clone(path), t)})
			}
			for _, t := range difference(a.Imports, s.VRFs[i].Imports) {
				p.Commands = append(p.Commands, Command{"set", append(slices.Clone(path), t)})
			}
		}
	}
	if s.Mode == "evpn" {
		p.Warnings = append(p.Warnings, "This matrix manages explicit RT configuration only. With no explicit imports, FRR may use automatic imports; they are not represented here.")
	}
	if save {
		p.Warnings = append(p.Warnings, "Save persists the entire running configuration, including unrelated unsaved CLI changes.")
	}
	return p, nil
}
func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	bts, _ := json.Marshal([]string{a, b})
	return string(bts)
}
func verifyPlan(p Plan, s Snapshot) bool {
	if len(p.After) != len(s.VRFs) {
		return false
	}
	for i, a := range p.After {
		b := s.VRFs[i]
		if a.Name != b.Name || !slices.Equal(a.Imports, b.Imports) || !slices.Equal(a.Exports, b.Exports) {
			return false
		}
	}
	return unmanagedFingerprint(p.Before) == unmanagedFingerprint(s)
}

func unmanagedFingerprint(s Snapshot) string {
	b, _ := json.Marshal(s.Raw)
	var raw M
	_ = json.Unmarshal(b, &raw)
	for _, v := range s.VRFs {
		afi := s.Family
		path := []string{"name", v.Name, "protocols", "bgp", "address-family"}
		if s.Mode == "evpn" {
			afi = "l2vpn-evpn"
		}
		path = append(path, afi, "route-target")
		if s.Mode == "mpls" {
			path = append(path, "vpn")
		}
		delete(object(at(raw, path...)), "import")
	}
	b, _ = json.Marshal(raw)
	return digest(string(b))
}
