package web

import (
	"bufio"
	"compress/gzip"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"

	"github.com/ugorji/go-common/pool"
)

const doUsePoolLocks = true
const doGzipResp = true

// Pipe is a step in handling a web request.
// A Pipe can do some work, call the next pipe in the chain,
// and finish up its work when that returns.
//
// Note that Pipes should still be smart about when to call Flush.
// The general guideline is that Pipes that do internally buffering
// should call Flush at the end of their ServeHttpPipe (after calling Pipeline.Next).
type Pipe interface {
	ServeHttpPipe(w ResponseWriter, r *http.Request, f *Pipeline)
}

// Pipeline contains a sequence of pipes, which work together to handle a request.
// You can have an accesslogger, security checker, gzipper, etc in the pipeline.
type Pipeline struct {
	s []Pipe
	i int
}

func NewPipeline(pipes ...Pipe) *Pipeline {
	return &Pipeline{s: pipes}
}

func (p *Pipeline) Next(w ResponseWriter, r *http.Request) {
	if p.i >= len(p.s) {
		return
	}
	x := p.s[p.i]
	p.i++
	// log.Debug(nil, "Calling Pipe: %T", x)
	x.ServeHttpPipe(w, r, p)
}

// Some pipes should not be closed because a pipeline is closing.
// The pipeline did not open them, and shouldn't manage their lifecycle.
// func (p *Pipeline) Close() error {
// 	var merr errorutil.Multi
// 	for _, p := range p.s {
// 		if c, ok := p.(io.Closer); ok {
// 			merr = append(merr, c.Close())
// 		}
// 	}
// 	return merr.NonNilError()
// }

//--------------------------------------

// Wraps a standard http.Handler into a Pipe.
// It calls its ServeHTTP, and then calls pipeline's next.
type HttpHandlerPipe struct {
	http.Handler
}

// ServeHttpPipe calls the wrapped Handlers ServeHTTP, calls pipeline's next.
func (h HttpHandlerPipe) ServeHttpPipe(w ResponseWriter, r *http.Request, f *Pipeline) {
	h.ServeHTTP(w, r)
	f.Next(w, r)
}

//--------------------------------------

// content type may be in form: text/html; charset = utf-8
// Note that net/http already uses a 4K buffer for request and response.
var gzipTypes = regexp.MustCompile(`^text/|(json|javascript|plain|html|css|xml)`)

type gzipWriter struct {
	started bool
	gw      *gzip.Writer
	s       *GzipPipe
	ResponseWriter
}

func (t *gzipWriter) Write(b []byte) (i int, err error) {
	// defer func() { println(">>>> gzipWriter: ", i) }()
	if doGzipResp && !t.started {
		t.started = true
		// If someone lower on the chain already set a Content-Encoding,
		// then we should do be a pass-through and do no compression.
		// This way, a lower player can do some caching and start serving
		// the compressed content before-hand.
		cEnc := t.Header().Get("Content-Encoding")
		if cEnc == "" {
			ctype := t.Header().Get("Content-Type")
			if ctype == "" {
				ctype = http.DetectContentType(b)
				t.Header().Set("Content-Type", ctype)
			}
			if gzipTypes.MatchString(ctype) {
				t.Header().Set("Content-Encoding", "gzip")
				t.gw = pool.Must(t.s.pool.Get(0)).(*gzip.Writer)
				t.gw.Reset(t.ResponseWriter)
			}
		} else {
			log.Debug(nil, "gzipWriter: skipping gzip. Content-Encoding already set.")
		}
	}
	if t.gw != nil {
		i, err = t.gw.Write(b)
	} else {
		i, err = t.ResponseWriter.Write(b)
	}
	return i, err
}

func (t *gzipWriter) Flush() {
	if t.gw != nil {
		log.Error2(nil, t.gw.Flush(), "Error flushing gzipWriter")
	}
	t.ResponseWriter.Flush()
}

func (t *gzipWriter) Close() (err error) {
	if t.gw != nil {
		err = t.gw.Close()
		t.s.pool.Put(t.gw)
	}
	return
}

// GzipPipe will convert http responseWriter to write out the compressed
// bits if accept-encoding contains gzip, and deciphered content-type
// is text/* or matches typical text types (xml, html, json, javascript, css).
type GzipPipe struct {
	level   int
	poolCap int
	pool    *pool.T
}

