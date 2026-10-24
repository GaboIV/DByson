import { test, expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { acceptanceTests } from "./acceptance";
import { connectionUXTests } from "./connections";
import { secureConnectivityTests } from "./security";

test("setup → conexión → tabla → editar → insertar → modificar → SQL → exportar", async ({ page }, testInfo) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Bienvenido a DByson" })).toBeVisible();
  await expect.poll(() => page.locator(".brand-logo:visible").evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);
  await page.screenshot({ path: testInfo.outputPath("logo-login.png"), fullPage: true });
  await page.getByLabel("Usuario", { exact: true }).fill("e2e-admin");
  await page.getByLabel(/^Contraseña/).fill("e2e-Administrator-2026!");
  await page.getByRole("button", { name: "Crear administrador" }).click();

  await page.getByRole("button", { name: /Nueva conexión/ }).click();
  await page.getByLabel("Nombre", { exact: true }).fill("MySQL E2E");
  await page.getByLabel("Host MySQL", { exact: true }).fill("database");
  await page.getByLabel("Usuario MySQL", { exact: true }).fill("e2e");
  await page.getByLabel("Contraseña MySQL", { exact: true }).fill("e2e-local-only");
  await page.getByLabel("Base de datos inicial").fill("dbyson_e2e");
  await page.getByRole("button", { name: "Continuar →" }).click();
  await page.getByRole("button", { name: "Continuar →" }).click();
  await page.getByLabel("Seguridad TLS de MySQL").selectOption("disabled");
  await page.getByRole("button", { name: "Continuar →" }).click();
  await page.getByRole("button", { name: "Probar conexión" }).click();
  await expect(page.getByText("Conexión correcta", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Guardar conexión" }).click();
  await page.getByRole("button", { name: /^MySQL E2E/ }).click();
  await page.locator(".table-item").filter({ hasText: "items" }).click();
  await expect(page.getByRole("cell", { name: /^Original/ })).toBeVisible();

  await page.getByRole("button", { name: "Editar registro 1", exact: true }).click();
  const edit = page.getByRole("dialog", { name: "Editar registro", exact: true });
  await edit.getByLabel("name", { exact: true }).fill("Editado E2E");
  await edit.getByRole("button", { name: "Guardar cambios" }).click();
  await expect(edit).toBeHidden();
  await expect(page.getByRole("cell", { name: /^Editado E2E/ })).toBeVisible();

  await page.getByRole("tab", { name: "Nuevo registro", exact: true }).click();
  await page.getByLabel("name", { exact: true }).fill("Insertado E2E");
  await page.getByRole("button", { name: "Insertar registro", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "Registro guardado correctamente." })).toBeVisible();

  await page.getByRole("tab", { name: "Modificar", exact: true }).click();
  await page.getByLabel("Sentencia ALTER TABLE").fill("ALTER TABLE items ADD COLUMN note VARCHAR(100) NULL;");
  await page.getByRole("button", { name: /Ejecutar sentencia ALTER/ }).click();
  const confirmation = page.getByRole("dialog").filter({ hasText: "Confirmar y ejecutar" });
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole("button", { name: "Cancelar", exact: true }).click();
  await expect(page.locator(".alter-columns-ref")).not.toContainText("note");
  await page.getByRole("button", { name: /Ejecutar sentencia ALTER/ }).click();
  const target = (await confirmation.locator("label").innerText()).match(/«(.+)»/)![1];
  await confirmation.getByRole("textbox").fill(target);
  await confirmation.getByRole("button", { name: "Confirmar y ejecutar" }).click();
  await expect(page.locator(".alter-columns-ref")).toContainText("note");

  await page.getByRole("button", { name: "⚡ SQL", exact: true }).click();
  const editor = page.locator(".cm-content:visible");
  await editor.fill("SELECT name FROM items ORDER BY id;");
  await page.getByRole("button", { name: "▶ Ejecutar todo", exact: true }).click();
  await expect(page.getByRole("cell", { name: /^Editado E2E/ })).toBeVisible();
  await expect(page.getByRole("cell", { name: /^Insertado E2E/ })).toBeVisible();

  await page.getByRole("button", { name: "📦 Exportar", exact: true }).click();
  await page.getByLabel("Tabla a exportar").selectOption("items");
  for (const format of ["csv", "json", "sql"]) {
    await page.getByLabel("Formato de salida").selectOption(format);
    const pending = page.waitForEvent("download");
    await page.getByRole("link", { name: /Descargar archivo/ }).click();
    const download = await pending;
    expect(await download.failure()).toBeNull();
    const content = await readFile((await download.path())!, "utf8");
    for (const value of ["Editado E2E", "Insertado E2E"]) {
      // SQL dumps preserve MySQL byte strings as hexadecimal literals.
      expect(content).toContain(format === "sql" ? Buffer.from(value).toString("hex") : value);
    }
    if (format === "json") expect(JSON.parse(content)).toHaveLength(2);
    if (format === "sql") expect(content).toMatch(/CREATE TABLE/i);
  }
});

acceptanceTests();
connectionUXTests();
secureConnectivityTests();
