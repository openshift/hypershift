package registryclient

import (
	"net/url"
	"sync"

	"github.com/docker/distribution/registry/client/auth"
)

var NoCredentials auth.CredentialStore = &noopCredentialStore{}

type RefreshTokenStore interface {
	RefreshToken(url *url.URL, service string) string
	SetRefreshToken(url *url.URL, service string, token string)
}

func NewRefreshTokenStore() RefreshTokenStore {
	return &refreshTokenStore{}
}

type refreshTokenKey struct {
	url     string
	service string
}

type refreshTokenStore struct {
	lock  sync.Mutex
	store map[refreshTokenKey]string
}

func (s *refreshTokenStore) RefreshToken(url *url.URL, service string) string {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.store[refreshTokenKey{url: url.String(), service: service}]
}

func (s *refreshTokenStore) SetRefreshToken(url *url.URL, service, token string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.store == nil {
		s.store = make(map[refreshTokenKey]string)
	}
	s.store[refreshTokenKey{url: url.String(), service: service}] = token
}

type noopCredentialStore struct{}

func (*noopCredentialStore) Basic(url *url.URL) (string, string) {
	return "", ""
}

func (*noopCredentialStore) RefreshToken(url *url.URL, service string) string {
	return ""
}

func (*noopCredentialStore) SetRefreshToken(url *url.URL, service, token string) {
}

func NewBasicCredentials() *BasicCredentials {
	return &BasicCredentials{refreshTokenStore: &refreshTokenStore{}}
}

type basicForURL struct {
	url                url.URL
	username, password string
}

type BasicCredentials struct {
	creds []basicForURL
	*refreshTokenStore
}

func (c *BasicCredentials) Add(url *url.URL, username, password string) {
	c.creds = append(c.creds, basicForURL{*url, username, password})
}

func (c *BasicCredentials) Basic(url *url.URL) (string, string) {
	for _, cred := range c.creds {
		if cred.url.Host != "" && cred.url.Host != url.Host {
			continue
		}
		if cred.url.Path != "" && cred.url.Path != url.Path {
			continue
		}
		return cred.username, cred.password
	}
	return "", ""
}
