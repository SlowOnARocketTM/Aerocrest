package main

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/ini.v1"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	appDir := filepath.Join("/etc/slipstream/")
	_, err := os.Stat(filepath.Join(appDir, "slipstream.cfg"))
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("Please Run sudo ./setup.sh first")
		}
		logger.Error("Failed to load config", "error", err)
	}
	cfgFile, err := ini.Load(filepath.Join(appDir, "slipstream.cfg"))
	if err != nil {
		logger.Error("Failed to load config", "error", err)
	}
	port := cfgFile.Section("").Key("port").String()
	isFirstRun, _ := cfgFile.Section("").Key("isFirstRun").Bool()

	if isFirstRun == true {
		secureKey, kErr := generateSecretKey()
		if kErr != nil {
			logger.Error("could not generate a secret key", "error", kErr)
		}
		cfgFile.Section("").Key("secret").SetValue(secureKey)
		cfgFile.Section("").Key("isFirstRun").SetValue("false")
		cerr := cfgFile.SaveTo(filepath.Join(appDir, "slipstream.cfg"))
		if cerr != nil {
			logger.Error("could not save secret key", "error", cerr)
		}
	}

	logger.Info("Current Secret Key", "key", cfgFile.Section("").Key("secret").String())
	cfg := config{
		secret:   cfgFile.Section("").Key("secret").String(),
		addr:     ":" + port,
		wTimeout: 120 * time.Second,
		rTimeout: 60 * time.Second,
	}

	app := api{config: cfg}
	handler := app.Mount()
	err = app.Run(handler)
	if err != nil {
		logger.Error("Server failed to start", "error", err)
		os.Exit(1)
	}
}

func generateSecretKey() (string, error) {
	bytes := make([]byte, 32)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
