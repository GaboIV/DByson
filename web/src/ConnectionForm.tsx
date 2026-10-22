import { useEffect, useRef, useState, type FormEvent } from "react";
import { useClipboard } from "./clipboard";
import { api, emptyHop, type Connection, type ConnectionInput, type Discovery, type Env, type SSHConfig, type SSHHop, type TestResponse, type TLSMode } from "./api";

interface Props { existing?: Connection; initial?: Partial<ConnectionInput>; onSaved: (c: Connection) => void; onCancel: () => void }
const steps = ["Destino", "Ruta y SSH", "Protección", "Comprobar"];
const normalizeSSH = (c?: SSHConfig): SSHConfig => ({...emptyHop(), ...c, enabled: Boolean(c?.enabled), port: c?.port || 22, auth: c?.auth || "key", jump: c?.jump ? {...c.jump, port: c.jump.port || 22, auth: c.jump.auth || "key"} : undefined});
const message = (e: unknown) => e instanceof Error ? e.message : "No se pudo completar la operación";

function HopEditor({ value, onChange, title, jump, connectionId }: { value: SSHHop; onChange: (h: SSHHop) => void; title: string; jump?: SSHHop; connectionId?: string }) {
 const [observation, setObservation] = useState<{fingerprint: string; algorithm: string} | null>(null);
 const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
 const request = useRef<AbortController | null>(null);
 useEffect(() => { setObservation(null); request.current?.abort(); setBusy(false); }, [value.host, value.port, jump?.host, jump?.port]);
 useEffect(() => () => request.current?.abort(), []);
 function update(p: Partial<SSHHop>) { onChange({ ...value, ...p, ...((p.host !== undefined || p.port !== undefined) ? {fingerprint: ""} : {}) }); }
 async function observe() {
  request.current?.abort(); const controller = new AbortController(); request.current = controller; setBusy(true); setError(""); setObservation(null);
  try { const r = await api<{fingerprint: string; algorithm: string}>("POST", connectionId ? `/api/connections/${connectionId}/ssh/fingerprint` : "/api/ssh/fingerprint", {host: value.host, port: value.port, user: value.user, ...(jump ? {jump} : {})}, {signal: controller.signal}); if (!controller.signal.aborted) setObservation(r); }
  catch (e) { if (!controller.signal.aborted) setError(message(e)); } finally { if (!controller.signal.aborted) setBusy(false); }
 }
 async function attach(file?: File) {
  if (!file) return; if (file.size > 128 * 1024) { setError("La clave debe ocupar como máximo 128 KiB"); return; }
  try { update({privateKey: await file.text(), keyFile: "", keyLabel: file.name, passphrase: ""}); setError(""); } catch { setError("No se pudo leer el archivo"); }
 }
 return <section className="ssh-hop">
  <h3>{title}</h3>
  <div className="form-columns">
   <label>Host SSH<input value={value.host} onChange={e => update({host: e.target.value})} placeholder="ssh.ejemplo.com" required /></label>
   <label>Puerto SSH<input type="number" min={1} max={65535} value={value.port} onChange={e => update({port: Number(e.target.value)})} required /></label>
   <label>Usuario SSH<input value={value.user} onChange={e => update({user: e.target.value})} required autoComplete="off" /></label>
  </div>
  <label>Autenticación<select value={value.auth} onChange={e => update({auth: e.target.value as SSHHop["auth"]})}><option value="key">Clave privada · OpenSSH / PPK</option><option value="password">Contraseña SSH</option><option value="agent">ssh-agent del servidor DByson</option></select></label>
  {value.auth === "password" && <label>Contraseña SSH<input type="password" value={value.password || ""} onChange={e => update({password: e.target.value})} placeholder={value.hasPassword ? "Guardada; deja vacío para conservarla" : ""} autoComplete="new-password" /></label>}
  {value.auth === "key" && <>
   <label className="file-field">Adjuntar clave privada<input type="file" accept=".ppk,.pem,.key,.openssh,*" onChange={e => void attach(e.target.files?.[0])} /><small className="muted">{value.keyLabel || (value.hasPrivateKey ? "Clave guardada en el vault" : "OpenSSH o PuTTY PPK v2/v3. El archivo se guarda cifrado.")}</small></label>
   <label>Passphrase de la clave<input type="password" value={value.passphrase || ""} onChange={e => update({passphrase: e.target.value})} autoComplete="new-password" placeholder="Si la clave está cifrada" /></label>
   <details><summary>Usar un archivo montado en el servidor</summary><label>Ruta dentro del directorio de claves<input value={value.keyFile || ""} onChange={e => update({keyFile: e.target.value, privateKey: "", keyLabel: ""})} placeholder="equipo/id_ed25519 o equipo/servidor.ppk" /></label><p className="muted">Requiere ADMINBASE_SSH_KEY_DIR en el servidor. Al compartir, el receptor necesitará esa ruta en su propia instalación o adjuntar la clave.</p></details>
  </>}
  {value.auth === "agent" && <p className="muted">Usa el agente configurado con SSH_AUTH_SOCK en el servidor; no el agente del navegador.</p>}
  <div className="host-verification">
   <div className="section-heading"><strong>Identidad del servidor</strong><button type="button" onClick={() => void observe()} disabled={busy || !value.host}>{busy ? "Leyendo huella…" : "Obtener huella"}</button></div>
   <label>Huella de confianza<input className="mono" value={value.fingerprint} onChange={e => update({fingerprint: e.target.value})} placeholder="SHA256:…" /></label>
   {observation && <div className="fingerprint-observation"><span>{observation.algorithm}</span><code>{observation.fingerprint}</code><p>Contrasta esta huella con el administrador del servidor antes de aceptarla.</p><button type="button" onClick={() => {update({fingerprint: observation.fingerprint}); setObservation(null);}}>Confiar en esta huella</button></div>}
   {value.fingerprint && <small className="okmsg">Huella fijada. Un cambio bloqueará la conexión.</small>}
  </div>
  {error && <p className="error" role="alert">{error}</p>}
 </section>;
}

