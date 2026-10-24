import { test, expect, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { execFileSync } from "node:child_process";

async function table(page: Page, name: string) {
  await page.getByRole("button", { name: /^MySQL E2E/ }).click();
  await page
    .locator(".table-item:visible .table-name")
    .filter({ hasText: new RegExp(`^${name}$`) })
    .click();
  await expect(page.getByRole("tab", { name: "Contenido", exact: true })).toBeVisible();
}

async function sql(page: Page, query: string) {
  if (!(await page.locator(".cm-content:visible").count())) await page.getByRole("button", { name: "⚡ SQL", exact: true }).click();
  await page.locator(".cm-content:visible").fill(query);
  await page.getByRole("button", { name: "▶ Ejecutar todo", exact: true }).click();
}

async function confirmation(page: Page, accept: boolean) {
  const modal = page.getByRole("dialog").filter({ hasText: "Confirmar y ejecutar" });
  await expect(modal).toBeVisible();
  if (!accept) return modal.getByRole("button", { name: "Cancelar", exact: true }).click();
  const target = (await modal.locator("label").innerText()).match(/«(.+)»/)![1];
  await modal.getByRole("textbox").fill("destino incorrecto");
  await expect(modal.getByRole("button", { name: "Confirmar y ejecutar" })).toBeDisabled();
  await modal.getByRole("textbox").fill(target);
  await modal.getByRole("button", { name: "Confirmar y ejecutar" }).click();
}

async function download(page: Page, name: RegExp) {
  const pending = page.waitForEvent("download");
  await page.getByRole("link", { name }).click();
  const result = await pending;
  expect(await result.failure()).toBeNull();
  return readFile((await result.path())!, "utf8");
}

// Independent oracle against the disposable database, not against UI state or cache.
function db(query: string) {
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
      query,
    ],
    { encoding: "utf8" },
  ).trim();
}

function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [],
    field = "",
    quoted = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (ch === '"') {
      if (quoted && text[i + 1] === '"') {
        field += '"';
        i++;
      } else quoted = !quoted;
    } else if (ch === "," && !quoted) {
      row.push(field);
      field = "";
    } else if (ch === "\n" && !quoted) {
      row.push(field.replace(/\r$/, ""));
      rows.push(row);
      row = [];
      field = "";
    } else field += ch;
  }
  if (field || row.length) {
    row.push(field);
    rows.push(row);
  }
  return rows;
}

