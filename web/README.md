# go-serverapp/web

This repository contains the `go-serverapp/web` library (or command).

To install:

```
go get github.com/ugorji/go-serverapp/web
```

# Package Documentation


Package web is a very lightweight framework for building web applications
using `go`.

It adds clean seamless and easy to use support for:

    - access logging
    - pipelines
    - template management
    - shared storage
    - ...


## TEMPLATES

Templates support optimized management of templates for the whole
application.

The model is as below:

    - Each view page (e.g. landing page) corresponds to a given Template (set)
    - A config file defines the templates, so we can re-use templates
    - A Tree is used, so that a TemplateSet can be easily configured to share templates. E.g.
      All the core templates share the same header and footers.

## Typical usage for loading from within main app

```
    vcn = new(web.ViewConfigNode)
    myVfs = new(util.VFS)
    err = myVfs.AddIfExist("templates.zip", "templates")
    f, err = os.Open("resources/core/web/views.json")
    err = json.NewDecoder(f).Decode(vcn)
    views = web.NewViews()
    views.FnMap["Eq"] = reflect.DeepEqual // MUST register functions BEFORE adding templates
    views.AddTemplates(myVfs, regexp.MustCompile(`.*\.thtml`)) // ...
    views.Load(vcn)
```

The View Handlers can call:

```
    views.Views["core.landing"].Execute(writer, "main", data)
```


## WEBSTORE

WebStore allows us store information along with a namespace. It simply
maintains a non-synchronized map[interface{}] map[string]interface{} for
each namespace. It expects that the namespace represents usage within a
single goroutine.

An example usage is within a web environment where the namespace can be the
*http.Request object.

It is generally recommended to pass structures around in function calls.
However, there may be times where using this is the only option, and in
those situations, use with care.

It can be used in a web environment, where you want to store data on behalf
of a request. In this context, the namespace is the request.

## Exported Package API

```go
const FlashMessage = "FlashMessage"
const MinMimeSniffLen = 64
var ClosedErr = errors.New("<closed>")
func AddHandlerMessages(r *http.Request, w http.ResponseWriter, ckName string, ...) (err error)
func NewCookie(host, name, value string, ttlsec int, encode bool) *http.Cookie
func NewGzipWriterPool(level, initPoolLen, poolCap int) *pool.T
type AccessLogger struct{ ... }
    func NewAccessLogger(filename string) *AccessLogger
type BufferPipe struct{ ... }
    func NewBufferPipe(size, initPoolLen, poolCap int) (s *BufferPipe)
type FlusherPipe struct{}
type GzipPipe struct{ ... }
    func NewGzipPipe(level, initPoolLen, poolCap int) (s *GzipPipe)
type HTTPServer struct{ ... }
type HandlerMessage struct{ ... }
type HttpHandlerPipe struct{ ... }
type Listener struct{ ... }
    func NewListener(l net.Listener, maxNumConn int32, panicFlags OnPanicFlags) (s *Listener)
type OnPanicFlags uint8
    const OnPanicRecover OnPanicFlags = 1 << iota ...
type Pipe interface{ ... }
type Pipeline struct{ ... }
    func NewPipeline(pipes ...Pipe) *Pipeline
type ResponseWriter interface{ ... }
    func AsResponseWriter(w http.ResponseWriter) ResponseWriter
type ViewConfigNode struct{ ... }
type Views struct{ ... }
    func NewViews() *Views
type WebStore struct{ ... }
    func NewWebStore() *WebStore
```
