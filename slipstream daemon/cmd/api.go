package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chyeh/pubip"
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
		publicIP, _ := pubip.Get()
		welcomeMsg := fmt.Sprintf("Welcome to your aerocrest daemon! Please set it up on the panel using the following information: \n Token: %s \n Public IP: %s \n Port: %s", app.config.secret, publicIP, strings.TrimPrefix(app.config.addr, ":"))
		w.Write([]byte(welcomeMsg))
	})
	dockerCli, dErr := docker.NewClientFromEnv()
	if dErr != nil {
		log.Printf("Failed to create docker client, err: %s", dErr)
	}
	r.Route("/api", func(r chi.Router) {
		r.Use(BearerKeyAuth(app.config.secret))
		r.Route("/servers", func(r chi.Router) {
			r.Get("/", func(writer http.ResponseWriter, request *http.Request) {
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
			r.Post("/", func(writer http.ResponseWriter, request *http.Request) {
				image := request.URL.Query().Get("image")
				containerName := request.URL.Query().Get("name")
				mem, err := strconv.Atoi(request.URL.Query().Get("ram"))
				mcVer := request.URL.Query().Get("version")
				if mcVer == "" {
					mcVer = "LATEST"
				}
				if err != nil {
					http.Error(writer, "ERROR: could not convert ram string to int: "+err.Error(), http.StatusBadRequest)
					return
				}
				cpuCount, err := strconv.ParseFloat(request.URL.Query().Get("cpu"), 64)
				if err != nil {
					http.Error(writer, "ERROR: could not convert cpu string to float: "+err.Error(), http.StatusBadRequest)
					return
				}
				iSplit := strings.Split(image, ":")
				err = dockerCli.PullImage(docker.PullImageOptions{
					Repository: iSplit[0],
					Tag:        iSplit[1],
				}, docker.AuthConfiguration{})
				if err != nil {
					http.Error(writer, "ERROR: could not pull image: "+err.Error(), http.StatusInternalServerError)
					return
				}
				container, err := dockerCli.CreateContainer(docker.CreateContainerOptions{
					Name: containerName,
					Config: &docker.Config{
						Image:        image,
						Env:          []string{"EULA=TRUE", "VERSION=" + mcVer, fmt.Sprintf("MEMORY=%dM", mem-256)},
						ExposedPorts: map[docker.Port]struct{}{"25565/tcp": {}},
					},
					HostConfig: &docker.HostConfig{
						Memory:    int64(mem) * 1024 * 1024,
						CPUPeriod: 100000,
						CPUQuota:  int64(math.Round(100000 * cpuCount)),
						PortBindings: map[docker.Port][]docker.PortBinding{
							"25565/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
						},
					},
				})
				if err != nil {
					http.Error(writer, "ERROR: could not create container: "+err.Error(), http.StatusInternalServerError)
					return
				}
				err = dockerCli.StartContainer(container.ID, nil)
				if err != nil {
					http.Error(writer, "ERROR: could not start container: "+err.Error(), http.StatusInternalServerError)
					return
				}
				info, err := dockerCli.InspectContainerWithOptions(docker.InspectContainerOptions{ID: container.ID})
				if err != nil {
					return
				}
				infoMsg := fmt.Sprintf("Docker container has been created and is running \n ID: %s \n PORT: %s", container.ID, info.NetworkSettings.Ports["25565/tcp"][0].HostPort)
				writer.Write([]byte(infoMsg))
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
