//go:build js && wasm

// Command wasm compiles the sqlfmt formatter to WebAssembly for in-browser
// use, exposing:
//
//	globalThis.sqlfmt.format(sql)
//	  -> { output: string } on success
//	  -> { error: string }  on a real parse/format error
//	globalThis.sqlfmt.version
//	  -> the sqlfmt version string, e.g. "0.1" or "0.1.gd986ff7" -- same
//	     string and same convention as `sqlfmt -V`, and the same
//	     "Library.version" convention used by e.g. React.version,
//	     Vue.version, and d3.version
//
// Built with TinyGo (-target=wasm), not the standard `go build` js/wasm
// target, to keep the module small -- see the "WebAssembly build" section
// of README.md. Load it alongside the matching wasm_exec.js glue (copied
// into this build's output directory by `make wasm`, from TinyGo's own
// target support files, not the standard Go toolchain's), e.g.:
//
//	<script src="wasm_exec.js"></script>
//	<script>
//	  const go = new Go();
//	  WebAssembly.instantiateStreaming(fetch("sqlfmt.wasm"), go.importObject)
//	    .then((result) => go.run(result.instance));
//	</script>
package main

import (
	"strings"
	"syscall/js"

	"github.com/dimitri/sqlfmt/format"
)

// version is overridden at build time via -ldflags "-X main.version=...";
// see the VERSION computation in the Makefile (same value `sqlfmt -V`
// reports for the same build).
var version = "0.1"

func formatSQL(_ js.Value, args []js.Value) any {
	result := make(map[string]any)
	if len(args) != 1 || args[0].Type() != js.TypeString {
		result["error"] = "sqlfmt.format(sql): expected a single string argument"
		return result
	}

	out, err := format.Format(strings.NewReader(args[0].String()))
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	result["output"] = out
	return result
}

func main() {
	done := make(chan struct{})
	sqlfmt := js.Global().Get("Object").New()
	sqlfmt.Set("format", js.FuncOf(formatSQL))
	sqlfmt.Set("version", version)
	js.Global().Set("sqlfmt", sqlfmt)
	<-done // keep the Go runtime (and the exported function) alive
}
