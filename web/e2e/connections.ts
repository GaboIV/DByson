import { test, expect, type Page } from "@playwright/test";
import type { Connection, ConnectionInput } from "../src/api";

const fingerprint = "SHA256:Kb8nX1L7GkN9TztPq4k42vwJd3Z3Ek4UJiX6vdF8d1Q";
const jumpFingerprint = "SHA256:Am2cY8V3RtP5QwS6Ew9k42vwJd3Z3Ek4UJiX6vdF8d1Q";
const profiles: Connection[] = [
  {
    id: "qa-local",
    name: "Desarrollo local",
    env: "local",
    host: "host.docker.internal",
    username: "developer",
    database: "inventario",
    port: 3306,
    tlsMode: "disabled",
    readOnly: false,
    hasPassword: true,
    hasCaCert: false,
    hasClientCert: false,
  },
  {
    id: "qa-prod",
    name: "Producción",
    env: "prod",
    host: "127.0.0.1",
    username: "reader",
    database: "ventas",
    port: 3306,
    tlsMode: "verify_identity",
    readOnly: true,
    hasPassword: true,
    hasCaCert: true,
    hasClientCert: false,
    ssh: { enabled: true, host: "ssh.example.test", port: 22, user: "operator", auth: "password", fingerprint, hasPassword: true },
  },
  {
    id: "qa-stage",
    name: "Equipo · staging",
    env: "staging",
    host: "mysql.example.test",
    username: "tester",
    database: "",
    port: 3306,
    tlsMode: "required",
    readOnly: false,
    hasPassword: true,
    hasCaCert: false,
    hasClientCert: false,
  },
  {
    id: "qa-analytics",
    name: "Analítica",
    env: "dev",
    host: "analytics.example.test",
    username: "reports",
    database: "informes",
    port: 3306,
    tlsMode: "verify_identity",
    readOnly: true,
    hasPassword: true,
    hasCaCert: false,
    hasClientCert: false,
  },
];

async function login(page: Page) {
  await page.goto("/");
  await page.getByLabel("Usuario", { exact: true }).fill("e2e-admin");
  await page.getByLabel("Contraseña", { exact: true }).fill("e2e-Administrator-2026!");
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
}

async function newSSH(page: Page) {
  await page.getByRole("button", { name: /Nueva conexión/ }).click();
  await page.getByLabel("Nombre", { exact: true }).fill("SSH de prueba");
  await page.getByLabel("Host MySQL", { exact: true }).fill("127.0.0.1");
  await page.getByLabel("Usuario MySQL", { exact: true }).fill("tester");
  await page.getByRole("button", { name: "Continuar →" }).click();
  await page.getByLabel("Conectar a través de un túnel SSH").check();
  const hop = page.locator(".ssh-hop").last();
  await hop.getByLabel("Host SSH", { exact: true }).fill("ssh.example.test");
  await hop.getByLabel("Usuario SSH", { exact: true }).fill("operator");
  await hop.getByLabel(/^Autenticación/).selectOption("password");
  await hop.getByLabel("Contraseña SSH", { exact: true }).fill("disposable-only");
}

async function noHorizontalOverflow(page: Page) {
  const overflowing = await page
    .locator(".auth-page, .home-content, .connection-wizard, .wizard-body, .ssh-hop, .modal-content")
    .evaluateAll((elements) => elements.filter((el) => el.scrollWidth > el.clientWidth + 1).map((el) => el.className));
  expect(overflowing).toEqual([]);
  const collapsedControls = await page.locator('input[type="checkbox"]:visible, input[type="radio"]:visible').evaluateAll(
    (elements) =>
      elements.filter((el) => {
        const box = el.getBoundingClientRect();
        return box.width < 14 || box.height < 14;
      }).length,
  );
  expect(collapsedControls).toBe(0);
}

