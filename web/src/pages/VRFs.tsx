import { Plus, RefreshCw, Search, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Badge, Blank, Field, Modal } from "../components/ui";
import { api, ApiError, errorText } from "../lib/api";
import type { Device, Site } from "../types";

type VPN = {
  import_route_map: string;
  export_route_map: string;
  per_nexthop: boolean;
  enabled: boolean;
  rd: string;
  label: string;
  exports: string[];
  imports: string[];
};
type EVPN = {
  enabled: boolean;
  vni: string;
  rd: string;
  exports: string[];
  imports: string[];
  ipv4: boolean;
  ipv6: boolean;
};
type VRF = {
  name: string;
  table: string;
  description: string;
  asn: string;
  ipv4: VPN;
  ipv6: VPN;
  evpn: EVPN;
  dependencies: string[];
};
type Inventory = {
  route_maps: string[];
  fingerprint: string;
  version: string;
  writable: boolean;
  global_asn: string;
  vrfs: VRF[];
};
type Intent = {
  device_id: number;
  fingerprint: string;
  action: string;
  vrf: VRF;
  confirm_name: string;
  remove_policies: boolean;
  operation_id: string;
};
type Plan = {
  commands: { op: string; path: string[] }[];
  warnings: string[];
  save: boolean;
};
const vpn = (): VPN => ({
  import_route_map: "",
  export_route_map: "",
  per_nexthop: false,
  enabled: false,
  rd: "",
  label: "auto",
  exports: [],
  imports: [],
});
const empty = (asn: string): VRF => ({
  name: "",
  table: "",
  description: "",
  asn,
  ipv4: vpn(),
  ipv6: vpn(),
  evpn: {
    enabled: false,
    vni: "",
    rd: "",
    exports: [],
    imports: [],
    ipv4: true,
    ipv6: false,
  },
  dependencies: [],
});
const splitTargets = (s: string) => s.split(/[\s,]+/).filter(Boolean);

