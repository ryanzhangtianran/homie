#!/bin/sh
# curl -fsSL https://raw.githubusercontent.com/ryanzhangtianran/homie/main/install.sh | sh
set -e
os=$(uname -s | tr A-Z a-z); arch=$(uname -m)
case $arch in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) echo "unsupported $arch"; exit 1;; esac
mkdir -p "$HOME/.local/bin"
curl -fsSL "https://github.com/${HOMIE_REPO:-ryanzhangtianran/homie}/releases/latest/download/homie_${os}_${arch}" -o "$HOME/.local/bin/homie"
chmod +x "$HOME/.local/bin/homie"
echo "installed $HOME/.local/bin/homie"
