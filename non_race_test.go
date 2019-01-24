// +build !race

package gogo

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dolab/gogo/pkgs/hooks"
	"github.com/dolab/httptesting"
	"github.com/golib/assert"
)

func Test_ServerWithHealthz(t *testing.T) {
	server := fakeHealthzServer()

	go server.Run()
	for {
		if len(server.Address()) > 0 {
			break
		}
	}

	client := httptesting.New(server.Address(), false)

	request := client.New(t)
	request.Get(GogoHealthz, nil)
	request.AssertOK()
	request.AssertEmpty()
}

func Test_ServerWithTcp(t *testing.T) {
	server := fakeTcpServer()
	server.GET("/server/tcp", func(ctx *Context) {
		ctx.SetStatus(http.StatusNotImplemented)
	})

	go server.Run()
	for {
		if len(server.Address()) > 0 {
			break
		}
	}

	client := httptesting.New(server.Address(), false)

	request := client.New(t)
	request.Get("/server/tcp", nil)
	request.AssertStatus(http.StatusNotImplemented)
	request.AssertEmpty()
}

var benchmarkServerWithTcpOnce sync.Once

func Benchmark_ServerWithTcp(b *testing.B) {
	server := fakeServer()
	server.GET("/bench/tcp", func(ctx *Context) {
		ctx.SetStatus(http.StatusNotImplemented)
	})

	var (
		endpoint string
	)
	benchmarkServerWithTcpOnce.Do(func() {
		go server.Run()
		for {
			if len(server.Address()) > 0 {
				break
			}
		}

		endpoint = "http://" + server.Address() + "/bench/tcp"
	})

	client := &http.Client{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.Get(endpoint)
	}
}

func Test_ServerWithUnix(t *testing.T) {
	it := assert.New(t)

	server := fakeUnixServer()
	server.GET("/server/unix", func(ctx *Context) {
		ctx.SetStatus(http.StatusNotImplemented)
	})

	go server.Run()
	for {
		if len(server.Address()) > 0 {
			break
		}
	}
	defer os.Remove("/tmp/gogo.sock")

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (conn net.Conn, err error) {
				return net.Dial("unix", server.Address())
			},
		},
	}

	// it should work
	response, err := client.Get("http://unix/server/unix")
	if it.Nil(err) {
		defer response.Body.Close()

		it.Equal(http.StatusNotImplemented, response.StatusCode)

		data, err := ioutil.ReadAll(response.Body)
		it.Nil(err)
		it.Empty(data)
	}

	// it should return error
	response, err = http.DefaultClient.Get("http://unix/server/unix")
	it.NotNil(err)
	it.Nil(response)
}

var benchmarkServerWithUnix sync.Once

func Benchmark_ServerWithUnix(b *testing.B) {
	server := fakeUnixServer()
	server.GET("/bench/unix", func(ctx *Context) {
		ctx.SetStatus(http.StatusNotImplemented)
	})

	benchmarkServerWithUnix.Do(func() {
		go server.Run()
		for {
			if len(server.Address()) > 0 {
				break
			}
		}
	})
	defer os.Remove("/tmp/gogo.sock")

	unixConn, unixErr := net.Dial("unix", "/tmp/gogo.sock")
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (conn net.Conn, err error) {
				return unixConn, unixErr
			},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.Get("http://unix/server/unix")
	}
}

func Test_Server_loggerNewWithReuse(t *testing.T) {
	it := assert.New(t)
	logger := NewAppLogger("nil", "")
	config, _ := fakeConfig("application.throttle.json")

	server := NewAppServer(config, logger)

	// new with request id
	alog := server.loggerNew("di-tseuqer-x")
	if it.NotNil(alog) {
		it.Implements((*Logger)(nil), alog)

		it.Equal("di-tseuqer-x", alog.RequestID())
	}
	server.loggerReuse(alog)

	// it should work for the same id
	blog := server.loggerNew("di-tseuqer-x")
	if it.NotNil(blog) {
		it.Equal("di-tseuqer-x", blog.RequestID())

		it.Equal(fmt.Sprintf("%p", alog), fmt.Sprintf("%p", blog))
	}
	server.loggerReuse(blog)

	clog := server.loggerNew("x-request-id")
	if it.NotNil(blog) {
		it.Equal("x-request-id", clog.RequestID())

		it.Equal(fmt.Sprintf("%p", alog), fmt.Sprintf("%p", clog))
	}
}

