//go:build js && wasm

// Command wasm compiles the sqlfmt formatter to WebAssembly for in-browser
// use (GOOS=js GOARCH=wasm), exposing a single global JS function:
//
//	globalThis.sqlfmt.format(sql)
//	  -> { output: string } on success
//	  -> { error: string }  on a real parse/format error
//
// Load it alongside the Go runtime's wasm_exec.js glue (copied into this
// build's output directory by `make wasm`, from
// $(go env GOROOT)/lib/wasm/wasm_exec.js), e.g.:
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
