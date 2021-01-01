package wrr

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/traefik/traefik/v2/pkg/config/dynamic"
)

func Int(v int) *int { return &v }

type responseRecorder struct {
	*httptest.ResponseRecorder
	save     map[string]int
	sequence []string
	status   []int
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.save[r.Header().Get("server")]++
	r.sequence = append(r.sequence, r.Header().Get("server"))
	r.status = append(r.status, statusCode)
	r.ResponseRecorder.WriteHeader(statusCode)
}

func TestBalancer(t *testing.T) {
	balancer := New(nil)

	balancer.AddService("first", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "first")
		rw.WriteHeader(http.StatusOK)
	}), Int(3))

	balancer.AddService("second", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "second")
		rw.WriteHeader(http.StatusOK)
	}), Int(1))

	recorder := &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}
	for i := 0; i < 4; i++ {
		balancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}

	assert.Equal(t, 3, recorder.save["first"])
	assert.Equal(t, 1, recorder.save["second"])
}

func TestBalancerNoService(t *testing.T) {
	balancer := New(nil)

	recorder := httptest.NewRecorder()
	balancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusInternalServerError, recorder.Result().StatusCode)
}

func TestBalancerOneServerZeroWeight(t *testing.T) {
	balancer := New(nil)

	balancer.AddService("first", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "first")
		rw.WriteHeader(http.StatusOK)
	}), Int(1))

	balancer.AddService("second", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {}), Int(0))

	recorder := &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}
	for i := 0; i < 3; i++ {
		balancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}

	assert.Equal(t, 3, recorder.save["first"])
}

func TestBalancerNoServiceUp(t *testing.T) {
	balancer := New(nil)

	balancer.AddService("first", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
	}), Int(1))

	balancer.AddService("second", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
	}), Int(1))

	balancer.SetStatus("first", false, false)
	balancer.SetStatus("second", false, false)

	recorder := httptest.NewRecorder()
	balancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Result().StatusCode)
}

func TestBalancerOneServerDown(t *testing.T) {
	balancer := New(nil)

	balancer.AddService("first", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "first")
		rw.WriteHeader(http.StatusOK)
	}), Int(1))

	balancer.AddService("second", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
	}), Int(1))
	balancer.SetStatus("second", false, false)

	recorder := &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}
	for i := 0; i < 3; i++ {
		balancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}

	assert.Equal(t, 3, recorder.save["first"])
}

func TestBalancerDownThenUp(t *testing.T) {
	balancer := New(nil)

	balancer.AddService("first", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "first")
		rw.WriteHeader(http.StatusOK)
	}), Int(1))

	balancer.AddService("second", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "second")
		rw.WriteHeader(http.StatusOK)
	}), Int(1))
	balancer.SetStatus("second", false, false)

	recorder := &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}
	for i := 0; i < 3; i++ {
		balancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}
	assert.Equal(t, 3, recorder.save["first"])

	balancer.SetStatus("second", true, false)
	recorder = &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}
	for i := 0; i < 2; i++ {
		balancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}
	assert.Equal(t, 1, recorder.save["first"])
	assert.Equal(t, 1, recorder.save["second"])
}

