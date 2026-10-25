import test from "node:test";
import assert from "node:assert/strict";
import { MAX_TABS, restoreActiveTab, restoreTabs, scope } from "../src/workTabs.ts";

function tab(id, extra = {}) {
  return { id, connectionId: "c1", database: "db", table: "t", sql: false, view: "data", title: "t", ...extra };
}

test("restoreTabs: ignora JSON roto, no-listas y entradas malformadas", () => {
  assert.deepEqual(restoreTabs(null), []);
  assert.deepEqual(restoreTabs(""), []);
  assert.deepEqual(restoreTabs("no-json{"), []);
  assert.deepEqual(restoreTabs(JSON.stringify({ id: "x" })), []);
  assert.deepEqual(
    restoreTabs(JSON.stringify([tab("a"), null, "x", { id: 1 }, { ...tab("b"), connectionId: 2 }, tab("c")])),
    [tab("a"), tab("c")].map((t) => ({
      ...t,
      dirty: false,
      customTitle: undefined,
      groupTable: "t",
      groupDatabase: "db",
      groupId: scope("c1", "db", "t"),
    })),
  );
});

test("restoreTabs: normaliza vista, grupo, título y tope de pestañas", () => {
  const long = "q".repeat(80);
  const restored = restoreTabs(
    JSON.stringify([
      { ...tab("s", { table: "", view: "¿?", sql: true }), customTitle: long, dirty: true },
      { ...tab("d", { view: "¿?" }) },
      { ...tab("b", { table: "", view: "¿?" }) },
      ...Array.from({ length: MAX_TABS + 5 }, (_, i) => tab(`t${i}`, { table: "", view: "browse" })),
    ]),
  );
  const [sql, data, browse] = restored;
  assert.equal(sql.view, "sql");
  assert.equal(sql.dirty, false);
  assert.equal(sql.customTitle, "q".repeat(60));
  assert.equal(data.view, "data");
  assert.equal(browse.view, "browse");
  assert.equal(browse.groupId, "b");
  assert.equal(restored.length, MAX_TABS);
  assert.equal(restored.at(-1).id, `t${MAX_TABS - 4}`);
});

test("restoreActiveTab: vuelve a la pestaña guardada solo si sigue abierta", () => {
  const tabs = [tab("a"), tab("b")];
  assert.equal(restoreActiveTab("b", tabs), "b");
  for (const stored of [null, "", "home", "cerrada"]) assert.equal(restoreActiveTab(stored, tabs), "home");
  assert.equal(restoreActiveTab("a", []), "home");
});
