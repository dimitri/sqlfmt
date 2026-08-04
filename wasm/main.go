//go:build js && wasm

// Command wasm compiles the sqlfmt formatter to WebAssembly for in-browser
// use, exposing a single global JS function:
//
//	globalThis.sqlfmt.format(sql)
//	  -> { output: string } on success
//	  -> { error: string }  on a real parse/format error
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
	js.Global().Set("sqlfmt", sqlfmt)
	<-done // keep the Go runtime (and the exported function) alive
}
