package main

import(
	"testing"
	"time"
	"net/http"
	"net/http/httptest"
	"log"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"reflect"
	"sync"
	"context"
)

func makeStubHandler(numbers []int, timeout time.Duration) func(http.ResponseWriter, *http.Request) {
	data, _ := json.Marshal(Numbers{Numbers: numbers})
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(timeout * time.Millisecond)
		fmt.Fprintf(w, "%s", data)
	}
}

// ReadTimeout prevents from getting stuck while reading from clients.
// Althought it's totally fine to have thouse timeouts in production, in tests it might hide real problem.
// Yep, TestHTTP gonna stuck forever w/o this option. Further investigation required.
func startServer(port int) *http.Server {
	server := &http.Server{
		Addr: "localhost:8080",
		ReadTimeout: 1 * time.Second,
		WriteTimeout: 1 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil {
			log.Print(err)
		}
	}()
	return server
}

func stopServer(server *http.Server) {
	if err := server.Shutdown(context.TODO()); err != nil {
		log.Fatal(err)
	}
}

// Process and check real http request to /numbers endpoint
func numbers_get(t *testing.T, url string, expected_status int, expected_numbers []int) {
	// Let's use 600ms in order to avoid false alarms (rough: 50ms overhead for /numbers handler + 50ms for transport)
	client := http.Client {
		Timeout: 600 * time.Millisecond,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	res, err := client.Get(url)
	if err != nil {
		t.Fatalf("Unexpected error while getting %s: %s", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != expected_status {
		t.Errorf("Got status (%s): %d, expected: %d", url, res.StatusCode, expected_status)
	}
	if res.StatusCode != http.StatusOK {
		return
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Unexpected error while fetching data from %s: %s", url, err)
	}
	numbers := Numbers{}
	err = json.Unmarshal(body, &numbers)
	if err != nil {
		t.Fatalf("Unexpected error while parsing json from %s: %s -- %s", url, err, body)
	}
	if !reflect.DeepEqual(numbers.Numbers, expected_numbers) {
		t.Errorf("Got numbers (%s): %v, expected: %v", url, numbers.Numbers, expected_numbers)
	}
}

// Process and check httptest request to /numbers endpoint
func numbers_rr(t *testing.T, url string, handler http.HandlerFunc, expected_status int, expected_numbers []int) {
	rr := httptest.NewRecorder()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("Error while constructing test request to %s: %s", url, err)
	}
	handler.ServeHTTP(rr, req)
	if rr.Code != expected_status {
		t.Errorf("Got status (%s): %d, expected: %d", url, rr.Code, expected_status)
	}
	if rr.Code != http.StatusOK {
		return
	}
	body, err := ioutil.ReadAll(rr.Body)
	if err != nil {
		t.Fatalf("Unexpected error while fetching data from %s: %s", url, err)
	}
	numbers := Numbers{}
	err = json.Unmarshal(body, &numbers)
	if err != nil {
		t.Fatalf("Unexpected error while parsing json from %s: %s -- %s", url, err, body)
	}
	if !reflect.DeepEqual(numbers.Numbers, expected_numbers) {
		t.Errorf("Got numbers (%s): %v, expected: %v", url, numbers.Numbers, expected_numbers)
	}
}

type NumbersGetterStubCfg struct {
	Numbers []int
	Timeout time.Duration
}

func stubConfig(numbers []int, timeout time.Duration) NumbersGetterStubCfg {
	return NumbersGetterStubCfg {
		Numbers: numbers,
		Timeout: timeout * time.Millisecond,
	}
}

type NumbersGetterStub struct {
	Config map[string]NumbersGetterStubCfg
}

func (g NumbersGetterStub) get(url string, unused time.Duration) ([]int, int, error) {
	n, ok := g.Config[url]; if ok {
		time.Sleep(n.Timeout)
		return n.Numbers, 200, nil
	} else {
		return nil, 404, nil
	}
}

type TestCase struct {
	Url string
	Numbers []int
	Status int
}

func TestBasic(t *testing.T) {
	log.SetFlags(0)
	log.SetOutput(ioutil.Discard)
	s := NumbersGetterStub{ // configure numbers sources (stubs)
		Config: map[string]NumbersGetterStubCfg{
			"/test1": stubConfig([]int{1, 2, 3, 4}, 0),
			"/test2": stubConfig([]int{5, 6, 1, 1}, 100),
			"/test3": stubConfig([]int{1, 2}, 300),
			"/test4": stubConfig([]int{11, 12}, 400),
			"/test5": stubConfig([]int{101, 102}, 450),
			"/test6": stubConfig([]int{1001, 1002}, 550),
		},
	}
	testCases := []TestCase {
		{Url: "/numbers?u=/wrong", Numbers: nil, Status: 200},
		{Url: "/numbers?u=/test1", Numbers: []int{1, 2, 3, 4}, Status: 200},
		{Url: "/numbers?u=/test1&u=/test2", Numbers: []int{1, 2, 3, 4, 5, 6}, Status: 200},
		{Url: "/numbers?u=/test3&u=/test4&u=/test5&u=/test6", Numbers: []int{1, 2, 11, 12, 101, 102}, Status: 200},
	}
	handler := http.HandlerFunc(makeNumbersHandler(s))
	for _, test := range testCases {
		numbers_rr(t, test.Url, handler, test.Status, test.Numbers)
	}
}

// Send lots of concurent requests to /numbers
// This test might fail! It depends on amount of CPUs/$GOMAXPROCS.
// If it doesn't fail, just increase `loops` :) Btw 500 seems to be stable for my laptop.
// How its fail:
// 1. /numbers cuts processing in ~500msecs.
// 2. Some of endpoints (/test5 and sometimes /test4) are still in progress.
// It's either limitaion of runtime's timers implementation or my bad code :)
// At first sight it's not a channel issue (which is used in /numbers handler)
// Likely it's an issue in test handlers but not in the /numbers implementation
// Maybe it's even bad idea to launch so many routines.
func TestConcurent(t *testing.T) {
	log.SetFlags(0)
	log.SetOutput(ioutil.Discard)
	s := NumbersGetterStub{ // configure numbers sources (stubs)
		Config: map[string]NumbersGetterStubCfg{
			"/test1": stubConfig([]int{1, 2, 3, 4}, 0),
			"/test2": stubConfig([]int{5, 6, 1, 1}, 100),
			"/test3": stubConfig([]int{1, 2}, 300),
			"/test4": stubConfig([]int{11, 12}, 400),
			"/test5": stubConfig([]int{101, 102}, 450),
			"/test6": stubConfig([]int{1001, 1002}, 550),
		},
	}
	testCases := []TestCase {
		{Url: "/numbers?u=/wrong", Numbers: nil, Status: 200},
		{Url: "/numbers?u=/test1", Numbers: []int{1, 2, 3, 4}, Status: 200},
		{Url: "/numbers?u=/test1&u=/test2", Numbers: []int{1, 2, 3, 4, 5, 6}, Status: 200},
		{Url: "/numbers?u=/test3&u=/test4&u=/test5&u=/test6", Numbers: []int{1, 2, 11, 12, 101, 102}, Status: 200},
	}
	handler := http.HandlerFunc(makeNumbersHandler(s))
	loops := 500
	var wg sync.WaitGroup
	for i := 0; i < loops; i++ {
		for _, test := range testCases {
			wg.Add(1)
			go func(test TestCase) {
				start := time.Now()
				numbers_rr(t, test.Url, handler, test.Status, test.Numbers)
				elapsed := time.Now().Sub(start)
				if elapsed > 550 * time.Millisecond {
					t.Errorf("Requisting %s have taken %v", test.Url, elapsed)
				}
				wg.Done()
			}(test)
		}
	}
	wg.Wait()
}

// No mocks anymore. Real http endpoints for /numbers and /tests-s
func TestHTTP(t *testing.T) {
	log.SetFlags(0)
	log.SetOutput(ioutil.Discard)
	http.HandleFunc("/numbers", makeNumbersHandler(NumbersGetterHttp{}))
	http.HandleFunc("/test1", makeStubHandler([]int{1, 2, 3, 4}, 0))
	http.HandleFunc("/test2", makeStubHandler([]int{5, 6, 1, 1}, 100))
	http.HandleFunc("/test3", makeStubHandler([]int{1, 2}, 300))
	http.HandleFunc("/test4", makeStubHandler([]int{11, 12}, 400))
	http.HandleFunc("/test5", makeStubHandler([]int{101, 102}, 450))
	http.HandleFunc("/test6", makeStubHandler([]int{1001, 1002}, 550))
	server := startServer(8080)
	defer stopServer(server)
	testCases := []TestCase {
		{Url: "http://localhost:8080/unimplemented", Numbers: nil, Status: 404},
		{Url: "http://localhost:8080/numbers?u=http://localhost:8080/wrong", Numbers: nil, Status: 200},
		{Url: "http://localhost:8080/numbers?u=http://localhost:8080/test1", Numbers: []int{1, 2, 3, 4}, Status: 200},
		{Url: "http://localhost:8080/numbers?u=http://localhost:8080/test1&u=http://localhost:8080/test2", Numbers: []int{1, 2, 3, 4, 5, 6}, Status: 200},
		{Url: "http://localhost:8080/numbers?u=http://localhost:8080/test3&u=http://localhost:8080/test4&u=http://localhost:8080/test5&u=http://localhost:8080/test6", Numbers: []int{1, 2, 11, 12, 101, 102}, Status: 200},
	}
	loops := 10 // relaxed hitting the service
	var wg sync.WaitGroup
	for i := 0; i < loops; i++ {
		for _, test := range testCases {
			wg.Add(1)
			go func(test TestCase) {
				numbers_get(t, test.Url, test.Status, test.Numbers)
				wg.Done()
			}(test)
		}
	}
	wg.Wait()
}