type testService struct {
	hooks       int64
	middlewares int64
	v1          Grouper
}

func (svc *testService) Init(config Configer, group Grouper) {
	svc.hooks = 0
	svc.v1 = group
}

func (svc *testService) Middlewares() {
	svc.v1.Use(func(ctx *Context) {
		atomic.AddInt64(&svc.middlewares, 1)

		ctx.Next()
	})
}

func (svc *testService) Resources() {
	svc.v1.GET("/server/service", func(ctx *Context) {
		ctx.AddHeader("x-gogo-hooks", strings.Join(ctx.Request.Header["X-Gogo-Hooks"], ","))

		ctx.Text("Hello, service!")
	})
}

func (svc *testService) RequestReceivedHooks() []hooks.NamedHook {
	return []hooks.NamedHook{
		{
			Name: "request_receved@testing",
			Apply: func(w http.ResponseWriter, r *http.Request) bool {
				r.Header.Add("x-gogo-hooks", "Received")

				atomic.AddInt64(&svc.hooks, 1)
				return true
			},
		},
	}
}

func (svc *testService) RequestRoutedHooks() []hooks.NamedHook {
	return []hooks.NamedHook{
		{
			Name: "request_routed@testing",
			Apply: func(w http.ResponseWriter, r *http.Request) bool {
				r.Header.Add("x-gogo-hooks", "Routed")

				atomic.AddInt64(&svc.hooks, 1)
				return true
			},
		},
	}
}

func (svc *testService) ResponseReadyHooks() []hooks.NamedHook {
	return []hooks.NamedHook{
		{
			Name: "response_ready@testing",
			Apply: func(w http.ResponseWriter, r *http.Request) bool {
				r.Header.Add("x-gogo-hooks", "Ready")

				atomic.AddInt64(&svc.hooks, 1)
				return true
			},
		},
	}
}

func (svc *testService) ResponseAlwaysHooks() []hooks.NamedHook {
	return []hooks.NamedHook{
		{
			Name: "response_always@testing",
			Apply: func(w http.ResponseWriter, r *http.Request) bool {
				r.Header.Add("x-gogo-hooks", "Always")

				atomic.AddInt64(&svc.hooks, 1)
				return true
			},
		},
	}
}

func Test_Server_NewService(t *testing.T) {
	it := assert.New(t)
	service := &testService{}
	server := fakeServer()
	server.NewService(service)

	go server.Run()
	for {
		if len(server.Address()) > 0 {
			break
		}
	}

	client := httptesting.New(server.Address(), false)

	request := client.New(t)
	request.Get("/server/service", nil)
	request.AssertOK()
	request.AssertHeader("x-gogo-hooks", "Received,Routed")
	request.AssertContains("Hello, service!")

	it.EqualValues(1, service.middlewares)
	it.EqualValues(4, service.hooks)
}

func Test_Server_NewServiceWithConcurrency(t *testing.T) {
	it := assert.New(t)
	service := &testService{}
	server := fakeServer()
	server.NewService(service)

	go server.Run()
	for {
		if len(server.Address()) > 0 {
			break
		}
	}

	client := httptesting.New(server.Address(), false)

	var (
		max = 10

		wg sync.WaitGroup
	)

	wg.Add(max)
	for i := 0; i < max; i++ {
		go func() {
			defer wg.Done()

			request := client.New(t)
			request.Get("/server/service", nil)
			request.AssertOK()
			request.AssertHeader("x-gogo-hooks", "Received,Routed")
			request.AssertContains("Hello, service!")
		}()
	}
	wg.Wait()

	it.EqualValues(1*max, service.middlewares)
	it.EqualValues(4*max, service.hooks)
}

var benchmarkServiceOnce sync.Once

func Benchmark_Server_Service(b *testing.B) {
	service := &testService{}
	server := fakeServer()
	server.NewService(service)

	var (
		endpoint string
	)
	benchmarkServiceOnce.Do(func() {
		go server.Run()
		for {
			if len(server.Address()) > 0 {
				break
			}
		}

		endpoint = "http://" + server.Address() + "/server/service"
	})

	client := &http.Client{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.Get(endpoint)
	}
}
