package services

import (
	"context"
	"net/url"
)

// ConnectService builds the address of an OAuth consent screen. It reads a URL's
// query string, which is not a database: url.URL.Query takes no argument, and a
// statement's Query always takes the statement or follows a Prepare.
type ConnectService struct {
	authorize string
}

// AuthorizeURL adds the client id to the partner's authorization address.
func (s *ConnectService) AuthorizeURL(_ context.Context, clientID string) (string, error) {
	target, err := url.Parse(s.authorize)
	if err != nil {
		return "", err
	}
	q := target.Query()
	q.Set("client_id", clientID)
	target.RawQuery = q.Encode()
	return target.String(), nil
}
