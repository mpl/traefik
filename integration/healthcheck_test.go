package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-check/check"
	"github.com/traefik/traefik/v2/integration/try"
	checker "github.com/vdemeester/shakers"
)

// HealthCheck test suites (using libcompose).
type HealthCheckSuite struct {
	BaseSuite
	whoami1IP string
	whoami2IP string
	whoami3IP string
	whoami4IP string
}

func (s *HealthCheckSuite) SetUpSuite(c *check.C) {
	s.createComposeProject(c, "healthcheck")
	s.composeProject.Start(c)

	s.whoami1IP = s.composeProject.Container(c, "whoami1").NetworkSettings.IPAddress
	s.whoami2IP = s.composeProject.Container(c, "whoami2").NetworkSettings.IPAddress
	s.whoami3IP = s.composeProject.Container(c, "whoami3").NetworkSettings.IPAddress
	s.whoami4IP = s.composeProject.Container(c, "whoami4").NetworkSettings.IPAddress
}

func (s *HealthCheckSuite) TestSimpleConfiguration(c *check.C) {
	file := s.adaptFile(c, "fixtures/healthcheck/simple.toml", struct {
		Server1 string
		Server2 string
	}{s.whoami1IP, s.whoami2IP})
	defer os.Remove(file)

	cmd, display := s.traefikCmd(withConfigFile(file))
	defer display(c)
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer s.killCmd(cmd)

	// wait for traefik
	err = try.GetRequest("http://127.0.0.1:8080/api/rawdata", 60*time.Second, try.BodyContains("Host(`test.localhost`)"))
	c.Assert(err, checker.IsNil)

	frontendHealthReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000/health", nil)
	c.Assert(err, checker.IsNil)
	frontendHealthReq.Host = "test.localhost"

	err = try.Request(frontendHealthReq, 500*time.Millisecond, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)

	// Fix all whoami health to 500
	client := &http.Client{}
	whoamiHosts := []string{s.whoami1IP, s.whoami2IP}
	for _, whoami := range whoamiHosts {
		statusInternalServerErrorReq, err := http.NewRequest(http.MethodPost, "http://"+whoami+"/health", bytes.NewBuffer([]byte("500")))
		c.Assert(err, checker.IsNil)
		_, err = client.Do(statusInternalServerErrorReq)
		c.Assert(err, checker.IsNil)
	}

	// Verify no backend service is available due to failing health checks
	err = try.Request(frontendHealthReq, 3*time.Second, try.StatusCodeIs(http.StatusServiceUnavailable))
	c.Assert(err, checker.IsNil)

	// Change one whoami health to 200
	statusOKReq1, err := http.NewRequest(http.MethodPost, "http://"+s.whoami1IP+"/health", bytes.NewBuffer([]byte("200")))
	c.Assert(err, checker.IsNil)
	_, err = client.Do(statusOKReq1)
	c.Assert(err, checker.IsNil)

	// Verify frontend health : after
	err = try.Request(frontendHealthReq, 3*time.Second, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)

	frontendReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000/", nil)
	c.Assert(err, checker.IsNil)
	frontendReq.Host = "test.localhost"

	// Check if whoami1 responds
	err = try.Request(frontendReq, 500*time.Millisecond, try.BodyContains(s.whoami1IP))
	c.Assert(err, checker.IsNil)

	// Check if the service with bad health check (whoami2) never respond.
	err = try.Request(frontendReq, 2*time.Second, try.BodyContains(s.whoami2IP))
	c.Assert(err, checker.Not(checker.IsNil))

	// TODO validate : run on 80
	resp, err := http.Get("http://127.0.0.1:8000/")

	// Expected a 404 as we did not configure anything
	c.Assert(err, checker.IsNil)
	c.Assert(resp.StatusCode, checker.Equals, http.StatusNotFound)
}

