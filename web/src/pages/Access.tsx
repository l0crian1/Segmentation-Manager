import { ArrowRight, LockKeyhole, Network, ShieldCheck } from "lucide-react";
import React, { useState } from "react";
import { Badge, Brand, Field } from "../components/ui";
import { api, errorText } from "../lib/api";
export default function Access({
  setup,
  onReady,
}: {
  setup: boolean;
  onReady: () => void;
}) {
  const [form, setForm] = useState({
    claim: "",
    name: "",
    password: "",
    title: "Segmentation Manager",
  });
  const [step, setStep] = useState(0);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [restore, setRestore] = useState(false);
  const [file, setFile] = useState<File>();
  const update = (key: string, value: string) =>
    setForm((f) => ({ ...f, [key]: value }));
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    if (setup && !restore && step < 1) {
      setStep(1);
      return;
    }
    setBusy(true);
    try {
      if (restore) {
        if (!file) throw new Error("Choose an encrypted backup");
        const f = new FormData();
        f.set("claim", form.claim);
        f.set("password", form.password);
        f.set("file", file);
        const res = await fetch("/api/restore", { method: "POST", body: f });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error);
        location.reload();
        return;
      }
      await api(
        setup ? "setup" : "login",
        "POST",
        setup ? form : { name: form.name, password: form.password },
      );
      onReady();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="access">
      <div className="access-story">
        <Brand />
        <div>
          <span className="eyebrow">LESS CONFIGURATION. MORE CLARITY.</span>
          <h1>
            Every relationship.
            <br />
            One clear view.
          </h1>
          <p>
            Focused microsegmentation for your VyOS fleet.
            <br />
            Connect VRFs with confidence, one pair at a time.
          </p>
          <div className="sample-net">
            <span>APPLICATIONS</span>
            <i />
            <Network size={45} />
            <i />
            <span>SHARED SERVICES</span>
          </div>
        </div>
        <small>
          MPLS L3VPN <span> / </span> VXLAN EVPN
        </small>
      </div>
      <div className="access-form">
        <div className="access-card">
          <Badge tone="green">
            <ShieldCheck size={12} />
            {setup ? "FIRST-TIME SETUP" : "SECURE WORKSPACE"}
          </Badge>
          <h2>
            {restore
              ? "Restore your workspace"
              : setup
                ? step === 0
                  ? "Make it yours"
                  : "Create your administrator"
                : "Welcome back"}
          </h2>
          <p>
            {restore
              ? "Restore an encrypted backup into this fresh installation."
              : setup
                ? "A few essentials, then you’re ready to connect your first router."
                : "Sign in to manage your route-target relationships."}
          </p>
          {setup && !restore && (
            <div className="steps">
              <span className={step === 0 ? "active" : ""}>
                01 · Installation
              </span>
              <span className={step === 1 ? "active" : ""}>
                02 · Administrator
              </span>
            </div>
          )}
          <form onSubmit={submit}>
            {setup && (step === 0 || restore) && (
              <>
                <Field
                  label="Installation claim code"
                  help="Find the one-time code in this container’s startup logs."
                >
                  <input
                    autoFocus
                    required
                    value={form.claim}
                    onChange={(e) => update("claim", e.target.value)}
                    autoComplete="off"
                  />
                </Field>
                {!restore && (
                  <Field label="Workspace name">
                    <input
                      required
                      maxLength={100}
                      value={form.title}
                      onChange={(e) => update("title", e.target.value)}
                    />
                  </Field>
                )}
              </>
            )}
            {(!setup || step === 1) && !restore && (
              <Field label="Username">
                <input
                  autoFocus
                  required
                  minLength={2}
                  maxLength={64}
                  autoComplete="username"
                  value={form.name}
                  onChange={(e) => update("name", e.target.value)}
                />
              </Field>
            )}
            {(!setup || step === 1 || restore) && (
              <Field
                label={restore ? "Backup password" : "Password"}
                help={setup ? "Use at least 12 characters." : undefined}
              >
                <input
                  required
                  type="password"
                  minLength={setup ? 12 : 1}
                  maxLength={256}
                  autoComplete={setup ? "new-password" : "current-password"}
                  value={form.password}
                  onChange={(e) => update("password", e.target.value)}
                />
              </Field>
            )}
            {restore && (
              <Field label="Encrypted backup">
                <input
                  required
                  type="file"
                  accept=".msb"
                  onChange={(e) => setFile(e.target.files?.[0])}
                />
              </Field>
            )}
            {error && (
              <div className="alert error" role="alert">
                {error}
              </div>
            )}
            <button className="primary full" disabled={busy}>
              {busy
                ? "Please wait…"
                : restore
                  ? "Restore workspace"
                  : setup
                    ? step === 0
                      ? "Continue"
                      : "Create workspace"
                    : "Sign in"}
              <ArrowRight size={17} />
            </button>
          </form>
          {setup && (
            <div className="access-links">
              {step === 1 && !restore && (
                <button className="link" onClick={() => setStep(0)}>
                  Back
                </button>
              )}
              <button
                className="link"
                onClick={() => {
                  setRestore(!restore);
                  setError("");
                }}
              >
                {restore ? "Set up a new workspace" : "Restore from a backup"}
              </button>
            </div>
          )}
          <div className="secure-note">
            <LockKeyhole size={14} />
            Credentials stay with this installation.
          </div>
        </div>
      </div>
    </div>
  );
}
