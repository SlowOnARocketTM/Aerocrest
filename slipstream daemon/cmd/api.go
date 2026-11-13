package main

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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

func BearerKeyAuth(secretKey string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			token, found := strings.CutPrefix(authHeader, "Bearer ")
			if !found || token == "" {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(secretKey)) != 1 {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
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
		r.Use(BearerKeyAuth(app.config.secret))
		r.Route("/servers", func(r chi.Router) {
			r.Get("/", func(writer http.ResponseWriter, request *http.Request) {
				dockerCli, dErr := docker.NewClientFromEnv()
				if dErr != nil {
					log.Printf("Failed to create docker client, err: %s", dErr)
				}
				containers, dErr := dockerCli.ListContainers(docker.ListContainersOptions{All: true})
				if dErr != nil {
					log.Printf("Failed to list docker containers, err: %s", dErr)
				}
				marshal, dErr := json.MarshalIndent(containers, "", "  ")
				if dErr != nil {
					log.Printf("Failed to marshall the docker containers list, err: %s", dErr)
				}
				writer.Write(marshal)
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
