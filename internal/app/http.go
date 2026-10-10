package app

import (
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"segmentation-manager/web"
	"strconv"
	"strings"
	"time"
)

func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, e error) { send(w, status, M{"error": e.Error()}) }
func body(w http.ResponseWriter, r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("JSON content type required")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return errors.New("invalid request body")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
func idOf(r *http.Request) int64 { n, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); return n }
func (a *App) Handler() http.Handler {
	assets, _ := fs.Sub(web.Assets, "dist")
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/restore" {
			a.gate.Lock()
			defer a.gate.Unlock()
		} else {
			a.gate.RLock()
			defer a.gate.RUnlock()
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/healthz" {
			send(w, 200, M{"ok": true})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != "GET" && r.Method != "HEAD" {
				fail(w, 405, errors.New("method not allowed"))
				return
			}
			if strings.Contains(r.URL.Path, ".") {
				files.ServeHTTP(w, r)
			} else {
				r.URL.Path = "/"
				files.ServeHTTP(w, r)
			}
			return
		}
		if r.Method != "GET" && !sameOrigin(r) {
			fail(w, 403, errors.New("origin rejected"))
			return
		}
		switch r.URL.Path {
		case "/api/setup":
			a.setup(w, r)
			return
		case "/api/login":
			a.login(w, r)
			return
		case "/api/restore":
			a.restoreHTTP(w, r)
			return
		}
		u, token, ok := a.user(r)
		if !ok {
			fail(w, 401, errors.New("sign in to continue"))
			return
		}
		if r.Method != "GET" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(a.csrf(token))) != 1 {
			fail(w, 403, errors.New("session verification failed; reload the page"))
			return
		}
		if r.URL.Path == "/api/me" && r.Method == "GET" {
			send(w, 200, M{"user": u, "csrf": a.csrf(token), "settings": a.settings()})
			return
		}
		if r.URL.Path == "/api/logout" && r.Method == "POST" {
			_, _ = a.db.Exec("DELETE FROM sessions WHERE token=?", digest(token))
			http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
			send(w, 200, M{"ok": true})
			return
		}
		if !roleAllows(u.Role, r.Method, r.URL.Path) {
			fail(w, 403, errors.New("your role does not permit this action"))
			return
		}
		switch r.URL.Path {
		case "/api/sites":
			a.sites(w, r, u)
		case "/api/devices":
			a.devices(w, r, u)
		case "/api/users":
			a.users(w, r, u)
		case "/api/settings":
			a.settingsHTTP(w, r, u)
		case "/api/trust":
			a.trust(w, r)
		case "/api/vrfs":
			a.vrfsHTTP(w, r)
		case "/api/vrfs/plan":
			a.vrfPlanHTTP(w, r, u, false)
		case "/api/vrfs/apply":
			a.vrfPlanHTTP(w, r, u, true)
		case "/api/snapshot":
			a.snapshotHTTP(w, r)
		case "/api/plan":
			a.planHTTP(w, r, u, false)
		case "/api/apply":
			a.planHTTP(w, r, u, true)
		case "/api/operation":
			a.operationHTTP(w, r)
		case "/api/audit":
			a.auditHTTP(w, r)
		case "/api/backup":
			a.backupHTTP(w, r, u)
		case "/api/certificate":
			a.certificateHTTP(w, r, u)
		default:
			fail(w, 404, errors.New("not found"))
		}
	})
}
func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		send(w, 200, M{"configured": a.configured()})
		return
	}
	if r.Method != "POST" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	if !a.allowed(r) {
		fail(w, 429, errors.New("too many attempts; try again in 10 minutes"))
		return
	}
	var b struct {
		Claim    string `json:"claim"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Title    string `json:"title"`
	}
	if e := body(w, r, &b); e != nil {
		fail(w, 400, e)
		return
	}
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	if a.configured() {
		fail(w, 409, errors.New("installation is already configured"))
		return
	}
	if a.claim == "" || subtle.ConstantTimeCompare([]byte(b.Claim), []byte(a.claim)) != 1 {
		fail(w, 403, errors.New("invalid installation claim code"))
		return
	}
	if !validPassword(b.Password) || len(strings.TrimSpace(b.Name)) < 2 || len(b.Name) > 64 || len(b.Title) > 100 {
		fail(w, 400, errors.New("name must be 2–64 characters; password must be 12–256 characters"))
		return
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer tx.Rollback()
	res, e := tx.Exec("INSERT INTO users(name,password,role) VALUES(?,?,'administrator')", strings.TrimSpace(b.Name), passwordHash(b.Password))
	if e != nil {
		fail(w, 400, dbError(e))
		return
	}
	s := defaults()
	if strings.TrimSpace(b.Title) != "" {
		s.Name = strings.TrimSpace(b.Title)
	}
	j, _ := json.Marshal(s)
	if _, e = tx.Exec("UPDATE settings SET value=? WHERE id=1", string(j)); e != nil {
		fail(w, 500, e)
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, e)
		return
	}
	id, _ := res.LastInsertId()
	u := User{id, b.Name, roleAdministrator}
	a.claim = ""
	_ = os.Remove(filepath.Join(a.data, "setup.claim"))
	a.audit(u, "installation.completed", "Initial administrator created")
	if e = a.session(w, r, u); e != nil {
		fail(w, 500, e)
		return
	}
	send(w, 201, M{"ok": true})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	if !a.allowed(r) {
		fail(w, 429, errors.New("too many attempts; try again in 10 minutes"))
		return
	}
	var b struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if e := body(w, r, &b); e != nil {
		fail(w, 400, e)
		return
	}
	if len(b.Password) > 256 {
		fail(w, 401, errors.New("invalid username or password"))
		return
	}
	var u User
	var hash string
	e := a.db.QueryRow("SELECT id,name,role,password FROM users WHERE name=?", b.Name).Scan(&u.ID, &u.Name, &u.Role, &hash)
	if e != nil {
		hash = "a2id$dummy-salt-constant$invalid"
	}
	if !passwordOK(b.Password, hash) || e != nil {
		fail(w, 401, errors.New("invalid username or password"))
		return
	}
	if e = a.session(w, r, u); e != nil {
		fail(w, 500, e)
		return
	}
	a.audit(u, "auth.login", "Signed in")
	send(w, 200, M{"ok": true})
}
func (a *App) sites(w http.ResponseWriter, r *http.Request, u User) {
	switch r.Method {
	case "GET":
		rows, e := a.db.Query("SELECT id,name,description FROM sites ORDER BY name")
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		out := []Site{}
		for rows.Next() {
			var s Site
			if e = rows.Scan(&s.ID, &s.Name, &s.Description); e != nil {
				fail(w, 500, e)
				return
			}
			out = append(out, s)
		}
		send(w, 200, out)
	case "POST", "PUT":
		var b Site
		if e := body(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		if len(strings.TrimSpace(b.Name)) == 0 || len(b.Name) > 100 || len(b.Description) > 1000 {
			fail(w, 400, errors.New("site name required (max 100 characters)"))
			return
		}
		var e error
		if r.Method == "POST" {
			_, e = a.db.Exec("INSERT INTO sites(name,description) VALUES(?,?)", strings.TrimSpace(b.Name), b.Description)
		} else {
			_, e = a.db.Exec("UPDATE sites SET name=?,description=? WHERE id=?", strings.TrimSpace(b.Name), b.Description, b.ID)
		}
		if e != nil {
			fail(w, 409, dbError(e))
			return
		}
		a.audit(u, "site.saved", b.Name)
		send(w, 200, M{"ok": true})
	case "DELETE":
		_, e := a.db.Exec("DELETE FROM sites WHERE id=?", idOf(r))
		if e != nil {
			fail(w, 409, errors.New("reassign the site's devices before deleting it"))
			return
		}
		a.audit(u, "site.deleted", fmt.Sprint(idOf(r)))
		send(w, 200, M{"ok": true})
	default:
		fail(w, 405, errors.New("method not allowed"))
	}
}
func (a *App) devices(w http.ResponseWriter, r *http.Request, u User) {
	switch r.Method {
	case "GET":
		rows, e := a.db.Query("SELECT id,site_id,name,host,port,trust,fingerprint,save,status,checked,version FROM devices ORDER BY name")
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		out := []Device{}
		for rows.Next() {
			var d Device
			if e = rows.Scan(&d.ID, &d.SiteID, &d.Name, &d.Host, &d.Port, &d.Trust, &d.Fingerprint, &d.Save, &d.Status, &d.Checked, &d.Version); e != nil {
				fail(w, 500, e)
				return
			}
			out = append(out, d)
		}
		send(w, 200, out)
	case "POST", "PUT":
		var b struct {
			ID          int64  `json:"id"`
			SiteID      int64  `json:"site_id"`
			Name        string `json:"name"`
			Host        string `json:"host"`
			Port        int    `json:"port"`
			Trust       string `json:"trust"`
			Fingerprint string `json:"fingerprint"`
			Save        bool   `json:"save"`
			Key         string `json:"key"`
		}
		if e := body(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		if e := validateHost(b.Host, b.Port); e != nil {
			fail(w, 400, e)
			return
		}
		b.Fingerprint = strings.ToLower(strings.ReplaceAll(b.Fingerprint, ":", ""))
		if (b.Trust != "system" && b.Trust != "pin") || (b.Trust == "pin" && (len(b.Fingerprint) != 64 || !hexString(b.Fingerprint))) || len(b.Name) == 0 || len(b.Name) > 100 || len(b.Key) > 4096 {
			fail(w, 400, errors.New("name, valid trust mode and SHA-256 pin are required"))
			return
		}
		var secret string
		var e error
		if b.Key != "" {
			secret, e = a.seal(b.Key)
		} else if r.Method == "PUT" {
			old, err := a.device(b.ID)
			e = err
			secret = old.Secret
		} else {
			e = errors.New("API key is required")
		}
		if e != nil {
			fail(w, 400, e)
			return
		}
		lock := a.lock(b.ID)
		lock.Lock()
		defer lock.Unlock()
		if r.Method == "POST" {
			_, e = a.db.Exec("INSERT INTO devices(site_id,name,host,port,trust,fingerprint,save,secret) VALUES(?,?,?,?,?,?,?,?)", b.SiteID, b.Name, b.Host, b.Port, b.Trust, b.Fingerprint, b.Save, secret)
		} else {
			_, e = a.db.Exec("UPDATE devices SET site_id=?,name=?,host=?,port=?,trust=?,fingerprint=?,save=?,secret=?,status='Not checked',checked='',version='' WHERE id=?", b.SiteID, b.Name, b.Host, b.Port, b.Trust, b.Fingerprint, b.Save, secret, b.ID)
		}
		if e != nil {
			fail(w, 409, dbError(e))
			return
		}
		a.audit(u, "device.saved", b.Name)
		send(w, 200, M{"ok": true})
	case "DELETE":
		lock := a.lock(idOf(r))
		lock.Lock()
		defer lock.Unlock()
		if _, e := a.db.Exec("DELETE FROM devices WHERE id=?", idOf(r)); e != nil {
			fail(w, 500, e)
			return
		}
		a.audit(u, "device.deleted", fmt.Sprint(idOf(r)))
		send(w, 200, M{"ok": true})
	default:
		fail(w, 405, errors.New("method not allowed"))
	}
}
func hexString(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func (a *App) users(w http.ResponseWriter, r *http.Request, u User) {
	if r.Method == "GET" {
		rows, e := a.db.Query("SELECT id,name,role FROM users ORDER BY name")
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer rows.Close()
		out := []User{}
		for rows.Next() {
			var x User
			_ = rows.Scan(&x.ID, &x.Name, &x.Role)
			out = append(out, x)
		}
		send(w, 200, out)
		return
	}
	if r.Method != "POST" && r.Method != "PUT" && r.Method != "DELETE" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	var b struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if r.Method == "DELETE" {
		b.ID = idOf(r)
	} else {
		if e := body(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		if len(b.Name) < 2 || len(b.Name) > 64 || !validRole(b.Role) || (b.Password != "" && !validPassword(b.Password)) || (r.Method == "POST" && !validPassword(b.Password)) {
			fail(w, 400, errors.New("valid name, role and a 12–256 character password are required"))
			return
		}
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer tx.Rollback()
	if r.Method != "POST" {
		var role string
		var n int
		if e = tx.QueryRow("SELECT role FROM users WHERE id=?", b.ID).Scan(&role); e != nil {
			fail(w, 404, errors.New("user not found"))
			return
		}
		_ = tx.QueryRow("SELECT count(*) FROM users WHERE role='administrator'").Scan(&n)
		if role == roleAdministrator && n == 1 && (r.Method == "DELETE" || b.Role != roleAdministrator) {
			fail(w, 409, errors.New("the last administrator cannot be removed or demoted"))
			return
		}
	}
	switch r.Method {
	case "DELETE":
		_, e = tx.Exec("DELETE FROM users WHERE id=?", b.ID)
	case "POST":
		_, e = tx.Exec("INSERT INTO users(name,password,role) VALUES(?,?,?)", b.Name, passwordHash(b.Password), b.Role)
	case "PUT":
		_, e = tx.Exec("UPDATE users SET name=?,role=? WHERE id=?", b.Name, b.Role, b.ID)
		if e == nil && b.Password != "" {
			_, e = tx.Exec("UPDATE users SET password=? WHERE id=?", passwordHash(b.Password), b.ID)
		}
		if e == nil {
			_, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", b.ID)
		}
	}
	if e != nil {
		fail(w, 409, dbError(e))
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, e)
		return
	}
	a.audit(u, "user.changed", fmt.Sprintf("id=%d name=%s", b.ID, b.Name))
	send(w, 200, M{"ok": true})
}
func (a *App) settingsHTTP(w http.ResponseWriter, r *http.Request, u User) {
	if r.Method == "GET" {
		send(w, 200, a.settings())
		return
	}
	if r.Method != "PUT" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	var s Settings
	if e := body(w, r, &s); e != nil {
		fail(w, 400, e)
		return
	}
	if len(s.Name) == 0 || len(s.Name) > 100 || s.Refresh < 10 || s.Refresh > 3600 || s.Timeout < 3 || s.Timeout > 120 || s.SessionHours < 1 || s.SessionHours > 24 || s.RetentionDays < 7 || s.RetentionDays > 3650 {
		fail(w, 400, errors.New("settings outside allowed ranges"))
		return
	}
	b, _ := json.Marshal(s)
	if _, e := a.db.Exec("UPDATE settings SET value=? WHERE id=1", string(b)); e != nil {
		fail(w, 500, e)
		return
	}
	_, _ = a.db.Exec("DELETE FROM sessions WHERE expires<?", time.Now().Unix())
	_, _ = a.db.Exec("DELETE FROM audit WHERE created<?", time.Now().AddDate(0, 0, -s.RetentionDays).UTC().Format(time.RFC3339))
	a.audit(u, "settings.saved", "Application preferences updated")
	send(w, 200, s)
}
func (a *App) trust(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	var b struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if e := body(w, r, &b); e != nil {
		fail(w, 400, e)
		return
	}
	if e := validateHost(b.Host, b.Port); e != nil {
		fail(w, 400, e)
		return
	}
	dial := &net.Dialer{Timeout: 5 * time.Second}
	c, e := tls.DialWithDialer(dial, "tcp", net.JoinHostPort(b.Host, strconv.Itoa(b.Port)), &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	if e != nil {
		fail(w, 502, errors.New("could not inspect device TLS certificate"))
		return
	}
	defer c.Close()
	cert := c.ConnectionState().PeerCertificates[0]
	send(w, 200, M{"fingerprint": fingerprint(cert), "subject": cert.Subject.String(), "expires": cert.NotAfter, "note": "No credentials sent. Verify this fingerprint through a trusted channel before enrolling."})
}
func (a *App) snapshotHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	d, e := a.device(idOf(r))
	if e != nil {
		fail(w, 404, errors.New("device not found"))
		return
	}
	router, e := a.router(d)
	if e != nil {
		fail(w, 400, e)
		return
	}
	family := r.URL.Query().Get("family")
	if family == "" {
		family = "ipv4-unicast"
	}
	s, e := router.snapshot(r.Context(), d, r.URL.Query().Get("mode"), family)
	status := "Available"
	if e != nil {
		status = e.Error()
	}
	_, _ = a.db.Exec("UPDATE devices SET status=?,checked=?,version=? WHERE id=?", status, time.Now().UTC().Format(time.RFC3339), s.Version, d.ID)
	if e != nil {
		fail(w, 502, e)
		return
	}
	send(w, 200, s)
}
func (a *App) planHTTP(w http.ResponseWriter, r *http.Request, u User, apply bool) {
	if r.Method != "POST" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	var b struct {
		DeviceID    int64  `json:"device_id"`
		Mode        string `json:"mode"`
		Family      string `json:"family"`
		Fingerprint string `json:"fingerprint"`
		Edits       []Edit `json:"edits"`
		OperationID string `json:"operation_id"`
	}
	if e := body(w, r, &b); e != nil {
		fail(w, 400, e)
		return
	}
	if b.OperationID != "" && !operationIDPattern.MatchString(b.OperationID) {
		fail(w, 400, errors.New("invalid operation ID"))
		return
	}
	encoded, _ := json.Marshal(b)
	requestHash := digest(string(encoded))
	if apply && b.OperationID != "" {
		if result, hash, err := a.operationResult(b.OperationID); err == nil {
			if hash != requestHash {
				fail(w, 409, errors.New("operation ID already used for a different request"))
				return
			}
			send(w, 200, result)
			return
		}
	}
	lock := a.lock(b.DeviceID)
	if !lock.TryLock() {
		fail(w, 409, errors.New("another operation is active for this device"))
		return
	}
	transferred := false
	defer func() {
		if !transferred {
			lock.Unlock()
		}
	}()
	d, e := a.device(b.DeviceID)
	if e != nil {
		fail(w, 404, errors.New("device not found"))
		return
	}
	router, e := a.router(d)
	if e != nil {
		fail(w, 400, e)
		return
	}
	s, e := router.snapshot(r.Context(), d, b.Mode, b.Family)
	if e != nil {
		fail(w, 502, e)
		return
	}
	if b.Fingerprint != s.Fingerprint {
		send(w, 409, M{"error": "Router configuration changed. Refresh and review your edits.", "snapshot": s})
		return
	}
	p, e := buildPlan(s, b.Edits, d.Save)
	if e != nil {
		fail(w, 422, e)
		return
	}
	if !apply {
		send(w, 200, p)
		return
	}
	if len(p.Commands) == 0 {
		send(w, 200, M{"status": "unchanged", "snapshot": s})
		return
	}
	op := b.OperationID
	if op == "" {
		op = random(18)
	}
	details, _ := json.Marshal(M{"mode": b.Mode, "family": b.Family, "commands": p.Commands, "before": s.VRFs, "after": p.After, "request_hash": requestHash})
	if _, e = a.db.Exec("INSERT INTO operations(id,device_id,user_id,created,status,detail) VALUES(?,?,?,?,?,?)", op, d.ID, u.ID, time.Now().UTC().Format(time.RFC3339), "applying", string(details)); e != nil {
		fail(w, 409, errors.New("could not create operation; no change sent"))
		return
	}
	transferred = true
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		defer lock.Unlock()
		a.runApply(op, d, u, router, p, details)
	}()
	send(w, 202, M{"status": "applying", "operation_id": op, "message": "Applying configuration; waiting for router verification."})
}

func (a *App) auditHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	rows, e := a.db.Query("SELECT created,user_name,action,detail FROM audit ORDER BY id DESC LIMIT 200")
	if e != nil {
		fail(w, 500, e)
		return
	}
	out := []M{}
	for rows.Next() {
		var at, user, action, detail string
		_ = rows.Scan(&at, &user, &action, &detail)
		out = append(out, M{"at": at, "user": user, "action": action, "detail": detail})
	}
	rows.Close()
	ops := []M{}
	rows, e = a.db.Query("SELECT id,device_id,created,status,detail FROM operations ORDER BY created DESC LIMIT 100")
	if e == nil {
		defer rows.Close()
		for rows.Next() {
			var id, created, status, detail string
			var device int64
			_ = rows.Scan(&id, &device, &created, &status, &detail)
			ops = append(ops, M{"id": id, "device_id": device, "at": created, "status": status, "detail": detail})
		}
	}
	send(w, 200, M{"events": out, "operations": ops})
}
