#!/usr/bin/env sh
# Sends the project through sonar-scanner running in Docker.
#
# Required environment:
#   SONAR_HOST_URL - SonarQube server, e.g. https://sonar.example.com
#   SONAR_TOKEN    - user token with "Execute Analysis" permission
set -eu

: "${SONAR_HOST_URL:?Set SONAR_HOST_URL to your SonarQube server URL}"
: "${SONAR_TOKEN:?Set SONAR_TOKEN to a sonar-scanner token}"

exec docker run --rm \
  -e SONAR_HOST_URL \
  -e SONAR_TOKEN \
  -e SONAR_SCANNER_OPTS="-Dsonar.projectBaseDir=/usr/src" \
  -v "$(pwd):/usr/src" \
  sonarsource/sonar-scanner-cli
