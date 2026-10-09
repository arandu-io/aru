// Package gateway is a second tree inside app/Services: the payment engine,
// its ports and its adapters, found by nobody looking for the services.
package gateway

// Charge sends the amount to the gateway.
func Charge(amount int64) error { return nil }
