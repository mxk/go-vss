vss
===

[![Go Reference](https://pkg.go.dev/badge/github.com/mxk/go-vss.svg)](https://pkg.go.dev/github.com/mxk/go-vss)
[![Report card](https://goreportcard.com/badge/github.com/mxk/go-vss)](https://goreportcard.com/report/github.com/mxk/go-vss)

Package vss exposes Windows Volume Shadow Copy API.

```
go get github.com/mxk/go-vss
```

The current v1 API is stable and production-ready. It is based on the WMI [Win32_ShadowCopy] class.

[Win32_ShadowCopy]: https://learn.microsoft.com/en-us/previous-versions/windows/desktop/legacy/aa394428(v=vs.85)
