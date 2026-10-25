import { test, expect, type Page, type APIRequestContext } from "@playwright/test";
import { execFileSync } from "node:child_process";

const password = "e2e-Administrator-2026!";
async function login(page: Page, url = "/") {
  await page.goto(url);
  await page.getByLabel("Usuario", { exact: true }).fill("e2e-admin");
  await page.getByLabel("Contraseña", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
}
async function table(page: Page, name: string) {
  await page.getByRole("button", { name: /^MySQL E2E/ }).click();
  await page
    .locator(".table-item:visible .table-name")
    .filter({ hasText: new RegExp(`^${name}$`) })
    .click();
  await expect(page.getByRole("tab", { name: "Contenido", exact: true })).toBeVisible();
}
async function tools(page: Page, name: string) {
  await page.getByRole("button", { name: /Herramientas · Importar/ }).click();
  await page.getByRole("navigation", { name: "Herramientas" }).getByRole("button", { name, exact: true }).click();
  return page.getByRole("dialog", { name: "Herramientas de productividad" });
}
async function confirm(page: Page, accept = true) {
  const d = page.getByRole("dialog").filter({ hasText: "Confirmar y ejecutar" });
  await expect(d).toBeVisible();
  if (!accept) {
    await d.getByRole("button", { name: "Cancelar", exact: true }).click();
    return;
  }
  const target = (await d.locator("label").innerText()).match(/«(.+)»/)![1];
  await d.getByRole("textbox").fill(target);
  await d.getByRole("button", { name: "Confirmar y ejecutar" }).click();
  await expect(d).toBeHidden();
}
function db(sql: string) {
  return execFileSync(
    "docker",
    [
      "compose",
      "-p",
      "dbyson-e2e",
      "-f",
      "../compose.e2e.yml",
      "exec",
      "-T",
      "database",
      "mysql",
      "--default-character-set=utf8mb4",
      "-uroot",
      "-pe2e-root-only",
      "--batch",
      "--skip-column-names",
      "dbyson_e2e",
      "-e",
      sql,
    ],
    { encoding: "utf8" },
  ).trim();
}
async function csrf(request: APIRequestContext) {
  return (await (await request.get("/api/auth/me")).json()).csrf as string;
}

export function productivityTests() {
  test.describe("Fase 3: productividad", () => {
    test("biblioteca del servidor: guardar, recargar, abrir, eliminar y EXPLAIN", async ({ page }, info) => {
      await login(page);
      await table(page, "items");
      await page.getByRole("button", { name: "⚡ SQL", exact: true }).click();
      const editor = page.locator(".cm-content:visible");
      await editor.fill("SELECT id, name FROM items WHERE id = 1;");
      await page.getByRole("button", { name: "Consultas guardadas", exact: true }).click();
      const lib = page.getByRole("dialog", { name: "Consultas guardadas" });
      await lib.getByLabel("Nombre para guardar").fill("Mi consulta persistente");
      await lib.getByRole("button", { name: "Guardar consulta actual" }).click();
      await expect(lib.locator("article")).toContainText("Mi consulta persistente");
      await lib.getByRole("button", { name: "Cerrar", exact: true }).click();
      await page.reload();
      // La recarga restaura la pestaña activa; se vuelve a Conexiones para seguir.
      await page.getByRole("tab", { name: "Conexiones", exact: true }).click();
      await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
      await table(page, "items");
      await page.getByRole("button", { name: "⚡ SQL", exact: true }).click();
      await page.getByRole("button", { name: "Consultas guardadas", exact: true }).click();
      await expect(lib.locator("article")).toContainText("Mi consulta persistente");
      await lib.getByRole("button", { name: "Abrir", exact: true }).click();
      await expect(page.locator(".cm-content:visible")).toHaveText("SELECT id, name FROM items WHERE id = 1;");
      await page.getByRole("button", { name: "EXPLAIN visual", exact: true }).click();
      await expect(page.getByRole("region", { name: "Plan visual EXPLAIN" })).toContainText("items");
      await page.screenshot({ path: info.outputPath("explain-visual.png"), fullPage: true });
      await page.getByRole("button", { name: "Consultas guardadas", exact: true }).click();
      await lib.getByRole("button", { name: "Eliminar consulta Mi consulta persistente" }).click();
      await expect(lib.locator("article")).toHaveCount(0);
    });
    test("importación CSV: vista previa, cancelar, NULL, multilínea y rollback", async ({ page }, info) => {
      await login(page);
      await table(page, "qa_imports");
      const d = await tools(page, "Importar");
      const file = d.locator('input[type="file"]');
      await file.setInputFiles({
        name: "datos.csv",
        mimeType: "text/csv",
        buffer: Buffer.from('id,nombre,notas\n1,"Muñoz, ""QA""","Uno\nDos"\n2,Segundo,\\N\n'),
      });
      await expect(d).toContainText("2 filas · 3 columnas");
      await d.getByRole("button", { name: "Importar CSV", exact: true }).click();
      await confirm(page, false);
      expect(db("SELECT COUNT(*) FROM qa_imports")).toBe("0");
      await d.getByRole("button", { name: "Importar CSV", exact: true }).click();
      await confirm(page);
      await expect(d.getByRole("status")).toContainText("2 filas importadas");
      expect(db("SELECT HEX(nombre),HEX(notas),notas IS NULL FROM qa_imports ORDER BY id")).toBe(
        `${Buffer.from('Muñoz, "QA"').toString("hex").toUpperCase()}\t${Buffer.from("Uno\nDos").toString("hex").toUpperCase()}\t0\n536567756E646F\tNULL\t1`,
      );
      await file.setInputFiles({
        name: "duplicado.csv",
        mimeType: "text/csv",
        buffer: Buffer.from("id,nombre\n3,No guardar\n1,Duplicado\n"),
      });
      await d.getByRole("button", { name: "Importar CSV", exact: true }).click();
      await confirm(page);
      await expect(d.getByRole("alert")).toContainText(/Duplicate|duplicate/i);
      expect(db("SELECT COUNT(*) FROM qa_imports")).toBe("2");
      await page.screenshot({ path: info.outputPath("csv-rollback.png"), fullPage: true });
      await file.setInputFiles({ name: "invalido.csv", mimeType: "text/csv", buffer: Buffer.from('id,nombre\n1,"sin cierre') });
      await expect(d.getByRole("alert")).toContainText("comillas");
      await expect(d.getByRole("button", { name: "Importar CSV", exact: true })).toBeDisabled();
      await file.setInputFiles({ name: "bad-encoding.csv", mimeType: "text/csv", buffer: Buffer.from([0xff, 0xfe, 0x61]) });
      await expect(d.getByRole("alert")).toContainText("UTF-8");
    });
    test("detalle reconstruido, navegación FK compuesta, NULL, diagrama y alineación", async ({ page }, info) => {
      await login(page);
      await table(page, "qa_children");
      const sidebar = page.getByRole("button", { name: "⚡ SQL", exact: true });
      const tabs = page.getByRole("tab", { name: "Contenido", exact: true });
      const a = await sidebar.boundingBox(),
        b = await tabs.boundingBox();
      expect(Math.abs(a!.y - b!.y)).toBeLessThanOrEqual(2);
      expect(Math.abs(a!.height - b!.height)).toBeLessThanOrEqual(2);
      const searchBox = await page.getByRole("searchbox", { name: "Filtrar tablas" }).boundingBox();
      const filterBox = await page.getByRole("button", { name: "Todas (*) ▾", exact: true }).boundingBox();
      expect(Math.abs(searchBox!.y - filterBox!.y)).toBeLessThanOrEqual(2);
      await page.getByRole("button", { name: "Ver detalle del registro 1", exact: true }).click();
      const detail = page.getByRole("dialog").filter({ hasText: "DETALLE DEL REGISTRO" });
      await expect(detail.getByRole("navigation", { name: "Registros relacionados" })).toBeVisible();
      await expect(detail.getByRole("button", { name: /↗ qa_parents/ })).toBeEnabled();
      await page.screenshot({ path: info.outputPath("detalle-registro.png"), fullPage: true });
      await detail.getByRole("button", { name: /↗ qa_parents/ }).click();
      await expect(page.locator(".cm-content:visible")).toContainText("`a` =");
      await expect(page.locator(".cm-content:visible")).toContainText(" AND `b` =");
      await page.getByRole("button", { name: "▶ Ejecutar todo", exact: true }).click();
      await expect(page.getByRole("cell", { name: /^Padre compuesto/ })).toBeVisible();
      await page.getByRole("tab", { name: "Contenido", exact: true }).click();
      await page.getByRole("button", { name: "Ver detalle del registro 2", exact: true }).click();
      await expect(detail.getByRole("button", { name: /↗ qa_parents/ })).toBeDisabled();
      await detail.getByRole("button", { name: "Cerrar detalle" }).click();
      const d = await tools(page, "Diagrama ER");
      await expect(d.locator(".er-canvas")).toContainText("qa_parents");
      await expect(d.locator("svg path[marker-end]")).not.toHaveCount(0);
      await page.screenshot({ path: info.outputPath("diagrama-er.png"), fullPage: true });
    });
    test("diseñador visual genera y aplica una columna con confirmación", async ({ page }, info) => {
      await login(page);
      await table(page, "qa_imports");
      await page.getByRole("tab", { name: "Modificar", exact: true }).click();
      const designer = page.locator(".structure-designer:visible");
      await designer.getByLabel("Nombre de columna", { exact: true }).fill("revision_visual");
      await designer.getByRole("combobox", { name: "Tipo", exact: true }).selectOption("INT");
      await designer.getByRole("button", { name: "Preparar cambio en SQL" }).click();
      await expect(page.getByLabel("Sentencia ALTER TABLE")).toHaveValue(/ADD COLUMN `revision_visual` INT NULL/);
      await page.getByRole("button", { name: /Ejecutar sentencia ALTER/ }).click();
      await confirm(page);
      await expect(page.locator(".alter-columns-ref:visible")).toContainText("revision_visual");
      await designer.getByRole("combobox", { name: "Operación", exact: true }).selectOption("modify");
      await designer.getByRole("combobox", { name: "Columna", exact: true }).selectOption("revision_visual");
      await designer.getByRole("combobox", { name: "Tipo", exact: true }).selectOption("VARCHAR(100)");
      await designer.getByRole("button", { name: "Preparar cambio en SQL" }).click();
      await expect(page.getByLabel("Sentencia ALTER TABLE")).toHaveValue(/MODIFY COLUMN `revision_visual` VARCHAR\(100\) DEFAULT NULL/);
      await page.getByRole("button", { name: /Ejecutar sentencia ALTER/ }).click();
      await confirm(page);
      await expect(page.locator(".alter-columns-ref:visible").getByRole("row").filter({ hasText: "revision_visual" })).toContainText(
        "varchar(100)",
      );
      expect(
        db(
          "SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='dbyson_e2e' AND TABLE_NAME='qa_imports' AND COLUMN_NAME='revision_visual'",
        ),
      ).toBe("1");
      await page.screenshot({ path: info.outputPath("estructura-visual.png"), fullPage: true });
    });
    test("importar SQL abre borrador y ejecuta las sentencias revisadas", async ({ page }) => {
      await login(page);
      await table(page, "qa_imports");
      const d = await tools(page, "Importar");
      await d.getByRole("combobox", { name: "Formato", exact: true }).selectOption("sql");
      await d.locator('input[type="file"]').setInputFiles({
        name: "lecturas.sql",
        mimeType: "text/plain",
        buffer: Buffer.from("SELECT 'Importada A' AS valor; SELECT 'Importada B' AS valor;"),
      });
      await d.getByRole("button", { name: "Revisar script en editor SQL" }).click();
      await expect(page.locator(".cm-content:visible")).toContainText("Importada A");
      await page.getByRole("button", { name: "▶ Ejecutar todo", exact: true }).click();
      await expect(page.getByRole("tablist", { name: "Resultados de las consultas" }).getByRole("tab")).toHaveCount(2);
      await page.getByRole("tablist", { name: "Resultados de las consultas" }).getByRole("tab").nth(1).click();
      await expect(page.getByRole("cell", { name: /^Importada B/ })).toBeVisible();
    });
    test("panel de procesos envía KILL QUERY real con confirmación", async ({ page, browser }) => {
      await login(page);
      await table(page, "items");
      const other = await browser.newContext();
      try {
        const second = await other.newPage();
        await login(second);
        const me = await csrf(second.request);
        const connections = await (await second.request.get("/api/connections")).json();
        const id = connections.find((c: { name: string }) => c.name === "MySQL E2E").id;
        const pending = second.request.post(`/api/connections/${id}/query`, {
          headers: { "X-CSRF-Token": me },
          data: { query: "SELECT SLEEP(20)", database: "dbyson_e2e" },
        });
        await expect
          .poll(() => db("SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE USER='e2e' AND INFO LIKE '%SLEEP(20)%'"))
          .toBe("1");
        const d = await tools(page, "Procesos");
        const row = d.getByRole("row").filter({ hasText: "SELECT SLEEP(20)" });
        await expect(row).toBeVisible();
        await row.getByRole("button", { name: "Detener consulta" }).click();
        await confirm(page);
        await pending;
        await expect
          .poll(() => db("SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE USER='e2e' AND INFO LIKE '%SLEEP(20)%'"))
          .toBe("0");
      } finally {
        await other.close();
      }
    });
    test("cuentas MySQL: crear, conceder, inspeccionar, revocar y eliminar sin filtrar contraseña", async ({ page }) => {
      await login(page);
      const token = await csrf(page.request);
      const created = await page.request.post("/api/connections", {
        headers: { "X-CSRF-Token": token },
        data: {
          name: "Admin MySQL temporal",
          host: "database",
          username: "root",
          password: "e2e-root-only",
          database: "dbyson_e2e",
          tlsMode: "disabled",
        },
      });
      expect(created.status()).toBe(201);
      const conn = await created.json();
      await page.reload();
      await page.getByRole("button", { name: /^Admin MySQL temporal/ }).click();
      const d = await tools(page, "Usuarios MySQL");
      await d.getByLabel("Usuario MySQL", { exact: true }).fill("qa_account");
      await d.getByLabel("Host permitido").fill("%");
      const secret = "qa-secret-DoNotLog-2026!";
      await d.getByLabel("Contraseña para nueva cuenta").fill(secret);
      await d.getByRole("button", { name: "Crear cuenta", exact: true }).click();
      await confirm(page);
      await expect(d.getByRole("status")).toContainText("Cambio aplicado");
      expect(db("SELECT COUNT(*) FROM mysql.user WHERE User='qa_account'")).toBe("1");
      await d.getByRole("button", { name: "Conceder permisos" }).click();
      await confirm(page);
      await expect(d.getByRole("status")).toContainText("Cambio aplicado");
      await d.getByRole("button", { name: "Ver permisos" }).click();
      await expect(d.locator("pre")).toContainText("SELECT");
      await d.getByRole("button", { name: "Revocar permisos" }).click();
      await confirm(page);
      await expect(d.getByRole("status")).toContainText("Cambio aplicado");
      await d.getByRole("button", { name: "Eliminar cuenta", exact: true }).click();
      await confirm(page);
      await expect(d.getByRole("status")).toContainText("Cambio aplicado");
      expect(db("SELECT COUNT(*) FROM mysql.user WHERE User='qa_account'")).toBe("0");
      const audit = await (await page.request.get("/api/audit")).text();
      expect(audit).not.toContain(secret);
      await page.request.delete(`/api/connections/${conn.id}`, { headers: { "X-CSRF-Token": token } });
    });
    test("detalle responsive: teclado y temas a 1366, 390 y 320 píxeles", async ({ page }, info) => {
      await login(page);
      await table(page, "qa_exports");
      for (const theme of ["Claro", "Oscuro"]) {
        await page.setViewportSize({ width: 1366, height: 768 });
        await page.getByRole("button", { name: "Preferencias del espacio", exact: true }).click();
        const prefs = page.getByRole("dialog", { name: "Preferencias del espacio" });
        await prefs.getByRole("button", { name: theme, exact: true }).click();
        await prefs.getByRole("button", { name: "Cerrar", exact: true }).click();
        await page.getByRole("button", { name: "Ver detalle del registro 1", exact: true }).click();
        const detail = page.locator(".record-inspector:visible");
        for (const width of [1366, 390, 320]) {
          await page.setViewportSize({ width, height: width < 500 ? 844 : 768 });
          await expect(detail).toBeVisible();
          const geometry = await detail.evaluate((el) => ({
            width: el.getBoundingClientRect().width,
            scroll: el.scrollWidth,
            client: el.clientWidth,
          }));
          expect(geometry.width).toBeLessThanOrEqual(width - 15);
          expect(geometry.scroll).toBeLessThanOrEqual(geometry.client + 1);
          await page.screenshot({ path: info.outputPath(`detalle-${theme}-${width}.png`), fullPage: true });
        }
        await detail.getByRole("button", { name: "Cerrar detalle" }).focus();
        await page.keyboard.press("Shift+Tab");
        await expect.poll(() => detail.evaluate((el) => el.contains(document.activeElement))).toBe(true);
        await page.keyboard.press("Escape");
        await expect(detail).toBeHidden();
      }
    });
    test("passkey real en autenticador virtual: registro, acceso, replay y eliminación", async ({ page, context }, info) => {
      await login(page, "http://localhost:18080/");
      const cdp = await context.newCDPSession(page);
      await cdp.send("WebAuthn.enable");
      const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
        options: {
          protocol: "ctap2",
          transport: "internal",
          hasResidentKey: true,
          hasUserVerification: true,
          isUserVerified: true,
          automaticPresenceSimulation: true,
        },
      });
      try {
        await page.getByRole("button", { name: "Seguridad", exact: true }).click();
        const panel = page.locator(".passkeys-panel");
        await panel.getByLabel("Nombre de la passkey").fill("Llave E2E");
        await panel.getByLabel("Contraseña actual para gestionar passkeys").fill(password);
        await panel.getByRole("button", { name: "Registrar passkey" }).click();
        await expect(panel.getByRole("listitem")).toContainText("Llave E2E");
        await page.screenshot({ path: info.outputPath("passkeys.png"), fullPage: true });
        await page.getByRole("dialog", { name: "Seguridad de la cuenta" }).getByRole("button", { name: "Cerrar", exact: true }).click();
        await page.getByRole("button", { name: "Salir", exact: true }).click();
        await expect(page.getByRole("button", { name: "Entrar con passkey" })).toBeVisible();
        // Alter a real assertion's origin; the server must reject it and consume its challenge.
        await page.route("**/api/auth/passkeys/login/finish", async (route) => {
          const body = route.request().postDataJSON();
          const clientData = JSON.parse(Buffer.from(body.response.clientDataJSON, "base64url").toString());
          clientData.origin = "https://attacker.invalid";
          body.response.clientDataJSON = Buffer.from(JSON.stringify(clientData)).toString("base64url");
          await route.continue({ postData: JSON.stringify(body) });
        });
        await page.getByRole("button", { name: "Entrar con passkey" }).click();
        await expect(page.getByRole("alert")).toContainText("passkey authentication failed");
        expect((await page.request.get("http://localhost:18080/api/auth/me")).status()).toBe(401);
        await page.unroute("**/api/auth/passkeys/login/finish");
        let assertion: unknown;
        page.on("request", (r) => {
          if (r.url().endsWith("/passkeys/login/finish")) assertion = r.postDataJSON();
        });
        await page.getByRole("button", { name: "Entrar con passkey" }).click();
        await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
        expect((await (await page.request.get("http://localhost:18080/api/auth/me")).json()).mfa).toBe(true);
        expect((await page.request.post("http://localhost:18080/api/auth/passkeys/login/finish", { data: assertion })).status()).toBe(400);
        await page.getByRole("button", { name: "Seguridad", exact: true }).click();
        await panel.getByLabel("Contraseña actual para gestionar passkeys").fill(password);
        await panel.getByRole("button", { name: "Eliminar passkey Llave E2E" }).click();
        await expect(panel.getByRole("listitem")).toHaveCount(0);
      } finally {
        await cdp.send("WebAuthn.removeVirtualAuthenticator", { authenticatorId });
        await cdp.detach();
      }
    });
  });
}
