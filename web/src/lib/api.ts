import type { VRF } from "../types";
export let csrf = "";
export function setCSRF(value: string) {
  csrf = value;
}
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
  }
}
export async function api<T = any>(
  path: string,
  method = "GET",
  data?: unknown,
): Promise<T> {
  const res = await fetch("/api/" + path, {
    method,
    headers: {
      ...(data !== undefined ? { "Content-Type": "application/json" } : {}),
      "X-CSRF-Token": csrf,
    },
    body: data === undefined ? undefined : JSON.stringify(data),
  });
  const out = await res.json();
  if (!res.ok) throw new ApiError(out.error || "Request failed", res.status);
  return out;
}
export const errorText = (e: unknown) =>
  e instanceof Error
    ? e.message
    : typeof e === "string"
      ? e
      : "Something went wrong";
export const date = (x: string) =>
  x ? new Date(x).toLocaleString() : "Not checked";
export const pair = (a: string, b: string) => JSON.stringify([a, b].sort());
export function relation(a: VRF, b: VRF) {
  return (
    Number(a.imports.some((t) => b.exports.includes(t))) +
    Number(b.imports.some((t) => a.exports.includes(t)))
  );
}
