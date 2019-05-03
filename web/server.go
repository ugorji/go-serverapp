package web

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ugorji/go-common/logging"
)

const MinMimeSniffLen = 64 // 512 is default

var log = logging.PkgLogger()

// Server implements net.Listener and http.Handler.
//
// Typical Usage:
//    l, err = net.Listen("tcp", ":8080")
//    lis = web.NewListener(l, 1000, OnPanicRecover)
//    httpWebSvr = web.NewServer(lis)
//    httpWebSvr.AddPipe(web.HttpHandlerPipe{myHandler})
//    http.Serve(httpWebSvr, httpWebSvr)
type HTTPServer struct {
	Pipes []Pipe
	*Listener
	seq uint64
}

// // Return a new server.
// func NewHTTPServer(l *Listener) (s *HTTPServer) {
// 	return &HTTPServer{Listener: l}
// }

// func (s *HTTPServer) AddPipe(pipes ...Pipe) {
// 	s.pipes = append(s.pipes, pipes...)
// }

// Remove (*Server).Serve()
// Users should create their own http.Server and pass this as a listener.
// This allows users configure R/W Timeout, MaxHeaderBytes, TLSNextProto, etc.
// func (s *Server) Serve() (err error) {
// 	httpsvr := &http.Server { Handler: s }
// 	return httpsvr.Serve(s)
// }

// ServeHTTP serves the request as a pipeline.
func (s *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer s.handlePanic()
	// ServeHttp creates a pipeline, with the Server at the front, and a FlusherPipe at the end.
	// This guarantees that the output from the underlying Handler is flushed.
	ps := make([]Pipe, len(s.Pipes)+2)
	copy(ps[1:], s.Pipes)
	ps[0] = s
	ps[len(ps)-1] = FlusherPipe{}
	w2 := AsResponseWriter(w)
	NewPipeline(ps...).Next(w2, r)
	// w2.Write(nil) // force headers written
	w2.Flush() // force headers written
}

func (s *HTTPServer) ServeHttpPipe(w ResponseWriter, r *http.Request, f *Pipeline) {
	onClosed := func() {
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	//if we're in pause mode, disable keep-alive for in-flight connections.
	//(so keep-alive conns in don't starve others new connections from coming in).
	//else if > maxconn, then set pause mode.
	onPaused := func() {
		w.Header().Set("Connection", "close")
	}
	onRun := func() {
		f.Next(w, r)
	}
	// add a seq num to the id, so we can correlate requests in the log file
	n := atomic.AddUint64(&s.seq, 1)
	time0 := time.Now()
	log.Debug(nil, "Request %d: %s%s", n, r.Host, r.URL.Path)
	s.Listener.Run(onClosed, onPaused, onRun)
	log.Debug(nil, "Request %d: %s%s, %d bytes, response: %d, in %v",
		n, r.Host, r.URL.Path, w.NumBytesWritten(), w.ResponseCode(), time.Since(time0))
}
