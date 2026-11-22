#!/bin/sh
# Builds build/tf-plan-cost.zip for the Lambda function (linux/arm64).
set -eu
cd "$(dirname "$0")"
root=$(cd ../.. && pwd)
rm -rf build && mkdir -p build/pkg
version=$(git -C "$root" describe --tags --always --dirty 2>/dev/null || echo dev)
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags="-s -w -X main.version=$version" -o deploy/aws/build/pkg/tf-plan-cost ./cmd/tf-plan-cost)
cp bootstrap build/pkg/bootstrap
chmod 755 build/pkg/bootstrap build/pkg/tf-plan-cost
(cd build/pkg && zip -q -X ../tf-plan-cost.zip bootstrap tf-plan-cost)
echo "built deploy/aws/build/tf-plan-cost.zip ($version)"
