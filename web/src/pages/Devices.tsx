import { ArrowRight, Check, Plus, Search, Server, Trash2 } from "lucide-react";
import React, { useState } from "react";
import { Badge, Blank, Field, Modal } from "../components/ui";
import { api, date } from "../lib/api";
import type { Device, Site } from "../types";
export default function Devices({
  devices,
  sites,
  admin,
  filter,
  setFilter,
  onDone,
  onError,
  manage,
}: {
  devices: Device[];
  sites: Site[];
  admin: boolean;
  filter: number;
  setFilter: (id: number) => void;
  onDone: (s: string) => void;
  onError: (e: unknown) => void;
  manage: (id: number) => void;
}) {
  type Form = {
    id: number;
    name: string;
    site_id: number;
    host: string;
    port: number;
    trust: string;
    fingerprint: string;
    save: boolean;
    key: string;
  };
  const [edit, setEdit] = useState<Form>();
  const [inspected, setInspected] = useState<{
    fingerprint: string;
    subject: string;
    expires: string;
  }>();
  const [busy, setBusy] = useState(false);
  const [query, setQuery] = useState("");
  const list = devices.filter(
    (d) =>
      (!filter || d.site_id === filter) &&
      (d.name + " " + d.host).toLowerCase().includes(query.toLowerCase()),
  );
  function begin(d?: Device) {
    setInspected(undefined);
    setEdit(
      d
        ? {
            id: d.id,
            name: d.name,
            site_id: d.site_id,
            host: d.host,
            port: d.port,
            trust: d.trust,
            fingerprint: d.fingerprint,
            save: d.save,
            key: "",
          }
        : {
            id: 0,
            name: "",
            site_id: filter || sites[0]?.id || 0,
            host: "",
            port: 443,
            trust: "system",
            fingerprint: "",
            save: true,
            key: "",
          },
    );
  }
  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api("devices", edit!.id ? "PUT" : "POST", edit);
      setEdit(undefined);
      onDone("Device saved. Open a matrix to discover its current state.");
    } catch (e) {
      onError(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <div className="page-heading">
        <div>
          <span className="eyebrow">FLEET</span>
          <h1>Devices</h1>
          <p>Your VyOS routers, organized and ready to manage.</p>
        </div>
        {admin && (
          <button
            className="primary"
            disabled={!sites.length}
            onClick={() => begin()}
          >
            <Plus size={16} />
            Add device
          </button>
        )}
      </div>
      {!sites.length && (
        <div className="alert warning">
          Create a site before adding your first device.
        </div>
      )}
      <div className="list-toolbar">
        <div className="search">
          <Search size={16} />
          <input
            placeholder="Search devices…"
            aria-label="Search devices"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <select
          aria-label="Filter devices by site"
          value={filter}
          onChange={(e) => setFilter(Number(e.target.value))}
        >
          <option value={0}>All sites</option>
          {sites.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <span>{list.length} devices</span>
      </div>
      {list.length ? (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Device</th>
                <th>Site</th>
                <th>Last connection</th>
                <th>Trust</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {list.map((d) => (
                <tr key={d.id}>
                  <td>
                    <button
                      className="device-name"
                      onClick={() => manage(d.id)}
                    >
                      <Server size={18} />
                      <span>
                        {d.name}
                        <small>
                          {d.host}:{d.port}
                        </small>
                      </span>
                    </button>
                  </td>
                  <td>{sites.find((s) => s.id === d.site_id)?.name}</td>
                  <td>
                    <Badge
                      tone={
                        d.status === "Available"
                          ? "green"
                          : d.status === "Not checked"
                            ? "neutral"
                            : "amber"
                      }
                    >
                      {d.status === "Available"
                        ? "Available"
                        : d.status === "Not checked"
                          ? "Not checked"
                          : "Needs attention"}
                    </Badge>
                    <small title={d.status}>{date(d.checked)}</small>
                  </td>
                  <td>
                    {d.trust === "pin" ? "Pinned certificate" : "System CA"}
                    <small>{d.save ? "Apply & save" : "Apply only"}</small>
                  </td>
                  <td>
                    <div className="row-actions">
                      <button className="link" onClick={() => manage(d.id)}>
                        Open matrix
                        <ArrowRight size={14} />
                      </button>
                      {admin && (
                        <>
                          <button className="link" onClick={() => begin(d)}>
                            Edit
                          </button>
                          <button
                            className="icon danger"
                            aria-label={`Remove ${d.name}`}
                            onClick={async () => {
                              if (
                                !confirm(
                                  `Remove ${d.name} from inventory? Router configuration is unaffected.`,
                                )
                              )
                                return;
                              try {
                                await api("devices?id=" + d.id, "DELETE");
                                onDone("Device removed.");
                              } catch (e) {
                                onError(e);
                              }
                            }}
                          >
                            <Trash2 size={16} />
                          </button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <Blank
          icon={Server}
          title={
            devices.length ? "No matching devices" : "Your fleet starts here"
          }
        >
          Add a router’s management address and API key. Existing VRFs are
          discovered directly from the device.
        </Blank>
      )}
      {edit && (
        <Modal
          title={edit.id ? "Edit device" : "Add a VyOS device"}
          wide
          onClose={() => setEdit(undefined)}
        >
          <form onSubmit={save}>
            <div className="form-grid">
              <Field label="Device name">
                <input
                  autoFocus
                  required
                  maxLength={100}
                  value={edit.name}
                  onChange={(e) => setEdit({ ...edit, name: e.target.value })}
                />
              </Field>
              <Field label="Site">
                <select
                  required
                  value={edit.site_id}
                  onChange={(e) =>
                    setEdit({ ...edit, site_id: Number(e.target.value) })
                  }
                >
                  {sites.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="Management hostname or IP">
                <input
                  required
                  value={edit.host}
                  placeholder="192.0.2.10"
                  onChange={(e) => {
                    setInspected(undefined);
                    setEdit({ ...edit, host: e.target.value, fingerprint: "" });
                  }}
                />
              </Field>
              <Field label="HTTPS API port">
                <input
                  required
                  type="number"
                  min={1}
                  max={65535}
                  value={edit.port}
                  onChange={(e) => {
                    setInspected(undefined);
                    setEdit({
                      ...edit,
                      port: Number(e.target.value),
                      fingerprint: "",
                    });
                  }}
                />
              </Field>
            </div>
            <Field
              label="API key"
              help={
                edit.id
                  ? "Leave blank to retain the stored key."
                  : "The key is encrypted at rest and never returned to the browser."
              }
            >
              <input
                required={!edit.id}
                type="password"
                autoComplete="new-password"
                maxLength={4096}
                value={edit.key}
                onChange={(e) => setEdit({ ...edit, key: e.target.value })}
              />
            </Field>
            <Field label="Certificate trust">
              <select
                value={edit.trust}
                onChange={(e) => setEdit({ ...edit, trust: e.target.value })}
              >
                <option value="system">
                  Verify using system certificate authorities
                </option>
                <option value="pin">
                  Trust an exact certificate fingerprint
                </option>
              </select>
            </Field>
            {edit.trust === "pin" && (
              <div className="trust-panel">
                <Field label="SHA-256 fingerprint">
                  <input
                    required
                    pattern="[a-fA-F0-9:]{64,95}"
                    value={edit.fingerprint}
                    onChange={(e) =>
                      setEdit({ ...edit, fingerprint: e.target.value })
                    }
                  />
                </Field>
                <button
                  className="secondary"
                  type="button"
                  disabled={busy || !edit.host}
                  onClick={async () => {
                    setBusy(true);
                    try {
                      setInspected(
                        await api("trust", "POST", {
                          host: edit.host,
                          port: edit.port,
                        }),
                      );
                    } catch (e) {
                      onError(e);
                    } finally {
                      setBusy(false);
                    }
                  }}
                >
                  Inspect presented certificate
                </button>
                {inspected && (
                  <div className="certificate">
                    <small>
                      {inspected.subject} · Expires {date(inspected.expires)}
                    </small>
                    <code>{inspected.fingerprint}</code>
                    <p>
                      Verify the fingerprint through a trusted channel before
                      accepting it. Inspection sends no API credentials.
                    </p>
                    <button
                      className="link"
                      type="button"
                      onClick={() =>
                        setEdit({ ...edit, fingerprint: inspected.fingerprint })
                      }
                    >
                      <Check size={14} />
                      Use this fingerprint
                    </button>
                  </div>
                )}
              </div>
            )}
            <label className="checkbox">
              <input
                type="checkbox"
                checked={edit.save}
                onChange={(e) => setEdit({ ...edit, save: e.target.checked })}
              />
              <span>
                Save the router configuration after verified changes
                <small>
                  Saving also persists any unrelated unsaved running
                  configuration.
                </small>
              </span>
            </label>
            <div className="modal-actions">
              <button
                type="button"
                className="secondary"
                onClick={() => setEdit(undefined)}
              >
                Cancel
              </button>
              <button className="primary" disabled={busy}>
                {busy ? "Please wait…" : "Save device"}
              </button>
            </div>
          </form>
        </Modal>
      )}
    </>
  );
}