export function acceptanceTests() {
  test.describe("Aceptación de v0.1.0", () => {
    test.beforeEach(async ({ page }) => {
      await page.goto("/");
      await page.getByLabel("Usuario", { exact: true }).fill("e2e-admin");
      await page.getByLabel(/^Contraseña/).fill("e2e-Administrator-2026!");
      await page.getByRole("button", { name: "Entrar", exact: true }).click();
      await expect(page.getByRole("tab", { name: "Conexiones", exact: true })).toBeVisible();
    });

    test("registros: valores especiales, NULL, duplicado, recuperación y cancelación", async ({ page }) => {
      await table(page, "qa_records");
      await page.getByRole("tab", { name: "Nuevo registro", exact: true }).click();
      await expect(page.getByLabel("id", { exact: true })).toBeDisabled();
      await page.getByLabel("codigo", { exact: true }).fill("QA-001");
      await page.getByLabel("nombre", { exact: true }).fill('Muñoz, "prueba"');
      await page.getByLabel("NULL en notas").uncheck();
      await page.getByLabel("notas", { exact: true }).fill("Línea uno\nLínea dos");
      await page.getByRole("button", { name: "Insertar registro", exact: true }).click();
      await expect(page.getByRole("status").filter({ hasText: "Registro guardado correctamente" })).toBeVisible();
      await page.getByLabel("codigo", { exact: true }).fill("QA-001");
      await page.getByLabel("nombre", { exact: true }).fill("Duplicado");
      await page.getByRole("button", { name: "Insertar registro", exact: true }).click();
      await expect(page.locator(".insert-record-view .alert-error:visible")).toContainText(/Duplicate|duplicad/i);
      await expect(page.getByRole("status").filter({ hasText: "Registro guardado correctamente" })).toHaveCount(0);
      await expect(page.getByLabel("nombre", { exact: true })).toHaveValue("Duplicado");
      expect(db("SELECT COUNT(*) FROM qa_records")).toBe("1");
      await page.getByLabel("codigo", { exact: true }).fill("QA-002");
      await page.getByLabel("nombre", { exact: true }).fill("Segundo registro");
      await page.getByRole("button", { name: "Insertar registro", exact: true }).click();
      await expect(page.getByRole("status").filter({ hasText: "Registro guardado correctamente" })).toBeVisible();
      expect(db("SELECT notas IS NULL FROM qa_records WHERE codigo='QA-002'")).toBe("1");
      expect(db("SELECT HEX(notas) FROM qa_records WHERE codigo='QA-001'")).toBe(
        Buffer.from("Línea uno\nLínea dos").toString("hex").toUpperCase(),
      );
      await page.getByRole("tab", { name: "Contenido", exact: true }).click();
      await page.getByRole("button", { name: "🔄", exact: true }).click();
      await page.getByRole("button", { name: "Editar registro 2", exact: true }).click();
      const edit = page.getByRole("dialog", { name: "Editar registro", exact: true });
      await edit.getByLabel("nombre", { exact: true }).fill("Segundo registro editado");
      await edit.getByRole("button", { name: "Guardar cambios" }).click();
      await expect(edit).toBeHidden();
      await page.getByRole("button", { name: "🔄", exact: true }).click();
      await page.getByRole("button", { name: "Editar registro 2", exact: true }).click();
      await edit.getByLabel("nombre", { exact: true }).fill("NO GUARDAR");
      await edit.getByRole("button", { name: "Cancelar", exact: true }).click();
      await page.getByRole("dialog", { name: "Confirmar acción" }).getByRole("button", { name: "Cancelar", exact: true }).click();
      await expect(edit.getByLabel("nombre", { exact: true })).toHaveValue("NO GUARDAR");
      await edit.getByRole("button", { name: "Cancelar", exact: true }).click();
      await page.getByRole("dialog", { name: "Confirmar acción" }).getByRole("button", { name: "Continuar", exact: true }).click();
      await expect(edit).toBeHidden();
      expect(db("SELECT nombre FROM qa_records WHERE codigo='QA-002'")).toBe("Segundo registro editado");
      expect(db("SELECT COUNT(*) FROM qa_records")).toBe("2");
    });

    test("estructura: cancelar, destino incorrecto, confirmar, error y recuperación", async ({ page }) => {
      await table(page, "qa_structure");
      await page.getByRole("tab", { name: "Modificar", exact: true }).click();
      const query = "ALTER TABLE qa_structure ADD COLUMN revision_manual VARCHAR(50) NULL;";
      await page.getByLabel("Sentencia ALTER TABLE").fill(query);
      await page.getByRole("button", { name: /Ejecutar sentencia ALTER/ }).click();
      await confirmation(page, false);
      expect(
        db(
          "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='dbyson_e2e' AND table_name='qa_structure' AND column_name='revision_manual'",
        ),
      ).toBe("0");
      await page.getByRole("button", { name: /Ejecutar sentencia ALTER/ }).click();
      await confirmation(page, true);
      await expect(page.locator(".alter-columns-ref:visible")).toContainText("revision_manual");
      await page.getByRole("button", { name: /Ejecutar sentencia ALTER/ }).click();
      await confirmation(page, true);
      await expect(page.locator(".alter-table-view .alert-error:visible")).toContainText(/Duplicate|duplicad/i);
      await page.getByLabel("Sentencia ALTER TABLE").fill("ALTER TABLE qa_structure ADD COLUMN recuperada INT NULL;");
      await page.getByRole("button", { name: /Ejecutar sentencia ALTER/ }).click();
      await confirmation(page, true);
      await expect(page.locator(".alter-columns-ref:visible")).toContainText("recuperada");
      await page.getByRole("tab", { name: "Estructura", exact: true }).click();
      await expect(page.locator(".structure-view:visible")).toContainText("revision_manual");
      await page.getByRole("tab", { name: "Nuevo registro", exact: true }).click();
      await expect(page.getByLabel("revision_manual", { exact: true })).toBeVisible();
    });

    test("borradores: cambiar vistas, cerrar, cancelar, descartar y recargar", async ({ page }) => {
      await table(page, "qa_drafts");
      await page.getByRole("tab", { name: "Nuevo registro", exact: true }).click();
      await page.getByLabel("nombre", { exact: true }).fill("Borrador sin insertar");
      await page.getByRole("tab", { name: "Contenido", exact: true }).click();
      await page.getByRole("tab", { name: "Nuevo registro", exact: true }).click();
      await expect(page.getByLabel("nombre", { exact: true })).toHaveValue("Borrador sin insertar");
      await page.getByRole("tab", { name: "Modificar", exact: true }).click();
      await page.getByLabel("Sentencia ALTER TABLE").fill("ALTER TABLE qa_drafts ADD COLUMN borrador INT;");
      await page.getByRole("tab", { name: "Contenido", exact: true }).click();
      await page.getByRole("tab", { name: "Modificar", exact: true }).click();
      await expect(page.getByLabel("Sentencia ALTER TABLE")).toHaveValue("ALTER TABLE qa_drafts ADD COLUMN borrador INT;");
      await page.getByRole("button", { name: "⚡ SQL", exact: true }).click();
      await page.locator(".cm-content:visible").fill("SELECT 'borrador';");
      await page.getByRole("tab", { name: "Contenido", exact: true }).click();
      await page.getByRole("tab", { name: "Consulta 1", exact: true }).click();
      await expect(page.locator(".cm-content:visible")).toHaveText("SELECT 'borrador';");
      await page.getByRole("button", { name: "Cerrar pestaña Consulta 1", exact: true }).click();
      const discard = page.getByRole("dialog", { name: "Confirmar acción" });
      await expect(discard).toContainText("cambios sin guardar");
      await discard.getByRole("button", { name: "Cancelar", exact: true }).click();
      await expect(page.locator(".cm-content:visible")).toHaveText("SELECT 'borrador';");
      const pending = page.waitForEvent("dialog");
      const reload = page.reload({ timeout: 5000 }).catch(() => {});
      const dialog = await pending;
      expect(dialog.type()).toBe("beforeunload");
      await dialog.dismiss();
      await reload;
      await expect(page.locator(".cm-content:visible")).toHaveText("SELECT 'borrador';");
      await page.getByRole("button", { name: "Cerrar pestaña Consulta 1", exact: true }).click();
      await discard.getByRole("button", { name: "Continuar", exact: true }).click();
      await expect(page.getByRole("tab", { name: "Consulta 1", exact: true })).toHaveCount(0);
      await page.getByRole("button", { name: "Cerrar pestaña qa_drafts", exact: true }).click();
      await discard.getByRole("button", { name: "Cancelar", exact: true }).click();
      await expect(page.getByRole("tab", { name: /MySQL E2E · qa_drafts/ })).toBeVisible();
      await page.getByRole("button", { name: "Cerrar pestaña qa_drafts", exact: true }).click();
      await discard.getByRole("button", { name: "Continuar", exact: true }).click();
      expect(db("SELECT COUNT(*) FROM qa_drafts")).toBe("1");
      expect(
        db(
          "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='dbyson_e2e' AND table_name='qa_drafts' AND column_name='borrador'",
        ),
      ).toBe("0");
    });

    test("SQL: atajo, selección, varias sentencias, error, autocompletado y archivo", async ({ page }, info) => {
      await table(page, "qa_exports");
      await page.getByRole("button", { name: "⚡ SQL", exact: true }).click();
      const editor = page.locator(".cm-content:visible");
      await editor.fill("SELECT 'primera' AS prueba;\nSELECT 'segunda' AS prueba;");
      await editor.press("Control+Home");
      await editor.press("Control+Shift+End");
      await editor.press("ArrowLeft");
      await editor.press("Shift+End");
      await page.getByRole("button", { name: "▶ Ejecutar selección", exact: true }).click();
      await expect(page.getByRole("tablist", { name: "Resultados de las consultas" }).getByRole("tab")).toHaveCount(1);
      await expect(page.getByRole("cell", { name: /^primera(?: |$)/ })).toBeVisible();
      await editor.press("Control+End");
      await editor.press("Control+Enter");
      const results = page.getByRole("tablist", { name: "Resultados de las consultas" }).getByRole("tab");
      await expect(results).toHaveCount(2);
      await results.nth(1).click();
      await expect(page.getByRole("cell", { name: /^segunda(?: |$)/ })).toBeVisible();
      await sql(page, "SELECT columna_inexistente FROM qa_exports;");
      await expect(page.locator(".sql-result-pane .alert-error:visible")).toContainText(/Unknown|inexistente/i);
      await sql(page, "SELECT COUNT(*) AS total FROM qa_exports;");
      await expect(page.getByRole("cell", { name: /^2(?: |$)/ })).toBeVisible();
      await editor.fill("SELECT * FROM qa_exp");
      await editor.press("Control+Space");
      await expect(page.getByRole("option").filter({ hasText: "qa_exports" })).toBeVisible();
      await editor.press("Escape");
      await editor.fill("SELECT q. FROM qa_exports q;");
      await editor.press("Control+Home");
      for (let i = 0; i < 9; i++) await editor.press("ArrowRight");
      await editor.press("Control+Space");
      await expect(page.getByRole("option").filter({ hasText: "codigo" })).toBeVisible();
      await editor.press("Escape");
      await editor.fill("SELECT 'archivo recuperado' AS prueba;");
      const pending = page.waitForEvent("download");
      await page.getByRole("button", { name: "⬇ Descargar .sql", exact: true }).click();
      const saved = await pending;
      const file = info.outputPath("consulta.sql");
      await saved.saveAs(file);
      expect(await readFile(file, "utf8")).toContain("archivo recuperado");
      await editor.fill("SELECT 'reemplazar';");
      const chooser = page.waitForEvent("filechooser");
      await page.getByRole("button", { name: "📂 Abrir .sql", exact: true }).click();
      await page.getByRole("dialog", { name: "Confirmar acción" }).getByRole("button", { name: "Continuar", exact: true }).click();
      await (await chooser).setFiles(file);
      await expect(editor).toHaveText("SELECT 'archivo recuperado' AS prueba;");
      await expect.poll(() => editor.locator("span").count()).toBeGreaterThan(0);
      await page.screenshot({ path: info.outputPath("editor-sql.png"), fullPage: true });
    });

    test("exportaciones: CSV multilínea, JSON NULL y las cuatro opciones de SQL", async ({ page }, info) => {
      await table(page, "qa_exports");
      await page.getByRole("button", { name: "📦 Exportar", exact: true }).click();
      await page.getByLabel("Tabla a exportar").selectOption("qa_exports");
      await page.getByLabel("Formato de salida").selectOption("csv");
      const csv = parseCsv(await download(page, /Descargar archivo/));
      expect(csv).toEqual([
        ["id", "codigo", "nombre", "notas"],
        ["1", "QA-001", 'Muñoz, "prueba"', "Línea uno\nLínea dos"],
        ["2", "QA-002", "Segundo registro", "NULL"],
      ]);
      await page.getByLabel("Tabla a exportar").selectOption("");
      await expect(page.locator(".export-view:visible a[download]")).not.toHaveAttribute("href");
      await page.getByLabel("Formato de salida").selectOption("json");
      await expect(page.locator(".export-view:visible a[download]")).not.toHaveAttribute("href");
      await page.getByLabel("Tabla a exportar").selectOption("qa_exports");
      const json = JSON.parse(await download(page, /Descargar archivo/));
      expect(json).toHaveLength(2);
      expect(json[0]).toMatchObject({ codigo: "QA-001", nombre: 'Muñoz, "prueba"', notas: "Línea uno\nLínea dos" });
      expect(json[1].notas).toBeNull();
      await page.getByLabel("Formato de salida").selectOption("sql");
      const both = await download(page, /Descargar archivo/);
      expect(both).toMatch(/CREATE TABLE/);
      expect(both).toMatch(/INSERT INTO/);
      await page.getByLabel("Incluir datos (INSERT INTO)").uncheck();
      const structure = await download(page, /Descargar archivo/);
      expect(structure).toMatch(/CREATE TABLE/);
      expect(structure).not.toMatch(/INSERT INTO/);
      await page.getByLabel("Incluir estructura (CREATE TABLE)").uncheck();
      await expect(page.locator(".export-view:visible a[download]")).not.toHaveAttribute("href");
      await page.getByLabel("Incluir datos (INSERT INTO)").check();
      const data = await download(page, /Descargar archivo/);
      expect(data).toMatch(/INSERT INTO/);
      expect(data).not.toMatch(/CREATE TABLE/);
      // Restore the downloaded dump in a separate disposable schema and compare bytes.
      execFileSync(
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
        ],
        { input: `CREATE DATABASE qa_restore; USE qa_restore;\n${both}` },
      );
      expect(db("SELECT HEX(nombre), HEX(notas), notas IS NULL FROM qa_exports ORDER BY id")).toBe(
        db("SELECT HEX(nombre), HEX(notas), notas IS NULL FROM qa_restore.qa_exports ORDER BY id"),
      );
      await page.screenshot({ path: info.outputPath("exportaciones.png"), fullPage: true });
    });

    test("Sólo ver global: rechazar escritura y permitir lectura y exportación", async ({ page }) => {
      await table(page, "qa_exports");
      await page.getByRole("button", { name: "Preferencias del espacio", exact: true }).click();
      await page.getByRole("checkbox", { name: /^Sólo ver/ }).check();
      await page.getByRole("dialog", { name: "Preferencias del espacio" }).getByRole("button", { name: "Cerrar", exact: true }).click();
      await expect(page.getByRole("tab", { name: "Modificar", exact: true })).toHaveCount(0);
      await expect(page.getByRole("tab", { name: "Nuevo registro", exact: true })).toHaveCount(0);
      await expect(page.getByRole("button", { name: "Editar registro 1", exact: true })).toHaveCount(0);
      await sql(page, "UPDATE qa_exports SET nombre='NO_DEBE_GUARDARSE' WHERE codigo='QA-001';");
      await expect(page.locator(".sql-result-pane .alert-error:visible")).toContainText(/lectura|read.only/i);
      expect(db("SELECT nombre FROM qa_exports WHERE codigo='QA-001'")).toBe('Muñoz, "prueba"');
      await sql(page, "SELECT COUNT(*) AS total FROM qa_exports;");
      await expect(page.getByRole("cell", { name: /^2(?: |$)/ })).toBeVisible();
      await page.getByRole("button", { name: "📦 Exportar", exact: true }).click();
      await page.getByLabel("Tabla a exportar").selectOption("qa_exports");
      expect(await download(page, /Descargar archivo/)).toContain("CREATE TABLE");
    });

    test("conexión solo lectura con preferencia global desactivada", async ({ page }) => {
      await page.getByRole("button", { name: /Nueva conexión/ }).click();
      await page.getByLabel("Nombre", { exact: true }).fill("MySQL QA solo lectura");
      await page.getByLabel("Host MySQL", { exact: true }).fill("database");
      await page.getByLabel("Usuario MySQL", { exact: true }).fill("e2e");
      await page.getByLabel("Contraseña MySQL", { exact: true }).fill("e2e-local-only");
      await page.getByLabel("Base de datos inicial").fill("dbyson_e2e");
      await page.getByRole("button", { name: "Continuar →" }).click();
      await page.getByRole("button", { name: "Continuar →" }).click();
      await page.getByLabel("Seguridad TLS de MySQL").selectOption("disabled");
      await page.getByLabel("Solo lectura", { exact: true }).check();
      await page.getByRole("button", { name: "Continuar →" }).click();
      await page.getByRole("button", { name: "Guardar conexión" }).click();
      await page.getByRole("button", { name: /^MySQL QA solo lectura/ }).click();
      await page
        .locator(".table-item:visible .table-name")
        .filter({ hasText: /^qa_exports$/ })
        .click();
      await page.getByRole("button", { name: "Preferencias del espacio", exact: true }).click();
      await expect(page.getByRole("checkbox", { name: /^Sólo ver/ })).not.toBeChecked();
      await page.getByRole("dialog", { name: "Preferencias del espacio" }).getByRole("button", { name: "Cerrar", exact: true }).click();
      await expect(page.getByRole("tab", { name: "Modificar", exact: true })).toHaveCount(0);
      await expect(page.getByRole("tab", { name: "Nuevo registro", exact: true })).toHaveCount(0);
      await sql(page, "UPDATE qa_exports SET nombre='NO_DEBE_GUARDARSE' WHERE codigo='QA-001';");
      await expect(page.locator(".sql-result-pane .alert-error:visible")).toContainText(/lectura|read.only/i);
      expect(db("SELECT nombre FROM qa_exports WHERE codigo='QA-001'")).toBe('Muñoz, "prueba"');
      await sql(page, "SELECT COUNT(*) AS total FROM qa_exports;");
      await expect(page.getByRole("cell", { name: /^2(?: |$)/ })).toBeVisible();
    });

    test("revisión visual: logo, favicon, claro y oscuro", async ({ page }, info) => {
      const icon = await page.locator('link[rel="icon"]').getAttribute("href");
      expect((await page.request.get(icon!)).ok()).toBeTruthy();
      await table(page, "qa_exports");
      for (const theme of ["Claro", "Oscuro"]) {
        await page.getByRole("button", { name: "Preferencias del espacio", exact: true }).click();
        await page.getByRole("button", { name: theme, exact: true }).click();
        await page.getByRole("dialog", { name: "Preferencias del espacio" }).getByRole("button", { name: "Cerrar", exact: true }).click();
        await expect
          .poll(() => page.locator(".brand-logo:visible").evaluate((img: HTMLImageElement) => img.naturalWidth))
          .toBeGreaterThan(0);
        await page.screenshot({ path: info.outputPath(`tabla-${theme}.png`), fullPage: true });
        await page.getByRole("tab", { name: "Nuevo registro", exact: true }).click();
        await page.screenshot({ path: info.outputPath(`formulario-${theme}.png`), fullPage: true });
        await page.getByRole("tab", { name: "Modificar", exact: true }).click();
        await page.getByLabel("Sentencia ALTER TABLE").fill("ALTER TABLE qa_exports ADD COLUMN visual_temporal INT;");
        await page.getByRole("button", { name: /Ejecutar sentencia ALTER/ }).click();
        await expect(page.getByRole("dialog").filter({ hasText: "Confirmar y ejecutar" })).toBeVisible();
        await page.screenshot({ path: info.outputPath(`confirmacion-${theme}.png`), fullPage: true });
        await confirmation(page, false);
        await sql(page, "SELECT columna_visual_inexistente FROM qa_exports;");
        await expect(page.locator(".sql-result-pane .alert-error:visible")).toContainText(/Unknown|inexistente/i);
        await page.screenshot({ path: info.outputPath(`error-${theme}.png`), fullPage: true });
        await page.getByRole("tab", { name: "Contenido", exact: true }).click();
      }
    });
  });
}
