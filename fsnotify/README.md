# go-serverapp/fsnotify

This repository contains the `go-serverapp/fsnotify` library.

To install:

```
go get github.com/ugorji/go-serverapp/fsnotify
```

# Package Documentation


Package fsnotify provides filesystem listening and notification support.

## Exported Package API

```go
var ClosedErr = errorutil.String("<watcher closed>")
type WatchEvent struct{ ... }
type Watcher struct{ ... }
    func NewWatcher(bufsize, sysBufSize int, sleepTime time.Duration, fn func([]*WatchEvent)) (w *Watcher, err error)
```