func (s *HealthCheckSuite) TestMultipleEntrypoints(c *check.C) {
	file := s.adaptFile(c, "fixtures/healthcheck/multiple-entrypoints.toml", struct {
		Server1 string
		Server2 string
	}{s.whoami1IP, s.whoami2IP})
	defer os.Remove(file)

	cmd, display := s.traefikCmd(withConfigFile(file))
	defer display(c)
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer s.killCmd(cmd)

	// Wait for traefik
	err = try.GetRequest("http://localhost:8080/api/rawdata", 60*time.Second, try.BodyContains("Host(`test.localhost`)"))
	c.Assert(err, checker.IsNil)

	// Check entrypoint http1
	frontendHealthReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000/health", nil)
	c.Assert(err, checker.IsNil)
	frontendHealthReq.Host = "test.localhost"

	err = try.Request(frontendHealthReq, 500*time.Millisecond, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)

	// Check entrypoint http2
	frontendHealthReq, err = http.NewRequest(http.MethodGet, "http://127.0.0.1:9000/health", nil)
	c.Assert(err, checker.IsNil)
	frontendHealthReq.Host = "test.localhost"

	err = try.Request(frontendHealthReq, 500*time.Millisecond, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)

	// Set the both whoami health to 500
	client := &http.Client{}
	whoamiHosts := []string{s.whoami1IP, s.whoami2IP}
	for _, whoami := range whoamiHosts {
		statusInternalServerErrorReq, err := http.NewRequest(http.MethodPost, "http://"+whoami+"/health", bytes.NewBuffer([]byte("500")))
		c.Assert(err, checker.IsNil)
		_, err = client.Do(statusInternalServerErrorReq)
		c.Assert(err, checker.IsNil)
	}

	// Verify no backend service is available due to failing health checks
	err = try.Request(frontendHealthReq, 5*time.Second, try.StatusCodeIs(http.StatusServiceUnavailable))
	c.Assert(err, checker.IsNil)

	// reactivate the whoami2
	statusInternalServerOkReq, err := http.NewRequest(http.MethodPost, "http://"+s.whoami2IP+"/health", bytes.NewBuffer([]byte("200")))
	c.Assert(err, checker.IsNil)
	_, err = client.Do(statusInternalServerOkReq)
	c.Assert(err, checker.IsNil)

	frontend1Req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000/", nil)
	c.Assert(err, checker.IsNil)
	frontend1Req.Host = "test.localhost"

	frontend2Req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:9000/", nil)
	c.Assert(err, checker.IsNil)
	frontend2Req.Host = "test.localhost"

	// Check if whoami1 never responds
	err = try.Request(frontend2Req, 2*time.Second, try.BodyContains(s.whoami1IP))
	c.Assert(err, checker.NotNil)

	// Check if whoami1 never responds
	err = try.Request(frontend1Req, 2*time.Second, try.BodyContains(s.whoami1IP))
	c.Assert(err, checker.NotNil)
}

func (s *HealthCheckSuite) TestPortOverload(c *check.C) {
	// Set one whoami health to 200
	client := &http.Client{}
	statusInternalServerErrorReq, err := http.NewRequest(http.MethodPost, "http://"+s.whoami1IP+"/health", bytes.NewBuffer([]byte("200")))
	c.Assert(err, checker.IsNil)
	_, err = client.Do(statusInternalServerErrorReq)
	c.Assert(err, checker.IsNil)

	file := s.adaptFile(c, "fixtures/healthcheck/port_overload.toml", struct {
		Server1 string
	}{s.whoami1IP})
	defer os.Remove(file)

	cmd, display := s.traefikCmd(withConfigFile(file))
	defer display(c)
	err = cmd.Start()
	c.Assert(err, checker.IsNil)
	defer s.killCmd(cmd)

	// wait for traefik
	err = try.GetRequest("http://127.0.0.1:8080/api/rawdata", 10*time.Second, try.BodyContains("Host(`test.localhost`)"))
	c.Assert(err, checker.IsNil)

	frontendHealthReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000/health", nil)
	c.Assert(err, checker.IsNil)
	frontendHealthReq.Host = "test.localhost"

	// We test bad gateway because we use an invalid port for the backend
	err = try.Request(frontendHealthReq, 500*time.Millisecond, try.StatusCodeIs(http.StatusBadGateway))
	c.Assert(err, checker.IsNil)

	// Set one whoami health to 500
	statusInternalServerErrorReq, err = http.NewRequest(http.MethodPost, "http://"+s.whoami1IP+"/health", bytes.NewBuffer([]byte("500")))
	c.Assert(err, checker.IsNil)
	_, err = client.Do(statusInternalServerErrorReq)
	c.Assert(err, checker.IsNil)

	// Verify no backend service is available due to failing health checks
	err = try.Request(frontendHealthReq, 3*time.Second, try.StatusCodeIs(http.StatusServiceUnavailable))
	c.Assert(err, checker.IsNil)
}

