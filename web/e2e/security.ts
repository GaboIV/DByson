import { test, expect, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";

async function login(page: Page) {
  await page.goto("/");
  await page.getByLabel("Usuario", { exact: true }).fill("e2e-admin");
  await page.getByLabel("Contraseña", { exact: true }).fill("e2e-Administrator-2026!");
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
}

function sleepingQueries() {
  return Number(
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
        "-uroot",
        "-pe2e-root-only",
        "--batch",
        "--skip-column-names",
        "-e",
        "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE USER = 'e2e' AND INFO LIKE '%SLEEP(20)%'",
      ],
      { encoding: "utf8" },
    ).trim(),
  );
}

export function secureConnectivityTests() {
  test.describe("Fase 2: conectividad segura", () => {
    test("KILL QUERY real, aislamiento entre sesiones y detención del resto del script", async ({ page, browser }) => {
      await login(page);
      const other = await browser.newContext();
      try {
        const second = await other.newPage();
        await login(second);
        await page.getByRole("button", { name: /^MySQL E2E/ }).click();
        await page
          .locator(".table-item:visible .table-name")
          .filter({ hasText: /^items$/ })
          .click();
        await page.getByRole("button", { name: "⚡ SQL", exact: true }).click();
        await page.locator(".cm-content:visible").fill("SELECT SLEEP(20); SELECT 'NEVER_EXECUTE';");
        const sent: string[] = [];
        let cancelPath = "";
        page.on("request", (request) => {
          if (request.method() === "POST" && request.url().endsWith("/query")) {
            const body = request.postDataJSON();
            sent.push(body.query);
            cancelPath = request.url().replace(/\/query$/, `/queries/${body.requestId}/cancel`);
          }
        });
        await page.getByRole("button", { name: "▶ Ejecutar todo", exact: true }).click();
        await expect.poll(sleepingQueries).toBe(1);
        const session = await (await second.request.get("/api/auth/me")).json();
        const foreign = await second.request.post(cancelPath, { headers: { "X-CSRF-Token": session.csrf }, data: {} });
        expect(foreign.status()).toBe(404);
        expect(sleepingQueries()).toBe(1);
        const response = page.waitForResponse((res) => res.url() === cancelPath);
        await page.getByRole("button", { name: "Cancelar consulta", exact: true }).click();
        expect((await response).status()).toBe(200);
        await expect(page.getByRole("button", { name: "▶ Ejecutar todo", exact: true })).toBeEnabled();
        await expect.poll(sleepingQueries).toBe(0);
        expect(sent).toHaveLength(1);
        await page.getByRole("tablist", { name: "Resultados de las consultas" }).getByRole("tab").nth(1).click();
        await expect(page.getByText("Sentencia omitida: se detuvo el script.", { exact: true })).toBeVisible();
        await page.locator(".cm-content:visible").fill("SELECT 42 AS recuperada;");
        await page.getByRole("button", { name: "▶ Ejecutar todo", exact: true }).click();
        await expect(page.getByRole("cell", { name: /^42(?: |$)/ })).toBeVisible();
      } finally {
        await other.close();
      }
    });

    test("Panel de sesiones: revocar otros navegadores y conservar la sesión actual", async ({ page, browser }, info) => {
      await login(page);
      const context = await browser.newContext();
      try {
        const second = await context.newPage();
        await login(second);
        await second.getByRole("button", { name: /^MySQL E2E/ }).click();
        await second
          .locator(".table-item:visible .table-name")
          .filter({ hasText: /^items$/ })
          .click();
        await second.getByRole("button", { name: "⚡ SQL", exact: true }).click();
        await second.locator(".cm-content:visible").fill("SELECT SLEEP(20);");
        await second.getByRole("button", { name: "▶ Ejecutar todo", exact: true }).click();
        await expect.poll(sleepingQueries).toBe(1);
        await page.getByRole("button", { name: "Seguridad", exact: true }).click();
        const panel = page.getByRole("region", { name: "Sesiones activas", exact: true });
        await expect(panel.getByText("Esta sesión", { exact: true })).toBeVisible();
        await expect(panel.getByRole("button", { name: "Cerrar las demás sesiones" })).toBeEnabled();
        await panel.getByRole("button", { name: "Cerrar las demás sesiones" }).click();
        await page.getByRole("dialog", { name: "Confirmar acción" }).getByRole("button", { name: "Continuar", exact: true }).click();
        await expect(panel.locator(".session-item")).toHaveCount(1);
        await expect.poll(sleepingQueries).toBe(0);
        expect((await second.request.get("/api/auth/me")).status()).toBe(401);
        await expect(second.getByRole("button", { name: "▶ Ejecutar todo", exact: true })).toBeEnabled();
        await second.locator(".cm-content:visible").fill("SELECT 42;");
        await second.getByRole("button", { name: "▶ Ejecutar todo", exact: true }).click();
        await expect(second.getByRole("heading", { name: "Entra a DByson", exact: true })).toBeVisible();
        await expect(second.getByRole("alert")).toContainText("Tu sesión venció o fue revocada");
        expect((await page.request.get("/api/auth/me")).status()).toBe(200);
        await page.screenshot({ path: info.outputPath("sesiones-activas.png"), fullPage: true });
      } finally {
        await context.close();
      }
    });
  });
}