func NewGzipPipe(level, initPoolLen, poolCap int) (s *GzipPipe) {
	s = &GzipPipe{poolCap: poolCap, level: level}
	if !doGzipResp {
		return
	}
	switch {
	case level == gzip.DefaultCompression,
		level >= gzip.BestSpeed && level <= gzip.BestCompression:
	default:
		level = gzip.DefaultCompression
	}
	s.level = level
	s.pool = NewGzipWriterPool(level, initPoolLen, poolCap)
	return
}

func NewGzipWriterPool(level, initPoolLen, poolCap int) *pool.T {
	fn := func(v interface{}, a pool.Action, l int) (v2 interface{}, err error) {
		v2 = v
		switch a {
		case pool.GET:
			if v == nil {
				v2, _ = gzip.NewWriterLevel(ioutil.Discard, level)
			}
		case pool.PUT:
			if bv, ok := v.(*gzip.Writer); ok {
				bv.Reset(ioutil.Discard)
			}
			if l >= poolCap {
				v2 = nil
			}
		case pool.DISPOSE:
			if bv, ok := v.(*gzip.Writer); ok {
				log.Error2(nil, bv.Close(), "Error closing gzip Writer")
			}
		}
		return
	}
	p, _ := pool.New(fn, initPoolLen, poolCap)
	return p
}

func (s *GzipPipe) ServeHttpPipe(w ResponseWriter, r *http.Request, f *Pipeline) {
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		f.Next(w, r)
		return
	}
	w2 := &gzipWriter{ResponseWriter: w, s: s}
	f.Next(w2, r)
	w2.Flush() // gzip needs to close itself, so it must flush.
	log.Error2(nil, w2.Close(), "Error closing gzipWriter")
}

//--------------------------------------

type bufWriter struct {
	b *bufio.Writer
	s *BufferPipe
	ResponseWriter
}

func (t *bufWriter) Write(b []byte) (i int, err error) {
	// defer func() { println(">>>> bufWriter: ", i) }()
	return t.b.Write(b)
}

func (t *bufWriter) Flush() {
	log.Error2(nil, t.b.Flush(), "Error flushing bufWriter")
	t.ResponseWriter.Flush()
}

func (t *bufWriter) Close() (err error) {
	err = t.b.Flush()
	t.s.pool.Put(t.b)
	return
}

// BufferPipe allows you use a Buffer, especially close to the lowest
// handler, to ensure that handlers up the chain do not get results
// in tiny quantities.
//
// Templates may write out results in bytes of length 1, 2, 17, etc.
// Some pipes may need to determine the content type before converting
// the result (e.g. gzip pipe). BufferPipe helps here.
type BufferPipe struct {
	size    int
	poolCap int
	pool    *pool.T
}

func NewBufferPipe(size, initPoolLen, poolCap int) (s *BufferPipe) {
	s = &BufferPipe{size: size, poolCap: poolCap}
	fn := func(v interface{}, a pool.Action, l int) (v2 interface{}, err error) {
		v2 = v
		switch a {
		case pool.GET:
			if v == nil {
				v2 = bufio.NewWriterSize(ioutil.Discard, s.size)
			}
		case pool.PUT:
			if bv, ok := v.(*bufio.Writer); ok {
				bv.Reset(ioutil.Discard)
			}
		}
		return
	}
	s.pool, _ = pool.New(fn, initPoolLen, poolCap)
	return
}

func (s *BufferPipe) ServeHttpPipe(w ResponseWriter, r *http.Request, f *Pipeline) {
	bw := pool.Must(s.pool.Get(0)).(*bufio.Writer)
	bw.Reset(w)
	w2 := &bufWriter{b: bw, s: s, ResponseWriter: w}
	f.Next(w2, r)
	w2.Flush() // we use a buffer internally, so MUST flush
	log.Error2(nil, w2.Close(), "Error closing bufWriter")
}

//--------------------------------------

// Flusher pipe just does a flush after calling Next.
// May be a good candidate to put on the end of the pipeline closest.
// This way, it calls Flush across the whole pipeline chain.
type FlusherPipe struct{}

func (s FlusherPipe) ServeHttpPipe(w ResponseWriter, r *http.Request, f *Pipeline) {
	f.Next(w, r)
	w.Flush()
}
