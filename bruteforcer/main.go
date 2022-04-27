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
			time.Sleep(10 * time.Second)
			resolver.printStats()
		}
	}()

	resolver.ResolveRange(start, end)

	resolver.printStats()
	fmt.Println("done!")
}
