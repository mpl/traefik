package integration

import (
	"bytes"
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
	// TODO(mpl): create them only for TestPropagate?
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

	/*
		rootReq, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8000", nil)
		c.Assert(err, checker.IsNil)
		rootReq.Host = "root.localhost"
	*/

	// Verify load-balancing on foo, still works, and that we're getting wsp1, wsp4, wsp1, wsp4, etc.
	for i := 0; i < 4; i++ {
		var want string
		// TODO(mpl): use mod
		if i == 0 || i == 2 {
			want = `IP: ` + s.whoami4IP
		} else {
			want = `IP: ` + s.whoami2IP
		}
		err = try.Request(rootReq, 3*time.Second, try.BodyContains(want))
		c.Assert(err, checker.IsNil)
	}

	return

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
