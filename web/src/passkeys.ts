import { api, type Session } from "./api";

export function passkeysSupported() {
  return (
    window.isSecureContext &&
    typeof PublicKeyCredential !== "undefined" &&
    typeof PublicKeyCredential.parseCreationOptionsFromJSON === "function" &&
    typeof PublicKeyCredential.parseRequestOptionsFromJSON === "function"
  );
}

export async function registerPasskey(name: string, password: string) {
  if (!passkeysSupported()) throw new Error("Las passkeys requieren HTTPS (o localhost) y un navegador con WebAuthn moderno.");
  const options = await api<{ publicKey: Parameters<typeof PublicKeyCredential.parseCreationOptionsFromJSON>[0] }>(
    "POST",
    "/api/auth/passkeys/register/begin",
    { name, password },
  );
  const credential = await navigator.credentials.create({ publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(options.publicKey) });
  if (!(credential instanceof PublicKeyCredential)) throw new Error("No se creó la passkey.");
  await api("POST", "/api/auth/passkeys/register/finish", credential.toJSON());
}

export async function loginPasskey(): Promise<Session> {
  if (!passkeysSupported()) throw new Error("Las passkeys requieren HTTPS (o localhost) y un navegador con WebAuthn moderno.");
  const options = await api<{ publicKey: Parameters<typeof PublicKeyCredential.parseRequestOptionsFromJSON>[0] }>(
    "POST",
    "/api/auth/passkeys/login/begin",
    {},
  );
  const credential = await navigator.credentials.get({ publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(options.publicKey) });
  if (!(credential instanceof PublicKeyCredential)) throw new Error("No se recibió la passkey.");
  return api<Session>("POST", "/api/auth/passkeys/login/finish", credential.toJSON());
}