// Checks if all the loadbalancers created will correctly update the server status.
func (s *HealthCheckSuite) TestMultipleRoutersOnSameService(c *check.C) {
	file := s.adaptFile(c, "fixtures/healthcheck/multiple-routers-one-same-service.toml", struct {
		Server1 string
	}{s.whoami1IP})
	defer os.Remove(file)

	cmd, display := s.traefikCmd(withConfigFile(file))
	defer display(c)
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer s.killCmd(cmd)

	// wait for traefik
	err = try.GetRequest("http://127.0.0.1:8080/api/rawdata", 60*time.Second, try.BodyContains("Host(`test.localhost`)"))
	c.Assert(err, checker.IsNil)

	// Set whoami health to 200 to be sure to start with the wanted status
	client := &http.Client{}
	statusOkReq, err := http.NewRequest(http.MethodPost, "http://"+s.whoami1IP+"/health", bytes.NewBuffer([]byte("200")))
	c.Assert(err, checker.IsNil)
	_, err = client.Do(statusOkReq)
	c.Assert(err, checker.IsNil)

	// check healthcheck on web1 entrypoint
	healthReqWeb1, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000/health", nil)
	c.Assert(err, checker.IsNil)
	healthReqWeb1.Host = "test.localhost"
	err = try.Request(healthReqWeb1, 1*time.Second, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)

	// check healthcheck on web2 entrypoint
	healthReqWeb2, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:9000/health", nil)
	c.Assert(err, checker.IsNil)
	healthReqWeb2.Host = "test.localhost"

	err = try.Request(healthReqWeb2, 500*time.Millisecond, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)

	// Set whoami health to 500
	statusInternalServerErrorReq, err := http.NewRequest(http.MethodPost, "http://"+s.whoami1IP+"/health", bytes.NewBuffer([]byte("500")))
	c.Assert(err, checker.IsNil)
	_, err = client.Do(statusInternalServerErrorReq)
	c.Assert(err, checker.IsNil)

	// Verify no backend service is available due to failing health checks
	err = try.Request(healthReqWeb1, 3*time.Second, try.StatusCodeIs(http.StatusServiceUnavailable))
	c.Assert(err, checker.IsNil)

	err = try.Request(healthReqWeb2, 3*time.Second, try.StatusCodeIs(http.StatusServiceUnavailable))
	c.Assert(err, checker.IsNil)

	// Change one whoami health to 200
	statusOKReq1, err := http.NewRequest(http.MethodPost, "http://"+s.whoami1IP+"/health", bytes.NewBuffer([]byte("200")))
	c.Assert(err, checker.IsNil)
	_, err = client.Do(statusOKReq1)
	c.Assert(err, checker.IsNil)

	// Verify health check
	err = try.Request(healthReqWeb1, 3*time.Second, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)

	err = try.Request(healthReqWeb2, 3*time.Second, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)
}

