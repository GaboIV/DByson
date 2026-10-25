import { test, expect, type Page } from "@playwright/test";

async function fixture(page: Page) {
  const state = {
    expired: false,
    dataStatus: 200,
    login: 0,
    dataRequests: [] as URL[],
    headers: [] as string[],
    hold: false,
    release: () => {},
  };
  const fields = [
    "id",
    "Code",
    "DeliveryNoteCode",
    "PurchaseOrderId",
    "AnnualExercise",
    "IsAvailable",
    "Uploaded",
    "DocumentId",
    "IssueDate",
    "IsHideTax",
    "IsAutoBlocked",
    "UnlockedAt",
    "UnlockedById",
  ];
  const columns = fields.map((field, index) => ({
    field,
    type: index ? "varchar(100)" : "int",
    null: "YES",
    key: index ? "" : "PRI",
    default: null,
    extra: "",
    comment: "",
  }));
  await page.route("**/api/**", async (route) => {
    const request = route.request(),
      url = new URL(request.url()),
      path = url.pathname;
    let status = 200,
      data: unknown = {};
    if (path === "/api/status") data = { setupRequired: false };
    else if (path === "/api/auth/passkeys/status") data = { enabled: false };
    else if (path === "/api/auth/login") {
      if (request.postDataJSON().password !== "correct-password-2026!") {
        status = 401;
        data = { error: "invalid username or password" };
      } else {
        state.expired = false;
        state.login++;
        data = { username: "qa", role: "admin", csrf: `csrf-${state.login}`, mfa: true, mfaPolicy: "off" };
      }
    } else if (state.expired) {
      status = 401;
      data = { error: "authentication required" };
    } else if (path === "/api/auth/me") data = { username: "qa", role: "admin", csrf: `csrf-${state.login}`, mfa: true, mfaPolicy: "off" };
    else if (path === "/api/auth/mfa") data = { enabled: false, recoveryCodesLeft: 0, policy: "off", sessionVerified: true };
    else if (path === "/api/auth/mfa/setup") {
      status = 401;
      data = { error: "Contraseña incorrecta" };
    } else if (path === "/api/auth/sessions") data = { sessions: [] };
    else if (path === "/api/auth/passkeys") data = { enabled: false, credentials: [] };
    else if (path === "/api/connections")
      data = [{ id: "qa", name: "Conexión QA", env: "dev", database: "qa", host: "fixture", port: 3306, username: "qa", readOnly: false }];
    else if (path.endsWith("/databases")) data = { databases: ["qa"], databaseDetails: [] };
    else if (path.endsWith("/tables")) data = { tables: [{ name: "core_document", type: "BASE TABLE", rows: 1 }] };
    else if (path.endsWith("/schema"))
      data = { name: "core_document", columns, primaryKey: ["id"], indexes: [], createStatement: "CREATE TABLE core_document (id INT)" };
    else if (path.endsWith("/export")) {
      await route.fulfill({
        headers: { "Content-Disposition": 'attachment; filename="fixture.csv"' },
        contentType: "text/csv",
        body: "id,Code\n1,QA-001\n",
      });
      return;
    } else if (path.endsWith("/data")) {
      state.dataRequests.push(url);
      state.headers.push(request.headers()["x-csrf-token"] ?? "");
      if (state.hold) {
        state.hold = false;
        await new Promise<void>((resolve) => {
          state.release = resolve;
        });
        status = 401;
        data = { error: "authentication required" };
      } else if (state.dataStatus !== 200) {
        status = state.dataStatus;
        data = { error: "Acceso denegado por MySQL" };
      } else
        data = {
          columns: fields,
          rows: [[1, ...fields.slice(1).map((field) => `${field} QA`)]],
          totalRows: 1,
          totalRowsKnown: true,
          executedSql: "SELECT * FROM core_document LIMIT 50",
          isSelect: true,
        };
    } else {
      status = 404;
      data = { error: "unused fixture" };
    }
    try {
      await route.fulfill({ status, json: data });
    } catch {
      /* Expiring the session intentionally aborts pending requests. */
    }
  });
  await page.goto("/");
  await page.getByRole("button", { name: /^Conexión QA/ }).click();
  await page.locator(".table-item:visible").filter({ hasText: "core_document" }).click();
  await page.getByRole("button", { name: "Ver detalle del registro 1", exact: true }).waitFor();
  return state;
}

