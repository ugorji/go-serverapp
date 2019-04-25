# go-serverapp/app

This repository contains the `go-serverapp/app` library (or command).

To install:

```
go get github.com/ugorji/go-serverapp/app
```

# Package Documentation


Package app provides shared foundation for server based applications.


## CONTEXT

We do not depend on information stored per request. Instead, we pass all the
information that is needed for each function via arguments.

All interaction in the application may require a Context. The Context
includes (for example):

  - app-engine.Context
  - tx (is this in a transaction?)
  - util.SafeStore (contains all information that normally goes into request attributes)

Handlers should take an app.Context. The top-level handler should create it
and pass it down the function chain. WebRouter supports this natively with a
Context object.


## DRIVER

This is the interface between the services and the backend system. It is
exposed by an app.Context.

Some typical things the Driver will support:

  - SendMail(...)
  - Give me an appropriate *http.Client
  - Store some entities in the backend
  - Load an entity from backend given its id, or some of its properties
  - ...


## MISC

Some misc info:

  - To allow for rpc, testing, and other uses, we provide a header called
    "Z-App-Json-Response-On-Error" (app.UseJsonOnErrHttpHeaderKey).
    If set, then we return errors as a json string, as opposed to showing the
    user friendly, and browser friendly, error view page.
    RPC, Testing, etc will set this on their requests.


## Base App

This is the base of an actual application. It sets up everything and handles
requests. By design, it fully implements app.AppDriver.

It sets up the following specifically:

  - logging (calling logging.RunAsync if desired)
  - app Driver (setup app.Svc)
  - Initialize template sets for all the different views.
    Including creating a function map for the templates and defining an appropriate Render method
  - Setup Routing logic ie how to route requests
  - Setup OauthHandles for all our supported oauth providers
  - map all requests to its builtin dispatcher
    (which wraps router.Dispatch and does pre and post things)

Why we did things a certain way:

  - Wrapping ResponseWriter:
    So we can know if the headers have been written (ie response committed)

We need to differentiate code for dev environment from prod environment:

  - Tests should not be shipped on prod
  - LoadInit, other dev things should not even run on prod

This package expects the following:

  - Define a route called "landing" (which is typically mapped to Path: /)
    so we can route to the landing page or show the link to the landing page
  - Define views called "error", "notfound" so we can show something when either is encountered from code
  - Also define view called "apperror", and we just show its content when there's an error
    without inheriting or depending on anyone else.


## WEB ROUTER

Intelligently dispatches requests to handlers.

It figures out what handler to dispatch a request to, by analysing any/all
attributes of the request (headers and url). It also supports the ability to
recreate a request (URL + Headers) based on the characteristics of a Route.

A Route is Matched if all its Routes or Route Expressions match.

It works as follows:

  - A Route is a node in a tree. It can have children, and also have matchExpr to determine
    whether to proceed walking down the tree or not.
  - At runtime, the package looks for the deepest Route which can handle a Request,
    in sequence. This means that a branch is checked, and if it matches, then its children
    are checked. All this recursively. If none of it's children can handle the request, then
    the branch handles it.
  - You can also reverse-create a URL for a route, from the parameters of the route. For example,
    if a route has Host: {hostname}.mydomain.com, and Path: /show/{id}, you should be able to
    reconstruct the URL for that host, passing appropriate parameters for hostname and id.

An application will define functions with the signature:

```
    (w http.ResponseWriter, r *http.Request) (int, os.Error)
```

These functions will handle requests, and return the appropriate http status
code, and an error The application wrapper can then decide whether to do
anything further based on these e.g. show a custom error view, etc

In addition, during a Dispatch, when Paths are matched, it will look for
named variables and store them in a request-scoped store. This way, they are
not parsed again, and can be used for:

  - during request handling, to get variables
  - during URL generation of a named route

An application using the router will have pseudo-code like:

```
    -----------------------------------------------------
    func main() {
      go logging.Run()
      root = web.NewRoot("Root")
      web.NewRoute(root, "p1", p1).Path("/p1/${key}")
      ...
      http.HandleFunc("/", MyTopLevelHandler)
    }
```

```go
    func MyTopLevelHandler(w http.ResponseWriter, r *http.Request) {
      ... some filtering work
      statusCode, err = web.Dispatch(ctx, root, w, r)
      ... some more filtering and error handling
    }
    -----------------------------------------------------
```

## Exported Package API

```go
const VarsKey = "router_vars" ...
const UseJsonOnErrHttpHeaderKey = "Z-App-Json-Response-On-Error"
func Dispatch(ctx Context, root *Route, w http.ResponseWriter, r *http.Request) error
func DumpRequest(c Context, r *http.Request) (err error)
func NoMatchFoundHandler(c Context, w http.ResponseWriter, r *http.Request) error
func RegisterAppDriver(appname string, driver Driver)
func TrueExpr(store safestore.I, req *http.Request) (bool, error)
func Vars(sf safestore.I) (vars map[string]string)
type AppInfo struct{ ... }
type BaseApp struct{ ... }
    func NewApp(devServer bool, uuid string, viewsCfgPath string, lld LowLevelDriver) (gapp *BaseApp, err error)
type BaseDriver struct{ ... }
type BasicContext struct{ ... }
type BlobInfo struct{ ... }
type BlobReader interface{ ... }
type BlobWriter interface{ ... }
type Cache interface{ ... }
type Context interface{ ... }
type Driver interface{ ... }
    func AppDriver(appname string) (dr Driver)
type HTTPHandler struct{ ... }
type Handler interface{ ... }
type HandlerFunc func(Context, http.ResponseWriter, *http.Request) error
type Key interface{ ... }
type LowLevelDriver interface{ ... }
type PageNotFoundError string
type QueryFilter struct{ ... }
type QueryFilterOp int
    const EQ QueryFilterOp ...
    func ToQueryFilterOp(op string) QueryFilterOp
type QueryOpts struct{ ... }
type Route struct{ ... }
    func NewRoot(name string) (root *Route)
    func NewRoute(parent *Route, name string, handler Handler) *Route
    func NewRouteFunc(parent *Route, name string, handler HandlerFunc) *Route
type SafeStoreCache struct{ ... }
type Tier int32
    const DEVELOPMENT Tier = iota + 1 ...
type User struct{ ... }
```
