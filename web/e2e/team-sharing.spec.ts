import { test, expect, type Page } from "@playwright/test";
import type { Connection, ConnectionGrant, TeamUser } from "../src/api";

async function fixture(page: Page, role = "admin", access: Connection["access"] = "owner") {
  const state = {
    users: [
      { id: 1, username: "gabo", role: "admin", disabled: false, createdAt: 1 },
      { id: 2, username: "ana.garcia", role: "member", disabled: false, createdAt: 1 },
      { id: 3, username: "carlos", role: "viewer", disabled: false, createdAt: 1 },
      { id: 4, username: "maria", role: "member", disabled: true, createdAt: 1 },
    ] as TeamUser[],
    connection: {
      id: "team-db",
      name: "Analítica del equipo",
      host: "mysql.internal",
      port: 3306,
      username: "analytics",
      database: "analytics",
      env: "dev",
      tlsMode: "required",
      readOnly: false,
      shared: false,
      ownerId: 1,
      access,
    } as Connection,
    grants: [] as ConnectionGrant[],
    writes: [] as { path: string; body: Record<string, unknown> }[],
    failUsers: false,
  };
  await page.route("**/api/**", async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname;
    const method = request.method();
    const body = request.postData() ? request.postDataJSON() : {};
    let data: unknown = {},
      status = 200;
    if (method !== "GET") state.writes.push({ path, body });
    if (path === "/api/status") data = { setupRequired: false };
    else if (path === "/api/auth/me") data = { username: "gabo", role, csrf: "fixture-csrf", mfa: true, mfaPolicy: "off" };
    else if (path === "/api/connections") data = [state.connection];
    else if (path === "/api/users" && method === "GET") {
      if (state.failUsers) {
        status = 500;
        data = { error: "No se pudo cargar el equipo" };
      } else data = state.users;
    } else if (path === "/api/users" && method === "POST") {
      state.users.push({ id: 10, username: body.username, role: body.role, disabled: false, createdAt: 1 });
      status = 201;
    } else if (path.startsWith("/api/users/")) {
      const user = state.users.find((u) => u.id === Number(path.split("/").at(-1)))!;
      if (method === "DELETE") state.users = state.users.filter((u) => u !== user);
      else {
        if (body.role) user.role = body.role;
        if (body.disabled !== undefined) user.disabled = body.disabled;
        if (body.email !== undefined) user.email = body.email;
      }
    } else if (path.endsWith("/grants/users"))
      data = state.users
        .filter((u) => !u.disabled && u.role !== "admin" && u.id !== state.connection.ownerId)
        .map(({ id, username, role }) => ({ id, username, role }));
    else if (path.endsWith("/sharing")) {
      state.connection.shared = body.shared;
      if (!body.shared) state.grants = [];
      data = { shared: body.shared };
    } else if (path.endsWith("/grants") && method === "GET") data = state.grants;
    else if (path.endsWith("/grants") && method === "POST") {
      const user = state.users.find((u) => u.id === body.userId)!;
      state.grants = state.grants.filter((g) => g.userId !== user.id);
      state.grants.push({ userId: user.id, username: user.username, role: user.role, level: body.level });
      status = 201;
    } else if (path.includes("/grants/") && method === "DELETE") {
      state.grants = state.grants.filter((g) => g.userId !== Number(path.split("/").at(-1)));
      status = 204;
    } else {
      status = 404;
      data = { error: "Unused fixture" };
    }
    await route.fulfill(status === 204 ? { status } : { status, json: data });
  });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
  return state;
}

