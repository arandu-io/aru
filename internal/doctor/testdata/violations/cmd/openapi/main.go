// Command openapi writes the application's contracts beside the binary,
// with a wiring of its own.
package main

import "fmt"

func main() {
	fmt.Println(`{"openapi": "3.1.0"}`)
}
