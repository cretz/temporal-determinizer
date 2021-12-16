
Under development POC to support deterministic Go runtime. Options:

* [TinyGo](https://tinygo.org)
  * [Too many restrictions](https://tinygo.org/docs/reference/lang-support/)
  * Not (yet) obvious how to intercept certain
* Code rewriting/transpiling
  * Feasible (can even do it via `toolexec` option on `go build` with import paths changes)
  * A bit difficult because we have to make sure to avoid Go stdlib or Temporal lib itself. Therefore we'd have to
    replace a lot of public runtime.
  * Lots to replace (e.g. panicking on crypto rand), but again technically doable
* WASM
  * Hard to replace `runtime.newproc` and map iterations to make deterministic
  * Using Go WASM is targeted for JS and therefore you have to have a harness
    [like this](https://github.com/mattn/gowasmer) just to work
* [Yaegi](https://github.com/traefik/yaegi)
  * Seemingly limited ability to change runtime interpretation of 
* [golang.org/x/tools/go/ssa/interp](https://pkg.go.dev/golang.org/x/tools/go/ssa/interp)
  * Most code unexported, so no injection points, but code is small that hopefully hijacking small pieces is safe
  * Currently evaluating in this repository