import {
  Activity,
  ArrowLeftRight,
  ArrowRight,
  Check,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  LockKeyhole,
  Plus,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Badge, Blank, Field, Modal } from "../components/ui";
import { api, ApiError, pair, relation } from "../lib/api";
import type { Device, Edit, Plan, Prefs, Site, Snapshot, VRF } from "../types";
export default function Matrix({
  mode,
  device,
  devices,
  sites,
  siteFilter,
  setSiteFilter,
  selectDevice,
  admin,
  prefs,
  onError,
  onDone,
  onDevices,
}: {
  mode: string;
  device?: Device;
  devices: Device[];
  sites: Site[];
  siteFilter: number;
  setSiteFilter: (n: number) => void;
  selectDevice: (n: number) => void;
  admin: boolean;
  prefs: Prefs;
  onError: (e: unknown) => void;
  onDone: (s: string) => void;
  onDevices: () => void;
}) {
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [family, setFamily] = useState("ipv4-unicast");
  const [busy, setBusy] = useState(false);
  const [stale, setStale] = useState("");
  const [edits, setEdits] = useState<Record<string, Edit>>({});
  const [plan, setPlan] = useState<Plan>();
  const [query, setQuery] = useState("");
  const [rowPage, setRowPage] = useState(0);
  const [colPage, setColPage] = useState(0);
  const [help, setHelp] = useState(false);
  const request = useRef(0);
  const [operation, setOperation] = useState("");
  const [operationMessage, setOperationMessage] = useState("");
  const operationRef = useRef("");
  const notFoundSince = useRef(0);
  const operationKey = `segmentation-manager-operation:${device?.id}:${mode}:${family}`;
  function trackOperation(id: string) {
    operationRef.current = id;
    notFoundSince.current = 0;
    setOperation(id);
    if (id) localStorage.setItem(operationKey, id);
    else localStorage.removeItem(operationKey);
    request.current++;
  }
  const pending = Object.values(edits);
  useEffect(() => {
    window.dispatchEvent(
      new CustomEvent("segmentation-manager-pending", {
        detail: pending.length > 0 || !!operation,
      }),
    );
    const beforeUnload = (e: BeforeUnloadEvent) => {
      if (pending.length || operation) {
        e.preventDefault();
      }
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => {
      window.removeEventListener("beforeunload", beforeUnload);
      window.dispatchEvent(
        new CustomEvent("segmentation-manager-pending", { detail: false }),
      );
    };
  }, [pending.length, operation]);
  const editsRef = useRef(edits);
  editsRef.current = edits;
  const snapshotRef = useRef(snapshot);
  snapshotRef.current = snapshot;
  async function refresh(manual = false) {
    if (!device || operationRef.current) return;
    if (
      manual &&
      Object.keys(editsRef.current).length &&
      !confirm("Refresh will discard your pending edits. Continue?")
    )
      return;
    const seq = ++request.current;
    setBusy(true);
    try {
      const active = await api(`operation?device_id=${device.id}`);
      if (seq !== request.current || operationRef.current) return;
      if (active.status === "applying") {
        trackOperation(active.operation_id);
        setBusy(false);
        return;
      }
      const s = await api<Snapshot>(
        `snapshot?id=${device.id}&mode=${mode}&family=${family}`,
      );
      if (seq !== request.current || operationRef.current) return;
      if (Object.keys(editsRef.current).length && !manual) {
        if (s.fingerprint !== snapshotRef.current?.fingerprint)
          setStale(
            "Router configuration changed while you were editing. Refresh before applying.",
          );
        return;
      }
      setSnapshot(s);
      setStale("");
      if (manual) {
        setEdits({});
        setPlan(undefined);
      }
    } catch (e) {
      if (seq === request.current) {
        setStale("Refresh failed. Last observed state may be out of date.");
        onError(e);
      }
    } finally {
      if (seq === request.current) setBusy(false);
    }
  }
  useEffect(() => {
    setSnapshot(undefined);
    setEdits({});
    setPlan(undefined);
    setStale("");
    setRowPage(0);
    setColPage(0);
    const saved = localStorage.getItem(operationKey) || "";
    operationRef.current = saved;
    setOperation(saved);
    setBusy(false);
    if (!saved) void refresh();
    return () => {
      request.current++;
    };
  }, [device?.id, family, mode]);
  useEffect(() => {
    const timer = setInterval(() => {
      if (!document.hidden && !busy && !plan && !operationRef.current)
        void refresh();
    }, prefs.refresh * 1000);
    return () => clearInterval(timer);
  }, [device?.id, family, prefs.refresh, busy, plan]);
  function changeContext(fn: () => void) {
    if (operationRef.current) return;
    if (!pending.length || confirm("Discard pending pair changes?")) fn();
  }
  function toggle(a: VRF, b: VRF) {
    const key = pair(a.name, b.name);
    const old = edits[key];
    const connected = old ? old.enabled : relation(a, b) === 2;
    const enabled = !connected;
    setEdits((prev) => {
      const copy = { ...prev };
      if (
        (enabled && relation(a, b) === 2) ||
        (!enabled && relation(a, b) === 0)
      )
        delete copy[key];
      else copy[key] = { a: a.name, b: b.name, enabled };
      return copy;
    });
    setPlan(undefined);
  }
  function payload() {
    return {
      device_id: device!.id,
      mode,
      family,
      fingerprint: snapshot!.fingerprint,
      edits: pending,
    };
  }
  async function review() {
    setBusy(true);
    try {
      setPlan(await api<Plan>("plan", "POST", payload()));
    } catch (e) {
      onError(e);
    } finally {
      setBusy(false);
    }
  }
  async function apply() {
    const id = crypto.randomUUID();
    trackOperation(id);
    setOperationMessage(
      "Applying configuration. Large commits may take several minutes.",
    );
    setBusy(true);
    setPlan(undefined);
    try {
      const res = await api("apply", "POST", {
        ...payload(),
        operation_id: id,
      });
      if (res.status === "unchanged") {
        trackOperation("");
        setEdits({});
        setSnapshot(res.snapshot);
        setStale("");
        onDone("No configuration change needed.");
      }
    } catch (e) {
      if (e instanceof ApiError && e.status < 500) {
        trackOperation("");
        onError(e);
      } else
        setOperationMessage(
          "Connection interrupted. Checking the recorded operation; the write will not be resubmitted.",
        );
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    if (!operation) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const res = await api(`operation?id=${encodeURIComponent(operation)}`);
        if (stopped) return;
        if (res.status === "applying") {
          setOperationMessage(
            "Applying and verifying on VyOS. Large commits may take several minutes.",
          );
        } else {
          trackOperation("");
          setPlan(undefined);
          setBusy(false);
          if (res.snapshot?.mode === mode && res.snapshot?.family === family)
            setSnapshot(res.snapshot);
          if (["applied-saved", "applied-unsaved"].includes(res.status)) {
            setEdits({});
            editsRef.current = {};
            setStale("");
            onDone(res.message);
            if (res.snapshot?.mode !== mode || res.snapshot?.family !== family)
              void refresh();
          } else {
            setStale(res.message);
            onError(res.message);
          }
          return;
        }
      } catch (e) {
        if (stopped) return;
        if (e instanceof ApiError && e.status === 404 && !busy) {
          if (!notFoundSince.current) notFoundSince.current = Date.now();
          if (Date.now() - notFoundSince.current > 300000) {
            trackOperation("");
            setStale(
              "No recorded operation was found. Refresh the router before reviewing your changes again.",
            );
            return;
          }
        }
        setOperationMessage(
          "Waiting to reconnect to the application. The operation continues on the server.",
        );
      }
      timer = setTimeout(poll, 2000);
    }
    timer = setTimeout(poll, 1500);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [operation, busy, device?.id, mode, family]);
  const all = snapshot?.vrfs || [];
  const filtered = all.filter((v) =>
    v.name.toLowerCase().includes(query.toLowerCase()),
  );
  const row = filtered.slice(rowPage * 24, (rowPage + 1) * 24);
  const col = filtered.slice(colPage * 24, (colPage + 1) * 24);
  let connected = 0,
    partial = 0;
  for (let i = 0; i < all.length; i++)
    for (let j = i + 1; j < all.length; j++) {
      if (relation(all[i], all[j]) === 2) connected++;
      else if (relation(all[i], all[j]) === 1) partial++;
    }
  return (
    <>
      <div className="page-heading">
        <div>
          <span className="eyebrow">
            {mode === "mpls" ? "MPLS L3VPN" : "VXLAN EVPN"}
          </span>
          <h1>Connectivity matrix</h1>
          <p>
            Manage route-target imports between VRFs and corresponding VRFs on
            other PEs.
          </p>
        </div>
        <button className="secondary" onClick={() => setHelp(true)}>
          <CircleHelp size={16} />
          How it works
        </button>
      </div>
      <section className="device-bar">
        <div className="device-icon">
          <Server size={21} />
        </div>
        <Field label="SITE">
          <select
            aria-label="Select site"
            disabled={!!operation}
            value={siteFilter}
            onChange={(e) =>
              changeContext(() => setSiteFilter(Number(e.target.value)))
            }
          >
            <option value={0}>All sites</option>
            {sites.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="MANAGED DEVICE">
          <select
            aria-label="Select device"
            disabled={!!operation}
            value={device?.id || 0}
            onChange={(e) =>
              changeContext(() => selectDevice(Number(e.target.value)))
            }
          >
            {!devices.length && <option value={0}>No devices</option>}
            {devices.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name} · {d.host}
              </option>
            ))}
          </select>
        </Field>
        {mode === "mpls" && (
          <Field label="ADDRESS FAMILY">
            <select
              disabled={!!operation}
              value={family}
              onChange={(e) => changeContext(() => setFamily(e.target.value))}
            >
              <option value="ipv4-unicast">IPv4 unicast</option>
              <option value="ipv6-unicast">IPv6 unicast</option>
            </select>
          </Field>
        )}
        <div className="device-status">
          {snapshot ? (
            <>
              <Badge tone={stale ? "amber" : "green"}>
                <span className="status-dot" />
                {stale ? "Needs refresh" : "Observed"}
              </Badge>
              <small>{snapshot.version}</small>
            </>
          ) : (
            <Badge>{busy ? "Connecting…" : "Awaiting discovery"}</Badge>
          )}
        </div>
        <button
          className="icon refresh"
          disabled={!device || busy || !!operation}
          onClick={() => refresh(true)}
          title="Refresh from router"
          aria-label="Refresh from router"
        >
          <RefreshCw size={18} className={busy ? "spin" : ""} />
        </button>
      </section>
      {!device ? (
        <Blank
          icon={Server}
          title="Connect your first router"
          action={
            admin ? (
              <button className="primary" onClick={onDevices}>
                <Plus size={16} />
                Add a device
              </button>
            ) : undefined
          }
        >
          Add a site and a VyOS device to discover its route-target
          relationships.
        </Blank>
      ) : (
        <>
          <div className="stats">
            <div>
              <span>DISCOVERED VRFs</span>
              <strong>{snapshot ? all.length : "—"}</strong>
              <small>
                {mode === "mpls" ? "VPN address family" : "IP-VRF / L3VNI"}
              </small>
            </div>
            <div>
              <span>BIDIRECTIONAL PAIRS</span>
              <strong>{snapshot ? connected : "—"}</strong>
              <small>Matching imports in both directions</small>
            </div>
            <div>
              <span>PARTIAL RELATIONSHIPS</span>
              <strong>{snapshot ? partial : "—"}</strong>
              <small>One direction currently matches</small>
            </div>
            <div>
              <span>LAST OBSERVATION</span>
              <strong className="time">
                {snapshot ? new Date(snapshot.at).toLocaleTimeString() : "—"}
              </strong>
              <small>Refresh interval · {prefs.refresh}s</small>
            </div>
          </div>
          {operation && (
            <div className="alert warning" role="status">
              <RefreshCw size={17} className="spin" />
              {operationMessage || "Checking active operation..."}
            </div>
          )}
          {stale && !operation && (
            <div className="alert warning">
              <Activity size={17} />
              {stale}
            </div>
          )}
          <section className="matrix-panel">
            <div className="matrix-tools">
              <div>
                <h2>
                  VRF relationships{" "}
                  <Badge>{mode === "mpls" ? "MPLS" : "EVPN"}</Badge>
                </h2>
                <p>
                  Select a pair for both directions, or a diagonal cell to
                  import its own export RTs.
                </p>
              </div>
              <div className="search">
                <Search size={16} />
                <input
                  aria-label="Filter VRFs"
                  placeholder="Find a VRF…"
                  value={query}
                  onChange={(e) => {
                    setQuery(e.target.value);
                    setRowPage(0);
                    setColPage(0);
                  }}
                />
              </div>
            </div>
            {snapshot && all.length > 0 ? (
              <>
                <div
                  className="matrix-scroll"
                  onKeyDown={(e) => {
                    const node = e.target as HTMLElement;
                    if (!node.dataset.cell) return;
                    let [r, c] = node.dataset.cell.split(",").map(Number);
                    if (e.key === "ArrowDown") r++;
                    else if (e.key === "ArrowUp") r--;
                    else if (e.key === "ArrowRight") c++;
                    else if (e.key === "ArrowLeft") c--;
                    else return;
                    e.preventDefault();
                    const target =
                      e.currentTarget.querySelector<HTMLButtonElement>(
                        `[data-cell="${r},${c}"]`,
                      );
                    target?.focus();
                  }}
                >
                  <table className="matrix">
                    <thead>
                      <tr>
                        <th className="corner">
                          <span>SOURCE / DESTINATION</span>
                          <ArrowLeftRight size={15} />
                        </th>
                        {col.map((v) => (
                          <th
                            key={v.name}
                            title={
                              v.reason || "Exports " + v.exports.join(", ")
                            }
                          >
                            {v.name}
                            <small>
                              {v.vni
                                ? "VNI " + v.vni
                                : v.exports.join(", ") || "No export RT"}
                            </small>
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {row.map((a, ri) => (
                        <tr key={a.name}>
                          <th title={a.reason || a.exports.join(", ")}>
                            {a.name}
                            <small>
                              {a.reason ? (
                                <span className="read-only">
                                  Read-only · inspect
                                </span>
                              ) : (
                                a.exports.join(", ")
                              )}
                            </small>
                          </th>
                          {col.map((b, ci) => {
                            const p = edits[pair(a.name, b.name)];
                            const n = relation(a, b);
                            const readOnly = !!a.reason || !!b.reason;
                            const unknown =
                              a.known === false || b.known === false;
                            const state = p
                              ? p.enabled
                                ? "on"
                                : "off"
                              : unknown
                                ? "unknown"
                                : n === 2
                                  ? "on"
                                  : n === 1
                                    ? "partial"
                                    : "off";
                            return (
                              <td key={b.name}>
                                {
                                  <button
                                    data-cell={`${ri},${ci}`}
                                    className={`cell ${state} ${p ? "pending" : ""}`}
                                    disabled={
                                      !admin ||
                                      readOnly ||
                                      unknown ||
                                      !!stale ||
                                      !!operation ||
                                      busy
                                    }
                                    aria-pressed={state === "on"}
                                    title={
                                      a.reason ||
                                      b.reason ||
                                      `${a.name === b.name ? "Import own export RTs from other PEs" : a.name + " / " + b.name}: ${state}`
                                    }
                                    aria-label={`${a.name} and ${b.name}: ${state}${p ? " pending" : ""}`}
                                    onClick={() => toggle(a, b)}
                                  >
                                    {state === "on" ? (
                                      <Check size={16} />
                                    ) : state === "unknown" ? (
                                      <LockKeyhole size={12} />
                                    ) : state === "partial" ? (
                                      <span>½</span>
                                    ) : (
                                      <span>·</span>
                                    )}
                                  </button>
                                }
                              </td>
                            );
                          })}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {!filtered.length && (
                  <div className="no-match">No VRFs match your search.</div>
                )}
                {filtered.length > 24 && (
                  <div className="pagination">
                    {[
                      ["Rows", rowPage, setRowPage],
                      ["Columns", colPage, setColPage],
                    ].map(([label, page, set]) => (
                      <div key={String(label)}>
                        {String(label)} {Number(page) * 24 + 1}–
                        {Math.min((Number(page) + 1) * 24, filtered.length)} of{" "}
                        {filtered.length}
                        <button
                          className="icon"
                          disabled={Number(page) === 0}
                          onClick={() =>
                            (set as (x: number) => void)(Number(page) - 1)
                          }
                          aria-label={`Previous ${label}`}
                        >
                          <ChevronLeft size={16} />
                        </button>
                        <button
                          className="icon"
                          disabled={(Number(page) + 1) * 24 >= filtered.length}
                          onClick={() =>
                            (set as (x: number) => void)(Number(page) + 1)
                          }
                          aria-label={`Next ${label}`}
                        >
                          <ChevronRight size={16} />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
              </>
            ) : (
              <Blank
                title={
                  busy
                    ? "Discovering VRFs…"
                    : snapshot
                      ? "No matching VRFs"
                      : "Waiting for router state"
                }
              >
                {busy
                  ? "Reading configuration from the selected VyOS router."
                  : snapshot
                    ? `No ${mode === "mpls" ? family + " MPLS" : "EVPN"} VRF configuration was discovered. Existing configuration is required; this application manages imports only.`
                    : "Use Refresh to retrieve configuration. No relationship state is assumed."}
              </Blank>
            )}
            <div className="matrix-legend">
              <span>
                <i className="legend-on" />
                Connected
              </span>
              <span>
                <i />
                Disconnected
              </span>
              <span>
                <i className="legend-partial" />
                Partial
              </span>
              <span>
                <LockKeyhole size={12} />
                Read-only / unknown
              </span>
              <span className="legend-pending">Outlined · pending change</span>
            </div>
          </section>
          {all.some((v) => v.reason) && (
            <details className="disclosure">
              <summary>
                {all.filter((v) => v.reason).length} VRFs need attention before
                editing
              </summary>
              {all
                .filter((v) => v.reason)
                .map((v) => (
                  <p key={v.name}>
                    <strong>{v.name}</strong> — {v.reason}
                  </p>
                ))}
            </details>
          )}
          <div className="matrix-footnote">
            <ShieldCheck size={17} />
            <span>
              This matrix displays explicitly configured route-target imports,
              not packet reachability or firewall isolation. Router
              configuration is the source of truth.
            </span>
          </div>
          {pending.length > 0 && !operation && (
            <div className="apply-bar">
              <span>
                <strong>
                  {pending.length} pair change{pending.length > 1 ? "s" : ""}
                </strong>
                <small>Not applied to {device.name}</small>
              </span>
              <button
                className="secondary"
                disabled={busy}
                onClick={() => {
                  setEdits({});
                  setPlan(undefined);
                }}
              >
                Discard
              </button>
              <button
                className="primary"
                disabled={busy || !!stale}
                onClick={review}
              >
                Review changes
                <ArrowRight size={16} />
              </button>
            </div>
          )}
        </>
      )}
      {help && (
        <Modal
          title="A simple matrix, in both directions"
          onClose={() => setHelp(false)}
        >
          <p>
            Each intersection represents a pair of VRFs on the selected router.
            Selecting it stages both imports together. Review the exact
            commands, then apply.
          </p>
          <p>
            Diagonal cells import a VRF's own export targets to receive
            corresponding VRF routes from other PEs.
          </p>
          <p>
            EVPN requires explicit export RTs configured on VyOS. Automatic RTs
            are not discovered or displayed. Multiple L3VNIs may share one
            external VXLAN interface.
          </p>
          <p>
            A partial relationship means only one direction currently matches.
            Enabling it completes the pair. Read-only cells include an
            explanation in the attention section.
          </p>
          <p>
            Shared targets can affect other relationships. The planner blocks
            pair changes that would alter another visible pair. Routers
            elsewhere may use the same targets, so review the target scope.
          </p>
          <p>
            Use a single-writer window when applying: an external CLI edit can
            race the API’s read and commit.
          </p>
          <button className="primary" onClick={() => setHelp(false)}>
            Got it
          </button>
        </Modal>
      )}
      {plan && (
        <Modal
          wide
          title="Review configuration changes"
          onClose={() => {
            if (!busy) setPlan(undefined);
          }}
        >
          <div className="review-target">
            <Server size={18} />
            <strong>{device?.name}</strong>
            <span>
              {device?.host} · {mode.toUpperCase()}
            </span>
          </div>
          <div className="pair-list">
            {pending.map((e) => (
              <div key={pair(e.a, e.b)}>
                <Badge tone={e.enabled ? "green" : "amber"}>
                  {e.enabled ? "Connect" : "Disconnect"}
                </Badge>
                <strong>{e.a}</strong>
                <ArrowLeftRight size={15} />
                <strong>{e.b}</strong>
              </div>
            ))}
          </div>
          <h3>Exact API commands</h3>
          <pre className="command-preview">
            {plan.commands
              .map(
                (c) =>
                  `${c.op} ${c.path.map((x) => JSON.stringify(x)).join(" ")}`,
              )
              .join("\n") || "No configuration change needed."}
          </pre>
          <div className="alert warning">
            Apply during a single-writer window. Concurrent external edits
            cannot be atomically excluded.
          </div>
          {plan.warnings.map((w) => (
            <p key={w} className="warning-text">
              {w}
            </p>
          ))}
          <div className="modal-actions">
            <button
              className="secondary"
              disabled={busy}
              onClick={() => setPlan(undefined)}
            >
              Back to matrix
            </button>
            <button className="primary" disabled={busy} onClick={apply}>
              {busy
                ? "Applying and verifying…"
                : plan.save
                  ? "Apply, verify & save"
                  : "Apply & verify"}
              <Check size={16} />
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}
