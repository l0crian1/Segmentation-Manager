package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"
)

var operationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)

// Read-back can initially see the old running configuration after an API timeout.
// Wait for the intended state without ever resubmitting the configure request.
func reconcile(ctx context.Context, router *Router, d Device, p Plan, interval time.Duration) (Snapshot, error) {
	var last Snapshot
	var err error
	for {
		last, err = router.snapshot(ctx, d, p.Before.Mode, p.Before.Family)
		if err == nil && verifyPlan(p, last) {
			return last, nil
		}
		if ctx.Err() != nil {
			return last, ctx.Err()
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *App) runApply(op string, d Device, u User, router *Router, p Plan, details []byte) {
	a.runConfiguration(op, d, u, router, p.Commands, details, func(ctx context.Context) (any, bool, bool) {
		fresh, e := router.snapshot(ctx, d, p.Before.Mode, p.Before.Family)
		if e != nil {
			return nil, false, false
		}
		return fresh, verifyPlan(p, fresh), true
	}, "snapshot")
}

// All router writes share durability, read-back reconciliation and save policy.
func (a *App) runConfiguration(op string, d Device, u User, router *Router, commands []Command, details []byte, observe func(context.Context) (any, bool, bool), resultKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	_, writeErr := router.call(ctx, "configure", commands)
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer readCancel()
	var fresh any
	verified := false
	for readCtx.Err() == nil {
		value, match, valid := observe(readCtx)
		if valid {
			fresh = value
		}
		if match {
			verified = true
			break
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-readCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	status, msg := "applied", "Requested configuration verified"
	if !verified {
		status, msg = "unresolved", "Verification did not complete. No write was replayed. Inspect the router and refresh before deciding on recovery."
		if fresh != nil {
			status, msg = "partial-or-failed", "Router state did not match the requested result within the verification window. Inspect current configuration and any external changes before recovery."
		}
	} else if writeErr != nil {
		msg = "The API response failed, but read-back confirmed the requested changes."
	}
	if status == "applied" && d.Save {
		if _, e := router.call(ctx, "config-file", M{"op": "save"}); e != nil {
			status, msg = "applied-unsaved", "Changes verified, but saving failed. The running configuration is not confirmed persistent."
		} else {
			status = "applied-saved"
		}
	} else if status == "applied" {
		status = "applied-unsaved"
		msg += ". Device policy leaves running configuration unsaved."
	}
	result := M{"status": status, "message": msg, "operation_id": op}
	if fresh != nil {
		result[resultKey] = fresh
	}
	var detail M
	_ = json.Unmarshal(details, &detail)
	detail["result"] = result
	encoded, _ := json.Marshal(detail)
	_ = a.operation(op, d.ID, u, status, string(encoded))
	a.audit(u, "router."+status, d.Name+" operation="+op)
}

func (a *App) operationResult(id string) (M, string, error) {
	var status, raw string
	if e := a.db.QueryRow("SELECT status,detail FROM operations WHERE id=?", id).Scan(&status, &raw); e != nil {
		return nil, "", e
	}
	var detail M
	if e := json.Unmarshal([]byte(raw), &detail); e != nil {
		return nil, "", e
	}
	result := object(detail["result"])
	result["operation_id"], result["status"] = id, status
	if status == "unresolved" && result["message"] == nil {
		result["message"] = "Operation interrupted by restart. Refresh and inspect the router; no write was replayed."
	}
	return result, str(detail["request_hash"]), nil
}

func (a *App) operationHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		fail(w, 405, errors.New("method not allowed"))
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		_ = a.db.QueryRow("SELECT id FROM operations WHERE device_id=? AND status='applying' ORDER BY created DESC LIMIT 1", r.URL.Query().Get("device_id")).Scan(&id)
		if id == "" {
			send(w, 200, M{"status": "idle"})
			return
		}
	}
	result, _, err := a.operationResult(id)
	if err != nil {
		fail(w, 404, errors.New("operation not found"))
		return
	}
	send(w, 200, result)
}