func TestBalancerPropagate(t *testing.T) {
	balancer1 := New(nil)

	balancer1.AddService("first", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "first")
		rw.WriteHeader(http.StatusOK)
	}), Int(1))
	balancer1.AddService("second", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "second")
		rw.WriteHeader(http.StatusOK)
	}), Int(1))

	balancer2 := New(nil)
	balancer2.AddService("third", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "third")
		rw.WriteHeader(http.StatusOK)
	}), Int(1))
	balancer2.AddService("fourth", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "fourth")
		rw.WriteHeader(http.StatusOK)
		// rw.WriteHeader(http.StatusInternalServerError)
	}), Int(1))

	topBalancer := New(nil)
	topBalancer.AddService("balancer1", balancer1, Int(1))
	topBalancer.AddService("balancer2", balancer2, Int(1))

	recorder := &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}
	for i := 0; i < 8; i++ {
		topBalancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}
	assert.Equal(t, 2, recorder.save["first"])
	assert.Equal(t, 2, recorder.save["second"])
	assert.Equal(t, 2, recorder.save["third"])
	assert.Equal(t, 2, recorder.save["fourth"])
	wantStatus := []int{200, 200, 200, 200, 200, 200, 200, 200}
	assert.Equal(t, wantStatus, recorder.status)

	// mocking of the healthcheck goroutine.
	go func() {
		// wait for the two children of balancer2 to get downed.
		for i := 0; i < 2; i++ {
			tm := time.NewTimer(5 * time.Second)
			select {
			case <-balancer2.statusc:
				down := true
				for _, v := range balancer2.status {
					if v {
						down = false
						break
					}
				}
				if down {
					topBalancer.SetStatus("balancer2", false, false)
				}
			case <-tm.C:
				t.Errorf("timeout")
			}
			if !tm.Stop() {
				<-tm.C
			}
		}
		// one more time to force main thread to wait for the propagation to be done,
		// before it checks the state of the tree.
		tm := time.NewTimer(5 * time.Second)
		select {
		case balancer2.statusc <- struct{}{}:
		case <-tm.C:
			t.Errorf("timeout")
		}
		if !tm.Stop() {
			<-tm.C
		}
	}()

	// fourth gets downed, but balancer2 still up since third is still up.
	balancer2.SetStatus("fourth", false, true)
	recorder = &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}
	for i := 0; i < 8; i++ {
		topBalancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}
	assert.Equal(t, 2, recorder.save["first"])
	assert.Equal(t, 2, recorder.save["second"])
	assert.Equal(t, 4, recorder.save["third"])
	assert.Equal(t, 0, recorder.save["fourth"])
	wantStatus = []int{200, 200, 200, 200, 200, 200, 200, 200}
	assert.Equal(t, wantStatus, recorder.status)

	// third gets downed, and the propagation triggers balancer2 to be marked as
	// down as well for topBalancer.
	balancer2.SetStatus("third", false, true)
	// Not a real status signal, we just use statusc to synchronize before going on.
	<-balancer2.statusc
	recorder = &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}
	for i := 0; i < 8; i++ {
		topBalancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}
	assert.Equal(t, 4, recorder.save["first"])
	assert.Equal(t, 4, recorder.save["second"])
	assert.Equal(t, 0, recorder.save["third"])
	assert.Equal(t, 0, recorder.save["fourth"])
	wantStatus = []int{200, 200, 200, 200, 200, 200, 200, 200}
	assert.Equal(t, wantStatus, recorder.status)
}

func TestBalancerAllServersZeroWeight(t *testing.T) {
	balancer := New(nil)

	balancer.AddService("test", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {}), Int(0))
	balancer.AddService("test2", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {}), Int(0))

	recorder := httptest.NewRecorder()
	balancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusInternalServerError, recorder.Result().StatusCode)
}

func TestSticky(t *testing.T) {
	balancer := New(&dynamic.Sticky{
		Cookie: &dynamic.Cookie{Name: "test"},
	})

	balancer.AddService("first", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "first")
		rw.WriteHeader(http.StatusOK)
	}), Int(1))

	balancer.AddService("second", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "second")
		rw.WriteHeader(http.StatusOK)
	}), Int(2))

	recorder := &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for i := 0; i < 3; i++ {
		for _, cookie := range recorder.Result().Cookies() {
			req.AddCookie(cookie)
		}
		recorder.ResponseRecorder = httptest.NewRecorder()

		balancer.ServeHTTP(recorder, req)
	}

	assert.Equal(t, 0, recorder.save["first"])
	assert.Equal(t, 3, recorder.save["second"])
}

// TestBalancerBias makes sure that the WRR algorithm spreads elements evenly right from the start,
// and that it does not "over-favor" the high-weighted ones with a biased start-up regime.
func TestBalancerBias(t *testing.T) {
	balancer := New(nil)

	balancer.AddService("first", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "A")
		rw.WriteHeader(http.StatusOK)
	}), Int(11))

	balancer.AddService("second", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", "B")
		rw.WriteHeader(http.StatusOK)
	}), Int(3))

	recorder := &responseRecorder{ResponseRecorder: httptest.NewRecorder(), save: map[string]int{}}

	for i := 0; i < 14; i++ {
		balancer.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	}

	wantSequence := []string{"A", "A", "A", "B", "A", "A", "A", "A", "B", "A", "A", "A", "B", "A"}

	assert.Equal(t, wantSequence, recorder.sequence)
}
