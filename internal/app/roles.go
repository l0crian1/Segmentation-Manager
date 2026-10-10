package app

import (
	"database/sql"
	"errors"
)

const (
	roleAdministrator = "administrator"
	roleNetworkAdmin  = "network-admin"
	roleViewOnly      = "view-only"
)

func validRole(role string) bool {
	return role == roleAdministrator || role == roleNetworkAdmin || role == roleViewOnly
}

// Authorization is checked centrally, including endpoints that use POST to read
// sensitive data (backups contain password hashes and the credential key).
func roleAllows(role, method, path string) bool {
	if !validRole(role) {
		return false
	}
	if method == "GET" {
		return true
	}
	if role == roleAdministrator {
		return true
	}
	if role == roleViewOnly {
		return false
	}
	switch path {
	case "/api/sites", "/api/devices", "/api/trust", "/api/plan", "/api/apply", "/api/vrfs/plan", "/api/vrfs/apply", "/api/settings":
		return true
	}
	return false
}

// SQLite CHECK constraints require rebuilding the users table. Disable FK
// actions before the transaction so dropping the old table cannot revoke live
// sessions; retain IDs and check all references before committing atomically.
func migrateRoles(db *sql.DB) error {
	db.SetMaxOpenConns(1)
	var version int
	if e := db.QueryRow("PRAGMA user_version").Scan(&version); e != nil {
		return e
	}
	if version == 2 {
		return nil
	}
	if version != 1 {
		return errors.New("unsupported database schema")
	}
	if _, e := db.Exec("PRAGMA foreign_keys=OFF"); e != nil {
		return e
	}
	defer db.Exec("PRAGMA foreign_keys=ON")
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`
 CREATE TABLE users_v2(id INTEGER PRIMARY KEY,name TEXT NOT NULL COLLATE NOCASE UNIQUE,password TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('administrator','network-admin','view-only')));
 INSERT INTO users_v2 SELECT id,name,password,CASE role WHEN 'admin' THEN 'administrator' WHEN 'viewer' THEN 'view-only' ELSE role END FROM users;
 DROP TABLE users;
 ALTER TABLE users_v2 RENAME TO users;
 PRAGMA user_version=2;`)
	if e != nil {
		return e
	}
	rows, e := tx.Query("PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	invalid := rows.Next()
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return rowErr
	}
	if invalid {
		return errors.New("role migration would leave invalid references")
	}
	return tx.Commit()
}
