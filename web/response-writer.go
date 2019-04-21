package web

import (
	"errors"
	// "io"
	"net"
	// "bytes"
	"net/http"
	"bufio"
	// "runtime/debug"
)

// ResponseWriter is a fat interface encompassing http.ResponseWriter,
// and adding all net/http responsewriter interfaces (Hijacker, Flusher, CloseNotifier).
// This is done so that all pipe writers can be hijacked, flushed, etc.
// It also adds some tracking methods.
type ResponseWriter interface {
	http.ResponseWriter
	http.CloseNotifier
	http.Flusher
	http.Hijacker
	// Write(b []byte) (i int, err error) 
	// Header() http.Header 
	// WriteHeader(code int) 
	// Flush() 
	// CloseNotify() <-chan bool 
	// Hijack() (net.Conn, *bufio.ReadWriter, error) 
	IsHeaderWritten() bool 
	NumBytesWritten() int64 
	ResponseCode() int 
}

func AsResponseWriter(w http.ResponseWriter) ResponseWriter {
	w2, _ := w.(ResponseWriter)
	if w2 == nil {
		w2 = &responseWriter{w: w}
	}
	return w2
}

// responseWriter implements ResponseWriter.
// It allows us initialize a ResponseWriter from a http.ResponseWriter
// and have all pipes leverage it.
type responseWriter struct {
	w http.ResponseWriter
	numBytesW int64 // number of bytes written to underlying http response
	code int 
	headerWritten bool
}

func (t *responseWriter)  NumBytesWritten() int64 {
	return t.numBytesW
}

func (t *responseWriter) ResponseCode() int {
	if t.code <= 0 {
		return http.StatusOK
	}
	return t.code
}

func (t *responseWriter) ensureHeaderWritten() {
	if !t.headerWritten {
		t.w.WriteHeader(t.ResponseCode())
		t.headerWritten = true
		// println(">>>>> ensureHeaderWritten called")
	}
}

func (t *responseWriter) Write(b []byte) (i int, err error) {
	t.ensureHeaderWritten()
	i, err = t.w.Write(b)
	t.numBytesW += int64(i)
	// println(">>>> bytes written:", i, ", total:", t.numBytesW)
	return i, err
}

func (t *responseWriter) Flush() {
	// Flushing MUST be called by folks who use buffers internally,
	// at the end of their pipes.
	// Others SHOULD NOT call Flush, else they inadvertently cause
	// the Writer to be committed prematurely.
	// Flush SHOULD be called only after calling Pipeline.Next.
	t.ensureHeaderWritten()
	if wt, ok := t.w.(http.Flusher); ok {
		wt.Flush()
	}
}

func (t *responseWriter) CloseNotify() <-chan bool {
	if wt, ok := t.w.(http.CloseNotifier); ok {
		return wt.CloseNotify()
	}
	return nil
}

func (t *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if wt, ok := t.w.(http.Hijacker); ok {
		return wt.Hijack()
	}
	return nil, nil, errors.New("Hijack is not supported")
}

func (t *responseWriter) Header() http.Header {
	return t.w.Header()
}

func (t *responseWriter) WriteHeader(code int) {
	// debug.PrintStack()
	if t.headerWritten {
		return
	}
	// header is only written during a real Write.
	// This way, the response is not committed till everyone on the pipeline
	// has had a chance to manipulate the headers. One point on the pipeline
	// cannot just commit.
	// The Server does a final Write(nil) to ensure that the headers are really
	// committed, and then a final Flush.
	// t.w.WriteHeader(code)
	t.code = code
}

func (t *responseWriter) IsHeaderWritten() bool {
	return t.headerWritten
}

// This is needed for sendfile optimization.
// However, with gzip (which is typical), this is not even used at all.
// So comment out for now.
// func (t *responseWriter) ReadFrom(src io.Reader) (n int64, err error) {
// 	if t.code <= 0 {
// 		t.code = http.StatusOK
// 	}
// 	// supports sendfile optimization in underlying http.responseWriter impl
// 	n, err = io.Copy(t.w, src)
// 	t.numBytesW += n
// 	return
// }

