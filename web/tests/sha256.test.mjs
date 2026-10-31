import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { __test } from "../src/workspace/sha256.ts";

test("SHA-256 sin WebCrypto coincide con Node en los bordes de bloque", () => {
  for (const text of ["", "abc", "x".repeat(55), "x".repeat(56), "x".repeat(64), "ñandú ".repeat(300)]) {
    const bytes = new TextEncoder().encode(text);
    const got = Buffer.from(__test.fallbackSha256(bytes)).toString("hex");
    assert.equal(got, createHash("sha256").update(bytes).digest("hex"), `longitud ${bytes.length}`);
  }
});
