import { Network, X } from "lucide-react";
import React, { useEffect, useRef, useState } from "react";
export function Brand() {
  return (
    <div className="brand">
      <div className="brand-mark">
        <Network size={23} />
      </div>
      <span>
        Segmentation Manager<small>VYOS CONTROL</small>
      </span>
    </div>
  );
}

export function Field({
  label,
  help,
  children,
}: {
  label: string;
  help?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
      {help && <small>{help}</small>}
    </label>
  );
}

export function Modal({
  title,
  children,
  onClose,
  wide = false,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    ref.current?.showModal();
    const listener = (e: Event) => setError((e as CustomEvent).detail);
    window.addEventListener("segmentation-manager-error", listener);
    return () => {
      ref.current?.close();
      window.removeEventListener("segmentation-manager-error", listener);
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={wide ? "modal wide" : "modal"}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
    >
      <div className="modal-title">
        <h2>{title}</h2>
        <button className="icon" aria-label="Close dialog" onClick={onClose}>
          <X size={20} />
        </button>
      </div>
      {error && (
        <div className="alert error" role="alert">
          {error}
        </div>
      )}
      {children}
    </dialog>
  );
}

export function Blank({
  icon: Icon = Network,
  title,
  children,
  action,
}: {
  icon?: typeof Network;
  title: string;
  children: React.ReactNode;
  action?: React.ReactNode;
}) {
  return (
    <div className="blank">
      <span className="empty-icon">
        <Icon size={30} />
      </span>
      <h2>{title}</h2>
      <p>{children}</p>
      {action}
    </div>
  );
}

export function Badge({
  children,
  tone = "neutral",
}: {
  children: React.ReactNode;
  tone?: string;
}) {
  return <span className={"badge " + tone}>{children}</span>;
}
