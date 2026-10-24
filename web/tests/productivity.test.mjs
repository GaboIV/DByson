import test from "node:test";
import assert from "node:assert/strict";
import { parseCSV, ident, literal } from "../src/workspace/productivity.ts";

test("CSV: BOM, CRLF, Unicode, comillas y saltos dentro de un campo", () => {
  assert.deepEqual(parseCSV('\uFEFFid,nombre,notas\r\n1,"Muñoz, ""QA""","Uno\nDos"\r\n'), [
    ["id", "nombre", "notas"],
    ["1", 'Muñoz, "QA"', "Uno\nDos"],
  ]);
  assert.deepEqual(parseCSV("a;b\n1;2", ";"), [
    ["a", "b"],
    ["1", "2"],
  ]);
  assert.deepEqual(parseCSV('a,b\n"",\n'), [
    ["a", "b"],
    ["", ""],
  ]);
});
test("CSV: rechaza truncamiento, comillas rotas y exceso de filas", () => {
  for (const value of ['a,b\n"unclosed,b', "a,b\n1", 'a,b\n"a"invalid,b', 'a,b\na"b,c', "a,b\n1,2,3"]) assert.throws(() => parseCSV(value));
  assert.throws(() => parseCSV("a\n" + "1\n".repeat(5001)));
});
test("CSV: round-trip de datos con caracteres reservados en 128 filas", () => {
  let seed = 42;
  const chars = ["a", "ñ", '"', ",", "\r", "\n", "\\", "0", " "];
  const rows = [["a", "b", "c"]];
  for (let i = 0; i < 128; i++) {
    const row = [];
    for (let j = 0; j < 3; j++) {
      let value = "";
      for (let n = 0; n < 30; n++) {
        seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
        value += chars[seed % chars.length];
      }
      row.push(value);
    }
    rows.push(row);
  }
  const csv = rows.map((row) => row.map((v) => '"' + v.replaceAll('"', '""') + '"').join(",")).join("\r\n");
  assert.deepEqual(parseCSV(csv), rows);
});
test("Identificadores y valores FK no dependen del modo de escape SQL", () => {
  assert.equal(ident("a`b"), "`a``b`");
  const value = "ñ'\\\n; DROP TABLE x;--";
  const sql = literal(value);
  assert.equal(sql, `CONVERT(X'${Buffer.from(value).toString("hex")}' USING utf8mb4)`);
  assert.equal(literal(""), "CONVERT(X'' USING utf8mb4)");
});
