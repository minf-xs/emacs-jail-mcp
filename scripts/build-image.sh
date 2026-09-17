#!/usr/bin/env bash
set -euo pipefail

IMAGE_NAME="${1:-emacs-jail:latest}"
CONTAINERFILE="${2:-Containerfile}"

echo "Building container image: ${IMAGE_NAME} using ${CONTAINERFILE}..."
podman build -t "${IMAGE_NAME}" -f "${CONTAINERFILE}" .
echo "Successfully built ${IMAGE_NAME}"
