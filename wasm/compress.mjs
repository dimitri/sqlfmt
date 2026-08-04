// Produces a gzip pre-compressed copy of dist/wasm/sqlfmt.wasm
// (sqlfmt.wasm.gz) at maximum compression, using Node's built-in zlib
// rather than requiring a system `gzip` binary. Run via `make wasm` (after
// the tinygo + wasm-opt build). See the "WebAssembly build" section of
// README.md for why this exists and how to consume it -- GitHub Releases
// does not serve Content-Encoding, so a browser must decompress this
// explicitly (via DecompressionStream) rather than relying on transparent
// HTTP compression.
import { readFile, writeFile } from "node:fs/promises";
import { gzipSync, constants as zlibConstants } from "node:zlib";
import { fileURLToPath } from "node:url";
import path from "node:path";

const wasmDir = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "dist", "wasm");
const wasmPath = path.join(wasmDir, "sqlfmt.wasm");

const input = await readFile(wasmPath);

const gz = gzipSync(input, { level: zlibConstants.Z_BEST_COMPRESSION });
await writeFile(wasmPath + ".gz", gz);

console.log(
  `sqlfmt.wasm      ${input.length.toLocaleString()} bytes\n` +
    `sqlfmt.wasm.gz  ${gz.length.toLocaleString()} bytes`,
);
