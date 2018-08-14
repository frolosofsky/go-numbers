package main

import (
	"net/http"
	"log"
	"encoding/json"
	"time"
	"io/ioutil"
	"sort"
	"context"
)

// Simplifies json encoding/decoding
type Numbers struct {
	Numbers []int `json:"numbers"`
}

// set* functions
// Workarounded traditional (and obviously sorted) set
func setAdd(set map[int]struct{}, value ...int) {
	for _, v := range value {
		set[v] = struct{}{}
	}
}

func setToArray(set map[int]struct{}) []int {
	var result []int
	for v, _ := range set {
		result = append(result, v)
	}
	sort.Ints(result)
	return result
}

// Purpose: to abstract fetching numbers from url.
// Returns: numbers slice (nil if error),
//          HTTP status code (-1 if error)
//          Error (nil if no error)
type NumbersGetter interface {
	get(ctx context.Context, url string) ([]int, int, error)
}

// Http implementation of `NumbersGetter`.
// `get` blocks current routine until it fetches data from endpoint, or error occurs, or context cancelled.
type NumbersGetterHttp struct {}

func (NumbersGetterHttp) get(ctx context.Context, url string) ([]int, int, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, -1, err
	}
	client := http.Client{}
	res, err := client.Do(req.WithContext(ctx))
	if res != nil {
		defer res.Body.Close()
	}
	if err != nil {
		return nil, -1, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, res.StatusCode, nil
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, -1, err
	}
	numbers := Numbers{}
	err = json.Unmarshal(body, &numbers)
	if err != nil {
		return nil, -1, err
	}
	return numbers.Numbers, res.StatusCode, nil
}

// Retrieves numbers from channel `c`. Blocks current routine until gets `expected` amount of results or context cancelled.
// Stores collected data in a `result` set. Returns sorted array representation of the set.
func collectNumbers(ctx context.Context, expected int, c <-chan []int) []int {
	result := make(map[int]struct{})
	for expected > 0 {
		select {
		case numbers := <-c:
			log.Printf("Got numbers: %v\n", numbers)
			expected -= 1
			setAdd(result, numbers...)
		case <-ctx.Done():
			log.Printf("Unhandled urls: %d\n", expected)
			expected = 0
		}
	}
	return setToArray(result)
}

// Fetches numbers, pass to channel (to be processed by `collectNumbers`), log errors
func fetchNumbers(ctx context.Context, g NumbersGetter, url string, c chan []int) {
	var result []int
	if numbers, status, err := g.get(ctx, url); err != nil {
		log.Println(err)
	} else if status != http.StatusOK {
		log.Printf("%s responded with %d", url, status)
	} else {
		result = numbers
	}
	select {
	case <-ctx.Done():
	case c <- result: // pass result even if error occured (let `collectNumbers` decrement it's `expected`)
	}
}

// Constructs handler for /numbers request
// Handler creates and configures context in order to cancel all sub-requests which are timed out and
// when client closes connection.
func makeNumbersHandler(g NumbersGetter) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		q := r.URL.Query();
		urls := q["u"]
		c := make(chan []int)
		ctx, _ := context.WithTimeout(r.Context(), 500 * time.Millisecond)
		for _, url := range urls {
			go fetchNumbers(ctx, g, url, c)
		}
		numbers := collectNumbers(ctx, len(urls), c)
		data, _ := json.Marshal(Numbers{Numbers:numbers})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		log.Printf("Processing took %v", time.Now().Sub(start))
	}
}

func makeHttpNumbersHandler() func(http.ResponseWriter, *http.Request) {
	return makeNumbersHandler(NumbersGetterHttp{})
}

func main() {
	http.HandleFunc("/numbers", makeHttpNumbersHandler())
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", nil))
}
