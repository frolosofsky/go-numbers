package main

import (
	"net/http"
	"log"
	"encoding/json"
	"time"
	"io/ioutil"
	"sort"
)

type Numbers struct {
	Numbers []int `json:"numbers"`
}

func setPutValue(set map[int]struct{}, value int) {
	set[value] = struct{}{}
}

func setPutArray(set map[int]struct{}, values []int) {
	for _, v := range values {
		setPutValue(set, v)
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

type NumbersGetter interface {
	get(url string, timeout time.Duration) ([]int, int, error)
}

type NumbersGetterHttp struct {}

func (NumbersGetterHttp) get(url string, timeout time.Duration) ([]int, int, error) {
	client := http.Client {
		Timeout: timeout,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	res, err := client.Get(url)
	if res != nil {
		defer res.Body.Close()
	}
	if err != nil {
		return nil, -1, err
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, -1, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, res.StatusCode, nil
	}
	numbers := Numbers{}
	err = json.Unmarshal(body, &numbers)
	if err != nil {
		return nil, -1, err
	}
	return numbers.Numbers, res.StatusCode, nil
}

func collectNumbers(w http.ResponseWriter, expected int, c <-chan []int, c_timeout <-chan time.Time) {
	result := make(map[int]struct{})
	for expected > 0 {
		select {
		case numbers := <-c:
			log.Printf("Got numbers: %v\n", numbers)
			expected -= 1
			setPutArray(result, numbers)
		case <-c_timeout:
			log.Printf("Timeout, unprocessed urls: %d\n", expected)
			expected = 0
		}
	}
	data, _ := json.Marshal(Numbers{Numbers:setToArray(result)})
	w.Write(data)
}

// Constructs handler for /numbers request
func makeNumbersHandler(g NumbersGetter) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		timer := time.NewTimer(500 * time.Millisecond)
		q := r.URL.Query();
		urls := q["u"]
		c := make(chan []int, 10)
		done := make(chan struct{})
		for _, url := range urls {
			go func(url string) {
				var result []int
				if numbers, status, err := g.get(url, 500 * time.Millisecond); err != nil {
					log.Println(err)
				} else if status != http.StatusOK {
					log.Printf("%s responded with %d", url, status)
				} else {
					result = numbers
				}
				select {
				case <-done:
					return
				default:
					c <- result
				}
			}(url)
		}
		collectNumbers(w, len(urls), c, timer.C)
		close(done) // notify long-processing getter to not send new data
		timer.Stop()
		log.Printf("Processing took %v", time.Now().Sub(start))
	}
}

// Constructs handler for numbers source API
// func makeStubHandler(numbers []int, timeout time.Duration) func(http.ResponseWriter, *http.Request) {
// 	data, _ := json.Marshal(Numbers{Numbers: numbers})
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		time.Sleep(timeout * time.Millisecond)
// 		fmt.Fprintf(w, "%s", data)
// 	}
// }

func main() {
	http.HandleFunc("/numbers", makeNumbersHandler(NumbersGetterHttp{}))
	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}
