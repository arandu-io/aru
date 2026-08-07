package services

import "net/http"

// Notify is a violation of the manifest: the project declares network = false
// and this leaves the process. Whoever installed it agreed to code that does
// not call out.
func Notify(url string) error {
	_, err := http.Get(url)
	return err
}
