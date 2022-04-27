package main

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"time"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	resolver := newResolver()

	start, _ := strconv.ParseUint(os.Args[1], 10, 64)
	end, _ := strconv.ParseUint(os.Args[2], 10, 64)
	fmt.Printf("info: using range %v-%v\n", start, end)

	go func() {
		for {
			time.Sleep(5 * time.Second)
			fmt.Println("")
			fmt.Printf("stats: RPS: %v\n", resolver.GetRPS())
			fmt.Printf("stats: %v total, %v total redirects, %v total 404s\n",
				resolver.TotalCounter,
				resolver.TotalRedirect,
				resolver.Total404,
			)
			fmt.Printf("stats: %v requests, %v error, %v 404, %v success\n",
				resolver.RequestCounter,
				resolver.RequestErrorCounter,
				resolver.RequestSuccess404Counter,
				resolver.RequestSuccessCounter,
			)
		}
	}()

	resolver.ResolveRange(start, end)
}
