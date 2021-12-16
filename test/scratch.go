package test

import (
	"fmt"
	"sync"
)

func main() {
	out := make([]int, 32)
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i] = i * 100
		}(i)
	}
	wg.Wait()
	fmt.Println(out)
}
