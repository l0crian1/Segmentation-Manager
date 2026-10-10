import { ArrowRight, Globe2, Plus, Trash2 } from "lucide-react";
import React, { useState } from "react";
import { Blank, Field, Modal } from "../components/ui";
import { api } from "../lib/api";
import type { Device, Site } from "../types";
export default function Sites({
  sites,
  devices,
  admin,
  onDone,
  onError,
  onOpen,
}: {
  sites: Site[];
  devices: Device[];
  admin: boolean;
  onDone: (s: string) => void;
  onError: (e: unknown) => void;
  onOpen: (id: number) => void;
}) {
  const [edit, setEdit] = useState<Site>();
  const [busy, setBusy] = useState(false);
  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api("sites", edit!.id ? "PUT" : "POST", edit);
      setEdit(undefined);
      onDone("Site saved.");
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
          <span className="eyebrow">WORKSPACE</span>
          <h1>Your sites</h1>
          <p>A simple way to organize routers by location or purpose.</p>
        </div>
        {admin && (
          <button
            className="primary"
            onClick={() => setEdit({ id: 0, name: "", description: "" })}
          >
            <Plus size={16} />
            Add site
          </button>
        )}
      </div>
      {sites.length ? (
        <div className="site-grid">
          {sites.map((s) => (
            <article className="site-card" key={s.id}>
              <div className="site-icon">
                <Globe2 size={25} />
              </div>
              <h2>{s.name}</h2>
              <p>{s.description || "No description"}</p>
              <div className="site-card-footer">
                <button className="link" onClick={() => onOpen(s.id)}>
                  {devices.filter((d) => d.site_id === s.id).length} devices
                  <ArrowRight size={14} />
                </button>
                {admin && (
                  <>
                    <button className="link" onClick={() => setEdit(s)}>
                      Edit
                    </button>
                    <button
                      className="icon danger"
                      aria-label={`Delete ${s.name}`}
                      onClick={async () => {
                        if (
                          !confirm(
                            `Delete site ${s.name}? Devices must be reassigned first.`,
                          )
                        )
                          return;
                        try {
                          await api("sites?id=" + s.id, "DELETE");
                          onDone("Site deleted.");
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
            </article>
          ))}
        </div>
      ) : (
        <Blank icon={Globe2} title="A home for your routers">
          Create a site for a data center, branch office, or any useful
          grouping.
        </Blank>
      )}
      {edit && (
        <Modal
          title={edit.id ? "Edit site" : "Add a site"}
          onClose={() => setEdit(undefined)}
        >
          <form onSubmit={save}>
            <Field label="Site name">
              <input
                autoFocus
                required
                maxLength={100}
                value={edit.name}
                onChange={(e) => setEdit({ ...edit, name: e.target.value })}
              />
            </Field>
            <Field label="Description">
              <textarea
                maxLength={1000}
                value={edit.description}
                onChange={(e) =>
                  setEdit({ ...edit, description: e.target.value })
                }
              />
            </Field>
            <div className="modal-actions">
              <button
                type="button"
                className="secondary"
                onClick={() => setEdit(undefined)}
              >
                Cancel
              </button>
              <button className="primary" disabled={busy}>
                Save site
              </button>
            </div>
          </form>
        </Modal>
      )}
    </>
  );
}