test("Filtros: barra unida, padding lateral, separadores y desplegables del tema", async ({ page }, info) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const state = await fixture(page);
  for (const theme of ["light", "dark"]) {
    await page.evaluate((theme) => (document.documentElement.dataset.theme = theme), theme);
    for (const width of [1920, 1280, 1024]) {
      await page.setViewportSize({ width, height: 800 });
      const layout = await page.evaluate(() => {
        const root = document.querySelector(".workbench-panel:not([hidden])")!;
        const tabs = root.querySelector(".table-view-toolbar")!.getBoundingClientRect();
        const bar = root.querySelector(".adminer-filter-bar")!;
        const rect = bar.getBoundingClientRect(),
          style = getComputedStyle(bar);
        return {
          gap: rect.top - tabs.bottom,
          top: style.paddingTop,
          bottom: style.paddingBottom,
          left: parseFloat(style.paddingLeft),
          native: bar.querySelectorAll("select").length,
          separators: [...bar.querySelectorAll(".filter-fieldset")].slice(1).every((el) => getComputedStyle(el).borderLeftWidth === "1px"),
          overflow: document.documentElement.scrollWidth > innerWidth,
        };
      });
      expect(Math.abs(layout.gap)).toBeLessThan(1);
      expect(layout.top).toBe("0px");
      expect(layout.bottom).toBe("0px");
      expect(layout.left).toBeGreaterThan(0);
      expect(layout.native).toBe(0);
      expect(layout.separators).toBe(true);
      expect(layout.overflow).toBe(false);
    }
    await page.setViewportSize({ width: 1536, height: 800 });
    const combo = page.getByRole("combobox", { name: "Columna (o donde sea)", exact: true });
    await combo.click();
    await expect(page.getByRole("listbox", { name: "Columna (o donde sea)", exact: true })).toBeVisible();
    await page.getByRole("searchbox", { name: "Buscar en Columna (o donde sea)" }).fill("Delivery");
    await expect(page.getByRole("option", { name: "DeliveryNoteCode", exact: true })).toBeVisible();
    await page.screenshot({ path: info.outputPath(`filtros-${theme}.png`), fullPage: true });
    await page.getByRole("option", { name: "DeliveryNoteCode", exact: true }).click();
    await expect(combo).toHaveAttribute("data-value", "DeliveryNoteCode");
    await expect(combo).toBeFocused();
  }
  const count = state.dataRequests.length;
  const operator = page.getByRole("combobox", { name: "Operador de búsqueda", exact: true });
  await operator.focus();
  await operator.press("ArrowDown");
  await operator.press("Home");
  await operator.press("Enter");
  await expect(operator).toHaveAttribute("data-value", "=");
  await expect(page.getByRole("listbox")).toHaveCount(0);
  await page.getByPlaceholder("Valor…", { exact: true }).fill("buscado");
  expect(state.dataRequests).toHaveLength(count);
  await page.getByRole("button", { name: "Mostrar", exact: true }).click();
  await expect.poll(() => state.dataRequests.length).toBe(count + 1);
  expect(state.dataRequests.at(-1)!.searchParams.get("filterCol")).toBe("DeliveryNoteCode");
  expect(state.dataRequests.at(-1)!.searchParams.get("filterOp")).toBe("=");
  expect(state.dataRequests.at(-1)!.searchParams.get("filterVal")).toBe("buscado");
  await operator.click();
  await operator.press("Escape");
  await expect(operator).toBeFocused();
  const pageSize = page.getByRole("combobox", { name: "Filas por página", exact: true });
  await pageSize.click();
  const popup = page.locator(".select-popover");
  const box = await popup.boundingBox();
  expect(box!.y).toBeGreaterThanOrEqual(0);
  expect(box!.y + box!.height).toBeLessThanOrEqual(800);
  await page.getByRole("option", { name: "100 filas", exact: true }).click();
  await expect.poll(() => state.dataRequests.at(-1)?.searchParams.get("pageSize")).toBe("100");
  await operator.click();
  await page.setViewportSize({ width: 1280, height: 800 });
  await expect(page.getByRole("listbox")).toHaveCount(0);
  await operator.click();
  await page.getByRole("button", { name: "Preferencias del espacio", exact: true }).click();
  await expect(page.getByRole("listbox")).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("Filtros múltiples: agrega hasta 3 líneas en panel flotante sin aumentar alto de barra y luego abre modal", async ({ page }) => {
  const state = await fixture(page);
  const initialBarHeight = (await page.locator(".adminer-filter-bar").boundingBox())!.height;
  await expect(page.locator(".filter-condition-row")).toHaveCount(1);

  await page.getByRole("combobox", { name: "Columna (o donde sea)", exact: true }).click();
  await page.getByRole("option", { name: "Code", exact: true }).click();
  await page.getByRole("textbox", { name: "Valor de búsqueda", exact: true }).fill("100");

  await page.getByRole("button", { name: "Agregar condición", exact: true }).click();
  await expect(page.getByRole("region", { name: "Condiciones adicionales" })).toBeVisible();
  await expect(page.locator(".filter-condition-row")).toHaveCount(2);
  await page.getByRole("combobox", { name: "Columna de condición 2", exact: true }).click();
  await page.getByRole("option", { name: "AnnualExercise", exact: true }).click();
  await page.getByRole("textbox", { name: "Valor de condición 2", exact: true }).fill("2026");

  await page.getByRole("button", { name: "Agregar condición", exact: true }).click();
  await expect(page.locator(".filter-condition-row")).toHaveCount(3);
  await page.getByRole("combobox", { name: "Columna de condición 3", exact: true }).click();
  await page.getByRole("option", { name: "IsAvailable", exact: true }).click();
  await page.getByRole("textbox", { name: "Valor de condición 3", exact: true }).fill("1");

  // La barra principal mantiene su altura de 1 sola línea sin empujar la tabla
  const barHeightWithPopover = (await page.locator(".adminer-filter-bar").boundingBox())!.height;
  expect(Math.abs(barHeightWithPopover - initialBarHeight)).toBeLessThan(2);

  // Al hacer clic en + con 3 condiciones ya configuradas, se levanta el modal con los filtros configurados hasta ahora
  await page.getByRole("button", { name: "Agregar condición", exact: true }).click();
  await expect(page.getByRole("region", { name: "Condiciones adicionales" })).toHaveCount(0);
  const modal = page.getByRole("dialog", { name: "Condiciones de filtro" });
  await expect(modal).toBeVisible();
  await expect(modal.locator(".filters-modal-row")).toHaveCount(3);
  await expect(modal.getByRole("textbox", { name: "Valor del filtro 1", exact: true })).toHaveValue("100");
  await expect(modal.getByRole("textbox", { name: "Valor del filtro 2", exact: true })).toHaveValue("2026");
  await expect(modal.getByRole("textbox", { name: "Valor del filtro 3", exact: true })).toHaveValue("1");

  // Agregar una cuarta condición dentro del modal y aplicar
  await modal.getByRole("button", { name: "+ Agregar condición", exact: true }).click();
  await expect(modal.locator(".filters-modal-row")).toHaveCount(4);
  await modal.getByRole("combobox", { name: "Columna del filtro 4", exact: true }).click();
  await page.getByRole("option", { name: "DocumentId", exact: true }).click();
  await modal.getByRole("textbox", { name: "Valor del filtro 4", exact: true }).fill("8");

  const prevCount = state.dataRequests.length;
  await modal.getByRole("button", { name: "Aplicar filtros", exact: true }).click();
  await expect(modal).toHaveCount(0);
  await expect.poll(() => state.dataRequests.length).toBe(prevCount + 1);

  const lastParams = state.dataRequests.at(-1)!.searchParams;
  expect(lastParams.get("filterCol")).toBe("Code");
  expect(lastParams.get("filterVal")).toBe("100");
  expect(JSON.parse(lastParams.get("filters") || "[]")).toEqual([
    { col: "Code", op: "LIKE", val: "100" },
    { col: "AnnualExercise", op: "LIKE", val: "2026" },
    { col: "IsAvailable", op: "LIKE", val: "1" },
    { col: "DocumentId", op: "LIKE", val: "8" },
  ]);
  await expect(page.getByRole("button", { name: "+3", exact: true })).toBeVisible();
});

test("Sesión vencida: salir del espacio, mostrar aviso y volver a autenticar sin bucle", async ({ page }, info) => {
  const state = await fixture(page);
  state.expired = true;
  await page.getByRole("button", { name: "🔄", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Entra a DByson", exact: true })).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("Tu sesión venció o fue revocada");
  await expect(page.locator(".workspace")).toHaveCount(0);
  await page.screenshot({ path: info.outputPath("sesion-vencida.png"), fullPage: true });
  await page.getByLabel("Usuario", { exact: true }).fill("qa");
  await page.getByLabel("Contraseña", { exact: true }).fill("incorrecta");
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page.getByText("invalid username or password", { exact: true })).toBeVisible();
  await page.getByLabel("Contraseña", { exact: true }).fill("correct-password-2026!");
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
  await expect(page.locator(".session-expired-notice")).toHaveCount(0);
  await page.getByRole("button", { name: /^Conexión QA/ }).click();
  await page.locator(".table-item:visible").filter({ hasText: "core_document" }).click();
  await expect.poll(() => state.headers.at(-1)).toBe("csrf-1");
});

test("Un 401 de contraseña 2FA y un 403 de permisos no cierran una sesión válida", async ({ page }) => {
  const state = await fixture(page);
  await page.getByRole("tab", { name: "Conexiones", exact: true }).click();
  await page.getByRole("button", { name: "Seguridad", exact: true }).click();
  const security = page.getByRole("dialog", { name: "Seguridad de la cuenta" });
  await security.getByRole("button", { name: "Activar verificación en dos pasos", exact: true }).click();
  await security.getByLabel("Contraseña actual", { exact: true }).fill("incorrecta");
  await security.getByRole("button", { name: "Continuar", exact: true }).click();
  await expect(security.getByRole("alert")).toContainText("Contraseña incorrecta");
  await expect(security).toBeVisible();
  await expect(page.getByRole("heading", { name: "Entra a DByson", exact: true })).toHaveCount(0);
  await security.getByRole("button", { name: "Cerrar", exact: true }).click();
  await page.getByRole("tab", { name: /Conexión QA · core_document/ }).click();
  state.dataStatus = 403;
  await page.getByRole("button", { name: "🔄", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Acceso denegado por MySQL");
  await expect(page.getByRole("heading", { name: "Entra a DByson", exact: true })).toHaveCount(0);
});

test("Al volver a la ventana se detecta la sesión vencida y se cierra una confirmación pendiente", async ({ page }) => {
  const state = await fixture(page);
  await page.clock.install();
  await page.getByRole("button", { name: "⚡ SQL", exact: true }).click();
  await page.locator(".cm-content:visible").fill("SELECT 'borrador';");
  await page.getByRole("button", { name: "Cerrar pestaña Consulta 1", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Confirmar acción" })).toBeVisible();
  state.expired = true;
  await page.clock.fastForward(31000);
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(page.getByRole("heading", { name: "Entra a DByson", exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("alert")).toContainText("Tu sesión venció");
});

test("Una respuesta antigua no invalida la sesión creada después del reingreso", async ({ page }) => {
  const state = await fixture(page);
  await page.clock.install();
  state.hold = true;
  await page.getByRole("button", { name: "🔄", exact: true }).click();
  await expect.poll(() => state.dataRequests.length).toBe(2);
  state.expired = true;
  await page.clock.fastForward(31000);
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(page.getByRole("heading", { name: "Entra a DByson", exact: true })).toBeVisible();
  await page.getByLabel("Usuario", { exact: true }).fill("qa");
  await page.getByLabel("Contraseña", { exact: true }).fill("correct-password-2026!");
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Conexiones", exact: true })).toBeVisible();
  state.release();
  await page.getByRole("button", { name: /^Conexión QA/ }).click();
  await page.locator(".table-item:visible").filter({ hasText: "core_document" }).click();
  await expect(page.getByRole("button", { name: "Ver detalle del registro 1", exact: true })).toBeVisible();
  expect(state.headers.at(-1)).toBe("csrf-1");
  await expect(page.locator(".session-expired-notice")).toHaveCount(0);
});

test("Exportar con sesión vencida lleva al login; con sesión válida descarga el archivo", async ({ page }) => {
  const state = await fixture(page);
  await page.getByRole("button", { name: "📦 Exportar", exact: true }).click();
  const pending = page.waitForEvent("download");
  await page.getByRole("link", { name: "⬇ Descargar archivo", exact: true }).click();
  expect(await (await pending).failure()).toBeNull();
  let downloads = 0;
  page.on("download", () => downloads++);
  state.expired = true;
  await page.getByRole("link", { name: "⬇ Descargar archivo", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Entra a DByson", exact: true })).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("Tu sesión venció");
  expect(downloads).toBe(0);
});
