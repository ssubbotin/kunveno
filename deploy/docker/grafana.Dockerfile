# syntax=docker/dockerfile:1
# Grafana with the kunveno data source and dashboard built in, so the compose stack needs no files
# next to it. Built in CI from the repository root:
#   docker build -f deploy/docker/grafana.Dockerfile .
FROM grafana/grafana:12.1.1
COPY deploy/docker/grafana/provisioning /etc/grafana/provisioning
COPY deploy/docker/grafana/dashboards /etc/grafana/dashboards