export default function VRFs({
  device,
  devices,
  sites,
  selectDevice,
  admin,
  onDone,
  onError,
}: {
  device?: Device;
  devices: Device[];
  sites: Site[];
  selectDevice: (id: number) => void;
  admin: boolean;
  onDone: (s: string) => void;
  onError: (e: unknown) => void;
}) {
  const [data, setData] = useState<Inventory>();
  const [busy, setBusy] = useState(false);
  const [stale, setStale] = useState(false);
  const [query, setQuery] = useState("");
  const [edit, setEdit] = useState<Intent>();
  const [plan, setPlan] = useState<Plan>();
  const [operation, setOperation] = useState<{ id: string; started: number }>();
  const active = useRef(true);
  const request = useRef(0);
  const storageKey = `segmentation-manager-vrf-operation-${device?.id}`;
  const callbacks = useRef({ onDone, onError });
  callbacks.current = { onDone, onError };
  function track(value?: { id: string; started: number }) {
    setOperation(value);
    try {
      if (value) localStorage.setItem(storageKey, JSON.stringify(value));
      else localStorage.removeItem(storageKey);
    } catch {
      /* Private storage can be unavailable. */
    }
  }
  async function refresh() {
    if (!device) return;
    const seq = ++request.current;
    setBusy(true);
    try {
      const job = await api(`operation?device_id=${device.id}`);
      if (!active.current || seq !== request.current) return;
      if (job.status === "applying") {
        track({ id: job.operation_id, started: Date.now() });
        return;
      }
      const result = await api<Inventory>(`vrfs?id=${device.id}`);
      if (active.current && seq === request.current) {
        setData(result);
        setStale(false);
      }
    } catch (e) {
      if (active.current) {
        setStale(true);
        callbacks.current.onError(e);
      }
    } finally {
      if (active.current && seq === request.current) setBusy(false);
    }
  }
  useEffect(() => {
    active.current = true;
    let saved: { id: string; started: number } | undefined;
    try {
      saved =
        JSON.parse(localStorage.getItem(storageKey) || "null") || undefined;
    } catch {
      /* Ignore damaged storage. */
    }
    if (saved?.id) setOperation(saved);
    else void refresh();
    return () => {
      active.current = false;
      request.current++;
    };
  }, [device?.id]);
  useEffect(() => {
    const pending = !!edit && admin;
    window.dispatchEvent(
      new CustomEvent("segmentation-manager-pending", { detail: pending }),
    );
    const unload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    if (pending) window.addEventListener("beforeunload", unload);
    return () => {
      window.removeEventListener("beforeunload", unload);
      window.dispatchEvent(
        new CustomEvent("segmentation-manager-pending", { detail: false }),
      );
    };
  }, [edit, admin]);
  useEffect(() => {
    if (!operation) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const result = await api(
          `operation?id=${encodeURIComponent(operation!.id)}`,
        );
        if (stopped) return;
        if (result.status !== "applying") {
          track();
          setEdit(undefined);
          setPlan(undefined);
          if (
            result.status.startsWith("applied") ||
            result.status === "unchanged"
          )
            callbacks.current.onDone(
              result.message || "VRF configuration verified.",
            );
          else
            callbacks.current.onError(
              result.message ||
                "Operation requires inspection. Refresh before editing.",
            );
          await refresh();
          return;
        }
      } catch (e) {
        if (stopped) return;
        if (
          e instanceof ApiError &&
          e.status === 404 &&
          Date.now() - operation!.started > 300000
        ) {
          track();
          setStale(true);
          callbacks.current.onError(
            "The operation could not be located. Refresh and inspect the router before retrying; no write was replayed.",
          );
          return;
        }
      }
      if (!stopped) timer = setTimeout(poll, 2000);
    }
    void poll();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [operation?.id]);
  function begin(action: string, vrf = empty(data?.global_asn || "")) {
    if (!device || !data) return;
    setPlan(undefined);
    setEdit({
      device_id: device.id,
      fingerprint: data.fingerprint,
      action,
      vrf: structuredClone(vrf),
      confirm_name: "",
      remove_policies: false,
      operation_id: crypto.randomUUID(),
    });
  }
  function change(patch: Partial<VRF>) {
    setPlan(undefined);
    setEdit((e) => e && { ...e, vrf: { ...e.vrf, ...patch } });
  }
  async function preview(e: React.FormEvent) {
    e.preventDefault();
    if (!admin) return;
    setBusy(true);
    try {
      setPlan(await api<Plan>("vrfs/plan", "POST", edit));
    } catch (e) {
      onError(e);
    } finally {
      setBusy(false);
    }
  }
  async function apply() {
    if (!edit || !admin) return;
    setBusy(true);
    request.current++;
    // Record the ID before sending: a disconnected browser must never replay a write.
    track({ id: edit.operation_id, started: Date.now() });
    try {
      const result = await api("vrfs/apply", "POST", edit);
      setEdit(undefined);
      setPlan(undefined);
      if (result.status === "unchanged") {
        track();
        onDone("No configuration changes needed.");
        await refresh();
      }
    } catch (e) {
      if (e instanceof ApiError && e.status < 500) {
        track();
        setPlan(undefined);
        setStale(true);
      } else {
        setEdit(undefined);
        setPlan(undefined);
      }
      onError(e);
    } finally {
      setBusy(false);
    }
  }
  const locked = busy || !!operation;
  const editable = admin && data?.writable && !locked && !stale;
  const vrf = edit?.vrf;
  const original = data?.vrfs.find((v) => v.name === vrf?.name);
  const removing =
    !!original &&
    ((original.ipv4.enabled && !vrf?.ipv4.enabled) ||
      (original.ipv6.enabled && !vrf?.ipv6.enabled) ||
      (original.evpn.enabled && !vrf?.evpn.enabled));
  const close = () => {
    if (!busy && !operation) {
      setEdit(undefined);
      setPlan(undefined);
    }
  };
  return (
    <>
      <div className="page-heading">
        <div>
          <span className="eyebrow">ROUTING INSTANCES</span>
          <h1>VRFs</h1>
          <p>
            Manage VRFs and their route-target settings on your existing
            network.
          </p>
        </div>
        {admin && (
          <button
            className="primary"
            disabled={!editable}
            onClick={() => begin("create")}
          >
            <Plus size={16} />
            Create VRF
          </button>
        )}
      </div>
      <section className="device-bar">
        <Field label="Router">
          <select
            aria-label="Router"
            value={device?.id || 0}
            disabled={locked || !!edit}
            onChange={(e) => selectDevice(Number(e.target.value))}
          >
            {!devices.length && <option value={0}>No routers</option>}
            {devices.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name} · {sites.find((s) => s.id === d.site_id)?.name} ·{" "}
                {d.host}
              </option>
            ))}
          </select>
        </Field>
        <div className="device-status">
          <Badge tone={stale ? "amber" : "green"}>
            {stale
              ? "Needs refresh"
              : data
                ? `${data.vrfs.length} VRFs`
                : "Awaiting discovery"}
          </Badge>
          <small>{data?.version}</small>
        </div>
        <button
          className="icon refresh"
          aria-label="Refresh VRFs"
          disabled={!device || locked || !!edit}
          onClick={() => void refresh()}
        >
          <RefreshCw size={18} className={busy ? "spin" : ""} />
        </button>
      </section>
      {operation && (
        <div className="alert warning" role="status">
          Applying configuration and verifying router state. This operation
          continues if you leave the page.
        </div>
      )}
      {data && !data.writable && (
        <div className="alert warning">
          Read-only inventory. VRF changes require VyOS 1.5 or a tested rolling
          build.
        </div>
      )}
      {!device ? (
        <Blank title="Choose a managed router">
          Add a router in Devices to discover its VRFs.
        </Blank>
      ) : (
        <>
          <div className="list-toolbar">
            <div className="search">
              <Search size={16} />
              <input
                aria-label="Search VRFs"
                placeholder="Search VRFs…"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>
          </div>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>VRF name</th>
                  <th>Table ID</th>
                  <th>Type</th>
                  <th>Configuration</th>
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {data?.vrfs
                  .filter((v) =>
                    `${v.name} ${v.table} ${v.description}`
                      .toLowerCase()
                      .includes(query.toLowerCase()),
                  )
                  .map((v) => (
                    <tr key={v.name}>
                      <td>
                        <strong>{v.name}</strong>
                        <small className="vrf-detail">{v.description}</small>
                      </td>
                      <td>{v.table || "—"}</td>
                      <td>
                        <div className="vrf-badges">
                          {(v.ipv4.enabled || v.ipv6.enabled) && (
                            <Badge tone="mpls">MPLS</Badge>
                          )}
                          {v.evpn.enabled && <Badge tone="vxlan">VXLAN</Badge>}
                          {!v.ipv4.enabled &&
                            !v.ipv6.enabled &&
                            !v.evpn.enabled && (
                              <span className="vrf-muted">—</span>
                            )}
                        </div>
                      </td>
                      <td>
                        {v.asn && (
                          <small className="vrf-detail">BGP AS {v.asn}</small>
                        )}
                        {v.evpn.vni && (
                          <small className="vrf-detail">
                            L3VNI {v.evpn.vni}
                          </small>
                        )}
                        {!v.asn && !v.evpn.vni && "—"}
                      </td>
                      <td>
                        {!admin && (
                          <button
                            disabled={locked}
                            onClick={() => begin("view", v)}
                          >
                            View
                          </button>
                        )}
                        {admin && (
                          <div className="row-actions">
                            <button
                              disabled={!editable}
                              onClick={() => begin("update", v)}
                            >
                              Edit
                            </button>
                            <button
                              className="icon"
                              aria-label={`Delete ${v.name}`}
                              disabled={!editable}
                              onClick={() => begin("delete", v)}
                            >
                              <Trash2 size={16} />
                            </button>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
            {data?.vrfs.length === 0 && (
              <Blank title="No VRFs configured">
                Create a VRF with optional MPLS VPN and VXLAN/EVPN settings.
              </Blank>
            )}
          </div>
        </>
      )}
      {edit && vrf && (
        <Modal
          wide
          title={
            edit.action === "create"
              ? "Create VRF"
              : `${edit.action === "delete" ? "Delete" : edit.action === "view" ? "View" : "Edit"} ${vrf.name}`
          }
          onClose={close}
        >
          <form onSubmit={preview}>
            <fieldset className="vrf-form" disabled={locked || !admin}>
              {edit.action === "delete" ? (
                <>
                  <div className="alert warning">
                    This deletes the VRF and all routing configuration inside
                    it. Interfaces and other external resources are retained.
                    Existing references must be detached on VyOS first.
                  </div>
                  {!!vrf.dependencies.length && (
                    <div className="alert error">
                      <strong>Blocking references</strong>
                      <ul>
                        {vrf.dependencies.map((d) => (
                          <li key={d}>
                            <code>{d}</code>
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                  <Field label={`Type ${vrf.name} to confirm`}>
                    <input
                      required
                      value={edit.confirm_name}
                      onChange={(e) => {
                        setPlan(undefined);
                        setEdit({ ...edit, confirm_name: e.target.value });
                      }}
                    />
                  </Field>
                </>
              ) : (
                <>
                  <div className="form-grid">
                    <Field
                      label="VRF name"
                      help={
                        edit.action === "update"
                          ? "Names and table IDs are fixed to avoid recreating the VRF."
                          : "1–15 characters; start with a letter. Interface names are reserved."
                      }
                    >
                      <input
                        required
                        maxLength={15}
                        value={vrf.name}
                        disabled={edit.action === "update"}
                        onChange={(e) => change({ name: e.target.value })}
                      />
                    </Field>
                    <Field label="Routing table ID">
                      <input
                        required
                        type="number"
                        min={100}
                        max={65535}
                        value={vrf.table}
                        disabled={edit.action === "update"}
                        onChange={(e) => change({ table: e.target.value })}
                      />
                    </Field>
                    <Field label="Description">
                      <input
                        maxLength={256}
                        value={vrf.description}
                        onChange={(e) =>
                          change({ description: e.target.value })
                        }
                      />
                    </Field>
                    <Field
                      label="BGP ASN"
                      help="Shared by the enabled technologies in this VRF."
                    >
                      <input
                        type="number"
                        min={1}
                        max={4294967295}
                        required={
                          vrf.ipv4.enabled ||
                          vrf.ipv6.enabled ||
                          vrf.evpn.enabled
                        }
                        value={vrf.asn}
                        onChange={(e) => change({ asn: e.target.value })}
                      />
                    </Field>
                  </div>
                  <section className="vrf-section">
                    <h3>MPLS VPN</h3>
                    <p className="vrf-muted">
                      Configure VPN import/export participation separately for
                      each address family.
                    </p>
                    {(["ipv4", "ipv6"] as const).map((f) => (
                      <div key={f} className="vrf-family">
                        <label className="checkbox">
                          <input
                            type="checkbox"
                            checked={vrf[f].enabled}
                            onChange={(e) =>
                              change({
                                [f]: {
                                  ...vrf[f],
                                  enabled: e.target.checked,
                                  label: vrf[f].label || "auto",
                                },
                              })
                            }
                          />
                          Enable {f === "ipv4" ? "IPv4" : "IPv6"} MPLS VPN
                        </label>
                        {vrf[f].enabled && (
                          <>
                            <div className="form-grid">
                              <Field
                                label={`${f === "ipv4" ? "IPv4" : "IPv6"} route distinguisher`}
                              >
                                <input
                                  required
                                  placeholder="65000:101"
                                  value={vrf[f].rd}
                                  onChange={(e) =>
                                    change({
                                      [f]: { ...vrf[f], rd: e.target.value },
                                    })
                                  }
                                />
                              </Field>
                              <Field
                                label="VPN export label"
                                help="auto or a label from 16 to 1048575"
                              >
                                <input
                                  required
                                  value={vrf[f].label}
                                  onChange={(e) =>
                                    change({
                                      [f]: { ...vrf[f], label: e.target.value },
                                    })
                                  }
                                />
                              </Field>
                            </div>
                            <label className="checkbox">
                              <input
                                type="checkbox"
                                checked={vrf[f].per_nexthop}
                                onChange={(e) =>
                                  change({
                                    [f]: {
                                      ...vrf[f],
                                      per_nexthop: e.target.checked,
                                    },
                                  })
                                }
                              />
                              Per-Nexthop Label Allocation (
                              {f === "ipv4" ? "IPv4" : "IPv6"})
                            </label>
                            <p className="vrf-muted">
                              Off uses the default per-VRF label allocation.
                            </p>
                            <div className="form-grid">
                              {(["import", "export"] as const).map(
                                (direction) => {
                                  const key = `${direction}_route_map` as const;
                                  const value = vrf[f][key] || "";
                                  const maps = data?.route_maps || [];
                                  const label = `${f === "ipv4" ? "IPv4" : "IPv6"} VPN ${direction} route-map`;
                                  return (
                                    <Field key={direction} label={label}>
                                      <select
                                        aria-label={label}
                                        value={value}
                                        onChange={(e) =>
                                          change({
                                            [f]: {
                                              ...vrf[f],
                                              [key]: e.target.value,
                                            },
                                          })
                                        }
                                      >
                                        <option value="">None</option>
                                        {value && !maps.includes(value) && (
                                          <option value={value} disabled>
                                            {value} (not found on router)
                                          </option>
                                        )}
                                        {maps.map((name) => (
                                          <option key={name} value={name}>
                                            {name}
                                          </option>
                                        ))}
                                      </select>
                                    </Field>
                                  );
                                },
                              )}
                            </div>
                            <p className="vrf-muted">
                              Optional existing route-maps. Choose None to
                              remove an assignment. Refresh the VRFs page after
                              changing route-maps on the router.
                            </p>
                            <TargetField
                              key={`${vrf.name}-${f}`}
                              label={`${f === "ipv4" ? "IPv4" : "IPv6"} export route-targets`}
                              value={vrf[f].exports}
                              onChange={(exports) =>
                                change({ [f]: { ...vrf[f], exports } })
                              }
                            />
                            <p className="vrf-muted">
                              Preserved imports:{" "}
                              {vrf[f].imports.join(", ") ||
                                "None explicitly configured"}
                            </p>
                          </>
                        )}
                      </div>
                    ))}
                  </section>
                  <section className="vrf-section">
                    <h3>VXLAN / EVPN</h3>
                    <label className="checkbox">
                      <input
                        type="checkbox"
                        checked={vrf.evpn.enabled}
                        onChange={(e) =>
                          change({
                            evpn: {
                              ...vrf.evpn,
                              enabled: e.target.checked,
                              ipv4: vrf.evpn.ipv4 || !vrf.evpn.ipv6,
                            },
                          })
                        }
                      />
                      Enable EVPN
                    </label>
                    {vrf.evpn.enabled && (
                      <>
                        <div className="form-grid">
                          <Field label="L3VNI">
                            <input
                              required
                              type="number"
                              min={1}
                              max={16777215}
                              value={vrf.evpn.vni}
                              onChange={(e) =>
                                change({
                                  evpn: { ...vrf.evpn, vni: e.target.value },
                                })
                              }
                            />
                          </Field>
                          <Field
                            label="EVPN route distinguisher"
                            help="Optional; leave blank for FRR to derive the RD."
                          >
                            <input
                              value={vrf.evpn.rd}
                              placeholder="Automatic"
                              onChange={(e) =>
                                change({
                                  evpn: { ...vrf.evpn, rd: e.target.value },
                                })
                              }
                            />
                          </Field>
                        </div>
                        <TargetField
                          label="EVPN export route-targets"
                          value={vrf.evpn.exports}
                          onChange={(exports) =>
                            change({ evpn: { ...vrf.evpn, exports } })
                          }
                        />
                        <div className="vrf-badges">
                          {(["ipv4", "ipv6"] as const).map((f) => (
                            <label key={f} className="checkbox">
                              <input
                                type="checkbox"
                                checked={vrf.evpn[f]}
                                onChange={(e) =>
                                  change({
                                    evpn: {
                                      ...vrf.evpn,
                                      [f]: e.target.checked,
                                    },
                                  })
                                }
                              />
                              Advertise {f === "ipv4" ? "IPv4" : "IPv6"} unicast
                            </label>
                          ))}
                        </div>
                        <p className="vrf-muted">
                          Preserved imports:{" "}
                          {vrf.evpn.imports.join(", ") ||
                            "None explicitly configured"}
                        </p>
                      </>
                    )}
                  </section>
                  <p className="vrf-muted">
                    Changes are limited to this VRF and its BGP settings. Use
                    your existing MPLS transport, EVPN peers and shared
                    VXLAN/bridge configuration. Configure import relationships
                    in the connectivity matrices.
                  </p>
                  {removing && (
                    <label className="checkbox alert warning">
                      <input
                        required
                        type="checkbox"
                        checked={edit.remove_policies}
                        onChange={(e) => {
                          setPlan(undefined);
                          setEdit({
                            ...edit,
                            remove_policies: e.target.checked,
                          });
                        }}
                      />
                      Remove import/export policies for the technologies I
                      disabled.
                    </label>
                  )}
                </>
              )}
              {plan && (
                <section className="vrf-section">
                  <h3>Review configuration</h3>
                  {plan.warnings.map((w) => (
                    <p key={w} className="alert warning">
                      {w}
                    </p>
                  ))}
                  <pre className="command-preview">
                    {plan.commands
                      .map(
                        (c) =>
                          `${c.op} ${c.path.map((p) => (/^[A-Za-z0-9_:./-]+$/.test(p) ? p : JSON.stringify(p))).join(" ")}`,
                      )
                      .join("\n") || "No changes"}
                  </pre>
                  <p className="vrf-muted">
                    {plan.save
                      ? "Save the router's running configuration after verification."
                      : "Commit to running configuration only; device policy does not save."}
                  </p>
                </section>
              )}
            </fieldset>
            <div className="modal-actions">
              <button type="button" className="secondary" onClick={close}>
                {admin ? "Cancel" : "Close"}
              </button>
              {admin &&
                (plan ? (
                  <button
                    type="button"
                    className="primary"
                    onClick={() => void apply()}
                    disabled={locked || !plan.commands.length || stale}
                  >
                    Apply {plan.commands.length} commands
                  </button>
                ) : (
                  <button
                    className="primary"
                    disabled={
                      locked ||
                      stale ||
                      (edit.action === "delete" &&
                        (edit.confirm_name !== vrf.name ||
                          !!vrf.dependencies.length))
                    }
                  >
                    Review changes
                  </button>
                ))}
            </div>
          </form>
        </Modal>
      )}
    </>
  );
}

function TargetField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string[];
  onChange: (v: string[]) => void;
}) {
  const [text, setText] = useState(value.join(", "));
  return (
    <Field
      label={label}
      help="Explicit ASN:number or IPv4:number targets, separated by commas or spaces."
    >
      <input
        required
        value={text}
        placeholder="65000:101"
        onChange={(e) => {
          setText(e.target.value);
          onChange(splitTargets(e.target.value));
        }}
      />
    </Field>
  );
}
