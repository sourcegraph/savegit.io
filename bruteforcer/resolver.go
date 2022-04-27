package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mercari.io/go-dnscache"
	"go.uber.org/zap"
)

const b62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func encodeID(id uint64) string {
	var out []byte

	for id > 0 {
		tmp := id % 62
		id /= 62
		out = append(out, b62Alphabet[tmp])
	}

	return string(out)
}

// ResolvedShortlink represents a single resolved shortlink
type ResolvedShortlink struct {
	isError     bool
	url         string
	resolvedURL string
}

// Resolver resolves git.io shortlinks
type Resolver struct {
	TotalCounter             uint64
	TotalRedirect            uint64
	Total404                 uint64
	RequestCounter           uint64
	RequestSuccessCounter    uint64
	RequestSuccess404Counter uint64
	RequestErrorCounter      uint64
	startTime                time.Time
	workerCount              int

	// read-only
	finishedAlready map[string]struct{}

	saveLock sync.Mutex
	toSave   map[string]ResolvedShortlink
}

func newResolver() *Resolver {
	r := &Resolver{
		workerCount: 1500,
		startTime:   time.Now(),
		toSave:      map[string]ResolvedShortlink{},
	}
	r.finishedAlready = r.read()
	return r
}

// GetRPS returns req/sec
func (r *Resolver) GetRPS() float64 {
	requestCounter := atomic.AddUint64(&r.RequestCounter, 0)
	return float64(requestCounter) / time.Since(r.startTime).Seconds()
}

func (r *Resolver) printStats() {
	totalCounter := atomic.AddUint64(&r.TotalCounter, 0)
	totalRedirect := atomic.AddUint64(&r.TotalRedirect, 0)
	total404 := atomic.AddUint64(&r.Total404, 0)
	requestCounter := atomic.AddUint64(&r.RequestCounter, 0)
	requestErrorCounter := atomic.AddUint64(&r.RequestErrorCounter, 0)
	requestSuccess404Counter := atomic.AddUint64(&r.RequestSuccess404Counter, 0)
	requestSuccessCounter := atomic.AddUint64(&r.RequestSuccessCounter, 0)

	fmt.Println("")
	fmt.Printf("stats: RPS: %v\n", r.GetRPS())
	fmt.Printf("stats: %v total, %v total redirects, %v total 404s\n",
		totalCounter,
		totalRedirect,
		total404,
	)
	fmt.Printf("stats: %v requests, %v error, %v 404, %v success\n",
		requestCounter,
		requestErrorCounter,
		requestSuccess404Counter,
		requestSuccessCounter,
	)
}

func (r *Resolver) read() map[string]struct{} {
	results := map[string]struct{}{}
	file, err := os.Open("data.txt")
	if err != nil {
		return map[string]struct{}{}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		split := strings.Split(line, ",")
		url, redirectURL := split[0], split[1]
		if redirectURL == "" {
			atomic.AddUint64(&r.Total404, 1)
		} else {
			atomic.AddUint64(&r.TotalRedirect, 1)
		}
		results[url] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
	return results
}

func (r *Resolver) finished(resolved ResolvedShortlink) {
	if resolved.isError {
		return
	}

	r.saveLock.Lock()
	defer r.saveLock.Unlock()
	r.toSave[resolved.url] = resolved
	if resolved.resolvedURL == "" {
		atomic.AddUint64(&r.Total404, 1)
	} else {
		atomic.AddUint64(&r.TotalRedirect, 1)
	}
	if len(r.toSave) > 10000 {
		f, err := os.OpenFile("data.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Println("error: save:", err)
			return
		}
		defer f.Close()
		for _, entry := range r.toSave {
			if _, err := f.WriteString(fmt.Sprintf("%s,%s\n", trim(entry.url), entry.resolvedURL)); err != nil {
				fmt.Println("error: save:", err)
				return
			}
		}
		r.toSave = map[string]ResolvedShortlink{}
	}
}

func (r *Resolver) flush() {
	r.saveLock.Lock()
	defer r.saveLock.Unlock()
	fmt.Println("write", len(r.toSave))

	f, err := os.OpenFile("data.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("error: save:", err)
		return
	}
	defer f.Close()
	for _, entry := range r.toSave {
		if _, err := f.WriteString(fmt.Sprintf("%s,%s\n", trim(entry.url), entry.resolvedURL)); err != nil {
			fmt.Println("error: save:", err)
			return
		}
	}
	r.toSave = map[string]ResolvedShortlink{}
}

func trim(u string) string {
	return strings.TrimPrefix(u, "https://git.io/")
}

func (r *Resolver) startWorker(queue chan string, output chan ResolvedShortlink) {
	go func() {
		resolver, _ := dnscache.New(24*time.Hour, 5*time.Second, zap.NewNop())

		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 1024,
				TLSHandshakeTimeout: 20 * time.Second,
				DialContext:         dnscache.DialFunc(resolver, nil),
			},
			Timeout: 20 * time.Second,
		}

		for {
			url := <-queue
			atomic.AddUint64(&r.TotalCounter, 1)

			if _, finishedAlready := r.finishedAlready[trim(url)]; finishedAlready {
				continue
			}
			atomic.AddUint64(&r.RequestCounter, 1)

			resp, err := client.Head(url)
			if err != nil {
				atomic.AddUint64(&r.RequestErrorCounter, 1)
				output <- ResolvedShortlink{
					isError: true,
				}
				// fmt.Println("error:", err)
				continue
			}
			_, _ = ioutil.ReadAll(resp.Body) // allegedly needed in order for keep-alive?
			resp.Body.Close()

			if resp.StatusCode == 302 {
				resolvedURL, err := resp.Location()
				if err != nil {
					atomic.AddUint64(&r.RequestErrorCounter, 1)
					output <- ResolvedShortlink{
						isError: true,
					}
					continue
				}

				atomic.AddUint64(&r.RequestSuccessCounter, 1)
				resolved := ResolvedShortlink{
					isError:     false,
					url:         url,
					resolvedURL: resolvedURL.String(),
				}
				r.finished(resolved)
				output <- resolved
			} else if resp.StatusCode == 404 {
				atomic.AddUint64(&r.RequestSuccess404Counter, 1)
				resolved := ResolvedShortlink{
					isError:     false,
					url:         url,
					resolvedURL: "",
				}
				r.finished(resolved)
				output <- resolved

			} else {
				atomic.AddUint64(&r.RequestErrorCounter, 1)

				resolved := ResolvedShortlink{
					isError: true,
				}
				output <- resolved
			}
		}
	}()
}

// ResolveRange resolves (bruteforce) a range of git.io shortlinks.
func (r *Resolver) ResolveRange(start uint64, end uint64) {
	workChannel := make(chan string, 8192)

	output := make(chan ResolvedShortlink, 8192)
	for i := 0; i < r.workerCount; i++ {
		r.startWorker(workChannel, output)
	}

	waitFor := 0
	for i := start; i < end; i++ {
		waitFor++
		url := fmt.Sprintf("https://git.io/%s", encodeID(i))
		workChannel <- url
	}

	for {
		resolved := <-output
		_ = resolved
		waitFor--
		if (waitFor == 0) {
			break
		}
	}
	r.flush()
}
