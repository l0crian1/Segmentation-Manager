package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/argon2"
	"net"
	"net/http"
	"strings"
	"time"
)

func passwordHash(password string) string {
	salt := random(16)
	h := argon2.IDKey([]byte(password), []byte(salt), 2, 64*1024, 2, 32)
	return fmt.Sprintf("a2id$%s$%s", salt, base64.RawStdEncoding.EncodeToString(h))
}
func passwordOK(p, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 3 || parts[0] != "a2id" {
		return false
	}
	h := argon2.IDKey([]byte(p), []byte(parts[1]), 2, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare([]byte(base64.RawStdEncoding.EncodeToString(h)), []byte(parts[2])) == 1
}
func digest(s string) string      { x := sha256.Sum256([]byte(s)); return hex.EncodeToString(x[:]) }
func validPassword(p string) bool { return len(p) >= 12 && len(p) <= 256 }
func (a *App) csrf(token string) string {
	h := hmac.New(sha256.New, a.key)
	h.Write([]byte("csrf:" + token))
	return hex.EncodeToString(h.Sum(nil))
}
func (a *App) session(w http.ResponseWriter, r *http.Request, u User) error {
	token := random(32)
	now := time.Now().Unix()
	_, e := a.db.Exec("INSERT INTO sessions VALUES(?,?,?,?)", digest(token), u.ID, now+int64(a.settings().SessionHours)*3600, now)
	if e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: a.settings().SessionHours * 3600})
	return nil
}
func (a *App) user(r *http.Request) (User, string, bool) {
	var u User
	c, e := r.Cookie(sessionCookieName)
	if e != nil {
		return u, "", false
	}
	now := time.Now().Unix()
	e = a.db.QueryRow("SELECT u.id,u.name,u.role FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.token=? AND s.expires>? AND s.last_seen>?", digest(c.Value), now, now-1800).Scan(&u.ID, &u.Name, &u.Role)
	if e != nil {
		return u, "", false
	}
	_, _ = a.db.Exec("UPDATE sessions SET last_seen=? WHERE token=?", now, digest(c.Value))
	return u, c.Value, true
}
func (a *App) allowed(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.limitMu.Lock()
	defer a.limitMu.Unlock()
	now := time.Now()
	if len(a.attempts) > 10000 {
		for k, v := range a.attempts {
			if now.After(v.until) {
				delete(a.attempts, k)
			}
		}
		if len(a.attempts) > 10000 {
			return false
		}
	}
	x := a.attempts[host]
	if now.After(x.until) {
		x = attempt{until: now.Add(10 * time.Minute)}
	}
	x.n++
	a.attempts[host] = x
	return x.n <= 15
}
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "" || o == "https://"+r.Host
}
