package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

func (a *App) vrfsHTTP(w http.ResponseWriter, r *http.Request) {
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
	s, e := router.vrfInventory(r.Context(), d)
	if e != nil {
		fail(w, 502, e)
		return
	}
	send(w, 200, s)
}
func (a *App) vrfPlanHTTP(w http.ResponseWriter, r *http.Request, u User, apply bool) {
	if r.Method != "POST" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	var b VRFIntent
	if e := body(w, r, &b); e != nil {
		fail(w, 400, e)
		return
	}
	if b.OperationID != "" && !operationIDPattern.MatchString(b.OperationID) {
		fail(w, 400, errors.New("invalid operation ID"))
		return
	}
	encoded, _ := json.Marshal(b)
	hash := digest("vrf-management:" + string(encoded))
	if apply && b.OperationID != "" {
		if result, h, e := a.operationResult(b.OperationID); e == nil {
			if h != hash {
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
	s, e := router.vrfInventory(r.Context(), d)
	if e != nil {
		fail(w, 502, e)
		return
	}
	if b.Fingerprint != s.Fingerprint {
		fail(w, 409, errors.New("router configuration changed; close this editor, refresh and review your changes"))
		return
	}
	p, e := buildVRFPlan(s, b, d.Save)
	if e != nil {
		fail(w, 422, e)
		return
	}
	if !apply {
		send(w, 200, p)
		return
	}
	if len(p.Commands) == 0 {
		send(w, 200, M{"status": "unchanged", "inventory": s})
		return
	}
	op := b.OperationID
	if op == "" {
		op = random(18)
	}
	details, _ := json.Marshal(M{"mode": "vrfs", "action": b.Action, "name": b.VRF.Name, "commands": p.Commands, "request_hash": hash})
	if _, e = a.db.Exec("INSERT INTO operations(id,device_id,user_id,created,status,detail) VALUES(?,?,?,?,?,?)", op, d.ID, u.ID, time.Now().UTC().Format(time.RFC3339), "applying", string(details)); e != nil {
		fail(w, 409, errors.New("could not create operation; no change sent"))
		return
	}
	transferred = true
	a.workers.Add(1)
	go func() { defer a.workers.Done(); defer lock.Unlock(); a.runVRFApply(op, d, u, router, p, details) }()
	send(w, 202, M{"status": "applying", "operation_id": op, "message": "Applying VRF configuration and verifying router state."})
}
func (a *App) runVRFApply(op string, d Device, u User, router *Router, p VRFPlan, details []byte) {
	a.runConfiguration(op, d, u, router, p.Commands, details, func(ctx context.Context) (any, bool, bool) {
		s, e := router.vrfInventory(ctx, d)
		if e != nil {
			return nil, false, false
		}
		return s, verifyVRFPlan(p, s), true
	}, "inventory")
}
