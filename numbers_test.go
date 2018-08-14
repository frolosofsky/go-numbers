package main

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Constructs HTTP request handler with predefined response and timeout
func makeStubHandler(data string, timeout int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(timeout) * time.Millisecond)
		w.Write([]byte(data))
	}
}

// ReadTimeout prevents from getting stuck while reading from clients.
// Althought it's totally fine to have thouse timeouts in production, in tests it might hide real problem.
// Yep, TestHTTP gonna stuck forever w/o this option, rarely and randomly.
// TODO: Further investigation required.
func startServer(t *testing.T, port int) *http.Server {
	server := &http.Server{
		Addr:         "127.0.0.1:8080",
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil {
			t.Log(err)
		}
	}()
	return server
}

// reflect.DeepEqual compares empty slices in a strange way
// And everybody are saying it's extremely slow :)
func numbers_cmp(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Wrapper for passing http requests and comparing responses with expected values
func numbers_get(t *testing.T, url string, expected_status int, expected_numbers []int) {
	g := NumbersGetterHttp{}
	// rough, 600 = 500 + routines overhead + transport overhead. Everything is in a single process.
	ctx, _ := context.WithTimeout(context.Background(), 600*time.Millisecond)
	result, status, err := g.get(ctx, url)
	if ctx.Err() != nil {
		t.Errorf("Request failed: %s", ctx.Err())
		return
	}
	if err != nil && expected_status != -1 { // -1 is used when we're expecting something malformed, so let's silently accept the error
		t.Error(err)
		return
	}
	if status != expected_status {
		t.Errorf("Got status %d, expected %d (%s)", status, expected_status, url)
		return
	}
	if expected_status != -1 && !numbers_cmp(result, expected_numbers) {
		t.Errorf("Got numbers %v, expected %v (%s)", result, expected_numbers, url)
		return
	}
}

// Process and check httptest request to /numbers endpoint
// TODO: figure out how to reuse NumbersGetterHttp.get() and numbers_get() for this purpose instead of having numbers_rr
func numbers_rr(t *testing.T, url string, handler http.HandlerFunc, expected_status int, expected_numbers []int) {
	rr := httptest.NewRecorder()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Error(err)
		return
	}
	handler.ServeHTTP(rr, req)
	if rr.Code != expected_status {
		t.Errorf("Got status (%s): %d, expected: %d", url, rr.Code, expected_status)
		return
	}
	if rr.Code != http.StatusOK {
		return
	}
	body, err := ioutil.ReadAll(rr.Body)
	if err != nil {
		t.Error(err)
		return
	}
	numbers := Numbers{}
	err = json.Unmarshal(body, &numbers)
	if err != nil {
		t.Errorf("Unexpected error while parsing json from %s: %s -- %s", url, err, body)
		return
	}
	if !reflect.DeepEqual(numbers.Numbers, expected_numbers) {
		t.Errorf("Got numbers (%s): %v, expected: %v", url, numbers.Numbers, expected_numbers)
		return
	}
}

// NumbersGetterStub(Cfg) implements mock for fetching data provided by /test-endpoints in memory w/o network layer
type NumbersGetterStubCfg struct {
	Numbers []int
	Timeout time.Duration
}

func stubConfig(numbers []int, timeout int) NumbersGetterStubCfg {
	return NumbersGetterStubCfg{
		Numbers: numbers,
		Timeout: time.Duration(timeout) * time.Millisecond,
	}
}

type NumbersGetterStub struct {
	Config map[string]NumbersGetterStubCfg
}

func (g NumbersGetterStub) get(ctx context.Context, url string) ([]int, int, error) {
	n, ok := g.Config[url]
	if ok {
		time.Sleep(n.Timeout)
		return n.Numbers, 200, nil
	} else {
		return nil, 404, nil
	}
}

func TestCollectNumbers(t *testing.T) {
	log.SetFlags(0)
	log.SetOutput(ioutil.Discard)
	cases := []struct {
		Input  [][]int
		Result []int
	}{{
		Input:  [][]int{},
		Result: []int{},
	}, {
		Input:  [][]int{[]int{}},
		Result: []int{},
	}, {
		Input:  [][]int{[]int{}, []int{}},
		Result: []int{},
	}, {
		Input:  [][]int{[]int{1}},
		Result: []int{1},
	}, {
		Input:  [][]int{[]int{1}, []int{1}},
		Result: []int{1},
	}, {
		Input:  [][]int{[]int{1, 3}, []int{2}},
		Result: []int{1, 2, 3},
	}, {
		Input:  [][]int{[]int{9, 1}, []int{1}, []int{5, 1, 42}},
		Result: []int{1, 5, 9, 42},
	}}
	channel := make(chan []int, 10)
	for _, c := range cases {
		for _, i := range c.Input {
			channel <- i
		}
		r := collectNumbers(context.Background(), len(c.Input), channel)
		if !numbers_cmp(r, c.Result) {
			t.Errorf("collect %v, got %v, expected %v", c.Input, r, c.Result)
		}
	}

	channel <- []int{1, 2}
	channel <- []int{0, 0}
	ctx, _ := context.WithTimeout(context.Background(), 100*time.Millisecond)
	result := collectNumbers(ctx, 999, channel)
	if !numbers_cmp(result, []int{0, 1, 2}) {
		t.Errorf("collect %v, got %v, expected %v", [][]int{[]int{1, 2}, []int{0, 0}}, result, []int{0, 1, 2})
	}
}