func (s *HealthCheckSuite) TestPropagate(c *check.C) {
	file := s.adaptFile(c, "fixtures/healthcheck/propagate.toml", struct {
		Server1 string
		Server2 string
		Server3 string
		Server4 string
	}{s.whoami1IP, s.whoami2IP, s.whoami3IP, s.whoami4IP})
	defer os.Remove(file)

	cmd, display := s.traefikCmd(withConfigFile(file))
	defer display(c)
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer s.killCmd(cmd)

	// wait for traefik
	err = try.GetRequest("http://127.0.0.1:8080/api/rawdata", 60*time.Second, try.BodyContains("Host(`root.localhost`)"))
	c.Assert(err, checker.IsNil)

	frontendHealthReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000/health", nil)
	c.Assert(err, checker.IsNil)
	frontendHealthReq.Host = "root.localhost"

	rootReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000", nil)
	c.Assert(err, checker.IsNil)
	rootReq.Host = "root.localhost"

	err = try.Request(rootReq, 500*time.Millisecond, try.StatusCodeIs(http.StatusOK))
	c.Assert(err, checker.IsNil)

	// Bring whoami1 and whoami3 down
	client := &http.Client{}
	whoamiHosts := []string{s.whoami1IP, s.whoami3IP}
	for _, whoami := range whoamiHosts {
		statusInternalServerErrorReq, err := http.NewRequest(http.MethodPost, "http://"+whoami+"/health", bytes.NewBuffer([]byte("500")))
		c.Assert(err, checker.IsNil)
		_, err = client.Do(statusInternalServerErrorReq)
		c.Assert(err, checker.IsNil)
	}

	time.Sleep(time.Second)

	// Verify load-balancing on root still works, and that we're getting wsp2, wsp4, wsp2, wsp4, etc.
	var want string
	for i := 0; i < 4; i++ {
		if i%2 == 0 {
			want = `IP: ` + s.whoami4IP
		} else {
			want = `IP: ` + s.whoami2IP
		}
		// N.B: using a Nanosecond here is important, because we actually want the request
		// to be tried only once. And we're too lazy not to use try.Request.
		err = try.Request(rootReq, time.Nanosecond, try.BodyContains(want))
		c.Assert(err, checker.IsNil)
	}

	fooReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000", nil)
	c.Assert(err, checker.IsNil)
	fooReq.Host = "foo.localhost"

	// Verify load-balancing on foo still works, and that we're getting wsp2, wsp2, wsp2, wsp2, etc.
	want = `IP: ` + s.whoami2IP
	for i := 0; i < 4; i++ {
		err = try.Request(fooReq, time.Nanosecond, try.BodyContains(want))
		c.Assert(err, checker.IsNil)
	}

	barReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000", nil)
	c.Assert(err, checker.IsNil)
	barReq.Host = "bar.localhost"

	// Verify load-balancing on bar still works, and that we're getting wsp2, wsp2, wsp2, wsp2, etc.
	want = `IP: ` + s.whoami2IP
	for i := 0; i < 4; i++ {
		err = try.Request(barReq, time.Nanosecond, try.BodyContains(want))
		c.Assert(err, checker.IsNil)
	}

	// Bring whoami2 and whoami4 down
	whoamiHosts = []string{s.whoami2IP, s.whoami4IP}
	for _, whoami := range whoamiHosts {
		statusInternalServerErrorReq, err := http.NewRequest(http.MethodPost, "http://"+whoami+"/health", bytes.NewBuffer([]byte("500")))
		c.Assert(err, checker.IsNil)
		_, err = client.Do(statusInternalServerErrorReq)
		c.Assert(err, checker.IsNil)
	}

	time.Sleep(time.Second)

	// Verify that everything is down, and that we get 503s everywhere.
	for i := 0; i < 2; i++ {
		err = try.Request(rootReq, time.Nanosecond, try.StatusCodeIs(http.StatusServiceUnavailable))
		c.Assert(err, checker.IsNil)
		err = try.Request(fooReq, time.Nanosecond, try.StatusCodeIs(http.StatusServiceUnavailable))
		c.Assert(err, checker.IsNil)
		err = try.Request(barReq, time.Nanosecond, try.StatusCodeIs(http.StatusServiceUnavailable))
		c.Assert(err, checker.IsNil)
	}

	// Bring everything back up.
	whoamiHosts = []string{s.whoami1IP, s.whoami2IP, s.whoami3IP, s.whoami4IP}
	for _, whoami := range whoamiHosts {
		statusOKReq, err := http.NewRequest(http.MethodPost, "http://"+whoami+"/health", bytes.NewBuffer([]byte("200")))
		c.Assert(err, checker.IsNil)
		_, err = client.Do(statusOKReq)
		c.Assert(err, checker.IsNil)
	}

	time.Sleep(time.Second)

	// Verify everything is up on root router.
	wantIPs := []string{s.whoami3IP, s.whoami1IP, s.whoami4IP, s.whoami2IP}
	for i := 0; i < 4; i++ {
		want := `IP: ` + wantIPs[i]
		err = try.Request(rootReq, time.Nanosecond, try.BodyContains(want))
		c.Assert(err, checker.IsNil)
	}

	// Verify everything is up on foo router.
	wantIPs = []string{s.whoami1IP, s.whoami1IP, s.whoami3IP, s.whoami2IP}
	for i := 0; i < 4; i++ {
		want := `IP: ` + wantIPs[i]
		err = try.Request(fooReq, time.Nanosecond, try.BodyContains(want))
		c.Assert(err, checker.IsNil)
	}

	// Verify everything is up on bar router.
	wantIPs = []string{s.whoami1IP, s.whoami1IP, s.whoami3IP, s.whoami2IP}
	for i := 0; i < 4; i++ {
		want := `IP: ` + wantIPs[i]
		err = try.Request(barReq, time.Nanosecond, try.BodyContains(want))
		c.Assert(err, checker.IsNil)
	}

	noHealthReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000", nil)
	c.Assert(err, checker.IsNil)
	noHealthReq.Host = "no.healthcheck.localhost"

	// Verify everything is up when the wsp service does not have a health check.
	wantIPs = []string{s.whoami1IP, s.whoami3IP, s.whoami2IP, s.whoami4IP}
	for _, ip := range wantIPs {
		want := `IP: ` + ip
		err = try.Request(noHealthReq, time.Nanosecond, try.BodyContains(want))
		c.Assert(err, checker.IsNil)
	}
}

