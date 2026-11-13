package main

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/oauth"
)

type api struct {
	config config
}

type config struct {
	secret   string
	addr     string
	wTimeout time.Duration
	rTimeout time.Duration
}

func (app *api) Mount() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("The API is functioning properly"))
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Welcome to your aerocrest daemon! Please set it up on the panel using the token and the server's public ip address"))
	})

	r.Route("/api", func(r chi.Router) {
		r.Use(oauth.Authorize("key", nil))
		r.Route("/servers", func(r chi.Router) {
			r.Get("/servers", func(writer http.ResponseWriter, request *http.Request) {
				writer.Write([]byte("Servers List"))
			})
		})
	})
	return r
}

func (app *api) Run(h http.Handler) error {
	srv := http.Server{
		Handler:      h,
		Addr:         app.config.addr,
		WriteTimeout: app.config.wTimeout,
		ReadTimeout:  app.config.rTimeout,
		IdleTimeout:  time.Minute,
	}
	log.Printf("API is running at: %s", app.config.addr)
	return srv.ListenAndServe()
}
