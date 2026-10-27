import { test, expect } from "@playwright/test";

test("Google Workspace: botón, redirección y error de dominio", async ({ page }, info) => {
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/status") await route.fulfill({ json: { setupRequired: false } });
    else if (path === "/api/auth/me") await route.fulfill({ status: 401, json: { error: "authentication required" } });
    else if (path === "/api/auth/passkeys/status") await route.fulfill({ json: { enabled: false } });
    else if (path === "/api/auth/oidc/status")
      await route.fulfill({ json: { enabled: true, provider: "Google", google: true, credentialsEnabled: false } });
    else if (path === "/api/auth/oidc/login") await route.fulfill({ status: 302, headers: { location: "/?sso=error&reason=domain" } });
    else await route.fulfill({ status: 404, json: { error: "unused fixture" } });
  });
  await page.goto("/");
  const google = page.getByRole("button", { name: "Continuar con Google", exact: true });
  await expect(google).toBeVisible();
  await expect(page.getByLabel("Usuario", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Contraseña", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Entrar", exact: true })).toHaveCount(0);
  await expect(page.getByText("Usa tu cuenta corporativa de Google Workspace.")).toBeVisible();
  // Google must be usable with the local username/password fields empty.
  await google.click();
  await expect(page).toHaveURL(/sso=error&reason=domain/);
  await expect(page.getByRole("alert")).toContainText("dominio autorizado");
  for (const theme of ["light", "dark"]) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1366, 390]) {
      await page.setViewportSize({ width, height: 850 });
      await page.screenshot({ path: info.outputPath(`google-${theme}-${width}.png`) });
    }
  }
});
