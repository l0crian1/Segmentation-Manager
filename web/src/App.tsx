import {
  ArrowRight,
  Globe2,
  Layers3,
  LogOut,
  Network,
  Server,
  Settings2,
  ShieldCheck,
  Users,
  X,
} from "lucide-react";
import React, { useEffect, useRef, useState } from "react";
import { ThemeSelector } from "./components/ThemeSelector";
import { Brand } from "./components/ui";
import { api, errorText, setCSRF } from "./lib/api";
import Access from "./pages/Access";
import Accounts from "./pages/Accounts";
import Devices from "./pages/Devices";
import Matrix from "./pages/Matrix";
import Settings from "./pages/Settings";
import Sites from "./pages/Sites";
import VRFs from "./pages/VRFs";
import type { Device, Prefs, Site, User } from "./types";
export default function App() {
  const [ready, setReady] = useState<boolean>();
  const [user, setUser] = useState<User>();
  const [prefs, setPrefs] = useState<Prefs>({
    name: "Segmentation Manager",
    refresh: 30,
    timeout: 15,
    session_hours: 8,
    retention_days: 90,
  });
  const [nav, setNav] = useState("mpls");
  const [sites, setSites] = useState<Site[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [selected, setSelected] = useState(0);
  const [siteFilter, setSiteFilter] = useState(0);
  const [notice, setNotice] = useState("");
  const [failure, setFailure] = useState("");
  const [loading, setLoading] = useState(true);
  const pending = useRef(false);
  useEffect(() => {
    const listener = (e: Event) => {
      pending.current = Boolean((e as CustomEvent).detail);
    };
    window.addEventListener("segmentation-manager-pending", listener);
    return () =>
      window.removeEventListener("segmentation-manager-pending", listener);
  }, []);
  async function inventory() {
    const [s, d] = await Promise.all([
      api<Site[]>("sites"),
      api<Device[]>("devices"),
    ]);
    setSites(s);
    setDevices(d);
    setSelected((old) => (d.some((x) => x.id === old) ? old : d[0]?.id || 0));
  }
  async function boot() {
    setLoading(true);
    try {
      const s = await api("setup");
      setReady(s.configured);
      if (s.configured) {
        try {
          const m = await api("me");
          setCSRF(m.csrf);
          setUser(m.user);
          setPrefs(m.settings);
          await inventory();
        } catch {
          setUser(undefined);
        }
      }
    } catch (e) {
      setFailure(errorText(e));
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    void boot();
  }, []);
  function report(e: unknown) {
    setFailure(errorText(e));
    setNotice("");
    window.dispatchEvent(
      new CustomEvent("segmentation-manager-error", { detail: errorText(e) }),
    );
  }
  function done(message: string) {
    setNotice(message);
    setFailure("");
    void inventory();
  }
  const selectedDevice = devices.find((d) => d.id === selected);
  const isAdmin = user?.role === "administrator";
  const canConfigure = isAdmin || user?.role === "network-admin";
  if (loading)
    return (
      <div className="loading">
        <Network size={30} />
        <p>Opening your workspace…</p>
      </div>
    );
  if (ready === undefined)
    return (
      <div className="loading">
        <p>{failure || "Could not reach the application"}</p>
        <button onClick={boot}>Retry</button>
      </div>
    );
  if (!ready || !user)
    return (
      <>
        <Access setup={!ready} onReady={boot} />
        <ThemeSelector access />
      </>
    );
  const links = [
    { id: "mpls", label: "MPLS", icon: Network, sub: "L3VPN" },
    { id: "evpn", label: "VXLAN", icon: Layers3, sub: "EVPN" },
    { id: "vrfs", label: "VRFs", icon: Layers3 },
    { id: "sites", label: "Sites", icon: Globe2 },
    { id: "devices", label: "Devices", icon: Server },
    { id: "users", label: "Users", icon: Users },
    { id: "settings", label: "Settings", icon: Settings2 },
  ];
  return (
    <div className="app">
      <aside className="sidebar">
        <Brand />
        <div className="workspace-label">{prefs.name}</div>
        <div className="nav-label">MICROSEGMENTATION</div>
        <nav>
          {links.map((item, i) => (
            <React.Fragment key={item.id}>
              {i === 3 && <div className="nav-label management">WORKSPACE</div>}
              <button
                className={nav === item.id ? "nav-item current" : "nav-item"}
                onClick={() => {
                  if (pending.current && !confirm("Discard pending changes?"))
                    return;
                  setNav(item.id);
                  setFailure("");
                  setNotice("");
                }}
              >
                <item.icon size={19} />
                <span>{item.label}</span>
                {item.sub && <small>{item.sub}</small>}
              </button>
            </React.Fragment>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="scope-note">
            <ShieldCheck size={17} />
            <span>
              VRFs and route-target policies.
              <br />
              Your router stays in control.
            </span>
          </div>
          <div className="profile">
            <span className="avatar">
              {user.name.slice(0, 2).toUpperCase()}
            </span>
            <span>
              {user.name}
              <small>
                {isAdmin
                  ? "Administrator"
                  : canConfigure
                    ? "Network admin"
                    : "View-only"}
              </small>
            </span>
            <button
              className="icon"
              aria-label="Sign out"
              onClick={async () => {
                await api("logout", "POST");
                setUser(undefined);
              }}
            >
              <LogOut size={17} />
            </button>
          </div>
        </div>
      </aside>
      <div className="main">
        <header className="topbar">
          <div className="breadcrumb">
            Workspace <span>/</span>{" "}
            <strong>{links.find((x) => x.id === nav)?.label}</strong>
          </div>
          <ThemeSelector />
          <span className="local-badge">
            <span />
            LOCAL CONTROL PLANE
          </span>
        </header>
        <main>
          {(failure || notice) && (
            <div
              className={"alert " + (failure ? "error" : "success")}
              role={failure ? "alert" : "status"}
            >
              <span>{failure || notice}</span>
              <button
                className="icon"
                aria-label="Dismiss message"
                onClick={() => {
                  setFailure("");
                  setNotice("");
                }}
              >
                <X size={15} />
              </button>
            </div>
          )}
          {(nav === "mpls" || nav === "evpn") && (
            <Matrix
              key={nav}
              mode={nav}
              device={selectedDevice}
              devices={devices.filter(
                (d) => !siteFilter || d.site_id === siteFilter,
              )}
              sites={sites}
              siteFilter={siteFilter}
              setSiteFilter={(id) => {
                setSiteFilter(id);
                const first = devices.find((d) => !id || d.site_id === id);
                setSelected(first?.id || 0);
              }}
              selectDevice={setSelected}
              admin={canConfigure}
              prefs={prefs}
              onError={report}
              onDone={done}
              onDevices={() => setNav("devices")}
            />
          )}
          {nav === "vrfs" && (
            <VRFs
              key={selected}
              device={selectedDevice}
              devices={devices}
              sites={sites}
              selectDevice={setSelected}
              admin={canConfigure}
              onDone={done}
              onError={report}
            />
          )}
          {nav === "sites" && (
            <Sites
              sites={sites}
              devices={devices}
              admin={canConfigure}
              onDone={done}
              onError={report}
              onOpen={(id) => {
                setSiteFilter(id);
                setNav("devices");
              }}
            />
          )}
          {nav === "devices" && (
            <Devices
              devices={devices}
              sites={sites}
              admin={canConfigure}
              filter={siteFilter}
              setFilter={setSiteFilter}
              onDone={done}
              onError={report}
              manage={(id) => {
                setSelected(id);
                setNav("mpls");
              }}
            />
          )}
          {nav === "users" && (
            <Accounts admin={isAdmin} onError={report} onDone={done} />
          )}
          {nav === "settings" && (
            <Settings
              administrator={isAdmin}
              canConfigure={canConfigure}
              prefs={prefs}
              setPrefs={setPrefs}
              onError={report}
              onDone={done}
            />
          )}
        </main>
        <footer>
          Segmentation Manager <span>Focused VyOS route-target management</span>
          <a
            href="https://docs.vyos.io/en/rolling/configuration/vrf/index.html"
            target="_blank"
            rel="noreferrer"
          >
            VyOS documentation <ArrowRight size={12} />
          </a>
        </footer>
      </div>
    </div>
  );
}
