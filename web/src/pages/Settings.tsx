import { Download, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { Badge, Field } from "../components/ui";
import { api, csrf, date } from "../lib/api";
import type { Prefs } from "../types";
export default function Settings({
  administrator,
  canConfigure,
  prefs,
  setPrefs,
  onError,
  onDone,
}: {
  administrator: boolean;
  canConfigure: boolean;
  prefs: Prefs;
  setPrefs: (p: Prefs) => void;
  onError: (e: unknown) => void;
  onDone: (s: string) => void;
}) {
  const [form, setForm] = useState(prefs);
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState("general");
  const [backupPassword, setBackupPassword] = useState("");
  const [certificate, setCertificate] = useState("");
  const [key, setKey] = useState("");
  const [audit, setAudit] = useState<{ events: any[]; operations: any[] }>({
    events: [],
    operations: [],
  });
  async function history() {
    try {
      setAudit(await api("audit"));
    } catch (e) {
      onError(e);
    }
  }
  useEffect(() => {
    if (tab === "activity") void history();
  }, [tab]);
  return (
    <>
      <div className="page-heading">
        <div>
          <span className="eyebrow">WORKSPACE</span>
          <h1>Settings</h1>
          <p>
            Configure this installation without changing your container
            environment.
          </p>
        </div>
      </div>
      <div className="tabs">
        {["general", "backup", "security", "activity"].map((t) => (
          <button
            className={tab === t ? "active" : ""}
            key={t}
            onClick={() => setTab(t)}
          >
            {t.charAt(0).toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>
      {tab === "general" && (
        <form
          className="settings-panel"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            try {
              const s = await api<Prefs>("settings", "PUT", form);
              setPrefs(s);
              onDone("Application settings saved.");
            } catch (e) {
              onError(e);
            } finally {
              setBusy(false);
            }
          }}
        >
          <fieldset
            className="permission-fields"
            disabled={!canConfigure || busy}
          >
            <h2>Application preferences</h2>
            <Field label="Workspace name">
              <input
                required
                maxLength={100}
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </Field>
            <div className="form-grid">
              {(
                [
                  {
                    key: "refresh",
                    label: "Matrix refresh interval",
                    unit: "seconds",
                    min: 10,
                    max: 3600,
                  },
                  {
                    key: "timeout",
                    label: "Device read timeout",
                    unit: "seconds",
                    min: 3,
                    max: 120,
                  },
                  {
                    key: "session_hours",
                    label: "Maximum session duration",
                    unit: "hours · 30-minute idle expiry",
                    min: 1,
                    max: 24,
                  },
                  {
                    key: "retention_days",
                    label: "Audit retention",
                    unit: "days · cleanup when settings are saved",
                    min: 7,
                    max: 3650,
                  },
                ] as const
              ).map((f) => (
                <Field key={f.key} label={f.label} help={f.unit}>
                  <input
                    type="number"
                    required
                    min={f.min}
                    max={f.max}
                    value={form[f.key]}
                    onChange={(e) =>
                      setForm({ ...form, [f.key]: Number(e.target.value) })
                    }
                  />
                </Field>
              ))}
            </div>
            <button className="primary" disabled={busy}>
              Save preferences
            </button>
          </fieldset>
        </form>
      )}
      {tab === "backup" && (
        <div className="settings-panel">
          <div className="section-icon">
            <Download size={23} />
          </div>
          <h2>Back up your workspace</h2>
          {!administrator && (
            <p className="alert warning">
              Only administrators can export backups containing account and
              credential data.
            </p>
          )}
          <p>
            Export a consistent database snapshot and the device credential
            encryption key in a password-protected file. Keep the password
            separately.
          </p>
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              setBusy(true);
              try {
                const res = await fetch("/api/backup", {
                  method: "POST",
                  headers: {
                    "Content-Type": "application/json",
                    "X-CSRF-Token": csrf,
                  },
                  body: JSON.stringify({ password: backupPassword }),
                });
                if (!res.ok) throw new Error((await res.json()).error);
                const url = URL.createObjectURL(await res.blob());
                const a = document.createElement("a");
                a.href = url;
                a.download = `segmentation-manager-${new Date().toISOString().slice(0, 10)}.msb`;
                a.click();
                setTimeout(() => URL.revokeObjectURL(url), 1000);
                setBackupPassword("");
                onDone("Encrypted backup downloaded.");
              } catch (e) {
                onError(e);
              } finally {
                setBusy(false);
              }
            }}
          >
            <fieldset
              className="permission-fields"
              disabled={!administrator || busy}
            >
              <Field
                label="Backup password"
                help="At least 12 characters. Required to restore this file."
              >
                <input
                  type="password"
                  required
                  minLength={12}
                  maxLength={256}
                  autoComplete="new-password"
                  value={backupPassword}
                  onChange={(e) => setBackupPassword(e.target.value)}
                />
              </Field>
              <button className="primary" disabled={busy}>
                <Download size={16} />
                {busy ? "Creating backup…" : "Download encrypted backup"}
              </button>
            </fieldset>
          </form>
          <hr />
          <h3>Restore into a fresh container</h3>
          <p>
            Start a new container with an empty data volume. On the setup page
            choose “Restore from a backup” and enter that installation’s claim
            code and your backup password. Existing sessions are revoked. The
            destination keeps its own HTTPS certificate.
          </p>
          <small>
            For a complete offline backup, stop the application and copy its
            entire data volume, including TLS keys. Never copy only a live
            SQLite database file.
          </small>
        </div>
      )}
      {tab === "security" && (
        <div className="settings-panel">
          <h2>Application HTTPS certificate</h2>
          {!administrator && (
            <p className="alert warning">
              Only administrators can replace the application certificate.
            </p>
          )}
          <p>
            A generated certificate secures the first boot. Install your
            organization’s certificate and matching private key here, then
            restart the container to activate them.
          </p>
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              setBusy(true);
              try {
                const r = await api("certificate", "POST", {
                  certificate,
                  key,
                });
                setCertificate("");
                setKey("");
                onDone(r.message);
              } catch (e) {
                onError(e);
              } finally {
                setBusy(false);
              }
            }}
          >
            <fieldset
              className="permission-fields"
              disabled={!administrator || busy}
            >
              <Field label="PEM certificate chain">
                <textarea
                  required
                  className="mono"
                  rows={6}
                  value={certificate}
                  onChange={(e) => setCertificate(e.target.value)}
                />
              </Field>
              <Field label="PEM private key">
                <textarea
                  required
                  className="mono"
                  rows={6}
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                />
              </Field>
              <button className="primary" disabled={busy}>
                Install certificate
              </button>
            </fieldset>
          </form>
          <hr />
          <h3>Session protection</h3>
          <p>
            Passwords use Argon2id. Session cookies are Secure, HttpOnly and
            SameSite Strict. State changes require a CSRF token. A session
            expires after 30 minutes of inactivity or the configured maximum
            duration.
          </p>
        </div>
      )}
      {tab === "activity" && (
        <>
          <div className="list-toolbar">
            <h2>Recent activity</h2>
            <button className="secondary" onClick={history}>
              <RefreshCw size={15} />
              Refresh
            </button>
          </div>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>When</th>
                  <th>User</th>
                  <th>Action</th>
                  <th>Details</th>
                </tr>
              </thead>
              <tbody>
                {audit.events.map((e, i) => (
                  <tr key={i}>
                    <td>{date(e.at)}</td>
                    <td>{e.user}</td>
                    <td>
                      <code>{e.action}</code>
                    </td>
                    <td>{e.detail}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <h2 className="spaced">Router operations</h2>
          {audit.operations.length ? (
            audit.operations.map((o) => (
              <details className="disclosure" key={o.id}>
                <summary>
                  <Badge
                    tone={o.status === "applied-saved" ? "green" : "amber"}
                  >
                    {o.status}
                  </Badge>{" "}
                  Device {o.device_id} · {date(o.at)}
                </summary>
                <pre className="command-preview">
                  {JSON.stringify(JSON.parse(o.detail), null, 2)}
                </pre>
              </details>
            ))
          ) : (
            <p>No configuration operations have been submitted.</p>
          )}
        </>
      )}
    </>
  );
}
