package wrr

import (
	"container/heap"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/log"
)

type namedHandler struct {
	http.Handler
	name     string
	weight   float64
	deadline float64
}

type stickyCookie struct {
	name     string
	secure   bool
	httpOnly bool
}

// Balancer is a WeightedRoundRobin load balancer based on Earliest Deadline First (EDF).
// (https://en.wikipedia.org/wiki/Earliest_deadline_first_scheduling)
// Each pick from the schedule has the earliest deadline entry selected.
// Entries have deadlines set at currentDeadline + 1 / weight,
// providing weighted round robin behavior with floating point weights and an O(log n) pick time.
type Balancer struct {
	stickyCookie *stickyCookie

	mutex       sync.RWMutex
	handlers    []*namedHandler
	curDeadline float64
	// status describes the condition of each child service of this load-balancer.
	// It is keyed by name of the service, and the value is true for "up", and false
	// for "down". Eeach service is considered "up" by default on creation.
	status   map[string]bool
	updaters []func(bool)
	// statusc is used to trigger a health/status check of Balancer (actually of all
	// the balancers sharing the same config), whenever the status of one of its
	// children services changes.
	statusc chan struct{}
}

// New creates a new load balancer.
func New(sticky *dynamic.Sticky) *Balancer {
	balancer := &Balancer{
		status: make(map[string]bool),
		// TODO(mpl): haven't initialized updaters on purpose here. think about it harder.
	}
	if sticky != nil && sticky.Cookie != nil {
		balancer.stickyCookie = &stickyCookie{
			name:     sticky.Cookie.Name,
			secure:   sticky.Cookie.Secure,
			httpOnly: sticky.Cookie.HTTPOnly,
		}
	}
	return balancer
}

// Len implements heap.Interface/sort.Interface.
func (b *Balancer) Len() int { return len(b.handlers) }

// Less implements heap.Interface/sort.Interface.
func (b *Balancer) Less(i, j int) bool {
	return b.handlers[i].deadline < b.handlers[j].deadline
}

// Swap implements heap.Interface/sort.Interface.
func (b *Balancer) Swap(i, j int) {
	b.handlers[i], b.handlers[j] = b.handlers[j], b.handlers[i]
}

// Push implements heap.Interface for pushing an item into the heap.
func (b *Balancer) Push(x interface{}) {
	h, ok := x.(*namedHandler)
	if !ok {
		return
	}

	b.handlers = append(b.handlers, h)
}

// Pop implements heap.Interface for poping an item from the heap.
// It panics if b.Len() < 1.
func (b *Balancer) Pop() interface{} {
	h := b.handlers[len(b.handlers)-1]
	b.handlers = b.handlers[0 : len(b.handlers)-1]
	return h
}

// Statusc returns the channel used to propagate status to the healthchecker.
func (b *Balancer) Statusc() chan struct{} {
	return b.statusc
}

// TODO(mpl): optimize, either by having b.status actually only contain the up
// ones, or by storing the whole state of b in a new field.
func (b *Balancer) isUP() bool {
	for _, up := range b.status {
		if up {
			return true
		}
	}
	return false
}

// TODO(mpl): doc, and remove parent (even though convenient for debugging).
func (b *Balancer) SetStatus(parentName, name string, up bool) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if _, ok := b.status[name]; !ok {
		return
	}

	upBefore := b.isUP()

	log.WithoutContext().Debugf("Setting status of %s to %v on %s", name, up, parentName)
	b.status[name] = up

	if !upBefore {
		if !up {
			// We're still down, no need to propagate
			log.WithoutContext().Debugf("No need to propagate that %s is still DOWN", parentName)
			return
		}
		// propagate upwards that we are now UP
		log.WithoutContext().Debugf("And propagating that status of %s is now UP", parentName)
		for _, fn := range b.updaters {
			fn(true)
		}
		return
	}

	if b.isUP() {
		// we were up before and we still are, no need to propagate
		log.WithoutContext().Debugf("No need to propagate that %s is still UP", parentName)
		return
	}
	// propagate upwards that we are now DOWN
	log.WithoutContext().Debugf("And propagating that status of %s is now DOWN", parentName)
	for _, fn := range b.updaters {
		fn(false)
	}
}

// TODO(mpl): doc. specify not guarded.
func (b *Balancer) RegisterStatusUpdater(fn func(up bool)) {
	b.updaters = append(b.updaters, fn)
}

var errNoAvailableServer = errors.New("no available server")

func (b *Balancer) nextServer() (*namedHandler, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	// TODO(mpl): maybe also use len(b.status) as fast fail sentinel
	// It all depends on whether we want a different diagnostic for "no servers" VS "all servers are down".
	if len(b.handlers) == 0 {
		return nil, fmt.Errorf("no servers in the pool")
	}
	up := false
	for _, h := range b.handlers {
		if b.status[h.name] {
			up = true
			break
		}
	}
	if !up {
		return nil, errNoAvailableServer
	}

	var handler *namedHandler
	for {
		// Pick handler with closest deadline.
		handler = heap.Pop(b).(*namedHandler)

		// curDeadline should be handler's deadline so that new added entry would have a fair competition environment with the old ones.
		b.curDeadline = handler.deadline
		handler.deadline += 1 / handler.weight

		heap.Push(b, handler)
		if b.status[handler.name] {
			break
		}
	}

	log.WithoutContext().Debugf("Service selected by WRR: %s", handler.name)
	return handler, nil
}

func (b *Balancer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if b.stickyCookie != nil {
		cookie, err := req.Cookie(b.stickyCookie.name)

		if err != nil && !errors.Is(err, http.ErrNoCookie) {
			log.WithoutContext().Warnf("Error while reading cookie: %v", err)
		}

		if err == nil && cookie != nil {
			for _, handler := range b.handlers {
				if handler.name != cookie.Value {
					continue
				}
				b.mutex.RLock()
				up := b.status[handler.name]
				b.mutex.RUnlock()
				if !up {
					continue
				}
				handler.ServeHTTP(w, req)
				return
			}
		}
	}

	server, err := b.nextServer()
	if err != nil {
		if errors.Is(err, errNoAvailableServer) {
			http.Error(w, errNoAvailableServer.Error(), http.StatusServiceUnavailable)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if b.stickyCookie != nil {
		cookie := &http.Cookie{Name: b.stickyCookie.name, Value: server.name, Path: "/", HttpOnly: b.stickyCookie.httpOnly, Secure: b.stickyCookie.secure}
		http.SetCookie(w, cookie)
	}

	server.ServeHTTP(w, req)
}

// AddService adds a handler.
// A handler with a non-positive weight is ignored.
func (b *Balancer) AddService(name string, handler http.Handler, weight *int) {
	w := 1
	if weight != nil {
		w = *weight
	}
	if w <= 0 { // non-positive weight is meaningless
		return
	}

	h := &namedHandler{Handler: handler, name: name, weight: float64(w)}

	b.mutex.Lock()
	h.deadline = b.curDeadline + 1/h.weight
	heap.Push(b, h)
	b.status[name] = true
	b.mutex.Unlock()
}
