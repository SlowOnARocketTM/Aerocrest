#!/bin/bash

CONFIG_DIR="/etc/slipstream"
CONFIG_FILE="$CONFIG_DIR/slipstream.cfg"

mkdir -p "$CONFIG_DIR"

read -r -p "Enter the port you want the service to run on: " port
if grep -q "^port=" /etc/slipstream/slipstream.cfg 2>/dev/null; then
    sudo sed -i "s/^port=.*/port=$port/" /etc/slipstream/slipstream.cfg
else
    printf "port=%s\n" "$port" | sudo tee -a /etc/slipstream/slipstream.cfg > /dev/null
fi
if grep -q "^isFirstRun=" /etc/slipstream/slipstream.cfg 2>/dev/null; then
    sudo sed -i "s/^isFirstRun=.*/isFirstRun=true/" /etc/slipstream/slipstream.cfg
else
    printf "isFirstRun=true\n" | sudo tee -a /etc/slipstream/slipstream.cfg > /dev/null
fi
# setup db ?

sudo apt update
sudo apt-get install -y docker.io docker-compose-v2
sudo systemctl enable --now docker

if [ -n "$SUDO_USER" ]; then
  sudo chown root:"$SUDO_USER" "$CONFIG_FILE"
fi
sudo chmod 640 "$CONFIG_FILE"

