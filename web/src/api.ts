export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

let csrf = "";
export const setCsrf = (t: string) => {
  csrf = t;
};

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: {
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
      ...(csrf ? { "X-CSRF-Token": csrf } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, (data as { error?: string }).error ?? `Error ${res.status}`);
  return data as T;
}

export type Env = "local" | "dev" | "staging" | "prod";
export type TLSMode = "disabled" | "required" | "verify_ca" | "verify_identity";

export interface Session {
  username: string;
  role: string;
  csrf: string;
}

export interface Connection {
  id: string;
  name: string;
  env: Env;
  host: string;
  port: number;
  username: string;
  database: string;
  tlsMode: TLSMode;
  readOnly: boolean;
  hasPassword: boolean;
  hasCaCert: boolean;
  hasClientCert: boolean;
}

export interface ConnectionInput {
  name: string;
  env: Env;
  host: string;
  port: number;
  username: string;
  database: string;
  tlsMode: TLSMode;
  readOnly: boolean;
  password?: string;
  caCert?: string;
  clientCert?: string;
  clientKey?: string;
}

export interface TestResponse {
  ok: boolean;
  error?: string;
  result?: { serverVersion: string; tls: boolean; tlsCipher?: string; tlsVersion?: string; latencyMs: number };
}