export function ConnectionForm({ existing, initial, onSaved, onCancel }: Props) {
 const {copy,copyNotice}=useClipboard();
 const [draft, setDraft] = useState<ConnectionInput>(() => ({name: existing?.name || "", env: existing?.env || "dev", host: existing?.host || "", port: existing?.port || 3306, username: existing?.username || "", database: existing?.database || "", tlsMode: existing?.tlsMode || "verify_identity", readOnly: existing?.readOnly || false, ssh: normalizeSSH(existing?.ssh), ...initial}));
 const [step, setStep] = useState(0); const [error, setError] = useState(""); const [test, setTest] = useState<TestResponse | null>(null);
 const [busy, setBusy] = useState(""); const [discovery, setDiscovery] = useState<Discovery | null>(null);
 const request = useRef<AbortController | null>(null); const dirty = useRef(false);
 useEffect(() => () => request.current?.abort(), []);
 function update(p: Partial<ConnectionInput>) { dirty.current = true; setDraft(d => ({...d,...p})); setTest(null); setError(""); }
 const ssh = draft.ssh || {...emptyHop(), enabled: false};
 function setSSH(p: Partial<SSHConfig>) { update({ssh: {...ssh,...p}}); }
 function payload() { const p = {...draft}; for (const k of ["password", "caCert", "clientCert", "clientKey"] as const) if (!p[k]) delete p[k]; return p; }
 async function discover(current = false) {
  request.current?.abort(); const controller = new AbortController(); request.current = controller; setBusy("discovery");setError("");
  try { setDiscovery(await api<Discovery>("POST", "/api/discovery", current ? {host: draft.host, port: draft.port} : {}, {signal: controller.signal})); }
  catch(e) { if (!controller.signal.aborted) setError(message(e)); } finally { if (!controller.signal.aborted) setBusy(""); }
 }
 async function runTest() {
  request.current?.abort(); const controller = new AbortController(); request.current = controller; setBusy("test"); setError(""); setTest(null);
  try { setTest(await api<TestResponse>("POST", existing ? `/api/connections/${existing.id}/test-draft` : "/api/connections/test", payload(), {signal: controller.signal})); }
  catch(e) { if (!controller.signal.aborted) setError(message(e)); } finally { if (!controller.signal.aborted) setBusy(""); }
 }
 async function submit(e: FormEvent) {
  e.preventDefault(); if (step < 3) { setStep(step + 1); return; }
  setBusy("save"); setError("");
  try { onSaved(await api<Connection>(existing ? "PUT" : "POST", existing ? `/api/connections/${existing.id}` : "/api/connections", payload())); dirty.current = false; }
  catch (e) { setError(message(e)); } finally { setBusy(""); }
 }
 function cancel() { if (!dirty.current || window.confirm("¿Descartar los cambios de esta conexión?")) onCancel(); }
 return <form className="connection-wizard card" onSubmit={submit}>
  <div className="section-heading"><div><p className="eyebrow">CONEXIONES / {existing ? "EDITAR" : "NUEVA"}</p><h2>{existing ? existing.name : "Conecta tu próxima base de datos"}</h2><p className="muted">MySQL y MariaDB · directa, Docker o a través de SSH</p></div><button type="button" onClick={cancel} aria-label="Cerrar formulario">✕</button></div>
  <nav className="wizard-steps" aria-label="Pasos de conexión">{steps.map((label,i) => <button key={label} type="button" aria-current={step === i ? "step" : undefined} onClick={() => {setStep(i);setError("");}}><span>{i + 1}</span>{label}</button>)}</nav>
  <fieldset className="wizard-body" disabled={busy !== ""}>
  {step === 0 && <>
   <h3>¿Dónde están tus datos?</h3>
   <div className="connection-presets">
    <button type="button" onClick={() => {update({host: "host.docker.internal", env: "local"});void discover();}}><strong>Host de Docker</strong><small>MySQL publicado en tu equipo</small></button>
    <button type="button" onClick={() => {update({host: "mysql", env: "local"});void discover();}}><strong>Red de Docker</strong><small>Nombre del servicio en la misma red</small></button>
    <button type="button" onClick={() => {update({host: "127.0.0.1"});setSSH({enabled: true});}}><strong>Servidor por SSH</strong><small>Destino visto desde el servidor remoto</small></button>
   </div>
   <div className="form-columns"><label>Nombre<input autoFocus value={draft.name} onChange={e => update({name: e.target.value})} required maxLength={100} placeholder="Desarrollo · facturación" /></label><label>Entorno<select value={draft.env} onChange={e => update({env: e.target.value as Env})}>{["local","dev","staging","prod"].map(e => <option key={e}>{e}</option>)}</select></label></div>
   <div className="form-columns"><label>Host MySQL<input value={draft.host} onChange={e => update({host: e.target.value})} required placeholder="db.ejemplo.com" /></label><label>Puerto MySQL<input type="number" min={1} max={65535} value={draft.port} onChange={e => update({port: Number(e.target.value)})} required /></label></div>
   <div className="form-columns"><label>Usuario MySQL<input value={draft.username} onChange={e => update({username: e.target.value})} required autoComplete="off" /></label><label>Contraseña MySQL<input type="password" value={draft.password || ""} onChange={e => update({password: e.target.value})} autoComplete="new-password" placeholder={existing?.hasPassword ? "Guardada; deja vacío para conservarla" : ""} /></label></div>
   <label>Base de datos inicial<input value={draft.database} onChange={e => update({database: e.target.value})} placeholder="Opcional: podrás elegirla al conectar" /></label>
   <div className="discovery-panel"><div className="section-heading"><div><strong>Encontrar MySQL / MariaDB</strong><p className="muted">Comprueba nombres habituales y puertos locales 3306–3308.</p></div><button type="button" onClick={() => void discover()}>Buscar servicios</button></div><button type="button" disabled={!draft.host} onClick={() => void discover(true)}>Identificar este host y puerto</button>
   {discovery && <><p className="muted">{discovery.note}</p>{discovery.services.length === 0 && <p>No se encontraron servicios MySQL accesibles en los destinos comprobados.</p>}{discovery.services.map(s => <button type="button" className="discovered-service" key={`${s.host}:${s.port}`} onClick={() => update({host: s.host, port: s.port, name: draft.name || `${s.version.includes("MariaDB") ? "MariaDB" : "MySQL"} · ${s.host}`})}><strong>{s.host}:{s.port}</strong><span>{s.version}</span><small>{s.source} · Usar destino</small></button>)}<details><summary>Redes visibles {discovery.containerized ? "desde el contenedor" : "desde el servidor"}</summary>{discovery.networks.map((n,i) => <div className="network-discovery-row" key={i}><p className="mono">{n.name} · {n.addresses.join(", ")}</p>{n.name.startsWith("Docker · ") && /^[a-zA-Z0-9_.-]+$/.test(n.name.slice(9)) && <button type="button" onClick={() => void copy(`docker network connect ${n.name.slice(9)} adminbase`, "Comando copiado")}>Copiar acceso a esta red</button>}</div>)}<p>Para resolver nombres de servicios Docker, DByson y MySQL deben compartir una red.</p></details></>}
   </div>
  </>}
  {step === 1 && <>
   <h3>Elige la ruta de conexión</h3><label className="check"><input type="checkbox" checked={ssh.enabled} onChange={e => setSSH({enabled: e.target.checked})} />Conectar a través de un túnel SSH</label>
   {!ssh.enabled ? <div className="route-preview"><strong>DByson → {draft.host || "MySQL"}:{draft.port}</strong><p className="muted">Conexión directa desde el servidor DByson.</p></div> : <>
    <div className="route-preview">DByson → {ssh.jump ? `${ssh.jump.host || "Bastión"} → ` : ""}{ssh.host || "SSH"} → {draft.host || "MySQL"}:{draft.port}<p className="muted">El host MySQL se resuelve desde el servidor SSH. Para MySQL en ese servidor, usa 127.0.0.1.</p></div>
    <label className="check"><input type="checkbox" checked={Boolean(ssh.jump)} onChange={e => setSSH({jump: e.target.checked ? emptyHop() : undefined})} />Pasar primero por un bastión (ProxyJump)</label>
    {ssh.jump && <HopEditor title="1. Bastión" value={ssh.jump} onChange={jump => setSSH({jump})} connectionId={existing?.id} />}
    <HopEditor title={ssh.jump ? "2. Servidor SSH de destino" : "Servidor SSH"} value={ssh} onChange={hop => setSSH(hop)} jump={ssh.jump} connectionId={existing?.id} />
   </>}
  </>}
  {step === 2 && <>
   <h3>Protege la conexión</h3><label>Seguridad TLS de MySQL<select value={draft.tlsMode} onChange={e => update({tlsMode: e.target.value as TLSMode})}><option value="verify_identity">Verificar certificado y nombre del host</option><option value="verify_ca">Verificar certificado de la CA</option><option value="required">Cifrar sin verificar identidad</option><option value="disabled">Sin TLS MySQL</option></select></label>
   {draft.tlsMode === "disabled" && <p className="warn">{ssh.enabled ? "SSH protege el tramo hasta el servidor SSH. El tramo de ese servidor a MySQL no usará TLS." : "Las credenciales y los datos viajarán sin TLS. Usa esta opción solo en una red de confianza."}</p>}
   {draft.tlsMode === "required" && <p className="warn">Se cifran los datos, pero no se verifica la identidad del servidor MySQL.</p>}
   {draft.tlsMode !== "disabled" && <details><summary>Certificados MySQL</summary>{([['caCert','Certificado CA (PEM)',existing?.hasCaCert],['clientCert','Certificado de cliente (PEM)',existing?.hasClientCert],['clientKey','Clave privada TLS (PEM)',existing?.hasClientCert]] as const).map(([k,label,saved]) => <label key={k}>{label}{saved && <small>Guardado</small>}<textarea value={draft[k] || ""} onChange={e => update({[k]: e.target.value})} rows={4} spellCheck={false} /></label>)}</details>}
   <label className="check"><input type="checkbox" checked={draft.readOnly} onChange={e => update({readOnly: e.target.checked})} />Solo lectura</label><p className="muted">Bloquea las operaciones de escritura desde DByson y refuerza la sesión MySQL.</p>
  </>}
  {step === 3 && <>
   <h3>Todo listo para comprobar</h3><dl className="connection-summary"><dt>Conexión</dt><dd>{draft.name || "Sin nombre"} · {draft.env}</dd><dt>Destino MySQL</dt><dd>{draft.username}@{draft.host}:{draft.port}{draft.database && `/${draft.database}`}</dd><dt>Ruta</dt><dd>{ssh.enabled ? `${ssh.jump ? ssh.jump.host + " → " : ""}${ssh.user}@${ssh.host}:${ssh.port} → MySQL` : "Directa"}</dd><dt>Protección</dt><dd>TLS: {draft.tlsMode} · {draft.readOnly ? "Solo lectura" : "Lectura y escritura"}</dd></dl>
   {ssh.enabled && <p className="muted">Las claves privadas SSH se guardan cifradas y se comparten por separado. El perfil compartido conserva su referencia.</p>}
   <button type="button" onClick={() => void runTest()}>Probar conexión</button>
   {test && <div className={test.ok ? "test-result success" : "test-result failure"} role="status"><strong>{test.ok ? "Conexión correcta" : "No se pudo conectar"}</strong><p>{test.ok && test.result ? `${test.result.serverVersion} · ${test.result.latencyMs} ms · ${ssh.enabled ? "túnel SSH · " : ""}${test.result.tls ? `TLS ${test.result.tlsVersion}` : "sin TLS MySQL"}` : test.error}</p></div>}
  </>}
  </fieldset>
  {busy && <p className="muted" role="status">{busy === "discovery" ? "Buscando servicios…" : busy === "test" ? "Comprobando la ruta y MySQL…" : "Guardando conexión…"}</p>}
  {error && <p className="error" role="alert">{error}</p>}
  <div className="wizard-footer"><button type="button" onClick={cancel}>Cancelar</button><span className="spacer" />{step > 0 && <button type="button" onClick={() => setStep(step - 1)} disabled={Boolean(busy)}>Anterior</button>}<button className="primary" disabled={Boolean(busy)}>{step === 3 ? "Guardar conexión" : "Continuar →"}</button></div>
 {copyNotice}
 </form>;
}