func (s *HealthCheckSuite) TestPropagateReload(c *check.C) {
	// Setup a WSP service without the healthcheck enabled (wsp-service1)
	withoutHealthCheck := s.adaptFile(c, "fixtures/healthcheck/reload_without_healthcheck.toml", struct {
		Server1 string
		Server2 string
	}{s.whoami1IP, s.whoami2IP})
	defer os.Remove(withoutHealthCheck)
	withHealthCheck := s.adaptFile(c, "fixtures/healthcheck/reload_with_healthcheck.toml", struct {
		Server1 string
		Server2 string
	}{s.whoami1IP, s.whoami2IP})
	defer os.Remove(withHealthCheck)

	cmd, display := s.traefikCmd(withConfigFile(withoutHealthCheck))
	defer display(c)
	err := cmd.Start()
	c.Assert(err, checker.IsNil)
	defer s.killCmd(cmd)

	// wait for traefik
	err = try.GetRequest("http://127.0.0.1:8080/api/rawdata", 60*time.Second, try.BodyContains("Host(`root.localhost`)"))
	c.Assert(err, checker.IsNil)

	frontendHealthReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000/health", nil)
	c.Assert(err, checker.IsNil)
	frontendHealthReq.Host = "root.localhost"

	// Allow one of the underlying services on it to fail all servers HC (whoami2)
	client := &http.Client{}
	statusOKReq, err := http.NewRequest(http.MethodPost, "http://"+s.whoami2IP+"/health", bytes.NewBuffer([]byte("500")))
	c.Assert(err, checker.IsNil)
	_, err = client.Do(statusOKReq)
	c.Assert(err, checker.IsNil)

	rootReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000", nil)
	c.Assert(err, checker.IsNil)
	rootReq.Host = "root.localhost"

	// Check the failed service (whoami2) is getting requests, but answer 500
	err = try.Request(rootReq, 500*time.Millisecond, try.StatusCodeIs(http.StatusServiceUnavailable))
	c.Assert(err, checker.IsNil)

	// Enable the healthcheck on the root WSP (wsp-service1) and let Traefik reload the config
	fr1, err := os.OpenFile(withoutHealthCheck, os.O_APPEND|os.O_WRONLY, 0644)
	defer fr1.Close()
	c.Assert(fr1, checker.NotNil)
	c.Assert(err, checker.IsNil)
	err = fr1.Truncate(0)
	c.Assert(err, checker.IsNil)

	fr2, err := os.ReadFile(withHealthCheck)
	c.Assert(err, checker.IsNil)
	_, err = fmt.Fprint(fr1, string(fr2))
	c.Assert(err, checker.IsNil)

	// wait for traefik
	time.Sleep(1 * time.Second)
	err = try.GetRequest("http://127.0.0.1:8080/api/rawdata", 60*time.Second, try.BodyContains("Host(`root.localhost`)"))
	c.Assert(err, checker.IsNil)

	// Check the failed service (whoami2) is not getting requests
	wantIPs := []string{s.whoami1IP, s.whoami1IP, s.whoami1IP, s.whoami1IP}
	for _, ip := range wantIPs {
		want := "IP: " + ip
		err = try.Request(rootReq, time.Nanosecond, try.BodyContains(want))
		c.Assert(err, checker.IsNil)
	}
}
