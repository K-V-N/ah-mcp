package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	appie "github.com/gwillem/appie-go"
)

var (
	clientMu     sync.RWMutex
	globalClient *appie.Client
)

// unscopeBonus strips the active-order headers from bonuspage requests.
//
// appie-go sends `appie-current-order-id` on every request once it knows an
// active order (GetOrder — i.e. any cart tool — and ReopenOrder set it; only
// RevertOrder clears it). For /bonuspage/ that header makes AH scope the
// response to that order: /v3/metadata then returns a single collapsed period
// around the order instead of the real bonus weeks, so resolveBonusWeek loses
// the current week's real end date and next week disappears entirely. Since
// this server is long-lived and shares one client, a single cart call used to
// blind every later bonus lookup ("no bonus week covers <next week>").
//
// Our bonus tools always resolve the week from an explicit date, so order
// scoping is never wanted here.
type unscopeBonus struct{ base http.RoundTripper }

func (t unscopeBonus) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "/bonuspage/") {
		req = req.Clone(req.Context())
		req.Header.Del("appie-current-order-id")
		req.Header.Del("appie-current-order-hash")
	}
	return t.base.RoundTrip(req)
}

// GetClient returns the singleton appie client, creating it from the tokens file
// if it has not been initialised yet.
func GetClient() (*appie.Client, error) {
	clientMu.RLock()
	c := globalClient
	clientMu.RUnlock()
	if c != nil {
		return c, nil
	}
	return ReloadClient()
}

// ReloadClient creates a fresh appie client loaded from the tokens file.
// Call this after a successful OAuth login to pick up newly saved tokens.
func ReloadClient() (*appie.Client, error) {
	path := TokensPath()
	hc := &http.Client{Transport: unscopeBonus{http.DefaultTransport}}
	c, err := appie.NewWithConfig(path, appie.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("create appie client: %w", err)
	}
	clientMu.Lock()
	globalClient = c
	clientMu.Unlock()
	return c, nil
}