export function connectionUXTests() {
  test.describe("Conexiones: distribución e identidad SSH", () => {
    test.beforeEach(async ({ page }) => {
      // Only test identities/profiles are simulated. Authentication, assets and
      // the original nine acceptance cases continue using the disposable API.
      await page.route("**/api/connections", async (route) => {
        if (route.request().method() === "GET") return route.fulfill({ json: profiles });
        return route.fallback();
      });
    });

    test("SSH: cancelar, confiar y propagar la huella al probar y guardar", async ({ page }) => {
      await login(page);
      await newSSH(page);
      let lookups = 0;
      await page.route("**/api/ssh/fingerprint", async (route) => {
        lookups++;
        expect(route.request().postDataJSON()).toMatchObject({ host: "ssh.example.test", user: "operator", port: 22 });
        await route.fulfill({ json: { fingerprint, algorithm: "ssh-ed25519" } });
      });
      await page.getByRole("button", { name: "Continuar →" }).click();
      const dialog = page.getByRole("dialog", { name: "Verificar servidor SSH" });
      await expect(dialog.getByText(fingerprint, { exact: true })).toBeVisible();
      await dialog.getByRole("button", { name: "Cancelar", exact: true }).click();
      await expect(page.getByLabel("Host SSH", { exact: true })).toHaveValue("ssh.example.test");
      await expect(page.getByLabel("Huella de confianza")).toHaveValue("");
      await page.getByRole("button", { name: "Continuar →" }).click();
      await dialog.getByRole("button", { name: "Confiar y continuar" }).click();
      await expect(page.getByLabel("Seguridad TLS de MySQL")).toBeVisible();
      let tested: ConnectionInput | undefined;
      let saved: ConnectionInput | undefined;
      await page.route("**/api/connections/test", async (route) => {
        tested = route.request().postDataJSON();
        await route.fulfill({ json: { ok: true, result: { serverVersion: "MySQL QA", latencyMs: 2, tls: true } } });
      });
      await page.route("**/api/connections", async (route) => {
        if (route.request().method() !== "POST") return route.fallback();
        saved = route.request().postDataJSON();
        await route.fulfill({ json: { ...profiles[0], ...saved, id: "qa-created" } });
      });
      await page.getByRole("button", { name: "Continuar →" }).click();
      await page.getByRole("button", { name: "Probar conexión", exact: true }).click();
      await expect(page.getByText("Conexión correcta", { exact: true })).toBeVisible();
      expect(tested?.ssh?.fingerprint).toBe(fingerprint);
      await page.getByRole("button", { name: "Guardar conexión", exact: true }).click();
      await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
      expect(saved?.ssh?.fingerprint).toBe(fingerprint);
      expect(lookups).toBe(2);
    });

    test("Bastión: verificar en orden, recuperar un error y conservar cada identidad", async ({ page }) => {
      await login(page);
      await newSSH(page);
      await page.getByLabel("Pasar primero por un bastión (ProxyJump)").check();
      const bastion = page.locator(".ssh-hop").first();
      await bastion.getByLabel("Host SSH", { exact: true }).fill("bastion.example.test");
      await bastion.getByLabel("Usuario SSH", { exact: true }).fill("jump-user");
      await bastion.getByLabel(/^Autenticación/).selectOption("password");
      await bastion.getByLabel("Contraseña SSH", { exact: true }).fill("jump-disposable-only");
      const hosts: string[] = [];
      await page.route("**/api/ssh/fingerprint", async (route) => {
        const input = route.request().postDataJSON();
        hosts.push(input.host);
        if (input.host === "bastion.example.test") {
          expect(input.jump).toBeUndefined();
          return route.fulfill({ json: { fingerprint: jumpFingerprint, algorithm: "ssh-ed25519" } });
        }
        expect(input.jump).toMatchObject({ fingerprint: jumpFingerprint, host: "bastion.example.test", password: "jump-disposable-only" });
        if (hosts.length === 2) return route.fulfill({ status: 502, json: { error: "El servidor SSH no responde" } });
        return route.fulfill({ json: { fingerprint, algorithm: "ssh-ed25519" } });
      });
      await page.getByRole("button", { name: "Continuar →" }).click();
      await page.getByRole("dialog", { name: "Verificar bastión SSH" }).getByRole("button", { name: "Confiar y continuar" }).click();
      const server = page.getByRole("dialog", { name: "Verificar servidor SSH" });
      await expect(server.getByRole("alert")).toHaveText("El servidor SSH no responde");
      await expect(server.getByRole("button", { name: "Confiar y continuar" })).toHaveCount(0);
      await server.getByRole("button", { name: "Cancelar", exact: true }).click();
      await page.getByRole("button", { name: "Continuar →" }).click();
      await server.getByRole("button", { name: "Confiar y continuar" }).click();
      await expect(page.getByLabel("Seguridad TLS de MySQL")).toBeVisible();
      expect(hosts).toEqual(["bastion.example.test", "ssh.example.test", "ssh.example.test"]);
      await page.getByRole("button", { name: /Ruta y SSH/ }).click();
      await expect(bastion.locator(".trusted-fingerprint")).toHaveText(jumpFingerprint);
      await expect(page.locator(".ssh-hop").last().locator(".trusted-fingerprint")).toHaveText(fingerprint);
    });

    test("Editar: conservar huellas fijadas, detectar un cambio y bloquear saltos sin confirmar", async ({ page }) => {
      await login(page);
      await page.locator(".conn").filter({ hasText: "Producción" }).getByRole("button", { name: "Editar", exact: true }).click();
      await page.getByRole("button", { name: /Ruta y SSH/ }).click();
      await expect(page.locator(".trusted-fingerprint")).toHaveText(fingerprint);
      let calls = 0;
      await page.route("**/api/connections/qa-prod/ssh/fingerprint", async (route) => {
        calls++;
        await route.fulfill({ json: { fingerprint: jumpFingerprint, algorithm: "ssh-ed25519" } });
      });
      await page.getByRole("button", { name: "Revisar identidad" }).click();
      const dialog = page.getByRole("dialog", { name: "Verificar servidor SSH" });
      await expect(dialog.getByText(/La huella ha cambiado/)).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(page.locator(".trusted-fingerprint")).toHaveText(fingerprint);
      await page.getByLabel("Host SSH", { exact: true }).fill("otro.example.test");
      await expect(page.locator(".trusted-fingerprint")).toHaveCount(0);
      await page.getByRole("button", { name: /Comprobar/ }).click();
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Confiar y continuar" }).click();
      await expect(page.getByRole("heading", { name: "Todo listo para comprobar" })).toBeVisible();
      expect(calls).toBe(2);
    });

    test("Cancelar la consulta de identidad en curso permite corregir el destino", async ({ page }) => {
      await login(page);
      await newSSH(page);
      await page.route("**/api/ssh/fingerprint", async (route) => {
        await new Promise((resolve) => setTimeout(resolve, 700));
        await route.fulfill({ json: { fingerprint, algorithm: "ssh-ed25519" } }).catch(() => {});
      });
      await page.getByRole("button", { name: "Continuar →" }).click();
      const dialog = page.getByRole("dialog", { name: "Verificar servidor SSH" });
      await expect(dialog.getByRole("button", { name: "Confiar y continuar" })).toBeDisabled();
      await dialog.getByRole("button", { name: "Cancelar", exact: true }).click();
      await page.getByLabel("Host SSH", { exact: true }).fill("corregido.example.test");
      await page.getByRole("button", { name: "Continuar →" }).click();
      await expect(dialog.getByText("corregido.example.test:22", { exact: true })).toBeVisible();
      await dialog.getByRole("button", { name: "Confiar y continuar" }).click();
      await expect(page.getByLabel("Seguridad TLS de MySQL")).toBeVisible();
    });

    for (const viewport of [
      { width: 1920, height: 1080 },
      { width: 1366, height: 768 },
      { width: 768, height: 1024 },
      { width: 390, height: 844 },
      { width: 320, height: 568 },
    ]) {
      test(`Responsive ${viewport.width}: acceso, lista y cuatro pasos en ambos temas`, async ({ page }, info) => {
        await page.setViewportSize(viewport);
        await page.goto("/");
        await expect(page.getByRole("button", { name: "Entrar", exact: true })).toBeVisible();
        for (const theme of ["light", "dark"]) {
          await page.emulateMedia({ colorScheme: theme as "light" | "dark" });
          await noHorizontalOverflow(page);
          await page.screenshot({ path: info.outputPath(`acceso-${theme}.png`), fullPage: true });
        }
        await login(page);
        for (const theme of ["light", "dark"]) {
          await page.evaluate((value) => {
            document.documentElement.dataset.theme = value;
          }, theme);
          await noHorizontalOverflow(page);
          const top = await page.locator(".conn").first().boundingBox();
          expect(top!.y).toBeLessThan(viewport.width >= 768 ? 310 : 430);
          await page.screenshot({ path: info.outputPath(`conexiones-${theme}.png`), fullPage: true });
        }
        await newSSH(page);
        await page.getByRole("button", { name: /Destino/ }).click();
        for (const theme of ["light", "dark"]) {
          await page.evaluate((value) => {
            document.documentElement.dataset.theme = value;
          }, theme);
          await noHorizontalOverflow(page);
          await page.screenshot({ path: info.outputPath(`destino-${theme}.png`), fullPage: true });
          const footer = await page.getByRole("button", { name: "Continuar →" }).boundingBox();
          expect(footer!.y + footer!.height).toBeLessThanOrEqual(viewport.height + 1);
        }
        await page.getByRole("button", { name: "Continuar →" }).click();
        await page.getByLabel(/^Autenticación/).selectOption("key");
        for (const theme of ["light", "dark"]) {
          await page.evaluate((value) => {
            document.documentElement.dataset.theme = value;
          }, theme);
          await noHorizontalOverflow(page);
          await page.screenshot({ path: info.outputPath(`ssh-${theme}.png`), fullPage: true });
        }
        await page.getByLabel(/^Autenticación/).selectOption("password");
        await page.route("**/api/ssh/fingerprint", async (route) => route.fulfill({ json: { fingerprint, algorithm: "ssh-ed25519" } }));
        await page.getByRole("button", { name: "Continuar →" }).click();
        const dialog = page.getByRole("dialog", { name: "Verificar servidor SSH" });
        await expect(dialog.getByText(fingerprint, { exact: true })).toBeVisible();
        for (const theme of ["light", "dark"]) {
          await page.evaluate((value) => {
            document.documentElement.dataset.theme = value;
          }, theme);
          await noHorizontalOverflow(page);
          await page.screenshot({ path: info.outputPath(`identidad-${theme}.png`), fullPage: true });
        }
        await dialog.getByRole("button", { name: "Confiar y continuar" }).click();
        await page.getByText("Certificados MySQL", { exact: true }).click();
        for (const theme of ["light", "dark"]) {
          await page.evaluate((value) => {
            document.documentElement.dataset.theme = value;
          }, theme);
          await noHorizontalOverflow(page);
          await page.screenshot({ path: info.outputPath(`proteccion-${theme}.png`), fullPage: true });
        }
        await page.getByRole("button", { name: "Continuar →" }).click();
        for (const theme of ["light", "dark"]) {
          await page.evaluate((value) => {
            document.documentElement.dataset.theme = value;
          }, theme);
          await noHorizontalOverflow(page);
          await page.screenshot({ path: info.outputPath(`comprobar-${theme}.png`), fullPage: true });
        }
      });
    }
  });
}
