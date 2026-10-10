export type User = { id: number; name: string; role: string };
export type Site = { id: number; name: string; description: string };
export type Device = {
  id: number;
  site_id: number;
  name: string;
  host: string;
  port: number;
  trust: string;
  fingerprint: string;
  save: boolean;
  status: string;
  checked: string;
  version: string;
};
export type Prefs = {
  name: string;
  refresh: number;
  timeout: number;
  session_hours: number;
  retention_days: number;
};
export type VRF = {
  name: string;
  imports: string[];
  exports: string[];
  vni?: string;
  reason?: string;
  both: boolean;
  known?: boolean;
};
export type Snapshot = {
  device_id: number;
  mode: string;
  family: string;
  version: string;
  at: string;
  fingerprint: string;
  vrfs: VRF[];
  warnings: string[];
};
export type Edit = { a: string; b: string; enabled: boolean };
export type Plan = {
  commands: { op: string; path: string[] }[];
  before: Snapshot;
  after: VRF[];
  warnings: string[];
  save: boolean;
};
