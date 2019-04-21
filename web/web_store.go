/*
WEBSTORE

WebStore allows us store information along with a namespace.
It simply maintains a non-synchronized map[interface{}] map[string]interface{}
for each namespace. It expects that the namespace represents usage
within a single goroutine.

An example usage is within a web environment where the namespace can be the
*http.Request object.

It is generally recommended to pass structures around in function
calls. However, there may be times where using this is the only option,
and in those situations, use with care.

It can be used in a web environment, where you want to store data on behalf of a
request. In this context, the namespace is the request.
*/
package web

import (
	"sync"
	"github.com/ugorji/go-common/safestore"
)

//var Global *WebStore = New()

type WebStore struct {
	lock sync.RWMutex
	// use a interface{} here as the key (so that this SafeStore can be used anywhere)
	container map[interface{}]*safestore.T
}

func NewWebStore() *WebStore {
	return &WebStore{container: make(map[interface{}]*safestore.T)}
}

func (rc *WebStore) Get(ns interface{}, key string) interface{} {
	if rq := rc.rq(ns); rq != nil {
		return rq.Get(key)
	}
	return nil
}

func (rc *WebStore) Put(ns interface{}, key string, val interface{}) {
	rc.rqNonNil(ns).Put(key, val, 0)
}

// Ensure this clear method is called at the appropriate time to clear
// out the namespace entries.
//
// For example, in a web context, where information is stored on behalf
// of the request, ensure this method is called at the end of the request.
func (rc *WebStore) Clear(ns interface{}) {
	rc.lock.Lock()
	defer rc.lock.Unlock()
	delete(rc.container, ns)
}

func (rc *WebStore) rq(ns interface{}) *safestore.T {
	// Note: num or locks/unlocks should match, even with the defer calls, else we'd hang
	rc.lock.RLock()
	defer rc.lock.RUnlock()
	return rc.container[ns]
}

func (rc *WebStore) rqNonNil(ns interface{}) *safestore.T {
	// Note: num or locks/unlocks should match,
	//       even with the defer calls, else we'd hang
	rc.lock.Lock()
	defer rc.lock.Unlock()
	rq := rc.container[ns]
	if rq == nil {
		rq = safestore.New(false)
		rc.container[ns] = rq
	}
	return rq
}
