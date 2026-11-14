package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chyeh/pubip"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
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

	r.Use(middleware.Timeout(90 * time.Second))

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
						Tty:          true,
						OpenStdin:    true,
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
					http.Error(writer, err.Error(), http.StatusInternalServerError)
					return
				}
				InfoMsg := fmt.Sprintf("Docker container has been created and is running \n ID: %s \n PORT: %s", container.ID, info.NetworkSettings.Ports["25565/tcp"][0].HostPort)
				appDir := filepath.Join("/etc/slipstream/")
				byteData, err := json.MarshalIndent(info.HostConfig, "", "  ")
				if err != nil {
					http.Error(writer, err.Error(), http.StatusInternalServerError)
					return
				}
				err = os.WriteFile(filepath.Join(appDir, info.ID+".json"), byteData, 0600)
				if err != nil {
					http.Error(writer, err.Error(), http.StatusInternalServerError)
					return
				}

				writer.Write([]byte(InfoMsg))
			})
			r.Route("/{uuid}", func(r chi.Router) {
				r.Route("/power", func(r chi.Router) {
					r.Get("/", func(writer http.ResponseWriter, request *http.Request) {
						instruction := request.URL.Query().Get("instruction")
						containerId := chi.URLParam(request, "uuid")
						switch instruction {
						case "stop":
							err := dockerCli.StopContainer(containerId, 60)
							if err != nil {
								http.Error(writer, err.Error(), http.StatusInternalServerError)
								return
							}
							writer.Write([]byte("successfully stopped the server"))
						case "restart":
							err := dockerCli.StopContainer(containerId, 60)
							if err != nil {
								http.Error(writer, err.Error(), http.StatusInternalServerError)
								return
							}
							appDir := filepath.Join("/etc/slipstream/", containerId+".json")
							file, err := os.Open(appDir)
							if err != nil {
								http.Error(writer, err.Error(), http.StatusInternalServerError)
								return
							}
							var hostCfg docker.HostConfig
							err = json.NewDecoder(file).Decode(&hostCfg)
							if err != nil {
								http.Error(writer, err.Error(), http.StatusInternalServerError)
								return
							}
							err = dockerCli.StartContainer(containerId, &hostCfg)
							if err != nil {
								http.Error(writer, err.Error(), http.StatusInternalServerError)
								return
							}
							writer.Write([]byte("successfully restarted the server"))
						case "start":
							appDir := filepath.Join("/etc/slipstream/", containerId+".json")
							file, err := os.Open(appDir)
							if err != nil {
								http.Error(writer, err.Error(), http.StatusInternalServerError)
								return
							}
							var hostCfg docker.HostConfig
							err = json.NewDecoder(file).Decode(&hostCfg)
							if err != nil {
								http.Error(writer, err.Error(), http.StatusInternalServerError)
								return
							}
							err = dockerCli.StartContainer(containerId, &hostCfg)
							if err != nil {
								http.Error(writer, err.Error(), http.StatusInternalServerError)
								return
							}
							writer.Write([]byte("successfully started the server"))

							// add kill too
						}

					})
				})
				r.Delete("/", func(writer http.ResponseWriter, request *http.Request) {
					containerId := chi.URLParam(request, "uuid")
					err := dockerCli.RemoveContainer(docker.RemoveContainerOptions{
						ID:            containerId,
						RemoveVolumes: true,
					})
					if err != nil {
						http.Error(writer, err.Error(), http.StatusInternalServerError)
						return
					}
					appDir := filepath.Join("/etc/slipstream/", containerId+".json")
					err = os.Remove(appDir)
					if err != nil {
						http.Error(writer, err.Error(), http.StatusInternalServerError)
						return
					}
					writer.Write([]byte("successfully delete server id: " + containerId))
				})
				r.Post("/command", func(writer http.ResponseWriter, request *http.Request) {
					command := request.URL.Query().Get("command")
					containerId := chi.URLParam(request, "uuid")
					cmd := append([]string{"rcon-cli"}, command)
					exec, err := dockerCli.CreateExec(docker.CreateExecOptions{
						Cmd:          cmd,
						Container:    containerId,
						AttachStdout: true,
						AttachStderr: true,
					})
					if err != nil {
						http.Error(writer, err.Error(), http.StatusInternalServerError)
						return
					}
					var stdout, stderr bytes.Buffer
					err = dockerCli.StartExec(exec.ID, docker.StartExecOptions{
						OutputStream: &stdout,
						ErrorStream:  &stderr,
					})
					if err != nil {
						http.Error(writer, err.Error(), http.StatusInternalServerError)
						return
					}
					writer.Header().Set("Content-Type", "application/json")
					err = json.NewEncoder(writer).Encode(map[string]string{
						"stdout": stdout.String(),
						"stderr": stderr.String(),
					})
					if err != nil {
						http.Error(writer, err.Error(), http.StatusInternalServerError)
						return
					}
				})
				r.Get("/ws", handleWs(dockerCli))
			})
		})
	})
	return r
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func handleWs(dockerCli *docker.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		containerId := chi.URLParam(r, "uuid")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
			return
		}
		defer conn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			for {
				_, _, err := conn.ReadMessage()
				if err != nil {
					conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
					cancel()
					return
				}
			}
		}()

		pR, pW := io.Pipe()
		go func() {
			defer pW.Close()
			err := dockerCli.Logs(docker.LogsOptions{
				Context:      ctx,
				Container:    containerId,
				OutputStream: pW,
				ErrorStream:  pW,
				Tail:         "100",
				Follow:       true,
				Stdout:       true,
				Stderr:       true,
				RawTerminal:  true,
			})
			if err != nil {
				conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
				return
			}
		}()

		buf := make([]byte, 4096)
		for {
			n, err := pR.Read(buf)
			if n > 0 {
				err := conn.WriteMessage(websocket.TextMessage, buf[:n])
				if err != nil {
					conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
					return
				}
			}
			if err != nil {
				conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
				return
			}
		}
	}

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