test("Equipo: filtros, protección de administración y contraseña dentro de la app", async ({ page }) => {
  const state = await fixture(page);
  await page.getByRole("button", { name: "Equipo", exact: true }).click();
  const team = page.getByRole("dialog", { name: "Equipo", exact: true });
  await expect(team.getByRole("combobox", { name: "Rol de gabo", exact: true })).toBeDisabled();
  await expect(team.getByRole("button", { name: "Eliminar a gabo", exact: true })).toBeDisabled();
  await team.getByRole("button", { name: "Vincular correo de gabo", exact: true }).click();
  const linking = page.getByRole("dialog", { name: "Correo de Workspace · gabo" });
  await linking.getByLabel("Correo corporativo", { exact: true }).fill("gabo@equaly.pe");
  await linking.getByRole("button", { name: "Guardar correo", exact: true }).click();
  await expect(linking).toBeHidden();
  await expect(team.getByText("gabo@equaly.pe")).toBeVisible();
  expect(state.users[0].email).toBe("gabo@equaly.pe");
  await team.getByRole("searchbox", { name: "Buscar personas" }).fill("ana");
  await expect(team.locator(".team-member")).toHaveCount(1);
  await team.getByRole("button", { name: "Cambiar contraseña de ana.garcia", exact: true }).click();
  const reset = page.getByRole("dialog", { name: "Cambiar contraseña · ana.garcia" });
  await reset.getByLabel("Nueva contraseña", { exact: true }).fill("nueva-contraseña-2026");
  await reset.getByLabel("Mostrar contraseña").check();
  await expect(reset.getByLabel("Nueva contraseña", { exact: true })).toHaveAttribute("type", "text");
  await reset.getByRole("button", { name: "Guardar contraseña" }).click();
  await expect(reset).toBeHidden();
  expect(state.writes.at(-1)?.body).toEqual({ password: "nueva-contraseña-2026" });
  await team.getByRole("searchbox").fill("");
  await team.getByRole("button", { name: "Añadir persona" }).click();
  await team.getByLabel("Usuario", { exact: true }).fill("lucia");
  await team.getByLabel("Contraseña inicial").fill("contraseña-lucia-2026");
  await team.getByRole("button", { name: "Crear cuenta", exact: true }).click();
  await expect(team.locator(".team-member")).toHaveCount(5);
  expect(state.writes.at(-1)?.body).toEqual({ username: "lucia", password: "contraseña-lucia-2026", role: "member" });
});

test("Compartir: activar, conceder, cambiar, revocar y dejar de compartir", async ({ page }) => {
  const state = await fixture(page);
  await page.getByRole("button", { name: "Compartir con el equipo", exact: true }).click();
  const share = page.getByRole("dialog", { name: "Compartir con el equipo" });
  await share.getByRole("button", { name: "Activar uso compartido" }).click();
  await expect(share.getByText("Aún no has añadido a nadie")).toBeVisible();
  await share.getByRole("combobox", { name: "Persona que recibirá acceso" }).click();
  await page.getByRole("option", { name: "ana.garcia", exact: true }).click();
  await share.getByRole("button", { name: "Dar acceso", exact: true }).click();
  await expect(share.getByRole("combobox", { name: "Nivel de ana.garcia" })).toBeVisible();
  await share.getByRole("combobox", { name: "Nivel de ana.garcia" }).click();
  await page.getByRole("option", { name: "Puede gestionar", exact: true }).click();
  await expect.poll(() => state.grants[0]?.level).toBe("manage");
  await share.getByRole("button", { name: "Quitar acceso a ana.garcia" }).click();
  await page.getByRole("dialog", { name: "Confirmar acción" }).getByRole("button", { name: "Cancelar" }).click();
  expect(state.grants).toHaveLength(1);
  await share.getByRole("button", { name: "Dejar de compartir" }).click();
  await page.getByRole("dialog", { name: "Confirmar acción" }).getByRole("button", { name: "Continuar" }).click();
  await expect(share.getByText("Conexión privada", { exact: true })).toBeVisible();
  expect(state.grants).toHaveLength(0);
  expect(state.writes.filter((w) => w.path.endsWith("/sharing")).map((w) => w.body)).toEqual([{ shared: true }, { shared: false }]);
});

