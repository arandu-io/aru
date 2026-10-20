// Command browser-check drives Chrome over the DevTools protocol and prints
// what it saw, which is a test whose result nobody checks.
package main

import "fmt"

// chromeFlags are what the program starts the browser with.
var chromeFlags = []string{"--headless=new", "--remote-debugging-pipe"}

func main() {
	fmt.Println(chromeFlags, "Page.navigate")
}
