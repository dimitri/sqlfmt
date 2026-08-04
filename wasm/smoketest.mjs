// Smoke-tests the built dist/wasm/sqlfmt.wasm under Node: loads the Go
// runtime's wasm_exec.js glue, instantiates the module, and exercises
// globalThis.sqlfmt.format(sql) exactly as a browser page would. Run via
// `make wasm-test` (after `make wasm`).
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const wasmDir = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "dist", "wasm");

await import(path.join(wasmDir, "wasm_exec.js"));

const go = new globalThis.Go();
const wasmBytes = await readFile(path.join(wasmDir, "sqlfmt.wasm"));
const { instance } = await WebAssembly.instantiate(wasmBytes, go.importObject);
go.run(instance);

function assertEqual(got, want, label) {
  if (got !== want) {
    console.error(`FAIL ${label}\n  got:  ${JSON.stringify(got)}\n  want: ${JSON.stringify(want)}`);
    process.exitCode = 1;
  } else {
    console.log(`ok   ${label}`);
  }
}

const ok = globalThis.sqlfmt.format("select id,name from users where id=1;");
assertEqual(ok.error, undefined, "valid SQL: no error");
assertEqual(ok.output, "select id, name\n  from users\n where id = 1;\n", "valid SQL: output");

const badArg = globalThis.sqlfmt.format(42);
assertEqual(typeof badArg.error, "string", "non-string argument: reports an error");
assertEqual(badArg.output, undefined, "non-string argument: no output");

const unterminated = globalThis.sqlfmt.format("select 'unterminated");
assertEqual(typeof unterminated.error, "string", "lex error: reports an error");
assertEqual(unterminated.output, undefined, "lex error: no output");

if (process.exitCode) {
  console.error("wasm smoke test FAILED");
} else {
  console.log("wasm smoke test passed");
}