test("Permisos: un gestor comparte sin administrar cuentas; Solo lectura respeta los límites", async ({ page }) => {
  const state = await fixture(page, "member", "manage");
  state.connection.shared = true;
  await page.reload();
  await expect(page.getByRole("button", { name: "Equipo", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Gestionar acceso", exact: true }).click();
  const share = page.getByRole("dialog", { name: "Compartir con el equipo" });
  await expect(share.getByRole("button", { name: "Dejar de compartir" })).toHaveCount(0);
  await share.getByRole("combobox", { name: "Persona que recibirá acceso" }).click();
  await page.getByRole("option", { name: "carlos · Solo lectura", exact: true }).click();
  await expect(share.getByRole("combobox", { name: "Permiso que se concederá" })).toBeDisabled();
  await share.getByRole("button", { name: "Dar acceso", exact: true }).click();
  await expect(share.getByRole("combobox", { name: "Nivel de carlos" })).toBeDisabled();
  expect(state.grants[0]?.level).toBe("use");
});

test("Solo lectura: oculta edición y creación; bloquea producción", async ({ page }) => {
  const state = await fixture(page, "viewer", "use");
  state.connection.env = "prod";
  state.connection.shared = true;
  await page.reload();
  await expect(page.getByRole("button", { name: "Conectar", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Editar", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Nueva conexión" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Gestionar acceso", exact: true })).toHaveCount(0);
});

test("Equipo: error recuperable; diseño adaptable en claro y oscuro", async ({ page }, info) => {
  const state = await fixture(page);
  state.failUsers = true;
  await page.getByRole("button", { name: "Equipo", exact: true }).click();
  const team = page.getByRole("dialog", { name: "Equipo", exact: true });
  await expect(team.getByRole("alert")).toBeVisible();
  await expect(team.getByText("Cargando el equipo…")).toHaveCount(0);
  state.failUsers = false;
  await team.getByRole("button", { name: "Actualizar lista" }).click();
  await expect(team.locator(".team-member")).toHaveCount(4);
  for (const theme of ["light", "dark"]) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1366, 768, 390, 320]) {
      await page.setViewportSize({ width, height: 900 });
      expect(await team.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
      expect(await team.locator(".utility-dialog-body").evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
      await page.screenshot({ path: info.outputPath(`equipo-${theme}-${width}.png`) });
    }
  }
});

test("Compartir: diseño adaptable y exclusión de Solo lectura en producción", async ({ page }, info) => {
  const state = await fixture(page);
  state.connection.shared = true;
  state.connection.env = "prod";
  state.grants = [{ userId: 2, username: "ana.garcia", role: "member", level: "manage" }];
  await page.reload();
  await page.getByRole("button", { name: "Gestionar acceso", exact: true }).click();
  const share = page.getByRole("dialog", { name: "Compartir con el equipo" });
  await expect(share.getByRole("combobox", { name: "Nivel de ana.garcia" })).toBeVisible();
  await expect(share.getByRole("combobox", { name: "Persona que recibirá acceso" })).toBeDisabled();
  for (const theme of ["light", "dark"]) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1366, 768, 390, 320]) {
      await page.setViewportSize({ width, height: 900 });
      expect(await share.locator(".utility-dialog-body").evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
      await page.screenshot({ path: info.outputPath(`compartir-${theme}-${width}.png`) });
    }
  }
  await share.getByRole("button", { name: "Quitar acceso a ana.garcia" }).click();
  await page.getByRole("dialog", { name: "Confirmar acción" }).getByRole("button", { name: "Continuar" }).click();
  await expect(share.getByText("Aún no has añadido a nadie")).toBeVisible();
  expect(state.grants).toHaveLength(0);
});

test("Exportar una copia no concede permisos y exige gestionar para incluir credenciales", async ({ page }) => {
  await fixture(page, "member", "use");
  await page.getByRole("button", { name: "Exportar archivo" }).click();
  const transfer = page.getByRole("dialog", { name: "Exportar conexiones a un archivo" });
  await expect(transfer.getByText(/no concede acceso al equipo/)).toBeVisible();
  await expect(transfer.getByRole("checkbox", { name: "Incluir contraseñas y certificados TLS en un paquete cifrado" })).toBeDisabled();
  await expect(transfer.getByRole("button", { name: "Descargar archivo" })).toBeEnabled();
});