type TestCase struct {
	Url     string
	Numbers []int
	Status  int
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
	testCases := []TestCase{
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
// If it doesn't fail, just increase the `loops` :) Btw 500 seems to be stable for my laptop.
// How its fail:
// 1. /numbers cuts processing in ~500msecs.
// 2. Some of endpoints (/test5 and sometimes /test4) are still in progress.
// 3. Test expects to receive numbers from these endpoints in time, and fails.
// Likely it's an issue in test handlers but not in the /numbers implementation.
// Maybe it's even bad idea to launch so many routines?
// Imaginary solutions, tests side:
// * Control amount of routines that maintain test requests (like to have no more than 1k routines at the same time)
// * Process several requests by one routine, don't know how to achive that with blocking IO.
// Backend side:
// * Reserch advanced techniques of handling requests in Go.
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
	testCases := []TestCase{
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
				defer wg.Done()
				start := time.Now()
				numbers_rr(t, test.Url, handler, test.Status, test.Numbers)
				elapsed := time.Now().Sub(start)
				if elapsed > 550*time.Millisecond {
					t.Errorf("Requesting %s have taken %v", test.Url, elapsed)
				}
			}(test)
		}
	}
	wg.Wait()
}

// No mocks anymore. Real http endpoints for /numbers and /tests-s
// This test fails in ~1% cases. Random connection to /numbers or /test gest refused.
// TODO: Further investigation required.
func TestHTTP(t *testing.T) {
	log.SetFlags(0)
	log.SetOutput(ioutil.Discard)
	http.HandleFunc("/numbers", makeHttpNumbersHandler())
	http.HandleFunc("/malformed1", makeStubHandler("pshh", 0))
	http.HandleFunc("/malformed2", makeStubHandler(`{"numbers: 42"}`, 200))
	http.HandleFunc("/test1", makeStubHandler(`{"numbers": [1, 2, 3, 4], "extra": [99]}`, 0))
	http.HandleFunc("/test2", makeStubHandler(`{"numbers": [5, 6, 1, 1]}`, 100))
	http.HandleFunc("/test3", makeStubHandler(`{"numbers": [1, 2]}`, 300))
	http.HandleFunc("/test4", makeStubHandler(`{"numbers": [11, 12]}`, 400))
	http.HandleFunc("/test5", makeStubHandler(`{"numbers": [101, 102]}`, 450))
	http.HandleFunc("/test6", makeStubHandler(`{"numbers": [1001, 1002]}`, 550))
	startServer(t, 8080)
	testCases := []TestCase{
		{Url: "http://127.0.0.1:8080/malformed1", Numbers: nil, Status: -1},
		{Url: "http://127.0.0.1:8080/malformed2", Numbers: nil, Status: -1},
		{Url: "http://127.0.0.1:8080/unimplemented", Numbers: nil, Status: 404},
		{Url: "http://127.0.0.1:8080/numbers?u=http://127.0.0.1:8080/wrong", Numbers: nil, Status: 200},
		{Url: "http://127.0.0.1:8080/numbers?u=127.0.0.1:8080/test1", Numbers: nil, Status: 200}, // wrong url in `u`
		{Url: "http://127.0.0.1:8080/numbers?u=http://127.0.0.1:8080/test1", Numbers: []int{1, 2, 3, 4}, Status: 200},
		{Url: "http://127.0.0.1:8080/numbers?u=http://127.0.0.1:8080/test1&u=http://127.0.0.1:8080/test2", Numbers: []int{1, 2, 3, 4, 5, 6}, Status: 200},
		{Url: "http://127.0.0.1:8080/numbers?u=http://127.0.0.1:8080/test3&u=http://127.0.0.1:8080/test4&u=http://127.0.0.1:8080/test5&u=http://127.0.0.1:8080/test6", Numbers: []int{1, 2, 11, 12, 101, 102}, Status: 200},
	}
	var wg sync.WaitGroup
	for _, test := range testCases {
		wg.Add(1)
		go func(test TestCase) {
			defer wg.Done()
			numbers_get(t, test.Url, test.Status, test.Numbers)
		}(test)
	}
	wg.Wait()
	// Make "recursive" request. Use url.Values to properly URL-encode `u` arguments.
	q := url.Values{}
	q.Add("u", "http://127.0.0.1:8080/test5")
	q.Add("u", "http://127.0.0.1:8080/numbers?u=http://127.0.0.1:8080/test2&u=http://127.0.0.1:8080/test4")
	u := url.URL{
		Scheme:   "http",
		Host:     "127.0.0.1:8080",
		Path:     "numbers",
		RawQuery: q.Encode(),
	}
	numbers_get(t, u.String(), 200, []int{1, 5, 6, 11, 12, 101, 102})
}
