package main

import (
	"net/http"

	"go.uber.org/fx"
)

func NewHTTPServer(lc fx.Lifecycle) *http.Server {
	srv := &http.Server{Addr: ":8080"}
	// TODO: attach lifecycle
	return srv
}
