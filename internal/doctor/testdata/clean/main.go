// Command app is the whole application: in Go the binary is the server, so this
// file is what public/index.php and artisan are in Laravel.
package main

import (
	"log"

	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/view"
)

func main() {
	k := kernel.New()
	k.Register(view.NewModule())

	if err := k.Run(); err != nil {
		log.Fatal(err)
	}
}
